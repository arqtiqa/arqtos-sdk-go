package transport_test

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
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
