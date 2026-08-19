// Package verify is the outsider's verifier: given an evidence bundle, it
// replays the act chain and reports what it found — and, just as importantly,
// what its finding is relative to.
//
// # Why this is public, and here
//
// The verifier and the runtime must apply the SAME deterministic semantics, or
// the verifier is a second implementation of the reducer and the two disagree
// on exactly the cases that matter. So the semantics live in
// [github.com/arqtiqa/arqtos-sdk-go/kernel] and BOTH sides import them: the
// private runtime that produces bundles, and this package, which consumes them.
// A private reducer would make this package either an impossible import or that
// refused second implementation.
//
// # ⚠️ This build cannot verify anything
//
// [Verify] returns [ErrNotImplemented] for every input. That is deliberate and
// it is load-bearing rather than a placeholder's shrug: the minimum full replay
// lands later, and until it does, the one behaviour this package MUST NOT have
// is the ability to report success. A verifier that exits 0 without replaying
// is indistinguishable, at the call site, from one that replayed and agreed.
//
// # Proof relativity
//
// [Verify] never returns a bare pass. It returns a [Report], and a Report names
// four things about the claim: the root it is relative to, that root's
// provenance, when the root was observed, and the observation coverage the
// claim rests on.
//
// This is not decoration. `Verify(proof, root)` never proves the root is
// canonical: a compromised administrator can fork a tape before a revocation
// and produce a perfectly self-consistent package. Self-contained is never
// self-authenticating — so an unwitnessed result is visibly downgraded rather
// than silently accepted, and the zero value of every field here asserts
// nothing at all.
package verify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"
)

// ErrNotImplemented is returned by [Verify] for every input in this build.
//
// It exists so a caller can distinguish "this build has no replay" from "this
// bundle is bad" — two conclusions with opposite responses, which a generic
// failure would collapse.
var ErrNotImplemented = errors.New("verify: full replay is not implemented in this build")

// A Provenance says where the root a claim is relative to actually came from.
//
// It is an integer type whose zero value means "nothing was said" rather than a
// string type whose zero value is "". A Report nobody filled in must not read
// as one carrying the strongest provenance available.
type Provenance int

const (
	// ProvenanceUnspecified is the zero value: the claim named no provenance
	// for its root. It is not a provenance — see [Provenance.Stated] — and a
	// report carrying it is not making a provenance claim at all.
	ProvenanceUnspecified Provenance = iota

	// ProvenanceTenantSigned is a root asserted by the tenant's own key.
	//
	// ⚠️ This is the weakest of the three that say anything, and the reason is
	// the one the standing law exists for: a tenant tape gives replay, not
	// split-view detection. A compromised tenant root can fork it.
	ProvenanceTenantSigned

	// ProvenanceHostObserved is a root corroborated by the code host's own
	// view — stronger than tenant-signed because it is a second party, and
	// still not independent, because the host is inside the same trust
	// boundary the tenant administers.
	ProvenanceHostObserved

	// ProvenanceExternallyWitnessed is a root an independent witness attested.
	// It is the only value for which [Provenance.Witnessed] is true, and the
	// only one under which a result is not downgraded.
	ProvenanceExternallyWitnessed
)

var provenanceNames = map[Provenance]string{
	ProvenanceUnspecified:         "unspecified",
	ProvenanceTenantSigned:        "tenant_signed",
	ProvenanceHostObserved:        "host_observed",
	ProvenanceExternallyWitnessed: "externally_witnessed",
}

var provenances = func() []Provenance {
	out := make([]Provenance, 0, len(provenanceNames))
	for p := range provenanceNames {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}()

// Provenances returns the closed provenance vocabulary, as a copy.
func Provenances() []Provenance { return slices.Clone(provenances) }

// Valid reports whether p is in the closed vocabulary.
func (p Provenance) Valid() bool {
	_, ok := provenanceNames[p]
	return ok
}

// Stated reports whether p actually names a provenance, as opposed to being
// absent. [ProvenanceUnspecified] is valid and states nothing.
func (p Provenance) Stated() bool { return p.Valid() && p != ProvenanceUnspecified }

// Witnessed reports whether the root was attested by an INDEPENDENT witness.
//
// ⚠️ Only [ProvenanceExternallyWitnessed] qualifies. Tenant-signed and
// host-observed roots are both inside the boundary the tenant administers, so
// neither detects a split view — a result resting on them is downgraded on the
// report rather than silently accepted.
func (p Provenance) Witnessed() bool { return p == ProvenanceExternallyWitnessed }

// String names p, or marks it invalid. It never returns the empty string: a
// blank would print as an absent field rather than as a broken one.
func (p Provenance) String() string {
	if n, ok := provenanceNames[p]; ok {
		return n
	}
	return fmt.Sprintf("invalid_provenance(%d)", int(p))
}

// A Coverage is the observation coverage a completeness claim rests on.
//
// Coverage is REPORTED, never assumed — a claim that does not name its coverage
// is not a smaller claim, it is an unfalsifiable one.
type Coverage int

const (
	// CoverageUnspecified is the zero value: no coverage was reported. It is
	// not a coverage level — see [Coverage.Reported].
	CoverageUnspecified Coverage = iota

	// CoverageC1 is git-only observation: what the repository itself shows.
	CoverageC1

	// CoverageC2 is the composite host ledger — change-request inventory by
	// stable node identity, ref state, a webhook journal, protection
	// snapshots — reconciled against decided events.
	CoverageC2

	// CoverageC3 is the host's own audit tape, consumed additively when one is
	// bound.
	//
	// ⚠️ C3 is the HOST's tape and is never self-supplied. A process
	// certifying that it missed nothing is circular: the ceiling for a
	// self-supplied claim is C2, on a declared universe.
	CoverageC3
)

var coverageNames = map[Coverage]string{
	CoverageUnspecified: "unspecified",
	CoverageC1:          "C1",
	CoverageC2:          "C2",
	CoverageC3:          "C3",
}

var coverages = func() []Coverage {
	out := make([]Coverage, 0, len(coverageNames))
	for c := range coverageNames {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}()

// Coverages returns the closed coverage vocabulary, as a copy.
func Coverages() []Coverage { return slices.Clone(coverages) }

// Valid reports whether c is in the closed vocabulary.
func (c Coverage) Valid() bool {
	_, ok := coverageNames[c]
	return ok
}

// Reported reports whether c actually names a coverage level, as opposed to
// none having been reported.
func (c Coverage) Reported() bool { return c.Valid() && c != CoverageUnspecified }

// String names c, or marks it invalid. It never returns the empty string.
func (c Coverage) String() string {
	if n, ok := coverageNames[c]; ok {
		return n
	}
	return fmt.Sprintf("invalid_coverage(%d)", int(c))
}

// A Report is the result of a verification, carrying the four things every
// verification claim must name.
//
// ⚠️ Its zero value asserts NOTHING, by construction: an empty Report names no
// root, states no provenance and reports no coverage. That is what stops an
// unpopulated result from reading as a witnessed, fully-covered pass.
type Report struct {
	// Root is the root this claim is relative to.
	Root string

	// RootProvenance is where that root came from. A claim whose provenance is
	// not [Provenance.Witnessed] is downgraded on the report.
	RootProvenance Provenance

	// ObservedAt is when the root was observed — the claim's freshness. A
	// verification is a statement about a moment, and omitting the moment
	// makes it a statement about no moment.
	ObservedAt time.Time

	// Coverage is the observation coverage the claim rests on.
	Coverage Coverage
}

// Verify replays the evidence bundle read from bundle and reports what it found.
//
// ⚠️ In this build it does none of that: it returns the zero [Report] and
// [ErrNotImplemented] for every input, including a nil bundle. See the package
// documentation for why the inability to succeed is the point rather than a gap.
//
// The signature is provisional in one respect and settled in another. That it
// takes bytes and returns a [Report] plus an error is settled — proof relativity
// requires the Report. Whether a bundle arrives as an [io.Reader] or as a path
// to an on-disk git bundle is decided when the replay is built, and callers
// should expect that half to move.
func Verify(ctx context.Context, bundle io.Reader) (Report, error) {
	return Report{}, ErrNotImplemented
}
