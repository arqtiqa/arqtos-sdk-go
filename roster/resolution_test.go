package roster_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// Placeholder directory identifiers. No estate identifiers anywhere: these are
// shapes, not addresses of anybody.
const (
	idPersonA   = "placeholder-principal-a"
	idPersonB   = "placeholder-principal-b"
	idSuspended = "placeholder-principal-suspended"
	idMachine   = "placeholder-principal-machine"
	idGroup     = "placeholder-group"
	idNestedIn  = "placeholder-parent-group"
	idNoGroup   = "placeholder-absent-group"
)

func person(id string) roster.Principal {
	return roster.Principal{
		ID:          id,
		Handle:      id,
		Email:       id + "@example.invalid",
		DisplayName: "Placeholder " + id,
		Active:      true,
		Kind:        roster.PrincipalHuman,
	}
}

// TestResolvedRefusesAnEmptyList is REQ-ARQ-P-17 at the point of the mistake,
// for the class where the blast radius is largest. A directory read that came
// back with nothing because it was unauthenticated, throttled or misdirected
// has nothing to report as a success — and an offboarding sweep over the
// (empty list, nil error) shape revokes access for everybody.
//
// The constructor hands back a fault instead, and the Resolution it hands back
// with it is unreadable, so a connector that ignores the error still cannot
// launder an unread directory into an empty one.
func TestResolvedRefusesAnEmptyList(t *testing.T) {
	t.Run("nil slice", func(t *testing.T) {
		res, err := roster.Resolved[roster.Principal](nil, roster.Complete)
		assertUnresolvedFault(t, res, err)
	})
	t.Run("zero-length slice", func(t *testing.T) {
		res, err := roster.Resolved([]roster.Principal{}, roster.Complete)
		assertUnresolvedFault(t, res, err)
	})
	t.Run("zero-length group slice", func(t *testing.T) {
		res, err := roster.Resolved([]roster.Group{}, roster.Complete)
		assertUnresolvedFault(t, res, err)
	})
	t.Run("zero-length membership slice", func(t *testing.T) {
		res, err := roster.Resolved([]roster.Membership{}, roster.Complete)
		assertUnresolvedFault(t, res, err)
	})
	// An empty list asserted Partial is unreadable for the SAME reason an
	// empty list asserted Complete is: there is nothing to report as a
	// success either way, and FaultUnresolved -- not FaultPartial -- is the
	// more specific diagnosis when the list is also empty.
	t.Run("zero-length slice asserted Partial", func(t *testing.T) {
		res, err := roster.Resolved([]roster.Principal{}, roster.Partial)
		assertUnresolvedFault(t, res, err)
	})
}

// TestResolvedRefusesAPartialAssertion is Finding 1's fix at the point of the
// mistake: roster.Resolved(itemsSoFar) used to be exactly what an author
// facing a mid-pagination failure would reach for, and nothing distinguished
// the result from a complete read. A truncated but non-empty list must not be
// constructible as a readable success at all.
func TestResolvedRefusesAPartialAssertion(t *testing.T) {
	res, err := roster.Resolved([]roster.Principal{person(idPersonA)}, roster.Partial)
	if err == nil {
		t.Fatalf("Resolved(items, roster.Partial) returned no error: a truncated read must not be readable as a success")
	}
	var fe *roster.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *roster.FaultError", err)
	}
	if fe.Fault != roster.FaultPartial {
		t.Fatalf("Fault = %q, want %q", fe.Fault, roster.FaultPartial)
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
	if !strings.Contains(fe.Detail, "typed failure") {
		t.Fatalf("the fault must tell the author to return a typed failure instead, got: %s", fe.Detail)
	}
	if _, ierr := res.Items(); ierr == nil {
		t.Fatalf("the Resolution returned alongside a Partial fault must not be readable")
	}
}

// TestCompletenessVocabularyIsClosedAndDerivedFromOneSource: Valid and String
// both derive from one map, the same way PrincipalKind's and cerr.Kind's do,
// so a value cannot be half-added — in the enum but nameless, or named but
// not enumerable.
func TestCompletenessVocabularyIsClosedAndDerivedFromOneSource(t *testing.T) {
	for _, c := range []roster.Completeness{roster.Complete, roster.Partial} {
		if !c.Valid() {
			t.Fatalf("%v is a vocabulary member but not Valid", c)
		}
		if name := c.String(); name == "" || strings.HasPrefix(name, "invalid_") {
			t.Fatalf("%d renders as %q, which is not a real name", int(c), name)
		}
	}
	if roster.Complete.String() == roster.Partial.String() {
		t.Fatalf("Complete and Partial must not render identically")
	}
}

// TestAnOutOfVocabularyCompletenessCannotClaimToBePartial is the point of
// adding Valid/String at all: an error message built from c.String() must
// name what was ACTUALLY passed. Before this, Resolved hardcoded "Partial" in
// its message for ANY non-Complete value, so Resolved(items,
// Completeness(99)) — a bug, not an honest partial-read assertion — produced
// a FaultError claiming the caller passed roster.Partial, which is false.
func TestAnOutOfVocabularyCompletenessCannotClaimToBePartial(t *testing.T) {
	bogus := roster.Completeness(99)
	if bogus.Valid() {
		t.Fatalf("Completeness(99) must not be Valid")
	}
	if got := bogus.String(); got != "invalid_completeness(99)" {
		t.Fatalf("Completeness(99).String() = %q", got)
	}
	if bogus.String() == roster.Partial.String() {
		t.Fatalf("an invalid Completeness must not render like Partial")
	}

	// The exact phrase the old, hardcoded message used to claim regardless of
	// what was actually passed. Its presence here would mean the message still
	// asserts a specific call (Resolved(items, roster.Partial)) that did not
	// happen — a different, false, and more specific claim than the honest
	// "neither Complete nor Partial" wording the fix uses instead.
	const falseCallClaim = "Resolved(items, roster.Partial)"

	_, err := roster.Resolved(truncatedForCompleteness, bogus)
	if err == nil {
		t.Fatalf("Resolved must refuse an out-of-vocabulary Completeness")
	}
	if strings.Contains(err.Error(), falseCallClaim) {
		t.Fatalf("the error must not claim the caller wrote Resolved(items, roster.Partial) when it did not: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_completeness(99)") {
		t.Fatalf("the error must name what was ACTUALLY passed, got: %v", err)
	}

	// The zero value — what a forgotten Completeness argument produces — must
	// be refused the same way, and for the same honest reason.
	var zero roster.Completeness
	if zero.Valid() {
		t.Fatalf("the zero Completeness must not be Valid")
	}
	_, zeroErr := roster.Resolved(truncatedForCompleteness, zero)
	if zeroErr == nil {
		t.Fatalf("Resolved(items, Completeness(0)) must be refused")
	}
	if strings.Contains(zeroErr.Error(), falseCallClaim) {
		t.Fatalf("Resolved(items, Completeness(0)) must not be reported as Resolved(items, roster.Partial): %v", zeroErr)
	}

	// The genuine Partial case must still say Partial — this is not a
	// regression check pinning the WRONG thing.
	_, partialErr := roster.Resolved(truncatedForCompleteness, roster.Partial)
	if !strings.Contains(partialErr.Error(), falseCallClaim) {
		t.Fatalf("an HONEST roster.Partial assertion must still be named as such: %v", partialErr)
	}
}

var truncatedForCompleteness = []roster.Principal{person(idPersonA), person(idPersonB)}

func assertUnresolvedFault[T any](t *testing.T, res roster.Resolution[T], err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("Resolved returned no error: (empty list, nil error) must not be constructible")
	}
	var fe *roster.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *roster.FaultError — a broken connector must be named, not coerced into a generic error", err)
	}
	if fe.Fault != roster.FaultUnresolved {
		t.Fatalf("Fault = %q, want %q", fe.Fault, roster.FaultUnresolved)
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
	if !strings.Contains(fe.Detail, "EmptyRoster") {
		t.Fatalf("the fault must name the construct the author should have reached for, got: %s", fe.Detail)
	}
	if _, ierr := res.Items(); ierr == nil {
		t.Fatalf("the Resolution returned alongside the fault must not be readable")
	}
}

// TestZeroResolutionIsUnreadable closes the other half of REQ-ARQ-P-17: the
// zero Resolution — what a connector returning `Resolution[Principal]{}, nil`
// produces — cannot be read as a directory of nobody. There is no accessor
// that yields an empty slice from it.
func TestZeroResolutionIsUnreadable(t *testing.T) {
	var zero roster.Resolution[roster.Principal]
	items, err := zero.Items()
	if err == nil {
		t.Fatalf("the zero Resolution must not read as an empty roster")
	}
	if items != nil {
		t.Fatalf("a failed Items() must not hand back a list (%v)", items)
	}
	var fe *roster.FaultError
	if !errors.As(err, &fe) || fe.Fault != roster.FaultUnresolved {
		t.Fatalf("Items() error = %v, want a FaultError of %q", err, roster.FaultUnresolved)
	}
	if cerr.Retryable(err) {
		t.Fatalf("a contract violation must not be retryable: a broken connector does not improve")
	}
	if cerr.TripsBreaker(err) {
		t.Fatalf("a contract violation must not trip a breaker: the fault is in the connector, not in backend load")
	}
}

// TestEmptyRosterIsPresentAndDistinctFromUnresolved is the other side of the
// same requirement: a directory that genuinely holds nobody is expressible,
// but only by SAYING SO. A newly created group with no members is a real state
// and a host must be able to see it — emptiness is asserted, never inferred
// from a length.
func TestEmptyRosterIsPresentAndDistinctFromUnresolved(t *testing.T) {
	res := roster.EmptyRoster[roster.Membership]()
	items, err := res.Items()
	if err != nil {
		t.Fatalf("EmptyRoster().Items() = %v, want a present, empty list", err)
	}
	if len(items) != 0 {
		t.Fatalf("EmptyRoster() list = %d entries, want 0", len(items))
	}

	// The distinction that matters: a deliberate empty reads; an unresolved
	// one does not.
	if _, err := (roster.Resolution[roster.Membership]{}).Items(); err == nil {
		t.Fatalf("unresolved must not read like EmptyRoster")
	}
}

// TestResolutionStringReportsStateAndCountButNeverTheRecords: the rendering is
// for a log, and a Resolution's contents are personal data about identifiable
// people. How many were read is diagnosis; who they are is not, and a host
// that formats a struct containing a Resolution must not thereby write the
// directory into its logs.
func TestResolutionStringReportsStateAndCountButNeverTheRecords(t *testing.T) {
	res, err := roster.Resolved([]roster.Principal{person(idPersonA), person(idPersonB)}, roster.Complete)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	s := res.String()
	if strings.Contains(s, idPersonA) || strings.Contains(s, "@example.invalid") {
		t.Fatalf("String() leaked a directory record: %q", s)
	}
	if !strings.Contains(s, "2") {
		t.Fatalf("String() must report how many were read, got %q", s)
	}
	// %v and %#v both route through the redacting renderers, so a struct that
	// merely CONTAINS a Resolution cannot print the directory either.
	if v := fmt.Sprintf("%v / %#v", res, res); strings.Contains(v, idPersonA) {
		t.Fatalf("formatting leaked a directory record: %q", v)
	}

	var unresolved roster.Resolution[roster.Principal]
	if unresolved.String() == s {
		t.Fatalf("an unresolved Resolution must not render like a resolved one (%q)", unresolved.String())
	}
	if !strings.Contains(unresolved.String(), "UNRESOLVED") {
		t.Fatalf("unresolved renders as %q", unresolved.String())
	}
	if unresolved.GoString() != unresolved.String() {
		t.Fatalf("GoString must redact the same way: %q", unresolved.GoString())
	}
	// One entry is rendered in the singular. A count that reads "1 entries" is
	// cosmetic, but the reason it is pinned is not: the plural branch is where
	// an off-by-one in the renderer would hide.
	one, err := roster.Resolved([]roster.Principal{person(idPersonA)}, roster.Complete)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	if !strings.Contains(one.String(), "1 entry") {
		t.Fatalf("a single entry renders as %q", one.String())
	}
	if !strings.Contains(roster.EmptyRoster[roster.Principal]().String(), "0 entries") {
		t.Fatalf("an asserted-empty roster renders as %q", roster.EmptyRoster[roster.Principal]().String())
	}
}

// TestResolutionIsIsolatedFromTheConnectorsSlice: a connector that reuses,
// truncates or overwrites its own backing array after handing it over must not
// be able to change what the host was given. Without the copy, a connector
// that pooled one slice across reads would retroactively empty a Resolution
// the host had already accepted — which is the unresolved-reads-as-empty
// failure arriving by a different route.
func TestResolutionIsIsolatedFromTheConnectorsSlice(t *testing.T) {
	src := []roster.Principal{person(idPersonA), person(idPersonB)}
	res, err := roster.Resolved(src, roster.Complete)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	src[0] = person("placeholder-someone-else")
	src = src[:0]
	_ = src

	items, err := res.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(items) != 2 || items[0].ID != idPersonA {
		t.Fatalf("the Resolution followed the connector's mutation: %+v", items)
	}
}

// TestItemsHandsOutACopy: a host that sorts, filters or annotates the list in
// place must not change what the next reader of the same Resolution sees. Two
// hosts sharing one Resolution and disagreeing about its contents is the sort
// of bug that is diagnosed as a directory problem.
func TestItemsHandsOutACopy(t *testing.T) {
	res, err := roster.Resolved([]roster.Principal{person(idPersonA), person(idPersonB)}, roster.Complete)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	first, err := res.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	first[0].Active = false
	first[1].ID = "placeholder-overwritten"

	second, err := res.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if !second[0].Active || second[1].ID != idPersonB {
		t.Fatalf("a caller mutated the Resolution through Items(): %+v", second)
	}
}

// TestGroupParentIDsAreDeepCopied is Finding 4's fix: Group.ParentIDs is the
// only slice-valued field across the three Roster types, and a plain
// slices.Clone of []Group only copies each Group by value — the ParentIDs
// slice HEADER, not the backing array it points at. Before the fix, mutating
// ParentIDs through one Items() call was visible to the next Items() call on
// the SAME Resolution, and reached all the way back into the connector's own
// source slice: exactly the aliasing [Resolution.Items]' doc promises does
// not happen.
func TestGroupParentIDsAreDeepCopied(t *testing.T) {
	t.Run("Items is isolated from its own previous read", func(t *testing.T) {
		res, err := roster.Resolved([]roster.Group{{ID: idGroup, ParentIDs: []string{idNestedIn}}}, roster.Complete)
		if err != nil {
			t.Fatalf("Resolved: %v", err)
		}
		first, err := res.Items()
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		first[0].ParentIDs[0] = "placeholder-host-mutated"

		second, err := res.Items()
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		if second[0].ParentIDs[0] != idNestedIn {
			t.Fatalf("mutating ParentIDs from one Items() call changed what the next call sees: %+v", second)
		}
	})

	t.Run("Resolved is isolated from the connector's own slice", func(t *testing.T) {
		src := []roster.Group{{ID: idGroup, ParentIDs: []string{idNestedIn}}}
		res, err := roster.Resolved(src, roster.Complete)
		if err != nil {
			t.Fatalf("Resolved: %v", err)
		}
		src[0].ParentIDs[0] = "placeholder-connector-mutated"

		items, err := res.Items()
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		if items[0].ParentIDs[0] != idNestedIn {
			t.Fatalf("the Resolution followed the connector's mutation of its own ParentIDs slice: %+v", items)
		}
	})
}

// TestResolutionHasNoLengthAccessor is the design decision pinned as a test,
// because it is the one a future contributor will most reasonably want to undo.
//
// A Len() or IsEmpty() that answered 0/true for an UNRESOLVED Resolution puts
// the ambiguity straight back: every caller that branched on it before reading
// would again be guessing which of the two things a zero meant, and the type
// would be decoration. The only way to learn how many entries there are is to
// pass the readability check first.
//
// It is asserted through the exported surface rather than by reading the source
// so that it fails on an added method rather than on a reworded comment.
func TestResolutionHasNoLengthAccessor(t *testing.T) {
	var zero roster.Resolution[roster.Principal]
	// If a Len() or IsEmpty() is ever added, one of these assertions stops
	// compiling and this test must be revisited deliberately rather than
	// silently.
	var _ interface {
		Items() ([]roster.Principal, error)
	} = zero
	if _, ok := any(zero).(interface{ Len() int }); ok {
		t.Fatalf("Resolution grew a Len(): an unresolved roster would then report 0 entries, which is the ambiguity this type removes")
	}
	if _, ok := any(zero).(interface{ IsEmpty() bool }); ok {
		t.Fatalf("Resolution grew an IsEmpty(): an unresolved roster would then report true, which is the ambiguity this type removes")
	}
}
