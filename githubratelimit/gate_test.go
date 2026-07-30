// Nothing in this file waits.
//
// Every Gate under test is built on fakeClock, whose Sleep records the duration
// it was asked for and advances a counter instead of a thread. So the assertions
// are on the COMPUTED delay and the NUMBER of attempts, both exact, and never on
// how long anything took. A test that asserted `elapsed < 50ms` would be
// asserting a property of the machine, and under CI's -race build with coverage
// instrumentation that machine is several times slower than the one any such
// threshold gets chosen on: those tests do not fail honestly, they flake.
//
// TestNoWallClockAssertionsInThisPackagesTests enforces it rather than trusting
// it.
package githubratelimit_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/githubratelimit"
)

// fakeClock is the time seam. Sleep does not sleep: it records the duration and
// moves the clock forward, so a fifteen-minute wait costs nothing to test.
type fakeClock struct {
	mu       sync.Mutex
	now      time.Time
	slept    []time.Duration
	failAt   int   // the 1-based Sleep call that returns failWith, 0 for none
	failWith error // what that call returns
}

func newFakeClock() *fakeClock { return &fakeClock{now: classifyNow} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	if c.failWith != nil && len(c.slept) == c.failAt {
		return c.failWith
	}
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.slept...)
}

// randSeq returns the given values in order, repeating the last one once the
// sequence is spent, so a backoff assertion is exact rather than statistical.
func randSeq(values ...float64) func() float64 {
	i := 0
	return func() float64 {
		v := values[i]
		if i < len(values)-1 {
			i++
		}
		return v
	}
}

// observations replays a fixed sequence of responses, recording how many
// attempts were actually made.
type observations struct {
	items []githubratelimit.Observation
	calls int
}

func (o *observations) next() githubratelimit.Observation {
	obs := o.items[min(o.calls, len(o.items)-1)]
	o.calls++
	return obs
}

func primaryRefusal() githubratelimit.Observation {
	return githubratelimit.Observation{
		Status: http.StatusForbidden,
		Header: primaryHeader(0, 5000, 5000, resetUnix, "core"),
		Body:   []byte(primaryRefusalBody),
	}
}

func secondaryRefusal(retryAfter string) githubratelimit.Observation {
	return githubratelimit.Observation{
		Status: http.StatusForbidden,
		Header: secondaryHeader(retryAfter),
		Body:   []byte(secondaryRefusalBody),
	}
}

func healthy() githubratelimit.Observation {
	return githubratelimit.Observation{
		Status: http.StatusOK,
		Header: primaryHeader(4000, 5000, 1000, resetUnix, "core"),
		Body:   []byte(`{"ok":true}`),
	}
}

func TestNewFillsEveryDefault(t *testing.T) {
	g := githubratelimit.New(githubratelimit.Options{})
	if got := g.Attempts(); got != githubratelimit.DefaultAttempts {
		t.Fatalf("Attempts() = %d, want %d", got, githubratelimit.DefaultAttempts)
	}
	if got := g.Resource(); got != githubratelimit.ResourceCore {
		t.Fatalf("Resource() = %q, want %q", got, githubratelimit.ResourceCore)
	}
	if g.Primary().Known() || g.Points().Known() {
		t.Fatalf("a fresh gate must hold no measured budget")
	}
	// The default jitter must actually be applied, so an unset field cannot
	// silently mean "no jitter".
	if got := g.Backoff(1); got >= githubratelimit.DefaultBase {
		t.Fatalf("Backoff(1) = %v; with the default jitter it must fall below the base of %v at least sometimes",
			got, githubratelimit.DefaultBase)
	}
}

// TestBackoffIsExponentialCappedAndJittered asserts the COMPUTED delay for a
// pinned rand sequence. Every figure here is arithmetic, not a measurement.
func TestBackoffIsExponentialCappedAndJittered(t *testing.T) {
	g := githubratelimit.New(githubratelimit.Options{
		Clock:    newFakeClock(),
		Base:     time.Second,
		MaxDelay: 8 * time.Second,
		Jitter:   0.5,
		Rand:     randSeq(0), // the floor of each jitter window
	})
	// floor = d/2 for jitter 0.5, and d doubles until it hits the 8s cap.
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	for i, w := range want {
		if got := g.Backoff(i + 1); got != w {
			t.Fatalf("Backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

// TestJitterMakesTwoWorkersWaitDifferentAmounts is the acceptance criterion
// stated as a test. Fixed backoff makes every worker refused in the same instant
// retry in the same instant, which is the burst the secondary limit refused them
// for — the herd is worse than the original limit and re-forms on every retry.
func TestJitterMakesTwoWorkersWaitDifferentAmounts(t *testing.T) {
	opts := func(r float64) githubratelimit.Options {
		return githubratelimit.Options{
			Clock: newFakeClock(), Base: time.Second, MaxDelay: time.Minute,
			Jitter: 0.5, Rand: randSeq(r),
		}
	}
	first := githubratelimit.New(opts(0.0)).Backoff(3)
	second := githubratelimit.New(opts(0.99)).Backoff(3)
	if first == second {
		t.Fatalf("two workers computed the same delay (%v) for the same attempt — that is the thundering herd", first)
	}
	// Both must still land inside the jitter window [d/2, d] for attempt 3,
	// where the uncapped d is 4s.
	for _, d := range []time.Duration{first, second} {
		if d < 2*time.Second || d > 4*time.Second {
			t.Fatalf("jittered delay %v is outside the window [2s, 4s]", d)
		}
	}
}

// TestJitterIsOnlyTurnedOffDeliberately. Zero means "unset", and an unset field
// must not switch off the defence against a thundering herd.
func TestJitterIsOnlyTurnedOffDeliberately(t *testing.T) {
	unset := githubratelimit.New(githubratelimit.Options{
		Base: time.Second, MaxDelay: time.Minute, Rand: randSeq(0),
	})
	if got := unset.Backoff(1); got != 500*time.Millisecond {
		t.Fatalf("Backoff(1) with Jitter unset = %v, want 500ms (the default 0.5 jitter applied)", got)
	}
	off := githubratelimit.New(githubratelimit.Options{
		Base: time.Second, MaxDelay: time.Minute, Jitter: -1, Rand: randSeq(0),
	})
	if got := off.Backoff(1); got != time.Second {
		t.Fatalf("Backoff(1) with Jitter deliberately negative = %v, want the un-jittered 1s", got)
	}
}

// TestBackoffIsAlwaysPositiveAndNeverExceedsTheCap sweeps far past the point
// where a naive shift overflows an int64 duration into a negative one — a
// negative delay would make a wait run backwards.
func TestBackoffIsAlwaysPositiveAndNeverExceedsTheCap(t *testing.T) {
	g := githubratelimit.New(githubratelimit.Options{
		Base: time.Second, MaxDelay: 30 * time.Second, Jitter: 0.5, Rand: randSeq(0.5),
	})
	for _, attempt := range []int{-5, 0, 1, 2, 10, 62, 63, 64, 1000} {
		got := g.Backoff(attempt)
		if got <= 0 {
			t.Fatalf("Backoff(%d) = %v, must be positive", attempt, got)
		}
		if got > 30*time.Second {
			t.Fatalf("Backoff(%d) = %v, exceeds the 30s cap", attempt, got)
		}
	}
	if g.Backoff(-5) != g.Backoff(1) {
		t.Fatalf("an attempt below 1 must be treated as 1")
	}
}

// TestBackoffIsNeverZeroAtAnyJitter is the trap DefaultJitter hides.
//
// Full jitter — Options.Jitter of 1, an in-range documented value and a
// well-known strategy — has a window floor of ZERO by definition, so an unlucky
// draw computes a delay of nothing. A backoff of nothing is a tight retry
// straight back into the limit that just refused the request, which is the one
// outcome this package must not produce. The earlier revision of the test that
// claimed to assert positivity only ever ran jitter 0.5, where the floor is
// d/2 and zero is unreachable.
func TestBackoffIsNeverZeroAtAnyJitter(t *testing.T) {
	for _, jitter := range []float64{-1, 0, 0.01, 0.5, 0.99, 1, 1.5} {
		for _, r := range []float64{0, 0.0000001, 0.5, 0.9999999} {
			g := githubratelimit.New(githubratelimit.Options{
				Base: time.Second, MaxDelay: time.Minute, Jitter: jitter, Rand: randSeq(r),
			})
			for _, attempt := range []int{1, 2, 5, 40} {
				got := g.Backoff(attempt)
				if got <= 0 {
					t.Fatalf("Backoff(%d) with jitter %v and rand %v = %v; a backoff of nothing retries into the limit immediately",
						attempt, jitter, r, got)
				}
				if got < githubratelimit.MinBackoff {
					t.Fatalf("Backoff(%d) with jitter %v and rand %v = %v, below MinBackoff (%v)",
						attempt, jitter, r, got, githubratelimit.MinBackoff)
				}
				if got > time.Minute {
					t.Fatalf("Backoff(%d) with jitter %v and rand %v = %v, above the cap", attempt, jitter, r, got)
				}
			}
		}
	}
}

// TestBackoffClampsARandThatBreaksItsContract — a value outside [0,1) would
// compute a delay past the cap, or a negative one.
func TestBackoffClampsARandThatBreaksItsContract(t *testing.T) {
	low := githubratelimit.New(githubratelimit.Options{
		Base: time.Second, MaxDelay: time.Minute, Jitter: 0.5, Rand: randSeq(-3),
	})
	if got := low.Backoff(1); got != 500*time.Millisecond {
		t.Fatalf("Backoff with a negative rand = %v, want the window floor of 500ms", got)
	}
	high := githubratelimit.New(githubratelimit.Options{
		Base: time.Second, MaxDelay: time.Minute, Jitter: 0.5, Rand: randSeq(7),
	})
	if got := high.Backoff(1); got != time.Second {
		t.Fatalf("Backoff with a rand above 1 = %v, want the window ceiling of 1s", got)
	}
}

// TestAZeroGateDefaultsItselfRatherThanReportingAPhantomLimit. Gate is an
// exported struct, so `var g Gate` and `Gate{}` compile for any third party and
// Go has no way to forbid them.
//
// Left undefaulted, that value is dangerous in two ways, and the second is worse
// than a panic: its Clock is nil, so Observe dereferences nothing; and its
// attempt count is zero, so Do's loop runs NO attempt and then reports a rate
// limit — mechanism "none", zero attempts — for a request nobody sent, which
// classifies as KindRateLimited and opens the host's circuit breaker.
//
// It uses the real clock deliberately: every response here is healthy, so
// nothing waits, and that is the point — a zero gate must be usable, not merely
// non-panicking.
func TestAZeroGateDefaultsItselfRatherThanReportingAPhantomLimit(t *testing.T) {
	var g githubratelimit.Gate

	if got := g.Attempts(); got != githubratelimit.DefaultAttempts {
		t.Fatalf("Attempts() on a zero Gate = %d, want the default %d", got, githubratelimit.DefaultAttempts)
	}
	if got := g.Resource(); got != githubratelimit.ResourceCore {
		t.Fatalf("Resource() on a zero Gate = %q, want %q", got, githubratelimit.ResourceCore)
	}
	if got := g.Backoff(1); got <= 0 {
		t.Fatalf("Backoff(1) on a zero Gate = %v, want a positive delay", got)
	}
	// Would have panicked on the nil clock.
	if v := g.Observe(healthy()); v.Limited() {
		t.Fatalf("Observe on a zero Gate reported %v for a healthy response", v.Mechanism)
	}
	if !g.Primary().Known() {
		t.Fatalf("a zero Gate must still record the budget it observed")
	}
	if err := g.Admit(context.Background(), 1); err != nil {
		t.Fatalf("Admit on a zero Gate = %v, want nil", err)
	}

	calls := 0
	got, err := githubratelimit.Do(context.Background(), &githubratelimit.Gate{}, "Probe",
		func(context.Context) (int, githubratelimit.Observation, error) {
			calls++
			return 7, healthy(), nil
		})
	if err != nil {
		t.Fatalf("Do on a zero Gate = %v, want nil — a rate-limit error for a request never made trips the host breaker on nothing", err)
	}
	if calls != 1 {
		t.Fatalf("the attempt ran %d time(s), want 1", calls)
	}
	if got != 7 {
		t.Fatalf("value = %d, want 7", got)
	}
}

// TestALateResponseCannotResurrectASpentBudget. Within one window GitHub's
// remaining only goes down, but replies to concurrent requests do not arrive in
// the order they were sent — so a reply to an EARLIER request can land after one
// reporting a lower figure. Recording it verbatim would raise the recorded
// remaining back up and wave the next request through into a refusal the gate
// already had the evidence to avoid.
func TestALateResponseCannotResurrectASpentBudget(t *testing.T) {
	at := func(remaining int, reset time.Time) githubratelimit.Observation {
		return githubratelimit.Observation{
			Status: http.StatusOK,
			Header: primaryHeader(remaining, 5000, 5000-remaining, reset, "core"),
			Body:   []byte(`{}`),
		}
	}
	nextWindow := resetUnix.Add(time.Hour)

	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk})

	g.Observe(at(3, resetUnix))
	g.Observe(at(9, resetUnix)) // the late reply, same window
	if got := g.Primary().Remaining; got != 3 {
		t.Fatalf("Remaining = %d, want 3 — a late reply must not raise the recorded budget", got)
	}
	g.Observe(at(0, resetUnix))
	if !g.Primary().Exhausted() {
		t.Fatalf("a lower figure in the same window must still be recorded")
	}
	// A genuinely new window replaces whatever is held, however healthy.
	g.Observe(at(5000, nextWindow))
	if got := g.Primary(); got.Remaining != 5000 || !got.Reset.Equal(nextWindow) {
		t.Fatalf("budget = %+v, want the new window's 5000", got)
	}
	// And a reply from the window that is over is ignored entirely.
	g.Observe(at(1, resetUnix))
	if got := g.Primary(); got.Remaining != 5000 || !got.Reset.Equal(nextWindow) {
		t.Fatalf("budget = %+v, want the new window untouched by a reply from the old one", got)
	}
}

func TestALateResponseCannotResurrectASpentPointBudget(t *testing.T) {
	points := func(remaining int, resetAt time.Time) githubratelimit.Observation {
		return githubratelimit.Observation{
			Status: http.StatusOK,
			Points: githubratelimit.NewPointBudget(githubratelimit.PointBudget{
				Limit: 5000, Remaining: remaining, Used: 5000 - remaining, ResetAt: resetAt,
			}),
		}
	}
	nextWindow := pointsResetAt.Add(time.Hour)

	g := githubratelimit.New(githubratelimit.Options{Clock: newFakeClock()})
	g.Observe(points(40, pointsResetAt))
	g.Observe(points(900, pointsResetAt))
	if got := g.Points().Remaining; got != 40 {
		t.Fatalf("Remaining = %d, want 40 — a late reply must not raise the recorded point budget", got)
	}
	g.Observe(points(5000, nextWindow))
	if got := g.Points(); got.Remaining != 5000 || !got.ResetAt.Equal(nextWindow) {
		t.Fatalf("point budget = %+v, want the new window's 5000", got)
	}
	g.Observe(points(1, pointsResetAt))
	if got := g.Points().Remaining; got != 5000 {
		t.Fatalf("Remaining = %d, want the new window untouched by a reply from the old one", got)
	}
}

func TestObserveRecordsTheBudgetOnASuccessfulResponse(t *testing.T) {
	g := githubratelimit.New(githubratelimit.Options{Clock: newFakeClock()})
	v := g.Observe(healthy())
	if v.Limited() {
		t.Fatalf("a healthy response is not a refusal")
	}
	if got := g.Primary(); !got.Known() || got.Remaining != 4000 {
		t.Fatalf("Primary() = %+v, want the budget off the successful response — that is what makes pre-emption possible at all", got)
	}
}

// TestObserveDoesNotRecordFromAForeignBucket. Search is 30 a minute against
// core's 5000 an hour; folding them into one number stalls every core request
// for the rest of the hour the first time a search is refused. The verdict still
// carries it, so a host wiring one gate where it needs two can see the mismatch.
func TestObserveDoesNotRecordFromAForeignBucket(t *testing.T) {
	g := githubratelimit.New(githubratelimit.Options{Clock: newFakeClock()})
	v := g.Observe(githubratelimit.Observation{
		Status: http.StatusForbidden,
		Header: primaryHeader(0, 30, 30, resetUnix, "search"),
		Body:   []byte(primaryRefusalBody),
	})
	if !v.Primary.Known() || v.Primary.Resource != "search" {
		t.Fatalf("the verdict must still report the foreign budget, got %+v", v.Primary)
	}
	if g.Primary().Known() {
		t.Fatalf("a search budget must not be recorded as the core gate's state")
	}
}

func TestObserveRecordsAnUnlabelledBudget(t *testing.T) {
	g := githubratelimit.New(githubratelimit.Options{Clock: newFakeClock()})
	g.Observe(githubratelimit.Observation{
		Status: http.StatusOK,
		Header: primaryHeader(4000, 5000, 1000, resetUnix, ""),
		Body:   []byte(`{}`),
	})
	if !g.Primary().Known() {
		t.Fatalf("an unlabelled budget must be recorded, or a server that omits the resource header turns pre-emption off entirely")
	}
}

func TestObserveRecordsThePointBudget(t *testing.T) {
	g := githubratelimit.New(githubratelimit.Options{Clock: newFakeClock()})
	g.Observe(githubratelimit.Observation{Status: http.StatusOK, Body: []byte(graphQLBudgetBody)})
	got := g.Points()
	if !got.Known() || got.Remaining != 1200 || got.QueryCost != 47 {
		t.Fatalf("Points() = %+v, want the budget off the body", got)
	}
}

func TestAdmitAdmitsWhenNothingIsMeasuredOrThereIsRoom(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk, Reserve: 10})

	if err := g.Admit(context.Background(), 100); err != nil {
		t.Fatalf("Admit against an unmeasured budget = %v, want nil: pre-emptively blocking on no evidence stalls a host forever", err)
	}
	g.Observe(githubratelimit.Observation{
		Status: http.StatusOK,
		Header: primaryHeader(50, 5000, 4950, resetUnix, "core"),
		Body:   []byte(`{}`),
	})
	if err := g.Admit(context.Background(), 40); err != nil {
		t.Fatalf("Admit(40) against 50 remaining with 10 reserved = %v, want nil", err)
	}
	for _, n := range []int{0, -1} {
		if err := g.Admit(context.Background(), n); err != nil {
			t.Fatalf("Admit(%d) = %v, want nil: nothing was asked for", n, err)
		}
	}
	if got := clk.sleeps(); len(got) != 0 {
		t.Fatalf("nothing should have waited, slept %v", got)
	}
}

// TestAdmitWaitsForTheResetThenForgetsTheSpentBudget. Forgetting matters: the
// recorded numbers describe a window that is over, and a gate that kept them
// would wait a second time on a budget that has already rolled over.
func TestAdmitWaitsForTheResetThenForgetsTheSpentBudget(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk})
	g.Observe(primaryRefusal())
	if !g.Primary().Exhausted() {
		t.Fatalf("the fixture must leave an exhausted budget recorded")
	}

	if err := g.Admit(context.Background(), 1); err != nil {
		t.Fatalf("Admit = %v, want nil after waiting out the window", err)
	}
	if got := clk.sleeps(); len(got) != 1 || got[0] != 15*time.Minute {
		t.Fatalf("slept %v, want exactly one wait of 15m — the remainder of the window", got)
	}
	if g.Primary().Known() {
		t.Fatalf("the spent budget must be forgotten once its window is over")
	}
	if err := g.Admit(context.Background(), 1); err != nil {
		t.Fatalf("the second Admit = %v, want nil", err)
	}
	if got := clk.sleeps(); len(got) != 1 {
		t.Fatalf("slept %v, want no second wait", got)
	}
}

func TestAdmitAdmitsImmediatelyWhenTheWindowHasAlreadyPassed(t *testing.T) {
	clk := newFakeClock()
	clk.now = resetUnix.Add(time.Hour) // well past the fixture's reset
	g := githubratelimit.New(githubratelimit.Options{Clock: clk})
	g.Observe(primaryRefusal())

	if err := g.Admit(context.Background(), 1); err != nil {
		t.Fatalf("Admit = %v, want nil", err)
	}
	if got := clk.sleeps(); len(got) != 0 {
		t.Fatalf("slept %v, want nothing: the window the budget describes is over", got)
	}
	if g.Primary().Known() {
		t.Fatalf("a budget describing a finished window must be forgotten")
	}
}

// TestAdmitRefusesASequenceLargerThanTheWholeQuotaWithoutWaiting — no amount of
// waiting makes it fit, and a wait that can never succeed is a hang with extra
// steps. It is a caller error, so cerr.KindInvalid, not a rate-limit refusal.
func TestAdmitRefusesASequenceLargerThanTheWholeQuotaWithoutWaiting(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk, Reserve: 100})
	g.Observe(primaryRefusal()) // limit 5000

	err := g.Admit(context.Background(), 4901)
	if err == nil {
		t.Fatalf("Admit(4901) against a 5000 quota with 100 reserved must fail")
	}
	if got := cerr.KindOf(err); got != cerr.KindInvalid {
		t.Fatalf("KindOf = %v, want KindInvalid: this is the caller's arithmetic, not the backend refusing load", got)
	}
	if cerr.TripsBreaker(err) {
		t.Fatalf("a caller error must not open the host's breaker")
	}
	if got := clk.sleeps(); len(got) != 0 {
		t.Fatalf("slept %v, want nothing — waiting can never make it fit", got)
	}
	// One request below the ceiling is admitted, after the wait.
	if err := g.Admit(context.Background(), 4900); err != nil {
		t.Fatalf("Admit(4900) = %v, want nil", err)
	}
}

// TestAdmitIsTheNeverHalfAppliedGuarantee is the acceptance criterion, stated
// exactly: a multi-step mutation interrupted by a rate limit must either
// COMPLETE after waiting or leave the remote unchanged. This gate guarantees the
// first, and buys it by not letting the sequence start until the whole of it
// fits — so the wait happens with ZERO mutations applied.
func TestAdmitIsTheNeverHalfAppliedGuarantee(t *testing.T) {
	const steps = 40
	clk := newFakeClock()
	var applied int
	var appliedWhenWaiting []int
	var announced []githubratelimit.Wait

	g := githubratelimit.New(githubratelimit.Options{
		Clock: clk,
		Notify: func(w githubratelimit.Wait) {
			appliedWhenWaiting = append(appliedWhenWaiting, applied)
			announced = append(announced, w)
		},
	})
	// A budget with room for 12 of the 40 mutations: the shape that half-applies.
	g.Observe(githubratelimit.Observation{
		Status: http.StatusOK,
		Header: primaryHeader(12, 5000, 4988, resetUnix, "core"),
		Body:   []byte(`{}`),
	})

	if err := g.Admit(context.Background(), steps); err != nil {
		t.Fatalf("Admit(%d) = %v, want nil after waiting out the window", steps, err)
	}
	for range steps {
		applied++
	}

	if applied != steps {
		t.Fatalf("applied %d of %d mutations — the sequence did not complete", applied, steps)
	}
	if len(appliedWhenWaiting) != 1 {
		t.Fatalf("waits announced: %d, want exactly 1", len(appliedWhenWaiting))
	}
	if appliedWhenWaiting[0] != 0 {
		t.Fatalf("the wait happened with %d mutations already applied; the whole point is that it happens with none",
			appliedWhenWaiting[0])
	}
	if got := clk.sleeps(); len(got) != 1 || got[0] != 15*time.Minute {
		t.Fatalf("slept %v, want one wait of 15m", got)
	}
	// A pre-emptive wait belongs to no attempt loop, so it reports neither an
	// attempt nor a total. "attempt 0 of 5" would have an operator looking for
	// four retries that are not going to happen.
	w := announced[0]
	if w.Attempt != 0 || w.Attempts != 0 {
		t.Fatalf("pre-emptive wait reported attempt %d of %d, want 0 of 0", w.Attempt, w.Attempts)
	}
	if w.Mechanism != githubratelimit.MechanismPrimary || !w.Dictated || w.Op != "" {
		t.Fatalf("pre-emptive wait = %+v, want the primary mechanism, a dictated wait, and no op", w)
	}
	if !w.Until.Equal(resetUnix) {
		t.Fatalf("Until = %v, want the window's reset %v", w.Until, resetUnix)
	}
}

// TestAdmitReportsACancelledWaitAsATypedRefusalThatNamesTheReset — a host with a
// deadline gets a typed refusal saying when the limit clears, not a bare context
// error and not a partially-run sequence. The context IS the ceiling on the wait.
func TestAdmitReportsACancelledWaitAsATypedRefusalThatNamesTheReset(t *testing.T) {
	clk := newFakeClock()
	clk.failAt, clk.failWith = 1, context.DeadlineExceeded
	g := githubratelimit.New(githubratelimit.Options{Clock: clk})
	g.Observe(primaryRefusal())

	err := g.Admit(context.Background(), 1)
	if err == nil {
		t.Fatalf("Admit must fail when its wait is abandoned")
	}
	var rlErr *githubratelimit.Error
	if !errors.As(err, &rlErr) {
		t.Fatalf("err = %T (%v), want *githubratelimit.Error", err, err)
	}
	if rlErr.Mechanism != githubratelimit.MechanismPrimary {
		t.Fatalf("Mechanism = %v, want primary", rlErr.Mechanism)
	}
	if !rlErr.RetryAt.Equal(resetUnix) {
		t.Fatalf("RetryAt = %v, want the window's reset %v", rlErr.RetryAt, resetUnix)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the cause must stay reachable so a caller can tell a deadline from a refusal")
	}
	if got := cerr.KindOf(err); got != cerr.KindRateLimited {
		t.Fatalf("KindOf = %v, want KindRateLimited", got)
	}
	// An abandoned wait learned nothing, so the budget must NOT be discarded:
	// it is the evidence the next Admit needs.
	if !g.Primary().Known() {
		t.Fatalf("an abandoned wait must not throw away the measured budget")
	}
}

func TestAdmitPointsMirrorsAdmitAgainstThePointBudget(t *testing.T) {
	ctx := context.Background()

	t.Run("unmeasured admits", func(t *testing.T) {
		clk := newFakeClock()
		g := githubratelimit.New(githubratelimit.Options{Clock: clk})
		if err := g.AdmitPoints(ctx, 900); err != nil {
			t.Fatalf("AdmitPoints against an unmeasured budget = %v, want nil", err)
		}
		for _, n := range []int{0, -5} {
			if err := g.AdmitPoints(ctx, n); err != nil {
				t.Fatalf("AdmitPoints(%d) = %v, want nil", n, err)
			}
		}
		if got := clk.sleeps(); len(got) != 0 {
			t.Fatalf("slept %v, want nothing", got)
		}
	})

	t.Run("room admits, short waits for the point window", func(t *testing.T) {
		clk := newFakeClock()
		g := githubratelimit.New(githubratelimit.Options{Clock: clk, PointReserve: 200})
		// 1200 remaining, 200 reserved: 1000 points of headroom.
		g.Observe(githubratelimit.Observation{Status: http.StatusOK, Body: []byte(graphQLBudgetBody)})
		if err := g.AdmitPoints(ctx, 1000); err != nil {
			t.Fatalf("AdmitPoints(1000) = %v, want nil", err)
		}
		if got := clk.sleeps(); len(got) != 0 {
			t.Fatalf("slept %v, want nothing", got)
		}
		if err := g.AdmitPoints(ctx, 1001); err != nil {
			t.Fatalf("AdmitPoints(1001) = %v, want nil after waiting", err)
		}
		if got := clk.sleeps(); len(got) != 1 || got[0] != 45*time.Minute {
			t.Fatalf("slept %v, want one wait of 45m — the POINT window, not the request window", got)
		}
		if g.Points().Known() {
			t.Fatalf("the spent point budget must be forgotten once its window is over")
		}
	})

	t.Run("a query costing more than the whole budget never fits", func(t *testing.T) {
		clk := newFakeClock()
		g := githubratelimit.New(githubratelimit.Options{Clock: clk})
		g.Observe(githubratelimit.Observation{Status: http.StatusOK, Body: []byte(graphQLBudgetBody)})
		err := g.AdmitPoints(ctx, 5001)
		if got := cerr.KindOf(err); got != cerr.KindInvalid {
			t.Fatalf("KindOf = %v, want KindInvalid for a query larger than the whole budget", got)
		}
		if got := clk.sleeps(); len(got) != 0 {
			t.Fatalf("slept %v, want nothing", got)
		}
	})

	t.Run("an abandoned wait reports graphql_cost", func(t *testing.T) {
		clk := newFakeClock()
		clk.failAt, clk.failWith = 1, context.Canceled
		g := githubratelimit.New(githubratelimit.Options{Clock: clk})
		g.Observe(githubratelimit.Observation{Status: http.StatusOK, Body: []byte(graphQLRefusedWithBudgetBody)})
		var rlErr *githubratelimit.Error
		if err := g.AdmitPoints(ctx, 1); !errors.As(err, &rlErr) {
			t.Fatalf("err = %v, want *githubratelimit.Error", err)
		}
		if rlErr.Mechanism != githubratelimit.MechanismGraphQLCost {
			t.Fatalf("Mechanism = %v, want graphql_cost", rlErr.Mechanism)
		}
		if !rlErr.RetryAt.Equal(pointsResetAt) {
			t.Fatalf("RetryAt = %v, want %v", rlErr.RetryAt, pointsResetAt)
		}
	})
}

func TestDoReturnsTheValueWhenNothingRefusedIt(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk})
	seq := &observations{items: []githubratelimit.Observation{healthy()}}

	got, err := githubratelimit.Do(context.Background(), g, "GetRepo",
		func(context.Context) (string, githubratelimit.Observation, error) {
			return "placeholder/repo", seq.next(), nil
		})
	if err != nil {
		t.Fatalf("Do = %v, want nil", err)
	}
	if got != "placeholder/repo" {
		t.Fatalf("value = %q", got)
	}
	if seq.calls != 1 {
		t.Fatalf("attempts = %d, want 1", seq.calls)
	}
	if s := clk.sleeps(); len(s) != 0 {
		t.Fatalf("slept %v, want nothing", s)
	}
}

// TestDoHonoursRetryAfterOnASecondaryRefusal asserts the DELAY it was asked to
// sleep and the NUMBER of attempts — not how long anything took.
func TestDoHonoursRetryAfterOnASecondaryRefusal(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk, Attempts: 3, Jitter: -1})
	seq := &observations{items: []githubratelimit.Observation{
		secondaryRefusal("45"),
		secondaryRefusal("90"),
		healthy(),
	}}

	got, err := githubratelimit.Do(context.Background(), g, "ListPRs",
		func(context.Context) (int, githubratelimit.Observation, error) {
			obs := seq.next()
			if obs.Status != http.StatusOK {
				return 0, obs, errors.New("403 Forbidden")
			}
			return 7, obs, nil
		})
	if err != nil {
		t.Fatalf("Do = %v, want nil — the third attempt succeeded", err)
	}
	if got != 7 {
		t.Fatalf("value = %d, want 7", got)
	}
	if seq.calls != 3 {
		t.Fatalf("attempts = %d, want 3", seq.calls)
	}
	want := []time.Duration{45 * time.Second, 90 * time.Second}
	if s := clk.sleeps(); len(s) != len(want) || s[0] != want[0] || s[1] != want[1] {
		t.Fatalf("slept %v, want %v — each refusal's own retry-after", s, want)
	}
}

// TestDoJittersWhenTheRefusalNamesNoWait — a secondary limit frequently names
// none, which is exactly when the jitter matters.
func TestDoJittersWhenTheRefusalNamesNoWait(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{
		Clock: clk, Attempts: 3, Base: time.Second, MaxDelay: time.Minute,
		Jitter: 0.5, Rand: randSeq(0, 1),
	})
	seq := &observations{items: []githubratelimit.Observation{secondaryRefusal("")}}

	_, err := githubratelimit.Do(context.Background(), g, "MergePR",
		func(context.Context) (string, githubratelimit.Observation, error) {
			return "", seq.next(), errors.New("403 Forbidden")
		})

	var rlErr *githubratelimit.Error
	if !errors.As(err, &rlErr) {
		t.Fatalf("err = %T (%v), want *githubratelimit.Error", err, err)
	}
	if rlErr.Mechanism != githubratelimit.MechanismSecondary {
		t.Fatalf("Mechanism = %v, want secondary", rlErr.Mechanism)
	}
	if rlErr.Attempts != 3 {
		t.Fatalf("Attempts = %d, want 3", rlErr.Attempts)
	}
	if !rlErr.RetryAt.IsZero() {
		t.Fatalf("RetryAt = %v, want the zero Time: a secondary limit with no retry-after does not say when it ends", rlErr.RetryAt)
	}
	if seq.calls != 3 {
		t.Fatalf("attempts = %d, want 3", seq.calls)
	}
	// rand 0 then 1: the floor of attempt 1's window, then the ceiling of
	// attempt 2's. Both computed, both exact.
	want := []time.Duration{500 * time.Millisecond, 2 * time.Second}
	if s := clk.sleeps(); len(s) != len(want) || s[0] != want[0] || s[1] != want[1] {
		t.Fatalf("slept %v, want %v", s, want)
	}
}

// TestDoDiscardsAPartialValueOnARateLimitRefusal is the fail-closed test that
// matters most. GitHub refuses a point-exhausted GraphQL query with HTTP 200 and
// PARTIAL data, so a caller decoding that body has a value, no error, and half an
// answer — a truncated result indistinguishable from a complete one.
func TestDoDiscardsAPartialValueOnARateLimitRefusal(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{
		Clock: clk, Attempts: 2, Base: time.Second, MaxDelay: time.Minute, Jitter: -1,
	})
	partial := githubratelimit.Observation{Status: http.StatusOK, Body: []byte(graphQLPartialBody)}
	calls := 0

	got, err := githubratelimit.Do(context.Background(), g, "ListPRs",
		func(context.Context) ([]int, githubratelimit.Observation, error) {
			calls++
			// The dangerous return: a value, a nil error, and half a result.
			return []int{1}, partial, nil
		})

	if got != nil {
		t.Fatalf("Do returned %v alongside a rate-limit refusal; a partial answer that looks complete is the whole defect", got)
	}
	var rlErr *githubratelimit.Error
	if !errors.As(err, &rlErr) {
		t.Fatalf("err = %T (%v), want *githubratelimit.Error", err, err)
	}
	if rlErr.Mechanism != githubratelimit.MechanismGraphQLCost {
		t.Fatalf("Mechanism = %v, want graphql_cost — the refusal arrived on a 200 with no header at all", rlErr.Mechanism)
	}
	if calls != 2 {
		t.Fatalf("attempts = %d, want 2", calls)
	}
	if !cerr.TripsBreaker(err) {
		t.Fatalf("an unresolved rate limit must open the host breaker")
	}
}

// TestDoReturnsANonRateLimitFailureVerbatimAndDoesNotRetryIt. A wrapper that
// retried a 500 would turn a broken backend into a slow one, and a missing
// permission into a five-attempt stall.
func TestDoReturnsANonRateLimitFailureVerbatimAndDoesNotRetryIt(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk, Attempts: 5})
	sentinel := errors.New("500 Internal Server Error")
	calls := 0

	got, err := githubratelimit.Do(context.Background(), g, "GetDiff",
		func(context.Context) (string, githubratelimit.Observation, error) {
			calls++
			return "should not be read", githubratelimit.Observation{
				Status: http.StatusInternalServerError,
				Header: primaryHeader(4000, 5000, 1000, resetUnix, "core"),
				Body:   []byte(`{"message":"Server Error"}`),
			}, sentinel
		})

	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the caller's own error unchanged", err)
	}
	if cerr.TripsBreaker(err) {
		t.Fatalf("a 500 is not evidence the backend is refusing load; it must not open the breaker")
	}
	if got != "" {
		t.Fatalf("value = %q, want the zero value alongside a failure", got)
	}
	if calls != 1 {
		t.Fatalf("attempts = %d, want 1 — this package retries rate limits, not requests", calls)
	}
	if s := clk.sleeps(); len(s) != 0 {
		t.Fatalf("slept %v, want nothing", s)
	}
	// A 403 for a missing scope is the same case and matters more, because it
	// looks like a rate limit from a distance.
	calls = 0
	_, err = githubratelimit.Do(context.Background(), g, "GetDiff",
		func(context.Context) (string, githubratelimit.Observation, error) {
			calls++
			return "", githubratelimit.Observation{
				Status: http.StatusForbidden,
				Header: primaryHeader(4000, 5000, 1000, resetUnix, "core"),
				Body:   []byte(missingScopeBody),
			}, sentinel
		})
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("a 403 for a missing scope: err = %v after %d attempt(s), want the caller's error after 1", err, calls)
	}
}

// TestDoWithOneAttemptReportsTheLimitInsteadOfWaitingItOut. Attempts: 1 is a
// legitimate configuration — the caller is asking to be told about the limit
// rather than to have it waited out.
func TestDoWithOneAttemptReportsTheLimitInsteadOfWaitingItOut(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk, Attempts: 1})
	calls := 0

	_, err := githubratelimit.Do(context.Background(), g, "ListBranches",
		func(context.Context) (int, githubratelimit.Observation, error) {
			calls++
			return 0, primaryRefusal(), errors.New("403 Forbidden")
		})

	var rlErr *githubratelimit.Error
	if !errors.As(err, &rlErr) {
		t.Fatalf("err = %T (%v), want *githubratelimit.Error", err, err)
	}
	if rlErr.Mechanism != githubratelimit.MechanismPrimary || rlErr.Attempts != 1 {
		t.Fatalf("Error = %+v, want primary after 1 attempt", rlErr)
	}
	if !rlErr.RetryAt.Equal(resetUnix) {
		t.Fatalf("RetryAt = %v, want the reset %v — surfacing the reset time is the point", rlErr.RetryAt, resetUnix)
	}
	if calls != 1 {
		t.Fatalf("attempts = %d, want 1", calls)
	}
	if s := clk.sleeps(); len(s) != 0 {
		t.Fatalf("slept %v, want nothing", s)
	}
}

// TestDoWaitsPreemptivelyBeforeSpendingAKnownSpentBudget — the primary limit is
// respected BEFORE exhaustion, not after a failure. The budget here was measured
// off a SUCCESSFUL response, so no request has been refused yet.
func TestDoWaitsPreemptivelyBeforeSpendingAKnownSpentBudget(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk})
	g.Observe(githubratelimit.Observation{
		Status: http.StatusOK, // the request that spent the last unit SUCCEEDED
		Header: primaryHeader(0, 5000, 5000, resetUnix, "core"),
		Body:   []byte(`{}`),
	})
	calls := 0

	got, err := githubratelimit.Do(context.Background(), g, "CreatePR",
		func(context.Context) (string, githubratelimit.Observation, error) {
			calls++
			return "pr-1", healthy(), nil
		})
	if err != nil {
		t.Fatalf("Do = %v, want nil", err)
	}
	if got != "pr-1" || calls != 1 {
		t.Fatalf("value %q after %d attempt(s), want pr-1 after 1", got, calls)
	}
	if s := clk.sleeps(); len(s) != 1 || s[0] != 15*time.Minute {
		t.Fatalf("slept %v, want one pre-emptive wait of 15m BEFORE the request was made", s)
	}
}

// TestDoAnnouncesEveryWaitBeforeItBegins is the visibility criterion. A sweep
// that pauses silently is indistinguishable from a hung one, and an operator
// kills the second — turning a wait that would have completed into the
// half-applied sweep the wait existed to prevent.
func TestDoAnnouncesEveryWaitBeforeItBegins(t *testing.T) {
	clk := newFakeClock()
	var waits []githubratelimit.Wait
	var g *githubratelimit.Gate
	g = githubratelimit.New(githubratelimit.Options{
		Clock: clk, Attempts: 3, Base: time.Second, MaxDelay: time.Minute,
		Jitter: 0.5, Rand: randSeq(0),
		// Reading the gate's own budget from inside Notify is the obvious thing
		// for a real callback to log, and it would DEADLOCK if the callback ran
		// under the gate's lock. This test hangs rather than fails if that
		// regresses, which is the loudest signal available for a deadlock.
		Notify: func(w githubratelimit.Wait) {
			_ = g.Primary()
			waits = append(waits, w)
		},
	})
	seq := &observations{items: []githubratelimit.Observation{
		secondaryRefusal("30"), // dictated
		secondaryRefusal(""),   // undictated: the gate must jitter
		healthy(),
	}}

	if _, err := githubratelimit.Do(context.Background(), g, "ReconcileStatus",
		func(context.Context) (int, githubratelimit.Observation, error) {
			obs := seq.next()
			if obs.Status != http.StatusOK {
				return 0, obs, errors.New("403 Forbidden")
			}
			return 1, obs, nil
		}); err != nil {
		t.Fatalf("Do = %v, want nil", err)
	}

	if len(waits) != 2 {
		t.Fatalf("announced %d wait(s), want 2: %+v", len(waits), waits)
	}
	first, second := waits[0], waits[1]
	if first.Op != "ReconcileStatus" || first.Mechanism != githubratelimit.MechanismSecondary {
		t.Fatalf("first wait = %+v, want the op and mechanism named", first)
	}
	if first.Attempt != 1 || first.Attempts != 3 {
		t.Fatalf("first wait attempt %d of %d, want 1 of 3", first.Attempt, first.Attempts)
	}
	if first.Delay != 30*time.Second || !first.Dictated {
		t.Fatalf("first wait = %v dictated=%v, want 30s dictated", first.Delay, first.Dictated)
	}
	if !first.Until.Equal(classifyNow.Add(30 * time.Second)) {
		t.Fatalf("first wait Until = %v, want %v — an operator needs the until-when, not just the how-long",
			first.Until, classifyNow.Add(30*time.Second))
	}
	if second.Attempt != 2 || second.Dictated {
		t.Fatalf("second wait = %+v, want attempt 2 and NOT dictated: the refusal named no wait, so the gate jittered", second)
	}
	if second.Delay != time.Second {
		t.Fatalf("second wait delay = %v, want 1s (attempt 2's jitter floor)", second.Delay)
	}
	if !second.Until.Equal(first.Until.Add(second.Delay)) {
		t.Fatalf("second wait Until = %v, want it computed off the clock after the first wait", second.Until)
	}
}

// TestDoReportsACancelledWaitAsATypedRefusalAndNoValue — a host with a deadline
// gets a typed refusal naming when the limit clears, and never a partial value.
func TestDoReportsACancelledWaitAsATypedRefusalAndNoValue(t *testing.T) {
	clk := newFakeClock()
	clk.failAt, clk.failWith = 1, context.DeadlineExceeded
	g := githubratelimit.New(githubratelimit.Options{Clock: clk, Attempts: 4, Jitter: -1})

	got, err := githubratelimit.Do(context.Background(), g, "ApplyItemFieldValues",
		func(context.Context) (map[string]string, githubratelimit.Observation, error) {
			return map[string]string{"half": "applied"}, secondaryRefusal("60"), errors.New("403 Forbidden")
		})

	if got != nil {
		t.Fatalf("Do returned %v after abandoning its wait; a partial answer must never escape", got)
	}
	var rlErr *githubratelimit.Error
	if !errors.As(err, &rlErr) {
		t.Fatalf("err = %T (%v), want *githubratelimit.Error", err, err)
	}
	if rlErr.Mechanism != githubratelimit.MechanismSecondary || rlErr.Attempts != 1 {
		t.Fatalf("Error = %+v, want secondary after 1 attempt", rlErr)
	}
	if !rlErr.RetryAt.Equal(classifyNow.Add(60 * time.Second)) {
		t.Fatalf("RetryAt = %v, want the retry-after applied to the clock", rlErr.RetryAt)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the deadline must stay reachable through the chain")
	}
}

// TestDoKeepsTheAttemptsOwnErrorReachable. The verdict names the mechanism and
// the reset time, but only the attempt's own error carries WHICH request was
// refused — the URL, the backend's own message, a caller sentinel. An operator
// handed "secondary rate limit, 3 attempts" and nothing else has to go find the
// call themselves.
func TestDoKeepsTheAttemptsOwnErrorReachable(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk, Attempts: 2, Jitter: -1})
	sentinel := errors.New("403 Forbidden on GET /repos/placeholder/repo/pulls?page=7")

	_, err := githubratelimit.Do(context.Background(), g, "ListPRs",
		func(context.Context) (int, githubratelimit.Observation, error) {
			return 0, secondaryRefusal(""), sentinel
		})

	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; the attempt's own error must stay reachable through the chain", err)
	}
	// And it is STILL classified as a rate limit, so a host acting on the Kind
	// alone is unaffected by carrying the cause.
	if got := cerr.KindOf(err); got != cerr.KindRateLimited {
		t.Fatalf("KindOf = %v, want KindRateLimited", got)
	}
	if !cerr.TripsBreaker(err) {
		t.Fatalf("carrying the cause must not stop the refusal opening the breaker")
	}
	var rlErr *githubratelimit.Error
	if !errors.As(err, &rlErr) || rlErr.Err == nil {
		t.Fatalf("Error.Err = %v, want the attempt's error", rlErr)
	}
	if !strings.Contains(err.Error(), "page=7") {
		t.Fatalf("Error() = %q, want the refused request named in it", err.Error())
	}
}

func TestDoRejectsANilGateOrANilAttempt(t *testing.T) {
	_, err := githubratelimit.Do(context.Background(), nil, "op",
		func(context.Context) (int, githubratelimit.Observation, error) { return 0, healthy(), nil })
	if got := cerr.KindOf(err); got != cerr.KindInvalid {
		t.Fatalf("nil gate: KindOf = %v, want KindInvalid", got)
	}
	g := githubratelimit.New(githubratelimit.Options{Clock: newFakeClock()})
	_, err = githubratelimit.Do[int](context.Background(), g, "op", nil)
	if got := cerr.KindOf(err); got != cerr.KindInvalid {
		t.Fatalf("nil attempt: KindOf = %v, want KindInvalid", got)
	}
}

// TestDoRefusesASequenceThatCanNeverFitBeforeMakingAnyRequest — Admit's caller
// error propagates out of Do unchanged, and no attempt is made.
func TestDoStopsWhenAdmitReportsACallerError(t *testing.T) {
	clk := newFakeClock()
	g := githubratelimit.New(githubratelimit.Options{Clock: clk, Reserve: 5000})
	g.Observe(primaryRefusal()) // limit 5000, reserve 5000: one request never fits
	calls := 0

	_, err := githubratelimit.Do(context.Background(), g, "ListProjectItems",
		func(context.Context) (int, githubratelimit.Observation, error) {
			calls++
			return 0, healthy(), nil
		})
	if got := cerr.KindOf(err); got != cerr.KindInvalid {
		t.Fatalf("KindOf = %v, want KindInvalid", got)
	}
	if calls != 0 {
		t.Fatalf("attempts = %d, want 0 — a request that can never be admitted must not be made", calls)
	}
}

// TestGateIsSafeForConcurrentUse. The traffic shape that provokes GitHub's
// secondary limits is a burst of parallel workers, so a gate only usable from one
// goroutine would be a gate for the case that does not need one. Run under -race
// this is the mutual-exclusion assertion.
func TestGateIsSafeForConcurrentUse(t *testing.T) {
	const workers = 24
	clk := newFakeClock()
	var notified int
	var mu sync.Mutex
	g := githubratelimit.New(githubratelimit.Options{
		Clock: clk, Attempts: 3, Base: time.Millisecond, MaxDelay: time.Second,
		Jitter: 0.5,
		Notify: func(githubratelimit.Wait) {
			mu.Lock()
			notified++
			mu.Unlock()
		},
	})

	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Half the workers are refused twice then succeed; the rest read
			// and write the gate's state alongside them.
			if i%2 == 0 {
				seq := &observations{items: []githubratelimit.Observation{
					secondaryRefusal(""), secondaryRefusal(""), healthy(),
				}}
				_, results[i] = githubratelimit.Do(context.Background(), g, "concurrent",
					func(context.Context) (int, githubratelimit.Observation, error) {
						obs := seq.next()
						if obs.Status != http.StatusOK {
							return 0, obs, errors.New("403 Forbidden")
						}
						return i, obs, nil
					})
				return
			}
			g.Observe(healthy())
			_ = g.Primary()
			_ = g.Points()
			_ = g.Backoff(2)
			results[i] = g.Admit(context.Background(), 1)
		}()
	}
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Fatalf("worker %d = %v, want nil", i, err)
		}
	}
	mu.Lock()
	got := notified
	mu.Unlock()
	// 12 refused workers × 2 waits each. An exact count, not a timing.
	if got != 24 {
		t.Fatalf("announced %d waits, want 24", got)
	}
}

// TestNoWallClockAssertionsInThisPackagesTests enforces the discipline the
// package doc claims, in the same shape as the module's own IP-boundary test:
// a gate rather than a promise.
//
// A test that reads the real clock, sleeps, or asserts on elapsed time is
// asserting a property of the machine it runs on. Under CI's -race build with
// coverage instrumentation that machine is several times slower than the one any
// threshold gets chosen on, so such a test does not fail honestly — it flakes,
// and a flaky gate gets disabled, which is how the guarantee goes away.
func TestNoWallClockAssertionsInThisPackagesTests(t *testing.T) {
	// The scan reads the parsed SYNTAX, not the file text. A text scan would
	// flag its own needle list and the prose in the comment above — and the
	// obvious fix, weakening the needles until they stop matching the guard
	// itself, weakens the guard.
	//
	// Every name here is a way to reach the real clock from a test, which is
	// what the Clock seam exists to make unnecessary.
	forbidden := map[string]bool{
		"Now": true, "Since": true, "Sleep": true, "After": true,
		"Tick": true, "NewTimer": true, "NewTicker": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Clean(name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		checked++
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.SelectorExpr:
				pkg, ok := node.X.(*ast.Ident)
				if ok && pkg.Name == "time" && forbidden[node.Sel.Name] {
					t.Errorf("%s calls time.%s: this package's tests assert the COMPUTED backoff and the NUMBER of attempts, never wall-clock time",
						fset.Position(node.Pos()), node.Sel.Name)
				}
			case *ast.Ident:
				// An identifier named for a measured duration is the shape of
				// the assertion this forbids, whatever it was computed from.
				if strings.Contains(strings.ToLower(node.Name), "elapsed") {
					t.Errorf("%s declares or reads %q: a duration measured off the running machine is not an assertion about this package",
						fset.Position(node.Pos()), node.Name)
				}
			}
			return true
		})
	}
	// Assert the denominator: a scan that found no files to read is a gate that
	// passed because it looked at nothing.
	if checked < 4 {
		t.Fatalf("scanned only %d test file(s); the package has more than that, so the scan is not reading what it claims to", checked)
	}
}
