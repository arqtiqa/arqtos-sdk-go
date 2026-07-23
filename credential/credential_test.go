package credential_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

func TestMaterialRedactsAndWipes(t *testing.T) {
	m := credential.NewMaterial([]byte("s3cret"))
	if strings.Contains(m.String(), "s3cret") {
		t.Fatalf("String() must not reveal material: %q", m.String())
	}
	if string(m.Reveal()) != "s3cret" {
		t.Fatalf("Reveal() wrong")
	}
	m.Zero()
	if len(m.Reveal()) != 0 {
		t.Fatalf("Zero() must wipe material")
	}
}

func TestLeaseExpired(t *testing.T) {
	now := time.Unix(1000, 0)
	l := credential.Lease{ExpiresAt: now.Add(time.Minute)}
	if l.Expired(now) {
		t.Fatalf("not expired yet")
	}
	if !l.Expired(now.Add(2 * time.Minute)) {
		t.Fatalf("should be expired")
	}
}

// Compile-time proof a type can satisfy CredentialLoader.
type stubLoader struct{ connector.Connector }

func (stubLoader) Resolve(context.Context, ref.Ref) (*credential.Material, error) { return nil, nil }
func (stubLoader) List(context.Context, string) ([]ref.Ref, error)                { return nil, nil }
func (stubLoader) Lease(context.Context, ref.Ref) (*credential.Material, credential.Lease, error) {
	return nil, credential.Lease{}, nil
}
func (stubLoader) Renew(context.Context, credential.Lease) (credential.Lease, error) {
	return credential.Lease{}, nil
}
func (stubLoader) Revoke(context.Context, credential.Lease) error { return nil }

var _ credential.CredentialLoader = stubLoader{}
