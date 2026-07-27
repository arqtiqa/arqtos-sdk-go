package roster

import (
	"slices"
	"strconv"
)

// A Resolution is what a [Roster] list operation returns: either a list the
// connector actually read from the directory, or nothing at all.
//
// # Why this type exists
//
// A []Principal of length zero is AMBIGUOUS. It means either "this directory
// genuinely has nobody" or "the read failed and I am returning a zero value",
// and no amount of care at the call site can tell the two apart after the
// fact. The second reading is the dangerous one, because of what the host does
// next: an offboarding sweep computes "in arqtos but no longer in the
// directory" and revokes it. Fed an empty list that meant "the read failed",
// that sweep deprovisions the entire estate — correctly, according to
// everything it was told.
//
// This is the same conflation the credential class refuses with
// credential.Resolution, and it is deliberately the SAME SHAPE here rather
// than a second invention. The blast radius is worse: a credential that
// resolves to "" fails one authentication, while a roster that reads as nobody
// removes everyone's access at once.
//
// Resolution removes the shape rather than warning about it:
//
//   - There is no exported field and no constructor that produces a readable
//     Resolution from an empty list. [Resolved] refuses one and returns a
//     [FaultError] instead, at the point of the mistake.
//   - The zero Resolution — what `return Resolution[Principal]{}, nil`
//     produces — is not an empty list. It is UNRESOLVED, and
//     [Resolution.Items] refuses to read it. There is no accessor that turns
//     it into a nil slice.
//   - A directory that genuinely holds nobody is expressible, but only by
//     saying so: [EmptyRoster]. Emptiness is asserted, never inferred from a
//     length.
//
// Callers therefore never test a roster for emptiness. They call
// [Resolution.Items] and handle its error — which is a different question,
// asked of the connector rather than of the directory.
//
// # There is no Len, and no IsEmpty
//
// One accessor, on purpose. A Len() that answered 0 for an unresolved
// Resolution would put the ambiguity back: every caller that branched on
// Len() == 0 before reading would be back to guessing which of the two things
// it meant, and the type would be decoration. Ask [Resolution.Items], handle
// its error, and take len() of what it gives you.
//
// # Personal data
//
// A resolved Resolution holds directory records about people. [Resolution.String]
// therefore reports the STATE and the COUNT and never the contents, so a host
// that logs one — or formats a struct that contains one — does not write the
// directory into its logs. Reaching the records requires the explicit
// [Resolution.Items] call, the same way reaching secret bytes requires an
// explicit reveal.
type Resolution[T any] struct {
	// items is what was read. It is meaningful only when resolved is true; a
	// resolved Resolution with a zero-length items is a deliberately-empty
	// directory from [EmptyRoster].
	items []T
	// resolved is the presence flag, and it is a distinct field rather than a
	// nil check on items on purpose: nil-versus-empty is invisible at a call
	// site, survives no round trip, and is exactly the sort of distinction
	// that gets lost to an innocent append or copy. Presence is recorded, not
	// inferred.
	resolved bool
}

// Resolved wraps a list a connector actually read.
//
// It returns a [FaultError] when items is nil or empty: at that point the
// connector has nothing to report as a success, so it must report either a
// failure or a deliberate [EmptyRoster]. The Resolution returned alongside the
// error is unreadable, so a connector that ignores the error still cannot pass
// an unread directory off as an empty one.
//
// A connector whose directory legitimately holds no principals, no groups or
// no members calls [EmptyRoster] instead — deliberately, and in the knowledge
// that its backend distinguishes "read successfully, found nobody" from
// "could not read".
//
// The list is copied, so a connector that reuses or truncates its own slice
// afterwards cannot retroactively change what the host was handed.
func Resolved[T any](items []T) (Resolution[T], error) {
	if len(items) == 0 {
		return Resolution[T]{}, &FaultError{
			Op:    "List",
			Fault: FaultUnresolved,
			Detail: "the connector reported success while carrying no list; a directory read that returned nothing " +
				"because it failed, was unauthenticated, or was misdirected must be reported as a failure, and one " +
				"that genuinely found nobody must say so with EmptyRoster",
		}
	}
	return Resolution[T]{items: slices.Clone(items), resolved: true}, nil
}

// EmptyRoster reports a directory that genuinely, verifiably holds nothing of
// the requested kind — distinct from a read that produced nothing.
//
// This is an ASSERTION by the connector, not a fallback. Call it only where the
// backend can distinguish an empty result from an unauthenticated, throttled,
// misdirected or failed read. A connector that cannot make that distinction has
// a failure on its hands, not an empty directory, and must return a typed error
// (see the cerr package).
//
// It is a genuine state — a newly created group has no members, and a host must
// be able to see that and remove the access that came with it. What it is not
// is somewhere to put an error.
func EmptyRoster[T any]() Resolution[T] {
	return Resolution[T]{items: []T{}, resolved: true}
}

// Items returns the list that was read.
//
// It returns a [FaultError] when nothing was resolved. That error — not an
// empty slice — is how an unresolved roster reaches a caller, which is the
// whole point of the type: reading requires passing the check, so there is no
// path from "the connector returned nothing" to "the directory is empty".
//
// The returned list is empty, but present, for an [EmptyRoster]. It is a copy:
// a host that sorts, filters or annotates it in place cannot change what the
// next reader of the same Resolution sees.
func (r Resolution[T]) Items() ([]T, error) {
	if !r.resolved {
		return nil, &FaultError{
			Op:    "List",
			Fault: FaultUnresolved,
			Detail: "read of a resolution that carries no list; an unresolved roster is not a roster of nobody, " +
				"and treating it as one deprovisions everyone",
		}
	}
	return slices.Clone(r.items), nil
}

// present reports whether the resolution carries a list at all. It is
// deliberately unexported: a caller asks [Resolution.Items] and handles its
// error, rather than branching on presence and then reading anyway.
func (r Resolution[T]) present() bool { return r.resolved }

// entries is the in-package read: the backing slice, with no presence check and
// no copy. It exists for this package's own guards ([CheckPrincipals],
// [CheckMemberships]), which have already established presence via
// [CheckResolution] and only ITERATE what they were handed.
//
// It is unexported because both of the protections it skips matter to a caller
// and to neither of those guards: an unchecked read outside this package would
// silently yield a nil slice for an unresolved roster — the exact conflation
// this type removes — and an uncopied one would let a host mutate what the next
// reader sees.
func (r Resolution[T]) entries() []T { return r.items }

// String reports the STATE and the COUNT, never the records. A Resolution
// travels through host code that logs, and its contents are personal data
// about identifiable people; how many were read is diagnosis, who they are is
// not.
func (r Resolution[T]) String() string {
	if !r.resolved {
		return "[UNRESOLVED roster]"
	}
	return "[roster of " + strconv.Itoa(len(r.items)) + " entr" + plural(len(r.items)) + "]"
}

// GoString redacts under %#v for the same reason as String.
func (r Resolution[T]) GoString() string { return r.String() }

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
