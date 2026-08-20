package reduce_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/tapeformat"
)

// ⭐ THE FIRST ENGINEERING ACT OF THIS DESIGN, and it is a test file with no
// implementation beside it.
//
// # Why these are written before the reducer
//
// A verification test written AFTER the verifier is a test shaped to whatever
// the verifier happened to do. These three claims — genesis accepted, a permit
// double-spend rejected, a tree swap rejected — are written first, over a
// committed fixture DAG, so the reducer has to satisfy a check it did not
// author.
//
// # ⚠️ They stay RED into W2, and that is the schedule
//
// The reducer that turns them green is separate, deliberate work. What this file
// delivers is the failing tests: they exist, they name what they wait for, and
// they precede the code they will judge.
//
// # ⚠️ "For the right reason" is checked TODAY, and green
//
// A test red from a nil dereference is indistinguishable from one red because a
// defect went uncaught. So the DAG's own properties are asserted below and pass
// now: the tape reads and chain-verifies, every accepted entry has its act, the
// double-spend candidate really does reuse a permit the tape already spent, and
// the tree-swap candidate really is observed against a tree its act does not
// bind. Without that, the three skips below would be promises rather than
// fixtures.

const dag = "testdata/dag"

func read[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dag, path))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	v, err := contracts.Decode[T](raw)
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return v
}

func acceptedTape(t *testing.T) []tapeformat.Entry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dag, "tape.tape"))
	if err != nil {
		t.Fatalf("reading the tape: %v", err)
	}
	entries, err := tapeformat.ReadStream(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("the fixture tape does not read: %v", err)
	}
	return entries
}

func acceptedActs(t *testing.T) map[string]contracts.ActSpec {
	t.Helper()
	dirents, err := os.ReadDir(filepath.Join(dag, "accepted"))
	if err != nil {
		t.Fatalf("reading the accepted acts: %v", err)
	}
	out := make(map[string]contracts.ActSpec, len(dirents))
	for _, e := range dirents {
		a := read[contracts.ActSpec](t, filepath.Join("accepted", e.Name()))
		if _, dup := out[a.ActBodyID]; dup {
			t.Fatalf("two accepted acts share body id %s", a.ActBodyID)
		}
		out[a.ActBodyID] = a
	}
	return out
}

// ---------------------------------------------------------------------------
// The DAG's own properties — GREEN, and what makes the RED fixtures meaningful
// ---------------------------------------------------------------------------

func TestFixtureDAG_TapeReadsAndVerifies(t *testing.T) {
	entries := acceptedTape(t)
	if len(entries) != 2 {
		t.Fatalf("the accepted prefix holds %d entries, want 2 (the genesis act and one merge)", len(entries))
	}
	if err := tapeformat.VerifyChain(entries); err != nil {
		t.Fatalf("the fixture tape is not a chain: %v", err)
	}
}

// ⚠️ Every entry must have its act. An entry pointing at nothing would make the
// double-spend fixture below untestable while looking complete.
func TestFixtureDAG_EveryAcceptedEntryHasItsAct(t *testing.T) {
	entries := acceptedTape(t)
	acts := acceptedActs(t)

	// ⚠️ Entry 0 is the GENESIS ACT and deliberately has no ActSpec. The
	// previous fixture reused the genesis id as an ActSpec.act_body_id, which
	// asserts that a digest under DomainGenesis and one under DomainActBody are
	// interchangeable — exactly what domain separation exists to prevent.
	if len(acts) != len(entries)-1 {
		t.Fatalf("the tape holds %d entries and %d ActSpecs are committed; entry 0 is the genesis act "+
			"and has none", len(entries), len(acts))
	}
	for i, e := range entries[1:] {
		if _, ok := acts[e.ActBodyID]; !ok {
			t.Errorf("entry %d records act %s, which is not committed", i+1, e.ActBodyID)
		}
	}
}

// ⭐ THE BINDING THE FIXTURE PREVIOUSLY DID NOT HAVE. Every committed act's
// act_body_id is re-derived from the act itself and compared. Before this, the
// ids were LITERALS — so nothing tied the DAG to the encoding it claims to use,
// and the public act type could not be canonically encoded at all.
func TestFixtureDAG_EveryActIDIsTheActsOwnDigest(t *testing.T) {
	acts := acceptedActs(t)
	for _, name := range []string{"candidates/double-spend.json", "candidates/tree-swap.json"} {
		a := read[contracts.ActSpec](t, name)
		acts[a.ActBodyID] = a
	}

	checked := 0
	for id, a := range acts {
		unsealed := a
		unsealed.ActBodyID = ""
		got, err := canonical.ActBodyID(unsealed)
		if err != nil {
			t.Fatalf("act %s cannot be canonically encoded: %v", id, err)
		}
		if got != id {
			t.Errorf("act carries id %s but digests to %s — the id is a literal, not a digest", id, got)
		}
		checked++
	}
	if checked != 3 {
		t.Fatalf("checked %d acts, want 3", checked)
	}
}

// The tape's first act is the genesis act, and its id is the genesis act's
// DIGEST rather than a value written by hand — so the fixture cannot drift from
// the document it claims to be.
func TestFixtureDAG_TheFirstEntryIsTheGenesisAct(t *testing.T) {
	g := read[contracts.RepositoryGenesis](t, "genesis.json")
	if err := g.Validate(); err != nil {
		t.Fatalf("the fixture genesis does not validate: %v", err)
	}
	id, err := g.ID()
	if err != nil {
		t.Fatalf("genesis id: %v", err)
	}
	if got := acceptedTape(t)[0].ActBodyID; got != id {
		t.Fatalf("the tape's first entry is act %s; the genesis document digests to %s", got, id)
	}

	// ⚠️ And no ActSpec claims that id. Domain tags exist so a genesis digest
	// and an act-body digest are not interchangeable; a fixture that used one
	// as the other would assert they are.
	if _, ok := acceptedActs(t)[id]; ok {
		t.Error("an ActSpec carries the genesis act's id — that id is a digest under a different domain tag")
	}
}

// ⚠️ THE DOUBLE-SPEND FIXTURE IS A REAL DOUBLE-SPEND. If the candidate's permit
// differed from the spent one, the RED test below would be waiting for a
// rejection that should never happen — and would go green the day a reducer
// rejected everything.
func TestFixtureDAG_TheDoubleSpendCandidateReusesASpentPermit(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/double-spend.json")
	acts := acceptedActs(t)

	var spentBy string
	for _, a := range acts {
		if a.Permit == candidate.Permit {
			spentBy = a.ActBodyID
		}
	}
	if spentBy == "" {
		t.Fatalf("no accepted act spends permit %+v, so this candidate is not a double-spend", candidate.Permit)
	}
	if candidate.ActBodyID == spentBy {
		t.Fatal("the candidate IS the accepted act, so nothing is being spent twice")
	}
	if candidate.Permit.IssuerActBodyID == "" {
		t.Fatal("the candidate names no permit at all")
	}
	t.Logf("permit %s#%s is already spent by %s", candidate.Permit.IssuerActBodyID, candidate.Permit.OutputIndex, spentBy)
}

// ⚠️ THE TREE-SWAP FIXTURE IS A REAL SWAP: the act binds one tree and the
// observation reports another. Both halves are asserted, because a fixture whose
// two trees happened to match would make the RED test below meaningless.
func TestFixtureDAG_TheTreeSwapCandidateIsObservedAgainstADifferentTree(t *testing.T) {
	act := read[contracts.ActSpec](t, "candidates/tree-swap.json")
	obs := read[contracts.EvidenceEvent](t, "candidates/tree-swap-observation.json")

	if obs.ActBodyID != act.ActBodyID {
		t.Fatalf("the observation is about act %s, the candidate is %s", obs.ActBodyID, act.ActBodyID)
	}
	if obs.EventKind != contracts.EventObservation {
		t.Errorf("the fixture event is a %s, not an observation", obs.EventKind)
	}
	observed := obs.Detail["observed_tree_oid"]
	if observed == "" {
		t.Fatal("the observation reports no tree, so there is nothing to compare against the bound one")
	}
	if act.CandidateTreeOID == "" {
		t.Fatal("the candidate act binds no tree")
	}
	if observed == act.CandidateTreeOID {
		t.Fatalf("the observed tree equals the bound tree (%s), so this fixture is not a swap", observed)
	}
	t.Logf("act binds tree %s; observation reports %s", act.CandidateTreeOID, observed)
}

// ⚠️ The two rejection candidates must be different acts. Two fixtures that
// reduced to one would be one fixture and a duplicate, with the second one's
// coverage imaginary.
func TestFixtureDAG_TheTwoRejectionCandidatesAreDistinct(t *testing.T) {
	a := read[contracts.ActSpec](t, "candidates/double-spend.json")
	b := read[contracts.ActSpec](t, "candidates/tree-swap.json")
	if a.ActBodyID == b.ActBodyID {
		t.Fatal("the two candidates are the same act")
	}
	if a.Permit == b.Permit {
		t.Error("the two candidates spend the same permit, so the tree-swap fixture is also a double-spend " +
			"and a reducer catching only one would satisfy both tests")
	}
}

// ---------------------------------------------------------------------------
// THE THREE RED FIXTURES
// ---------------------------------------------------------------------------

// ⚠️ RED ON PURPOSE, skipped so CI stays green on a tree whose gap is KNOWN.
//
// Replaying from zero, the genesis act must be ACCEPTED, and the state that
// results must name the root grant and the root keys. This is the only one of
// the three that is an acceptance rather than a rejection — and it has to be
// here, because a reducer that rejected everything would satisfy the other two.
func TestReduce_AcceptsRepositoryGenesis(t *testing.T) {
	t.Skip("RED FIXTURE — waiting on the reducer. Replaying testdata/dag from zero must ACCEPT the " +
		"genesis act and produce state naming its root grant and root keys. It is the acceptance case " +
		"on purpose: a reducer that refused every input would pass the two rejection fixtures below. " +
		"See TestReduceFixturesAreStillNeeded.")
}

// ⚠️ RED ON PURPOSE. The candidate spends a permit the accepted prefix has
// already spent — proved a real double-spend, green, above. It must be REJECTED,
// and the rejection must name the earlier spend: an act refused with "no" and no
// reason cannot be reconciled against the tape by anyone auditing it.
func TestReduce_RejectsAPermitDoubleSpend(t *testing.T) {
	t.Skip("RED FIXTURE — waiting on the reducer. testdata/dag/candidates/double-spend.json spends a " +
		"permit already spent by the accepted prefix, and must be REJECTED with a reason naming the " +
		"earlier spend. Asserting only that it was refused would pass against a reducer that refuses " +
		"everything, which is why the assertion is on the reason.")
}

// ⚠️ RED ON PURPOSE. The candidate's signed body binds one tree and the
// observation reports another — proved a real swap, green, above. Every
// signature is valid and the act is internally coherent; only the reducer
// comparing bound against observed catches it.
func TestReduce_RejectsATreeSwap(t *testing.T) {
	t.Skip("RED FIXTURE — waiting on the reducer. testdata/dag/candidates/tree-swap.json binds tree A " +
		"and is observed against tree B, and must be REJECTED with a reason naming the mismatch. This " +
		"is the W2 kill gate: if a tree swap is not caught here, the design's central claim does not hold.")
}

// ⚠️ THE GUARD ON ALL THREE SKIPS. A skipped test nobody removes is a permanent
// hole that reads as coverage, so this fails the moment the reducer exists —
// forcing the three fixtures to be written and the skips deleted in the same
// change.
//
// It reads the package's own source rather than calling anything, because there
// is nothing to call: kernel/reduce exports nothing, by design, until it does.
func TestReduceFixturesAreStillNeeded(t *testing.T) {
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
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		examined++
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if decl.Name.IsExported() {
					t.Errorf("%s exports %s: the reducer exists, so the three fixtures above must be "+
						"WRITTEN and their skips removed in this change.", name, decl.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
						t.Errorf("%s exports type %s: the reducer exists, so the three fixtures above "+
							"must be WRITTEN and their skips removed in this change.", name, ts.Name.Name)
					}
				}
			}
		}
	}
	// ⚠️ Count-asserted: a rename that made this walk examine nothing would
	// report no reducer and read exactly like a pass.
	if examined == 0 {
		t.Fatal("examined no source files, so this guard passed by looking at nothing")
	}
}

// The fixture set is committed and complete. A file deleted or renamed would
// otherwise leave the tests above exercising a smaller DAG than they name.
func TestFixtureDAG_IsCommittedAndComplete(t *testing.T) {
	want := []string{
		"accepted/act-1-spend.json",
		"candidates/double-spend.json",
		"candidates/tree-swap-observation.json",
		"candidates/tree-swap.json",
		"genesis.json",
		"tape.tape",
	}
	var got []string
	err := filepath.WalkDir(dag, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dag, path)
		if err != nil {
			return err
		}
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the fixture DAG: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("the DAG holds %v, want exactly %v", got, want)
	}
	for _, w := range want {
		if _, err := os.Stat(filepath.Join(dag, w)); err != nil {
			t.Errorf("%s is missing: %v", w, err)
		}
	}
}

// ⚠️ Every fixture must decode STRICTLY — unknown fields refused. A DAG that
// only decoded leniently would let a field drift in without anything noticing,
// and the reducer would then be replaying a document nobody reviewed.
func TestFixtureDAG_EveryRecordDecodesStrictly(t *testing.T) {
	checked := 0
	for _, f := range []struct {
		path string
		fn   func(*testing.T, string)
	}{
		{"genesis.json", func(t *testing.T, p string) { _ = read[contracts.RepositoryGenesis](t, p) }},
		{"accepted/act-1-spend.json", func(t *testing.T, p string) { _ = read[contracts.ActSpec](t, p) }},
		{"candidates/double-spend.json", func(t *testing.T, p string) { _ = read[contracts.ActSpec](t, p) }},
		{"candidates/tree-swap.json", func(t *testing.T, p string) { _ = read[contracts.ActSpec](t, p) }},
		{"candidates/tree-swap-observation.json", func(t *testing.T, p string) { _ = read[contracts.EvidenceEvent](t, p) }},
	} {
		t.Run(f.path, func(t *testing.T) { f.fn(t, f.path) })
		checked++
	}
	if checked != 5 {
		t.Fatalf("decoded %d records, want 5 — the tape is checked separately by its own reader", checked)
	}
}
