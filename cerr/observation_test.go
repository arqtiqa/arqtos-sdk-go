package cerr_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

func TestBucketsAreOpaqueAndClosed(t *testing.T) {
	for _, b := range []cerr.Bucket{cerr.BucketToken, cerr.BucketAccount, cerr.BucketTenant, cerr.BucketEgress, cerr.BucketUnknown} {
		if !b.Valid() {
			t.Fatalf("%q must be a valid consumer bucket", b)
		}
	}
	if cerr.Bucket("github").Valid() || cerr.Bucket("1password").Valid() {
		t.Fatal("provider-specific bucket names must not be valid on consumers")
	}
}

func TestObservationDistinguishesObservedEstimatedAndUnknownCounts(t *testing.T) {
	obs := cerr.Observation{Bucket: cerr.BucketAccount, UpstreamN: 3, UpstreamKind: cerr.CountObserved}
	if obs.UpstreamKind != cerr.CountObserved || obs.UpstreamN != 3 {
		t.Fatalf("%+v", obs)
	}
	est := cerr.Observation{Bucket: cerr.BucketToken, UpstreamN: 1, UpstreamKind: cerr.CountEstimated}
	unk := cerr.Observation{Bucket: cerr.BucketUnknown, UpstreamKind: cerr.CountUnknown}
	if est.UpstreamKind == cerr.CountObserved || unk.UpstreamN != 0 {
		t.Fatal("estimated/unknown counts collapsed into observed")
	}
}

func TestRateLimitedCarriesQuotaWithoutEchoingCredentials(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	secret := "super-secret-value"
	q := cerr.Observation{
		Bucket:       cerr.BucketAccount,
		ObservedAt:   now,
		RetryAt:      now.Add(4 * time.Hour),
		Provenance:   cerr.ProvenanceProvider,
		UpstreamN:    2,
		UpstreamKind: cerr.CountObserved,
	}
	err := cerr.RateLimited("Resolve", q, errors.New(secret))
	if cerr.KindOf(err) != cerr.KindRateLimited {
		t.Fatalf("KindOf = %v", cerr.KindOf(err))
	}
	if !cerr.TripsBreaker(err) {
		t.Fatal("account quota refusal must still trip the breaker")
	}
	got := err.Quota
	if got == nil || got.Bucket != cerr.BucketAccount || got.UpstreamKind != cerr.CountObserved {
		t.Fatalf("quota = %+v", got)
	}
	text := err.Error()
	if strings.Contains(text, secret) {
		t.Fatalf("error echoed credential material: %q", text)
	}
	if strings.Contains(fmt.Sprintf("%v", q), secret) {
		t.Fatalf("observation leaked material: %q", q)
	}
}

func TestUnauthorizedAndUnavailableStayDistinctFromRateLimited(t *testing.T) {
	unauth := cerr.New(cerr.KindUnauthorized, "Resolve", nil)
	unavail := cerr.New(cerr.KindUnavailable, "Resolve", nil)
	limited := cerr.RateLimited("Resolve", cerr.Observation{Bucket: cerr.BucketToken, UpstreamKind: cerr.CountUnknown}, nil)
	if cerr.KindOf(unauth) == cerr.KindRateLimited || cerr.KindOf(unavail) == cerr.KindRateLimited {
		t.Fatal("classification collapsed")
	}
	if cerr.TripsBreaker(unauth) || cerr.TripsBreaker(unavail) {
		t.Fatal("non-quota failures must not trip the breaker")
	}
	if !cerr.TripsBreaker(limited) {
		t.Fatal("rate limited must trip the breaker")
	}
}
