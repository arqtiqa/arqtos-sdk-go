package codehost

import (
	"context"
	"fmt"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// StoreFetchRequest addresses a registered bare RepositoryStore by opaque
// native identity. There is no URL field: locators and embedded tokens stay
// in connector construction state. Validate checks syntax only; the connector
// must confirm StoreDir is the registered bare store and must not mutate
// session trees.
type StoreFetchRequest struct {
	Realm    string
	NativeID string
	StoreDir string
}

// StoreFetchResult binds a completed fetch to its request. It is a receipt,
// not proof that objects are complete or that a session tree is untouched —
// connector real-Git tests prove isolation.
type StoreFetchResult struct {
	Realm    string
	NativeID string
	StoreDir string
}

// StoreFetcher is the optional CapStoreFetch surface. Fetch refreshes remote
// knowledge into an existing bare store. It is not CloneRepo and not
// AcquireExact. Failures return a zero result. Denied credentials are
// KindUnauthorized; missing stores KindUnavailable; cancellation KindTimeout
// wrapping ctx.Err(). No ambient credential fallback.
type StoreFetcher interface {
	FetchStore(context.Context, StoreFetchRequest) (StoreFetchResult, error)
}

func (r StoreFetchRequest) Validate() error {
	const op = "StoreFetchRequest.Validate"
	if r.Realm == "" || r.NativeID == "" {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("realm and native repository id are required"))
	}
	if !validRealm(r.Realm) {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("unsafe realm"))
	}
	if !validNativeID(r.NativeID) {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("native repository id must not be a path"))
	}
	if !validSourceDir(r.StoreDir) {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("store must be an explicit clean absolute directory"))
	}
	return nil
}

func (r StoreFetchResult) Validate(req StoreFetchRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if r.Realm != req.Realm || r.NativeID != req.NativeID || r.StoreDir != req.StoreDir {
		return cerr.New(cerr.KindContractViolation, "StoreFetchResult.Validate", fmt.Errorf("mismatched store-fetch receipt"))
	}
	return nil
}
