package githubratelimit_test

import (
	"net/http"
	"net/textproto"
	"strconv"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/githubratelimit"
)

// resetUnix is a fixed reset instant every primary fixture shares, so a test
// asserting a wait asserts arithmetic rather than the clock.
var resetUnix = time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)

func primaryHeader(remaining, limit, used int, reset time.Time, resource string) http.Header {
	h := http.Header{}
	h.Set(githubratelimit.HeaderRemaining, itoa(remaining))
	h.Set(githubratelimit.HeaderLimit, itoa(limit))
	h.Set(githubratelimit.HeaderUsed, itoa(used))
	h.Set(githubratelimit.HeaderReset, itoa(int(reset.Unix())))
	if resource != "" {
		h.Set(githubratelimit.HeaderResource, resource)
	}
	return h
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestParsePrimaryReadsEveryHeaderItIsGiven(t *testing.T) {
	h := primaryHeader(4321, 5000, 679, resetUnix, "core")
	b, ok := githubratelimit.ParsePrimary(h)
	if !ok {
		t.Fatalf("ParsePrimary reported no budget for a complete header set")
	}
	if !b.Known() {
		t.Fatalf("a budget read off real headers must report Known()")
	}
	if b.Remaining != 4321 || b.Limit != 5000 || b.Used != 679 {
		t.Fatalf("counters = remaining %d limit %d used %d", b.Remaining, b.Limit, b.Used)
	}
	if !b.Reset.Equal(resetUnix) {
		t.Fatalf("Reset = %v, want %v", b.Reset, resetUnix)
	}
	if b.Resource != "core" {
		t.Fatalf("Resource = %q, want core", b.Resource)
	}
	if b.Exhausted() {
		t.Fatalf("a budget with 4321 remaining is not exhausted")
	}
}

// TestUnknownPrimaryBudgetIsNeitherFullNorEmpty is the fail-closed core of the
// type. The zero value must not read as a healthy budget — that would make
// pre-emption silently stop working — and must not read as an exhausted one
// either, which would stall a host on no evidence at all.
func TestUnknownPrimaryBudgetIsNeitherFullNorEmpty(t *testing.T) {
	var b githubratelimit.PrimaryBudget
	if b.Known() {
		t.Fatalf("the zero PrimaryBudget must not report Known()")
	}
	if b.Exhausted() {
		t.Fatalf("an unmeasured budget must not report Exhausted(): there is no evidence of exhaustion")
	}
	if got := b.Headroom(0); got != 0 {
		t.Fatalf("Headroom on an unmeasured budget = %d, want 0: there is no evidence of room either", got)
	}
	if got := b.ResetIn(resetUnix); got != 0 {
		t.Fatalf("ResetIn on a budget with no reset = %v, want 0", got)
	}
}

// header builds a header through Set, which is the only way to get the
// canonical keys a real *http.Response carries.
//
// An earlier revision of this test built http.Header map literals directly,
// keyed on the constants' literal spelling — "X-RateLimit-Remaining". Get
// canonicalises its lookup to "X-Ratelimit-Remaining", so it found nothing, and
// every case below passed while asserting nothing about the incompleteness it
// claimed to be testing. That is why the falsifier at the end of this test
// exists: it fails if a case is refused for the wrong reason.
func header(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

// TestParsePrimaryRefusesAnIncompleteHeaderSet pins the requirement that BOTH
// decision-bearing headers are present. A remaining with no reset would let a
// gate conclude "exhausted" with no idea how long to wait, and the only wait
// derivable from it is none — a spin straight back into the limit.
func TestParsePrimaryRefusesAnIncompleteHeaderSet(t *testing.T) {
	goodReset := itoa(int(resetUnix.Unix()))
	cases := []struct {
		name   string
		header http.Header
		// completed is the same header with the ONE defect repaired. Every case
		// must parse once repaired, which is what proves the case was refused
		// for the reason it names rather than for a typo.
		completed http.Header
	}{
		{"nil header", nil, header(githubratelimit.HeaderRemaining, "0", githubratelimit.HeaderReset, goodReset)},
		{"no headers at all", header(), header(githubratelimit.HeaderRemaining, "0", githubratelimit.HeaderReset, goodReset)},
		{
			"remaining without a reset",
			header(githubratelimit.HeaderRemaining, "0"),
			header(githubratelimit.HeaderRemaining, "0", githubratelimit.HeaderReset, goodReset),
		},
		{
			"reset without a remaining",
			header(githubratelimit.HeaderReset, goodReset),
			header(githubratelimit.HeaderReset, goodReset, githubratelimit.HeaderRemaining, "7"),
		},
		{
			"unparseable remaining",
			header(githubratelimit.HeaderRemaining, "unknown", githubratelimit.HeaderReset, goodReset),
			header(githubratelimit.HeaderRemaining, "7", githubratelimit.HeaderReset, goodReset),
		},
		{
			"unparseable reset",
			header(githubratelimit.HeaderRemaining, "0", githubratelimit.HeaderReset, "soon"),
			header(githubratelimit.HeaderRemaining, "0", githubratelimit.HeaderReset, goodReset),
		},
		{
			"empty remaining",
			header(githubratelimit.HeaderRemaining, "", githubratelimit.HeaderReset, goodReset),
			header(githubratelimit.HeaderRemaining, "0", githubratelimit.HeaderReset, goodReset),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, ok := githubratelimit.ParsePrimary(tc.header)
			if ok {
				t.Fatalf("ParsePrimary reported a budget: %+v", b)
			}
			if b.Known() {
				t.Fatalf("the returned budget must be UNKNOWN, not a zero-valued known one")
			}
			// The falsifier: repairing the one named defect must make it parse.
			if _, ok := githubratelimit.ParsePrimary(tc.completed); !ok {
				t.Fatalf("the repaired header still does not parse, so the case above was refused for some other reason than %q", tc.name)
			}
		})
	}
}

// TestParsePrimaryDoesNotReadRetryAfter is the anti-collapse test at the parsing
// layer: retry-after is the SECONDARY mechanism's signal, and a primary parser
// that folded it in would be the conflation the package exists to prevent.
func TestParsePrimaryDoesNotReadRetryAfter(t *testing.T) {
	alone := http.Header{}
	alone.Set(githubratelimit.HeaderRetryAfter, "60")
	if b, ok := githubratelimit.ParsePrimary(alone); ok {
		t.Fatalf("a retry-after alone produced a primary budget: %+v", b)
	}

	// The case above would pass even if the parser DID read retry-after, since
	// remaining is absent either way. This is the one with teeth: a COMPLETE
	// primary header set alongside a retry-after that disagrees with the reset.
	// Reset must come from x-ratelimit-reset, and the retry-after must be absent
	// from the budget entirely.
	both := primaryHeader(0, 5000, 5000, resetUnix, "core")
	both.Set(githubratelimit.HeaderRetryAfter, "60")
	b, ok := githubratelimit.ParsePrimary(both)
	if !ok {
		t.Fatalf("a complete header set must parse")
	}
	if !b.Reset.Equal(resetUnix) {
		t.Fatalf("Reset = %v, want the x-ratelimit-reset value %v — not anything derived from retry-after", b.Reset, resetUnix)
	}
	if got := b.ResetIn(resetUnix.Add(-15 * time.Minute)); got != 15*time.Minute {
		t.Fatalf("ResetIn = %v, want 15m from the reset header; the retry-after said 60s and must not have been consulted", got)
	}
}

// TestHeaderConstantsAreCanonicalSoMapIndexingWorks. http.Header is a map whose
// keys Get and Set canonicalise, so a constant spelled the way GitHub documents
// it — "X-RateLimit-Remaining" — is INVISIBLE to Get when a consumer builds the
// map directly. Shipping the canonical spelling is what makes both routes work.
func TestHeaderConstantsAreCanonicalSoMapIndexingWorks(t *testing.T) {
	direct := http.Header{
		githubratelimit.HeaderRemaining: {"0"},
		githubratelimit.HeaderReset:     {itoa(int(resetUnix.Unix()))},
		githubratelimit.HeaderLimit:     {"5000"},
		githubratelimit.HeaderResource:  {githubratelimit.ResourceCore},
	}
	b, ok := githubratelimit.ParsePrimary(direct)
	if !ok {
		t.Fatalf("a header built by INDEXING the map with this package's constants must parse; the constants are not canonical")
	}
	if !b.Exhausted() || b.Limit != 5000 || b.Resource != githubratelimit.ResourceCore {
		t.Fatalf("budget = %+v", b)
	}
	for _, name := range []string{
		githubratelimit.HeaderLimit, githubratelimit.HeaderRemaining, githubratelimit.HeaderReset,
		githubratelimit.HeaderUsed, githubratelimit.HeaderResource, githubratelimit.HeaderRetryAfter,
	} {
		if got := textproto.CanonicalMIMEHeaderKey(name); got != name {
			t.Fatalf("constant %q canonicalises to %q; a consumer indexing the map with it silently reads nothing", name, got)
		}
	}
	// The trap itself, pinned so the reason the constants look the way they do
	// stays visible: GitHub's own documented spelling does NOT work this way.
	documented := http.Header{
		"X-RateLimit-Remaining": {"0"},
		"X-RateLimit-Reset":     {itoa(int(resetUnix.Unix()))},
	}
	if _, ok := githubratelimit.ParsePrimary(documented); ok {
		t.Fatalf("net/http canonicalisation changed: the documented spelling now works through map indexing, so this test's premise is stale")
	}
}

// TestParsePrimaryTreatsMissingLimitAndUsedAsAbsentNotZero — neither changes a
// decision, so they are recorded when present and left alone when not, while
// the budget itself is still known.
func TestParsePrimaryTreatsMissingLimitAndUsedAsAbsentNotZero(t *testing.T) {
	h := http.Header{}
	h.Set(githubratelimit.HeaderRemaining, "12")
	h.Set(githubratelimit.HeaderReset, itoa(int(resetUnix.Unix())))
	b, ok := githubratelimit.ParsePrimary(h)
	if !ok || !b.Known() {
		t.Fatalf("a budget with the two decision headers must be known")
	}
	if b.Limit != 0 || b.Used != 0 || b.Resource != "" {
		t.Fatalf("absent headers should stay at their zero values, got %+v", b)
	}
}

func TestPrimaryHeadroomHoldsBackTheReserve(t *testing.T) {
	b, _ := githubratelimit.ParsePrimary(primaryHeader(50, 5000, 4950, resetUnix, "core"))
	cases := []struct {
		reserve int
		want    int
	}{
		{0, 50},
		{10, 40},
		{50, 0},
		{500, 0},  // clamped, never negative
		{-10, 50}, // a negative reserve is not a bonus
	}
	for _, tc := range cases {
		if got := b.Headroom(tc.reserve); got != tc.want {
			t.Fatalf("Headroom(%d) = %d, want %d", tc.reserve, got, tc.want)
		}
	}
}

func TestPrimaryResetInIsClampedAtZeroForAPassedWindow(t *testing.T) {
	b, _ := githubratelimit.ParsePrimary(primaryHeader(0, 5000, 5000, resetUnix, "core"))
	if got := b.ResetIn(resetUnix.Add(-90 * time.Second)); got != 90*time.Second {
		t.Fatalf("ResetIn = %v, want 90s", got)
	}
	if got := b.ResetIn(resetUnix.Add(time.Hour)); got != 0 {
		t.Fatalf("ResetIn for a window that already passed = %v, want 0: a negative wait runs a backoff backwards", got)
	}
}

// TestPrimaryExhaustionNeedsRemainingAtOrBelowZero also covers the defensive
// case of a negative remaining, which GitHub does not send but which must not
// read as room.
func TestPrimaryExhaustionNeedsRemainingAtOrBelowZero(t *testing.T) {
	spent, _ := githubratelimit.ParsePrimary(primaryHeader(0, 5000, 5000, resetUnix, "core"))
	if !spent.Exhausted() {
		t.Fatalf("remaining 0 must report Exhausted()")
	}
	negative, _ := githubratelimit.ParsePrimary(primaryHeader(-1, 5000, 5001, resetUnix, "core"))
	if !negative.Exhausted() {
		t.Fatalf("a negative remaining must not read as room")
	}
	if got := negative.Headroom(0); got != 0 {
		t.Fatalf("Headroom on a negative remaining = %d, want 0", got)
	}
}

// TestAttributableToAdmitsAnUnlabelledBudgetAndRefusesAForeignBucket pins both
// halves of the bucket rule. Refusing a foreign bucket is what keeps a refused
// search from stalling core work for the rest of the hour; admitting an
// UNLABELLED one is what keeps a server that omits the header from turning
// pre-emption off entirely.
func TestAttributableToAdmitsAnUnlabelledBudgetAndRefusesAForeignBucket(t *testing.T) {
	core, _ := githubratelimit.ParsePrimary(primaryHeader(10, 5000, 4990, resetUnix, "core"))
	search, _ := githubratelimit.ParsePrimary(primaryHeader(1, 30, 29, resetUnix, "search"))
	unlabelled, _ := githubratelimit.ParsePrimary(primaryHeader(10, 5000, 4990, resetUnix, ""))

	if !core.AttributableTo(githubratelimit.ResourceCore) {
		t.Fatalf("a core budget must be attributable to the core bucket")
	}
	if search.AttributableTo(githubratelimit.ResourceCore) {
		t.Fatalf("a search budget must NOT be attributable to core: 30-a-minute folded into 5000-an-hour stalls every core request")
	}
	if !unlabelled.AttributableTo(githubratelimit.ResourceCore) {
		t.Fatalf("an unlabelled budget must be attributable, or a server that omits the header records no budget ever")
	}
	if !search.AttributableTo("search") {
		t.Fatalf("a search budget must be attributable to the search bucket")
	}
}
