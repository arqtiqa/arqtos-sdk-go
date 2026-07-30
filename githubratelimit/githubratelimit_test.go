package githubratelimit_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/githubratelimit"
)

// TestMechanismVocabularyIsClosed pins the mechanism vocabulary by name, the
// same way cerr's Kind vocabulary is pinned. The point of the package is that
// the three limits get three different responses, so a fourth mechanism added
// without a deliberate decision — or a third quietly merged into another — is
// the regression that matters most here, and it is invisible without this.
func TestMechanismVocabularyIsClosed(t *testing.T) {
	want := map[githubratelimit.Mechanism]string{
		githubratelimit.MechanismNone:        "none",
		githubratelimit.MechanismPrimary:     "primary",
		githubratelimit.MechanismSecondary:   "secondary",
		githubratelimit.MechanismGraphQLCost: "graphql_cost",
	}

	got := githubratelimit.Mechanisms()
	if len(got) != len(want) {
		t.Fatalf("Mechanisms() has %d entries, the pinned vocabulary has %d: %v", len(got), len(want), got)
	}
	for _, m := range got {
		name, ok := want[m]
		if !ok {
			t.Fatalf("Mechanisms() contains %v, which is not in the pinned vocabulary", m)
		}
		if m.String() != name {
			t.Fatalf("Mechanism %d renders %q, pinned as %q", int(m), m.String(), name)
		}
		if !m.Valid() {
			t.Fatalf("%v is in Mechanisms() but reports Valid() = false", m)
		}
	}
}

// TestMechanismsCopyCannotNarrowTheVocabulary proves the returned slice is a
// copy: a caller that mutates it must not be able to change what every other
// caller sees the vocabulary to be.
func TestMechanismsCopyCannotNarrowTheVocabulary(t *testing.T) {
	got := githubratelimit.Mechanisms()
	got[0] = githubratelimit.Mechanism(9999)
	again := githubratelimit.Mechanisms()
	if again[0] != githubratelimit.MechanismNone {
		t.Fatalf("mutating the returned slice changed the vocabulary: got %v", again)
	}
}

// TestInvalidMechanismDoesNotRenderAsNone is the same guard cerr.Kind carries: a
// value outside the vocabulary must not hide behind the zero value's name in a
// log line, because "none" is exactly the reading that would send an operator
// looking for a non-existent bug elsewhere.
func TestInvalidMechanismDoesNotRenderAsNone(t *testing.T) {
	m := githubratelimit.Mechanism(42)
	if m.Valid() {
		t.Fatalf("Mechanism(42) must not be Valid")
	}
	if got := m.String(); got != "invalid_mechanism(42)" {
		t.Fatalf("Mechanism(42).String() = %q, want invalid_mechanism(42)", got)
	}
	if m.Limited() {
		t.Fatalf("a mechanism outside the vocabulary must not report Limited()")
	}
}

func TestLimitedIsEveryMechanismButNone(t *testing.T) {
	if githubratelimit.MechanismNone.Limited() {
		t.Fatalf("MechanismNone must not report Limited()")
	}
	for _, m := range githubratelimit.Mechanisms() {
		if m == githubratelimit.MechanismNone {
			continue
		}
		if !m.Limited() {
			t.Fatalf("%v must report Limited()", m)
		}
	}
}

// TestErrorClassifiesAsRateLimitedAndTripsTheBreaker is the interop test with
// the cerr taxonomy: host code that only classifies must get the right answers
// off this package's error without knowing the type exists. In particular the
// breaker MUST open — the backend itself reported the refusal, which is the
// positive evidence cerr.TripsBreaker is defined on.
func TestErrorClassifiesAsRateLimitedAndTripsTheBreaker(t *testing.T) {
	reset := time.Date(2026, 7, 30, 14, 32, 0, 0, time.UTC)
	cause := errors.New("wait abandoned")
	e := &githubratelimit.Error{
		Op:        "ListProjectItems",
		Mechanism: githubratelimit.MechanismSecondary,
		Attempts:  3,
		RetryAt:   reset,
		Err:       cause,
	}

	if got := cerr.KindOf(e); got != cerr.KindRateLimited {
		t.Fatalf("KindOf = %v, want KindRateLimited — a refusal a host cannot classify is a breaker that never opens", got)
	}
	if !cerr.Classified(e) {
		t.Fatalf("Error must carry a Kind from the closed vocabulary")
	}
	if !cerr.TripsBreaker(e) {
		t.Fatalf("a rate-limit refusal must trip the host breaker")
	}
	if cerr.Retryable(e) {
		t.Fatalf("KindRateLimited is deliberately not Retryable: a rate limit is not waited out by retrying into it")
	}
	if !errors.Is(e, cause) {
		t.Fatalf("the underlying cause must stay reachable through Unwrap, or a cancelled wait cannot be told from a refused one")
	}
}

// TestErrorMessageNamesMechanismAttemptsAndResetTime is the "surface the reset
// time rather than a bare failure" requirement. "rate limited" alone does not
// tell an operator whether to wait or to reduce concurrency — two opposite
// remedies for two mechanisms that look identical from outside.
func TestErrorMessageNamesMechanismAttemptsAndResetTime(t *testing.T) {
	reset := time.Date(2026, 7, 30, 14, 32, 0, 0, time.UTC)
	e := &githubratelimit.Error{
		Op:        "ListProjectItems",
		Mechanism: githubratelimit.MechanismPrimary,
		Attempts:  4,
		RetryAt:   reset,
	}
	msg := e.Error()
	for _, want := range []string{"ListProjectItems", "primary", "4 attempt", "2026-07-30T14:32:00Z"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("Error() = %q, missing %q", msg, want)
		}
	}
}

// TestErrorWithNoResetTimeSaysSoRatherThanInventingOne — a secondary limit with
// no retry-after genuinely does not say when it ends, and rendering the zero
// Time as a timestamp would put "0001-01-01" in front of an operator as if it
// were information.
func TestErrorWithNoResetTimeSaysSoRatherThanInventingOne(t *testing.T) {
	e := &githubratelimit.Error{Mechanism: githubratelimit.MechanismSecondary, Attempts: 5}
	msg := e.Error()
	if !strings.Contains(msg, "named no reset time") {
		t.Fatalf("Error() = %q, want it to state that no reset time was given", msg)
	}
	if strings.Contains(msg, "0001-01-01") {
		t.Fatalf("Error() = %q, rendered the zero Time as if it were a reset time", msg)
	}
	if !strings.Contains(msg, "a github request") {
		t.Fatalf("Error() = %q, want a stand-in for the unnamed op", msg)
	}
}

// TestSystemClockSleepIsCancellable covers the one behaviour a fake clock cannot
// stand in for: that the real sleep abandons its wait when the context ends.
// Without it a host with a deadline blocks for the remainder of a full hour.
//
// It asserts the RETURNED ERROR, not how long the call took — the distinction
// this package's tests hold to throughout.
func TestSystemClockSleepIsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := githubratelimit.SystemClock{}.Sleep(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep returned %v, want context.Canceled — an uncancellable wait blocks a host for the whole window", err)
	}
}

// TestSystemClockSleepOfNothingIgnoresACancelledContext — there was nothing to
// interrupt, so reporting an interruption would make a no-op wait fail.
func TestSystemClockSleepOfNothingIgnoresACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (githubratelimit.SystemClock{}).Sleep(ctx, 0); err != nil {
		t.Fatalf("Sleep(ctx, 0) = %v, want nil", err)
	}
	if got := (githubratelimit.SystemClock{}).Now(); got.IsZero() {
		t.Fatalf("SystemClock.Now() returned the zero time")
	}
}
