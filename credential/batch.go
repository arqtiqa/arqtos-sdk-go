package credential

import (
	"context"

	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// A BatchResult is one requested reference's outcome inside a batch: either a
// readable [Resolution] or a typed failure, never both and never neither.
type BatchResult struct {
	// Ref is the reference this result answers. It lets a host attribute a
	// result without relying on position alone — and lets [CheckBatch] prove
	// the two agree.
	Ref ref.Ref
	// Resolution is the value, when Err is nil.
	Resolution Resolution
	// Err is this reference's typed failure, from the same closed vocabulary
	// a single Resolve uses. One reference failing does not fail the batch.
	Err error
}

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
// Realizes REQ-ARQ-P-20.
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
