package verify_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/verify"
)

// ⚠️ THIS IS THE TEST THE WHOLE STUB EXISTS FOR.
//
// The week-8 standing claim is "a production-shaped PR blocked by the required
// check, and `verify` exit 0 on that checkout". A stub that can return success
// satisfies that claim while replaying nothing — so the demo would pass on a
// build that verifies nothing, and the failure is invisible precisely because
// the observable outcome is identical to the real one.
//
// The genesis plan states it twice, in the week map and again in the scope
// manifest: the standing claim runs the REAL verifier, "never a stub's exit 0".
// This test is the mechanical form of that sentence. It must keep failing for
// as long as the replay is unimplemented, and the change that implements replay
// is the change that rewrites it.
func TestVerify_CannotReportSuccessInThisBuild(t *testing.T) {
	tests := []struct {
		name   string
		bundle func() *bytes.Reader
	}{
		{"empty bundle", func() *bytes.Reader { return bytes.NewReader(nil) }},
		{"arbitrary bytes", func() *bytes.Reader { return bytes.NewReader([]byte("not a bundle")) }},
		{"bundle-looking prefix", func() *bytes.Reader {
			return bytes.NewReader([]byte("# v2 git bundle\n"))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, err := verify.Verify(context.Background(), tt.bundle())

			if err == nil {
				t.Fatal("Verify returned a nil error: this build has no replay, so a nil error here " +
					"is the stub's exit 0 that the standing claim forbids")
			}
			if !errors.Is(err, verify.ErrNotImplemented) {
				t.Errorf("error is %v; want it to wrap ErrNotImplemented so a caller can tell "+
					"'not built yet' from 'this bundle is bad'", err)
			}
			if rep != (verify.Report{}) {
				t.Errorf("Report is %+v; want the zero Report — a stub must not populate a claim it did not compute", rep)
			}
		})
	}
}

// A nil bundle is the same answer, and for the same reason: there is no input,
// well-formed or not, that this build can verify.
func TestVerify_NilBundleIsRefusedTheSameWay(t *testing.T) {
	if _, err := verify.Verify(context.Background(), nil); !errors.Is(err, verify.ErrNotImplemented) {
		t.Fatalf("Verify(nil) error = %v; want ErrNotImplemented", err)
	}
}

// The error message has to say WHICH build, because the one thing a reader must
// not conclude from it is that their bundle is malformed.
func TestErrNotImplemented_SaysItIsTheBuildAndNotTheBundle(t *testing.T) {
	msg := verify.ErrNotImplemented.Error()
	for _, want := range []string{"replay", "build"} {
		if !strings.Contains(msg, want) {
			t.Errorf("ErrNotImplemented = %q; want it to contain %q so the reader blames the build, not their bundle", msg, want)
		}
	}
}

// Proof relativity (dcn-arq-00005 §26): a verification claim names its root, the
// root's provenance, its freshness and its C1/C2/C3 coverage. The zero value of
// each enum must be "nothing was said" rather than a real answer, so a Report
// nobody filled in cannot read as a witnessed, fully-covered one.
func TestZeroReport_AssertsNothing(t *testing.T) {
	var r verify.Report

	if r.RootProvenance.Stated() {
		t.Error("zero Report's RootProvenance reads as stated; an unfilled claim must assert nothing")
	}
	if r.RootProvenance.Witnessed() {
		t.Error("zero Report's RootProvenance reads as externally witnessed — the strongest claim, from an empty struct")
	}
	if r.Coverage.Reported() {
		t.Error("zero Report's Coverage reads as reported; coverage is reported, never assumed")
	}
	if r.Root != "" {
		t.Errorf("zero Report names root %q", r.Root)
	}
}

func TestProvenance_OnlyExternalWitnessCountsAsWitnessed(t *testing.T) {
	tests := map[verify.Provenance]struct{ valid, stated, witnessed bool }{
		verify.ProvenanceUnspecified:         {true, false, false},
		verify.ProvenanceTenantSigned:        {true, true, false},
		verify.ProvenanceHostObserved:        {true, true, false},
		verify.ProvenanceExternallyWitnessed: {true, true, true},
		verify.Provenance(99):                {false, false, false},
	}

	for p, want := range tests {
		if got := p.Valid(); got != want.valid {
			t.Errorf("Provenance(%d).Valid() = %v, want %v", p, got, want.valid)
		}
		if got := p.Stated(); got != want.stated {
			t.Errorf("Provenance(%d).Stated() = %v, want %v", p, got, want.stated)
		}
		if got := p.Witnessed(); got != want.witnessed {
			t.Errorf("Provenance(%d).Witnessed() = %v, want %v", p, got, want.witnessed)
		}
	}
}

func TestCoverage_UnspecifiedIsNotAReportedCoverage(t *testing.T) {
	tests := map[verify.Coverage]struct{ valid, reported bool }{
		verify.CoverageUnspecified: {true, false},
		verify.CoverageC1:          {true, true},
		verify.CoverageC2:          {true, true},
		verify.CoverageC3:          {true, true},
		verify.Coverage(42):        {false, false},
	}

	for c, want := range tests {
		if got := c.Valid(); got != want.valid {
			t.Errorf("Coverage(%d).Valid() = %v, want %v", c, got, want.valid)
		}
		if got := c.Reported(); got != want.reported {
			t.Errorf("Coverage(%d).Reported() = %v, want %v", c, got, want.reported)
		}
	}
}

// An invalid enum must not render as something a report could print as a claim.
func TestString_NamesEveryValidValueAndMarksTheRest(t *testing.T) {
	if got := verify.ProvenanceExternallyWitnessed.String(); got != "externally_witnessed" {
		t.Errorf("String() = %q, want externally_witnessed", got)
	}
	if got := verify.CoverageC2.String(); got != "C2" {
		t.Errorf("String() = %q, want C2", got)
	}
	for _, got := range []string{verify.Provenance(99).String(), verify.Coverage(42).String()} {
		if !strings.HasPrefix(got, "invalid") {
			t.Errorf("String() of an out-of-range value = %q; want an explicit invalid marker, "+
				"never a blank that prints as an absent field", got)
		}
	}
}

func TestProvenances_AndCoverages_ReturnCopies(t *testing.T) {
	a := verify.Provenances()
	a[0] = verify.Provenance(99)
	if verify.Provenances()[0] == verify.Provenance(99) {
		t.Error("Provenances() hands out the backing array; a caller can corrupt the vocabulary")
	}

	c := verify.Coverages()
	c[0] = verify.Coverage(42)
	if verify.Coverages()[0] == verify.Coverage(42) {
		t.Error("Coverages() hands out the backing array; a caller can corrupt the vocabulary")
	}
}
