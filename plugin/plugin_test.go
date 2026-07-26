package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credconform"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// grpcClient (the host-side stub dispensed by the go-plugin GRPCClient) must
// satisfy credential.CredentialLoader.
var _ credential.CredentialLoader = (*grpcClient)(nil)

const knownRef = "op://v/i/f"
const knownSecret = "s3cret"

// memLoader is a tiny in-test CredentialLoader served across the in-process
// gRPC round-trip: one ref resolves to a known secret, everything else is
// KindNotFound.
type memLoader struct {
	vals map[string]string // ref.String() -> value
}

func (m *memLoader) Resolve(_ context.Context, r ref.Ref) (credential.Resolution, error) {
	v, ok := m.vals[r.String()]
	if !ok {
		return credential.Resolution{}, cerr.New(cerr.KindNotFound, "Resolve", nil)
	}
	if v == "" {
		// This store distinguishes a stored-empty value from a failed read,
		// so it may say so. A store that cannot make that distinction must
		// not call ResolvedEmpty.
		return credential.ResolvedEmpty(), nil
	}
	return credential.Resolved(credential.NewMaterial([]byte(v)))
}

func (m *memLoader) List(_ context.Context, _ string) ([]ref.Ref, error) {
	refs := make([]ref.Ref, 0, len(m.vals))
	for k := range m.vals {
		r, err := ref.Parse(k)
		if err != nil {
			continue
		}
		refs = append(refs, r)
	}
	return refs, nil
}

func (m *memLoader) Lease(ctx context.Context, r ref.Ref) (credential.Resolution, credential.Lease, error) {
	res, err := m.Resolve(ctx, r)
	if err != nil {
		return credential.Resolution{}, credential.Lease{}, err
	}
	return res, credential.Lease{ID: "lease-1", Renewable: true}, nil
}

func (m *memLoader) Renew(_ context.Context, l credential.Lease) (credential.Lease, error) {
	return l, nil
}

func (m *memLoader) Revoke(_ context.Context, _ credential.Lease) error { return nil }

func (m *memLoader) Implements() connector.Class { return connector.ClassCredentialLoader }

func (m *memLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead}
}

func (m *memLoader) Health(_ context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}

func (m *memLoader) Close() error { return nil }

// newTestClient stands up an in-process go-plugin gRPC server/client pair
// (no subprocess) serving impl, dispenses the CredentialLoader plugin, and
// returns it already type-asserted to credential.CredentialLoader.
func newTestClient(t *testing.T, impl credential.CredentialLoader) credential.CredentialLoader {
	t.Helper()

	// The server return value is intentionally unused: client.Close() below
	// tears the server down for us (see the Cleanup comment).
	client, _ := goplugin.TestPluginGRPCConn(t, false, PluginMap(impl))
	t.Cleanup(func() {
		// client.Close() sends the go-plugin controller Shutdown RPC, whose
		// server-side handler calls GRPCServer.Stop() itself (see
		// grpc_controller.go in hashicorp/go-plugin). Also calling
		// server.Stop() here from the test goroutine races that handler
		// goroutine on GRPCServer's internal broker field -- observed under
		// `go test -race` in ~2/3 of runs. client.Close() alone is
		// sufficient to tear the in-process server down; do not add a
		// second, redundant Stop() call.
		_ = client.Close()
	})

	raw, err := client.Dispense(CredentialLoaderName)
	if err != nil {
		t.Fatalf("Dispense(%q): %v", CredentialLoaderName, err)
	}
	c, ok := raw.(credential.CredentialLoader)
	if !ok {
		t.Fatalf("dispensed value %T does not implement credential.CredentialLoader", raw)
	}
	return c
}

func TestResolveRoundTrip(t *testing.T) {
	impl := &memLoader{vals: map[string]string{knownRef: knownSecret}}
	c := newTestClient(t, impl)

	r, err := ref.Parse(knownRef)
	if err != nil {
		t.Fatalf("ref.Parse: %v", err)
	}

	res, err := c.Resolve(context.Background(), r)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	mat, err := res.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if got := string(mat.Reveal()); got != knownSecret {
		t.Fatalf("Reveal() = %q, want %q", got, knownSecret)
	}
}

func TestResolveMaterialRedactedOnFormat(t *testing.T) {
	impl := &memLoader{vals: map[string]string{knownRef: knownSecret}}
	c := newTestClient(t, impl)

	r, err := ref.Parse(knownRef)
	if err != nil {
		t.Fatalf("ref.Parse: %v", err)
	}

	res, err := c.Resolve(context.Background(), r)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	mat, err := res.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}

	formatted := fmt.Sprintf("%v %v", res, mat)
	if strings.Contains(formatted, knownSecret) {
		t.Fatalf("formatted material leaked secret: %q", formatted)
	}
}

func TestResolveNotFound(t *testing.T) {
	impl := &memLoader{vals: map[string]string{knownRef: knownSecret}}
	c := newTestClient(t, impl)

	r, err := ref.Parse("op://v/missing/f")
	if err != nil {
		t.Fatalf("ref.Parse: %v", err)
	}

	_, err = c.Resolve(context.Background(), r)
	if err == nil {
		t.Fatal("Resolve(missing ref): expected error, got nil")
	}
	if cerr.KindOf(err) != cerr.KindNotFound {
		t.Fatalf("KindOf = %v, want KindNotFound", cerr.KindOf(err))
	}
}

func TestCapabilitiesRoundTrip(t *testing.T) {
	impl := &memLoader{vals: map[string]string{knownRef: knownSecret}}
	c := newTestClient(t, impl)

	caps := c.Capabilities()
	if !caps.Has(credential.CapRead) {
		t.Fatalf("Capabilities() = %v, want to include CapRead", caps)
	}
}

func TestHealthRoundTrip(t *testing.T) {
	impl := &memLoader{vals: map[string]string{knownRef: knownSecret}}
	c := newTestClient(t, impl)

	h, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.Status != connector.Healthy {
		t.Fatalf("Health().Status = %v, want Healthy", h.Status)
	}
}

func TestImplementsIsCredentialLoader(t *testing.T) {
	impl := &memLoader{vals: map[string]string{knownRef: knownSecret}}
	c := newTestClient(t, impl)

	if got := c.Implements(); got != connector.ClassCredentialLoader {
		t.Fatalf("Implements() = %v, want %v", got, connector.ClassCredentialLoader)
	}
}

func TestLeaseRenewRevokeRoundTrip(t *testing.T) {
	impl := &memLoader{vals: map[string]string{knownRef: knownSecret}}
	c := newTestClient(t, impl)

	r, err := ref.Parse(knownRef)
	if err != nil {
		t.Fatalf("ref.Parse: %v", err)
	}

	res, lease, err := c.Lease(context.Background(), r)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	mat, err := res.Value()
	if err != nil {
		t.Fatalf("Lease Value: %v", err)
	}
	if got := string(mat.Reveal()); got != knownSecret {
		t.Fatalf("Lease material = %q, want %q", got, knownSecret)
	}
	if lease.ID != "lease-1" {
		t.Fatalf("Lease.ID = %q, want %q", lease.ID, "lease-1")
	}

	renewed, err := c.Renew(context.Background(), lease)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if renewed.ID != lease.ID {
		t.Fatalf("Renew().ID = %q, want %q", renewed.ID, lease.ID)
	}

	if err := c.Revoke(context.Background(), renewed); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
}

// brokenLoader is a deliberately NON-COMPLIANT provider: it reports success
// while resolving nothing — the shape a signed-out backend produces when it
// prints no output and exits 0. It exists to prove the wire binding does not
// launder that into an empty credential on the host side.
type brokenLoader struct{ memLoader }

func (b *brokenLoader) Resolve(context.Context, ref.Ref) (credential.Resolution, error) {
	return credential.Resolution{}, nil
}

// TestUnresolvedFromAProviderReachesTheHostAsAFault: the provider is
// out-of-process, so the host cannot inspect its code — the guarantee has to
// survive serialization. It does, because presence is carried by the message
// and the host-side stub checks it.
func TestUnresolvedFromAProviderReachesTheHostAsAFault(t *testing.T) {
	c := newTestClient(t, &brokenLoader{memLoader{vals: map[string]string{knownRef: knownSecret}}})

	r, err := ref.Parse(knownRef)
	if err != nil {
		t.Fatalf("ref.Parse: %v", err)
	}

	res, err := c.Resolve(context.Background(), r)
	if err == nil {
		t.Fatalf("a provider that resolved nothing must not look successful across the wire")
	}
	var fe *credential.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T (%v), want *credential.FaultError", err, err)
	}
	if fe.Fault != credential.FaultUnresolved {
		t.Fatalf("Fault = %q, want %q", fe.Fault, credential.FaultUnresolved)
	}
	if _, err := res.Value(); err == nil {
		t.Fatalf("the returned Resolution must not be readable")
	}
}

// emptySecretLoader holds a secret whose value really is empty, and says so.
type emptySecretLoader struct{ memLoader }

func (e *emptySecretLoader) Resolve(context.Context, ref.Ref) (credential.Resolution, error) {
	return credential.ResolvedEmpty(), nil
}

// TestDeliberatelyEmptySurvivesTheWire is the other half: an intentionally
// empty value must NOT be flattened into "unresolved" by the wire, or the
// contract's distinction would exist only in-process.
func TestDeliberatelyEmptySurvivesTheWire(t *testing.T) {
	c := newTestClient(t, &emptySecretLoader{memLoader{vals: map[string]string{knownRef: ""}}})

	r, err := ref.Parse(knownRef)
	if err != nil {
		t.Fatalf("ref.Parse: %v", err)
	}

	res, err := c.Resolve(context.Background(), r)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	mat, err := res.Value()
	if err != nil {
		t.Fatalf("a deliberately-empty value must stay readable across the wire: %v", err)
	}
	if len(mat.Reveal()) != 0 {
		t.Fatalf("material = %q, want empty", mat.Reveal())
	}
}

// ---------------------------------------------------------------------------
// The provider that is not a Go SDK user.
//
// Everything above serves a credential.CredentialLoader through this module's
// own marshalling, which is the easy case: the sender is conformant by
// construction. The tests below register a RAW connectorpb server instead —
// no transport.ResolutionToPB, no credential package — and put the real
// dispensed host stub in front of it. That is the actual threat model the
// plugin package's doc names: someone else's binary, possibly not even Go,
// emitting whatever its author thought the proto meant.
// ---------------------------------------------------------------------------

// rawProvider answers Resolve/Lease/ResolveBatch with literal wire messages,
// so a test can send an encoding no conformant Go sender would produce.
type rawProvider struct {
	connectorpb.UnimplementedCredentialLoaderServer
	material *connectorpb.Material
	caps     []string
	batch    []*connectorpb.ResolveBatchResult
}

func (p *rawProvider) Resolve(context.Context, *connectorpb.ResolveRequest) (*connectorpb.ResolveResponse, error) {
	return &connectorpb.ResolveResponse{Material: p.material}, nil
}

func (p *rawProvider) Lease(context.Context, *connectorpb.LeaseRequest) (*connectorpb.LeaseResponse, error) {
	return &connectorpb.LeaseResponse{Material: p.material, Lease: &connectorpb.Lease{Id: "lease-1"}}, nil
}

func (p *rawProvider) Capabilities(context.Context, *connectorpb.CapabilitiesRequest) (*connectorpb.CapabilitiesResponse, error) {
	return &connectorpb.CapabilitiesResponse{Capabilities: p.caps}, nil
}

func (p *rawProvider) ResolveBatch(context.Context, *connectorpb.ResolveBatchRequest) (*connectorpb.ResolveBatchResponse, error) {
	return &connectorpb.ResolveBatchResponse{Results: p.batch}, nil
}

// dialRaw serves p over a real gRPC connection and returns the host stub
// CredentialLoaderPlugin dispenses for it — the same value a host gets from
// Dispense, not a stand-in.
func dialRaw(t *testing.T, p *rawProvider, name string) credential.CredentialLoader {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	connectorpb.RegisterCredentialLoaderServer(srv, p)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	plug := &CredentialLoaderPlugin{Name: name}
	raw, err := plug.GRPCClient(context.Background(), nil, conn)
	if err != nil {
		t.Fatalf("GRPCClient: %v", err)
	}
	c, ok := raw.(credential.CredentialLoader)
	if !ok {
		t.Fatalf("dispensed %T, which does not implement credential.CredentialLoader", raw)
	}
	return c
}

// TestWireEmptinessMustBeAssertedNotInferred is the process boundary's half of
// the whole contract, and it is the case a presence-only wire could not
// express.
//
// proto3 does not put a zero-length bytes field on the wire, so a conformant
// ResolvedEmpty and a provider that resolved NOTHING and sent a
// default-constructed Material are byte-identical. A host reading
// "present message, no bytes" as a deliberately-empty value therefore hands
// out a readable, empty credential for a read that produced nothing — the
// exact bug this contract exists to prevent, reopened at the one boundary
// where the host cannot inspect the sender's code.
//
// The fix inverts which encoding is dangerous: emptiness must be ASSERTED
// with empty_by_assertion, so the encoding a confused author reaches for by
// accident means "unresolved", which is safe, and the hazardous meaning
// requires deliberate opt-in.
func TestWireEmptinessMustBeAssertedNotInferred(t *testing.T) {
	r, err := ref.Parse(knownRef)
	if err != nil {
		t.Fatalf("ref.Parse: %v", err)
	}

	t.Run("refused: no material message at all", func(t *testing.T) {
		assertWireFaultNamed(t, dialRaw(t, &rawProvider{material: nil}, "rogue-provider"), r)
	})

	t.Run("refused: a default-constructed Material", func(t *testing.T) {
		// What a foreign author writes for "I got nothing".
		assertWireFaultNamed(t, dialRaw(t, &rawProvider{material: &connectorpb.Material{}}, "rogue-provider"), r)
	})

	t.Run("refused: an explicitly zero-length value, unasserted", func(t *testing.T) {
		assertWireFaultNamed(t, dialRaw(t, &rawProvider{material: &connectorpb.Material{Value: []byte{}}}, "rogue-provider"), r)
	})

	t.Run("honoured: emptiness asserted", func(t *testing.T) {
		c := dialRaw(t, &rawProvider{material: &connectorpb.Material{EmptyByAssertion: true}}, "honest-provider")
		res, err := c.Resolve(context.Background(), r)
		if err != nil {
			t.Fatalf("an asserted empty secret must cross the wire: %v", err)
		}
		mat, err := res.Value()
		if err != nil {
			t.Fatalf("an asserted empty secret must stay readable: %v", err)
		}
		if len(mat.Reveal()) != 0 {
			t.Fatalf("material = %q, want empty", mat.Reveal())
		}
	})

	t.Run("bytes win over the assertion", func(t *testing.T) {
		// A contradictory message is not a third state: bytes present always
		// mean a resolved value, as the proto says.
		c := dialRaw(t, &rawProvider{material: &connectorpb.Material{
			Value: []byte(knownSecret), EmptyByAssertion: true,
		}}, "confused-provider")
		res, err := c.Resolve(context.Background(), r)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		mat, err := res.Value()
		if err != nil {
			t.Fatalf("Value: %v", err)
		}
		if string(mat.Reveal()) != knownSecret {
			t.Fatalf("Reveal() = %q, want %q", mat.Reveal(), knownSecret)
		}
	})

	t.Run("Lease is guarded the same way", func(t *testing.T) {
		c := dialRaw(t, &rawProvider{material: &connectorpb.Material{}}, "rogue-provider")
		res, _, err := c.Lease(context.Background(), r)
		if err == nil {
			t.Fatalf("Lease admitted an unasserted empty material")
		}
		assertNamedFault(t, err, "rogue-provider")
		if _, verr := res.Value(); verr == nil {
			t.Fatalf("the returned Resolution must not be readable")
		}
	})
}

// assertWireFaultNamed requires Resolve against c to be refused as a named
// contract fault, and the returned Resolution to be unreadable.
func assertWireFaultNamed(t *testing.T, c credential.CredentialLoader, r ref.Ref) {
	t.Helper()
	res, err := c.Resolve(context.Background(), r)
	if err == nil {
		mat, verr := res.Value()
		if verr == nil {
			t.Fatalf("a provider that resolved nothing produced a READABLE credential of %d bytes", len(mat.Reveal()))
		}
		t.Fatalf("the host accepted a provider that resolved nothing; the value is unreadable but no fault was raised")
	}
	assertNamedFault(t, err, "rogue-provider")
	if _, verr := res.Value(); verr == nil {
		t.Fatalf("the returned Resolution must not be readable")
	}
}

// assertNamedFault requires err to be a FaultUnresolved naming the connector.
// Refusing the value is not enough on its own: without the name, an operator
// is told a credential is broken and not which connector broke it.
func assertNamedFault(t *testing.T, err error, name string) {
	t.Helper()
	var fe *credential.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T (%v), want *credential.FaultError", err, err)
	}
	if fe.Fault != credential.FaultUnresolved {
		t.Fatalf("Fault = %q, want %q", fe.Fault, credential.FaultUnresolved)
	}
	if fe.Connector != name {
		t.Fatalf("Connector = %q, want %q — a fault nobody can attribute is half a report", fe.Connector, name)
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
}

// ---------------------------------------------------------------------------
// The batch RPC.
//
// CapBatchResolve used to be a capability a kind: provider connector could
// declare and STRUCTURALLY COULD NOT SATISFY: there was no ResolveBatch RPC,
// so the dispensed stub could not implement credential.BatchResolver and
// every provider declaring batch_resolve failed conformance unconditionally.
// ---------------------------------------------------------------------------

const otherRef = "op://<vault>/<other-item>/<field>"
const otherSecret = "placeholder-api-token"

// batchMemLoader is a compliant batch-capable provider implementation.
type batchMemLoader struct{ memLoader }

func (b *batchMemLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapBatchResolve}
}

func (b *batchMemLoader) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	out := make([]credential.BatchResult, 0, len(refs))
	for _, r := range refs {
		res, err := b.Resolve(ctx, r)
		if err != nil {
			failed, _ := credential.BatchFailed(r, err)
			out = append(out, failed)
			continue
		}
		resolved, _ := credential.BatchResolved(r, res)
		out = append(out, resolved)
	}
	return out, nil
}

var _ credential.BatchResolver = (*batchMemLoader)(nil)

// declaresBatchButCannot reports the capability without implementing the
// interface: the false declaration, over the wire.
type declaresBatchButCannot struct{ memLoader }

func (d *declaresBatchButCannot) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapBatchResolve}
}

func batchVals() map[string]string {
	return map[string]string{knownRef: knownSecret, otherRef: otherSecret}
}

// TestStubShapeMirrorsTheProvidersDeclaration: a host discovers an optional
// operation by type-asserting for its interface. That has to work the same
// way for a Track-B provider as for a native connector, or every host needs a
// second code path — and it has to be conditional, because a stub that always
// implemented BatchResolver would make every honest non-batching provider
// look implemented-but-undeclared to conformance.
func TestStubShapeMirrorsTheProvidersDeclaration(t *testing.T) {
	t.Run("declared: the stub batches", func(t *testing.T) {
		c := newTestClient(t, &batchMemLoader{memLoader{vals: batchVals()}})
		if _, ok := c.(credential.BatchResolver); !ok {
			t.Fatalf("a provider reporting %s must dispense a credential.BatchResolver, got %T",
				credential.CapBatchResolve, c)
		}
	})

	t.Run("not declared: the stub does not", func(t *testing.T) {
		c := newTestClient(t, &memLoader{vals: batchVals()})
		if _, ok := c.(credential.BatchResolver); ok {
			t.Fatalf("a provider that does not report %s must not dispense a credential.BatchResolver",
				credential.CapBatchResolve)
		}
	})
}

// TestBatchRoundTripOverTheWire drives the RPC end to end: one call, one
// result per requested reference, in order, each carrying its own outcome.
func TestBatchRoundTripOverTheWire(t *testing.T) {
	c := newTestClient(t, &batchMemLoader{memLoader{vals: batchVals()}})
	b, ok := c.(credential.BatchResolver)
	if !ok {
		t.Fatalf("dispensed %T, want a credential.BatchResolver", c)
	}

	a := mustParse(t, knownRef)
	other := mustParse(t, otherRef)
	missing := mustParse(t, "op://<vault>/<missing-item>/<field>")

	results, err := b.ResolveBatch(context.Background(), []ref.Ref{a, other, missing})
	if err != nil {
		t.Fatalf("ResolveBatch: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results for 3 references", len(results))
	}

	for i, want := range []struct {
		r   ref.Ref
		val string
	}{{a, knownSecret}, {other, otherSecret}} {
		if results[i].Ref() != want.r {
			t.Fatalf("result %d is for %s, want %s", i, results[i].Ref(), want.r)
		}
		if results[i].Err() != nil {
			t.Fatalf("result %d failed: %v", i, results[i].Err())
		}
		mat, verr := results[i].Resolution().Value()
		if verr != nil {
			t.Fatalf("result %d: %v", i, verr)
		}
		if string(mat.Reveal()) != want.val {
			t.Fatalf("result %d = %q, want %q", i, mat.Reveal(), want.val)
		}
	}

	// A per-reference failure is a result, not a batch failure — and it keeps
	// its classification across the wire, so the host acts on the Kind rather
	// than on the provider's wording.
	if results[2].Err() == nil {
		t.Fatalf("the missing reference must come back as a failed result")
	}
	if cerr.KindOf(results[2].Err()) != cerr.KindNotFound {
		t.Fatalf("per-result failure Kind = %v, want KindNotFound", cerr.KindOf(results[2].Err()))
	}
	if _, verr := results[2].Resolution().Value(); verr == nil {
		t.Fatalf("a failed result must not carry a readable resolution")
	}
}

// TestBatchFromAProviderIsGuarded: what the batch RPC returns is checked
// host-side for the same reason a single Resolve is. A batch whose results
// cannot be attributed to the references asked about is how the wrong secret
// reaches the wrong caller.
func TestBatchFromAProviderIsGuarded(t *testing.T) {
	a := mustParse(t, knownRef)
	other := mustParse(t, otherRef)

	for _, tc := range []struct {
		name    string
		results []*connectorpb.ResolveBatchResult
	}{
		{
			name:    "one result short",
			results: []*connectorpb.ResolveBatchResult{{Ref: transportRef(a), Material: &connectorpb.Material{Value: []byte("v")}}},
		},
		{
			name: "results out of order",
			results: []*connectorpb.ResolveBatchResult{
				{Ref: transportRef(other), Material: &connectorpb.Material{Value: []byte("v")}},
				{Ref: transportRef(a), Material: &connectorpb.Material{Value: []byte("v")}},
			},
		},
		{
			name: "a result carrying no outcome at all",
			results: []*connectorpb.ResolveBatchResult{
				{Ref: transportRef(a), Material: &connectorpb.Material{Value: []byte("v")}},
				{Ref: transportRef(other)},
			},
		},
		{
			name: "a result whose empty material is not asserted",
			results: []*connectorpb.ResolveBatchResult{
				{Ref: transportRef(a), Material: &connectorpb.Material{Value: []byte("v")}},
				{Ref: transportRef(other), Material: &connectorpb.Material{}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dialRaw(t, &rawProvider{
				caps:  []string{string(credential.CapRead), string(credential.CapBatchResolve)},
				batch: tc.results,
			}, "rogue-provider")
			b, ok := c.(credential.BatchResolver)
			if !ok {
				t.Fatalf("dispensed %T, want a credential.BatchResolver", c)
			}
			_, err := b.ResolveBatch(context.Background(), []ref.Ref{a, other})
			if err == nil {
				t.Fatalf("the host accepted a batch it cannot attribute to the request")
			}
			var fe *credential.FaultError
			if !errors.As(err, &fe) {
				t.Fatalf("error is %T (%v), want *credential.FaultError", err, err)
			}
			if fe.Connector != "rogue-provider" {
				t.Fatalf("Connector = %q, want the provider named", fe.Connector)
			}
		})
	}

	t.Run("an asserted empty secret is a valid batch outcome", func(t *testing.T) {
		c := dialRaw(t, &rawProvider{
			caps: []string{string(credential.CapRead), string(credential.CapBatchResolve)},
			batch: []*connectorpb.ResolveBatchResult{
				{Ref: transportRef(a), Material: &connectorpb.Material{EmptyByAssertion: true}},
			},
		}, "honest-provider")
		b, ok := c.(credential.BatchResolver)
		if !ok {
			t.Fatalf("dispensed %T, want a credential.BatchResolver", c)
		}
		got, err := b.ResolveBatch(context.Background(), []ref.Ref{a})
		if err != nil {
			t.Fatalf("an asserted empty secret must be a valid batch outcome: %v", err)
		}
		mat, verr := got[0].Resolution().Value()
		if verr != nil || len(mat.Reveal()) != 0 {
			t.Fatalf("want readable, empty material; got %v %v", mat, verr)
		}
	})
}

// TestBatchDeclaredButNotImplementedAnswersUnsupported: the stub's shape
// follows the provider's DECLARATION, so a provider that lies gets a stub
// with the method. Calling it must fail loudly and typed — never fan out to N
// single resolves behind the host's back, which is how a quota disappears
// with the evidence pointing somewhere else.
func TestBatchDeclaredButNotImplementedAnswersUnsupported(t *testing.T) {
	c := newTestClient(t, &declaresBatchButCannot{memLoader{vals: batchVals()}})
	b, ok := c.(credential.BatchResolver)
	if !ok {
		t.Fatalf("dispensed %T, want a credential.BatchResolver for a provider that declares batch", c)
	}
	_, err := b.ResolveBatch(context.Background(), []ref.Ref{mustParse(t, knownRef)})
	if err == nil {
		t.Fatalf("a provider that declares batch without implementing it must fail the call")
	}
	if cerr.KindOf(err) != cerr.KindUnsupported {
		t.Fatalf("KindOf = %v, want KindUnsupported", cerr.KindOf(err))
	}
}

// TestProviderKindConnectorPassesConformanceIncludingBatch is the whole point
// of adding the RPC. A kind: provider connector declaring batch_resolve —
// manifest and Capabilities() alike — must be able to score a genuinely green
// credconform report through the dispensed host stub. Before the RPC existed
// it could not: batch/declared-is-implemented failed unconditionally, so the
// capability was declarable and unreachable.
func TestProviderKindConnectorPassesConformanceIncludingBatch(t *testing.T) {
	c := newTestClient(t, &batchMemLoader{memLoader{vals: batchVals()}})

	m := manifest.Doc{
		Name:           "placeholder-provider",
		Implements:     connector.ClassCredentialLoader,
		Kind:           manifest.KindProvider,
		MinHostVersion: "0.4.0",
		Capabilities:   []connector.Capability{credential.CapRead, credential.CapBatchResolve},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("the fixture manifest must be valid: %v", err)
	}

	rep, err := credconform.Run(context.Background(), c, credconform.Options{
		Manifest:     m,
		Resolvable:   []ref.Ref{mustParse(t, knownRef), mustParse(t, otherRef)},
		Unresolvable: mustParse(t, "op://<vault>/<missing-item>/<field>"),
	})
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("a conformant batch-capable provider must pass over the wire:\n%s", rep)
	}
	// Green because the batch check RAN, not because it was skipped.
	var sawBatchShape bool
	for _, res := range rep.Results {
		if res.Name == credconform.CheckBatchShape {
			sawBatchShape = true
		}
	}
	if !sawBatchShape {
		t.Fatalf("%q did not run; the report is green because nothing looked:\n%s", credconform.CheckBatchShape, rep)
	}
}

func mustParse(t *testing.T, s string) ref.Ref {
	t.Helper()
	r, err := ref.Parse(s)
	if err != nil {
		t.Fatalf("ref.Parse(%q): %v", s, err)
	}
	return r
}

func transportRef(r ref.Ref) *connectorpb.Ref {
	return &connectorpb.Ref{Vault: r.Vault, Item: r.Item, Field: r.Field}
}
