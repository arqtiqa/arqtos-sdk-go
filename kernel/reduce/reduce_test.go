package reduce_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
	"github.com/arqtiqa/arqtos-sdk-go/internal/redfixture"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/reduce"
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
// THE KILL GATES
// ---------------------------------------------------------------------------
//
// ⚠️ Each of these has a REAL BODY that runs, and redfixture.Expect requires it
// to fail. An empty body fails Expect; a body that starts passing fails Expect
// and demands promotion. That is the difference between these and what shipped
// in W1 — t.Skip lines with nothing after them, tests that could not fail and
// therefore guarded nothing.
//
// Every one asserts the REASON, never merely that an error occurred. A reducer
// that refused every input would satisfy the weaker assertion, and would be
// exactly as wrong as one that accepted everything.

func input(t *testing.T) reduce.Input {
	t.Helper()
	return reduce.Input{
		Genesis:  read[contracts.RepositoryGenesis](t, "genesis.json"),
		Accepted: acceptedTape(t),
		Acts:     acceptedActs(t),
	}
}

// Replaying from zero ACCEPTS the genesis act, and the resulting state names the
// authority it established.
//
// ⚠️ PROMOTED from a red fixture. The assertions below are UNEDITED — the same
// ones written before the reducer existed, now taking *testing.T directly
// because redfixture.Expect requires its body to fail and this one no longer
// does. That is the promotion the harness demands, and editing the assertions
// to fit the implementation would be the one thing it exists to prevent.
//
// ⚠️ It is the only one of the four that is an acceptance, and it has to be
// here: a reducer that refused everything would pass the three rejections.
func TestReduce_AcceptsRepositoryGenesis(t *testing.T) {
	in := input(t)
	in.Accepted = in.Accepted[:1] // the genesis entry alone
	in.Acts = nil

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("reducing the genesis act returned %v; it must be judged, not deferred", err)
	}
	if !out.Accepted {
		t.Fatalf("the genesis act was refused: %s", out.Reason)
	}
	if len(out.RootKeys) != len(in.Genesis.RootKeys) {
		t.Errorf("the outcome names %d root keys and the genesis act establishes %d — an accepted "+
			"genesis that establishes no authority has been waved through rather than reduced",
			len(out.RootKeys), len(in.Genesis.RootKeys))
	}
	if out.RootGrant.Scope.Subjects == nil {
		t.Errorf("the outcome carries no root grant, so nothing below it could be checked for amplification")
	}
}

// ⚠️ The outcome must not ALIAS the caller's slice. A struct copy shares a
// slice's backing array, so a caller mutating one would silently change the
// other — the defect that made a golden fixture drift earlier in this project,
// and worse here because the aliased value is the root authority.
func TestReduce_TheOutcomeDoesNotAliasTheGenesisKeys(t *testing.T) {
	in := input(t)
	in.Accepted = in.Accepted[:1]
	in.Acts = nil

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil || !out.Accepted {
		t.Fatalf("the genesis act was not accepted: %v %s", err, out.Reason)
	}
	if len(out.RootKeys) == 0 {
		t.Fatal("no root keys to check")
	}
	out.RootKeys[0].KeyID = "mutated-through-the-outcome"
	if in.Genesis.RootKeys[0].KeyID == "mutated-through-the-outcome" {
		t.Fatal("the outcome shares the genesis act's backing array")
	}
}

// Every way the bootstrap can be unusable, refused as a DECISION with a reason —
// never as an error, and never by accident.
func TestReduce_RefusesAnUnusableBootstrap(t *testing.T) {
	cases := map[string]struct {
		mutate func(*reduce.Input)
		names  string
	}{
		"the genesis act does not validate": {
			func(in *reduce.Input) { in.Genesis.RootKeys = nil },
			"genesis act is unusable",
		},
		"the tape is empty": {
			func(in *reduce.Input) { in.Accepted = nil },
			"tape is empty",
		},
		"the tape does not begin at the genesis act": {
			func(in *reduce.Input) { in.Accepted[0].ActBodyID = "sha256:" + strings.Repeat("f", 64) },
			"rooted in the genesis it claims",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			in := input(t)
			in.Accepted = append([]tapeformat.Entry(nil), in.Accepted[:1]...)
			in.Acts = nil
			c.mutate(&in)

			out, err := reduce.Reduce(context.Background(), in)
			// ⚠️ A refusal is a DECISION. An error would mean the reducer could
			// not decide, which is a different thing with a different response.
			if err != nil {
				t.Fatalf("a decidable input returned an error: %v", err)
			}
			if out.Accepted {
				t.Fatal("accepted")
			}
			if !strings.Contains(out.Reason, c.names) {
				t.Errorf("the refusal %q does not name the problem (%q)", out.Reason, c.names)
			}
		})
	}
	if len(cases) != 3 {
		t.Fatalf("checked %d ways the bootstrap can be unusable, want 3", len(cases))
	}
}

// ⚠️ REFUSE BY DEFAULT. A rule that has not been written yet must not let
// anything through — so an input no rule admits is refused, with a reason
// saying so, rather than accepted or errored.
func TestReduce_RefusesWhatNoRuleAdmits(t *testing.T) {
	in := input(t) // two accepted entries, no candidate: no rule covers this yet
	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("returned an error rather than a decision: %v", err)
	}
	if out.Accepted {
		t.Fatal("an input no rule admits was ACCEPTED — the default must be refusal")
	}
	if out.Reason == "" {
		t.Error("refused without a reason")
	}
}

// The candidate spends a permit the accepted prefix has already spent — proved a
// real double-spend, green, above. The refusal must NAME the earlier spend.
// ⚠️ PROMOTED. Assertions unedited — the wrapper is gone because the body no
// longer fails, which is what redfixture.Expect demands.
func TestReduce_RejectsAPermitDoubleSpend(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/double-spend.json")
	in := input(t)
	in.Candidate = &candidate

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("reducing a double-spend returned %v; it must be judged, not deferred", err)
	}
	if out.Accepted {
		t.Fatal("a permit already spent by the accepted prefix was spent again")
	}
	// ⚠️ The REASON, not the refusal. A reducer that refuses everything
	// satisfies "not accepted" and is useless.
	var spentBy string
	for id, a := range in.Acts {
		if a.Permit == candidate.Permit {
			spentBy = id
		}
	}
	if !strings.Contains(out.Reason, spentBy) {
		t.Errorf("the refusal %q does not name the earlier spend (%s), so nobody auditing the tape "+
			"could reconcile it", out.Reason, spentBy)
	}
}

// ⚠️ THE OTHER HALF, without which the rule is indistinguishable from refusing
// everything. A candidate spending an UNSPENT permit must get past this rule.
func TestReduce_DoesNotRefuseAnUnspentPermit(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/double-spend.json")
	candidate.Permit.IssuerActBodyID = "sha256:" + strings.Repeat("a", 64)
	in := input(t)
	in.Candidate = &candidate

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if strings.Contains(out.Reason, "already spent") {
		t.Fatalf("an unspent permit was refused as a double-spend: %s", out.Reason)
	}
}

// The genesis act establishes authority rather than consuming it, so it must not
// be treated as a spender — otherwise the first act to reuse its permit shape
// would be refused for the wrong reason.
func TestReduce_TheGenesisActSpendsNothing(t *testing.T) {
	genesisEntry := input(t).Accepted[0]
	candidate := read[contracts.ActSpec](t, "candidates/double-spend.json")

	in := input(t)
	// An ActSpec is deliberately registered under the genesis entry's id, with
	// the candidate's permit. If genesis were treated as a spender, this would
	// be reported as a double-spend.
	impostor := candidate
	impostor.ActBodyID = genesisEntry.ActBodyID
	in.Acts[genesisEntry.ActBodyID] = impostor
	in.Candidate = &candidate

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if strings.Contains(out.Reason, genesisEntry.ActBodyID) {
		t.Errorf("the genesis act was treated as having spent a permit: %s", out.Reason)
	}
}

// ⚠️ AN ABSENT RECORD IS NOT EVIDENCE OF ABSENCE. An accepted entry whose
// ActSpec is missing might have spent this very permit, and treating it as
// unspent would let a double-spend through by WITHHOLDING A FILE.
func TestReduce_RefusesWhenAnAcceptedActIsMissing(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/double-spend.json")
	in := input(t)
	in.Candidate = &candidate
	for id := range in.Acts {
		delete(in.Acts, id)
	}

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if out.Accepted {
		t.Fatal("a candidate was accepted against a prefix whose acts are missing")
	}
	if !strings.Contains(out.Reason, "missing") {
		t.Errorf("the refusal %q does not say the record is missing — an absent act must not read "+
			"as an unspent permit", out.Reason)
	}
}

func TestReduce_RefusesACandidateNamingNoPermit(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/double-spend.json")
	candidate.Permit = contracts.PermitID{}
	in := input(t)
	in.Candidate = &candidate

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if out.Accepted || !strings.Contains(out.Reason, "no permit") {
		t.Errorf("a candidate with no permit was not refused for that reason: %v %q", out.Accepted, out.Reason)
	}
}

// The candidate's body binds one tree and the observation reports another —
// proved a real swap, green, above. Every signature is valid and the act is
// internally coherent; only the reducer comparing bound against observed catches
// it. This is the W2 kill gate.
func TestReduce_RejectsATreeSwap(t *testing.T) {
	redfixture.Expect(t, "the reducer", func(ft redfixture.T) {
		candidate := read[contracts.ActSpec](t, "candidates/tree-swap.json")
		observation := read[contracts.EvidenceEvent](t, "candidates/tree-swap-observation.json")
		in := input(t)
		in.Candidate = &candidate
		in.Observations = []contracts.EvidenceEvent{observation}

		out, err := reduce.Reduce(context.Background(), in)
		if err != nil {
			ft.Fatalf("reducing a tree swap returned %v; it must be judged, not deferred", err)
		}
		if out.Accepted {
			ft.Fatal("an act observed against a tree its signed body does not bind was accepted — if a " +
				"tree swap is not caught here, the design's central claim does not hold")
		}
		observed := observation.Detail["observed_tree_oid"]
		if !strings.Contains(out.Reason, candidate.CandidateTreeOID) || !strings.Contains(out.Reason, observed) {
			ft.Errorf("the refusal %q does not name both the bound tree (%s) and the observed one (%s)",
				out.Reason, candidate.CandidateTreeOID, observed)
		}
	})
}

// ⚠️ CLOCK ROLLBACK, and it lives HERE rather than in contracts because
// detection is a property of the accepted SEQUENCE, not of a pair of values.
// It was previously an empty skip in contracts/time_test.go, guarded by a canary
// watching for a `contracts` export containing "Rollback" — a signal that could
// never fire, because what it waits for is the reducer.
func TestReduce_RejectsAClockRollback(t *testing.T) {
	redfixture.Expect(t, "the reducer", func(ft redfixture.T) {
		in := input(t)
		if len(in.Accepted) < 2 {
			ft.Fatalf("the fixture tape holds %d entries; a rollback needs two", len(in.Accepted))
		}

		// The second entry's acceptance time moves BACKWARDS, from the same
		// authority — so the two are comparable and the regression is real
		// rather than an artefact of comparing two clocks.
		rolled := append([]tapeformat.Entry(nil), in.Accepted...)
		rolled[1].AcceptedAt = contracts.AcceptedTime{
			At:        rolled[0].AcceptedAt.At.Add(-time.Hour),
			Authority: rolled[0].AcceptedAt.Authority,
		}
		in.Accepted = rolled

		out, err := reduce.Reduce(context.Background(), in)
		if err != nil {
			ft.Fatalf("reducing a rolled-back tape returned %v; it must be judged, not deferred", err)
		}
		if out.Accepted {
			ft.Fatal("a tape whose acceptance times run backwards under one authority was accepted")
		}
		if !strings.Contains(strings.ToLower(out.Reason), "time") {
			ft.Errorf("the refusal %q does not name the time regression", out.Reason)
		}
	})
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
