package cerr

import "time"

// Bucket is an opaque quota-domain identity. The set is closed so consumers
// never grow provider-specific fields (credential-detail §3f).
type Bucket string

const (
	BucketToken   Bucket = "token"
	BucketAccount Bucket = "account"
	BucketTenant  Bucket = "tenant"
	BucketEgress  Bucket = "egress"
	BucketUnknown Bucket = "unknown"
)

// CountKind says whether an upstream operation count was observed, estimated,
// or unknown. SDK method calls are not billable requests.
type CountKind string

const (
	CountObserved  CountKind = "observed"
	CountEstimated CountKind = "estimated"
	CountUnknown   CountKind = "unknown"
)

// Provenance is where a retry/reset observation came from.
type Provenance string

const (
	ProvenanceProvider  Provenance = "provider"
	ProvenanceEstimated Provenance = "estimated"
	ProvenanceUnknown   Provenance = "unknown"
)

// An Observation is non-secret quota telemetry on a classified failure.
type Observation struct {
	Bucket       Bucket
	ObservedAt   time.Time
	RetryAt      time.Time
	Provenance   Provenance
	UpstreamN    int
	UpstreamKind CountKind
}

func (b Bucket) Valid() bool {
	switch b {
	case BucketToken, BucketAccount, BucketTenant, BucketEgress, BucketUnknown:
		return true
	default:
		return false
	}
}

func (k CountKind) Valid() bool {
	switch k {
	case CountObserved, CountEstimated, CountUnknown:
		return true
	default:
		return false
	}
}

func (p Provenance) Valid() bool {
	switch p {
	case ProvenanceProvider, ProvenanceEstimated, ProvenanceUnknown:
		return true
	default:
		return false
	}
}

// String is diagnosis, never material.
func (o Observation) String() string {
	return string(o.Bucket) + " " + string(o.UpstreamKind)
}
