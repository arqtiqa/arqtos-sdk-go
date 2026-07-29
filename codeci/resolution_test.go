package codeci_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codeci"
)

func pr(id string) codeci.PR {
	return codeci.PR{
		ID:       id,
		FullName: "placeholder-org/placeholder-repo",
		Branch:   "placeholder-branch-" + id,
		Title:    "placeholder title " + id,
		State:    codeci.PRStateOpen,
	}
}

// TestResolvedRefusesAnEmptyList is the point of the mistake for a class
// where the blast radius lands on CI safety: a read that came back with
// nothing because it was unauthenticated, throttled or misdirected has
// nothing to report as a success — a caller deciding "are there any open PRs
// left to review" from that shape draws the wrong conclusion with no way to
// notice.
func TestResolvedRefusesAnEmptyList(t *testing.T) {
	t.Run("nil slice", func(t *testing.T) {
		res, err := codeci.Resolved[codeci.PR](nil, codeci.Complete)
		assertUnresolvedFault(t, res, err)
	})
	t.Run("zero-length slice", func(t *testing.T) {
		res, err := codeci.Resolved([]codeci.PR{}, codeci.Complete)
		assertUnresolvedFault(t, res, err)
	})
	t.Run("zero-length diff-file slice", func(t *testing.T) {
		res, err := codeci.Resolved([]codeci.DiffFile{}, codeci.Complete)
		assertUnresolvedFault(t, res, err)
	})
	// An empty list asserted Partial is unreadable for the SAME reason an
	// empty list asserted Complete is: there is nothing to report as a
	// success either way, and FaultUnresolved is the more specific diagnosis.
	t.Run("zero-length slice asserted Partial", func(t *testing.T) {
		res, err := codeci.Resolved([]codeci.PR{}, codeci.Partial)
		assertUnresolvedFault(t, res, err)
	})
}

// TestResolvedRefusesAPartialAssertion: a truncated-but-non-empty list must
// not be constructible as a readable success. A caller deciding "does this
// ref have any failing checks" from a truncated-but-readable CheckRun list
// would act on an answer it never actually computed.
func TestResolvedRefusesAPartialAssertion(t *testing.T) {
	res, err := codeci.Resolved([]codeci.PR{pr("a")}, codeci.Partial)
	if err == nil {
		t.Fatalf("Resolved(items, codeci.Partial) returned no error: a truncated read must not be readable as a success")
	}
	var fe *codeci.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *codeci.FaultError", err)
	}
	if fe.Fault != codeci.FaultPartial {
		t.Fatalf("Fault = %q, want %q", fe.Fault, codeci.FaultPartial)
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

// TestCompletenessVocabularyIsClosedAndDerivedFromOneSource mirrors roster's
// same test: Valid and String both derive from one map, so a value cannot be
// half-added.
func TestCompletenessVocabularyIsClosedAndDerivedFromOneSource(t *testing.T) {
	for _, c := range []codeci.Completeness{codeci.Complete, codeci.Partial} {
		if !c.Valid() {
			t.Fatalf("%v is a vocabulary member but not Valid", c)
		}
		if name := c.String(); name == "" || strings.HasPrefix(name, "invalid_") {
			t.Fatalf("%d renders as %q, which is not a real name", int(c), name)
		}
	}
	if codeci.Complete.String() == codeci.Partial.String() {
		t.Fatalf("Complete and Partial must not render identically")
	}
}

// TestAnOutOfVocabularyCompletenessCannotClaimToBePartial: an error message
// built from c.String() must name what was ACTUALLY passed, not assume
// Partial for any non-Complete value.
func TestAnOutOfVocabularyCompletenessCannotClaimToBePartial(t *testing.T) {
	bogus := codeci.Completeness(99)
	if bogus.Valid() {
		t.Fatalf("Completeness(99) must not be Valid")
	}
	if got := bogus.String(); got != "invalid_completeness(99)" {
		t.Fatalf("Completeness(99).String() = %q", got)
	}

	const falseCallClaim = "Resolved(items, codeci.Partial)"

	_, err := codeci.Resolved(truncatedForCompleteness, bogus)
	if err == nil {
		t.Fatalf("Resolved must refuse an out-of-vocabulary Completeness")
	}
	if strings.Contains(err.Error(), falseCallClaim) {
		t.Fatalf("the error must not claim the caller wrote Resolved(items, codeci.Partial) when it did not: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_completeness(99)") {
		t.Fatalf("the error must name what was ACTUALLY passed, got: %v", err)
	}

	var zero codeci.Completeness
	if zero.Valid() {
		t.Fatalf("the zero Completeness must not be Valid")
	}
	_, zeroErr := codeci.Resolved(truncatedForCompleteness, zero)
	if zeroErr == nil {
		t.Fatalf("Resolved(items, Completeness(0)) must be refused")
	}
	if strings.Contains(zeroErr.Error(), falseCallClaim) {
		t.Fatalf("Resolved(items, Completeness(0)) must not be reported as Resolved(items, codeci.Partial): %v", zeroErr)
	}

	_, partialErr := codeci.Resolved(truncatedForCompleteness, codeci.Partial)
	if !strings.Contains(partialErr.Error(), falseCallClaim) {
		t.Fatalf("an HONEST codeci.Partial assertion must still be named as such: %v", partialErr)
	}
}

var truncatedForCompleteness = []codeci.PR{pr("a"), pr("b")}

func assertUnresolvedFault[T any](t *testing.T, res codeci.Resolution[T], err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("Resolved returned no error: (empty list, nil error) must not be constructible")
	}
	var fe *codeci.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *codeci.FaultError — a broken connector must be named, not coerced into a generic error", err)
	}
	if fe.Fault != codeci.FaultUnresolved {
		t.Fatalf("Fault = %q, want %q", fe.Fault, codeci.FaultUnresolved)
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
	if !strings.Contains(fe.Detail, "EmptyList") {
		t.Fatalf("the fault must name the construct the author should have reached for, got: %s", fe.Detail)
	}
	if _, ierr := res.Items(); ierr == nil {
		t.Fatalf("the Resolution returned alongside the fault must not be readable")
	}
}

// TestZeroResolutionIsUnreadable: the zero Resolution — what a connector
// returning `Resolution[PR]{}, nil` produces — cannot be read as "no open
// PRs". There is no accessor that yields an empty slice from it.
func TestZeroResolutionIsUnreadable(t *testing.T) {
	var zero codeci.Resolution[codeci.PR]
	items, err := zero.Items()
	if err == nil {
		t.Fatalf("the zero Resolution must not read as an empty list")
	}
	if items != nil {
		t.Fatalf("a failed Items() must not hand back a list (%v)", items)
	}
	var fe *codeci.FaultError
	if !errors.As(err, &fe) || fe.Fault != codeci.FaultUnresolved {
		t.Fatalf("Items() error = %v, want a FaultError of %q", err, codeci.FaultUnresolved)
	}
	if cerr.Retryable(err) {
		t.Fatalf("a contract violation must not be retryable: a broken connector does not improve")
	}
	if cerr.TripsBreaker(err) {
		t.Fatalf("a contract violation must not trip a breaker: the fault is in the connector, not in backend load")
	}
}

// TestEmptyListIsPresentAndDistinctFromUnresolved: a ref with no CI
// configured is a real state, and a host must be able to see it — emptiness
// is asserted, never inferred from a length.
func TestEmptyListIsPresentAndDistinctFromUnresolved(t *testing.T) {
	res := codeci.EmptyList[codeci.CheckRun]()
	items, err := res.Items()
	if err != nil {
		t.Fatalf("EmptyList().Items() = %v, want a present, empty list", err)
	}
	if len(items) != 0 {
		t.Fatalf("EmptyList() list = %d entries, want 0", len(items))
	}

	if _, err := (codeci.Resolution[codeci.CheckRun]{}).Items(); err == nil {
		t.Fatalf("unresolved must not read like EmptyList")
	}
}

// TestResolutionStringReportsStateAndCountButNeverTheEntries: a PR's title or
// body can carry text nobody meant to end up in a log line, so the rendering
// reports the state and the count only.
func TestResolutionStringReportsStateAndCountButNeverTheEntries(t *testing.T) {
	res, err := codeci.Resolved([]codeci.PR{pr("a"), pr("b")}, codeci.Complete)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	s := res.String()
	if strings.Contains(s, "placeholder title a") {
		t.Fatalf("String() leaked an entry: %q", s)
	}
	if !strings.Contains(s, "2") {
		t.Fatalf("String() must report how many were read, got %q", s)
	}
	if v := fmt.Sprintf("%v / %#v", res, res); strings.Contains(v, "placeholder title a") {
		t.Fatalf("formatting leaked an entry: %q", v)
	}

	var unresolved codeci.Resolution[codeci.PR]
	if unresolved.String() == s {
		t.Fatalf("an unresolved Resolution must not render like a resolved one (%q)", unresolved.String())
	}
	if !strings.Contains(unresolved.String(), "UNRESOLVED") {
		t.Fatalf("unresolved renders as %q", unresolved.String())
	}
	if unresolved.GoString() != unresolved.String() {
		t.Fatalf("GoString must redact the same way: %q", unresolved.GoString())
	}

	one, err := codeci.Resolved([]codeci.PR{pr("a")}, codeci.Complete)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	if !strings.Contains(one.String(), "1 entry") {
		t.Fatalf("a single entry renders as %q", one.String())
	}
	if !strings.Contains(codeci.EmptyList[codeci.PR]().String(), "0 entries") {
		t.Fatalf("an asserted-empty list renders as %q", codeci.EmptyList[codeci.PR]().String())
	}
}

// TestResolutionIsIsolatedFromTheConnectorsSlice: a connector that reuses or
// truncates its own backing array after handing it over must not be able to
// change what the host was given.
func TestResolutionIsIsolatedFromTheConnectorsSlice(t *testing.T) {
	src := []codeci.PR{pr("a"), pr("b")}
	res, err := codeci.Resolved(src, codeci.Complete)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	src[0] = pr("mutated")
	src = src[:0]
	_ = src

	items, err := res.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(items) != 2 || items[0].ID != "a" {
		t.Fatalf("the Resolution followed the connector's mutation: %+v", items)
	}
}

// TestItemsHandsOutACopy: a host that sorts, filters or annotates the list in
// place must not change what the next reader of the same Resolution sees.
func TestItemsHandsOutACopy(t *testing.T) {
	res, err := codeci.Resolved([]codeci.PR{pr("a"), pr("b")}, codeci.Complete)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	first, err := res.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	first[0].Draft = true
	first[1].ID = "overwritten"

	second, err := res.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if second[0].Draft || second[1].ID != "b" {
		t.Fatalf("a caller mutated the Resolution through Items(): %+v", second)
	}
}

// TestResolutionHasNoLengthAccessor pins the design decision: a Len() or
// IsEmpty() that answered 0/true for an UNRESOLVED Resolution would put the
// ambiguity straight back.
func TestResolutionHasNoLengthAccessor(t *testing.T) {
	var zero codeci.Resolution[codeci.PR]
	var _ interface {
		Items() ([]codeci.PR, error)
	} = zero
	if _, ok := any(zero).(interface{ Len() int }); ok {
		t.Fatalf("Resolution grew a Len(): an unresolved result would then report 0 entries, which is the ambiguity this type removes")
	}
	if _, ok := any(zero).(interface{ IsEmpty() bool }); ok {
		t.Fatalf("Resolution grew an IsEmpty(): an unresolved result would then report true, which is the ambiguity this type removes")
	}
}
