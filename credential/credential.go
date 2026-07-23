// Package credential defines the CredentialLoader connector-class contract:
// resolve op:// references to secret material, with lease/renew for dynamic secrets.
// Material is refs-only in, redacted-by-default out, and wipeable (dies-with-session).
package credential

import (
	"context"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

const (
	CapRead    connector.Capability = "read"
	CapLease   connector.Capability = "lease"
	CapRotate  connector.Capability = "rotate"
	CapOIDC    connector.Capability = "oidc"
	CapAppRole connector.Capability = "approle"
)

// Material holds resolved secret material. It redacts on String()/GoString();
// the raw bytes are reached only via Reveal(); Zero() wipes it.
type Material struct {
	b []byte
}

func NewMaterial(b []byte) *Material {
	c := make([]byte, len(b))
	copy(c, b)
	return &Material{b: c}
}

func (m *Material) Reveal() []byte { return m.b }

func (m *Material) String() string   { return "[REDACTED credential]" }
func (m *Material) GoString() string { return "[REDACTED credential]" }

func (m *Material) Zero() {
	for i := range m.b {
		m.b[i] = 0
	}
	m.b = m.b[:0]
}

type Lease struct {
	ID        string
	TTL       time.Duration
	ExpiresAt time.Time
	Renewable bool
}

// Expired reports whether the lease has expired as of now (now is injected for testability).
func (l Lease) Expired(now time.Time) bool { return !now.Before(l.ExpiresAt) }

// CredentialLoader is the vault/credential connector-class contract. Implemented by
// native in-proc connectors (1P) and Track-B out-of-process providers (Infisical, Vault).
type CredentialLoader interface {
	connector.Connector
	Resolve(ctx context.Context, r ref.Ref) (*Material, error)
	List(ctx context.Context, scope string) ([]ref.Ref, error)
	Lease(ctx context.Context, r ref.Ref) (*Material, Lease, error)
	Renew(ctx context.Context, l Lease) (Lease, error)
	Revoke(ctx context.Context, l Lease) error
}
