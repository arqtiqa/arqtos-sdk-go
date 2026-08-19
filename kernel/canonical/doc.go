// Package canonical is the encoding boundary: the one way an act's bytes are
// produced, so that two implementations hash the same act to the same identity.
//
// # The format is owned forever
//
// An act's identity is a hash of its canonical bytes, and acts are replayed
// indefinitely. So this encoding is not an implementation detail that can be
// improved later — a change to it changes every historical act's identity.
// Decoders are retained indefinitely and reducers are versioned, which is what
// makes an old act verifiable under the semantics it was accepted under.
//
// # ⚠️ Unknown fields are REJECTED, never ignored
//
// The permissive default — decode what you recognise, skip the rest — is the
// wrong one here. A field a verifier skips is a field the runtime may have
// acted on, and the two then agree on a hash while disagreeing on meaning.
// Rejection makes that divergence a loud failure at the boundary instead of a
// silent one at the conclusion.
//
// # What this package will hold
//
// The encoding standard, the domain-separation tags that stop one kind of
// object being reinterpreted as another, the hash algorithm, and the
// unknown-field rejection above — together with committed test vectors that
// must stay byte-stable across runs and across implementations.
//
// # ⚠️ Skeleton
//
// Empty on purpose. The encoding standard, tags, hash and vectors are decided
// as one act, with the vectors committed as part of that decision — writing an
// encoder first would settle those questions implicitly, by whatever the first
// implementation happened to do.
//
// Authority: dcn-arq-00005 §9, §21, §24.
package canonical
