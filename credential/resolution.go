package credential

// A Resolution is what a CredentialLoader returns from Resolve: either a
// value the connector actually obtained, or nothing at all.
//
// # Why this type exists
//
// The pair (empty value, no error) is the estate's most-repeated credential
// bug, and it is a backend behaviour rather than an author's carelessness: a
// signed-out `op environment read` prints NOTHING and exits 0. A contract
// whose resolve returns ([]byte, error) invites every connector author to
// forward that faithfully, and every caller downstream then has to remember
// to check for emptiness. One who forgets ships a connector that appears to
// work and silently supplies "".
//
// Resolution removes the shape rather than warning about it:
//
//   - There is no exported field and no constructor that produces a readable
//     Resolution from empty material. [Resolved] refuses it and returns a
//     [FaultError] instead, at the point of the mistake.
//   - The zero Resolution — what `return Resolution{}, nil` produces — is not
//     an empty value. It is unresolved, and [Resolution.Value] refuses to
//     read it. There is no accessor that turns it into "".
//   - A secret the operator genuinely stored as empty is expressible, but
//     only by saying so: [ResolvedEmpty]. Emptiness is asserted, never
//     inferred from the bytes.
//
// Callers therefore never test a credential for emptiness. They call
// [Resolution.Value] and handle its error — which is a different question,
// asked of the connector rather than of the secret.
//
// # The guarantee is a CONSTRUCTION-TIME one
//
// "Readable implies non-empty" holds for a Resolution as it is built. It is
// not maintained for the lifetime of the value, and one operation breaks it
// on purpose: [Material.Zero] wipes the bytes a Resolution already holds, so
// a Resolution built from a real secret and then Zero()ed reads back as
// present-and-empty.
//
// That is intended — Zero() is the dies-with-session wipe, and a caller
// reaching for it is saying "this material is finished with", not "this
// credential was always empty". What it means in practice: hold a resolved
// value for as long as you need it, then Zero() it, and do not read through
// the same Resolution afterwards. The guarantee this type gives is about
// what a CONNECTOR can hand you, not about what you can do to it later.
type Resolution struct {
	// m is nil exactly when nothing was resolved. A non-nil m of length zero
	// is a deliberately-empty value from [ResolvedEmpty].
	m *Material
}

// Resolved wraps material a connector actually obtained.
//
// It returns a [FaultError] when m is nil or carries no bytes: at that point
// the connector has nothing to report as a success, so it must report a
// failure. The Resolution returned alongside the error is unreadable, so a
// connector that ignores the error still cannot pass an empty value off as a
// resolved one.
//
// A connector whose backend legitimately holds an empty value calls
// [ResolvedEmpty] instead — deliberately, and in the knowledge that its
// backend distinguishes "stored empty" from "not signed in".
func Resolved(m *Material) (Resolution, error) {
	if m == nil || len(m.Reveal()) == 0 {
		return Resolution{}, &FaultError{
			Op:     "Resolve",
			Fault:  FaultUnresolved,
			Detail: "the connector reported success while carrying no value; a backend that returns empty on a signed-out or misdirected read must be reported as a failure, not as an empty credential",
		}
	}
	return Resolution{m: m}, nil
}

// ResolvedEmpty reports a secret whose value is genuinely, intentionally
// empty — distinct from a reference that did not resolve.
//
// This is an assertion by the connector, not a fallback. Call it only where
// the backend can distinguish a stored empty value from an unauthenticated,
// missing, or failed read. A connector that cannot make that distinction has
// a failure on its hands, not an empty secret, and must return a typed error
// (see the cerr package).
func ResolvedEmpty() Resolution { return Resolution{m: NewMaterial(nil)} }

// Value returns the resolved material.
//
// It returns a [FaultError] when nothing was resolved. That error — not a
// zero-length value — is how an unresolved credential reaches a caller, which
// is the whole point of the type: reading requires passing the check, so
// there is no path from "the connector returned nothing" to "the credential
// is empty".
//
// The returned material is empty, but present, for a [ResolvedEmpty].
func (r Resolution) Value() (*Material, error) {
	if r.m == nil {
		return nil, &FaultError{
			Op:     "Resolve",
			Fault:  FaultUnresolved,
			Detail: "read of a resolution that carries no value",
		}
	}
	return r.m, nil
}

// present reports whether the resolution carries a value at all. It is
// deliberately unexported: a caller asks [Resolution.Value] and handles its
// error, rather than branching on presence and then reading anyway.
func (r Resolution) present() bool { return r.m != nil }

// String redacts. A Resolution travels through host code that logs, and it
// must be no more revealing than the Material it carries. It does report
// whether anything was resolved, which is diagnosis, not material.
func (r Resolution) String() string {
	if r.m == nil {
		return "[UNRESOLVED credential]"
	}
	return "[REDACTED credential]"
}

// GoString redacts under %#v for the same reason as String.
func (r Resolution) GoString() string { return r.String() }
