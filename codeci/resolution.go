package codeci

import (
	"fmt"
	"slices"
	"strconv"
)

// A Resolution is what a [CodeCI] list operation returns: either a list the
// connector actually read from the code host, or nothing at all.
//
// # Why this type exists
//
// A []PR (or []DiffFile, []Branch, []CheckRun) of length zero is AMBIGUOUS.
// It means either "the code host genuinely has none of these" or "the read
// failed and I am returning a zero value", and no amount of care at the call
// site can tell the two apart after the fact. This is the same conflation
// [github.com/arqtiqa/arqtos-sdk-go/roster.Resolution] refuses, deliberately
// the SAME SHAPE here rather than a second invention — but the consequence
// lands on CI safety rather than on access: a caller deciding "does this ref
// have any failing checks" from a truncated GetCheckRuns that read as a
// complete, empty pass would merge on an answer it never actually computed.
//
// Resolution removes the shape rather than warning about it:
//
//   - There is no exported field and no constructor that produces a readable
//     Resolution from an empty list. [Resolved] refuses one and returns a
//     [FaultError] instead, at the point of the mistake.
//   - The zero Resolution — what `return Resolution[PR]{}, nil` produces — is
//     not an empty list. It is UNRESOLVED, and [Resolution.Items] refuses to
//     read it. There is no accessor that turns it into a nil slice.
//   - A code host that genuinely holds none of the requested kind is
//     expressible, but only by saying so: [EmptyList]. Emptiness is asserted,
//     never inferred from a length.
//
// Callers therefore never test a Resolution for emptiness. They call
// [Resolution.Items] and handle its error — a question about the connector,
// never about the code host.
//
// # A non-empty list is not necessarily a COMPLETE one
//
// [Resolved] also requires a [Completeness] on every call, with no default.
// ListPRs, GetDiff, ListBranches and GetCheckRuns are all documented to page
// internally for a real repository, and a real pagination loop fails
// somewhere in the middle at least once in production. Before Completeness
// existed, the natural line to write at that point was
// `return Resolved(itemsSoFar)`: readable, non-empty, and indistinguishable
// from a complete read. So [Resolved] takes [Completeness] explicitly at
// every construction site — [Complete] for the ordinary case, [Partial] for a
// read that stopped early — and there is deliberately no way to read a
// Partial resolution: a connector whose internal pagination breaks partway
// must return a typed failure instead.
type Resolution[T any] struct {
	items    []T
	resolved bool
}

// A Completeness is the connector's assertion about whether the list handed
// to [Resolved] is everything the operation is meant to report, or only as
// much of it as a read managed to examine before it stopped.
//
// There is no default. [Resolved] takes one on every call, so there is no
// path from "my internal pagination broke off partway" to a readable success
// without SAYING SO.
type Completeness int

const (
	// Complete asserts items is everything the operation is meant to report.
	Complete Completeness = iota + 1
	// Partial asserts items is only what a read managed to cover before it
	// stopped. [Resolved] refuses to build a readable Resolution from one: a
	// read that stopped early must surface as a typed failure
	// (cerr.KindUnavailable, cerr.KindTimeout, ...), never as a smaller
	// success.
	Partial
)

var completenessNames = map[Completeness]string{
	Complete: "Complete",
	Partial:  "Partial",
}

// Valid reports whether c is in the closed vocabulary.
func (c Completeness) Valid() bool {
	_, ok := completenessNames[c]
	return ok
}

// String renders c's stable name. A value outside the vocabulary renders as
// invalid_completeness(N) rather than as "Partial" or "Complete", so a
// message built from c.String() names what was actually passed instead of
// guessing.
func (c Completeness) String() string {
	if name, ok := completenessNames[c]; ok {
		return name
	}
	return "invalid_completeness(" + strconv.Itoa(int(c)) + ")"
}

// Resolved wraps a list a connector actually read, asserting its
// [Completeness].
//
// It returns a [FaultError] when items is nil or empty (a code host that
// genuinely holds nothing calls [EmptyList] instead), and when c is
// [Partial] (a truncated read is a typed failure, not a smaller success).
// The Resolution returned alongside either error is unreadable, so a
// connector that ignores the error still cannot pass an unread — or
// partially read — list off as a complete one.
//
// The list is copied, so a connector that reuses or truncates its own slice
// afterwards cannot retroactively change what the host was handed.
func Resolved[T any](items []T, c Completeness) (Resolution[T], error) {
	if len(items) == 0 {
		return Resolution[T]{}, &FaultError{
			Op:    "List",
			Fault: FaultUnresolved,
			Detail: "the connector reported success while carrying no list; a read that returned nothing because it " +
				"failed, was unauthenticated, or was misdirected must be reported as a failure, and one that genuinely " +
				"found nothing must say so with EmptyList",
		}
	}
	if c != Complete {
		detail := fmt.Sprintf(
			"the connector reported a PARTIAL read of %d entr%s as a success (Resolved(items, codeci.Partial)); a "+
				"read that stopped before covering everything it is meant to must be reported as a typed failure "+
				"(see the cerr package), not as a smaller success — a caller deciding, say, whether a ref has any "+
				"failing checks from a truncated-but-readable list would act on an answer it never actually computed",
			len(items), plural(len(items)))
		if c != Partial {
			detail = fmt.Sprintf(
				"Resolved was called with a Completeness of %s, which is neither codeci.Complete nor codeci.Partial "+
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
	return Resolution[T]{items: slices.Clone(items), resolved: true}, nil
}

// EmptyList reports a code host that genuinely, verifiably holds none of the
// requested kind — distinct from a read that produced nothing.
//
// This is an ASSERTION by the connector, not a fallback. Call it only where
// the backend distinguishes "read successfully, found none" from "could not
// read" — an HTTP 200 carrying an empty array is the first, and anything else
// is not. [CodeCI.GetDiff] documents why this is never a legitimate answer
// for a diff: a real pull/merge request always has at least one changed
// file.
func EmptyList[T any]() Resolution[T] { return Resolution[T]{items: []T{}, resolved: true} }

// Items returns the list that was read.
//
// It returns a [FaultError] when nothing was resolved. That error — not an
// empty slice — is how an unresolved Resolution reaches a caller: reading
// requires passing the check, so there is no path from "the connector
// returned nothing" to "the code host has none of these".
//
// The returned list is empty, but present, for an [EmptyList]. It is a copy —
// so a host that sorts, filters or annotates it in place cannot change what
// the next reader of the same Resolution sees.
func (r Resolution[T]) Items() ([]T, error) {
	if !r.resolved {
		return nil, &FaultError{
			Op:    "List",
			Fault: FaultUnresolved,
			Detail: "read of a resolution that carries no list; an unresolved result is not a result of nothing, " +
				"and treating it as one lets a caller act on a read that never completed",
		}
	}
	return slices.Clone(r.items), nil
}

// present reports whether the resolution carries a list at all. It is
// deliberately unexported: a caller asks [Resolution.Items] and handles its
// error, rather than branching on presence and then reading anyway.
func (r Resolution[T]) present() bool { return r.resolved }

// String reports the state and the count, never the entries. A Resolution
// travels through host code that logs, and a pull/merge request's title or
// body can carry text nobody meant to end up in a log line.
func (r Resolution[T]) String() string {
	if !r.resolved {
		return "[UNRESOLVED codeci list]"
	}
	return "[codeci list of " + strconv.Itoa(len(r.items)) + " entr" + plural(len(r.items)) + "]"
}

// GoString redacts under %#v for the same reason as String.
func (r Resolution[T]) GoString() string { return r.String() }

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
