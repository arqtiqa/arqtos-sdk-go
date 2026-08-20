// Package contracts holds the public record types: the shapes an outsider needs
// in order to read what a bundle contains, without needing the runtime that
// produced it.
//
// # The four record layers, kept apart
//
// The records are not one bag of structs. They are four layers with genuinely
// different mutation rules — see [MutabilityOf] — and collapsing any two loses
// something an audit needs:
//
//   - [ActSpec] — the immutable signed intent. Content and effect hashes live
//     here, and nothing in this layer is ever rewritten.
//   - [Witness] — append-only signatures and human ratifications, kept OUTSIDE
//     the signed body so that a co-signer never changes the thing being
//     identified.
//   - [EvidenceEvent] — append-only acceptance, attempts, observations, receipts
//     and violations. ⚠️ A host receipt is an immutable EVENT and never an
//     overwritable status field: overwrite it and an earlier AMBIGUOUS receipt
//     vanishes from replay, which is precisely the case an audit needs.
//   - [StatusView] — a disposable projection, rebuildable from zero to the same
//     result, and authoritative for nothing.
//
// ⚠️ What must never appear in the evidence layers: meter rows, heartbeats and
// leases. A governance ledger that accumulates per-person telemetry has become a
// different product with a different legal posture, and the boundary is far
// easier to hold here than to recover later.
//
// # Unknown fields are REJECTED
//
// [Decode] refuses a record carrying a field it does not know, and this is the
// most consequential line in the package. The permissive default — decode what
// you recognise, skip the rest — is exactly wrong: a field the verifier skips is
// a field the runtime may have ACTED ON, so the two agree on a hash while
// disagreeing on meaning. That disagreement surfaces on the disputed act, months
// later, with nothing to say which side was right.
//
// The JSON Schemas in schema/ set `additionalProperties: false` for the same
// reason, so a non-Go consumer validating against them reaches the same verdict.
//
// # One source, checked both ways
//
// The Go types and the JSON Schemas are two descriptions of one contract, and a
// second copy of a fact is a second thing to drift. The package's tests check
// them against each other in BOTH directions — a property with no field, and a
// field with no property, are both drift — and golden fixtures in testdata/ pin
// the byte-level wire form so a rename cannot land unnoticed.
//
// ⚠️ Field sets are v0 and deliberately narrow: every field is one the ratified
// act-kernel decision names. A public record type is far harder to remove from
// than to add to, because an outsider's verifier compiles against it.
package contracts
