package githubratelimit

import (
	"context"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"sync"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// Defaults for the [Options] a caller leaves zero. They are chosen for a host
// making bursts of board reads and writes against one repository, which is the
// traffic shape that provokes GitHub's secondary limits.
const (
	// DefaultAttempts is the total number of attempts, including the first —
	// not the number of retries.
	DefaultAttempts = 5
	// DefaultBase is the first backoff delay, doubling per attempt.
	DefaultBase = 1 * time.Second
	// DefaultMaxDelay caps ONE backoff delay. It does not cap a wait the
	// mechanism itself dictated: a primary reset an hour out is an hour, and
	// truncating it to a minute would retry into the limit 59 times.
	DefaultMaxDelay = 2 * time.Minute
	// DefaultJitter is the fraction of each computed backoff that is
	// randomised, so a delay lands uniformly in [d/2, d].
	//
	// Jitter is not a refinement. Fixed backoff makes every worker that was
	// refused at the same instant retry at the same instant, which is the
	// burst pattern the secondary limit refused them for — the herd is worse
	// than the original limit, and it re-forms on every retry.
	DefaultJitter = 0.5
	// MinBackoff is the shortest delay [Gate.Backoff] will return.
	//
	// It exists because full jitter ([Options.Jitter] of 1) has a window floor
	// of ZERO by definition, so an unlucky draw computes a delay of nothing —
	// and a "backoff" of nothing is a tight retry straight back into the limit
	// that refused the request, which is the one outcome this package must not
	// produce. The floor is small enough to be irrelevant against any real
	// GitHub reset and large enough to still be a pause.
	MinBackoff = time.Millisecond
)

// A Wait announces a pause BEFORE it begins, so a sweep that stops for eleven
// minutes says so. It is what [Options.Notify] receives.
//
// Visibility is a correctness property here, not ergonomics: a silent pause and
// a hang are indistinguishable from outside the process, and an operator kills
// the second one — turning a wait that would have completed into the
// half-applied sweep the wait existed to prevent.
type Wait struct {
	// Op is the operation the caller named, empty for a pre-emptive
	// [Gate.Admit] wait that belongs to no single call.
	Op string
	// Mechanism is which limit is being waited on.
	Mechanism Mechanism
	// Attempt is the 1-based attempt that was refused.
	Attempt int
	// Attempts is the total [Do] will make before giving up.
	//
	// Both Attempt and Attempts are 0 for a PRE-EMPTIVE wait — one [Gate.Admit]
	// took before any request was made. There is no attempt loop there, and
	// reporting "attempt 0 of 5" would leave an operator looking for four
	// retries that are not going to happen.
	Attempts int
	// Delay is how long the pause will last.
	Delay time.Duration
	// Until is the clock time the pause ends. This is the "until when" an
	// operator actually needs; Delay alone leaves them subtracting.
	Until time.Time
	// Dictated distinguishes a wait the MECHANISM named — a retry-after
	// header, a reset timestamp — from a jittered backoff the gate computed
	// because the mechanism named none. An operator reading a log of
	// undictated waits knows the remedy is less concurrency, not more patience.
	Dictated bool
}

// Options configures a [Gate]. Every field has a working default, so
// Options{} is usable.
type Options struct {
	// Clock is the time seam. Nil means [SystemClock].
	Clock Clock
	// Resource names the primary quota bucket this gate tracks — see
	// [HeaderResource]. Empty means [ResourceCore].
	//
	// One gate tracks ONE bucket. The buckets are independent and wildly
	// different sizes (search is 30 a minute against core's 5000 an hour), so a
	// gate that recorded them into one number would stall every core request
	// for the rest of the hour the first time a search was refused. A host
	// reaching more than one bucket builds more than one gate.
	Resource string
	// Reserve is how many primary requests [Gate.Admit] keeps in hand. It buys
	// room for the calls a host must still make when the bulk work stops — a
	// final status read, an error report — which are the calls an operator
	// needs most at exactly the moment the budget ran out.
	Reserve int
	// PointReserve is the same idea for the GraphQL point budget.
	PointReserve int
	// Attempts is the total attempts [Do] makes, including the first. Zero or
	// negative means [DefaultAttempts]. One means no retry at all, which is a
	// legitimate configuration: the caller is asking to be told about the
	// limit rather than to have it waited out.
	Attempts int
	// Base is the first backoff delay. Zero or negative means [DefaultBase].
	Base time.Duration
	// MaxDelay caps one computed backoff. Zero or negative means
	// [DefaultMaxDelay].
	MaxDelay time.Duration
	// Jitter is the randomised fraction of each computed backoff, 0..1. Zero
	// means [DefaultJitter]; to switch jitter OFF — which no production
	// configuration should — pass a negative value, so that turning off the
	// defence against a thundering herd has to be deliberate rather than the
	// consequence of leaving a field unset.
	Jitter float64
	// Rand returns a value in [0,1). Nil means math/rand/v2. The gate calls it
	// under its own lock, so an injected function need not be safe for
	// concurrent use — which is what lets a test supply a plain counter and
	// assert an exact delay.
	Rand func() float64
	// Notify receives a [Wait] before each pause. Nil means waits are silent,
	// which is a supported but poor choice — see [Wait].
	Notify func(Wait)
}

// A Gate applies GitHub's three rate limits to a stream of requests: it holds
// the last-known budgets, waits BEFORE spending a budget it knows is spent, and
// waits AFTER a refusal for as long as the refusing mechanism says.
//
// It is safe for concurrent use by multiple goroutines, which is not optional:
// the traffic shape that provokes GitHub's secondary limits is a burst of
// parallel workers, so a gate only usable from one goroutine would be a gate
// for the case that does not need one.
//
// Build it with [New]. A Gate that was NOT built with New — `var g Gate`, or one
// embedded in another struct — still works: every method defaults it on first
// use, exactly as New would have. See [Gate.ensure].
type Gate struct {
	// defaults is what makes the zero value safe. It runs at most once, either
	// from New or from the first method call on a Gate nobody built.
	defaults sync.Once

	clock        Clock
	resource     string
	reserve      int
	pointReserve int
	attempts     int
	base         time.Duration
	maxDelay     time.Duration
	jitter       float64

	mu      sync.Mutex
	rand    func() float64
	primary PrimaryBudget
	points  PointBudget

	notify func(Wait)
}

// New builds a Gate from opts, filling every unset field with its documented
// default. It never returns nil.
func New(opts Options) *Gate {
	g := &Gate{
		clock:        opts.Clock,
		resource:     opts.Resource,
		reserve:      opts.Reserve,
		pointReserve: opts.PointReserve,
		attempts:     opts.Attempts,
		base:         opts.Base,
		maxDelay:     opts.MaxDelay,
		jitter:       opts.Jitter,
		rand:         opts.Rand,
		notify:       opts.Notify,
	}
	g.ensure()
	return g
}

// ensure fills whatever is unset with its documented default, once.
//
// It exists because Gate is EXPORTED and Go cannot forbid its zero value: a
// third party writes `var g Gate`, or embeds one, and gets a gate whose Clock is
// nil and whose attempt count is zero. Both failures are unacceptable for an
// SDK, and the second is the dangerous one — it is not a panic but a quiet
// wrong answer. With attempts at zero, [Do]'s loop would run NO attempt at all
// and then report a rate limit for a request nobody sent, which trips the host's
// circuit breaker on a call that never happened.
//
// So every exported method calls this first. sync.Once gives the
// happens-before that makes the fields safe to read afterwards from any
// goroutine, which a plain nil check would not.
func (g *Gate) ensure() {
	g.defaults.Do(func() {
		if g.clock == nil {
			g.clock = SystemClock{}
		}
		if g.resource == "" {
			g.resource = ResourceCore
		}
		if g.reserve < 0 {
			g.reserve = 0
		}
		if g.pointReserve < 0 {
			g.pointReserve = 0
		}
		if g.attempts <= 0 {
			g.attempts = DefaultAttempts
		}
		if g.base <= 0 {
			g.base = DefaultBase
		}
		if g.maxDelay <= 0 {
			g.maxDelay = DefaultMaxDelay
		}
		switch {
		case g.jitter == 0:
			g.jitter = DefaultJitter
		case g.jitter < 0:
			// Deliberately off — see [Options.Jitter].
			g.jitter = 0
		case g.jitter > 1:
			g.jitter = 1
		}
		if g.rand == nil {
			g.rand = mathrand.Float64
		}
	})
}

// Resource reports which primary quota bucket this gate tracks.
func (g *Gate) Resource() string {
	g.ensure()
	return g.resource
}

// Attempts reports the total attempts [Do] will make, including the first.
func (g *Gate) Attempts() int {
	g.ensure()
	return g.attempts
}

// Primary reports the last primary budget the gate recorded, which is the
// UNKNOWN budget until a response has been observed for this gate's bucket.
func (g *Gate) Primary() PrimaryBudget {
	g.ensure()
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.primary
}

// Points reports the last GraphQL point budget the gate recorded, UNKNOWN until
// a response carrying one has been observed.
func (g *Gate) Points() PointBudget {
	g.ensure()
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.points
}

// Observe classifies a response and records the budgets it reported, returning
// the [Verdict] so the caller can act on it directly.
//
// A primary budget for a DIFFERENT bucket than this gate's is returned in the
// Verdict but NOT recorded — see [Options.Resource]. It is returned rather than
// dropped so that a host wiring one gate where it needs two can see the
// mismatch instead of watching pre-emption quietly never fire.
//
// Recording happens whether or not the response was a refusal. That is the
// whole basis of respecting the primary limit before exhaustion rather than
// after: the budget arrives on every response, including the successful ones.
//
// # A late response cannot resurrect a spent budget
//
// Within one window GitHub's remaining only ever goes DOWN, but responses to
// concurrent requests do not arrive in the order they were sent — so a reply to
// an earlier request can land after one reporting a lower figure, and recording
// it verbatim would raise the recorded remaining back up. That is a fail-open:
// the gate would then wave the next request straight through into a refusal it
// already had the evidence to avoid.
//
// So for the SAME window the lower remaining wins, and a budget describing an
// OLDER window is ignored entirely. A budget for a later window replaces
// whatever is held, because the window really has rolled over.
func (g *Gate) Observe(obs Observation) Verdict {
	g.ensure()
	v := Classify(obs, g.clock.Now())
	g.mu.Lock()
	defer g.mu.Unlock()
	if v.Primary.Known() && v.Primary.AttributableTo(g.resource) {
		g.primary = mergePrimary(g.primary, v.Primary)
	}
	if v.Points.Known() {
		g.points = mergePoints(g.points, v.Points)
	}
	return v
}

// mergePrimary picks which of two primary budgets describes the gate's real
// state. See [Gate.Observe] for why the newest arrival is not simply the answer.
func mergePrimary(held, incoming PrimaryBudget) PrimaryBudget {
	switch {
	case !held.known:
		return incoming
	case incoming.Reset.After(held.Reset):
		return incoming // a genuinely new window
	case incoming.Reset.Before(held.Reset):
		return held // a reply from a window that is over
	case incoming.Remaining < held.Remaining:
		return incoming // same window, further spent
	default:
		return held
	}
}

// mergePoints is mergePrimary for the GraphQL point budget, on the same rule.
func mergePoints(held, incoming PointBudget) PointBudget {
	switch {
	case !held.known:
		return incoming
	case incoming.ResetAt.After(held.ResetAt):
		return incoming
	case incoming.ResetAt.Before(held.ResetAt):
		return held
	case incoming.Remaining < held.Remaining:
		return incoming
	default:
		return held
	}
}

// Admit blocks until the recorded primary budget has room for requests more
// calls beyond [Options.Reserve], and is the pre-emptive half of this package.
//
// # This is the answer to "never half-applied"
//
// A sequence of mutations that runs out of budget halfway through leaves the
// remote half-changed, and no amount of retrying afterwards can tell an
// operator which half ran. The guarantee this gate makes is
// COMPLETES-AFTER-WAITING, and the way it buys that guarantee is to refuse to
// let the sequence START until the whole of it fits: a caller passes the number
// of requests the sequence will make, once, before the first one.
//
// The gate cannot roll back a mutation that already happened, so it does not
// pretend to offer the other guarantee. Admitting the whole sequence up front
// is the mechanism that keeps there from being a mutation to roll back.
//
// # What it does in each state
//
//   - requests <= 0: returns immediately. Nothing was asked for.
//   - the budget is UNKNOWN: admits. There is no evidence of exhaustion, and
//     pre-emptively blocking on no evidence would let one missing header stall
//     a host forever. The request itself is the probe that measures the budget.
//   - the budget has room: admits.
//   - the sequence is larger than the whole bucket minus the reserve: returns
//     cerr.KindInvalid WITHOUT waiting. No amount of waiting makes it fit, and
//     a wait that can never succeed is a hang with extra steps.
//   - the budget is short and the window has not rolled over: announces a
//     [Wait] and sleeps until the reset, then forgets the spent budget so the
//     next response re-measures it.
//   - the budget is short and the window has already passed: admits, and
//     forgets the spent budget. The recorded numbers describe a window that is
//     over; the next response carries the new ones.
//
// A ctx that is cancelled or times out during the wait ends it, and Admit
// returns an [Error] naming [MechanismPrimary] and the reset time — so a host
// with a deadline gets a typed refusal that says when the limit clears, not a
// bare context error and not a partially-run sequence. A host that must not
// block for the remainder of an hour bounds the wait that way: the context IS
// the ceiling.
func (g *Gate) Admit(ctx context.Context, requests int) error {
	g.ensure()
	if requests <= 0 {
		return nil
	}
	budget := g.Primary()
	if !budget.Known() || budget.Headroom(g.reserve) >= requests {
		return nil
	}
	if budget.Limit > 0 && requests > budget.Limit-g.reserve {
		return cerr.New(cerr.KindInvalid, "Admit", fmt.Errorf(
			"a sequence of %d request(s) cannot fit the %s quota of %d with %d held in reserve; it will never be admitted, however long it waits",
			requests, g.resource, budget.Limit, g.reserve))
	}
	return g.waitForWindow(ctx, MechanismPrimary, budget.ResetIn(g.clock.Now()), budget.Reset,
		func() { g.expirePrimary(budget) })
}

// AdmitPoints is [Gate.Admit] for the GraphQL point budget, which is accounted
// separately because it is a separate budget: a caller with thousands of REST
// requests remaining can have no points at all.
//
// It behaves identically state for state, against the recorded [PointBudget] and
// [Options.PointReserve], and reports [MechanismGraphQLCost] on the wait and on
// any failure. A GraphQL sweep that will also spend request count calls both.
func (g *Gate) AdmitPoints(ctx context.Context, points int) error {
	g.ensure()
	if points <= 0 {
		return nil
	}
	budget := g.Points()
	if !budget.Known() || budget.Headroom(g.pointReserve) >= points {
		return nil
	}
	if budget.Limit > 0 && points > budget.Limit-g.pointReserve {
		return cerr.New(cerr.KindInvalid, "AdmitPoints", fmt.Errorf(
			"a query costing %d point(s) cannot fit the graphql point budget of %d with %d held in reserve; it will never be admitted, however long it waits",
			points, budget.Limit, g.pointReserve))
	}
	return g.waitForWindow(ctx, MechanismGraphQLCost, budget.ResetIn(g.clock.Now()), budget.ResetAt,
		func() { g.expirePoints(budget) })
}

// waitForWindow is the shared tail of both Admit paths: sleep out the remainder
// of a spent window, then forget the spent budget.
//
// forget runs on BOTH exits where the window is over — the wait completing and
// the window already having passed — and deliberately not when the wait was
// abandoned: a cancelled wait learned nothing, and discarding the budget would
// throw away the evidence that the next Admit needs.
func (g *Gate) waitForWindow(ctx context.Context, m Mechanism, d time.Duration, reset time.Time, forget func()) error {
	if d <= 0 {
		forget()
		return nil
	}
	// Attempt and Attempts stay 0: this wait belongs to no attempt loop. See
	// [Wait.Attempts].
	g.announce(Wait{
		Mechanism: m,
		Delay:     d,
		Until:     g.clock.Now().Add(d),
		Dictated:  true,
	})
	if err := g.clock.Sleep(ctx, d); err != nil {
		return &Error{Op: "Admit", Mechanism: m, Attempts: 0, RetryAt: reset, Err: err}
	}
	forget()
	return nil
}

// expirePrimary forgets a spent budget, but only if it is still the one the
// caller waited on.
//
// The comparison is against the window, not the counters: a concurrent Observe
// may already have recorded a FRESH budget for the next window while this
// goroutine slept, and clobbering that with UNKNOWN would make every other
// goroutine re-probe a budget that had just been measured.
func (g *Gate) expirePrimary(spent PrimaryBudget) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.primary.known && g.primary.Reset.Equal(spent.Reset) {
		g.primary = PrimaryBudget{}
	}
}

// expirePoints is expirePrimary for the point budget, with the same
// same-window guard.
func (g *Gate) expirePoints(spent PointBudget) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.points.known && g.points.ResetAt.Equal(spent.ResetAt) {
		g.points = PointBudget{}
	}
}

// Backoff reports the delay before the attempt after the given 1-based attempt:
// Backoff(1) is the pause after the first attempt was refused.
//
// It is exponential from [Options.Base], doubling per attempt, capped at
// [Options.MaxDelay], and then jittered — the delay lands uniformly in
// [(1-jitter)·d, d]. Jitter is applied AFTER the cap so that the cap is a
// ceiling on what is actually slept rather than on the pre-jitter figure.
//
// It is exported because it is the thing a test should assert. Asserting the
// computed delay is exact; asserting that a sleep took roughly that long is a
// measurement of the machine, and under a -race build with coverage
// instrumentation that machine is slow enough to fail a threshold chosen on an
// idle one.
//
// The returned delay is NEVER zero or negative: it is at least [MinBackoff],
// unless [Options.MaxDelay] is itself smaller, in which case MaxDelay wins
// because a caller's explicit ceiling outranks this package's floor. An attempt
// below 1 is treated as 1, and an attempt large enough to overflow the doubling
// returns the cap, jittered, rather than a negative duration.
func (g *Gate) Backoff(attempt int) time.Duration {
	g.ensure()
	if attempt < 1 {
		attempt = 1
	}
	d := g.maxDelay
	// 62 shifts is past the point where any plausible base still fits an
	// int64 nanosecond duration; beyond it the doubling is the cap anyway.
	if shift := attempt - 1; shift < 62 {
		if scaled := g.base << shift; scaled > 0 && scaled < d {
			d = scaled
		}
	}
	if g.jitter > 0 {
		d = g.applyJitter(d)
	}
	// Never zero, never negative: see [MinBackoff]. The cap wins if a caller
	// configured a MaxDelay below the floor, so MaxDelay stays a ceiling.
	return min(max(d, MinBackoff), g.maxDelay)
}

// applyJitter spreads d uniformly over [(1-jitter)·d, d]. It is separate from
// [Gate.Backoff] only to keep the lock the Rand call needs off the rest of the
// arithmetic.
func (g *Gate) applyJitter(d time.Duration) time.Duration {
	floor := time.Duration(float64(d) * (1 - g.jitter))
	span := d - floor
	if span <= 0 {
		return floor
	}
	g.mu.Lock()
	r := g.rand()
	g.mu.Unlock()
	// A Rand that breaks its contract is clamped rather than trusted: a value
	// outside [0,1) would compute a delay outside the cap, or a negative one.
	if r < 0 {
		r = 0
	}
	if r >= 1 {
		r = 1
	}
	return floor + time.Duration(float64(span)*r)
}

// announce delivers a Wait to the caller's Notify, outside the gate's lock.
//
// Outside the lock is required, not tidy: a Notify that logged the gate's
// current budget — the obvious thing to log — would deadlock on [Gate.Primary]
// if it were called while the lock was held.
func (g *Gate) announce(w Wait) {
	if g.notify == nil {
		return
	}
	g.notify(w)
}

// Do runs fn under the gate's rate-limit discipline and returns fn's value only
// when no rate limit refused it.
//
// fn returns three things: its value, an [Observation] of the response, and its
// error. The value and the observation are separate because a refused attempt
// has an observation and no usable value — which is the case this signature
// exists to make unrepresentable.
//
// # Fail closed
//
// When the verdict is a rate-limit refusal, fn's value is DISCARDED and the
// zero T is returned. That holds even when fn returned a nil error, which is
// not a hypothetical: GitHub refuses a point-exhausted GraphQL query with HTTP
// 200 and a partial or null `data`, so a caller decoding that body gets a
// value, no error, and half an answer. A truncated answer that is
// indistinguishable from a complete one is the defect this discards it to
// prevent. On exhausting the attempts, Do returns the zero T and an [Error]
// naming the mechanism, the attempt count and the reset time.
//
// # What it retries, and what it does not
//
// Rate-limit refusals only. Any other failure — a 500, a 404, a 403 for a
// missing scope, a transport error — is returned verbatim on the first attempt,
// with fn's own value passed straight back if fn reported none. A wrapper that
// also retried those would turn a broken backend into a slow one and a missing
// permission into a five-attempt stall.
//
// # The wait per attempt
//
// The refusal's own [Verdict.RetryAfter] when it named one; otherwise a
// jittered [Gate.Backoff]. A secondary limit frequently names none, which is
// exactly when the jitter matters: every worker refused in the same burst would
// otherwise retry in the same instant and re-form the burst.
//
// Before each attempt it calls [Gate.Admit] for one request, so a known-spent
// primary budget is waited out rather than spent into a refusal. It does not
// call [Gate.AdmitPoints]: the cost of a GraphQL query is the caller's estimate
// to make, and guessing one here would be a number nobody could justify.
func Do[T any](ctx context.Context, g *Gate, op string, fn func(context.Context) (T, Observation, error)) (T, error) {
	var zero T
	if g == nil {
		return zero, cerr.New(cerr.KindInvalid, op, errors.New("nil gate: build one with githubratelimit.New"))
	}
	if fn == nil {
		return zero, cerr.New(cerr.KindInvalid, op, errors.New("nil attempt function"))
	}
	g.ensure()

	var last Verdict
	var lastErr error
	for attempt := 1; attempt <= g.attempts; attempt++ {
		if err := g.Admit(ctx, 1); err != nil {
			return zero, err
		}
		val, obs, err := fn(ctx)
		v := g.Observe(obs)
		if !v.Limited() {
			// Not a rate limit. fn's own outcome, unchanged — including a
			// failure this package has no business reinterpreting.
			if err != nil {
				return zero, err
			}
			return val, nil
		}
		// The attempt's own error is kept, not discarded: it is the only thing
		// that carries WHICH request was refused — the URL, the backend's own
		// message — and an operator handed "secondary rate limit, 5 attempts"
		// with no request in it has to go looking for the call themselves.
		last, lastErr = v, err
		if attempt == g.attempts {
			break
		}
		delay, dictated := v.RetryAfter, true
		if delay <= 0 {
			delay, dictated = g.Backoff(attempt), false
		}
		g.announce(Wait{
			Op:        op,
			Mechanism: v.Mechanism,
			Attempt:   attempt,
			Attempts:  g.attempts,
			Delay:     delay,
			Until:     g.clock.Now().Add(delay),
			Dictated:  dictated,
		})
		if serr := g.clock.Sleep(ctx, delay); serr != nil {
			// The sleep's own failure wins the Err slot over the attempt's:
			// a caller that passed a deadline needs errors.Is against it to
			// work, and that is the more actionable of the two.
			return zero, &Error{Op: op, Mechanism: v.Mechanism, Attempts: attempt, RetryAt: v.RetryAt, Err: serr}
		}
	}
	return zero, &Error{Op: op, Mechanism: last.Mechanism, Attempts: g.attempts, RetryAt: last.RetryAt, Err: lastErr}
}
