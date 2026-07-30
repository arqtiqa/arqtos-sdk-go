package githubratelimit_test

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/githubratelimit"
)

// ExampleDo shows the fail-closed behaviour that matters most.
//
// GitHub refuses a point-exhausted GraphQL query with HTTP **200** and PARTIAL
// data, so the attempt below returns a value AND a nil error while holding half
// an answer. Do discards it: a truncated result indistinguishable from a
// complete one is the whole defect.
//
// The clock is faked so the example's output is deterministic and it does not
// actually wait; production leaves Options.Clock nil for the real one, and
// leaves Options.Jitter unset so the backoff is jittered.
func ExampleDo() {
	gate := githubratelimit.New(githubratelimit.Options{
		Clock:    newFakeClock(),
		Attempts: 2,
		Jitter:   -1, // deterministic here only — never in production
		Notify: func(w githubratelimit.Wait) {
			fmt.Printf("waiting %s on the github %s limit (attempt %d of %d, dictated=%t)\n",
				w.Delay, w.Mechanism, w.Attempt, w.Attempts, w.Dictated)
		},
	})

	body := []byte(`{"data":{"repository":{"pullRequests":{"nodes":[{"number":1}]}}},` +
		`"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`)

	prs, err := githubratelimit.Do(context.Background(), gate, "ListPRs",
		func(context.Context) ([]int, githubratelimit.Observation, error) {
			// The dangerous return a real decoder would produce here.
			return []int{1}, githubratelimit.Observation{Status: http.StatusOK, Body: body}, nil
		})

	fmt.Printf("prs=%v (the partial answer never escapes)\n", prs)
	fmt.Println("classified as:", cerr.KindOf(err))
	fmt.Println("opens the host breaker:", cerr.TripsBreaker(err))
	// Output:
	// waiting 1s on the github graphql_cost limit (attempt 1 of 2, dictated=false)
	// prs=[] (the partial answer never escapes)
	// classified as: rate_limited
	// opens the host breaker: true
}

// ExampleGate_Admit shows the never-half-applied guarantee.
//
// The budget has room for 12 of the 40 mutations. Admit is called ONCE, before
// the first one, with the size of the whole sequence — so the wait happens with
// nothing yet applied, and the sequence then runs to completion. The gate cannot
// roll back a mutation that already happened; admitting the whole sequence up
// front is what keeps there from being one.
func ExampleGate_Admit() {
	const mutations = 40

	gate := githubratelimit.New(githubratelimit.Options{
		Clock: newFakeClock(),
		// Room kept in hand for the calls a host must still make when the bulk
		// work stops — a final status read, an error report.
		Reserve: 50,
		Notify: func(w githubratelimit.Wait) {
			fmt.Printf("holding the sequence %s on the github %s limit, until %s\n",
				w.Delay, w.Mechanism, w.Until.Format(time.RFC3339))
		},
	})

	// What the last response reported: 12 requests left in the hourly quota.
	gate.Observe(githubratelimit.Observation{
		Status: http.StatusOK,
		Header: primaryHeader(12, 5000, 4988, resetUnix, githubratelimit.ResourceCore),
		Body:   []byte(`{}`),
	})

	applied := 0
	if err := gate.Admit(context.Background(), mutations); err != nil {
		fmt.Println("refused:", err)
		return
	}
	for range mutations {
		applied++
	}
	fmt.Printf("applied %d of %d, and none of them ran before the wait\n", applied, mutations)
	// Output:
	// holding the sequence 15m0s on the github primary limit, until 2026-07-30T15:00:00Z
	// applied 40 of 40, and none of them ran before the wait
}

// ExampleClassify shows the three mechanisms being told apart. The second case
// is the one a naive handler gets wrong: on a secondary refusal the hourly quota
// reads HEALTHY, so a wait derived from the budget would be no wait at all.
func ExampleClassify() {
	now := time.Date(2026, 7, 30, 14, 45, 0, 0, time.UTC)
	reset := now.Add(15 * time.Minute)

	quotaSpent := http.Header{}
	quotaSpent.Set(githubratelimit.HeaderRemaining, "0")
	quotaSpent.Set(githubratelimit.HeaderReset, fmt.Sprint(reset.Unix()))

	burst := http.Header{}
	burst.Set(githubratelimit.HeaderRemaining, "4999") // healthy!
	burst.Set(githubratelimit.HeaderReset, fmt.Sprint(reset.Unix()))
	burst.Set(githubratelimit.HeaderRetryAfter, "60")

	for _, obs := range []githubratelimit.Observation{
		{Status: http.StatusForbidden, Header: quotaSpent, Body: []byte(`{"message":"API rate limit exceeded"}`)},
		{Status: http.StatusForbidden, Header: burst, Body: []byte(`{"message":"You have exceeded a secondary rate limit."}`)},
		{Status: http.StatusOK, Body: []byte(`{"data":{"rateLimit":{"remaining":0,"resetAt":"2026-07-30T15:30:00Z"}},` +
			`"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`)},
	} {
		v := githubratelimit.Classify(obs, now)
		fmt.Printf("http %d -> %-12s wait %-8s (hourly quota spent: %t)\n",
			obs.Status, v.Mechanism, v.RetryAfter, v.Primary.Exhausted())
	}
	// Output:
	// http 403 -> primary      wait 15m0s    (hourly quota spent: true)
	// http 403 -> secondary    wait 1m0s     (hourly quota spent: false)
	// http 200 -> graphql_cost wait 45m0s    (hourly quota spent: false)
}
