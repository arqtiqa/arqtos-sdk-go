package plugin

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

type authLifeLoader struct {
	memLoader
	expires time.Time
	renews  int
}

func (a *authLifeLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapAuthLifecycle}
}

func (a *authLifeLoader) AuthStatus(_ context.Context, _ time.Time) (credential.AuthSession, error) {
	return credential.AuthSession{ID: "auth-handle", Kind: credential.KindAuth, ExpiresAt: a.expires, Renewable: true}, nil
}

func (a *authLifeLoader) RenewAuth(_ context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	if err := credential.CheckAuthSession(s); err != nil {
		return credential.AuthSession{}, cerr.New(cerr.KindInvalid, "RenewAuth", err)
	}
	if s.Expired(now) {
		return credential.AuthSession{}, cerr.New(cerr.KindInvalid, "RenewAuth", nil)
	}
	a.renews++
	a.expires = now.Add(time.Hour)
	return credential.AuthSession{ID: s.ID, Kind: credential.KindAuth, ExpiresAt: a.expires, Renewable: true}, nil
}

func (a *authLifeLoader) Reauthenticate(_ context.Context, _ *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	a.expires = now.Add(time.Hour)
	return credential.AuthSession{ID: "auth-handle", Kind: credential.KindAuth, ExpiresAt: a.expires, Renewable: true}, nil
}

var _ credential.AuthLifecycle = (*authLifeLoader)(nil)

type declaresLifeButCannot struct{ memLoader }

func (d *declaresLifeButCannot) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapAuthLifecycle}
}

func TestStubShapeMirrorsAuthLifecycleDeclaration(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	t.Run("declared: the stub binds", func(t *testing.T) {
		c := newTestClient(t, &authLifeLoader{memLoader: memLoader{vals: batchVals()}, expires: now.Add(time.Hour)})
		if _, ok := c.(credential.AuthLifecycle); !ok {
			t.Fatalf("a provider reporting %s must dispense AuthLifecycle, got %T", credential.CapAuthLifecycle, c)
		}
	})
	t.Run("not declared: the stub does not", func(t *testing.T) {
		c := newTestClient(t, &memLoader{vals: batchVals()})
		if _, ok := c.(credential.AuthLifecycle); ok {
			t.Fatal("undeclared auth_lifecycle must not dispense AuthLifecycle")
		}
	})
}

func TestAuthLifecycleUsesHostClockWithoutHiddenRenewal(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	impl := &authLifeLoader{memLoader: memLoader{vals: batchVals()}, expires: now.Add(time.Hour)}
	c := newTestClient(t, impl)
	life, ok := c.(credential.AuthLifecycle)
	if !ok {
		t.Fatalf("dispensed %T", c)
	}
	s1, err := life.AuthStatus(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := life.AuthStatus(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !s1.ExpiresAt.Equal(s2.ExpiresAt) || impl.renews != 0 {
		t.Fatalf("AuthStatus mutated expiry (hidden renewal): %v then %v renews=%d", s1.ExpiresAt, s2.ExpiresAt, impl.renews)
	}
	if !s1.Expired(now.Add(2 * time.Hour)) {
		t.Fatal("host clock did not drive expiry")
	}
	_, err = life.RenewAuth(context.Background(), s1, now.Add(2*time.Hour))
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("expired renew: %v, want KindInvalid", err)
	}
}

func TestRenewAuthRejectsSecretLeaseHandle(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	c := newTestClient(t, &authLifeLoader{memLoader: memLoader{vals: batchVals()}, expires: now.Add(time.Hour)})
	life, ok := c.(credential.AuthLifecycle)
	if !ok {
		t.Fatalf("dispensed %T", c)
	}
	_, err := life.RenewAuth(context.Background(), credential.AuthSession{
		ID: "dyn-123", Kind: credential.KindSecretLease, ExpiresAt: now.Add(time.Hour), Renewable: true,
	}, now)
	if err == nil {
		t.Fatal("secret_lease handle accepted as auth")
	}
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("KindOf = %v, want KindInvalid", cerr.KindOf(err))
	}
}

func TestStaticAuthLifecycleDoesNotAdvertiseSecretLease(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	impl := &authLifeLoader{memLoader: memLoader{vals: batchVals()}, expires: now.Add(time.Hour)}
	if impl.Capabilities().Has(credential.CapLease) {
		t.Fatal("static KV under expiring auth must not advertise CapLease")
	}
	c := newTestClient(t, impl)
	_, _, err := c.Lease(context.Background(), mustParse(t, knownRef))
	if cerr.KindOf(err) != cerr.KindUnsupported {
		t.Fatalf("Lease = %v, want KindUnsupported", err)
	}
}

func TestAuthLifecycleDeclaredButNotImplementedAnswersUnsupported(t *testing.T) {
	c := newTestClient(t, &declaresLifeButCannot{memLoader{vals: batchVals()}})
	life, ok := c.(credential.AuthLifecycle)
	if !ok {
		t.Fatalf("dispensed %T", c)
	}
	_, err := life.AuthStatus(context.Background(), time.Now())
	if cerr.KindOf(err) != cerr.KindUnsupported {
		t.Fatalf("KindOf = %v, want KindUnsupported", cerr.KindOf(err))
	}
}

func TestOldPeerAuthLifecycleIsUnimplemented(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	connectorpb.RegisterCredentialLoaderServer(srv, &connectorpb.UnimplementedCredentialLoaderServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_, err = connectorpb.NewCredentialLoaderClient(conn).AuthStatus(context.Background(), &connectorpb.AuthStatusRequest{})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unimplemented {
		t.Fatalf("old peer AuthStatus: %v", err)
	}
	if cerr.KindOf(transport.ErrFromStatus(err)) != cerr.KindUnsupported {
		t.Fatalf("KindOf = %v", cerr.KindOf(transport.ErrFromStatus(err)))
	}
}
