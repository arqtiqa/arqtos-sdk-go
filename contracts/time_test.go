package contracts_test

import (
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
// RED FIXTURE — clock rollback
// ---------------------------------------------------------------------------

// ⚠️ THIS FIXTURE IS RED ON PURPOSE, and it is skipped rather than failing so
// that CI stays green on a tree whose gap is KNOWN and NAMED. The build sequence
// puts the fixture before the implementation deliberately: a detection written
// after the mechanism is a test shaped to whatever the mechanism happened to do.
//
// What it is waiting for: acceptance-time monotonicity. An authority whose clock
// goes BACKWARDS between two accepted acts must be detectable — a later act
// carrying an earlier time is either a rolled-back clock or a reordered chain,
// and both are findings rather than noise.
//
// ⚠️ It cannot be written yet because the reducer does not exist: detection is a
// property of the accepted sequence, not of a pair of values, and the sequence is
// the reducer's. Both land together.
func TestClockRollbackIsDetected(t *testing.T) {
	t.Skip("RED FIXTURE — waiting on the reducer. A later accepted act carrying an EARLIER time from " +
		"the same authority must be reported as a rollback. Detection is a property of the accepted " +
		"sequence, so it lands with the reducer; see TestClockRollbackFixtureIsStillNeeded, which fails " +
		"once the capability exists so this skip cannot outlive its reason.")
}

// ⚠️ THE GUARD ON THE SKIP ABOVE. A skipped test nobody removes is a permanent
// hole that reads as coverage, so this fails the moment the capability it waits
// for appears — forcing the fixture to be written and the skip deleted in the
// same change.
//
// It is deliberately a NEGATIVE assertion about the package's own surface: the
// day something here can order accepted times across a sequence, the fixture is
// writable and this test says so.
func TestClockRollbackFixtureIsStillNeeded(t *testing.T) {
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
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			if strings.Contains(fn.Name.Name, "Rollback") || strings.Contains(fn.Name.Name, "Monotonic") {
				t.Errorf("%s declares %s: the capability the clock-rollback fixture waits for now exists, "+
					"so the fixture must be WRITTEN and its skip removed in this change. A skipped test "+
					"that outlives its reason is a hole that reads as coverage.", name, fn.Name.Name)
			}
		}
	}
	if examined == 0 {
		t.Fatal("examined no source files, so this guard passed by looking at nothing")
	}
}
