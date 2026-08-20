package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// ErrInvalidTime is a recorded time that cannot be used in the replay path —
// unnamed authority, unstated clock provenance, or no time at all.
//
// ⚠️ Its own sentinel rather than a generic invalid-input error, because the
// remedy is specific: record the authority and its provenance at acceptance. A
// caller told only "invalid" will look at the timestamp.
var ErrInvalidTime = errors.New("contracts: recorded time is unusable")

// ErrInvalidGenesis is a malformed ledger bootstrap: a genesis act that does not
// say what the first link of a chain has to say.
var ErrInvalidGenesis = errors.New("contracts: repository genesis is unusable")

// ErrAmplification is an issued authority that exceeds the one it was issued
// from.
//
// ⚠️ Distinct from ErrInvalidGenesis on purpose. A malformed act is an authoring
// bug; amplification is authority appearing from nowhere, and the two deserve
// different reactions.
var ErrAmplification = errors.New("contracts: issued authority exceeds its parent")

// SchemaVersion is the version every record kind in this package carries.
//
// ⚠️ It is on EVERY kind, not on the bundle, and that is deliberate. A bundle
// carries records written at different times; a single version on the container
// would say only when the container was written, which is the one moment nobody
// is verifying.
const SchemaVersion = 1

// SchemaVersionNumber is [SchemaVersion] in the form records carry it.
//
// ⚠️ DERIVED, never restated. Two constants for one version drift, and the one
// nobody updates becomes the lie.
var SchemaVersionNumber = FromInt(SchemaVersion)

// A Number is an integer carried as its decimal string.
//
// ⚠️ THIS IS NOT A STYLE CHOICE. Records in this package are digested with the
// canonical encoding, and that encoding FORBIDS JSON numbers: two encoders can
// disagree about how a number is written — exponent form, trailing zero,
// precision — while agreeing it is the same number, and a record whose digest is
// its identity cannot afford that.
//
// An int-typed field therefore makes its record unencodable, and the failure is
// invisible until the first time an identity is computed. This type names the
// rule once, so a reflection test can require it everywhere rather than trusting
// each author to remember it.
type Number string

// ErrNotANumber is a Number that is not the canonical spelling of an integer.
var ErrNotANumber = errors.New("contracts: not the canonical decimal spelling of an integer")

// FromInt renders i as a Number.
func FromInt(i int64) Number { return Number(strconv.FormatInt(i, 10)) }

// Int parses n.
//
// ⚠️ A NON-CANONICAL SPELLING IS REFUSED, not silently accepted. "007" and "7"
// would otherwise be two spellings of one value in bytes that are digested,
// which is the exact ambiguity carrying integers as strings exists to remove.
func (n Number) Int() (int64, error) {
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrNotANumber, string(n))
	}
	if strconv.FormatInt(i, 10) != string(n) {
		return 0, fmt.Errorf("%w: %q is not how %d is written", ErrNotANumber, string(n), i)
	}
	return i, nil
}

// Valid reports whether n is the canonical spelling of an integer.
func (n Number) Valid() bool { _, err := n.Int(); return err == nil }

// A Kind names one record layer.
//
// ⚠️ The four layers are NOT one bag of structs with a discriminator. They have
// genuinely different mutation rules — see [MutabilityOf] — and collapsing any
// two loses something an audit needs.
type Kind string

const (
	// KindActSpec is the immutable signed intent. Content and effect hashes
	// live here, and nothing in this layer is ever rewritten.
	KindActSpec Kind = "actspec"

	// KindWitness is an append-only signature or ratification statement.
	//
	// ⚠️ Witnesses live OUTSIDE the signed body. `act_body_id` is computed over
	// the unsigned body precisely so that a co-signer never changes the thing
	// being identified — if witnesses were inside, every additional signature
	// would rename the act.
	KindWitness Kind = "witness"

	// KindEvidenceEvent is an append-only acceptance, attempt, observation,
	// receipt or violation.
	//
	// ⚠️ A host receipt is an EVENT here, never an overwritable status field.
	// Overwrite it and an earlier AMBIGUOUS receipt disappears from replay —
	// which is precisely the case an audit needs to see.
	KindEvidenceEvent Kind = "evidence_event"

	// KindStatusView is a disposable projection, rebuildable from zero to the
	// same result, and authoritative for nothing.
	KindStatusView Kind = "status_view"
)

var kinds = []Kind{KindActSpec, KindWitness, KindEvidenceEvent, KindStatusView}

// Kinds returns the closed set of record kinds, as a copy.
func Kinds() []Kind { return slices.Clone(kinds) }

// Valid reports whether k is in the closed set.
func (k Kind) Valid() bool { return slices.Contains(kinds, k) }

// A Mutability is how a record layer may change after it is written. It is the
// record-layer mutation matrix, expressed so it can be tested rather than
// remembered.
type Mutability int

const (
	// MutabilityUnspecified is the zero value: nothing was said. It is not a
	// mutability, and a kind that reports it is a kind this table forgot.
	MutabilityUnspecified Mutability = iota
	// Immutable: written once, never amended. A correction is a new record.
	Immutable
	// AppendOnly: new records join the layer; existing ones are never edited
	// and never deleted.
	AppendOnly
	// Disposable: may be deleted entirely and rebuilt from the layers above,
	// and rebuilding must reach the SAME result.
	Disposable
)

var mutabilityNames = map[Mutability]string{
	MutabilityUnspecified: "unspecified",
	Immutable:             "immutable",
	AppendOnly:            "append_only",
	Disposable:            "disposable",
}

// String names m, or marks it invalid. It never returns the empty string: a
// blank would print as an absent field rather than a broken one.
func (m Mutability) String() string {
	if n, ok := mutabilityNames[m]; ok {
		return n
	}
	return fmt.Sprintf("invalid_mutability(%d)", int(m))
}

// mutability is the record-layer mutation matrix.
var mutability = map[Kind]Mutability{
	KindActSpec:       Immutable,
	KindWitness:       AppendOnly,
	KindEvidenceEvent: AppendOnly,
	KindStatusView:    Disposable,
}

// MutabilityOf reports how k may change after it is written.
//
// A kind outside the closed set reports [MutabilityUnspecified] rather than a
// permissive default — an unknown kind must not inherit the loosest rule in the
// table.
func MutabilityOf(k Kind) Mutability { return mutability[k] }

// Decode parses one record of type T from data.
//
// ⚠️ UNKNOWN FIELDS ARE REJECTED, and this is the single most important line in
// the package. The permissive default — decode what you recognise, skip the rest
// — is exactly wrong here: a field the verifier skips is a field the runtime may
// have ACTED ON, so the two agree on a hash while disagreeing on meaning. That
// disagreement then surfaces on the disputed act, months later, with nothing to
// say which side was right.
//
// Rejection turns that into a loud failure at the boundary instead of a silent
// one at the conclusion.
func Decode[T any](data []byte) (T, error) {
	var out T
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("decoding record: %w", err)
	}
	// ⚠️ Trailing content is refused too. Two concatenated records must not
	// decode as the first one — a reader that silently dropped the remainder
	// would report a partial bundle as a whole one.
	if dec.More() {
		return out, fmt.Errorf("decoding record: trailing content after the first record")
	}
	return out, nil
}

// Encode renders a record as canonical-shaped JSON for fixtures and transport.
//
// ⚠️ This is NOT the canonical act encoding. An act's identity is a hash of the
// bytes produced by the kernel's canonical encoder, which owns domain separation
// and its own field ordering forever. This is the ordinary JSON form used for
// records, fixtures and transport, and calling it canonical would invite exactly
// the confusion the kernel package exists to prevent.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding record: %w", err)
	}
	return buf.Bytes(), nil
}
