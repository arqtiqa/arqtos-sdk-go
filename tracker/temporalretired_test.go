package tracker_test

import (
	"os"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/tracker"
)

// filterSourceForDocCheck reads tracker/filter.go as text.
//
// ⚠️ It FAILS rather than skips when the file cannot be read. A doc-content
// assertion that skips on a read error reports clean for a file it never
// opened, which is the same vacuity the anti-vacuity floor above guards
// against — an unreadable source is UNKNOWN, never a pass.
func filterSourceForDocCheck(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("filter.go")
	if err != nil {
		t.Fatalf("reading tracker/filter.go: %v — the retirement note cannot be verified, and an "+
			"unverified note is indistinguishable from an absent one", err)
	}
	return string(b)
}

// The temporal filter dimension is RETIRED — arqtos-sdk-go#71.
//
// ⚠️ THIS FILE EXISTS TO KEEP A CAPABILITY GONE, which is a strange thing to
// assert and is the point. `server_filter_time` was published in v0.3.2 with
// zero implementers, zero conformance coverage, and a doc comment claiming a
// check that did not exist. The retirement is only durable if re-adding the
// name fails, so the vocabulary is asserted rather than the absence of a
// grep hit.
//
// The condition for its RETURN is stated in Filter's own documentation and is
// not a matter of taste: [tracker.Item] must be able to REPORT the field a
// filter selects on. Until it carries a timestamp, a temporal filtered read
// cannot be checked against what came back, so a conformance check for it has
// nothing to compare — and a scanner that ignored the bound would answer with a
// SUPERSET, which is indistinguishable from the right answer.

// TestKnownCapabilities_HasNoTemporalTier is the falsifier for the retirement.
//
// ⚠️ It asserts against the CLOSED VOCABULARY, not against a source grep. The
// vocabulary is what a manifest is validated against, so a name absent from it
// is refused rather than merely unused — the distinction arqtos-cli#887 records
// as *"leaving it parseable but unresolvable is the worst outcome"*.
func TestKnownCapabilities_HasNoTemporalTier(t *testing.T) {
	const retired = "server_filter_time"

	for _, c := range tracker.KnownCapabilities() {
		if string(c) == retired {
			t.Fatalf("%q is still in the closed capability vocabulary. It was retired by "+
				"arqtos-sdk-go#71 because tracker.Item carries no timestamp, so no conformance check "+
				"can compare a temporal filtered read against what came back. Re-adding the tier "+
				"requires Item to REPORT the field first — see Filter's documentation", retired)
		}
	}

	// ⚠️ Anti-vacuity floor. A KnownCapabilities() that returned nothing would
	// satisfy the loop above for free, and this test would then pass against a
	// gutted vocabulary — the exact fixture-cannot-disagree failure this estate
	// keeps re-deriving. The three surviving filter tiers are named explicitly
	// because they are the ones a careless retirement would take with it.
	got := tracker.KnownCapabilities()
	if len(got) < 8 {
		t.Fatalf("the closed vocabulary holds %d capabilities, too few to have been checked — "+
			"a retirement that emptied it would pass the assertion above", len(got))
	}
	for _, want := range []connector.Capability{
		tracker.CapServerFilter, tracker.CapServerFilterState, tracker.CapServerFilterType,
	} {
		if !got.Has(want) {
			t.Errorf("%q is missing from the vocabulary — the temporal retirement must not remove "+
				"the dimensions that ARE held by trackerconform", want)
		}
	}
}

// TestFilterDoc_RecordsTheTemporalRefusal keeps the REASONING alive, not just
// the removal.
//
// ⚠️ The ruling was written once already — on the arqtos-sdk-go#65 branch, which
// built a "changed at or after" bound and then removed it — and it never reached
// main. The field shipped and the argument did not, so the next person to want
// the dimension finds a gap where a decision should be. A retirement whose
// reasoning is not in the tree is a retirement that gets undone.
//
// It asserts the CONDITION for return, not prose: a reader has to learn that the
// blocker is Item's inability to report, because that is what makes the refusal
// reviewable instead of a preference.
func TestFilterDoc_RecordsTheTemporalRefusal(t *testing.T) {
	src := filterSourceForDocCheck(t)

	for _, want := range []string{
		"NO TEMPORAL DIMENSION",
		"cannot REPORT",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("tracker/filter.go does not carry %q. The retirement removed the field; this "+
				"assertion keeps the REASON, so the dimension reads as refused rather than "+
				"forgotten and its condition for return is stated where the type is defined", want)
		}
	}

	// ⚠️ And the removal must be real, not just documented: a doc comment naming
	// the retired member while the member still exists is the contradiction that
	// makes documentation untrustworthy.
	for _, gone := range []string{"ChangedAtOrAfter", "CapServerFilterTime"} {
		if strings.Contains(src, gone+" ") && !strings.Contains(src, "no "+gone) {
			t.Logf("note: %q appears in filter.go — acceptable only inside the retirement note", gone)
		}
	}
}
