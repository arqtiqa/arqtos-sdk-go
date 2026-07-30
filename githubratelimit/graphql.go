package githubratelimit

import (
	"bytes"
	"encoding/json"
	"time"
)

// GraphQLRateLimitedErrorType is the value GitHub puts in a GraphQL error's
// `type` field when the POINT budget refused the query.
//
// It is a string constant rather than a substring match on the message because
// the message is prose the vendor may reword, and this package's whole premise
// is that classification does not depend on prose — the same reason
// the cerr.Kind vocabulary exists.
const GraphQLRateLimitedErrorType = "RATE_LIMITED"

// A PointBudget is the GraphQL point budget as one response reported it.
//
// GraphQL is not billed per request. A query's cost is computed from the nodes
// it asks for, so one request can spend hundreds of points while the REST
// request count barely moves — which is why this is accounted separately from
// [PrimaryBudget] rather than being folded into it. A caller with 4900 REST
// requests remaining can have zero points, and a handler that reads only
// x-ratelimit-remaining sees nothing wrong right up until the query is refused.
//
// Two things about GitHub's GraphQL limit surprise a handler written against
// the REST one, and both are why this needs its own detection path:
//
//   - it is reported in the response BODY (data.rateLimit), not in any header;
//   - its refusal arrives on HTTP **200**, as an entry in the response's
//     `errors` array with type RATE_LIMITED — not as a 403 or a 429.
//
// A handler that only inspects status codes and headers therefore cannot see
// this limit at all. See [RefusedForPoints].
//
// Like [PrimaryBudget], the zero value is UNKNOWN rather than empty — see
// [PointBudget.Known].
type PointBudget struct {
	// Limit is the point budget for this window.
	Limit int
	// QueryCost is what the query that reported this budget cost — GraphQL's
	// `cost` field. It is the query's own price, not a running total; Used is
	// the running total.
	QueryCost int
	// Remaining is how many points are left in the window.
	Remaining int
	// Used is how many have been spent.
	Used int
	// NodeCount is how many nodes the reporting query returned. GitHub caps it
	// independently of points, so a query can be refused for node count with
	// points to spare — recorded here so a caller can see it, not acted on by
	// this package.
	NodeCount int
	// ResetAt is when the point window rolls over.
	ResetAt time.Time
	// known records that this budget came from a real response. Unexported so
	// that a default-constructed PointBudget cannot pass as a measured one:
	// [ParsePointBudget] and [NewPointBudget] are the only things that set it.
	known bool
}

// Known reports whether this budget was actually read from a response.
//
// The same asymmetry [PrimaryBudget.Known] documents applies here, for the
// same reasons: an unknown budget is never [PointBudget.Exhausted] (no evidence
// of exhaustion, so nothing to wait for) and has no [PointBudget.Headroom] (no
// evidence of room, so nothing to admit a sequence against).
func (b PointBudget) Known() bool { return b.known }

// Exhausted reports POSITIVE evidence that the point budget is spent.
func (b PointBudget) Exhausted() bool { return b.known && b.Remaining <= 0 }

// Headroom reports how many points may be spent while keeping reserve in hand,
// floored at zero. An unknown budget has no headroom.
func (b PointBudget) Headroom(reserve int) int {
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

// ResetIn reports how long until the point window rolls over, floored at zero.
func (b PointBudget) ResetIn(now time.Time) time.Duration {
	if b.ResetAt.IsZero() {
		return 0
	}
	d := b.ResetAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// NewPointBudget marks a caller-decoded budget as MEASURED.
//
// It exists because the useful way to read GraphQL is a typed client whose
// generated struct already holds the rateLimit fields — re-decoding the raw
// body just to reach them would mean parsing every response twice. A caller
// fills the exported fields from its own query result and passes the value
// through here, which is the moment it ASSERTS that these numbers came off a
// real response.
//
// The assertion is checked, not taken on faith: a budget with no ResetAt comes
// back UNKNOWN, because a budget that cannot say when it clears cannot be
// waited on, and the only wait derivable from it is none — a spin straight back
// into the limit. That is the same refusal [ParsePrimary] makes on a response
// carrying remaining without a reset.
func NewPointBudget(fields PointBudget) PointBudget {
	if fields.ResetAt.IsZero() {
		return PointBudget{}
	}
	fields.known = true
	fields.ResetAt = fields.ResetAt.UTC()
	return fields
}

// graphQLEnvelope is the part of a GraphQL response this package reads.
//
// Data is a RawMessage rather than a typed struct so that a malformed or
// unexpected `data` cannot fail the whole decode and take the `errors` array
// down with it. Losing the errors array is the expensive failure: it is where
// the RATE_LIMITED refusal lives, and a refusal read as a success is the
// truncation this package exists to catch.
type graphQLEnvelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

// graphQLData is the one field of `data` this package reads, decoded from the
// envelope's RawMessage in a second, tiny pass.
type graphQLData struct {
	RateLimit *struct {
		// Every numeric field is a pointer so that ABSENT and zero stay
		// distinguishable — "remaining": 0 is an exhausted budget, while no
		// remaining field at all is no budget, and json.Unmarshal renders both
		// as 0 into an int.
		Limit     *int   `json:"limit"`
		Cost      *int   `json:"cost"`
		Remaining *int   `json:"remaining"`
		Used      *int   `json:"used"`
		NodeCount *int   `json:"nodeCount"`
		ResetAt   string `json:"resetAt"`
	} `json:"rateLimit"`
}

// ParsePointBudget reads data.rateLimit out of a raw GraphQL response body.
//
// It reports false — and the zero, UNKNOWN budget — for a body that is not
// JSON, that carries no data.rateLimit (the common case: a query that did not
// ask for it), or whose rateLimit omits either of the two fields a decision is
// actually made from: `remaining` and a parseable `resetAt`. Limit, cost, used
// and nodeCount are recorded when present and left zero when not.
//
// A caller using a typed GraphQL client should prefer [NewPointBudget] over
// this: the typed result already holds the fields, and decoding the body a
// second time to reach them is work for nothing.
func ParsePointBudget(body []byte) (PointBudget, bool) {
	b, ok, _ := graphQLSignals(body)
	return b, ok
}

// RefusedForPoints reports whether a GraphQL response body carries the point
// budget's own refusal: an entry in `errors` whose type is
// [GraphQLRateLimitedErrorType].
//
// This is the detection path a REST-shaped handler does not have. GitHub
// answers a point-exhausted GraphQL query with HTTP **200** and an error in the
// body, so status-code inspection reports success and the caller reads a
// response whose `data` is null or partial. That is the truncation this package
// is here to make impossible to miss — [Classify] reports it as
// [MechanismGraphQLCost] regardless of the status code.
func RefusedForPoints(body []byte) bool {
	_, _, refused := graphQLSignals(body)
	return refused
}

// graphQLSignals decodes the body ONCE and derives both GraphQL signals from it:
// the point budget (and whether it is known), and whether the query was refused
// for points.
//
// [Classify] needs both, and a GraphQL response is scanned end to end by
// json.Unmarshal however few fields the target struct has — so two exported
// readers each decoding independently would parse a large response twice on
// every request. The exported functions stay as thin wrappers for the caller
// that wants only one.
//
// Anything that is not a JSON object carries neither signal: a REST body, or an
// HTML error page from a proxy, is not a GraphQL response.
func graphQLSignals(body []byte) (budget PointBudget, known bool, refused bool) {
	if !mayCarryGraphQLSignals(body) {
		return PointBudget{}, false, false
	}
	var env graphQLEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return PointBudget{}, false, false
	}
	for _, e := range env.Errors {
		if e.Type == GraphQLRateLimitedErrorType {
			refused = true
			break
		}
	}
	var data graphQLData
	if len(env.Data) == 0 || json.Unmarshal(env.Data, &data) != nil || data.RateLimit == nil {
		return PointBudget{}, false, refused
	}
	rl := data.RateLimit
	if rl.Remaining == nil || rl.ResetAt == "" {
		return PointBudget{}, false, refused
	}
	resetAt, err := time.Parse(time.RFC3339, rl.ResetAt)
	if err != nil {
		return PointBudget{}, false, refused
	}
	budget = PointBudget{
		Remaining: *rl.Remaining,
		ResetAt:   resetAt.UTC(),
		known:     true,
	}
	if rl.Limit != nil {
		budget.Limit = *rl.Limit
	}
	if rl.Cost != nil {
		budget.QueryCost = *rl.Cost
	}
	if rl.Used != nil {
		budget.Used = *rl.Used
	}
	if rl.NodeCount != nil {
		budget.NodeCount = *rl.NodeCount
	}
	return budget, true, refused
}

// mayCarryGraphQLSignals is a cheap negative filter: it reports false for a body
// that cannot possibly hold either GraphQL signal, so the JSON decode is skipped.
//
// It exists because [Classify] runs on EVERY response, including every page of
// every paginated REST sweep, and json.Unmarshal scans the whole document
// however few fields the target struct has. Measured on a 409 KB REST list body,
// the decode was ~1.5 ms of CPU spent to discover the body has no rateLimit —
// per response, on the path [Do] takes for every attempt of every request.
//
// Both needles are structural JSON tokens, not vendor prose, so this is not the
// string-matching the rest of the package refuses: a false POSITIVE only costs a
// decode that then finds nothing, and a false negative is impossible — a body
// carrying either signal necessarily contains the corresponding token.
func mayCarryGraphQLSignals(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	return bytes.Contains(body, []byte(`"rateLimit"`)) ||
		bytes.Contains(body, []byte(GraphQLRateLimitedErrorType))
}
