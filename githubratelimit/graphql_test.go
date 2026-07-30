package githubratelimit_test

import (
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/githubratelimit"
)

// pointsResetAt is the fixed reset instant the GraphQL fixtures share.
var pointsResetAt = time.Date(2026, 7, 30, 15, 30, 0, 0, time.UTC)

const (
	// graphQLBudgetBody is the shape a query that asked for rateLimit gets back.
	graphQLBudgetBody = `{"data":{"viewer":{"login":"placeholder"},` +
		`"rateLimit":{"limit":5000,"cost":47,"remaining":1200,"used":3800,"nodeCount":312,` +
		`"resetAt":"2026-07-30T15:30:00Z"}}}`
	// graphQLRefusedBody is how GitHub refuses a point-exhausted query: HTTP 200,
	// data null, and the refusal in the errors array.
	graphQLRefusedBody = `{"data":null,"errors":[{"type":"RATE_LIMITED",` +
		`"message":"API rate limit exceeded for user ID 0."}]}`
	// graphQLPartialBody is the dangerous one: a refusal that ALSO carries data,
	// so a caller decoding it gets a value, no error, and half an answer.
	graphQLPartialBody = `{"data":{"repository":{"pullRequests":{"nodes":[{"number":1}]}}},` +
		`"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`
)

func TestParsePointBudgetReadsTheRateLimitField(t *testing.T) {
	b, ok := githubratelimit.ParsePointBudget([]byte(graphQLBudgetBody))
	if !ok {
		t.Fatalf("ParsePointBudget reported no budget for a body carrying rateLimit")
	}
	if !b.Known() {
		t.Fatalf("a budget read off a real body must report Known()")
	}
	if b.Limit != 5000 || b.QueryCost != 47 || b.Remaining != 1200 || b.Used != 3800 || b.NodeCount != 312 {
		t.Fatalf("point budget = %+v", b)
	}
	if !b.ResetAt.Equal(pointsResetAt) {
		t.Fatalf("ResetAt = %v, want %v", b.ResetAt, pointsResetAt)
	}
	if b.Exhausted() {
		t.Fatalf("1200 points remaining is not exhausted")
	}
}

// TestPointCostIsNotTheRequestCount is the "accounted separately" acceptance
// criterion, in the one shape that actually bites: a caller with plenty of REST
// requests left and no points at all. A handler reading only
// x-ratelimit-remaining sees nothing wrong here.
func TestPointCostIsNotTheRequestCount(t *testing.T) {
	obs := githubratelimit.Observation{
		Status: 200,
		Header: primaryHeader(4900, 5000, 100, resetUnix, "graphql"),
		Body: []byte(`{"data":{"rateLimit":{"limit":5000,"cost":1200,"remaining":0,"used":5000,` +
			`"nodeCount":10,"resetAt":"2026-07-30T15:30:00Z"}},` +
			`"errors":[{"type":"RATE_LIMITED","message":"exceeded"}]}`),
	}
	v := githubratelimit.Classify(obs, pointsResetAt.Add(-10*time.Minute))

	if !v.Primary.Known() || v.Primary.Exhausted() {
		t.Fatalf("the REST request count is healthy here; Primary = %+v", v.Primary)
	}
	if !v.Points.Known() || !v.Points.Exhausted() {
		t.Fatalf("the point budget is spent here; Points = %+v", v.Points)
	}
	if v.Mechanism != githubratelimit.MechanismGraphQLCost {
		t.Fatalf("Mechanism = %v, want graphql_cost — 4900 requests remaining and zero points is exactly the case a request-count handler cannot see", v.Mechanism)
	}
	if v.RetryAfter != 10*time.Minute {
		t.Fatalf("RetryAfter = %v, want 10m (the POINT window's reset, not the request window's)", v.RetryAfter)
	}
}

// TestUnknownPointBudgetIsNeitherFullNorEmpty mirrors the primary budget's
// fail-closed zero value, for the same reasons.
func TestUnknownPointBudgetIsNeitherFullNorEmpty(t *testing.T) {
	var b githubratelimit.PointBudget
	if b.Known() {
		t.Fatalf("the zero PointBudget must not report Known()")
	}
	if b.Exhausted() {
		t.Fatalf("an unmeasured point budget must not report Exhausted()")
	}
	if got := b.Headroom(0); got != 0 {
		t.Fatalf("Headroom on an unmeasured point budget = %d, want 0", got)
	}
	if got := b.ResetIn(pointsResetAt); got != 0 {
		t.Fatalf("ResetIn on a budget with no reset = %v, want 0", got)
	}
}

// TestParsePointBudgetRefusesABodyThatCannotDecideAWait pins the same
// fail-closed reading ParsePrimary makes: absent remaining and absent resetAt
// each make the budget UNKNOWN rather than a zero-valued known one, because
// json.Unmarshal renders "absent" and 0 identically into an int.
func TestParsePointBudgetRefusesABodyThatCannotDecideAWait(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"not json", "<html>502 Bad Gateway</html>"},
		{"no data", `{"errors":[{"type":"FORBIDDEN"}]}`},
		{"data without rateLimit", `{"data":{"viewer":{"login":"placeholder"}}}`},
		{"null data", `{"data":null}`},
		{"rateLimit without remaining", `{"data":{"rateLimit":{"limit":5000,"resetAt":"2026-07-30T15:30:00Z"}}}`},
		{"rateLimit without resetAt", `{"data":{"rateLimit":{"limit":5000,"remaining":0}}}`},
		{"unparseable resetAt", `{"data":{"rateLimit":{"remaining":0,"resetAt":"soon"}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, ok := githubratelimit.ParsePointBudget([]byte(tc.body))
			if ok {
				t.Fatalf("ParsePointBudget reported a budget: %+v", b)
			}
			if b.Known() {
				t.Fatalf("the returned budget must be UNKNOWN")
			}
		})
	}
}

// TestParsePointBudgetDistinguishesZeroRemainingFromAbsent is the pointer-field
// decision, stated as a test: an exhausted budget and no budget must not be the
// same value.
func TestParsePointBudgetDistinguishesZeroRemainingFromAbsent(t *testing.T) {
	exhausted, ok := githubratelimit.ParsePointBudget(
		[]byte(`{"data":{"rateLimit":{"remaining":0,"resetAt":"2026-07-30T15:30:00Z"}}}`))
	if !ok || !exhausted.Known() {
		t.Fatalf("a rateLimit stating remaining:0 must be a KNOWN budget")
	}
	if !exhausted.Exhausted() {
		t.Fatalf("remaining:0 must report Exhausted()")
	}
	absent, ok := githubratelimit.ParsePointBudget(
		[]byte(`{"data":{"rateLimit":{"resetAt":"2026-07-30T15:30:00Z"}}}`))
	if ok || absent.Known() {
		t.Fatalf("a rateLimit with no remaining field must be UNKNOWN, not an exhausted budget")
	}
}

// TestRefusedForPointsSeesARefusalOnHTTP200 is the detection path a REST-shaped
// handler simply does not have. GitHub answers a point-exhausted query with 200,
// so status-code inspection reports success.
func TestRefusedForPointsSeesARefusalOnHTTP200(t *testing.T) {
	if !githubratelimit.RefusedForPoints([]byte(graphQLRefusedBody)) {
		t.Fatalf("a RATE_LIMITED error in the body must be detected")
	}
	if !githubratelimit.RefusedForPoints([]byte(graphQLPartialBody)) {
		t.Fatalf("a RATE_LIMITED error alongside partial data must be detected — that is the truncation case")
	}
	for _, body := range []string{
		"",
		graphQLBudgetBody,
		`{"errors":[{"type":"FORBIDDEN","message":"Resource not accessible"}]}`,
		`{"errors":[{"message":"API rate limit exceeded"}]}`, // prose without the typed marker
		"not json at all",
	} {
		if githubratelimit.RefusedForPoints([]byte(body)) {
			t.Fatalf("RefusedForPoints returned true for %q", body)
		}
	}
}

// TestNewPointBudgetIsAnAssertionThatIsChecked covers the path a typed GraphQL
// client uses: the generated struct already holds the fields, so re-decoding the
// body would be work for nothing. The assertion is still checked — a budget with
// no ResetAt cannot be waited on, so it comes back UNKNOWN.
func TestNewPointBudgetIsAnAssertionThatIsChecked(t *testing.T) {
	b := githubratelimit.NewPointBudget(githubratelimit.PointBudget{
		Limit:     5000,
		QueryCost: 200,
		Remaining: 0,
		Used:      5000,
		ResetAt:   pointsResetAt.In(time.FixedZone("elsewhere", 3*60*60)),
	})
	if !b.Known() {
		t.Fatalf("a caller-decoded budget with a ResetAt must be KNOWN")
	}
	if !b.Exhausted() {
		t.Fatalf("remaining 0 must report Exhausted()")
	}
	if !b.ResetAt.Equal(pointsResetAt) || b.ResetAt.Location() != time.UTC {
		t.Fatalf("ResetAt = %v (%v), want the same instant normalised to UTC", b.ResetAt, b.ResetAt.Location())
	}

	noReset := githubratelimit.NewPointBudget(githubratelimit.PointBudget{Limit: 5000, Remaining: 0})
	if noReset.Known() {
		t.Fatalf("a budget that cannot say when it clears must come back UNKNOWN: the only wait derivable from it is none")
	}
}

func TestPointHeadroomHoldsBackTheReserve(t *testing.T) {
	b, _ := githubratelimit.ParsePointBudget([]byte(graphQLBudgetBody))
	cases := []struct {
		reserve int
		want    int
	}{
		{0, 1200},
		{200, 1000},
		{1200, 0},
		{5000, 0},
		{-1, 1200},
	}
	for _, tc := range cases {
		if got := b.Headroom(tc.reserve); got != tc.want {
			t.Fatalf("Headroom(%d) = %d, want %d", tc.reserve, got, tc.want)
		}
	}
}

// TestARefusalSurvivesAMalformedDataField. The refusal lives in `errors` and the
// budget in `data`, so a `data` this package cannot decode must not take the
// refusal down with it — a refusal read as a success is the truncation the whole
// package exists to catch, and it is the expensive direction to get wrong.
func TestARefusalSurvivesAMalformedDataField(t *testing.T) {
	for _, body := range []string{
		`{"data":{"rateLimit":[1,2]},"errors":[{"type":"RATE_LIMITED","message":"exceeded"}]}`,
		`{"data":[],"errors":[{"type":"RATE_LIMITED","message":"exceeded"}]}`,
		`{"data":"unexpected","errors":[{"type":"RATE_LIMITED","message":"exceeded"}]}`,
		`{"data":null,"errors":[{"type":"RATE_LIMITED","message":"exceeded"}]}`,
	} {
		if !githubratelimit.RefusedForPoints([]byte(body)) {
			t.Fatalf("the refusal was lost decoding %q", body)
		}
		if b, ok := githubratelimit.ParsePointBudget([]byte(body)); ok {
			t.Fatalf("a malformed data field must yield no budget, got %+v", b)
		}
	}
}

// TestABodyCarryingNeitherSignalIsNotMisread guards the cheap negative filter
// that keeps every paginated REST body from being JSON-decoded. The filter is a
// pure optimisation, so what has to hold is that it never invents a signal and
// never hides one.
func TestABodyCarryingNeitherSignalIsNotMisread(t *testing.T) {
	// A large REST list body, of the shape a paginated sweep returns.
	var rest []byte
	rest = append(rest, '[')
	for i := range 500 {
		if i > 0 {
			rest = append(rest, ',')
		}
		rest = append(rest, `{"number":1,"title":"placeholder","state":"open","limit":5000,"remaining":4999}`...)
	}
	rest = append(rest, ']')

	if b, ok := githubratelimit.ParsePointBudget(rest); ok {
		t.Fatalf("a REST body produced a point budget: %+v", b)
	}
	if githubratelimit.RefusedForPoints(rest) {
		t.Fatalf("a REST body was read as a point refusal")
	}
	// And a body that DOES carry the tokens is still read, so the filter cannot
	// be passing above by rejecting everything.
	if _, ok := githubratelimit.ParsePointBudget([]byte(graphQLBudgetBody)); !ok {
		t.Fatalf("a real GraphQL budget must still parse")
	}
	if !githubratelimit.RefusedForPoints([]byte(graphQLRefusedBody)) {
		t.Fatalf("a real point refusal must still be detected")
	}
}

func TestPointResetInIsClampedAtZeroForAPassedWindow(t *testing.T) {
	b, _ := githubratelimit.ParsePointBudget([]byte(graphQLBudgetBody))
	if got := b.ResetIn(pointsResetAt.Add(-4 * time.Minute)); got != 4*time.Minute {
		t.Fatalf("ResetIn = %v, want 4m", got)
	}
	if got := b.ResetIn(pointsResetAt.Add(time.Minute)); got != 0 {
		t.Fatalf("ResetIn for a passed window = %v, want 0", got)
	}
}
