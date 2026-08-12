package plugin_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/authenticator"
	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

// readSource reads a file from this package for the source-reading assertion
// below. A property about where a value COMES FROM cannot be observed by
// calling the function, so it is read instead.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// funcBody returns the body of the function whose declaration line is decl.
//
// It fails loudly when the declaration is not found, rather than returning an
// empty string: a body that is empty because the function was renamed would
// satisfy every "does not contain" assertion built on it, which is the
// vacuous-pass shape these tests exist to refuse.
func funcBody(t *testing.T, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("declaration %q not found; it was probably renamed, and this check silently stopped "+
			"checking. Fix the lookup, do not delete the test.", decl)
	}
	rest := src[i+len(decl):]
	j := strings.Index(rest, "\n}")
	if j < 0 {
		t.Fatalf("could not find the end of %q", decl)
	}
	return rest[:j]
}

// TestAuthenticatorHostPluginMapCarriesTheNegotiation: the host-side entry
// point is the only place the negotiation inputs can be supplied, so it must
// carry them. A map built without them is a gate that is off by default.
func TestAuthenticatorHostPluginMapCarriesTheNegotiation(t *testing.T) {
	doc := manifest.Doc{Name: "idp", Implements: connector.ClassAuthenticator, Kind: manifest.KindProvider, MinHostVersion: "0.4.0"}
	m := plugin.AuthenticatorHostPluginMap("idp", doc, "0.5.0")

	p, ok := m[plugin.AuthenticatorName].(*plugin.AuthenticatorPlugin)
	if !ok {
		t.Fatalf("plugin under key %q is %T", plugin.AuthenticatorName, m[plugin.AuthenticatorName])
	}
	if p.Name != "idp" || p.HostVersion != "0.5.0" || p.ProviderManifest.Name != "idp" {
		t.Fatalf("negotiation inputs not carried: %+v", p)
	}
}

// TestAuthenticatorDispenseFailsClosed drives the three ways the negotiation
// can be un-runnable. Each is a REFUSAL rather than an assumption, because the
// failure it prevents — running a connector against a contract it has already
// said it cannot work with — is silent when it happens.
func TestAuthenticatorDispenseFailsClosed(t *testing.T) {
	good := manifest.Doc{Name: "idp", Implements: connector.ClassAuthenticator, Kind: manifest.KindProvider, MinHostVersion: "0.4.0"}

	for _, tc := range []struct {
		name string
		p    *plugin.AuthenticatorPlugin
	}{
		{"no host version", &plugin.AuthenticatorPlugin{Name: "idp", ProviderManifest: good}},
		{"no provider manifest", &plugin.AuthenticatorPlugin{Name: "idp", HostVersion: "0.5.0"}},
		{"host older than min_host_version", &plugin.AuthenticatorPlugin{
			Name: "idp", ProviderManifest: good, HostVersion: "0.3.0",
		}},
		{"unparseable host version", &plugin.AuthenticatorPlugin{
			Name: "idp", ProviderManifest: good, HostVersion: "0.5.0-rc1",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A nil ClientConn is safe here precisely because every case must
			// refuse BEFORE it dials anything. A case that reached the wire
			// would panic, which is a louder failure than a wrong assertion.
			_, err := tc.p.GRPCClient(context.Background(), nil, nil)
			if err == nil {
				t.Fatal("dispense succeeded; the negotiation must fail closed")
			}
			if !cerr.Classified(err) {
				t.Errorf("refusal is unclassified (%v); a host routes on the classification", err)
			}
		})
	}
}

// TestAuthenticatorImplementsIsNotDerivedFromCapabilities is the rule the
// package doc calls not stylistic.
//
// The conformance harness's declared-is-implemented checks are only worth
// anything because "declared" and "implemented" come from INDEPENDENT signals.
// A stub whose class or shape were computed from the Capabilities RPC would
// make that check agree with itself whatever the provider does — a bug this SDK
// has already shipped once, in the credential class's batch capability.
//
// The test reads the source rather than the behaviour, because the property is
// about where a value COMES FROM, which behaviour cannot show.
func TestAuthenticatorImplementsIsNotDerivedFromCapabilities(t *testing.T) {
	src := readSource(t, "authenticator.go")

	body := funcBody(t, src, "func (c *authenticatorGRPCClient) Implements() connector.Class {")
	if strings.Contains(body, "caps") || strings.Contains(body, "Capabilities") {
		t.Errorf("Implements() consults capabilities:\n%s\n\nit must be a compile-time constant of this "+
			"binding, or the declared-is-implemented check agrees with itself", body)
	}
	if !strings.Contains(body, "connector.ClassAuthenticator") {
		t.Errorf("Implements() does not return the class constant:\n%s", body)
	}
}

// TestAuthenticatorAssertionCannotCarryAToken pins the wire half of the
// contract's structural promise: passthrough is unrepresentable because there
// is nowhere to put a token, on either side of the boundary.
func TestAuthenticatorAssertionCannotCarryAToken(t *testing.T) {
	forbidden := []string{"token", "idtoken", "accesstoken", "refreshtoken", "claims", "claim", "secret", "jwt"}
	rt := reflect.TypeOf(connectorpb.Assertion{})
	for i := range rt.NumField() {
		name := strings.ToLower(rt.Field(i).Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Errorf("connectorpb.Assertion has field %q: adding one is a change to a published wire "+
					"contract, not an extension of it", rt.Field(i).Name)
			}
		}
	}
}

// TestAuthenticatorChallengeRoundTripsAndNilIsRefusable covers the marshalling
// in both directions, and the nil case that a provider sending an empty message
// produces.
func TestAuthenticatorChallengeRoundTrips(t *testing.T) {
	want := authenticator.Challenge{AuthorizationURL: "https://idp.example/authorize", Handle: "h-9"}
	if got := transport.ChallengeFromPB(transport.ChallengeToPB(want)); got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if got := transport.ChallengeFromPB(nil); got != (authenticator.Challenge{}) {
		t.Fatalf("nil challenge = %+v, want the zero value so the guard refuses it", got)
	}
	if _, err := authenticator.CheckChallenge("idp", transport.ChallengeFromPB(nil), nil); err == nil {
		t.Fatal("a nil challenge crossed the boundary and was accepted")
	}
}

// TestAuthenticatorAssertionRoundTripsIncludingActiveFalse: active=false is
// proto3's default and does not travel on the wire at all, so a verified-but-
// disabled principal is exactly the value a careless mapping loses.
func TestAuthenticatorAssertionRoundTrips(t *testing.T) {
	for _, want := range []authenticator.Assertion{
		{PrincipalID: "00u1x", Authenticated: true, Active: true},
		{PrincipalID: "00u1x", Authenticated: true, Active: false},
		{},
	} {
		if got := transport.AssertionFromPB(transport.AssertionToPB(want)); got != want {
			t.Errorf("round trip = %+v, want %+v", got, want)
		}
	}
}

// TestAuthenticatorIncoherentAssertionDoesNotCrossTheWire is the boundary half
// of the contract's central refusal: a provider's incoherent answer becomes a
// NAMED fault rather than a session started for nobody.
func TestAuthenticatorIncoherentAssertionIsRefusedAtTheBoundary(t *testing.T) {
	// Authenticated with no principal — a success that identifies no one.
	crossed := transport.AssertionFromPB(&connectorpb.Assertion{Authenticated: true})

	_, err := authenticator.CheckAssertion("idp", crossed, nil)
	var fe *authenticator.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *authenticator.FaultError", err)
	}
	if fe.Connector != "idp" {
		t.Errorf("fault names %q, want the dialled connector's name", fe.Connector)
	}
}
