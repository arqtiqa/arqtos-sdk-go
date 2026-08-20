package verify_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/internal/redfixture"
	"github.com/arqtiqa/arqtos-sdk-go/verify"
)

const (
	genesisA = "sha256:aaaa56789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0"
	genesisB = "sha256:bbbb56789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0"
)

func anchorAt(p verify.Provenance) verify.Anchor {
	return verify.Anchor{
		GenesisID:  genesisA,
		Provenance: p,
		ObservedAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
	}
}

// The only path to an accepted anchor: the shown genesis matches one an
// INDEPENDENT witness attested.
func TestAnchor_AcceptsOnlyAWitnessedMatch(t *testing.T) {
	d, err := anchorAt(verify.ProvenanceExternallyWitnessed).Check(genesisA)
	if err != nil {
		t.Fatalf("a witnessed match returned an error: %v", err)
	}
	if !d.Accepted() {
		t.Fatalf("decision %s is not accepted", d)
	}
	if d.Downgraded() {
		t.Error("an accepted decision also reports itself downgraded")
	}
}

// ⚠️ THE ALTERNATE-GENESIS CASE, at the level this package can decide it today.
// A verifier holding an anchor and shown a DIFFERENT genesis refuses. It does
// not downgrade, and it does not weigh the chain's internal consistency: the
// chain is not the question.
func TestAnchor_RefusesAGenesisThatIsNotTheAnchoredOne(t *testing.T) {
	for _, p := range verify.Provenances() {
		if !p.Stated() {
			continue
		}
		t.Run(p.String(), func(t *testing.T) {
			d, err := anchorAt(p).Check(genesisB)
			if !errors.Is(err, verify.ErrAnchorMismatch) {
				t.Fatalf("error %v does not wrap ErrAnchorMismatch", err)
			}
			if d != verify.AnchorRefused {
				t.Errorf("decision is %s, want refused", d)
			}
			if d.Accepted() {
				t.Error("a mismatched genesis was accepted")
			}
		})
	}
}

// ⚠️ THE NO-ANCHOR CASE. A verifier with no trusted digest does not fail and
// does not pass: it proceeds with a VISIBLY downgraded result. The error is
// returned alongside the decision precisely so a caller that only checks err
// cannot mistake this for an accept.
func TestAnchor_WithNoAnchorDowngradesRatherThanAcceptingOrFailing(t *testing.T) {
	d, err := verify.Anchor{}.Check(genesisA)
	if !errors.Is(err, verify.ErrNoAnchor) {
		t.Fatalf("error %v does not wrap ErrNoAnchor", err)
	}
	if d != verify.AnchorDowngraded {
		t.Fatalf("decision is %s, want downgraded", d)
	}
	if d.Accepted() {
		t.Error("a result with no anchor at all reported itself accepted")
	}
	if !d.Downgraded() {
		t.Error("the no-anchor decision does not report itself downgraded")
	}
}

// A matching anchor that nobody independent witnessed is still downgraded. The
// tenant and the host are both inside the boundary the tenant administers, so
// neither detects a split view.
func TestAnchor_AnUnwitnessedMatchIsDowngraded(t *testing.T) {
	for _, p := range []verify.Provenance{verify.ProvenanceTenantSigned, verify.ProvenanceHostObserved} {
		t.Run(p.String(), func(t *testing.T) {
			d, err := anchorAt(p).Check(genesisA)
			if !errors.Is(err, verify.ErrUnwitnessedAnchor) {
				t.Fatalf("error %v does not wrap ErrUnwitnessedAnchor", err)
			}
			if d != verify.AnchorDowngraded {
				t.Errorf("decision is %s, want downgraded", d)
			}
		})
	}
}

// An anchor whose own provenance was never stated is not an anchor. Accepting
// it would let "somebody gave me this digest" stand in for "I know where this
// digest came from".
func TestAnchor_RefusesAnAnchorWithUnstatedProvenance(t *testing.T) {
	a := anchorAt(verify.ProvenanceUnspecified)
	d, err := a.Check(genesisA)
	if err == nil {
		t.Fatal("an anchor with no stated provenance was used")
	}
	if d.Accepted() {
		t.Error("an anchor with no stated provenance produced an accept")
	}
}

// Nothing shown is not the same as nothing anchored, and collapsing the two
// would let an empty bundle inherit the no-anchor downgrade instead of failing.
func TestAnchor_RefusesWhenNoGenesisWasShown(t *testing.T) {
	// ⚠️ Both anchor states, because the failure this guards against is an
	// empty bundle INHERITING one of the other two outcomes. Asserting only
	// "some error" let a mutation that deleted this rule pass on the mismatch
	// refusal next to it.
	for name, a := range map[string]verify.Anchor{
		"with an anchor": anchorAt(verify.ProvenanceExternallyWitnessed),
		"with none":      {},
	} {
		t.Run(name, func(t *testing.T) {
			d, err := a.Check("")
			if !errors.Is(err, verify.ErrNoGenesisShown) {
				t.Fatalf("error %v does not wrap ErrNoGenesisShown — "+
					"'there is nothing to compare' has collapsed into some other verdict", err)
			}
			if d.Accepted() || d.Downgraded() {
				t.Errorf("decision %s treats a missing genesis as a weaker claim rather than no claim", d)
			}
		})
	}
}

func TestAnchorDecision_StringNamesEveryValueAndMarksTheRest(t *testing.T) {
	for d, want := range map[verify.AnchorDecision]string{
		verify.AnchorDecisionUnspecified: "unspecified",
		verify.AnchorRefused:             "refused",
		verify.AnchorDowngraded:          "downgraded",
		verify.AnchorAccepted:            "accepted",
	} {
		if got := d.String(); got != want {
			t.Errorf("AnchorDecision(%d).String() = %q, want %q", int(d), got, want)
		}
	}
	if got := verify.AnchorDecision(99).String(); !strings.HasPrefix(got, "invalid") {
		t.Errorf("AnchorDecision(99).String() = %q; want an explicit invalid marker", got)
	}
}

// ⚠️ The zero decision asserts nothing — it is neither accepted nor downgraded,
// so an unpopulated result cannot read as either verdict.
func TestAnchorDecision_ZeroValueAssertsNothing(t *testing.T) {
	var d verify.AnchorDecision
	if d.Accepted() {
		t.Error("the zero decision reports accepted")
	}
	if d.Downgraded() {
		t.Error("the zero decision reports downgraded")
	}
}

func TestAnchor_Held(t *testing.T) {
	if (verify.Anchor{}).Held() {
		t.Error("an empty anchor reports itself held")
	}
	if !anchorAt(verify.ProvenanceTenantSigned).Held() {
		t.Error("a populated anchor reports itself not held")
	}
}

// ---------------------------------------------------------------------------
// The written rule
// ---------------------------------------------------------------------------

// ⚠️ The trust-anchor rule has an audience that never compiles this package: an
// outsider deciding whether to believe a bundle. Prose drifts silently and has
// no derivation, so the four cases are checked against the document.
func TestTrustAnchorDocumentCoversEveryCase(t *testing.T) {
	const path = "../docs/TRUST-ANCHOR.md"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the trust-anchor rule must be written down for a reader who never compiles this package: %v", err)
	}

	// ⚠️ The DECISION TABLE is what is checked, not the document as a whole. A
	// sweep for phrases anywhere in the file passes on words that appear in the
	// surrounding prose, so deleting a row of the table left it green.
	var rows []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
			continue
		}
		if strings.Contains(line, "case") && strings.Contains(line, "outcome") {
			continue // the header
		}
		if strings.Contains(line, "provenance") && strings.Contains(line, "independent") {
			continue // the provenance table's header
		}
		rows = append(rows, strings.ToLower(line))
	}

	cases := []struct{ when, then string }{
		{"no anchor", "downgraded"},
		{"does not match", "refused"},
		{"not independently witnessed", "downgraded"},
		{"externally witnessed", "accepted"},
	}
	for _, c := range cases {
		found := false
		for _, row := range rows {
			if strings.Contains(row, c.when) && strings.Contains(row, c.then) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no row of the decision table says %q is %q", c.when, c.then)
		}
	}
	if len(cases) != 4 {
		t.Fatalf("the rule has four cases and no others; checked %d", len(cases))
	}

	// ⚠️ Count-asserted both ways: a table that grew a fifth outcome is as much
	// a drift from the rule as one that lost a row, and a parse that matched
	// nothing would otherwise report full coverage.
	if len(rows) != len(cases)+3 {
		t.Errorf("parsed %d table rows, want %d (four decision rows plus the three provenance rows); "+
			"the document and the rule have diverged", len(rows), len(cases)+3)
	}

	doc := strings.Join(strings.Fields(strings.ToLower(string(raw))), " ")
	for _, phrase := range []string{
		"where the trusted digest comes from",
		"self-contained is never self-authenticating",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("the document does not carry the phrase %q", phrase)
		}
	}
}

// ---------------------------------------------------------------------------
// THE KILL GATE — the alternate genesis, as a CHAIN
// ---------------------------------------------------------------------------

// ⚠️ A REAL BODY, required to fail. It used to be a t.Skip with nothing after
// it — a test that could not fail, and therefore guarded nothing.
//
// The claim: a compromised administrator mints an alternate genesis and builds a
// tape on it that replays PERFECTLY — every signature valid, every act
// consistent. Such a bundle must be refused ON THE ANCHOR, and its internal
// consistency must never enter the decision.
//
// ⚠️ Anchor.Check already decides the digest comparison, and the tests above
// cover it green. That is NOT this claim. The dangerous case is the one where
// replay SUCCEEDS, because that is when a verifier is most tempted to report a
// pass — so this needs a real bundle and a real replay.
func TestAlternateGenesisChainIsRefusedOnTheAnchor(t *testing.T) {
	redfixture.Expect(t, "full replay in verify.Verify", func(ft redfixture.T) {
		// A bundle built on a genesis the verifier does not anchor.
		bundle := strings.NewReader(`{"genesis":"` + genesisB + `","acts":[]}`)

		report, err := verify.Verify(context.Background(), bundle)
		if errors.Is(err, verify.ErrNotImplemented) {
			ft.Fatalf("Verify cannot replay, so the anchor is never consulted: %v", err)
		}
		if err == nil {
			ft.Fatal("a bundle built on an unanchored genesis was accepted")
		}
		// ⚠️ THE REASON. Refused-for-any-reason would be satisfied by a verifier
		// that refuses everything, and by one that refused on the chain's
		// contents — which is exactly the reasoning an alternate genesis is
		// built to invite.
		if !errors.Is(err, verify.ErrAnchorMismatch) {
			ft.Errorf("the bundle was refused with %v, not on the anchor", err)
		}
		if report.RootProvenance.Witnessed() {
			ft.Errorf("the report claims a witnessed root for a bundle that failed its anchor check")
		}
	})
}
