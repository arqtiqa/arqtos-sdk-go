// Package repoobs is the versioned repository observation contract.
//
// It is the four-axis RepoObservation (dcn-arq-00007 correction 8,
// concurrency-detail §5.2) plus index freshness as a separate projection.
package repoobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
)

// SchemaVersion is the observation contract consumers pin.
const SchemaVersion = 1

// PayloadType is the WorkEvent payload_type for this grain.
const PayloadType = "repo_observation"

// ErrInvalid is the class of every refusal in this package.
var ErrInvalid = errors.New("repoobs: invalid observation")

// An Observation is one repository's reported state at one moment.
type Observation struct {
	SchemaVersion       int       `json:"schema_version"`
	ResourceID          string    `json:"resource_id"`
	SourceIdentity      string    `json:"source_identity"`
	TrustDomain         string    `json:"storage_trust_domain"`
	SourceRef           string    `json:"source_ref,omitempty"`
	SourceRevision      string    `json:"source_revision,omitempty"`
	SessionID           string    `json:"session_id,omitempty"`
	TreeSetID           string    `json:"tree_set_id,omitempty"`
	ObservedAt          time.Time `json:"observed_at"`
	ObservationRevision string    `json:"observation_revision"`
	Coverage            Coverage  `json:"coverage"`
	Source              Source    `json:"source"`
	Base                Base      `json:"base"`
	Local               Local     `json:"local"`
	Durability          Draft     `json:"durability"`
	Index               Index     `json:"index"`
}

// Source is the remote ref/OID observation.
type Source struct {
	Presence Presence `json:"presence"`
	Ref      string   `json:"ref,omitempty"`
	OID      string   `json:"oid,omitempty"`
	Fetch    Fetch    `json:"fetch,omitempty"`
}

// Base is this tree's pinned base versus the source observation.
type Base struct {
	Presence Presence `json:"presence"`
	Relation Relation `json:"relation,omitempty"`
}

// Local is staged, unstaged, untracked or unpublished work in this tree.
type Local struct {
	Presence    Presence `json:"presence"`
	Staged      bool     `json:"staged,omitempty"`
	Unstaged    bool     `json:"unstaged,omitempty"`
	Untracked   bool     `json:"untracked,omitempty"`
	Unpublished bool     `json:"unpublished,omitempty"`
}

// Dirty reports measured local work. Unknown is not dirty.
func (l Local) Dirty() bool {
	if l.Presence != PresenceMeasured {
		return false
	}
	return l.Staged || l.Unstaged || l.Untracked || l.Unpublished
}

// Draft is the implemented durability evidence level.
type Draft struct {
	Presence Presence   `json:"presence"`
	Level    Durability `json:"level,omitempty"`
}

// Index is searchable coverage of the base/profile and local-delta generation.
type Index struct {
	Presence    Presence `json:"presence"`
	BaseProfile string   `json:"base_profile,omitempty"`
	LocalDelta  string   `json:"local_delta,omitempty"`
	Pending     bool     `json:"pending,omitempty"`
	Missing     bool     `json:"missing,omitempty"`
}

// Decode parses one Observation, rejecting unknown fields.
func Decode(data []byte) (Observation, error) {
	return contracts.Decode[Observation](data)
}

// Current reports measured coverage, a successful fetch of a named OID, and an equal base.
func (o Observation) Current() bool {
	return o.Coverage == CoverageMeasured &&
		o.Source.Presence == PresenceMeasured &&
		o.Source.Fetch == FetchSucceeded &&
		o.Source.OID != "" &&
		o.Base.Presence == PresenceMeasured &&
		o.Base.Relation == RelationEqual
}

// Validate reports every reason o is not a usable observation.
func (o Observation) Validate() error {
	var problems []string
	if o.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("schema_version is %d, want %d", o.SchemaVersion, SchemaVersion))
	}
	for _, f := range []struct{ name, value string }{
		{"resource_id", o.ResourceID},
		{"source_identity", o.SourceIdentity},
		{"storage_trust_domain", o.TrustDomain},
		{"observation_revision", o.ObservationRevision},
	} {
		if strings.TrimSpace(f.value) == "" {
			problems = append(problems, "missing "+f.name)
		}
	}
	if o.ObservedAt.IsZero() {
		problems = append(problems, "missing observed_at")
	}
	if !o.Coverage.Valid() {
		problems = append(problems, "coverage "+o.Coverage.String()+" is outside the vocabulary")
	} else if !o.Coverage.Stated() {
		problems = append(problems, "coverage is unspecified")
	}
	if !o.Source.Presence.Valid() || !o.Base.Presence.Valid() || !o.Local.Presence.Valid() || !o.Durability.Presence.Valid() || !o.Index.Presence.Valid() {
		problems = append(problems, "an axis presence is outside the vocabulary")
	}

	if o.Coverage == CoverageMeasured && o.Source.Presence != PresenceMeasured {
		problems = append(problems, "measured coverage with an unmeasured source")
	}
	if o.Coverage != CoverageMeasured && o.Source.Presence == PresenceMeasured {
		problems = append(problems, "non-measured coverage with a measured source")
	}
	if o.Source.Fetch == FetchSucceeded && o.Source.Presence != PresenceMeasured {
		problems = append(problems, "fetch succeeded without a measured source")
	}
	if o.Base.Relation.Stated() && (o.Source.Presence != PresenceMeasured || o.Base.Presence != PresenceMeasured) {
		problems = append(problems, "base relation stated without a measured source and base")
	}
	if o.Durability.Level != DurabilityUnspecified && o.Durability.Presence != PresenceMeasured {
		problems = append(problems, "durability level stated without a measured presence")
	}
	if o.Local.Presence != PresenceMeasured && (o.Local.Staged || o.Local.Unstaged || o.Local.Untracked || o.Local.Unpublished) {
		problems = append(problems, "local work flags set without a measured presence")
	}
	if o.Index.Presence != PresenceMeasured && (o.Index.BaseProfile != "" || o.Index.LocalDelta != "" || o.Index.Pending || o.Index.Missing) {
		problems = append(problems, "index generation without a measured presence")
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(problems, "; "))
}

// An Envelope is the WorkEvent wrapper. Field names match core's SchemaVersion 1 envelope.
type Envelope struct {
	SchemaVersion  int             `json:"schema_version"`
	Org            string          `json:"org"`
	EventID        string          `json:"event_id"`
	EventType      string          `json:"event_type"`
	WorkID         string          `json:"work_id"`
	ResourceID     string          `json:"resource_id"`
	OccurredAt     time.Time       `json:"occurred_at"`
	ObservedAt     time.Time       `json:"observed_at"`
	TimeSource     string          `json:"time_source"`
	SourceSystem   string          `json:"source_system"`
	SourceNativeID string          `json:"source_native_id"`
	DedupeKey      string          `json:"dedupe_key"`
	CorrectionOf   string          `json:"correction_of,omitempty"`
	Actor          Actor           `json:"actor"`
	Audience       string          `json:"audience"`
	PayloadType    string          `json:"payload_type"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

// An Actor is who or what drove an envelope.
type Actor struct {
	Model string `json:"model,omitempty"`
	None  bool   `json:"none,omitempty"`
}

func (a Actor) stated() bool   { return a.None || a.Model != "" }
func (a Actor) coherent() bool { return a.None != (a.Model != "") }

// DecodeEnvelope parses one WorkEvent envelope, rejecting unknown fields.
func DecodeEnvelope(data []byte) (Envelope, error) {
	return contracts.Decode[Envelope](data)
}

// Validate reports every reason e is not a usable envelope.
func (e Envelope) Validate() error {
	var problems []string
	if e.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("schema_version is %d, want %d", e.SchemaVersion, SchemaVersion))
	}
	for _, f := range []struct{ name, value string }{
		{"org", e.Org},
		{"event_id", e.EventID},
		{"event_type", e.EventType},
		{"work_id", e.WorkID},
		{"resource_id", e.ResourceID},
		{"time_source", e.TimeSource},
		{"source_system", e.SourceSystem},
		{"source_native_id", e.SourceNativeID},
		{"dedupe_key", e.DedupeKey},
		{"audience", e.Audience},
		{"payload_type", e.PayloadType},
	} {
		if strings.TrimSpace(f.value) == "" {
			problems = append(problems, "missing "+f.name)
		}
	}
	if e.OccurredAt.IsZero() {
		problems = append(problems, "missing occurred_at")
	}
	if e.ObservedAt.IsZero() {
		problems = append(problems, "missing observed_at")
	}
	switch {
	case !e.Actor.stated():
		problems = append(problems, "actor states neither a model nor an explicit none")
	case !e.Actor.coherent():
		problems = append(problems, "actor claims both a model and none")
	}
	if e.PayloadType == PayloadType {
		if len(e.Payload) == 0 {
			problems = append(problems, "repo_observation payload_type without a payload")
		} else {
			obs, err := Decode(e.Payload)
			if err != nil {
				problems = append(problems, "payload: "+err.Error())
			} else if err := obs.Validate(); err != nil {
				problems = append(problems, "payload: "+err.Error())
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(problems, "; "))
}
