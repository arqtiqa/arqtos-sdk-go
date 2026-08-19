// Package kernel is the root of the deterministic act-kernel semantics: the
// encoding, the reducer, the predicate IR, the key-validity rules and the tape
// layout that decide what an act MEANS.
//
// It holds no code of its own. The semantics live in its subpackages —
// [github.com/arqtiqa/arqtos-sdk-go/kernel/canonical],
// [github.com/arqtiqa/arqtos-sdk-go/kernel/reduce],
// [github.com/arqtiqa/arqtos-sdk-go/kernel/predicate],
// [github.com/arqtiqa/arqtos-sdk-go/kernel/keyhistory] and
// [github.com/arqtiqa/arqtos-sdk-go/kernel/tapeformat] — and this file exists to
// say why they are together, and why they are HERE rather than in the runtime.
//
// # Why this family is public
//
// Two programs must agree, byte for byte, about what an act means: the private
// runtime that accepts acts, and the verifier an outsider runs against an
// exported bundle. If the runtime kept these semantics private, the verifier
// would have to either import something it cannot see, or re-derive them — and
// re-deriving them is the second kernel implementation this design refuses,
// because two implementations disagree exactly where it matters and nothing
// notices until a dispute.
//
// So the boundary is drawn by what must be SHARED, not by what is comfortable
// to publish: everything needed to replay is here, and everything needed only
// to operate — publication, execution, promotion, observation, the host gate,
// the sandbox, credentials, the journal, and writing to the tape — stays
// private to the runtime.
//
// # What is NOT here, deliberately
//
// The tape's WRITE path and its ref management are not in
// [github.com/arqtiqa/arqtos-sdk-go/kernel/tapeformat]: a verifier reads a tape
// and never writes one, and publishing the writer would publish the publication
// protocol's authority surface for no reader's benefit.
//
// # ⚠️ Skeleton
//
// These packages are declared and documented; their semantics are not
// implemented. The build sequence puts the encoding's committed test vectors
// and the reducer's first failing test AHEAD of any implementation, so a
// subpackage that is empty here is empty on purpose. Adding an implementation
// before its failing test exists is the one thing this skeleton is shaped to
// prevent.
//
// Authority: dcn-arq-00005 (the act kernel, 38 corrections) and
// arqtos-5-core-kernel-design.
package kernel
