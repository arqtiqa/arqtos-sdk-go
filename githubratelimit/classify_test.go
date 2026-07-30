package githubratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/githubratelimit"
)

// classifyNow is the fixed "now" every classification fixture is read against,
// chosen so the primary reset is 15 minutes out and the point reset 45. Nothing
// here reads a real clock, so nothing here can measure one.
var classifyNow = resetUnix.Add(-15 * time.Minute)

const (
	// primaryRefusalBody is what GitHub returns when the HOURLY quota is spent.
	// It names a rate limit but NOT a secondary one, which is the distinction
	// the body markers turn on.
	primaryRefusalBody = `{"message":"API rate limit exceeded for user ID 0.",` +
		`"documentation_url":"https://docs.github.com/rest/overview/rate-limits-for-the-rest-api"}`
	// secondaryRefusalBody is the phrasing GitHub uses for a secondary limit.
	secondaryRefusalBody = `{"message":"You have exceeded a secondary rate limit. ` +
		`Please wait a few minutes before you try again."}`
	// abuseRefusalBody is the older phrasing for the same mechanism.
	abuseRefusalBody = `{"message":"You have triggered an abuse detection mechanism. ` +
		`Please wait a few minutes before you try again."}`
	// missingScopeBody is a 403 that is NOT a rate limit at all. It must
	// classify as none, or a missing permission becomes a five-attempt stall.
	missingScopeBody = `{"message":"Resource not accessible by personal access token",` +
		`"documentation_url":"https://docs.github.com/rest"}`
	// graphQLRefusedWithBudgetBody is a point refusal that also reports the
	// budget, so the wait is derivable from the response.
	graphQLRefusedWithBudgetBody = `{"data":{"rateLimit":{"limit":5000,"cost":1400,` +
		`"remaining":0,"used":5000,"nodeCount":0,"resetAt":"2026-07-30T15:30:00Z"}},` +
		`"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`
)

// secondaryHeader is a HEALTHY primary budget plus an optional retry-after. The
// healthy budget is the whole point: on a secondary refusal the hourly quota
// reads fine, so a handler that "waits for the primary reset" computes a wait of
// nothing and retries straight back into the limit.
func secondaryHeader(retryAfter string) http.Header {
	h := primaryHeader(4999, 5000, 1, resetUnix, "core")
	if retryAfter != "" {
		h.Set(githubratelimit.HeaderRetryAfter, retryAfter)
	}
	return h
}

// TestTheThreeMechanismsAreDistinguished is the acceptance test the whole package
// exists for: three fixtures, three mechanisms, three different responses.
//
// It asserts the responses are DIFFERENT, not merely that each is right in
// isolation — a handler that collapsed the three would still pass a set of
// single-fixture assertions if the collapsed answer happened to satisfy each.
func TestTheThreeMechanismsAreDistinguished(t *testing.T) {
	cases := []struct {
		name           string
		obs            githubratelimit.Observation
		wantMechanism  githubratelimit.Mechanism
		wantRetryAfter time.Duration
		// wantPrimaryExhausted records whether the HOURLY quota was actually
		// spent on this fixture. It is false for both secondary cases, which is
		// what makes "back off the primary way" wrong for them.
		wantPrimaryExhausted bool
	}{
		{
			name: "primary: the hourly quota is spent, and the reset says when it returns",
			obs: githubratelimit.Observation{
				Status: http.StatusForbidden,
				Header: primaryHeader(0, 5000, 5000, resetUnix, "core"),
				Body:   []byte(primaryRefusalBody),
			},
			wantMechanism:        githubratelimit.MechanismPrimary,
			wantRetryAfter:       15 * time.Minute,
			wantPrimaryExhausted: true,
		},
		{
			name: "secondary: retry-after dictates the wait while the hourly quota reads healthy",
			obs: githubratelimit.Observation{
				Status: http.StatusForbidden,
				Header: secondaryHeader("60"),
				Body:   []byte(secondaryRefusalBody),
			},
			wantMechanism:  githubratelimit.MechanismSecondary,
			wantRetryAfter: 60 * time.Second,
		},
		{
			name: "secondary with no retry-after: no wait is dictated, so the caller must jitter",
			obs: githubratelimit.Observation{
				Status: http.StatusForbidden,
				Header: secondaryHeader(""),
				Body:   []byte(abuseRefusalBody),
			},
			wantMechanism:  githubratelimit.MechanismSecondary,
			wantRetryAfter: 0,
		},
		{
			name: "graphql point cost: refused on a 200, with the wait in the body and not in any header",
			obs: githubratelimit.Observation{
				Status: http.StatusOK,
				Body:   []byte(graphQLRefusedWithBudgetBody),
			},
			wantMechanism:  githubratelimit.MechanismGraphQLCost,
			wantRetryAfter: 45 * time.Minute,
		},
	}

	seen := map[githubratelimit.Mechanism]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := githubratelimit.Classify(tc.obs, classifyNow)
			if v.Mechanism != tc.wantMechanism {
				t.Fatalf("Mechanism = %v, want %v", v.Mechanism, tc.wantMechanism)
			}
			if !v.Limited() {
				t.Fatalf("a refusal must report Limited()")
			}
			if v.RetryAfter != tc.wantRetryAfter {
				t.Fatalf("RetryAfter = %v, want %v", v.RetryAfter, tc.wantRetryAfter)
			}
			if tc.wantRetryAfter > 0 {
				if want := classifyNow.Add(tc.wantRetryAfter); !v.RetryAt.Equal(want) {
					t.Fatalf("RetryAt = %v, want %v", v.RetryAt, want)
				}
			} else if !v.RetryAt.IsZero() {
				t.Fatalf("RetryAt = %v, want the zero Time when no wait was dictated", v.RetryAt)
			}
			if got := v.Primary.Exhausted(); got != tc.wantPrimaryExhausted {
				t.Fatalf("Primary.Exhausted() = %v, want %v", got, tc.wantPrimaryExhausted)
			}
			seen[v.Mechanism] = true
		})
	}

	// The set of mechanisms these fixtures produced must be exactly the three
	// real ones. If any two collapsed, one of them is missing here.
	for _, m := range githubratelimit.Mechanisms() {
		if m == githubratelimit.MechanismNone {
			continue
		}
		if !seen[m] {
			t.Fatalf("no fixture classified as %v — two mechanisms have collapsed into one", m)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("the fixtures produced %d distinct mechanisms, want 3: %v", len(seen), seen)
	}
}

// TestSecondaryRefusalDoesNotDeriveItsWaitFromThePrimaryBudget states the
// collapse failure directly. On a secondary refusal the primary budget reads
// healthy, so a handler that computed its wait from the budget would compute
// NOTHING and retry into the limit immediately.
func TestSecondaryRefusalDoesNotDeriveItsWaitFromThePrimaryBudget(t *testing.T) {
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusForbidden,
		Header: secondaryHeader("120"),
		Body:   []byte(secondaryRefusalBody),
	}, classifyNow)

	if !v.Primary.Known() {
		t.Fatalf("the primary budget is on the response and must be recorded even on a secondary refusal")
	}
	if v.Primary.Exhausted() {
		t.Fatalf("this fixture's hourly quota is healthy; the test is meaningless if it is not")
	}
	if got := v.Primary.ResetIn(classifyNow); got != 15*time.Minute {
		t.Fatalf("the primary reset is %v away, so a primary-derived wait would be that — the point is that it is NOT used", got)
	}
	if v.RetryAfter != 120*time.Second {
		t.Fatalf("RetryAfter = %v, want the 120s retry-after named", v.RetryAfter)
	}
}

// TestASpentQuotaOnASuccessfulResponseIsNotARefusal is the "before exhaustion,
// not after a failure" half. A 200 whose remaining has reached zero is the
// request that spent the last unit — it succeeded — and the budget it reports is
// what Gate.Admit acts on before the NEXT request is ever made.
func TestASpentQuotaOnASuccessfulResponseIsNotARefusal(t *testing.T) {
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusOK,
		Header: primaryHeader(0, 5000, 5000, resetUnix, "core"),
		Body:   []byte(`{"login":"placeholder"}`),
	}, classifyNow)

	if v.Limited() {
		t.Fatalf("Mechanism = %v: a successful response is not a refusal, whatever its budget says", v.Mechanism)
	}
	if !v.Primary.Exhausted() {
		t.Fatalf("the budget itself must still read as exhausted, or pre-emption has nothing to act on")
	}
	if v.RetryAfter != 0 || !v.RetryAt.IsZero() {
		t.Fatalf("a non-refusal must dictate no wait, got %v / %v", v.RetryAfter, v.RetryAt)
	}
	if len(v.Signals) != 0 {
		t.Fatalf("Signals = %v, want none", v.Signals)
	}
}

// TestA403ThatIsNotARateLimitClassifiesAsNone — this package retries rate
// limits, not requests. A missing scope retried five times is a five-attempt
// stall on a failure that will never clear.
func TestA403ThatIsNotARateLimitClassifiesAsNone(t *testing.T) {
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusForbidden,
		Header: primaryHeader(4321, 5000, 679, resetUnix, "core"),
		Body:   []byte(missingScopeBody),
	}, classifyNow)

	if v.Limited() {
		t.Fatalf("Mechanism = %v, want none: a 403 for a missing scope is not a rate limit", v.Mechanism)
	}
	if !v.Primary.Known() {
		t.Fatalf("the budget on the response is still recorded")
	}
}

// TestOtherFailuresClassifyAsNone covers the rest of the status space in one
// sweep, so a future edit that widened the refusal set to "any 4xx or 5xx" —
// turning a broken backend into a slow one — is caught.
//
// The fixture deliberately carries EVERY header-borne marker: a spent quota and
// a retry-after. An earlier revision used a healthy budget and no retry-after,
// which made the test vacuous — no marker could fire at any status, so the
// status gate was not what held the answer down and widening it would have gone
// unnoticed. Here the only thing keeping each of these from classifying as a
// refusal IS the status set.
func TestOtherFailuresClassifyAsNone(t *testing.T) {
	loaded := primaryHeader(0, 5000, 5000, resetUnix, "core")
	loaded.Set(githubratelimit.HeaderRetryAfter, "60")

	for _, status := range []int{0, 200, 201, 204, 301, 400, 401, 404, 409, 422, 500, 502, 503} {
		v := githubratelimit.Classify(githubratelimit.Observation{
			Status: status,
			Header: loaded,
			Body:   []byte(`{"message":"something else went wrong"}`),
		}, classifyNow)
		if v.Limited() {
			t.Fatalf("status %d classified as %v; only a rate-limit refusal may be retried by this package", status, v.Mechanism)
		}
		if len(v.Signals) != 0 {
			t.Fatalf("status %d fired signals %v", status, v.Signals)
		}
		// The falsifier: the same header at a real refusal status MUST classify,
		// or the sweep above is passing because the fixture is toothless again.
		if refused := githubratelimit.Classify(githubratelimit.Observation{
			Status: http.StatusForbidden,
			Header: loaded,
			Body:   []byte(`{"message":"something else went wrong"}`),
		}, classifyNow); !refused.Limited() {
			t.Fatalf("the fixture does not classify even at 403, so nothing above was suppressed by the status set")
		}
	}
}

// TestA429ThatIsPurelyPrimaryIsReportedAsPrimary. GitHub answers a PRIMARY
// refusal with 403 or 429, so a bare 429 whose quota is spent is the hourly
// limit, not abuse detection. Both readings compute the same wait — the reset —
// and name opposite remedies: fewer requests this hour versus less burst and
// concurrency. The label is what this package sells.
func TestA429ThatIsPurelyPrimaryIsReportedAsPrimary(t *testing.T) {
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusTooManyRequests,
		Header: primaryHeader(0, 5000, 5000, resetUnix, "core"),
		Body:   []byte(primaryRefusalBody),
	}, classifyNow)

	if v.Mechanism != githubratelimit.MechanismPrimary {
		t.Fatalf("Mechanism = %v, want primary — a bare 429 with the quota spent is the primary limit's own refusal", v.Mechanism)
	}
	want := []githubratelimit.Mechanism{githubratelimit.MechanismPrimary}
	if !slices.Equal(v.Signals, want) {
		t.Fatalf("Signals = %v, want %v — the 429 must not fire a secondary marker when the quota explains it", v.Signals, want)
	}
	if v.RetryAfter != 15*time.Minute {
		t.Fatalf("RetryAfter = %v, want 15m", v.RetryAfter)
	}
	// A 429 that carries a secondary marker of its own is still secondary,
	// quota or no quota — otherwise the conditional above would have swallowed
	// the real thing.
	h := primaryHeader(0, 5000, 5000, resetUnix, "core")
	h.Set(githubratelimit.HeaderRetryAfter, "30")
	if got := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusTooManyRequests, Header: h, Body: []byte(secondaryRefusalBody),
	}, classifyNow); got.Mechanism != githubratelimit.MechanismSecondary {
		t.Fatalf("Mechanism = %v, want secondary when the 429 carries a retry-after", got.Mechanism)
	}
}

// TestRetryAfterIsBoundedSoAnOverflowCannotUnderWait is the sharpest of the
// arithmetic traps. time.Duration counts nanoseconds in an int64, so
// `retry-after: 18446744074` multiplied by time.Second WRAPS — and the wrapped
// value is a plausible-looking 290ms. The gate would announce that the mechanism
// dictated a wait, sleep a third of a second, and retry straight into the limit:
// a silent under-wait, dressed as compliance.
func TestRetryAfterIsBoundedSoAnOverflowCannotUnderWait(t *testing.T) {
	cases := []struct {
		name       string
		retryAfter string
	}{
		{"overflows int64 nanoseconds", "18446744074"},
		{"just under the overflow, ~146 years", "4611686019"},
		{"a week", "604800"},
		{"an http date centuries out", "Mon, 02 Jan 3006 15:04:05 GMT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := primaryHeader(4999, 5000, 1, resetUnix, "core")
			h.Set(githubratelimit.HeaderRetryAfter, tc.retryAfter)
			v := githubratelimit.Classify(githubratelimit.Observation{
				Status: http.StatusForbidden, Header: h, Body: []byte(secondaryRefusalBody),
			}, classifyNow)

			if v.Mechanism != githubratelimit.MechanismSecondary {
				t.Fatalf("Mechanism = %v, want secondary: clamping the value must not stop it being a marker", v.Mechanism)
			}
			if v.RetryAfter != githubratelimit.MaxRetryAfter {
				t.Fatalf("RetryAfter = %v, want it clamped to MaxRetryAfter (%v)", v.RetryAfter, githubratelimit.MaxRetryAfter)
			}
			if v.RetryAfter <= 0 {
				t.Fatalf("RetryAfter = %v: a wrapped duration must never reach a caller", v.RetryAfter)
			}
		})
	}
	// The bound is a ceiling, not a floor: an ordinary value passes through.
	h := primaryHeader(4999, 5000, 1, resetUnix, "core")
	h.Set(githubratelimit.HeaderRetryAfter, "60")
	if got := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusForbidden, Header: h, Body: []byte(secondaryRefusalBody),
	}, classifyNow); got.RetryAfter != time.Minute {
		t.Fatalf("RetryAfter = %v, want an unclamped 1m", got.RetryAfter)
	}
}

// TestSecondaryBeatsPrimaryWhenBothAreSignalledAndTheWaitSatisfiesBoth pins the
// precedence AND the max-of-waits rule.
//
// Secondary wins because waiting out the primary reset does not clear it, so
// mis-reading it under-waits; the wait is the LONGER of the two so a response
// carrying both limits satisfies both. Between a wrong answer and a slow one,
// this package picks slow.
func TestSecondaryBeatsPrimaryWhenBothAreSignalledAndTheWaitSatisfiesBoth(t *testing.T) {
	h := primaryHeader(0, 5000, 5000, resetUnix, "core") // quota spent: 15m to reset
	h.Set(githubratelimit.HeaderRetryAfter, "60")        // secondary: 60s
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusTooManyRequests,
		Header: h,
		Body:   []byte(secondaryRefusalBody),
	}, classifyNow)

	if v.Mechanism != githubratelimit.MechanismSecondary {
		t.Fatalf("Mechanism = %v, want secondary — its remedy is the one not derivable from a budget", v.Mechanism)
	}
	want := []githubratelimit.Mechanism{githubratelimit.MechanismPrimary, githubratelimit.MechanismSecondary}
	if !slices.Equal(v.Signals, want) {
		t.Fatalf("Signals = %v, want %v — a reading that reports only the winner cannot be told from one that never saw the second marker", v.Signals, want)
	}
	if v.RetryAfter != 15*time.Minute {
		t.Fatalf("RetryAfter = %v, want 15m — the longer of the two waits, so both limits are satisfied", v.RetryAfter)
	}
}

// TestPointCostBeatsPrimaryWhenBothAreSignalled — both can fire on one response,
// because a point-refused query is billed against the graphql bucket whose
// headers can also read exhausted. The specific one is reported because the
// remedies differ: make the query cheaper versus make fewer requests.
func TestPointCostBeatsPrimaryWhenBothAreSignalled(t *testing.T) {
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusForbidden,
		Header: primaryHeader(0, 5000, 5000, resetUnix, "graphql"),
		Body:   []byte(graphQLRefusedWithBudgetBody),
	}, classifyNow)

	if v.Mechanism != githubratelimit.MechanismGraphQLCost {
		t.Fatalf("Mechanism = %v, want graphql_cost", v.Mechanism)
	}
	want := []githubratelimit.Mechanism{githubratelimit.MechanismPrimary, githubratelimit.MechanismGraphQLCost}
	if !slices.Equal(v.Signals, want) {
		t.Fatalf("Signals = %v, want %v", v.Signals, want)
	}
	if v.RetryAfter != 45*time.Minute {
		t.Fatalf("RetryAfter = %v, want 45m — the point window, which is the longer of the two", v.RetryAfter)
	}
}

// TestA429IsASecondaryMarkerWithoutAnyProse. The status code is a typed signal;
// the body phrases are the acknowledged last resort. A 429 with neither a
// retry-after nor a recognisable message must still be classified.
func TestA429IsASecondaryMarkerWithoutAnyProse(t *testing.T) {
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusTooManyRequests,
		Header: secondaryHeader(""),
		Body:   []byte(`{"message":"Too many requests"}`),
	}, classifyNow)

	if v.Mechanism != githubratelimit.MechanismSecondary {
		t.Fatalf("Mechanism = %v, want secondary", v.Mechanism)
	}
	if v.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %v, want 0: a 429 with no retry-after names no wait, so the caller must jitter", v.RetryAfter)
	}
}

// TestRetryAfterIsReadInBothFormsRFC9110Permits, and an unparseable one is not a
// marker at all — reporting a secondary limit and then retrying with no delay is
// worse than not recognising it.
func TestRetryAfterIsReadInBothFormsRFC9110Permits(t *testing.T) {
	cases := []struct {
		name       string
		retryAfter string
		wantLimit  bool
		wantWait   time.Duration
	}{
		{"delay-seconds", "90", true, 90 * time.Second},
		{"delay-seconds with whitespace", "  90  ", true, 90 * time.Second},
		{"zero seconds", "0", true, 0},
		{"negative seconds clamp to zero", "-30", true, 0},
		{"http date in the future", classifyNow.Add(5 * time.Minute).UTC().Format(http.TimeFormat), true, 5 * time.Minute},
		{"http date in the past clamps to zero", classifyNow.Add(-time.Hour).UTC().Format(http.TimeFormat), true, 0},
		{"unparseable is not a marker", "in a little while", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := primaryHeader(4999, 5000, 1, resetUnix, "core")
			h.Set(githubratelimit.HeaderRetryAfter, tc.retryAfter)
			v := githubratelimit.Classify(githubratelimit.Observation{
				Status: http.StatusForbidden,
				Header: h,
				Body:   []byte(`{"message":"Forbidden"}`),
			}, classifyNow)

			if v.Limited() != tc.wantLimit {
				t.Fatalf("Limited() = %v (mechanism %v), want %v", v.Limited(), v.Mechanism, tc.wantLimit)
			}
			if v.RetryAfter != tc.wantWait {
				t.Fatalf("RetryAfter = %v, want %v", v.RetryAfter, tc.wantWait)
			}
		})
	}
}

// TestSecondaryBodyMarkersAreCaseInsensitive — the phrases are the last resort
// already; making them case-sensitive as well would be a second way to miss one.
func TestSecondaryBodyMarkersAreCaseInsensitive(t *testing.T) {
	for _, body := range []string{
		`{"message":"You have exceeded a SECONDARY RATE LIMIT."}`,
		`{"message":"You have triggered an Abuse Detection mechanism."}`,
	} {
		v := githubratelimit.Classify(githubratelimit.Observation{
			Status: http.StatusForbidden,
			Header: secondaryHeader(""),
			Body:   []byte(body),
		}, classifyNow)
		if v.Mechanism != githubratelimit.MechanismSecondary {
			t.Fatalf("body %q classified as %v, want secondary", body, v.Mechanism)
		}
	}
}

// TestSecondaryBodyMarkersOnlyApplyToARefusalStatus — the phrase appearing in a
// successful response's payload (a comment quoting an error, say) is not a
// refusal of that request.
func TestSecondaryBodyMarkersOnlyApplyToARefusalStatus(t *testing.T) {
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusOK,
		Header: secondaryHeader(""),
		Body:   []byte(`{"body":"CI failed with: you have exceeded a secondary rate limit"}`),
	}, classifyNow)
	if v.Limited() {
		t.Fatalf("Mechanism = %v, want none: a 200 quoting the phrase is not a refusal of that request", v.Mechanism)
	}
}

// TestAlreadyDecodedPointsAreUsedWithoutReparsingTheBody covers the typed-client
// path: a caller that filled Observation.Points must not need to hand over a raw
// body as well.
func TestAlreadyDecodedPointsAreUsedWithoutReparsingTheBody(t *testing.T) {
	decoded := githubratelimit.NewPointBudget(githubratelimit.PointBudget{
		Limit: 5000, QueryCost: 900, Remaining: 0, Used: 5000, ResetAt: pointsResetAt,
	})
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusOK,
		Body:   []byte(`{"errors":[{"type":"RATE_LIMITED","message":"exceeded"}]}`),
		Points: decoded,
	}, classifyNow)

	if v.Mechanism != githubratelimit.MechanismGraphQLCost {
		t.Fatalf("Mechanism = %v, want graphql_cost", v.Mechanism)
	}
	if v.Points.QueryCost != 900 {
		t.Fatalf("Points = %+v, want the caller's decoded budget", v.Points)
	}
	if v.RetryAfter != 45*time.Minute {
		t.Fatalf("RetryAfter = %v, want 45m from the decoded budget", v.RetryAfter)
	}
}

// TestFromHTTPTakesTheBodySeparately — a response body can be read once, so a
// helper that consumed it would leave the caller unable to decode its payload.
func TestFromHTTPTakesTheBodySeparately(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Code = http.StatusForbidden
	rec.Header().Set(githubratelimit.HeaderRetryAfter, "30")
	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()

	obs := githubratelimit.FromHTTP(resp, []byte(secondaryRefusalBody))
	if obs.Status != http.StatusForbidden {
		t.Fatalf("Status = %d", obs.Status)
	}
	v := githubratelimit.Classify(obs, classifyNow)
	if v.Mechanism != githubratelimit.MechanismSecondary || v.RetryAfter != 30*time.Second {
		t.Fatalf("Classify = %v / %v, want secondary / 30s", v.Mechanism, v.RetryAfter)
	}

	nilObs := githubratelimit.FromHTTP(nil, []byte(graphQLRefusedWithBudgetBody))
	if nilObs.Status != 0 || nilObs.Header != nil {
		t.Fatalf("FromHTTP(nil, body) = %+v, want a zero Observation carrying only the body", nilObs)
	}
	// A body-only observation still carries the GraphQL signals, which is the
	// point: they are not in any header.
	if got := githubratelimit.Classify(nilObs, classifyNow); got.Mechanism != githubratelimit.MechanismGraphQLCost {
		t.Fatalf("Mechanism = %v, want graphql_cost from the body alone", got.Mechanism)
	}
}

// TestAForeignBucketsBudgetIsStillReported. Classify does not filter by bucket —
// that is the Gate's job — so a caller can see a search budget even on a gate
// tracking core, rather than watching pre-emption quietly never fire.
func TestAForeignBucketsBudgetIsStillReported(t *testing.T) {
	v := githubratelimit.Classify(githubratelimit.Observation{
		Status: http.StatusForbidden,
		Header: primaryHeader(0, 30, 30, resetUnix, "search"),
		Body:   []byte(primaryRefusalBody),
	}, classifyNow)

	if v.Mechanism != githubratelimit.MechanismPrimary {
		t.Fatalf("Mechanism = %v, want primary", v.Mechanism)
	}
	if v.Primary.Resource != "search" {
		t.Fatalf("Primary.Resource = %q, want search", v.Primary.Resource)
	}
}
