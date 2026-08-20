package contracts

import "time"

// ⚠️ THE FIELD SETS HERE ARE v0 AND DELIBERATELY NARROW. Every field is one the
// ratified act-kernel decision NAMES. A field that seemed useful but is not named
// is absent, because a public record type is far harder to remove from than to
// add to — an outsider's verifier compiles against this.

// A PermitID is an ActionPermit's identity, and it is an OUTPOINT: the issuing
// act's body id plus the output index within it.
//
// ⚠️ Derivation is not authority. A permit id that can be computed but does not
// correspond to an accepted issuer output is COUNTERFEIT — so this type carries
// the issuer's act body id rather than a self-describing hash.
type PermitID struct {
	IssuerActBodyID string `json:"issuer_act_body_id"`
	OutputIndex     Number `json:"output_index"`
}

// A ManifestEntry is one exact path and the digest of its content.
//
// The publication-time exact path/content manifest is the safety invariant —
// it replaced "tracking configuration", which described intent rather than fact.
type ManifestEntry struct {
	Path          string `json:"path"`
	ContentDigest string `json:"content_digest"`
}

// A ResourceRead declares a resource the act depends on and the version it was
// evaluated against.
//
// ⚠️ The declaration is never trusted. The reducer's actual access is measured
// through an instrumented state view and must be a SUBSET of what is declared;
// an undeclared access is a fault, not a warning. This field is what that check
// is against, which is why an absent expected_version is not a smaller claim —
// it is no claim.
type ResourceRead struct {
	ResourceID      string `json:"resource_id"`
	ExpectedVersion Number `json:"expected_version"`
}

// A ResourceWrite declares a resource the act changes, the version it expects to
// replace, and the digest it intends to produce.
type ResourceWrite struct {
	ResourceID      string `json:"resource_id"`
	ExpectedVersion Number `json:"expected_version"`
	NewDigest       string `json:"new_digest"`
}

// A Footprint is the act's declared dependency set.
//
// ⚠️ Anti-dependencies are real and are why NamespaceRoots exists: enumerating a
// lane takes a dependency on the namespace root, or a phantom insert between
// evaluation and acceptance is invisible.
type Footprint struct {
	Reads          []ResourceRead  `json:"reads"`
	Writes         []ResourceWrite `json:"writes"`
	NamespaceRoots []string        `json:"namespace_roots"`
}

// An ActSpec is the immutable signed intent — one mandatory signed body per act
// kind, with no selectable signature masks.
//
// ⚠️ ActBodyID is computed over the UNSIGNED body, and witnesses live in a
// separate layer. If witnesses were inside the signed body, every added
// signature would change the act's identity, and a co-signer could rename an act
// by co-signing it.
type ActSpec struct {
	SchemaVersion Number `json:"schema_version"`
	Kind          Kind   `json:"kind"`

	// ActBodyID identifies this act: the digest over the unsigned immutable body.
	ActBodyID string `json:"act_body_id"`
	// ActKind is the kind of act, which selects the one mandatory signed body.
	ActKind string `json:"act_kind"`

	// RepositoryID and ChangeRequestID are PROVIDER-NEUTRAL identities, not a
	// vendor's numbers. A model built from one host's identifiers encodes that
	// host's accidents as contract.
	RepositoryID    string `json:"repository_id"`
	ChangeRequestID string `json:"change_request_id"`

	// HeadSHA is the exact revision the decision is about. BaseConstraint is
	// what the merge must apply onto.
	HeadSHA        string `json:"head_sha"`
	BaseConstraint string `json:"base_constraint"`
	// MergeMethod is bound because squash, rebase and merge produce different
	// commits — binding the decision without it would bind an ambiguous effect.
	MergeMethod string `json:"merge_method"`
	// CandidateTreeOID is the exact tree the freeze-then-promote step produced.
	CandidateTreeOID string `json:"candidate_tree_oid"`

	// CharterDigest and CharterRuleID bind the COMPLETE charter, not a revealed
	// branch of it: a revealed branch cannot prove that no hidden weaker path
	// exists, which is why selective disclosure is deferred behind a
	// full-policy invariant certificate.
	CharterDigest string `json:"charter_digest"`
	CharterRuleID string `json:"charter_rule_id"`

	Manifest  []ManifestEntry `json:"manifest"`
	Permit    PermitID        `json:"permit"`
	Footprint Footprint       `json:"footprint"`

	// Nonce and ValidUntil make the intent single-use and time-bounded.
	// ⚠️ Expiry is mandatory: an intent with no expiry is a standing authority
	// nobody decided to grant.
	Nonce      string    `json:"nonce"`
	ValidUntil time.Time `json:"valid_until"`

	// ReducerVersion is recorded so a historical act verifies under the
	// semantics it was ACCEPTED under. Decoders are retained indefinitely for
	// the same reason.
	ReducerVersion string `json:"reducer_version"`
}

// A WitnessKind distinguishes a cryptographic signature from a human
// ratification. They are different claims and must not be counted together.
type WitnessKind string

const (
	// WitnessSignature is a key attesting to the act body.
	WitnessSignature WitnessKind = "signature"
	// WitnessRatification is a human approving it where the charter requires
	// one. ⚠️ `ratified_at` DERIVES from the accepted witness and its time
	// source; it is never authored on the act.
	WitnessRatification WitnessKind = "ratification"
)

// A Witness is one append-only signature or ratification over an act body.
type Witness struct {
	SchemaVersion Number `json:"schema_version"`
	Kind          Kind   `json:"kind"`

	// ActBodyID is what this witness is about.
	ActBodyID   string      `json:"act_body_id"`
	WitnessKind WitnessKind `json:"witness_kind"`

	// Principal and KeyID say who, and with which key. ⚠️ KeyID matters more
	// than it looks: a verifier must establish whether that key was valid WHEN
	// THE SIGNATURE WAS MADE, from the key registry's validity windows — a
	// signature without historical key status proves only that some key signed
	// some bytes.
	Principal string `json:"principal"`
	KeyID     string `json:"key_id"`

	Signature string `json:"signature"`

	// At and TimeSource record when, and on whose authority.
	// ⚠️ TimeSource is required. A timestamp with no named authority cannot be
	// replayed, because replay must not consult the current clock.
	At         time.Time `json:"at"`
	TimeSource string    `json:"time_source"`
}

// An EvidenceEventKind names what happened.
type EvidenceEventKind string

const (
	// EventAcceptance is the act being accepted at the canonical head.
	EventAcceptance EvidenceEventKind = "acceptance"
	// EventAttempt is an effect being attempted against the host.
	EventAttempt EvidenceEventKind = "attempt"
	// EventObservation is what the observer independently saw.
	EventObservation EvidenceEventKind = "observation"
	// EventReceipt is the host's own record of the effect.
	EventReceipt EvidenceEventKind = "receipt"
	// EventViolation is a governed effect with no valid permit and receipt.
	// ⚠️ Unknown is ALWAYS a violation, never noise.
	EventViolation EvidenceEventKind = "violation"
)

// An EvidenceEvent is one append-only fact about an act's journey.
//
// ⚠️ Receipts are EVENTS in this layer, never a status field elsewhere. An
// ambiguous attempt followed by a settled one leaves BOTH events; collapsing
// them into a current status deletes the ambiguity from replay, and the
// ambiguity is the case an audit is looking for.
type EvidenceEvent struct {
	SchemaVersion Number `json:"schema_version"`
	Kind          Kind   `json:"kind"`

	EventKind EvidenceEventKind `json:"event_kind"`
	ActBodyID string            `json:"act_body_id"`

	// Sequence is the accepted order. It is the key a mirrored chain is keyed
	// by, and it never renumbers.
	Sequence Number `json:"sequence"`

	// AcceptedTime is the recorded acceptance time WITH its authority and that
	// authority's clock provenance.
	//
	// ⚠️ It is [AcceptedTime], not a bare time plus a name. A string authority
	// carries no provenance, so a reader could not tell a disciplined clock from
	// a virtual machine that has been suspended — and a clock-rollback check
	// written against observations would have nothing to evaluate. Predicates
	// never evaluate "now"; they evaluate this.
	AcceptedTime AcceptedTime `json:"accepted_time"`

	// FinalSHA is the actual merged revision, bound at effect time rather than
	// at decision time — the final commit does not exist before a squash or
	// rebase merge, which is why binding it up front was unimplementable.
	FinalSHA string `json:"final_sha,omitempty"`
	// HostOperationID is the host's own identifier for the operation.
	HostOperationID string `json:"host_operation_id,omitempty"`

	// Detail carries the event-kind-specific facts. It is a string map in v0
	// rather than a union: typed grains are added as they are staffed, and an
	// under-specified union is harder to widen than a map is to replace.
	Detail map[string]string `json:"detail,omitempty"`
}

// A StatusEntry is one projected current answer.
type StatusEntry struct {
	ActBodyID string `json:"act_body_id"`
	// State is the projection's summary. It is DERIVED and is never authored.
	State string `json:"state"`
	// ObservedSpecID and DesiredSpecID are CONTENT identities, replacing
	// generation counters wherever a hash already gives exact identity.
	DesiredSpecID  string `json:"desired_spec_id,omitempty"`
	ObservedSpecID string `json:"observed_spec_id,omitempty"`
}

// A StatusView is a disposable projection.
//
// ⚠️ It is authoritative for NOTHING. Deleting it entirely must leave governed
// verification valid, and rebuilding it from zero must reach the same result —
// that property is what stops a projection quietly becoming a second source of
// truth.
type StatusView struct {
	SchemaVersion Number `json:"schema_version"`
	Kind          Kind   `json:"kind"`

	// RebuiltThroughSequence is the accepted sequence this projection covers.
	// A reader comparing two projections compares this, not their build times.
	RebuiltThroughSequence Number `json:"rebuilt_through_sequence"`

	Entries []StatusEntry `json:"entries"`
}
