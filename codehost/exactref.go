package codehost

import (
	"context"
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

const (
	CASApplied       CASOutcome = "applied"
	CASMismatch      CASOutcome = "mismatch"
	CASIndeterminate CASOutcome = "indeterminate"
)

// CASOutcome is a definite applied or mismatch result, or an indeterminate
// write that the caller must resolve with [ExactRefTransport.ReadExact].
type CASOutcome string

// ObjectID is a full git object id. Empty means the named ref does not exist.
type ObjectID string

// ExactReadRequest observes one explicit ref in one native repository.
type ExactReadRequest struct {
	Realm    string
	NativeID string
	Ref      string
}

// ExactReadResult is the observed head of the named ref.
type ExactReadResult struct {
	Realm    string
	NativeID string
	Ref      string
	ObjectID ObjectID
}

// CASRequest updates one explicit ref when its current object equals Expected.
type CASRequest struct {
	Realm    string
	NativeID string
	Ref      string
	Expected ObjectID
	New      ObjectID
}

// CASReceipt binds repository identity, ref, object ids and outcome.
// It is not policy approval.
type CASReceipt struct {
	Realm    string
	NativeID string
	Ref      string
	Expected ObjectID
	Observed ObjectID
	New      ObjectID
	Outcome  CASOutcome
}

// ExactRefTransport is the optional all-or-nothing read-and-CAS surface
// behind [CapExactRef]. Write support without the matching exact read cannot
// recover an indeterminate outcome.
type ExactRefTransport interface {
	ReadExact(ctx context.Context, req ExactReadRequest) (ExactReadResult, error)
	CompareAndSwap(ctx context.Context, req CASRequest) (CASReceipt, error)
}

func (r ExactReadRequest) Validate() error {
	return validateRepoRef("ExactReadRequest.Validate", r.Realm, r.NativeID, r.Ref)
}

func (r CASRequest) Validate() error {
	if err := validateRepoRef("CASRequest.Validate", r.Realm, r.NativeID, r.Ref); err != nil {
		return err
	}
	if r.Expected != "" && !validObjectID(r.Expected) {
		return cerr.New(cerr.KindInvalid, "CASRequest.Validate", fmt.Errorf("expected object id is not a full object id"))
	}
	if !validObjectID(r.New) {
		return cerr.New(cerr.KindInvalid, "CASRequest.Validate", fmt.Errorf("new object id is required"))
	}
	return nil
}

func validateRepoRef(op, realm, nativeID, ref string) error {
	if realm == "" || nativeID == "" || ref == "" {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("realm, native repository id and ref are required"))
	}
	if strings.Contains(nativeID, "/") {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("native repository id must not be a path"))
	}
	return nil
}

func validObjectID(id ObjectID) bool {
	n := len(id)
	if n != 40 && n != 64 {
		return false
	}
	for i := 0; i < n; i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
