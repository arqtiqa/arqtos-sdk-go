package roster

import (
	"fmt"
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
// # A non-empty list is not necessarily a COMPLETE one
//
// [Resolved] does one more thing beyond refusing an empty list: it requires a
// [Completeness] on every call. The gap it closes is a second conflation, the
// same shape as the first but for a subtler reason. ListPrincipals is
// documented to paginate internally for a real directory of any size, and a
// real pagination loop fails somewhere in the middle at least once in
// production — a 429 on page 7 of 250, a timeout on page 40 of 40. Before
// Completeness existed, the natural line for an author to write at that point
// was `return Resolved(whatIHaveSoFar)`: readable, non-empty, and — to every
// check in this package and to rosterconform — indistinguishable from a
// complete read. The blast radius is the SAME one that justifies this whole
// type: an offboarding sweep run against a principal list truncated after a
// failed page revokes everyone past the failure point, and is MORE likely to
// occur than a wholesale empty read, because pagination fails in the middle
// far more often than a read fails to start at all.
//
// So [Resolved] takes a [Completeness] explicitly, with no default, at every
// construction site: [Complete] for the ordinary case, and [Partial] for a
// read that stopped early. There is deliberately no way to read a Partial
// resolution — see [Completeness] — so the fix is the stronger of the two
// the type could have taken: a partial read is not merely discouraged, it is
// impossible to express as a success at all. A connector whose internal
// pagination breaks partway must return a typed failure (a cerr.Kind, see the
// cerr package), the same way a wholesale read failure already must.
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

// A Completeness is the connector's assertion about whether the list handed
// to [Resolved] is everything the operation is meant to report — every
// principal, every group, or every member of the group that was asked about
// — or only as much of it as a read managed to examine before it stopped.
//
// There is no default. [Resolved] takes one on every call, so there is no
// path from "my internal pagination broke off on page 7 of 250" to a
// readable success without SAYING SO. See [Complete] and [Partial].
type Completeness int

const (
	// Complete asserts items is everything the operation is meant to
	// report. This is the ordinary case: a connector whose read is atomic,
	// or whose pagination ran to its own natural end, asserts Complete.
	Complete Completeness = iota + 1
	// Partial asserts items is only what a read managed to cover before it
	// stopped — not everyone. [Resolved] refuses to build a readable
	// Resolution from one, the same way it refuses an empty list: a host's
	// reconcile loop cannot safely revoke access for a principal it never
	// got to examine, so a read that stopped early must surface as a typed
	// failure (cerr.KindUnavailable, cerr.KindTimeout, ...), never as a
	// smaller success. There is no reading of a Partial resolution —
	// reaching for this constant gets back a *FaultError naming exactly
	// that, in place of whatever typed failure your own pagination loop
	// should return instead.
	Partial
)

// completenessNames is the single source of truth for the closed Completeness
// vocabulary: [Completeness.Valid] and [Completeness.String] both derive from
// it, the same way [PrincipalKind] and cerr.Kind do — and for the same reason:
// so that an error message reporting what was actually passed never has to
// assume, and can never lie about, which constant a caller used.
var completenessNames = map[Completeness]string{
	Complete: "Complete",
	Partial:  "Partial",
}

// Valid reports whether c is in the closed vocabulary. A Completeness that is
// not is not an assertion of anything — it is an integer someone converted —
// and [Resolved] refuses it the same way it refuses Partial, but for a
// different reason: Partial is an honest "not everything"; an invalid value is
// not even that.
func (c Completeness) Valid() bool {
	_, ok := completenessNames[c]
	return ok
}

// String renders c's stable name. A value outside the vocabulary renders as
// invalid_completeness(N) rather than as "Partial" or "Complete", so a message
// built from c.String() names what was actually passed instead of guessing.
func (c Completeness) String() string {
	if name, ok := completenessNames[c]; ok {
		return name
	}
	return "invalid_completeness(" + strconv.Itoa(int(c)) + ")"
}

// Resolved wraps a list a connector actually read, asserting its
// [Completeness].
//
// It returns a [FaultError] when items is nil or empty: at that point the
// connector has nothing to report as a success, so it must report either a
// failure or a deliberate [EmptyRoster]. It also returns a [FaultError] when c
// is [Partial]: a read that stopped before covering everything it is meant to
// is not a smaller success, and asserting Partial gets back the failure that
// belongs there instead of a readable Resolution. The Resolution returned
// alongside either error is unreadable, so a connector that ignores the error
// still cannot pass an unread — or partially read — directory off as a
// complete one.
//
// A connector whose directory legitimately holds no principals, no groups or
// no members calls [EmptyRoster] instead — deliberately, and in the knowledge
// that its backend distinguishes "read successfully, found nobody" from
// "could not read".
//
// The list is copied, so a connector that reuses or truncates its own slice
// afterwards cannot retroactively change what the host was handed.
func Resolved[T any](items []T, c Completeness) (Resolution[T], error) {
	if len(items) == 0 {
		return Resolution[T]{}, &FaultError{
			Op:    "List",
			Fault: FaultUnresolved,
			Detail: "the connector reported success while carrying no list; a directory read that returned nothing " +
				"because it failed, was unauthenticated, or was misdirected must be reported as a failure, and one " +
				"that genuinely found nobody must say so with EmptyRoster",
		}
	}
	if c != Complete {
		detail := fmt.Sprintf(
			"the connector reported a PARTIAL read of %d entr%s as a success (Resolved(items, roster.Partial)); a "+
				"read that stopped before covering everything it is meant to must be reported as a typed failure "+
				"(see the cerr package), not as a smaller success — a host cannot safely revoke access for someone "+
				"it never got to examine",
			len(items), plural(len(items)))
		// c is not Partial either: name what was ACTUALLY passed rather than
		// assuming Partial. Completeness(99), or the zero value from a
		// forgotten argument, is not "a truncated read reported honestly" —
		// it is an unrecognised value, and the message must not claim it was
		// the one legitimate reason (Partial) this branch also exists for.
		if c != Partial {
			detail = fmt.Sprintf(
				"Resolved was called with a Completeness of %s, which is neither roster.Complete nor roster.Partial "+
					"— an assertion outside the closed vocabulary is refused the same way an honest Partial is, "+
					"because there is no reading under which trusting an unrecognised completeness value is safe",
				c)
		}
		return Resolution[T]{}, &FaultError{
			Op:     "List",
			Fault:  FaultPartial,
			Detail: detail,
		}
	}
	return Resolution[T]{items: cloneEntries(items), resolved: true}, nil
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
// The returned list is empty, but present, for an [EmptyRoster]. It is a copy
// — including [Group.ParentIDs], the only slice-valued field across the three
// Roster types — so a host that sorts, filters or annotates it in place
// cannot change what the next reader of the same Resolution sees.
func (r Resolution[T]) Items() ([]T, error) {
	if !r.resolved {
		return nil, &FaultError{
			Op:    "List",
			Fault: FaultUnresolved,
			Detail: "read of a resolution that carries no list; an unresolved roster is not a roster of nobody, " +
				"and treating it as one deprovisions everyone",
		}
	}
	return cloneEntries(r.items), nil
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

// cloneEntries copies items the way [Resolved] and [Resolution.Items] both
// need: a top-level copy so the caller's slice and the Resolution's no longer
// share a backing array, and — for [Group], whose ParentIDs is the only
// slice-valued field across the three Roster types — a copy of THAT nested
// slice too.
//
// slices.Clone alone stops at the top level: it copies each element by value,
// and copying a Group by value copies the ParentIDs slice HEADER (pointer,
// length, capacity) without copying what it points at. A Resolution built
// that way still shares its ParentIDs backing array with whatever slice the
// caller passed in, so a host that sorts ParentIDs in place through one
// Items() call changes what the next Items() call sees — and, via the same
// aliasing, reaches back into the connector's own slice. any(&out[i]) is how
// this reaches into the one field that needs it despite T being unconstrained:
// the assertion to *Group succeeds only for the instantiation where T is
// actually Group, so every other Resolution[T] pays nothing beyond the
// top-level copy it always needed.
func cloneEntries[T any](items []T) []T {
	out := slices.Clone(items)
	for i := range out {
		if g, ok := any(&out[i]).(*Group); ok {
			g.ParentIDs = slices.Clone(g.ParentIDs)
		}
	}
	return out
}
