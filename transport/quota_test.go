package transport_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

func TestRateLimitedQuotaRoundTripsOverStatus(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	secret := "super-secret-value"
	orig := cerr.RateLimited("Resolve", cerr.Observation{
		Bucket:       cerr.BucketAccount,
		ObservedAt:   now,
		RetryAt:      now.Add(4 * time.Hour),
		Provenance:   cerr.ProvenanceProvider,
		UpstreamN:    2,
		UpstreamKind: cerr.CountObserved,
	}, errors.New(secret))

	got := transport.ErrFromStatus(transport.ErrToStatus(orig))
	if cerr.KindOf(got) != cerr.KindRateLimited {
		t.Fatalf("KindOf = %v, want KindRateLimited", cerr.KindOf(got))
	}
	ce, ok := got.(*cerr.Error)
	if !ok {
		t.Fatalf("got %T", got)
	}
	if ce.Quota == nil || ce.Quota.Bucket != cerr.BucketAccount || ce.Quota.UpstreamKind != cerr.CountObserved {
		t.Fatalf("quota = %+v", ce.Quota)
	}
	if !ce.Quota.RetryAt.Equal(now.Add(4 * time.Hour)) {
		t.Fatalf("RetryAt = %v", ce.Quota.RetryAt)
	}
	text := got.Error()
	if strings.Contains(text, secret) {
		t.Fatalf("round-tripped error echoed credential: %q", text)
	}
	st, ok := status.FromError(transport.ErrToStatus(orig))
	if !ok {
		t.Fatal("ErrToStatus did not produce a gRPC status")
	}
	if st.Code() != codes.ResourceExhausted {
		t.Fatalf("code = %v", st.Code())
	}
	if strings.Contains(st.Message(), secret) {
		t.Fatalf("status message echoed credential: %q", st.Message())
	}
}

func TestUnauthorizedAndUnavailableDoNotCarryQuota(t *testing.T) {
	for _, orig := range []*cerr.Error{
		cerr.New(cerr.KindUnauthorized, "Resolve", nil),
		cerr.New(cerr.KindUnavailable, "Resolve", nil),
	} {
		got := transport.ErrFromStatus(transport.ErrToStatus(orig))
		if cerr.KindOf(got) != orig.Kind {
			t.Fatalf("KindOf = %v, want %v", cerr.KindOf(got), orig.Kind)
		}
		ce := got.(*cerr.Error)
		if ce.Quota != nil {
			t.Fatalf("%v grew a quota observation: %+v", orig.Kind, ce.Quota)
		}
	}
}

func TestOldPeerRateLimitedHasNilQuota(t *testing.T) {
	legacy := status.Error(codes.ResourceExhausted, "rate limited")
	got := transport.ErrFromStatus(legacy)
	if cerr.KindOf(got) != cerr.KindRateLimited {
		t.Fatalf("KindOf = %v, want KindRateLimited", cerr.KindOf(got))
	}
	if got.(*cerr.Error).Quota != nil {
		t.Fatal("an old peer without details must not invent a quota observation")
	}
}
