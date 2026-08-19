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
// # ⚠️ Skeleton
//
// Empty on purpose, and this package is the sharpest case of why. The first
// engineering act of this design is `reduce_test.go` — permit double-spend
// rejected, tree-swap rejected, repository genesis accepted, over a fixture DAG
// — and those tests are written to FAIL, and to stay failing, until the reducer
// that satisfies them is built as its own deliberate piece of work.
//
// An implementation landing here before that test exists would be judged by
// tests written to match it, which is the failure the ordering exists to
// prevent.
//
// Authority: dcn-arq-00005 §10, §13, §17, §22, §27.
package reduce
