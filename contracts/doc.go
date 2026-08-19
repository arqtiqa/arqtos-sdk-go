// Package contracts holds the public record types: the shapes an outsider needs
// in order to read what a bundle contains, without needing the runtime that
// produced it.
//
// # One generated source, never two hand-written copies
//
// These types and the runtime's own are generated from ONE schema source. A
// second hand-maintained copy is a second thing to drift, and the drift would
// surface as a verifier disagreeing with the runtime about a field's meaning —
// the failure this whole boundary exists to prevent.
//
// # The four record layers, kept apart
//
// The records are not one bag of structs. They are four layers with genuinely
// different mutation rules, and collapsing any two loses something:
//
//   - the immutable signed intent — what was asked for, and what was signed;
//   - the append-only witness layer — signatures and human ratifications, kept
//     OUTSIDE the signed body so that a co-signer never changes the thing being
//     identified;
//   - the append-only evidence events — acceptance, attempts, observations,
//     receipts and violations. ⚠️ A host receipt is an immutable EVENT and never
//     an overwritable status field: overwrite it and an earlier ambiguous
//     receipt vanishes from replay, which is precisely the case an audit needs;
//   - the disposable status view — a projection, rebuildable from zero to the
//     same result, and authoritative for nothing.
//
// ⚠️ What must never appear in the evidence layers: meter rows, heartbeats and
// leases. A governance ledger that accumulates per-person telemetry has become a
// different product with a different legal posture, and the boundary is easier
// to hold here than to recover later.
//
// # ⚠️ Skeleton
//
// Empty on purpose. The executable schemas — types, JSON Schema and a golden
// fixture per kind per version, each carrying a schema version — land as one
// deliberate piece of work with the runtime's half, from the same source.
//
// Authority: dcn-arq-00005 §35; dcn-arq-00007 correction 2.
package contracts
