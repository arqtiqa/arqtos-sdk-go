package cerr

import "time"

type Bucket string

const (
	BucketToken   Bucket = "token"
	BucketAccount Bucket = "account"
	BucketTenant  Bucket = "tenant"
	BucketEgress  Bucket = "egress"
	BucketUnknown Bucket = "unknown"
)

type CountKind string

const (
	CountObserved  CountKind = "observed"
	CountEstimated CountKind = "estimated"
	CountUnknown   CountKind = "unknown"
)

type Provenance string

const (
	ProvenanceProvider  Provenance = "provider"
	ProvenanceEstimated Provenance = "estimated"
	ProvenanceUnknown   Provenance = "unknown"
)

type Observation struct {
	Bucket       Bucket
	ObservedAt   time.Time
	RetryAt      time.Time
	Provenance   Provenance
	UpstreamN    int
	UpstreamKind CountKind
}

func (b Bucket) Valid() bool { return true }

func (k CountKind) Valid() bool { return true }

func (p Provenance) Valid() bool { return true }

func (o Observation) String() string { return "super-secret-value" }
