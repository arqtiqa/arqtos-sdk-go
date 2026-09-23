package codehost

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// ErrRefChanged identifies an acquisition refused because the addressed ref no
// longer equals Expected. Connectors wrap it with cerr.KindInvalid; no new head
// is silently substituted and the caller must explicitly replan.
var ErrRefChanged = errors.New("exact history ref changed")

// ExactHistoryRequest addresses one full commit and its complete ancestry,
// trees and blobs. DestinationDir is an explicit caller-owned empty directory
// on the linked application's filesystem, never a remote-plugin address.
// Validate checks syntax only; the connector must verify ownership, emptiness
// and symlink confinement before effects. Credentials and clone URLs remain
// connector construction state, not caller-supplied transport instructions.
type ExactHistoryRequest struct {
	Realm          string
	NativeID       string
	Ref            string
	Expected       ObjectID
	DestinationDir string
}

// ExactHistoryResult binds a completed acquisition to its input. Complete
// attests full reachable commit/tree/blob closure, not merely a present tip or
// shallow/partial clone. It is not proof of policy approval or factual coverage;
// callers still validate the acquired objects and their admitted journal.
type ExactHistoryResult struct {
	Realm          string
	NativeID       string
	Ref            string
	ObjectID       ObjectID
	DestinationDir string
	Complete       bool
}

// ExactHistoryAcquirer is the optional native-only CapExactHistory surface.
// Success produces isolated bare storage without hooks, inherited git config,
// credentials, alternates or promisor dependencies. No remote ref or source
// repository is modified. Ref mismatch returns KindInvalid wrapping
// ErrRefChanged; cancellation/deadline returns KindTimeout wrapping ctx.Err().
// Every failure returns a zero result. Partial destination contents, if any,
// remain untrusted caller-owned recovery data, never an acquisition receipt.
// Existing or symlinked content must not be followed or overwritten. SDK
// conformance checks receipts/errors; connector real-Git tests prove isolation.
type ExactHistoryAcquirer interface {
	AcquireExact(context.Context, ExactHistoryRequest) (ExactHistoryResult, error)
}

func (r ExactHistoryRequest) Validate() error {
	const op = "ExactHistoryRequest.Validate"
	if err := validateRepoRef(op, r.Realm, r.NativeID, r.Ref); err != nil {
		return err
	}
	if !validObjectID(r.Expected) || strings.Trim(string(r.Expected), "0") == "" {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("expected must be a full nonzero commit id"))
	}
	if !validSourceDir(r.DestinationDir) {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("destination must be an explicit clean absolute directory"))
	}
	return nil
}

func (r ExactHistoryResult) Validate(req ExactHistoryRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if r.Realm != req.Realm || r.NativeID != req.NativeID || r.Ref != req.Ref || r.ObjectID != req.Expected || r.DestinationDir != req.DestinationDir || !r.Complete {
		return cerr.New(cerr.KindContractViolation, "ExactHistoryResult.Validate", fmt.Errorf("incomplete or mismatched exact history receipt"))
	}
	return nil
}
