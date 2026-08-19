// Package tapeformat is the act chain's on-disk layout, and the READING of it.
//
// # Reading only, and the seam is deliberate
//
// A verifier reads a tape; it never writes one. So this package carries the
// layout and the reader, and the WRITE path — appending acts, moving the
// canonical head, managing refs — stays in the private runtime.
//
// The split is not tidiness. Publication authority is the thing the whole design
// concentrates in one place: governed refs move only through the publication
// protocol, under compare-and-swap against an expected old object id, and no
// human or agent credential may move one. Publishing a writer here would put
// that surface in every consumer's hands to no reader's benefit, since reading
// is all a verifier needs.
//
// # Git is the tape
//
// The chain is ordinary git objects under a governed ref namespace, which is
// what makes the evidence portable: a bundle is a git bundle, an outsider needs
// no server, and the customer owns the artefact because they already own the
// repository.
//
// # ⚠️ Skeleton
//
// Empty on purpose. The layout is fixed together with the canonical encoding,
// since an act's position in the chain and its identity are decided by the same
// act — see [github.com/arqtiqa/arqtos-sdk-go/kernel/canonical].
//
// Authority: dcn-arq-00005 §2, §11, §25.
package tapeformat
