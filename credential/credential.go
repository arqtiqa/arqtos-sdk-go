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
	// CapBatchResolve declares that this connector resolves many references
	// in ONE backend call, via [BatchResolver]. A connector that declares it
	// MUST implement that interface — see BatchResolver for why a false
	// declaration is worse than no declaration at all.
	CapBatchResolve connector.Capability = "batch_resolve"
)

// knownCapabilities is the closed capability vocabulary of this connector
// class. A manifest declaring anything outside it fails conformance: the
// capability a host does not recognise is a capability the host will not use,
// and a typo is indistinguishable from a capability that has yet to ship.
var knownCapabilities = connector.Capabilities{
	CapRead, CapLease, CapRotate, CapOIDC, CapAppRole, CapBatchResolve,
}

// KnownCapabilities returns the closed capability vocabulary for
// CredentialLoader, as a copy. Adding one is a deliberate contract change.
func KnownCapabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), knownCapabilities...)
}

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

func (m Material) String() string   { return "[REDACTED credential]" }
func (m Material) GoString() string { return "[REDACTED credential]" }

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

// CredentialLoader is the vault/credential connector-class contract,
// implemented by native in-process connectors and by out-of-process
// providers alike.
//
// Every failure it returns is typed: a *cerr.Error whose Kind comes from
// cerr's closed vocabulary, so a host acts on the classification and never on
// the message. An unclassifiable failure is cerr.KindUnknown, which fails the
// call and escalates nothing.
//
// Neither Resolve nor Lease can report a success that carries no value — see
// [Resolution].
//
// Optional operations live behind capabilities rather than in this interface:
// [BatchResolver] behind [CapBatchResolve]. A host type-asserts for them, and
// a connector that declares one without implementing it fails conformance.
type CredentialLoader interface {
	connector.Connector
	Resolve(ctx context.Context, r ref.Ref) (Resolution, error)
	List(ctx context.Context, scope string) ([]ref.Ref, error)
	Lease(ctx context.Context, r ref.Ref) (Resolution, Lease, error)
	Renew(ctx context.Context, l Lease) (Lease, error)
	Revoke(ctx context.Context, l Lease) error
}
