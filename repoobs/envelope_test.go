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
