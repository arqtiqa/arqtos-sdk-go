package contracts_test

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
)

func goodAuthority() contracts.TimeAuthority {
	return contracts.TimeAuthority{Name: "authority:host-clock", Provenance: contracts.ClockSynchronised}
}

func goodTime() contracts.AcceptedTime {
	return contracts.AcceptedTime{
		At:        time.Date(2026, 8, 20, 9, 31, 0, 0, time.UTC),
		Authority: goodAuthority(),
	}
}

// ⚠️ An unnamed authority is refused, not defaulted. A timestamp nothing can
// attribute cannot be replayed and cannot be reconciled — it is not a weaker
// claim about time, it is no claim.
func TestTimeAuthority_RefusesAnUnnamedAuthority(t *testing.T) {
	for name, a := range map[string]contracts.TimeAuthority{
		"empty":      {Name: "", Provenance: contracts.ClockSynchronised},
		"whitespace": {Name: "   ", Provenance: contracts.ClockSynchronised},
	} {
		t.Run(name, func(t *testing.T) {
			err := a.Validate()
			if err == nil {
				t.Fatal("Validate accepted an unnamed authority")
			}
			if !errors.Is(err, contracts.ErrInvalidTime) {
				t.Errorf("error %v does not wrap ErrInvalidTime", err)
			}
		})
	}
}

// ⚠️ Unspecified provenance is refused rather than treated as the weakest real
// grade. "Nobody recorded it" and "we know it was a bare local clock" are
// different facts, and a default would make them indistinguishable — in the
// direction that overstates.
func TestTimeAuthority_RefusesUnstatedProvenance(t *testing.T) {
	a := goodAuthority()
	a.Provenance = contracts.ClockProvenanceUnspecified

	err := a.Validate()
	if err == nil {
		t.Fatal("Validate accepted an authority whose clock provenance was never stated")
	}
	if !strings.Contains(err.Error(), "provenance") {
		t.Errorf("error %q does not name the provenance", err)
	}
}

// The weakest real grade must be EXPRESSIBLE, or a deployment without better is
// forced to overstate. Without this, the test above would be satisfied by a rule
// that simply refused everything weak.
func TestTimeAuthority_AcceptsTheWeakestStatedProvenance(t *testing.T) {
	a := contracts.TimeAuthority{Name: "authority:host-clock", Provenance: contracts.ClockLocal}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate rejected an honestly-declared local clock: %v — a deployment without better "+
			"must be able to say so rather than overstate", err)
	}
}

func TestAcceptedTime_RefusesAZeroTime(t *testing.T) {
	tm := goodTime()
	tm.At = time.Time{}
	if err := tm.Validate(); !errors.Is(err, contracts.ErrInvalidTime) {
		t.Fatalf("Validate accepted a zero time: %v", err)
	}
}

func TestAcceptedTime_AcceptsAWellFormedTime(t *testing.T) {
	if err := goodTime().Validate(); err != nil {
		t.Fatalf("Validate rejected a well-formed recorded time: %v", err)
	}
}

func TestClockProvenance_StringNamesEveryValueAndMarksTheRest(t *testing.T) {
	for p, want := range map[contracts.ClockProvenance]string{
		contracts.ClockProvenanceUnspecified: "unspecified",
		contracts.ClockLocal:                 "local",
		contracts.ClockSynchronised:          "synchronised",
		contracts.ClockAttested:              "attested",
	} {
		if got := p.String(); got != want {
			t.Errorf("ClockProvenance(%d).String() = %q, want %q", int(p), got, want)
		}
	}
	for _, p := range []contracts.ClockProvenance{99, -1} {
		got := p.String()
		if got == "" {
			t.Errorf("ClockProvenance(%d).String() is empty — it would print as an absent field", int(p))
		}
		if !strings.HasPrefix(got, "invalid") {
			t.Errorf("ClockProvenance(%d).String() = %q; want an explicit invalid marker", int(p), got)
		}
	}
}

func TestClockProvenance_ValidAndStated(t *testing.T) {
	for p, want := range map[contracts.ClockProvenance]struct{ valid, stated bool }{
		contracts.ClockProvenanceUnspecified: {true, false},
		contracts.ClockLocal:                 {true, true},
		contracts.ClockSynchronised:          {true, true},
		contracts.ClockAttested:              {true, true},
		contracts.ClockProvenance(99):        {false, false},
	} {
		if got := p.Valid(); got != want.valid {
			t.Errorf("ClockProvenance(%d).Valid() = %v, want %v", int(p), got, want.valid)
		}
		if got := p.Stated(); got != want.stated {
			t.Errorf("ClockProvenance(%d).Stated() = %v, want %v", int(p), got, want.stated)
		}
	}
}

func TestClockProvenances_ReturnsACopy(t *testing.T) {
	p := contracts.ClockProvenances()
	p[0] = contracts.ClockProvenance(99)
	if contracts.ClockProvenances()[0] == contracts.ClockProvenance(99) {
		t.Error("ClockProvenances() hands out the backing array")
	}
}

// NotBefore compares two RECORDED times and takes nothing that could be the
// current clock. Both directions, so a constant would fail.
func TestAcceptedTime_NotBeforeComparesTwoRecordedTimes(t *testing.T) {
	early := goodTime()
	late := goodTime()
	late.At = early.At.Add(time.Minute)

	if !late.NotBefore(early) {
		t.Error("a later time reported as before an earlier one")
	}
	if early.NotBefore(late) {
		t.Error("an earlier time reported as not-before a later one")
	}
	if !early.NotBefore(early) {
		t.Error("a time is not not-before itself; the comparison must be inclusive")
	}
}

// ⚠️ THE STANDING RULE, ENFORCED RATHER THAN DOCUMENTED. Nothing in the replay
// path may read the current clock. This walks the package source and fails on a
// call to time.Now — the single most reasonable-looking line anyone could add
// here, and the one that would silently make replay non-deterministic.
func TestNothingInThisPackageReadsTheCurrentClock(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	examined := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		examined++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkg.Name == "time" && (sel.Sel.Name == "Now" || sel.Sel.Name == "Since" || sel.Sel.Name == "Until") {
				t.Errorf("%s calls time.%s at %s. Nothing in the replay path may read the current clock: "+
					"a value derived from it produces a different answer on replay than it did at "+
					"acceptance, which is the one thing replay exists to detect.",
					name, sel.Sel.Name, fset.Position(call.Pos()))
			}
			return true
		})
	}

	// ⚠️ The count is asserted: a rename or a build-tag change that made this
	// walk examine nothing would report no clock reads and read as a pass.
	if examined == 0 {
		t.Fatal("examined no source files, so this check passed by looking at nothing")
	}
	t.Logf("examined %d source file(s) for clock reads", examined)
}

// ---------------------------------------------------------------------------
// Clock rollback lives in kernel/reduce
// ---------------------------------------------------------------------------
//
// ⚠️ It USED to be an empty t.Skip here, guarded by a canary watching this
// package for an export containing "Rollback" or "Monotonic" — a signal that
// could never fire, because what the fixture waits for is the REDUCER.
//
// Detection is a property of the accepted SEQUENCE, not of a pair of values, so
// the fixture belongs where a sequence exists. It is now
// TestReduce_RejectsAClockRollback in kernel/reduce, with a real body that fails
// for a named reason rather than a skip that could not fail at all.
//
// What this package owns is the pair comparison that rests on:
// [AcceptedTime.NotBefore], and the refusal to compare two authorities.

// ⚠️ The name form is what reaches a record, and the record's digest is its
// identity — so this round trip is not a convenience test, it is the encoding
// the outsider reads.
func TestClockProvenance_JSONRoundTripsByName(t *testing.T) {
	for _, p := range contracts.ClockProvenances() {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("Marshal(%s): %v", p, err)
		}
		if want := `"` + p.String() + `"`; string(raw) != want {
			t.Errorf("Marshal(%s) = %s, want %s", p, raw, want)
		}
		var back contracts.ClockProvenance
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("Unmarshal(%s): %v", raw, err)
		}
		if back != p {
			t.Errorf("round trip changed %s into %s", p, back)
		}
	}
}

func TestClockProvenance_JSONRefusesANumberAndAnUnknownName(t *testing.T) {
	for name, raw := range map[string]string{
		"number":       `3`,
		"unknown name": `"gps"`,
		"null":         `null`,
	} {
		t.Run(name, func(t *testing.T) {
			var p contracts.ClockProvenance
			if err := json.Unmarshal([]byte(raw), &p); err == nil {
				t.Fatalf("Unmarshal accepted %s as %s", raw, p)
			}
		})
	}
}

func TestClockProvenance_MarshalRefusesAnOutOfVocabularyValue(t *testing.T) {
	if raw, err := json.Marshal(contracts.ClockProvenance(99)); err == nil {
		t.Fatalf("Marshal encoded an invalid provenance as %s", raw)
	}
}
