package githubratelimit

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// secondaryBodyMarkers are the phrases GitHub puts in a refused response's body
// when a SECONDARY limit is the reason.
//
// Matching on prose is exactly what the cerr taxonomy exists to stop host code
// doing, and it is a deliberate, bounded exception here for one reason: GitHub publishes
// no typed signal for a secondary limit. The reliable signals are a
// retry-after header and a 429 status, both of which [Classify] checks FIRST
// and neither of which is prose. These phrases are the last resort, for the
// documented case of a 403 that carries neither.
//
// The failure mode if GitHub rewords them is bounded and safe: the refusal
// classifies as [MechanismNone], the caller's 403 is returned as the failure it
// is, and the call FAILS. It does not silently succeed, and it does not retry
// into a limit forever.
var secondaryBodyMarkers = []string{
	"secondary rate limit",
	"abuse detection",
}

// refusalStatuses are the status codes on which GitHub refuses a request for
// rate-limit reasons.
//
// A 200 is absent on purpose and that is not an omission: the GraphQL point
// budget refuses ON a 200, which is why [Classify] checks the body for
// [RefusedForPoints] independently of the status code. What the status set
// gates is the HEADER-based reading — retry-after on a successful response
// says nothing about a limit, and a 200 whose x-ratelimit-remaining has reached
// zero is the request that spent the last unit, not one that was refused.
var refusalStatuses = []int{http.StatusForbidden, http.StatusTooManyRequests}

// MaxRetryAfter is the longest wait a retry-after header is believed for.
//
// GitHub's primary window is an hour and its secondary limits clear in minutes,
// so nothing longer than a day is a rate limit GitHub has. The bound exists
// because the alternative is not merely an absurd wait but a WRONG one:
// time.Duration counts nanoseconds in an int64, so `retry-after: 9300000000`
// multiplied by time.Second overflows and comes back NEGATIVE — and a negative
// wait is silently discarded, throwing away the very signal the header carried.
// A value just under the overflow point is worse still: it is a positive,
// believable-looking 146 years.
//
// Clamping DOWN rather than rejecting the header is the safe direction. A
// rejected header would stop being a secondary marker at all, and the refusal
// could then classify as nothing — a call returned as a bare 403 with no
// mechanism named. Clamped, the mechanism is still reported and the wait is
// merely capped, which a context deadline would bound anyway.
const MaxRetryAfter = 24 * time.Hour

// maxRetryAfterSeconds is MaxRetryAfter in whole seconds, compared against the
// header's integer form BEFORE it is multiplied out — after the multiplication
// an overflow is indistinguishable from a legitimate value.
const maxRetryAfterSeconds = int(MaxRetryAfter / time.Second)

// An Observation is everything one attempt saw that could carry a rate-limit
// signal. A caller fills in whichever fields its transport actually exposes;
// [Classify] reads what is there and reports what it could not determine as
// unknown rather than as healthy.
type Observation struct {
	// Status is the HTTP status code. Zero means the caller did not record one
	// — a transport that never got a response, say — and no header-based
	// reading fires.
	Status int
	// Header is the response's headers. It may be nil.
	Header http.Header
	// Body is the response body, already read by the caller. It may be nil;
	// the GraphQL signals are the only ones that need it.
	Body []byte
	// Points is a point budget the caller ALREADY decoded from a typed GraphQL
	// result — see [NewPointBudget]. When it is known, [Classify] uses it and
	// does not re-parse Body for a budget. When it is unknown, Classify falls
	// back to [ParsePointBudget] over Body.
	Points PointBudget
}

// FromHTTP builds an Observation from a response and the body a caller has
// already read off it.
//
// It takes the body separately rather than reading resp.Body itself, because a
// response body can be read once: a helper that consumed it would leave the
// caller unable to decode its own payload, and one that buffered and replaced
// it would silently change the caller's memory profile on every request.
//
// A nil resp yields the zero Observation, in which nothing is known.
func FromHTTP(resp *http.Response, body []byte) Observation {
	if resp == nil {
		return Observation{Body: body}
	}
	return Observation{Status: resp.StatusCode, Header: resp.Header, Body: body}
}

// A Verdict is [Classify]'s reading of one Observation: which mechanism refused
// the request, how long the refusal itself says to wait, and both budgets as
// they were reported.
type Verdict struct {
	// Mechanism is which limit refused, or [MechanismNone] if none did.
	//
	// When more than one marker is present it is the most SPECIFIC of them —
	// see [Classify] for the precedence and why it runs that way. Signals
	// records all of them.
	Mechanism Mechanism
	// RetryAfter is how long to wait before retrying, as the refusal itself
	// dictates. It is the LONGEST wait any observed marker called for, so a
	// response carrying two limits satisfies both.
	//
	// It is zero when the mechanism named no wait — a secondary limit with no
	// retry-after header does not say when it ends. A caller MUST NOT read
	// zero as "retry immediately": that is the case where a jittered backoff
	// is required, and [Gate] supplies one.
	RetryAfter time.Duration
	// RetryAt is when the refusal is expected to clear, in the clock's time.
	// Zero exactly when RetryAfter is zero.
	RetryAt time.Time
	// Primary is the hourly quota as this response reported it — known or not,
	// and recorded whether or not the request was refused. This is the field
	// that makes the pre-emptive check possible: see [Gate.Admit].
	Primary PrimaryBudget
	// Points is the GraphQL point budget as this response reported it, known or
	// not.
	Points PointBudget
	// Signals is every mechanism whose marker was present, in ascending order,
	// not only the one Mechanism reports.
	//
	// It exists because the mechanisms genuinely co-occur — a 429 can arrive
	// with the hourly quota also spent — and a caller or a test that only ever
	// sees the winner cannot tell a correctly-prioritised reading from one that
	// never noticed the second signal.
	Signals []Mechanism
}

// Limited reports whether the verdict is a rate-limit refusal at all.
func (v Verdict) Limited() bool { return v.Mechanism.Limited() }

// Classify reads one Observation and reports which of GitHub's three
// mechanisms refused it.
//
// # Detection, per mechanism
//
// SECONDARY, in the order checked: a retry-after header (seconds, or an HTTP
// date) on a refusal status; a 429 status, which GitHub uses for secondary
// limits and which is a typed signal rather than prose; or a refusal status
// whose body names a secondary rate limit or an abuse-detection trigger.
//
// GRAPHQL POINT COST: an errors entry of type RATE_LIMITED in the body — see
// [RefusedForPoints] — checked WITHOUT regard to the status code, because this
// refusal arrives on HTTP 200.
//
// PRIMARY: a refusal status whose x-ratelimit-remaining has reached zero. A
// remaining of zero on a SUCCESSFUL response is not a refusal — it is the
// request that spent the last unit — and it is reported through Primary, where
// [Gate.Admit] acts on it before the next request is ever made.
//
// # Precedence, and why it runs this way
//
// Secondary beats everything. It is the only mechanism whose remedy is not
// derivable from a budget: waiting out the primary reset, or the point reset,
// does NOT clear a secondary limit. So mis-reading a secondary refusal as one
// of the other two under-waits and is refused again, while the reverse merely
// over-waits. Between a wrong answer and a slow one, this package picks slow.
//
// Point cost beats primary. Both may be signalled on one response — a
// point-refused GraphQL query is billed against the graphql quota bucket, whose
// headers can read exhausted on the same response — and the operator's remedy
// differs: make the query cheaper versus make fewer requests. Reporting the
// specific one is what makes the message actionable.
//
// RetryAfter is not chosen by precedence. It is the LONGEST wait any observed
// marker dictated, so a response carrying two limits waits long enough for
// both. That is the same over-wait-rather-than-under-wait choice, applied to
// the duration instead of the name.
//
// # What it does NOT do
//
// It does not classify anything else. A 403 for a missing scope, a 404, a 500,
// a transport error — all of them are [MechanismNone], and a caller must return
// them as the failures they are. This package retries rate limits, not
// requests: a general retry wrapper that also swallowed a 500 would turn a
// broken backend into a slow one.
func Classify(obs Observation, now time.Time) Verdict {
	v := Verdict{}
	if budget, ok := ParsePrimary(obs.Header); ok {
		v.Primary = budget
	}
	// One decode of the body serves both GraphQL signals — see graphQLSignals.
	bodyBudget, bodyKnown, refusedForPoints := graphQLSignals(obs.Body)
	v.Points = obs.Points
	if !v.Points.Known() && bodyKnown {
		v.Points = bodyBudget
	}

	refusal := slices.Contains(refusalStatuses, obs.Status)
	var waits []time.Duration

	if d, ok := secondaryWait(obs, v.Primary, now, refusal); ok {
		v.Signals = append(v.Signals, MechanismSecondary)
		if d > 0 {
			waits = append(waits, d)
		}
	}
	if refusedForPoints {
		v.Signals = append(v.Signals, MechanismGraphQLCost)
		if d := v.Points.ResetIn(now); d > 0 {
			waits = append(waits, d)
		}
	}
	if refusal && v.Primary.Exhausted() {
		v.Signals = append(v.Signals, MechanismPrimary)
		if d := v.Primary.ResetIn(now); d > 0 {
			waits = append(waits, d)
		}
	}

	if len(v.Signals) == 0 {
		return v
	}
	// Ascending order puts the closed vocabulary's declaration order on the
	// slice, so Signals reads the same way Mechanisms() does.
	slices.Sort(v.Signals)
	v.Mechanism = mostSpecific(v.Signals)
	if len(waits) > 0 {
		v.RetryAfter = slices.Max(waits)
		v.RetryAt = now.Add(v.RetryAfter)
	}
	return v
}

// specificity orders the mechanisms by how specific their remedy is, highest
// first. It is deliberately separate from the Mechanism constants' numeric
// order: the constants are declared in the order the three are explained, and
// tying precedence to that would make reordering the documentation change the
// behaviour.
var specificity = map[Mechanism]int{
	MechanismSecondary:   3,
	MechanismGraphQLCost: 2,
	MechanismPrimary:     1,
}

// mostSpecific picks the winning mechanism from the markers that fired. Callers
// only reach it with a non-empty set.
func mostSpecific(signals []Mechanism) Mechanism {
	best := MechanismNone
	bestRank := 0
	for _, m := range signals {
		if rank := specificity[m]; rank > bestRank {
			best, bestRank = m, rank
		}
	}
	return best
}

// secondaryWait reports whether a secondary limit refused this response and, if
// so, the wait it named — which may be zero, meaning it named none.
//
// The markers are checked most-reliable first. A retry-after header carries a
// wait; the 429 status and the body phrases do not, and a caller must back off
// with jitter instead of retrying immediately.
//
// The 429 marker is conditional, and the condition matters: GitHub answers a
// PRIMARY refusal with 403 **or 429**, so a bare 429 whose x-ratelimit-remaining
// has reached zero is the hourly quota's own refusal, not an abuse-detection
// one. Reading it as secondary would compute the right wait — the reset is the
// longest observed wait either way — and tell the operator the wrong REMEDY:
// reduce burst and concurrency, when what is actually needed is fewer requests
// in the hour. So a bare 429 is a secondary marker only in the absence of
// positive primary evidence. A 429 that carries a retry-after or the secondary
// prose is still secondary, quota or no quota.
func secondaryWait(obs Observation, primary PrimaryBudget, now time.Time, refusal bool) (time.Duration, bool) {
	if refusal {
		if d, ok := retryAfter(obs.Header, now); ok {
			return d, true
		}
		if bodyNamesSecondaryLimit(obs.Body) {
			return 0, true
		}
	}
	if obs.Status == http.StatusTooManyRequests && !primary.Exhausted() {
		return 0, true
	}
	return 0, false
}

// retryAfter reads the Retry-After header in both forms RFC 9110 permits:
// delay-seconds, and an HTTP date.
//
// A header that is present but parses as neither is NOT a marker. Treating an
// unparseable retry-after as "wait zero" would report a secondary limit and
// then retry into it with no delay, which is worse than not recognising it —
// the other two markers still get their chance, and failing to recognise it at
// all still fails the call rather than succeeding.
//
// A wait in the past clamps to zero rather than going negative, so a stale
// date cannot compute a backoff that runs backwards. A wait beyond
// [MaxRetryAfter] clamps down to it — see there for why clamping rather than
// rejecting is the safe direction.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	if h == nil {
		return 0, false
	}
	raw := strings.TrimSpace(h.Get(HeaderRetryAfter))
	if raw == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs < 0 {
			return 0, true
		}
		if secs > maxRetryAfterSeconds {
			return MaxRetryAfter, true
		}
		return time.Duration(secs) * time.Second, true
	}
	if when, err := http.ParseTime(raw); err == nil {
		// Sub saturates at the Duration bounds rather than wrapping, so this
		// direction cannot go negative on a far-future date — but it can still
		// return centuries, which the clamp below refuses to sleep out.
		d := when.Sub(now)
		if d < 0 {
			return 0, true
		}
		return min(d, MaxRetryAfter), true
	}
	return 0, false
}

// bodyNamesSecondaryLimit reports whether the body carries one of the
// documented secondary-limit phrases, case-insensitively.
func bodyNamesSecondaryLimit(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	lower := strings.ToLower(string(body))
	for _, marker := range secondaryBodyMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
