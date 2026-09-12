package repoobs_test

import (
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/repoobs"
)

func TestCurrent_EachConjunctIsRequired(t *testing.T) {
	base := validObservation()
	if !base.Current() {
		t.Fatal("measured equal base should be current")
	}
	tests := []struct {
		name string
		mut  func(*repoobs.Observation)
	}{
		{"coverage unknown", func(o *repoobs.Observation) { o.Coverage = repoobs.CoverageUnknown }},
		{"source unmeasured", func(o *repoobs.Observation) { o.Source.Presence = repoobs.PresenceUnknown }},
		{"fetch failed", func(o *repoobs.Observation) { o.Source.Fetch = repoobs.FetchFailed }},
		{"empty OID", func(o *repoobs.Observation) { o.Source.OID = "" }},
		{"base unmeasured", func(o *repoobs.Observation) { o.Base.Presence = repoobs.PresenceUnknown }},
		{"base behind", func(o *repoobs.Observation) { o.Base.Relation = repoobs.RelationBehind }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := validObservation()
			tt.mut(&o)
			if o.Current() {
				t.Fatalf("%s still current", tt.name)
			}
		})
	}
}

func TestCurrent_UnknownCoverageIsNeverCurrent(t *testing.T) {
	o := validObservation()
	if !o.Current() {
		t.Fatal("measured equal base should be current")
	}
	o.Coverage = repoobs.CoverageUnknown
	if o.Current() {
		t.Fatal("unknown coverage reported as current while the source still looks measured")
	}
}

func TestCurrent_EmptyOIDAfterRelabelIsNotCurrent(t *testing.T) {
	o := validObservation()
	o.Coverage = repoobs.CoverageUnknown
	o.Source = repoobs.Source{Presence: repoobs.PresenceUnknown}
	o.Base = repoobs.Base{Presence: repoobs.PresenceUnknown}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}

	converted := o
	converted.Coverage = repoobs.CoverageMeasured
	converted.Source = repoobs.Source{Presence: repoobs.PresenceMeasured, Fetch: repoobs.FetchSucceeded}
	converted.Base = repoobs.Base{Presence: repoobs.PresenceMeasured, Relation: repoobs.RelationEqual}
	if err := converted.Validate(); err != nil {
		t.Fatal(err)
	}
	if converted.Current() {
		t.Fatal("unknown-to-current conversion with an empty OID made Current() true")
	}
}
