package authenticator_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/authenticator"
	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// TestKnownCapabilitiesIsEmptyAtV1 pins the deliberate v1 decision. An empty
// vocabulary is not an oversight: a device-code path, a machine-principal path,
// a refresh path and a step-up path were each identified and NOT added, because
// a capability nothing gates on is a speculative feature flag.
//
// ⚠️ WHAT THIS TEST DOES NOT COVER, and cannot while the vocabulary is empty.
//
// The sibling classes all return their vocabulary as a COPY, so a caller cannot
// widen a closed set by appending to it, and each pins that with a test. Here
// that property is UNOBSERVABLE: appending to an empty slice allocates a new
// array whether or not the function copied, so a version returning the package
// variable directly passes any append-based check. Measured — a deliberate
// mutation to `return knownCapabilities` does not fail this file.
//
// So the copy is implemented and asserted by inspection rather than by test,
// and the check becomes real the moment a capability is added. Whoever adds the
// first one should add the append-based assertion in the same commit; it will
// bite from then on. Written down because a test that silently covers nothing
// is the shape this SDK's harnesses exist to refuse.
func TestKnownCapabilitiesIsEmptyAtV1(t *testing.T) {
	if got := authenticator.KnownCapabilities(); len(got) != 0 {
		t.Fatalf("KnownCapabilities() = %v, want empty at v1", got)
	}
}

// TestAssertionCoherent covers the single invariant, in all four shapes.
//
// Both incoherent shapes are the point. An authenticated assertion naming
// nobody is a success that identifies no one; a named principal that denies
// authentication is a name nothing verified. Neither is a smaller answer.
func TestAssertionCoherent(t *testing.T) {
	for _, tc := range []struct {
		name          string
		authenticated bool
		principalID   string
		want          bool
	}{
		{"verified principal", true, "00u1x", true},
		{"answered nobody", false, "", true},
		{"authenticated with no principal", true, "", false},
		{"principal named while denying authentication", false, "00u1x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := authenticator.Assertion{Authenticated: tc.authenticated, PrincipalID: tc.principalID}
			if got := a.Coherent(); got != tc.want {
				t.Fatalf("Coherent() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAssertionCarriesNoCredentialMaterial is a structural test, not a
// behavioural one, because the obligation is structural: the type must make
// passthrough UNREPRESENTABLE rather than merely discouraged.
//
// A connector that returned a token would be brokering a credential for a third
// party, which contract invariant 2 prohibits. The cheapest way to guarantee it
// never happens is for there to be nowhere to put one.
func TestAssertionCarriesNoCredentialMaterial(t *testing.T) {
	forbidden := []string{
		"token", "idtoken", "accesstoken", "refreshtoken", "claims", "claim",
		"raw", "secret", "credential", "code", "verifier", "nonce", "assertionjwt", "jwt",
	}
	rt := reflect.TypeOf(authenticator.Assertion{})
	for i := range rt.NumField() {
		name := strings.ToLower(rt.Field(i).Name)
		for _, bad := range forbidden {
			if name == bad {
				t.Errorf("Assertion has field %q: this type must carry no credential material and no raw "+
					"claim set, so that passthrough is unrepresentable rather than merely discouraged",
					rt.Field(i).Name)
			}
		}
	}
}

// TestChallengeCarriesNoCredentialMaterial holds the same line on the other
// side of the flow. The handle is an opaque reference the host passes back; the
// PKCE verifier it stands for stays in the connector's memory.
func TestChallengeCarriesNoCredentialMaterial(t *testing.T) {
	forbidden := []string{"verifier", "secret", "token", "code", "clientsecret"}
	rt := reflect.TypeOf(authenticator.Challenge{})
	for i := range rt.NumField() {
		name := strings.ToLower(rt.Field(i).Name)
		for _, bad := range forbidden {
			if name == bad {
				t.Errorf("Challenge has field %q: the handle is opaque, and the verifier it stands for never "+
					"leaves the connector", rt.Field(i).Name)
			}
		}
	}
}

// TestVerificationFailuresAreDistinguishable covers the rule that gives this
// class its teeth: a rejected assertion is a typed FAILURE, never a successful
// assertion carrying Authenticated false.
//
// The five reasons are distinguishable from each other — an operator needs to
// know which check rejected the assertion — while all classifying as
// Unauthorized, because that is the answer a HOST routes on and all five have
// the same routing answer: do not retry, do not trip the breaker, do not start
// the session.
func TestVerificationFailuresAreDistinguishable(t *testing.T) {
	all := authenticator.VerificationFailures()
	if len(all) < 5 {
		t.Fatalf("VerificationFailures() = %v, want at least the five the contract names", all)
	}

	seen := map[authenticator.VerificationFailure]bool{}
	for _, f := range all {
		if seen[f] {
			t.Fatalf("VerificationFailures() repeats %q; the reasons must be distinguishable", f)
		}
		seen[f] = true

		err := &authenticator.VerificationError{Connector: "idp", Failure: f}
		if got := cerr.KindOf(err); got != cerr.KindUnauthorized {
			t.Errorf("failure %q classifies as %v, want KindUnauthorized — every verification failure has the "+
				"same routing answer even though operators need to tell them apart", f, got)
		}
		if !strings.Contains(err.Error(), string(f)) {
			t.Errorf("failure %q is not named in its own message %q", f, err.Error())
		}
	}

	for _, want := range []authenticator.VerificationFailure{
		authenticator.VerificationBadSignature,
		authenticator.VerificationWrongIssuer,
		authenticator.VerificationWrongAudience,
		authenticator.VerificationReplayedNonce,
		authenticator.VerificationExpired,
	} {
		if !seen[want] {
			t.Errorf("VerificationFailures() omits %q", want)
		}
	}
}

// TestVerificationErrorIsNotRetryableAndDoesNotTripTheBreaker pins the routing
// half. A broken credential does not improve on retry, and it is not backend
// load.
func TestVerificationErrorIsNotRetryableAndDoesNotTripTheBreaker(t *testing.T) {
	err := &authenticator.VerificationError{Connector: "idp", Failure: authenticator.VerificationExpired}
	if cerr.Retryable(err) {
		t.Error("a verification failure reports as retryable; retrying a rejected assertion rejects it again")
	}
	if cerr.TripsBreaker(err) {
		t.Error("a verification failure trips the circuit breaker; a rejected credential is not backend load")
	}
}

// TestCheckAssertionRefusesAnIncoherentReturn is the host-side guard. A
// connector's own typed failure passes through unchanged — failing correctly is
// not a fault — while a return the contract does not admit becomes a named
// contract violation.
func TestCheckAssertionRefusesAnIncoherentReturn(t *testing.T) {
	t.Run("incoherent is a named fault", func(t *testing.T) {
		_, err := authenticator.CheckAssertion("okta", authenticator.Assertion{Authenticated: true}, nil)
		if err == nil {
			t.Fatal("an authenticated assertion naming no principal was accepted")
		}
		var fe *authenticator.FaultError
		if !errors.As(err, &fe) {
			t.Fatalf("error is %T, want *authenticator.FaultError", err)
		}
		if fe.Connector != "okta" {
			t.Errorf("fault names connector %q, want %q — attribution is what the guard adds", fe.Connector, "okta")
		}
		if cerr.KindOf(err) != cerr.KindContractViolation {
			t.Errorf("fault classifies as %v, want KindContractViolation", cerr.KindOf(err))
		}
	})

	t.Run("a connector's own failure passes through", func(t *testing.T) {
		own := cerr.New(cerr.KindUnavailable, "Complete", errors.New("idp unreachable"))
		_, err := authenticator.CheckAssertion("okta", authenticator.Assertion{}, own)
		if cerr.KindOf(err) != cerr.KindUnavailable {
			t.Fatalf("a correct failure was reclassified to %v; failing correctly is not a fault", cerr.KindOf(err))
		}
		var fe *authenticator.FaultError
		if errors.As(err, &fe) {
			t.Fatal("a correct failure was turned into a contract fault")
		}
	})

	t.Run("a coherent assertion passes through unchanged", func(t *testing.T) {
		want := authenticator.Assertion{Authenticated: true, PrincipalID: "00u1x", Active: true}
		got, err := authenticator.CheckAssertion("okta", want, nil)
		if err != nil {
			t.Fatalf("a conformant assertion was refused: %v", err)
		}
		if got != want {
			t.Fatalf("assertion was modified in flight: %+v", got)
		}
	})
}

// TestCheckAssertionRefusesAnAssertionBesideAnError: a call either answers or
// fails. Returning both leaves a caller that checked only one of them acting on
// the other.
func TestCheckAssertionRefusesAnAssertionBesideAnError(t *testing.T) {
	verFail := &authenticator.VerificationError{Failure: authenticator.VerificationBadSignature}
	claimed := authenticator.Assertion{Authenticated: true, PrincipalID: "00u1x"}

	_, err := authenticator.CheckAssertion("okta", claimed, verFail)

	var fe *authenticator.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *authenticator.FaultError: an assertion returned beside an error is a "+
			"contract violation", err)
	}
	if fe.Fault != authenticator.FaultAssertionBesideError {
		t.Errorf("fault = %q, want %q", fe.Fault, authenticator.FaultAssertionBesideError)
	}
}

// TestCheckAssertionPassesTheCorrectRejectionShapeThrough pins the shape a
// connector SHOULD return when verification fails — the zero assertion and the
// typed rejection — and that the guard attributes it rather than reclassifying
// it.
func TestCheckAssertionPassesTheCorrectRejectionShapeThrough(t *testing.T) {
	verFail := &authenticator.VerificationError{Failure: authenticator.VerificationReplayedNonce}
	got, err := authenticator.CheckAssertion("okta", authenticator.Assertion{}, verFail)

	if got != (authenticator.Assertion{}) {
		t.Fatalf("an assertion was invented for a rejected exchange: %+v", got)
	}
	var ve *authenticator.VerificationError
	if !errors.As(err, &ve) {
		t.Fatalf("error is %T, want the connector's own *VerificationError unchanged", err)
	}
	if ve.Connector != "okta" {
		t.Errorf("rejection names connector %q, want it attributed to %q", ve.Connector, "okta")
	}
	if cerr.KindOf(err) != cerr.KindUnauthorized {
		t.Errorf("rejection classifies as %v, want KindUnauthorized", cerr.KindOf(err))
	}
}

// TestTheUndetectableShapeIsDocumentedRatherThanClaimed exists to stop a future
// reader believing the guard catches more than it does.
//
// A connector whose verification failed and which returns an anonymous
// assertion with a NIL error is, from outside, IDENTICAL to one reporting a
// genuine anonymous answer. Both are the zero Assertion and no error. No
// host-side guard can separate them — the difference is entirely inside the
// connector — so this test asserts the guard accepts it, and the obligation
// rests on the conformance harness driving a stub built to violate it.
//
// Removing this test would not change behaviour. It would remove the record
// that the acceptance below is DELIBERATE rather than an oversight.
func TestTheUndetectableShapeIsDocumentedRatherThanClaimed(t *testing.T) {
	got, err := authenticator.CheckAssertion("okta", authenticator.Assertion{}, nil)
	if err != nil {
		t.Fatalf("the guard refused a genuine anonymous answer: %v", err)
	}
	if got != (authenticator.Assertion{}) {
		t.Fatalf("assertion modified: %+v", got)
	}
	if !slices.Contains(authenticator.Faults(), authenticator.FaultAssertionBesideError) {
		t.Error("FaultAssertionBesideError is not in the closed fault set, so the detectable half is unnamed")
	}
}

// TestActiveFalseWithAuthenticatedTrueIsARealAnswer: a verified principal who
// is disabled is a real, permitted answer — reported as-is, never as an error
// and never defaulted. The host decides what to do with it.
func TestActiveFalseWithAuthenticatedTrueIsARealAnswer(t *testing.T) {
	a := authenticator.Assertion{Authenticated: true, PrincipalID: "00u1x", Active: false}
	if !a.Coherent() {
		t.Fatal("a verified but disabled principal is incoherent; it is a real answer")
	}
	got, err := authenticator.CheckAssertion("okta", a, nil)
	if err != nil {
		t.Fatalf("the guard refused a verified-but-disabled principal: %v", err)
	}
	if got.Active {
		t.Fatal("Active was defaulted to true")
	}
}

// TestFaultsAreDistinctAndNamed keeps the fault vocabulary closed and readable:
// a host acts on the name, so two faults sharing one string are one fault.
func TestFaultsAreDistinctAndNamed(t *testing.T) {
	seen := map[authenticator.Fault]bool{}
	for _, f := range authenticator.Faults() {
		if f == "" {
			t.Error("an unnamed fault: a host acts on the name")
		}
		if seen[f] {
			t.Errorf("fault %q is declared twice", f)
		}
		seen[f] = true
	}
	if len(seen) == 0 {
		t.Fatal("Faults() is empty; every assertion built on it would pass by observing nothing")
	}
}
