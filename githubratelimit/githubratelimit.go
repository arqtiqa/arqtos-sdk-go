// Package githubratelimit handles GitHub's THREE rate-limit mechanisms —
// primary quota, secondary (abuse-detection) limits, and the GraphQL point
// budget — as three separate things, because they are three separate things.
//
// # Why the package is named after one vendor
//
// Every other connector-facing package in this module is deliberately
// vendor-neutral: an ABC derived from a single backend encodes that backend's
// accidents as contract. This package is the opposite on purpose. The three
// mechanisms below are not a general model of rate limiting that GitHub
// happens to implement — they are GitHub's, down to the header names and the
// units. Naming the package after the vendor says so, so that nobody reaches
// for it as "the rate limiter" and quietly inherits GitHub's shape for a
// backend that does not have it.
//
// It lives in the SDK rather than in a host because a host reaches GitHub
// through more than one surface — a tracker and a CI/PR surface at least — and
// each one needs the identical discipline. Two implementations of this are two
// implementations that drift, and the drift is invisible until one of them
// under-waits in production.
//
// # The three mechanisms
//
// [MechanismPrimary] is the hourly quota. It is reported on EVERY response, in
// the x-ratelimit-remaining / x-ratelimit-reset headers, whether or not the
// request was refused. That is what makes it the one mechanism a caller can
// respect BEFORE exhausting it: see [Gate.Admit].
//
// [MechanismSecondary] is abuse detection — burst rate and concurrency, not
// volume. It is signalled differently (a retry-after header, or a 403/429
// whose body names a secondary limit) and, critically, waiting out the PRIMARY
// reset does not clear it. A handler that reads a secondary refusal as a
// primary one waits for the wrong thing and is refused again.
//
// [MechanismGraphQLCost] is a per-query POINT budget, reported in the response
// BODY's rateLimit field and not in headers at all. A single GraphQL request
// can spend hundreds of points, so the REST request count says nothing useful
// about it. See [PointBudget].
//
// Collapsing the three into one handler is the failure this package exists to
// prevent, so the vocabulary is closed ([Mechanisms]) and [Classify] reports
// exactly which one refused a response.
//
// # What it guarantees, and what it cannot
//
// For ONE request, [Do] guarantees completes-after-waiting or a typed failure:
// it never hands back a value alongside a rate-limit error, so a truncated
// answer cannot be mistaken for a complete one.
//
// For a MULTI-STEP sequence — a paginated sweep, a batch of board mutations —
// the guarantee is completes-after-waiting, and it is bought by not letting
// the sequence START until the whole of it fits in the remaining budget:
// [Gate.Admit] takes the number of requests the sequence will make. A sequence
// that cannot fit waits for the reset; a sequence larger than the entire
// hourly quota is refused as a caller error rather than waited on forever.
//
// The gate cannot roll back a mutation that already happened. Admitting the
// whole sequence up front is the mechanism that keeps there from being one.
//
// # Waits are visible
//
// Every wait is announced through [Options.Notify] before it begins, carrying
// the mechanism, the attempt number, the delay and the wall-clock time it ends
// ([Wait]). A sweep that pauses for eleven minutes and says nothing is
// indistinguishable from a hung one, and an operator kills the second.
//
// # Time is injected
//
// [Clock] is the only source of now and of sleeping. Tests supply a fake and
// assert the COMPUTED backoff and the NUMBER of attempts; nothing in this
// package's tests measures elapsed wall-clock time, which is how a
// backoff test flakes under -race.
package githubratelimit

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// A Mechanism names WHICH of GitHub's rate limits refused a request.
//
// The set is closed for the same reason [cerr.Kinds] is: a caller acts on the
// name, and the whole point of the package is that the three get different
// responses. A mechanism outside the set is not a classification — see
// [Mechanism.Valid].
type Mechanism int

const (
	// MechanismNone is the zero value: nothing in the response said a rate
	// limit refused it. It is NOT "the budget is healthy" — a response can
	// carry no rate-limit signal at all, and [PrimaryBudget.Known] is what
	// distinguishes those two.
	MechanismNone Mechanism = iota
	// MechanismPrimary is the hourly quota, signalled by
	// x-ratelimit-remaining reaching zero. The wait is until
	// x-ratelimit-reset; retrying before then is refused again.
	MechanismPrimary
	// MechanismSecondary is abuse detection: too many requests too fast, or
	// too many concurrent. Signalled by a retry-after header, or by a 403/429
	// whose body names a secondary limit.
	//
	// It is the mechanism a naive handler gets wrong, because the primary
	// budget on the very same response can read as healthy — remaining is not
	// zero, so "wait for the reset" computes a wait of nothing and retries
	// straight back into the limit.
	MechanismSecondary
	// MechanismGraphQLCost is the GraphQL point budget, reported in the
	// response body's rateLimit field rather than in any header. It is
	// accounted separately from the REST request count because one request can
	// cost hundreds of points — a caller with thousands of REST requests left
	// can have no points at all.
	MechanismGraphQLCost
)

// mechanismNames is the single source of truth for the closed vocabulary:
// [Mechanisms], [Mechanism.Valid] and [Mechanism.String] all derive from it,
// so a mechanism cannot be half-added.
var mechanismNames = map[Mechanism]string{
	MechanismNone:        "none",
	MechanismPrimary:     "primary",
	MechanismSecondary:   "secondary",
	MechanismGraphQLCost: "graphql_cost",
}

var mechanisms = func() []Mechanism {
	out := make([]Mechanism, 0, len(mechanismNames))
	for m := range mechanismNames {
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}()

// Mechanisms returns the closed mechanism vocabulary, in ascending order, as a
// copy. A caller cannot narrow or extend it by mutating the result.
func Mechanisms() []Mechanism { return slices.Clone(mechanisms) }

// Valid reports whether m is in the closed vocabulary.
func (m Mechanism) Valid() bool {
	_, ok := mechanismNames[m]
	return ok
}

// Limited reports whether m names an actual refusal — every mechanism except
// [MechanismNone]. It exists so a caller writes one predicate instead of
// enumerating three constants and forgetting the one added next.
func (m Mechanism) Limited() bool { return m.Valid() && m != MechanismNone }

// String renders m's stable log name. A value outside the vocabulary renders
// as invalid_mechanism(N) rather than as "none", so it cannot hide behind the
// zero value in a message.
func (m Mechanism) String() string {
	if name, ok := mechanismNames[m]; ok {
		return name
	}
	return "invalid_mechanism(" + strconv.Itoa(int(m)) + ")"
}

// An Error reports that a rate limit was not cleared within the attempts
// allowed, naming WHICH mechanism refused and WHEN it is expected to clear.
//
// It is a distinct type rather than a bare error for the reason the whole
// package exists: "rate limited" alone tells an operator nothing actionable,
// while "secondary, 3 attempts, clears at 14:32Z" tells them whether to wait
// or to reduce concurrency — two opposite remedies for two mechanisms that
// look identical from the outside.
//
// Its Unwrap exposes [cerr.KindRateLimited], so host code that only
// classifies — cerr.KindOf, cerr.TripsBreaker — gets the right answers without
// knowing this type exists. In particular TripsBreaker is true: the backend
// itself reported the refusal, which is exactly the positive evidence a
// breaker is meant to open on.
type Error struct {
	// Op is the operation the caller named, e.g. "ListProjectItems".
	Op string
	// Mechanism is which limit refused. It is never [MechanismNone]: an Error
	// is only built from a refusal.
	Mechanism Mechanism
	// Attempts is how many attempts were made in total, including the first.
	Attempts int
	// RetryAt is when the mechanism is expected to clear, in the [Clock]'s
	// time. It is the zero Time when the mechanism named no reset — a
	// secondary limit with no retry-after does not say when it ends, and
	// inventing a time would be worse than admitting that.
	RetryAt time.Time
	// Err is the underlying cause, where there is one distinct from the
	// refusal itself (a context cancellation during the wait, say).
	Err error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s: github %s rate limit not cleared in %d attempt(s)",
		e.opOrDefault(), e.Mechanism, e.Attempts)
	if !e.RetryAt.IsZero() {
		msg += "; expected to clear at " + e.RetryAt.UTC().Format(time.RFC3339)
	} else {
		msg += "; the limit named no reset time"
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) opOrDefault() string {
	if e.Op == "" {
		return "a github request"
	}
	return e.Op
}

// Unwrap exposes the refusal to the cerr taxonomy AND keeps the underlying
// cause reachable, so errors.Is against a context error still works through a
// wait that was cancelled.
func (e *Error) Unwrap() error {
	return cerr.New(cerr.KindRateLimited, e.Op, e.Err)
}
