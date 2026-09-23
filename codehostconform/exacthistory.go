package codehostconform

import (
	"context"
	"errors"
	"fmt"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
)

// ExactHistoryFixtures supplies isolated caller-owned empty destinations and
// an explicitly stale expected head. Required only for ExactHistoryAcquirer.
// The caller owns fixture setup/cleanup; the harness never deletes directories.
type ExactHistoryFixtures struct {
	Request         codehost.ExactHistoryRequest
	StaleRequest    codehost.ExactHistoryRequest
	CanceledRequest codehost.ExactHistoryRequest
}

func checkExactHistory(ctx context.Context, rep *Report, c codehost.CodeHost, fixtures *ExactHistoryFixtures) error {
	a, ok := c.(codehost.ExactHistoryAcquirer)
	if !ok {
		return nil
	}
	if fixtures == nil {
		return cerr.New(cerr.KindInvalid, "codehost.Conform", fmt.Errorf("exact history fixtures required"))
	}
	requests := []codehost.ExactHistoryRequest{fixtures.Request, fixtures.StaleRequest, fixtures.CanceledRequest}
	dirs := map[string]bool{}
	for _, req := range requests {
		if err := req.Validate(); err != nil {
			return err
		}
		if dirs[req.DestinationDir] {
			return cerr.New(cerr.KindInvalid, "codehost.Conform", fmt.Errorf("acquisition fixtures require distinct destinations"))
		}
		dirs[req.DestinationDir] = true
	}
	if fixtures.StaleRequest.Expected == fixtures.Request.Expected {
		return cerr.New(cerr.KindInvalid, "codehost.Conform", fmt.Errorf("stale fixture must name a different expected head"))
	}
	// Probe invalid input while the destination is still empty: otherwise a
	// non-empty-directory refusal can hide missing Expected validation.
	invalid := fixtures.Request
	invalid.Expected = ""
	result, err := a.AcquireExact(ctx, invalid)
	rep.add("exact_history/invalid-is-refused", cerr.KindOf(err) == cerr.KindInvalid && result == (codehost.ExactHistoryResult{}), "invalid input must return typed invalid and no receipt")
	result, err = a.AcquireExact(ctx, fixtures.Request)
	pass := err == nil && result.Validate(fixtures.Request) == nil
	rep.add("exact_history/complete-bound-receipt", pass, "success must bind the full request and attest complete object closure; disk isolation requires connector qualification")
	result, err = a.AcquireExact(ctx, fixtures.StaleRequest)
	rep.add("exact_history/ref-race-is-refused", cerr.KindOf(err) == cerr.KindInvalid && errors.Is(err, codehost.ErrRefChanged) && result == (codehost.ExactHistoryResult{}), "stale head must return typed invalid wrapping ErrRefChanged and no receipt")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	result, err = a.AcquireExact(canceled, fixtures.CanceledRequest)
	rep.add("exact_history/cancellation-is-typed", cerr.KindOf(err) == cerr.KindTimeout && errors.Is(err, context.Canceled) && result == (codehost.ExactHistoryResult{}), "cancellation must return typed timeout wrapping context.Canceled and no receipt")
	return nil
}
