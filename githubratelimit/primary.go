package githubratelimit

import (
	"net/http"
	"strconv"
	"time"
)

// The primary-quota headers GitHub puts on every response, refused or not.
//
// They are spelled in net/http's CANONICAL form — "X-Ratelimit-Remaining", with
// a lower-case "l", not the "X-RateLimit-Remaining" GitHub's own documentation
// uses. That is not a typo, and getting it wrong is a silent failure rather than
// a loud one.
//
// http.Header is a map whose keys are canonicalised by Get and Set, so both
// spellings behave identically through those methods and only ONE works when a
// consumer indexes or builds the map directly:
//
//	h := http.Header{githubratelimit.HeaderRemaining: {"0"}, ...}  // works
//	h["X-RateLimit-Remaining"] = []string{"0"}                     // silently invisible to Get
//
// An earlier revision of this package's own tests built fixture headers the
// second way and passed while asserting nothing — which is exactly the reason
// the canonical spelling ships here rather than the documentation's.
const (
	HeaderLimit     = "X-Ratelimit-Limit"
	HeaderRemaining = "X-Ratelimit-Remaining"
	HeaderReset     = "X-Ratelimit-Reset"
	HeaderUsed      = "X-Ratelimit-Used"
	// HeaderResource names WHICH quota bucket the other four describe —
	// "core", "search", "graphql", and others. It matters because the buckets
	// are independent and small ones exhaust fast: search is 30 requests a
	// minute against core's 5000 an hour. A handler that folds them into one
	// number stalls all core work for an hour the first time a search is
	// refused. See [Options.Resource].
	HeaderResource = "X-Ratelimit-Resource"
	// HeaderRetryAfter is the SECONDARY mechanism's signal, not the primary
	// one. It is named here because [ParsePrimary] deliberately does not read
	// it — see [Classify].
	HeaderRetryAfter = "Retry-After"
)

// ResourceCore is the bucket GitHub bills ordinary REST calls against, and the
// bucket a [Gate] assumes when nothing else is said.
const ResourceCore = "core"

// A PrimaryBudget is the hourly quota as one response reported it.
//
// It is a value, not a pointer, and its zero value is DELIBERATELY not a
// healthy budget: [PrimaryBudget.Known] reports false for it. That distinction
// is the whole reason this is a struct rather than two ints — a response whose
// headers could not be read must not be indistinguishable from one reporting
// 5000 remaining, or the pre-emptive check in [Gate.Admit] silently stops
// checking anything.
type PrimaryBudget struct {
	// Limit is the bucket's size for this window.
	Limit int
	// Remaining is how many requests are left in it.
	Remaining int
	// Used is how many have been spent, where GitHub reported it.
	Used int
	// Reset is when the window rolls over and Remaining returns to Limit.
	Reset time.Time
	// Resource names the bucket — see [HeaderResource]. It is empty when the
	// response did not say, which older GitHub Enterprise versions do not.
	Resource string
	// known records that this budget was actually read from a response rather
	// than default-constructed. It is unexported so that a caller cannot
	// fabricate a known budget by setting a field; [ParsePrimary] is the only
	// thing that sets it.
	known bool
}

// Known reports whether this budget was read from a response's headers.
//
// An unknown budget is not a full one and not an empty one — it is no
// information, and every predicate below treats it that way:
// [PrimaryBudget.Exhausted] is false (there is no evidence of exhaustion) and
// [PrimaryBudget.Headroom] is zero (there is no evidence of room). The
// asymmetry is intentional. Refusing to pre-emptively
// wait on no evidence keeps a missing header from stalling a host forever,
// while refusing to CLAIM room on no evidence keeps a sequence from being
// admitted against a budget nobody measured.
func (b PrimaryBudget) Known() bool { return b.known }

// Exhausted reports POSITIVE evidence that the hourly quota is spent: a known
// budget with nothing remaining. An unknown budget is never exhausted — see
// [PrimaryBudget.Known].
func (b PrimaryBudget) Exhausted() bool { return b.known && b.Remaining <= 0 }

// Headroom reports how many requests may be spent while keeping reserve in
// hand, floored at zero. An unknown budget has no headroom, so a caller asking
// "may I run 40 mutations" against an unmeasured budget is told to find out
// first rather than being waved through.
func (b PrimaryBudget) Headroom(reserve int) int {
	if !b.known {
		return 0
	}
	if reserve < 0 {
		reserve = 0
	}
	room := b.Remaining - reserve
	if room < 0 {
		return 0
	}
	return room
}

// ResetIn reports how long until the window rolls over, as measured from now,
// floored at zero. A budget with no reset time reports zero: there is nothing
// to wait for that this budget knows about.
func (b PrimaryBudget) ResetIn(now time.Time) time.Duration {
	if b.Reset.IsZero() {
		return 0
	}
	d := b.Reset.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// AttributableTo reports whether b may be recorded as the state of the named
// bucket — either because it says it IS that bucket, or because it does not say
// which bucket it is at all.
//
// The unlabelled case is admitted deliberately, and it is the one place this
// package prefers a possible mis-attribution to a certain fail-open. GitHub
// Enterprise versions that predate x-ratelimit-resource label nothing, so
// refusing an unlabelled budget would mean a [Gate] against such a server
// records no budget ever and pre-emption silently stops working — the exact
// "green because nothing looked" failure. On a server that DOES label, every
// budget is labelled, so the ambiguity does not arise there.
func (b PrimaryBudget) AttributableTo(resource string) bool {
	return b.Resource == "" || b.Resource == resource
}

// ParsePrimary reads the primary-quota headers off a response.
//
// It reports false — and returns the zero, UNKNOWN budget — unless the two
// headers the quota is actually decided from are both present and parse:
// x-ratelimit-remaining and x-ratelimit-reset. Limit, Used and Resource are
// recorded when present and left at their zero values when not, because none
// of them changes a decision; remaining and reset both do.
//
// Requiring BOTH is the fail-closed reading. A response carrying remaining
// without a reset would let [Gate.Admit] conclude "exhausted" with no idea how
// long to wait, and the only wait it could then compute is none — a spin
// straight back into the limit. Treating that as "no information" instead sends
// the caller to the one thing that does resolve it: making the request and
// reading a complete set of headers off the answer.
//
// It deliberately does NOT read retry-after. That header is the secondary
// mechanism's, and folding it in here is the exact collapse this package
// exists to prevent — see [Classify].
func ParsePrimary(h http.Header) (PrimaryBudget, bool) {
	if h == nil {
		return PrimaryBudget{}, false
	}
	remaining, ok := headerInt(h, HeaderRemaining)
	if !ok {
		return PrimaryBudget{}, false
	}
	resetUnix, ok := headerInt(h, HeaderReset)
	if !ok {
		return PrimaryBudget{}, false
	}
	b := PrimaryBudget{
		Remaining: remaining,
		Reset:     time.Unix(int64(resetUnix), 0).UTC(),
		Resource:  h.Get(HeaderResource),
		known:     true,
	}
	if limit, ok := headerInt(h, HeaderLimit); ok {
		b.Limit = limit
	}
	if used, ok := headerInt(h, HeaderUsed); ok {
		b.Used = used
	}
	return b, true
}

// headerInt reads a header as a base-10 integer. A missing header and an
// unparseable one are the same answer — no value — because a header carrying
// "unknown" or an empty string is not a number, and coercing it to 0 would
// read as "nothing remaining" on the one header where that is a decision.
func headerInt(h http.Header, name string) (int, bool) {
	raw := h.Get(name)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}
