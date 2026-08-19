// Package predicate is the Arqtos Predicate IR: the only language in which a
// charter rule can express a condition.
//
// # Why an IR and not a policy language
//
// The tempting move is to adopt a general-purpose policy language. It was
// refused, and the reason is precise: two independently designed languages are
// two semantics, not two vendors. A rule that means one thing to the runtime and
// another to the verifier is worse than a rule neither can evaluate, because it
// produces confident disagreement.
//
// A second evaluator may later be adapted to execute THIS IR, judged against a
// differential corpus — that is a second implementation of one semantics, which
// is the shape the two-vendor rule actually asks for.
//
// # The properties, all four load-bearing
//
//   - fixed typed operators — the operator set is closed, so a rule cannot
//     reach for something the verifier does not have;
//   - no IO and no current time — the same purity the reducer has, for the same
//     replay reason;
//   - bounded — evaluation terminates within a structural budget, so a
//     pathological rule cannot hang an outsider's verifier;
//   - deterministic errors — a rule that fails fails identically everywhere,
//     because a rejection code is part of the contract and not a message.
//
// # ⚠️ Skeleton
//
// Empty on purpose. Version 1 carries three fixed predicates and no compiler;
// the script compiler and selective disclosure are deferred behind their own
// triggers, and building the general case first is how the fixed three would
// quietly become a language.
//
// Authority: dcn-arq-00005 §12, §23, §37.
package predicate
