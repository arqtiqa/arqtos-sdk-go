package credential

import (
	"context"

	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// A BatchResult is one requested reference's outcome inside a batch: either a
// readable [Resolution] or a typed failure, never both and never neither.
//
// # Why the fields are unexported
//
// "Never both and never neither" was a sentence in this comment before it was
// a property of the type, and a sentence does not hold. A struct with three
// exported fields lets a connector fill in a resolution AND an error (the
// host then picks one, and two hosts pick differently), or neither (the host
// gets a silent blank for a reference it asked about) — which is the same
// (empty, no error) conflation [Resolution] exists to remove, one level down.
//
// So a BatchResult is built only by [BatchResolved] or [BatchFailed], each of
// which sets exactly one outcome, and is read only through [BatchResult.Ref],
// [BatchResult.Resolution] and [BatchResult.Err]. The zero BatchResult
// carries nothing and is caught by [CheckBatch], the same way the zero
// Resolution is caught by [CheckResolution].
type BatchResult struct {
	r   ref.Ref
	res Resolution
	err error
}

// BatchResolved records ref r's outcome as the resolution res.
//
// It returns a [FaultError] when res carries no value: a result that is
// neither a value nor a failure is not an outcome, and recording it would
// hand the host a blank for a reference it asked about. The BatchResult
// returned alongside the error keeps r — so [CheckBatch] can name the
// position and the reference — but carries no outcome, so a connector that
// ignores the error still cannot pass a blank off as a resolved value.
//
// A deliberately-empty secret ([ResolvedEmpty]) is a value, and is accepted.
func BatchResolved(r ref.Ref, res Resolution) (BatchResult, error) {
	if !res.present() {
		return BatchResult{r: r}, &FaultError{
			Op:     "ResolveBatch",
			Fault:  FaultUnresolved,
			Detail: "recorded a batch result for " + r.String() + " that carries no value; a reference that did not resolve needs a typed failure (see BatchFailed), not a blank result",
		}
	}
	return BatchResult{r: r, res: res}, nil
}

// BatchFailed records ref r's outcome as the typed failure err.
//
// err is this one reference's failure, from the same closed vocabulary a
// single Resolve uses (see the cerr package). One reference failing does not
// fail the batch.
//
// It returns a [FaultError] when err is nil, for the same reason
// [BatchResolved] refuses an empty resolution: the result would carry
// neither outcome. The BatchResult returned alongside the error keeps r and
// carries no outcome.
func BatchFailed(r ref.Ref, err error) (BatchResult, error) {
	if err == nil {
		return BatchResult{r: r}, &FaultError{
			Op:     "ResolveBatch",
			Fault:  FaultUnresolved,
			Detail: "recorded a batch failure for " + r.String() + " with a nil error; a reference that resolved needs its value (see BatchResolved), not a blank result",
		}
	}
	return BatchResult{r: r, err: err}, nil
}

// Ref is the reference this result answers. It lets a host attribute a result
// without relying on position alone — and lets [CheckBatch] prove the two
// agree.
func (b BatchResult) Ref() ref.Ref { return b.r }

// Resolution is the value, when [BatchResult.Err] is nil. For a result that
// carries a failure it is the zero Resolution, which is unreadable.
func (b BatchResult) Resolution() Resolution { return b.res }

// Err is this reference's typed failure, or nil when it resolved.
func (b BatchResult) Err() error { return b.err }

// String redacts, for the same reason [Resolution.String] does: a BatchResult
// travels through host code that logs. It reports which reference the result
// is for and whether it resolved, which is diagnosis, not material.
func (b BatchResult) String() string {
	switch {
	case b.err != nil:
		return "[" + b.r.String() + ": FAILED " + b.err.Error() + "]"
	case b.res.present():
		return "[" + b.r.String() + ": REDACTED credential]"
	default:
		return "[" + b.r.String() + ": no outcome]"
	}
}

// GoString redacts under %#v for the same reason as String.
func (b BatchResult) GoString() string { return b.String() }

// BatchResolver is the optional contract operation behind [CapBatchResolve]:
// resolve many references in ONE backend call.
//
// # Why it is optional, and why it is declared
//
// Whole-bundle fetch is what makes a small per-day call quota survivable, but
// it is an artifact of particular backends — an environment bundle in one, a
// KV-v2 read in another, nothing at all in a third. A host that assumes batch
// silently fans out to N calls against a backend that cannot batch, and reads
// multiply unnoticed until a quota is gone and everything that depended on it
// stops. That is not a hypothetical; it is why this is a declared capability.
//
// So a connector that can batch does two things, and must do both:
//
//   - implement this interface, and
//   - declare [CapBatchResolve] in its manifest and from Capabilities().
//
// Declaring without implementing is worse than not declaring: the host plans
// one call and gets a method that is not there. Conformance fails it.
//
// The host owns the degradation and the reporting: where the capability is
// absent it resolves one reference at a time and says so, so a quota
// investigation can see the fan-out instead of hunting it in the cache.
//
// An out-of-process (Track-B) provider satisfies this the same way a native
// connector does: the ResolveBatch RPC backs it, and the host-side stub
// implements this interface exactly when the provider reports
// [CapBatchResolve].
type BatchResolver interface {
	// ResolveBatch resolves refs in one backend call.
	//
	// It returns exactly one BatchResult per requested reference, in the
	// requested order, each carrying either a resolution or a typed failure.
	// A returned error is a failure of the batch call itself (the backend was
	// unreachable, the request was refused); per-reference failures belong in
	// the results, not here, so one missing field does not discard the other
	// ninety-nine values that were fetched with it.
	ResolveBatch(ctx context.Context, refs []ref.Ref) ([]BatchResult, error)
}
