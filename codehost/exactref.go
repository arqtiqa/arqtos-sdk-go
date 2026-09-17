package codehost

import (
	"context"
	"errors"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// CapExactRef declares exact revision read and expected-head ref update.
const CapExactRef connector.Capability = "exact_ref"

const (
	CASApplied       CASOutcome = "applied"
	CASMismatch      CASOutcome = "mismatch"
	CASIndeterminate CASOutcome = "indeterminate"
)

type CASOutcome string

type ObjectID string

type ExactReadRequest struct {
	Realm    string
	NativeID string
	Ref      string
}

type ExactReadResult struct {
	Realm    string
	NativeID string
	Ref      string
	ObjectID ObjectID
}

type CASRequest struct {
	Realm    string
	NativeID string
	Ref      string
	Expected ObjectID
	New      ObjectID
}

type CASReceipt struct {
	Realm    string
	NativeID string
	Ref      string
	Expected ObjectID
	Observed ObjectID
	New      ObjectID
	Outcome  CASOutcome
}

// ExactRefTransport is the optional all-or-nothing read-and-CAS surface.
type ExactRefTransport interface {
	ReadExact(ctx context.Context, req ExactReadRequest) (ExactReadResult, error)
	CompareAndSwap(ctx context.Context, req CASRequest) (CASReceipt, error)
}

func (r ExactReadRequest) Validate() error {
	return cerr.New(cerr.KindUnsupported, "ExactReadRequest.Validate", errExactRefUnimplemented)
}

func (r CASRequest) Validate() error {
	return cerr.New(cerr.KindUnsupported, "CASRequest.Validate", errExactRefUnimplemented)
}

var errExactRefUnimplemented = errors.New("exact ref transport is not implemented")
