package plugin

import (
	"context"
	"fmt"
	"strings"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
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

func (m *memLoader) Resolve(_ context.Context, r ref.Ref) (*credential.Material, error) {
	v, ok := m.vals[r.String()]
	if !ok {
		return nil, cerr.New(cerr.KindNotFound, "Resolve", nil)
	}
	return credential.NewMaterial([]byte(v)), nil
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

func (m *memLoader) Lease(ctx context.Context, r ref.Ref) (*credential.Material, credential.Lease, error) {
	mat, err := m.Resolve(ctx, r)
	if err != nil {
		return nil, credential.Lease{}, err
	}
	return mat, credential.Lease{ID: "lease-1", Renewable: true}, nil
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

	mat, err := c.Resolve(context.Background(), r)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
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

	mat, err := c.Resolve(context.Background(), r)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	formatted := fmt.Sprintf("%v", mat)
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

	mat, lease, err := c.Lease(context.Background(), r)
	if err != nil {
		t.Fatalf("Lease: %v", err)
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
