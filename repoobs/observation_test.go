package repoobs_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
	"github.com/arqtiqa/arqtos-sdk-go/repoobs"
)

func observedAt() time.Time {
	return time.Date(2026, 9, 12, 15, 4, 5, 0, time.UTC)
}

func measuredSource() repoobs.Source {
	return repoobs.Source{
		Presence: repoobs.PresenceMeasured,
		Ref:      "refs/heads/main",
		OID:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Fetch:    repoobs.FetchSucceeded,
	}
}

func validObservation() repoobs.Observation {
	return repoobs.Observation{
		SchemaVersion:         repoobs.SchemaVersion,
		ResourceID:            "resource:repo/governed",
		SourceIdentity:        "src:host/acme-platform-main",
		TrustDomain:           "td_isolate_acme",
		SourceRef:             "refs/heads/main",
		SourceRevision:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ObservedAt:            observedAt(),
		ObservationRevision:   "obs:1",
		Coverage:              repoobs.CoverageMeasured,
		Source:                measuredSource(),
		Base:                  repoobs.Base{Presence: repoobs.PresenceMeasured, Relation: repoobs.RelationEqual},
		Local:                 repoobs.Local{Presence: repoobs.PresenceMeasured},
		Durability:            repoobs.Draft{Presence: repoobs.PresenceMeasured, Level: repoobs.DurabilityLocalGit},
		Index:                 repoobs.Index{Presence: repoobs.PresenceMeasured, BaseProfile: "profile:v1", LocalDelta: "0"},
	}
}

func TestSchemaVersion_IsIdentifiedForConsumers(t *testing.T) {
	if repoobs.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1 — consumers pin this number, not a module pseudo-version", repoobs.SchemaVersion)
	}
	if repoobs.PayloadType != "repo_observation" {
		t.Fatalf("PayloadType = %q, want repo_observation so a WorkEvent envelope can name this grain", repoobs.PayloadType)
	}
}

func TestObservation_DistinguishesResourceIDStoreIdentityAndOptionalTree(t *testing.T) {
	o := validObservation()
	if o.ResourceID == o.SourceIdentity {
		t.Fatal("ResourceID and SourceIdentity collapsed — a rename would then look like a new resource")
	}
	if o.TrustDomain == "" || o.SourceIdentity == "" {
		t.Fatal("store identity fields are empty")
	}
	if err := o.Validate(); err != nil {
		t.Fatalf("valid observation: %v", err)
	}

	o.SessionID = ""
	o.TreeSetID = ""
	if err := o.Validate(); err != nil {
		t.Fatalf("session and tree-set are optional: %v", err)
	}
	o.SessionID = "sess:01"
	o.TreeSetID = "treeset:01"
	if err := o.Validate(); err != nil {
		t.Fatalf("exact tree identity: %v", err)
	}

	other := o
	other.ResourceID = "resource:repo/other"
	if other.SourceIdentity != o.SourceIdentity || other.TrustDomain != o.TrustDomain {
		t.Fatal("changing ResourceID moved the store identity")
	}
}

func TestObservation_FourGitAxesAndIndexAreSeparateValues(t *testing.T) {
	o := validObservation()
	o.Local.Unstaged = true
	o.Base.Relation = repoobs.RelationBehind
	o.Durability.Level = repoobs.DurabilityWorkingFiles
	o.Index.Pending = true
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	if o.Source.Presence != repoobs.PresenceMeasured {
		t.Fatal("local dirt overwrote the source observation")
	}
	if o.Base.Relation != repoobs.RelationBehind {
		t.Fatal("base relation was not retained as its own value")
	}
	if o.Durability.Level != repoobs.DurabilityWorkingFiles {
		t.Fatal("draft durability collapsed into local work")
	}
	if !o.Index.Pending {
		t.Fatal("index freshness is not a separate projection")
	}
}

func TestPresence_UnknownDeniedUnsupportedAndMeasuredZeroAreNotConflated(t *testing.T) {
	want := map[repoobs.Presence]string{
		repoobs.PresenceUnspecified: "unspecified",
		repoobs.PresenceUnknown:     "unknown",
		repoobs.PresenceDenied:      "denied",
		repoobs.PresenceUnsupported: "unsupported",
		repoobs.PresenceMeasured:    "measured",
	}
	got := repoobs.Presences()
	if len(got) != len(want) {
		t.Fatalf("Presences() has %d entries, pinned vocabulary has %d: %v", len(got), len(want), got)
	}
	for _, p := range got {
		name, ok := want[p]
		if !ok {
			t.Fatalf("Presences() contains %v (%q) outside the pinned vocabulary", p, p.String())
		}
		if p.String() != name {
			t.Fatalf("Presence %v renders %q, pinned as %q", p, p.String(), name)
		}
	}

	var zero repoobs.Presence
	if zero == repoobs.PresenceUnknown {
		t.Fatal("zero presence is unknown — unstated and observed-unknown collapsed")
	}
	if zero.Stated() {
		t.Fatal("zero presence reads as stated")
	}
	if repoobs.PresenceUnknown == repoobs.PresenceDenied {
		t.Fatal("unknown and denied are the same value")
	}
	if repoobs.PresenceUnknown == repoobs.PresenceUnsupported {
		t.Fatal("unknown and unsupported are the same value")
	}

	clean := validObservation()
	clean.Local = repoobs.Local{Presence: repoobs.PresenceMeasured}
	if err := clean.Validate(); err != nil {
		t.Fatal(err)
	}
	if clean.Local.Presence != repoobs.PresenceMeasured {
		t.Fatal("measured zero local work is not PresenceMeasured")
	}
	if clean.Local.Dirty() {
		t.Fatal("measured zero local work reads as dirty")
	}

	unknown := validObservation()
	unknown.Local = repoobs.Local{Presence: repoobs.PresenceUnknown}
	if unknown.Local.Dirty() {
		t.Fatal("unknown local work reads as dirty — that is measured zero wearing unknown's clothes")
	}
}

func TestObservation_CarriesTimeRevisionAndCoverage(t *testing.T) {
	o := validObservation()
	if o.ObservedAt.IsZero() {
		t.Fatal("missing observed_at")
	}
	if o.ObservationRevision == "" {
		t.Fatal("missing observation_revision")
	}
	if !o.Coverage.Stated() {
		t.Fatal("coverage unstated")
	}

	o.ObservedAt = time.Time{}
	if err := o.Validate(); !errors.Is(err, repoobs.ErrInvalid) {
		t.Fatalf("missing observed_at: %v, want ErrInvalid", err)
	}
}

func TestCurrent_UnknownCoverageIsNeverCurrent(t *testing.T) {
	o := validObservation()
	o.Coverage = repoobs.CoverageUnknown
	o.Source = repoobs.Source{Presence: repoobs.PresenceUnknown}
	o.Base = repoobs.Base{Presence: repoobs.PresenceUnknown}
	if err := o.Validate(); err != nil {
		t.Fatalf("honest unknown observation: %v", err)
	}
	if o.Current() {
		t.Fatal("unknown coverage reported as current")
	}
}

func TestValidate_RefusesUnknownToCurrentConversion(t *testing.T) {
	o := validObservation()
	o.Coverage = repoobs.CoverageUnknown
	o.Source = repoobs.Source{Presence: repoobs.PresenceUnknown}
	o.Base = repoobs.Base{Presence: repoobs.PresenceUnknown}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}

	converted := o
	converted.Coverage = repoobs.CoverageMeasured
	if err := converted.Validate(); !errors.Is(err, repoobs.ErrInvalid) {
		t.Fatalf("unknown axes relabelled measured: %v, want ErrInvalid", err)
	}
	if converted.Current() {
		t.Fatal("unknown-to-current conversion made Current() true")
	}
}

func TestValidate_RefusesFalselyCompleteCombinations(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*repoobs.Observation)
	}{
		{
			name: "unknown coverage with measured axes",
			mut: func(o *repoobs.Observation) {
				o.Coverage = repoobs.CoverageUnknown
			},
		},
		{
			name: "base equal to an unobserved source",
			mut: func(o *repoobs.Observation) {
				o.Source.Presence = repoobs.PresenceUnknown
				o.Source.Fetch = repoobs.FetchUnspecified
				o.Base.Presence = repoobs.PresenceMeasured
				o.Base.Relation = repoobs.RelationEqual
			},
		},
		{
			name: "denied source reporting a fetch success",
			mut: func(o *repoobs.Observation) {
				o.Source.Presence = repoobs.PresenceDenied
				o.Source.Fetch = repoobs.FetchSucceeded
			},
		},
		{
			name: "unsupported local work carrying dirty flags",
			mut: func(o *repoobs.Observation) {
				o.Local.Presence = repoobs.PresenceUnsupported
				o.Local.Unstaged = true
			},
		},
		{
			name: "index denied claiming a searchable generation",
			mut: func(o *repoobs.Observation) {
				o.Index.Presence = repoobs.PresenceDenied
				o.Index.LocalDelta = "7"
			},
		},
		{
			name: "missing resource_id",
			mut: func(o *repoobs.Observation) { o.ResourceID = "" },
		},
		{
			name: "missing source identity",
			mut: func(o *repoobs.Observation) { o.SourceIdentity = "" },
		},
		{
			name: "missing trust domain",
			mut: func(o *repoobs.Observation) { o.TrustDomain = "" },
		},
		{
			name: "wrong schema version",
			mut: func(o *repoobs.Observation) { o.SchemaVersion = 0 },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := validObservation()
			tt.mut(&o)
			if err := o.Validate(); !errors.Is(err, repoobs.ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestDecode_RejectsAWorkspaceKeyedObservation(t *testing.T) {
	o := validObservation()
	raw, err := contracts.Encode(o)
	if err != nil {
		t.Fatal(err)
	}
	var loose map[string]any
	if err := json.Unmarshal(raw, &loose); err != nil {
		t.Fatal(err)
	}
	loose["workspace"] = "ws:project"
	widened, err := json.Marshal(loose)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repoobs.Decode(widened); err == nil {
		t.Fatal("workspace field accepted — observation identity is not workspace-keyed")
	}
}

func TestGolden_ObservationRoundTrip(t *testing.T) {
	encoded, err := contracts.Encode(validObservation())
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	got, err := repoobs.Decode(encoded)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	again, err := contracts.Encode(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again) {
		t.Fatalf("round-trip drifted\n--- got ---\n%s\n--- want ---\n%s", again, encoded)
	}
}

func TestEnvelope_LegacyWorkEventRoundTrip(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "legacy-workevent.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := repoobs.DecodeEnvelope(want)
	if err != nil {
		t.Fatalf("legacy envelope refused: %v", err)
	}
	if env.PayloadType != "usage_fact" {
		t.Fatalf("payload_type = %q, want usage_fact", env.PayloadType)
	}
	if len(env.Payload) != 0 {
		t.Fatalf("legacy envelope grew a payload: %s", env.Payload)
	}
	got, err := contracts.Encode(env)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("legacy WorkEvent envelope did not round-trip\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestEnvelope_CarriesAnObservationPayloadWithoutDroppingIdentity(t *testing.T) {
	obsRaw, err := contracts.Encode(validObservation())
	if err != nil {
		t.Fatal(err)
	}
	env := repoobs.Envelope{
		SchemaVersion:  repoobs.SchemaVersion,
		Org:            "org:example",
		EventID:        "evt:01a01578-ec47-7209-9200-cac2a1f75c7f",
		EventType:      "repo_observation",
		WorkID:         "work:cr-41",
		ResourceID:     "resource:repo/governed",
		OccurredAt:     observedAt(),
		ObservedAt:     observedAt().Add(12 * time.Second),
		TimeSource:     "authority:host-clock",
		SourceSystem:   "arqtos-core",
		SourceNativeID: "obs-1",
		DedupeKey:      "arqtos-core:obs-1",
		Actor:          repoobs.Actor{None: true},
		Audience:       "audience:org",
		PayloadType:    repoobs.PayloadType,
		Payload:        json.RawMessage(bytes.TrimSpace(obsRaw)),
	}
	if err := env.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := contracts.Encode(env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repoobs.DecodeEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.PayloadType != repoobs.PayloadType {
		t.Fatalf("payload_type = %q", got.PayloadType)
	}
	obs, err := repoobs.Decode(got.Payload)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if err := obs.Validate(); err != nil {
		t.Fatal(err)
	}
	if got.ResourceID != env.ResourceID || got.EventID != env.EventID {
		t.Fatalf("envelope identity dropped: %+v", got)
	}
}

func TestEnvelope_RejectsObservationPayloadTypeWithoutPayload(t *testing.T) {
	env := repoobs.Envelope{
		SchemaVersion:  repoobs.SchemaVersion,
		Org:            "org:example",
		EventID:        "evt:01a01578-ec47-7209-9200-cac2a1f75c7f",
		EventType:      "repo_observation",
		WorkID:         "work:cr-41",
		ResourceID:     "resource:repo/governed",
		OccurredAt:     observedAt(),
		ObservedAt:     observedAt(),
		TimeSource:     "authority:host-clock",
		SourceSystem:   "arqtos-core",
		SourceNativeID: "obs-1",
		DedupeKey:      "arqtos-core:obs-1",
		Actor:          repoobs.Actor{None: true},
		Audience:       "audience:org",
		PayloadType:    repoobs.PayloadType,
	}
	if err := env.Validate(); !errors.Is(err, repoobs.ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}
