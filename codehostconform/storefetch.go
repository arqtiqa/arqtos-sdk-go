package codehostconform

import (
	"context"
	"errors"
	"fmt"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
)

// StoreFetchFixtures drive StoreFetcher. Required only when the connector
// implements the tier. Destinations may already exist (registered stores).
type StoreFetchFixtures struct {
	Request         codehost.StoreFetchRequest
	DeniedRequest   codehost.StoreFetchRequest
	CanceledRequest codehost.StoreFetchRequest
}

func checkStoreFetch(ctx context.Context, rep *Report, c codehost.CodeHost, fixtures *StoreFetchFixtures) error {
	a, ok := c.(codehost.StoreFetcher)
	if !ok {
		return nil
	}
	if fixtures == nil {
		return cerr.New(cerr.KindInvalid, "codehost.Conform", fmt.Errorf("store fetch fixtures required"))
	}
	requests := []codehost.StoreFetchRequest{fixtures.Request, fixtures.DeniedRequest, fixtures.CanceledRequest}
	for _, req := range requests {
		if err := req.Validate(); err != nil {
			return err
		}
	}
	if fixtures.DeniedRequest.NativeID == fixtures.Request.NativeID {
		return cerr.New(cerr.KindInvalid, "codehost.Conform", fmt.Errorf("denied fixture must name a different native id"))
	}
	invalid := fixtures.Request
	invalid.NativeID = ""
	result, err := a.FetchStore(ctx, invalid)
	rep.add("store_fetch/invalid-is-refused", cerr.KindOf(err) == cerr.KindInvalid && result == (codehost.StoreFetchResult{}), "invalid input must return typed invalid and no receipt")
	result, err = a.FetchStore(ctx, fixtures.Request)
	pass := err == nil && result.Validate(fixtures.Request) == nil
	rep.add("store_fetch/bound-receipt", pass, "success must bind realm, native id and store directory")
	result, err = a.FetchStore(ctx, fixtures.DeniedRequest)
	rep.add("store_fetch/unauthorized-is-typed", cerr.KindOf(err) == cerr.KindUnauthorized && result == (codehost.StoreFetchResult{}), "denied identity must return typed unauthorized and no receipt")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	result, err = a.FetchStore(canceled, fixtures.CanceledRequest)
	rep.add("store_fetch/cancellation-is-typed", cerr.KindOf(err) == cerr.KindTimeout && errors.Is(err, context.Canceled) && result == (codehost.StoreFetchResult{}), "cancellation must return typed timeout wrapping context.Canceled and no receipt")
	return nil
}
