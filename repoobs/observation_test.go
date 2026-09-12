package repoobs_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		SchemaVersion:       repoobs.SchemaVersion,
		ResourceID:          "resource:repo/governed",
		SourceIdentity:      "src:host/acme-platform-main",
		TrustDomain:         "td_isolate_acme",
		SourceRef:           "refs/heads/main",
		SourceRevision:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ObservedAt:          observedAt(),
		ObservationRevision: "obs:1",
		Coverage:            repoobs.CoverageMeasured,
		Source:              measuredSource(),
		Base:                repoobs.Base{Presence: repoobs.PresenceMeasured, Relation: repoobs.RelationEqual},
		Local:               repoobs.Local{Presence: repoobs.PresenceMeasured},
		Durability:          repoobs.Draft{Presence: repoobs.PresenceMeasured, Level: repoobs.DurabilityLocalGit},
		Index:               repoobs.Index{Presence: repoobs.PresenceMeasured, BaseProfile: "profile:v1", LocalDelta: "0"},
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

func TestREADME_NamesTheContractVersion(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	if !strings.Contains(doc, "[`repoobs`](repoobs/)") {
		t.Fatal("README does not list the repoobs package")
	}
	if !strings.Contains(doc, "`SchemaVersion` 1") {
		t.Fatal("README does not identify repoobs SchemaVersion 1")
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
	flagged := repoobs.Local{Presence: repoobs.PresenceUnknown, Unstaged: true}
	if flagged.Dirty() {
		t.Fatal("unknown local work with a dirty flag reported Dirty — the presence guard is the distinction")
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
	if !o.Current() {
		t.Fatal("measured equal base should be current")
	}
	o.Coverage = repoobs.CoverageUnknown
	if o.Current() {
		t.Fatal("unknown coverage reported as current while the source still looks measured")
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
	converted.Source = repoobs.Source{Presence: repoobs.PresenceMeasured, Fetch: repoobs.FetchSucceeded}
	converted.Base = repoobs.Base{Presence: repoobs.PresenceMeasured, Relation: repoobs.RelationEqual}
	if err := converted.Validate(); err != nil {
		t.Fatal(err)
	}
	if converted.Current() {
		t.Fatal("unknown-to-current conversion with an empty OID made Current() true")
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
			mut:  func(o *repoobs.Observation) { o.ResourceID = "" },
		},
		{
			name: "missing source identity",
			mut:  func(o *repoobs.Observation) { o.SourceIdentity = "" },
		},
		{
			name: "missing trust domain",
			mut:  func(o *repoobs.Observation) { o.TrustDomain = "" },
		},
		{
			name: "wrong schema version",
			mut:  func(o *repoobs.Observation) { o.SchemaVersion = 0 },
		},
		{
			name: "denied coverage with measured source",
			mut:  func(o *repoobs.Observation) { o.Coverage = repoobs.CoverageDenied },
		},
		{
			name: "unsupported coverage with measured source",
			mut:  func(o *repoobs.Observation) { o.Coverage = repoobs.CoverageUnsupported },
		},
		{
			name: "unknown draft claiming published durability",
			mut: func(o *repoobs.Observation) {
				o.Durability = repoobs.Draft{Presence: repoobs.PresenceUnknown, Level: repoobs.DurabilityPublished}
			},
		},
		{
			name: "unsupported draft claiming published durability",
			mut: func(o *repoobs.Observation) {
				o.Durability = repoobs.Draft{Presence: repoobs.PresenceUnsupported, Level: repoobs.DurabilityPublished}
			},
		},
		{
			name: "unknown base claiming equal",
			mut: func(o *repoobs.Observation) {
				o.Base = repoobs.Base{Presence: repoobs.PresenceUnknown, Relation: repoobs.RelationEqual}
			},
		},
		{
			name: "denied base claiming equal",
			mut: func(o *repoobs.Observation) {
				o.Base = repoobs.Base{Presence: repoobs.PresenceDenied, Relation: repoobs.RelationEqual}
			},
		},
		{
			name: "unknown source reporting fetch success",
			mut: func(o *repoobs.Observation) {
				o.Coverage = repoobs.CoverageUnknown
				o.Source = repoobs.Source{Presence: repoobs.PresenceUnknown, Fetch: repoobs.FetchSucceeded}
			},
		},
		{
			name: "unsupported source reporting fetch success",
			mut: func(o *repoobs.Observation) {
				o.Coverage = repoobs.CoverageUnknown
				o.Source = repoobs.Source{Presence: repoobs.PresenceUnsupported, Fetch: repoobs.FetchSucceeded}
			},
		},
		{
			name: "unknown index claiming a generation",
			mut: func(o *repoobs.Observation) {
				o.Index = repoobs.Index{Presence: repoobs.PresenceUnknown, LocalDelta: "7"}
			},
		},
		{
			name: "unsupported index claiming a generation",
			mut: func(o *repoobs.Observation) {
				o.Index = repoobs.Index{Presence: repoobs.PresenceUnsupported, LocalDelta: "7"}
			},
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
