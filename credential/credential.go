// Package credential defines the CredentialLoader connector-class contract:
// resolve op:// references to secret material, with lease/renew for dynamic secrets.
// Material is refs-only in, redacted-by-default out, and wipeable (dies-with-session).
//
// # ⚠️ Close does NOT cross the wire for this class — put the wipe elsewhere
//
// [CredentialLoader] embeds connector.Connector, so every implementation writes a
// Close. For THIS class it is never called over the wire: the CredentialLoader
// service binds no Close RPC, and an out-of-process provider's teardown is owned by
// the host killing the subprocess. The Roster and Authenticator services DO bind
// one, so the same code in one of those runs and here it does not.
//
// ⚠️ A wipe written in Close therefore silently does not run out of process, which
// is the one place the "wipeable (dies-with-session)" promise above is easiest to
// believe and hardest to check. Put it where it will actually run: at the point the
// material is finished with, and on process exit.
//
// ⚠️ This is a statement about the CURRENT binding, and prose cannot notice when a
// binding changes. TestCloseBinding_OnlyTheServicesThatBindItAreClaimedTo at the
// repository root is its control: if the CredentialLoader service gains a Close RPC
// — a legitimate outcome, and the other half of arqtiqa/arqtos-sdk-go#89 — that test
// fails and this paragraph must be corrected in the same change rather than left
// confidently wrong.
package credential

import (
	"context"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

const (
	// CapRead declares support for [CredentialLoader.Resolve] and
	// [CredentialLoader.List] over static secret material. It is the baseline —
	// docs/CONTRACT.md's "CredentialLoader capabilities" section expects it on
	// every CredentialLoader.
	CapRead connector.Capability = "read"
	// CapLease declares support for [CredentialLoader.Lease], [Renew] and
	// [Revoke] — dynamic, time-bounded secrets. See docs/CONTRACT.md,
	// "CredentialLoader capabilities".
	CapLease connector.Capability = "lease"
	// CapRotate declares that this connector's BACKING STORE can be asked to
	// rotate the underlying secret. Rotation itself is out of scope for this
	// contract version; the capability marks only that the store can be asked.
	// See docs/CONTRACT.md, "CredentialLoader capabilities".
	CapRotate connector.Capability = "rotate"
	// CapOIDC declares that THIS CONNECTOR authenticates OUTWARD to its own
	// backing store via OIDC federation, holding no long-lived credential of its
	// own.
	//
	// ⚠️ IT DOES NOT MEAN the reference this connector serves came from an OIDC
	// flow, and it is not a behaviour exposed inward to a host. docs/CONTRACT.md's
	// "CredentialLoader capabilities" section states the direction: these describe
	// "how the connector itself authenticates outward, not a behavior it exposes
	// inward — hosts use them to reason about the connector's own credential
	// posture, e.g. for audit and rotation policy."
	//
	// ⚠️ The inward reading has already been shipped once (arqtos-sdk-go#90): a
	// connector declared it on that reading, the manifest validated, no harness
	// objected, and something false about its credential posture reached the one
	// audience that acts on it.
	CapOIDC connector.Capability = "oidc"
	// CapAppRole declares that THIS CONNECTOR authenticates OUTWARD to its own
	// backing store via an AppRole-style role-id/secret-id mechanism.
	//
	// ⚠️ IT DOES NOT MEAN this connector serves AppRole credentials to a host. Same
	// direction as [CapOIDC], and the same reason — see that constant.
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
