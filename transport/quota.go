package transport

import (
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
)

func ObservationToPB(o cerr.Observation) *connectorpb.QuotaObservation {
	pb := &connectorpb.QuotaObservation{
		Bucket:       string(o.Bucket),
		Provenance:   string(o.Provenance),
		UpstreamN:    int32(o.UpstreamN),
		UpstreamKind: string(o.UpstreamKind),
	}
	if !o.ObservedAt.IsZero() {
		pb.ObservedAtUnix = o.ObservedAt.Unix()
	}
	if !o.RetryAt.IsZero() {
		pb.RetryAtUnix = o.RetryAt.Unix()
	}
	return pb
}

func ObservationFromPB(pb *connectorpb.QuotaObservation) *cerr.Observation {
	if pb == nil {
		return nil
	}
	o := cerr.Observation{
		Bucket:       cerr.Bucket(pb.GetBucket()),
		Provenance:   cerr.Provenance(pb.GetProvenance()),
		UpstreamN:    int(pb.GetUpstreamN()),
		UpstreamKind: cerr.CountKind(pb.GetUpstreamKind()),
	}
	if pb.GetObservedAtUnix() != 0 {
		o.ObservedAt = time.Unix(pb.GetObservedAtUnix(), 0).UTC()
	}
	if pb.GetRetryAtUnix() != 0 {
		o.RetryAt = time.Unix(pb.GetRetryAtUnix(), 0).UTC()
	}
	if !o.Bucket.Valid() {
		o.Bucket = cerr.BucketUnknown
	}
	if !o.UpstreamKind.Valid() {
		o.UpstreamKind = cerr.CountUnknown
	}
	if !o.Provenance.Valid() {
		o.Provenance = cerr.ProvenanceUnknown
	}
	return &o
}
