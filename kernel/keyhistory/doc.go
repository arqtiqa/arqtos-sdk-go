// Package keyhistory holds the rules for deciding whether a signature was valid
// WHEN IT WAS MADE — which is a different question from whether its key is
// valid now, and it is the question replay actually asks.
//
// # Why history rather than a key set
//
// A verifier replaying a two-year-old act cannot ask "is this key good?" — the
// key may have been rotated, revoked or transferred since, all legitimately.
// Checking against the current set would invalidate correct history; ignoring
// revocation would validate a compromised signature. So keys carry validity
// intervals, and issuance, rotation and revocation are ordinary acts on the tape
// rather than a subsystem beside it.
//
// # ⚠️ What a tenant tape cannot tell you
//
// Replay over one tenant's tape proves the history is internally consistent. It
// does NOT detect a split view: an administrator who forked the tape before a
// revocation produces a fork that is perfectly self-consistent. Until an
// independent witness exists, that limit is stated on the claim rather than
// papered over — a forked key history downgrades the result, visibly.
//
// # Refused, so it is not re-proposed
//
// No hierarchical-deterministic key tree, no organisation master seed, no secret
// sharing product. Principal keys are independently generated or held in the
// customer's own key manager: bring-your-own custody wins, and a master seed is
// exactly the single point this design must not have.
//
// # ⚠️ Skeleton
//
// Empty on purpose. Authority: dcn-arq-00005 §14, §26, §30.
package keyhistory
