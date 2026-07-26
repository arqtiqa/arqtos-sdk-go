package transport_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

func TestRefRoundTrip(t *testing.T) {
	r := ref.Ref{Vault: "v", Item: "i", Field: "f"}
	got := transport.RefFromPB(transport.RefToPB(r))
	if got != r {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, r)
	}
}

func TestRefFromPBNil(t *testing.T) {
	if got := transport.RefFromPB(nil); got != (ref.Ref{}) {
		t.Fatalf("RefFromPB(nil) = %+v, want zero value", got)
	}
}

func TestLeaseRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) // injected time, no wall-clock dependency
	l := credential.Lease{
		ID:        "lease-1",
		TTL:       5 * time.Minute,
		ExpiresAt: now.Add(5 * time.Minute),
		Renewable: true,
	}

	got := transport.LeaseFromPB(transport.LeaseToPB(l))

	if got.ID != l.ID {
		t.Errorf("ID: got %q, want %q", got.ID, l.ID)
	}
	if got.TTL != l.TTL {
		t.Errorf("TTL: got %v, want %v", got.TTL, l.TTL)
	}
	if !got.ExpiresAt.Equal(l.ExpiresAt) {
		t.Errorf("ExpiresAt: got %v, want %v", got.ExpiresAt, l.ExpiresAt)
	}
	if got.Renewable != l.Renewable {
		t.Errorf("Renewable: got %v, want %v", got.Renewable, l.Renewable)
	}
}

func TestLeaseFromPBNil(t *testing.T) {
	if got := transport.LeaseFromPB(nil); got != (credential.Lease{}) {
		t.Fatalf("LeaseFromPB(nil) = %+v, want zero value", got)
	}
}

func TestErrStatusRoundTripNotFound(t *testing.T) {
	orig := cerr.New(cerr.KindNotFound, "Resolve", nil)
	got := transport.ErrFromStatus(transport.ErrToStatus(orig))
	if cerr.KindOf(got) != cerr.KindNotFound {
		t.Fatalf("KindOf = %v, want KindNotFound", cerr.KindOf(got))
	}
}

// TestKindCodeMappingDistinct asserts each known cerr.Kind maps to its own,
// distinct codes.Code (no two Kinds collapse onto the same wire code) and that
// the mapping is reversible: Kind -> Code -> Kind recovers the original Kind.
func TestKindCodeMappingDistinct(t *testing.T) {
	kinds := []cerr.Kind{
		cerr.KindNotFound,
		cerr.KindUnauthorized,
		cerr.KindUnavailable,
		cerr.KindUnsupported,
		cerr.KindInvalid,
		cerr.KindTimeout,
		cerr.KindRateLimited,
		cerr.KindContractViolation,
	}

	// Every Kind in the closed vocabulary except Unknown must have a distinct
	// code: a Kind that quietly falls through to codes.Unknown loses its
	// classification the moment it crosses a process boundary.
	for _, k := range cerr.Kinds() {
		if k == cerr.KindUnknown {
			continue
		}
		if !slices.Contains(kinds, k) {
			t.Fatalf("Kind %v is in the vocabulary but not in this mapping test", k)
		}
	}

	seenCodes := make(map[codes.Code]cerr.Kind, len(kinds))
	for _, k := range kinds {
		wrapped := transport.ErrToStatus(cerr.New(k, "Op", errors.New("boom")))

		st, ok := status.FromError(wrapped)
		if !ok {
			t.Fatalf("ErrToStatus(%v) did not produce a gRPC status error", k)
		}
		if st.Code() == codes.Unknown {
			t.Fatalf("Kind %v mapped to codes.Unknown, want a distinct code", k)
		}
		if prevKind, exists := seenCodes[st.Code()]; exists {
			t.Fatalf("Kind %v and Kind %v both map to code %v; codes must be distinct", k, prevKind, st.Code())
		}
		seenCodes[st.Code()] = k

		back := transport.ErrFromStatus(wrapped)
		if cerr.KindOf(back) != k {
			t.Fatalf("round-trip: Kind %v -> code %v -> Kind %v", k, st.Code(), cerr.KindOf(back))
		}
	}
}

func TestKindUnknownMapsToCodeUnknown(t *testing.T) {
	wrapped := transport.ErrToStatus(cerr.New(cerr.KindUnknown, "Op", errors.New("boom")))
	st, ok := status.FromError(wrapped)
	if !ok {
		t.Fatalf("ErrToStatus(KindUnknown) did not produce a gRPC status error")
	}
	if st.Code() != codes.Unknown {
		t.Fatalf("KindUnknown: got code %v, want codes.Unknown", st.Code())
	}
	if cerr.KindOf(transport.ErrFromStatus(wrapped)) != cerr.KindUnknown {
		t.Fatalf("round-trip of KindUnknown did not come back as KindUnknown")
	}
}

// TestErrToStatusPlainError asserts a non-cerr error (no Kind at all) still
// crosses the wire safely, falling back to codes.Unknown.
func TestErrToStatusPlainError(t *testing.T) {
	wrapped := transport.ErrToStatus(errors.New("plain boom"))
	st, ok := status.FromError(wrapped)
	if !ok {
		t.Fatalf("ErrToStatus(plain error) did not produce a gRPC status error")
	}
	if st.Code() != codes.Unknown {
		t.Fatalf("plain error: got code %v, want codes.Unknown", st.Code())
	}
}

func TestErrToStatusFromNil(t *testing.T) {
	if got := transport.ErrToStatus(nil); got != nil {
		t.Fatalf("ErrToStatus(nil) = %v, want nil", got)
	}
}

func TestErrFromStatusFromNil(t *testing.T) {
	if got := transport.ErrFromStatus(nil); got != nil {
		t.Fatalf("ErrFromStatus(nil) = %v, want nil", got)
	}
}

// TestResolutionPresenceCrossesTheWire is the contract at the process
// boundary. The wire distinguishes the three states, and emptiness is
// ASSERTED rather than inferred from the length of a byte field:
//
//	no Material message                     -> nothing was resolved
//	Material, no bytes, empty_by_assertion  -> a deliberately-empty value
//	Material, no bytes, NO assertion        -> nothing was resolved
//	Material with bytes                     -> a value
//
// Presence alone cannot carry this. proto3 does not put a zero-length bytes
// field on the wire, so a conformant ResolvedEmpty and a provider that
// resolved nothing and sent a default-constructed Material serialize
// IDENTICALLY. Reading "present, no bytes" as deliberately-empty therefore
// hands the host an empty credential for a read that produced nothing — the
// conflation the contract refuses in-process, reopened at the boundary where
// the host cannot inspect the sender.
func TestResolutionPresenceCrossesTheWire(t *testing.T) {
	t.Run("unresolved sends no material message", func(t *testing.T) {
		if pb := transport.ResolutionToPB(credential.Resolution{}); pb != nil {
			t.Fatalf("an unresolved Resolution must not be sent as material, got %v", pb)
		}
		if _, err := transport.ResolutionFromPB(nil).Value(); err == nil {
			t.Fatalf("an absent material message must not read as an empty credential")
		}
	})

	t.Run("deliberately empty asserts itself on the wire", func(t *testing.T) {
		pb := transport.ResolutionToPB(credential.ResolvedEmpty())
		if pb == nil {
			t.Fatalf("a deliberately-empty value must cross as a present material message")
		}
		if len(pb.GetValue()) != 0 {
			t.Fatalf("value = %q, want empty", pb.GetValue())
		}
		if !pb.GetEmptyByAssertion() {
			t.Fatalf("a deliberately-empty value must set empty_by_assertion; without it the receiver " +
				"cannot tell it from a provider that resolved nothing")
		}
		mat, err := transport.ResolutionFromPB(pb).Value()
		if err != nil {
			t.Fatalf("round-trip of a deliberately-empty value: %v", err)
		}
		if len(mat.Reveal()) != 0 {
			t.Fatalf("round-tripped material = %q, want empty", mat.Reveal())
		}
	})

	t.Run("an unasserted empty material reads as unresolved", func(t *testing.T) {
		// The encoding a foreign author produces by accident. It must mean
		// the SAFE thing.
		for _, pb := range []*connectorpb.Material{
			{},
			{Value: []byte{}},
			{Value: nil},
		} {
			res := transport.ResolutionFromPB(pb)
			if _, err := res.Value(); err == nil {
				t.Fatalf("Material %v read as a readable credential; without empty_by_assertion it is unresolved", pb)
			}
			// And the host guard names whoever sent it.
			if _, err := credential.CheckResolution("placeholder-provider", "Resolve", res, nil); err == nil {
				t.Fatalf("CheckResolution accepted an unasserted empty material")
			}
		}
	})

	t.Run("bytes win over a contradictory assertion", func(t *testing.T) {
		res := transport.ResolutionFromPB(&connectorpb.Material{Value: []byte("v"), EmptyByAssertion: true})
		mat, err := res.Value()
		if err != nil {
			t.Fatalf("material with bytes must be a value: %v", err)
		}
		if string(mat.Reveal()) != "v" {
			t.Fatalf("Reveal() = %q, want %q", mat.Reveal(), "v")
		}
	})

	t.Run("a value round-trips", func(t *testing.T) {
		in, err := credential.Resolved(credential.NewMaterial([]byte("placeholder-value")))
		if err != nil {
			t.Fatalf("Resolved: %v", err)
		}
		mat, err := transport.ResolutionFromPB(transport.ResolutionToPB(in)).Value()
		if err != nil {
			t.Fatalf("round-trip: %v", err)
		}
		if string(mat.Reveal()) != "placeholder-value" {
			t.Fatalf("Reveal = %q", mat.Reveal())
		}
	})
}

// TestBatchResultCrossesTheWire covers the batch marshalling: exactly one
// outcome per result, the same Material presence rules one level down, and a
// per-reference failure that keeps its classification.
func TestBatchResultCrossesTheWire(t *testing.T) {
	r := ref.Ref{Vault: "<vault>", Item: "<item>", Field: "<field>"}

	t.Run("a value", func(t *testing.T) {
		res, err := credential.Resolved(credential.NewMaterial([]byte("placeholder-value")))
		if err != nil {
			t.Fatalf("Resolved: %v", err)
		}
		in, err := credential.BatchResolved(r, res)
		if err != nil {
			t.Fatalf("BatchResolved: %v", err)
		}
		got := transport.BatchResultFromPB(transport.BatchResultToPB(in))
		if got.Ref() != r {
			t.Fatalf("Ref() = %v, want %v", got.Ref(), r)
		}
		if got.Err() != nil {
			t.Fatalf("Err() = %v, want nil", got.Err())
		}
		mat, verr := got.Resolution().Value()
		if verr != nil || string(mat.Reveal()) != "placeholder-value" {
			t.Fatalf("round-trip lost the value: %v %v", mat, verr)
		}
	})

	t.Run("a deliberately-empty value", func(t *testing.T) {
		in, err := credential.BatchResolved(r, credential.ResolvedEmpty())
		if err != nil {
			t.Fatalf("BatchResolved: %v", err)
		}
		pb := transport.BatchResultToPB(in)
		if !pb.GetMaterial().GetEmptyByAssertion() {
			t.Fatalf("a deliberately-empty batch result must assert its emptiness on the wire")
		}
		mat, verr := transport.BatchResultFromPB(pb).Resolution().Value()
		if verr != nil || len(mat.Reveal()) != 0 {
			t.Fatalf("want readable, empty material; got %v %v", mat, verr)
		}
	})

	t.Run("a typed failure keeps its Kind", func(t *testing.T) {
		in, err := credential.BatchFailed(r, cerr.New(cerr.KindRateLimited, "ResolveBatch", errors.New("quota")))
		if err != nil {
			t.Fatalf("BatchFailed: %v", err)
		}
		got := transport.BatchResultFromPB(transport.BatchResultToPB(in))
		if got.Err() == nil {
			t.Fatalf("the failure did not survive the wire")
		}
		if cerr.KindOf(got.Err()) != cerr.KindRateLimited {
			t.Fatalf("KindOf = %v, want KindRateLimited — a classification lost on the wire is a breaker that never opens",
				cerr.KindOf(got.Err()))
		}
		if _, verr := got.Resolution().Value(); verr == nil {
			t.Fatalf("a failed result must not carry a readable resolution")
		}
	})

	t.Run("a blank result stays blank", func(t *testing.T) {
		// A foreign provider sending a result with no failure and no asserted
		// material. It must not become an empty credential.
		got := transport.BatchResultFromPB(&connectorpb.ResolveBatchResult{
			Ref: transport.RefToPB(r), Material: &connectorpb.Material{},
		})
		if got.Err() != nil {
			t.Fatalf("Err() = %v, want nil", got.Err())
		}
		if _, verr := got.Resolution().Value(); verr == nil {
			t.Fatalf("a blank wire result produced a READABLE credential")
		}
		// It keeps the ref, so CheckBatch reports the empty outcome rather
		// than a correspondence mismatch.
		if got.Ref() != r {
			t.Fatalf("Ref() = %v, want %v", got.Ref(), r)
		}
		_, err := credential.CheckBatch("placeholder-provider", []ref.Ref{r}, []credential.BatchResult{got}, nil)
		if err == nil {
			t.Fatalf("CheckBatch accepted a result carrying no outcome")
		}
	})

	t.Run("nil messages", func(t *testing.T) {
		if got := transport.BatchResultFromPB(nil); got.Err() != nil {
			t.Fatalf("BatchResultFromPB(nil).Err() = %v, want nil", got.Err())
		}
		if _, verr := transport.BatchResultFromPB(nil).Resolution().Value(); verr == nil {
			t.Fatalf("BatchResultFromPB(nil) must not be readable")
		}
		if got := transport.FailureToPB(nil); got != nil {
			t.Fatalf("FailureToPB(nil) = %v, want nil", got)
		}
		if got := transport.FailureFromPB(nil); got != nil {
			t.Fatalf("FailureFromPB(nil) = %v, want nil", got)
		}
	})

	t.Run("an OK-coded failure is not a failure", func(t *testing.T) {
		// A provider that filled in the failure message with zeroes has not
		// reported a failure, and must not be read as having done so.
		if got := transport.FailureFromPB(&connectorpb.Failure{}); got != nil {
			t.Fatalf("FailureFromPB(zero) = %v, want nil", got)
		}
	})
}
