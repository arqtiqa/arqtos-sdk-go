package verify

import (
	"errors"
	"fmt"
	"time"
)

// The trust-anchor rule.
//
// # ⚠️ Replay proves the tape is consistent, never that it is the right tape
//
// A verifier handed a bundle can replay it and find every act well-formed, every
// signature valid and every dependency satisfied — and still be looking at a
// chain a compromised administrator built on an alternate genesis. Internal
// consistency is not canonicity. So before a verifier reports anything, it must
// decide whether the genesis it was SHOWN is the genesis it should TRUST, and
// that decision cannot come from inside the bundle.
//
// # Where the trusted digest comes from
//
// An [Anchor] is a genesis id the verifier obtained OUT OF BAND, together with
// the provenance of however it obtained it. The provenances are the same three
// the rest of this package uses, and they are ordered by how much they survive:
// a tenant-published digest is inside the boundary the tenant administers, a
// host-observed one is a second party inside that same boundary, and only an
// externally-witnessed one is independent.
//
// # The rule, in four cases
//
//   - No anchor at all → the result is DOWNGRADED, never accepted and never
//     failed. A verifier with nothing to compare against can still say what the
//     tape contains; it cannot say the tape is the right one.
//   - An anchor, and the shown genesis DOES NOT MATCH it → REFUSED. The chain's
//     internal consistency does not enter this decision, because a forked chain
//     is consistent by construction.
//   - A match, on an anchor that is NOT INDEPENDENTLY WITNESSED → DOWNGRADED.
//   - A match, on an EXTERNALLY WITNESSED anchor → ACCEPTED. This is the only
//     path to an accept.
//
// Self-contained is never self-authenticating. The full rule, written for a
// reader who never compiles this package, is in ../docs/TRUST-ANCHOR.md.

var (
	// ErrNoAnchor is returned when the verifier holds no trusted genesis digest.
	//
	// ⚠️ It accompanies a DOWNGRADED decision rather than a failure: proceeding
	// is legitimate, and reporting a bare pass is not. It is returned as an
	// error so a caller that checks only err cannot read the downgrade as an
	// accept — the mistake this whole rule exists to prevent.
	ErrNoAnchor = errors.New("verify: no trust anchor — the result is relative to a genesis nobody vouched for")

	// ErrAnchorMismatch is a shown genesis that is not the anchored one.
	//
	// ⚠️ This is the alternate-genesis case, and it is a REFUSAL. A chain built
	// on a forged genesis is self-consistent by construction, so consistency
	// can never be the answer to it.
	ErrAnchorMismatch = errors.New("verify: the genesis shown is not the genesis anchored")

	// ErrUnwitnessedAnchor is a matching anchor that no independent party
	// attested. The result stands, downgraded.
	ErrUnwitnessedAnchor = errors.New("verify: the trust anchor is not independently witnessed")

	// ErrNoGenesisShown is a bundle that presents no genesis at all.
	//
	// ⚠️ Distinct from ErrNoAnchor. "I have nothing to compare against" and
	// "there is nothing to compare" are different failures, and collapsing them
	// would let an empty bundle inherit the no-anchor downgrade instead of
	// being rejected outright.
	ErrNoGenesisShown = errors.New("verify: the bundle presents no genesis")
)

// An Anchor is a genesis digest a verifier is willing to trust, and how it came
// by it.
//
// ⚠️ The zero value is "no anchor held", which is a real and common state — an
// outsider handed a bundle by a stranger has one. It downgrades; it does not
// fail.
type Anchor struct {
	// GenesisID is the trusted genesis act id. Empty means no anchor is held.
	GenesisID string

	// Provenance is how the verifier obtained GenesisID — NOT how the tenant
	// obtained it. An anchor is only as independent as the channel it arrived
	// through.
	Provenance Provenance

	// ObservedAt is when the anchor was obtained: the claim's freshness. An
	// anchor from before a known compromise is a different fact from one taken
	// after it.
	ObservedAt time.Time
}

// Held reports whether a names a genesis at all.
func (a Anchor) Held() bool { return a.GenesisID != "" }

// An AnchorDecision is the outcome of applying the trust-anchor rule.
//
// ⚠️ Its zero value asserts NOTHING — neither [AnchorDecision.Accepted] nor
// [AnchorDecision.Downgraded] is true for it — so an unpopulated result cannot
// read as a verdict.
type AnchorDecision int

const (
	// AnchorDecisionUnspecified is the zero value: no decision was reached.
	AnchorDecisionUnspecified AnchorDecision = iota
	// AnchorRefused is a shown genesis that contradicts the anchor.
	AnchorRefused
	// AnchorDowngraded is a result that stands but is visibly weaker: either
	// no anchor was held, or the anchor is not independently witnessed.
	AnchorDowngraded
	// AnchorAccepted is a match on an externally-witnessed anchor — the only
	// path to an accept.
	AnchorAccepted
)

var anchorDecisionNames = map[AnchorDecision]string{
	AnchorDecisionUnspecified: "unspecified",
	AnchorRefused:             "refused",
	AnchorDowngraded:          "downgraded",
	AnchorAccepted:            "accepted",
}

// Accepted reports whether the anchor rule was satisfied outright.
func (d AnchorDecision) Accepted() bool { return d == AnchorAccepted }

// Downgraded reports whether the result stands but is visibly weaker.
func (d AnchorDecision) Downgraded() bool { return d == AnchorDowngraded }

// String names d, or marks it invalid. It never returns the empty string.
func (d AnchorDecision) String() string {
	if n, ok := anchorDecisionNames[d]; ok {
		return n
	}
	return fmt.Sprintf("invalid_anchor_decision(%d)", int(d))
}

// Check applies the trust-anchor rule to the genesis a bundle presents.
//
// ⚠️ It returns a non-nil error for EVERY outcome except an accept, including
// the two downgrades. That is deliberate: a downgrade is a result a caller may
// legitimately proceed on, and it must be impossible to proceed on one without
// having seen why. The decision carries the verdict; the error carries the
// reason; neither alone is the answer.
func (a Anchor) Check(shownGenesisID string) (AnchorDecision, error) {
	if shownGenesisID == "" {
		return AnchorDecisionUnspecified, ErrNoGenesisShown
	}
	if !a.Held() {
		return AnchorDowngraded, ErrNoAnchor
	}
	if !a.Provenance.Stated() {
		// An anchor whose own provenance is unknown is not an anchor: it would
		// let "somebody gave me this digest" stand in for knowing where it came
		// from.
		return AnchorRefused, fmt.Errorf("%w: the anchor states no provenance for %s", ErrAnchorMismatch, a.GenesisID)
	}
	if shownGenesisID != a.GenesisID {
		return AnchorRefused, fmt.Errorf("%w: shown %s, anchored %s (via %s)",
			ErrAnchorMismatch, shownGenesisID, a.GenesisID, a.Provenance)
	}
	if !a.Provenance.Witnessed() {
		return AnchorDowngraded, fmt.Errorf("%w: the anchor is %s", ErrUnwitnessedAnchor, a.Provenance)
	}
	return AnchorAccepted, nil
}
