// Package reduce is the ONE pure reducer: the function from an accepted act and
// the state it declares to the outbox commands that follow from it.
//
// # Purity is the whole contract
//
// The reducer has NO ambient access at all — no filesystem, no environment, no
// clock, no network. A mediated state view is the only door, and an access the
// act did not declare is a fault rather than a convenience. This is not
// defensive style: replay is only meaningful if the same inputs produce the same
// outputs on a different machine, years later, and every ambient read is an
// input nobody recorded.
//
// Two consequences that are easy to erode and are therefore stated:
//
//   - ⚠️ It never reads "now". Predicates evaluate a recorded acceptance time
//     from a named time authority. A reducer that consulted the wall clock would
//     produce a different answer on replay than it did on acceptance, which is
//     the one thing replay exists to detect.
//   - ⚠️ It RETURNS, and never invokes — at any depth. Reduction produces
//     immutable outbox commands and makes zero calls. A reducer permitted one
//     level of invocation is a reducer whose behaviour depends on what it
//     called, and that dependency is not in the tape.
//
// # What it decides, and what it does not
//
// Reduce runs every rule it declares and ACCEPTS what none of them refused, so
// the property a caller relies on is that adding a rule can only ever refuse
// more — never widen what is admitted.
// Today those rules are: the accepted prefix is a chain; its acceptance times do
// not regress and come from one authority; a candidate is not already on the
// tape; its permit was not already spent; and an independent OBSERVATION reports
// the tree the act binds.
//
// # ⚠️ What it does NOT check, and a caller must not assume
//
//   - NON-AMPLIFICATION. A candidate carries a permit OUTPOINT, not a permit
//     body, so this package has no scope or expiry to compare against the root
//     grant. Attenuation is checked where permits are ISSUED, and a reducer that
//     has not seen the issuance is trusting that path.
//   - The CANONICAL HEAD. Nothing here holds a lock or performs a
//     compare-and-swap. A caller that reduced against a stale head and then
//     published would silently re-base; refusing that is the publisher's job.
//   - SIGNATURES. Reduce is given acts that a verifier already accepted.
//
// # How this package stopped being a skeleton, and what did not survive
//
// The first engineering act of this design was reduce_test.go, written before
// any reducer and wrapped in a harness that required each fixture to FAIL. The
// rejection fixtures were promoted honestly as their rules landed.
//
// ⚠️ The ALLOW-direction tests did not survive that discipline. Four of them
// asserted only that a phrase was ABSENT from the refusal reason, which a
// neighbouring rule's refusal satisfies — so the reducer admitted nothing but
// genesis for a week with a green suite. allowpath_test.go now enforces
// mechanically that a test claiming an acceptance reads Outcome.Accepted.
//
// Authority: dcn-arq-00005 §10, §13, §17, §22, §27.
package reduce
