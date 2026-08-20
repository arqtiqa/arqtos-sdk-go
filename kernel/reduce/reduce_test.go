package reduce_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
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
// the verifier happened to do. ⚠️ That protects the REJECTIONS only — see the
// allow-direction note below, where writing the test first did not help. These three claims — genesis accepted, a permit
// double-spend rejected, a tree swap rejected — are written first, over a
// committed fixture DAG, so the reducer has to satisfy a check it did not
// author.
//
// # ⚠️ They were RED into W2, and they are green now — PROMOTED, not edited
//
// Every one was written before the reducer existed and wrapped in
// redfixture.Expect, which requires its body to FAIL. As each rule landed, the
// harness went red — "RED FIXTURE IS NO LONGER RED" — and demanded the fixture
// be promoted to an ordinary test in the same change.
//
// The REJECTION assertions below are those originals, unedited. Only the wrapper
// is gone. Editing them to fit the implementation is the one thing that harness
// exists to prevent, and there is no longer a redfixture import in this file.
//
// # ⚠️ FOUR ALLOW-DIRECTION TESTS WERE REWRITTEN, AND THAT IS NOT A PROMOTION
//
// They are not fixtures and never were. Each claimed to prove the reducer ADMITS
// something, and each asserted only that a particular phrase was ABSENT from the
// refusal reason — which a neighbouring rule's refusal satisfies. All four were
// green for a week over a reducer whose only acceptance was the genesis case.
//
// One of them could not have passed honestly at all: it offered a candidate with
// no observation, so the correct answer was always a refusal, and the phrase it
// watched for was never going to appear.
//
// They now assert out.Accepted, and allowpath_test.go enforces mechanically that
// any test claiming an acceptance reads it — because the comments on those four
// named this exact failure mode and nobody, including their author, spotted that
// the assertion underneath did not check it.
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

// ⚠️ THE SWAP FIXTURE'S EVIDENCE MUST BE AN OBSERVATION, or the W2 kill gate is
// testing the wrong thing. Since only an observation feeds the tree comparison,
// a fixture that drifted to any other kind would make the swap test assert an
// UNOBSERVED refusal while still being green — a gate that had stopped checking
// what it names.
func TestFixtureDAG_TheSwapEvidenceIsAnObservation(t *testing.T) {
	ev := read[contracts.EvidenceEvent](t, "candidates/tree-swap-observation.json")
	if ev.EventKind != contracts.EventObservation {
		t.Fatalf("the tree-swap evidence is a %q, not an observation — the swap gate would pass vacuously", ev.EventKind)
	}
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
// ⚠️ ALL FOUR ARE GREEN, AND ALL FOUR WERE PROMOTED. Each was written before the
// reducer existed and wrapped in redfixture.Expect, which requires its body to
// FAIL: an empty body fails it, and a body that starts passing fails it and
// demands promotion in the same change. That is what happened to each of these
// as its rule landed, and the assertions are the originals — unedited.
//
// The contrast is with what shipped in W1: t.Skip lines with nothing after them,
// tests that could not fail and therefore guarded nothing.
//
// Every one asserts the REASON, never merely that an error occurred. A reducer
// that refused every input would satisfy the weaker assertion, and would be
// exactly as wrong as one that accepted everything — which is why each rule also
// carries a test proving it does NOT refuse the case it is meant to allow.

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

// ⚠️ THIS TEST REPLACES "REFUSE BY DEFAULT", WHICH WAS RETIRED WITH A REASON.
//
// While rules were missing, refusing by default was right: an unwritten rule
// must not let anything through. It was ALSO how the reducer came to admit no
// candidate at all — the genesis case was the only accept, every candidate fell
// through to the fallback, and the tests written to catch exactly that asserted
// a phrase was ABSENT rather than that the act was ACCEPTED. The fallback
// satisfied them.
//
// What replaces it is a property that survives new rules: ADDING A RULE CAN ONLY
// EVER REFUSE MORE. Both rule sets are enumerated, and this test fails if one
// shrinks — a rule dropped in a refactor is the only way the reducer gets more
// permissive by accident.
func TestReduce_RunsEveryRuleItDeclares(t *testing.T) {
	if got := len(reduce.PrefixRuleNames()); got != 1 {
		t.Errorf("prefix rules: got %d, want 1 — a rule was added or dropped without updating this test", got)
	}
	if got := len(reduce.CandidateRuleNames()); got != 3 {
		t.Errorf("candidate rules: got %d, want 3 — a rule was added or dropped without updating this test", got)
	}
	for _, name := range append(reduce.PrefixRuleNames(), reduce.CandidateRuleNames()...) {
		if name == "" {
			t.Error("a declared rule has no name — it cannot be reported in a refusal")
		}
	}
}

// A replay with no candidate asks whether the tape is well-formed and what
// authority it establishes. A verified one is ACCEPTED.
//
// ⚠️ This input was previously the fixture for "no rule admits this", and the
// same input was ALSO the fixture for "a tape in order is not refused by the
// time rule". Two tests, opposite names, one refusal, both green — which is what
// a phrase-absence assertion buys.
func TestReduce_AcceptsAReplayOfAnAcceptedTape(t *testing.T) {
	out, err := reduce.Reduce(context.Background(), input(t))
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("a verified tape replayed with no candidate was REFUSED: %s", out.Reason)
	}
	if out.Head == "" {
		t.Error("accepted without reporting the head it was decided against")
	}
}

// ⚠️ ONE ACT, ONE POSITION. Re-offering an act already on the tape is not a
// no-op: it would give one act two acceptance times, and it would spend its
// permit again the day the spend rule stops looking at the whole prefix.
func TestReduce_RefusesACandidateAlreadyOnTheTape(t *testing.T) {
	in := input(t)
	replayed := in.Acts[in.Accepted[1].ActBodyID]
	in.Candidate = &replayed

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if out.Accepted {
		t.Fatal("an act already on the tape was accepted a second time")
	}
	if !strings.Contains(out.Reason, "already on the tape") {
		t.Errorf("refused for the wrong reason: %s", out.Reason)
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

// ⚠️ THE OTHER HALF, and it asserts ACCEPTANCE. Asserting only that the
// "already spent" phrase is absent was the original defect: the candidate was
// refused by the NEXT rule, the phrase was duly absent, and the test was green
// while the reducer admitted nothing.
func TestReduce_AcceptsACandidateSpendingAnUnspentPermit(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/double-spend.json")
	candidate.Permit.IssuerActBodyID = "sha256:" + strings.Repeat("a", 64)
	in := input(t)
	in.Candidate = &candidate
	// ⚠️ AND IT MUST BE OBSERVED. The earlier version of this test supplied no
	// observation and asserted only that the phrase "already spent" was absent —
	// so the candidate was refused as UNOBSERVED, the phrase was duly absent, and
	// the test was green over an input that could never have been accepted.
	in.Observations = []contracts.EvidenceEvent{observing(t, candidate)}

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("a candidate spending an unspent permit was REFUSED: %s", out.Reason)
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
// ⭐ THE W2 KILL GATE, PROMOTED. Assertions unedited.
func TestReduce_RejectsATreeSwap(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/tree-swap.json")
	observation := read[contracts.EvidenceEvent](t, "candidates/tree-swap-observation.json")
	in := input(t)
	in.Candidate = &candidate
	in.Observations = []contracts.EvidenceEvent{observation}

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("reducing a tree swap returned %v; it must be judged, not deferred", err)
	}
	if out.Accepted {
		t.Fatal("an act observed against a tree its signed body does not bind was accepted — if a " +
			"tree swap is not caught here, the design's central claim does not hold")
	}
	observed := observation.Detail["observed_tree_oid"]
	if !strings.Contains(out.Reason, candidate.CandidateTreeOID) || !strings.Contains(out.Reason, observed) {
		t.Errorf("the refusal %q does not name both the bound tree (%s) and the observed one (%s)",
			out.Reason, candidate.CandidateTreeOID, observed)
	}
}

// ⭐ THE ACCEPT PATH. This is the act the whole design exists to admit: signed,
// authorised by an unspent permit, on an in-order chain, and independently
// observed to have produced the tree it binds. It is ACCEPTED.
func TestReduce_AcceptsAnActObservedAgainstTheTreeItBinds(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/tree-swap.json")
	in := input(t)
	in.Candidate = &candidate
	in.Observations = []contracts.EvidenceEvent{observing(t, candidate)}

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("an act observed against the tree it binds was REFUSED: %s", out.Reason)
	}
}

// ⚠️ ABSENCE OF EVIDENCE IS NOT EVIDENCE OF ABSENCE. An act nobody watched must
// not be accepted AS IF the effect had been seen to match — that is precisely
// the state a tree swap arrives in when the observer is degraded.
func TestReduce_RefusesAnActWithNoObservationRatherThanAssumingAMatch(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/tree-swap.json")
	in := input(t)
	in.Candidate = &candidate
	in.Observations = nil

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if out.Accepted {
		t.Fatal("an act with no observation at all was accepted as if its effect had been seen")
	}
	// ⚠️ And the reason must say WHICH it is. "Unobserved" and "observed and
	// wrong" are different findings with different responses, and a report that
	// could not tell them apart would send someone to the wrong place.
	if !strings.Contains(out.Reason, "no observation") {
		t.Errorf("the refusal %q does not distinguish unobserved from mismatched", out.Reason)
	}
	if strings.Contains(out.Reason, "not the effect that was authorised") {
		t.Errorf("an unobserved act was reported as a tree swap: %s", out.Reason)
	}
}

// An observation about a DIFFERENT act says nothing about this one. Applying it
// would refuse an innocent candidate on someone else's evidence.
func TestReduce_IgnoresAnObservationAboutAnotherAct(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/tree-swap.json")
	foreign := read[contracts.EvidenceEvent](t, "candidates/tree-swap-observation.json")
	foreign.ActBodyID = "sha256:" + strings.Repeat("b", 64)

	in := input(t)
	in.Candidate = &candidate
	in.Observations = []contracts.EvidenceEvent{foreign}

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if strings.Contains(out.Reason, "not the effect that was authorised") {
		t.Fatalf("an observation about another act was applied to this one: %s", out.Reason)
	}
	// It is unobserved, which is its own refusal.
	if !strings.Contains(out.Reason, "no observation") {
		t.Errorf("the refusal %q does not report the candidate as unobserved", out.Reason)
	}
}

func TestReduce_RefusesACandidateThatBindsNoTree(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/tree-swap.json")
	candidate.CandidateTreeOID = ""
	in := input(t)
	in.Candidate = &candidate

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if out.Accepted || !strings.Contains(out.Reason, "binds no tree") {
		t.Errorf("a candidate binding no tree was not refused for that reason: %v %q", out.Accepted, out.Reason)
	}
}

// ⚠️ CLOCK ROLLBACK, and it lives HERE rather than in contracts because
// detection is a property of the accepted SEQUENCE, not of a pair of values.
// It was previously an empty skip in contracts/time_test.go, guarded by a canary
// watching for a `contracts` export containing "Rollback" — a signal that could
// never fire, because what it waits for is the reducer.
// ⚠️ PROMOTED. Assertions unedited.
func TestReduce_RejectsAClockRollback(t *testing.T) {
	in := input(t)
	if len(in.Accepted) < 2 {
		t.Fatalf("the fixture tape holds %d entries; a rollback needs two", len(in.Accepted))
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
		t.Fatalf("reducing a rolled-back tape returned %v; it must be judged, not deferred", err)
	}
	if out.Accepted {
		t.Fatal("a tape whose acceptance times run backwards under one authority was accepted")
	}
	if !strings.Contains(strings.ToLower(out.Reason), "time") {
		t.Errorf("the refusal %q does not name the time regression", out.Reason)
	}
}

// ⚠️ The promoted fixture asserts only that the reason mentions "time", and the
// incomparable-authorities refusal below ALSO mentions clocks. That is the
// assert-the-sentinel trap one level up, so this pins the phrase that belongs to
// THIS rule.
func TestReduce_TheRollbackRefusalNamesTheRegressionSpecifically(t *testing.T) {
	in := rolledBack(t)
	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !strings.Contains(out.Reason, "regress") {
		t.Errorf("the refusal %q does not say the times regress", out.Reason)
	}
	if strings.Contains(out.Reason, "not comparable") {
		t.Errorf("a regression under ONE authority was reported as incomparable clocks: %s", out.Reason)
	}
}

// ⚠️ A DIFFERENT FINDING, AND IT MUST READ AS ONE. Incomparable clocks is a
// configuration fact about the acceptors; a regression is a clock that moved
// backwards. They send an operator to different places, and a reducer that
// collapsed them would send them to the wrong one.
func TestReduce_RefusesATapeWhoseAuthorityChanges(t *testing.T) {
	in := input(t)
	entries := append([]tapeformat.Entry(nil), in.Accepted...)
	entries[1].AcceptedAt.Authority = contracts.TimeAuthority{
		Name:       "authority:somewhere-else",
		Provenance: contracts.ClockLocal,
	}
	in.Accepted = entries

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if out.Accepted {
		t.Fatal("a tape spanning two incomparable clocks was accepted")
	}
	if !strings.Contains(out.Reason, "not comparable") {
		t.Errorf("the refusal %q does not say the clocks are not comparable", out.Reason)
	}
	if strings.Contains(out.Reason, "regress") {
		t.Errorf("two incomparable clocks were reported as a regression: %s", out.Reason)
	}
	// And it names BOTH authorities, or nobody can tell which acceptor drifted.
	for _, want := range []string{"authority:acceptor", "authority:somewhere-else"} {
		if !strings.Contains(out.Reason, want) {
			t.Errorf("the refusal %q does not name %q", out.Reason, want)
		}
	}
}

// ⚠️ NOT-BEFORE, NOT STRICTLY-AFTER. Two acts accepted in the same instant are
// ordered by their positions on the tape; refusing equal times would refuse a
// correct tape whose clock is coarser than its acceptance rate.
//
// ⚠️ THE FOURTH INSTANCE OF THE PHRASE-ABSENCE DEFECT, and the one nobody found
// by reading. Three were named in review; the mechanical scan in
// allowpath_test.go found this one on its first run.
func TestReduce_AcceptsEqualTimesAtAdjacentPositions(t *testing.T) {
	in := input(t)
	entries := append([]tapeformat.Entry(nil), in.Accepted...)
	entries[1].AcceptedAt = entries[0].AcceptedAt
	in.Accepted = entries

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("a tape with two acts accepted in the same instant was REFUSED: %s", out.Reason)
	}
}

// A tape whose times run forwards is ACCEPTED, or the time rule is
// indistinguishable from refusing every tape with more than one entry.
func TestReduce_AcceptsATapeWhoseTimesRunForwards(t *testing.T) {
	out, err := reduce.Reduce(context.Background(), input(t))
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("a tape whose times run forwards was REFUSED: %s", out.Reason)
	}
}

// observing builds an independent observation reporting the tree act binds — the
// evidence an act needs before the tree rule will let it through.
func observing(t *testing.T, act contracts.ActSpec) contracts.EvidenceEvent {
	t.Helper()
	ev := read[contracts.EvidenceEvent](t, "candidates/tree-swap-observation.json")
	ev.ActBodyID = act.ActBodyID
	ev.Detail[reduce.ObservedTreeKey] = act.CandidateTreeOID
	return ev
}

func rolledBack(t *testing.T) reduce.Input {
	t.Helper()
	in := input(t)
	entries := append([]tapeformat.Entry(nil), in.Accepted...)
	entries[1].AcceptedAt = contracts.AcceptedTime{
		At:        entries[0].AcceptedAt.At.Add(-time.Hour),
		Authority: entries[0].AcceptedAt.Authority,
	}
	in.Accepted = entries
	return in
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

// ⭐ ONLY AN OBSERVATION COUNTS. A receipt is the host's OWN record of what it
// did; an observation is what an independent observer saw. Reconciling a host's
// claim against itself is not reconciliation, and the tree gate rests entirely
// on the comparison being against a view the acting party did not write.
//
// ⚠️ The rule previously read the tree key from ANY evidence event. The fixture
// happens to be an observation, so every existing test passed — the defect was
// invisible to the whole suite.
func TestReduce_ReadsTheObservedTreeOnlyFromAnObservation(t *testing.T) {
	for _, kind := range []contracts.EvidenceEventKind{
		contracts.EventReceipt,
		contracts.EventAttempt,
		contracts.EventAcceptance,
		contracts.EventViolation,
	} {
		t.Run(string(kind), func(t *testing.T) {
			candidate := read[contracts.ActSpec](t, "candidates/tree-swap.json")
			swap := read[contracts.EvidenceEvent](t, "candidates/tree-swap-observation.json")
			swap.EventKind = kind // a MISMATCHING tree, reported by the wrong kind

			in := input(t)
			in.Candidate = &candidate
			in.Observations = []contracts.EvidenceEvent{swap}

			out, err := reduce.Reduce(context.Background(), in)
			if err != nil {
				t.Fatalf("Reduce: %v", err)
			}
			if out.Accepted {
				t.Fatal("an act with no independent observation was ACCEPTED")
			}
			// ⚠️ ASSERT THE REASON. Both outcomes here are refusals, and a test
			// checking only that the act was refused would pass on the swap
			// verdict — which is the answer that must NOT be given, because it
			// would mean the host's own record had satisfied the gate.
			if strings.Contains(out.Reason, "not the effect that was authorised") {
				t.Errorf("a %s satisfied the tree gate — the host's own record is not independent evidence: %s",
					kind, out.Reason)
			}
			if !strings.Contains(out.Reason, "no observation reports a tree") {
				t.Errorf("refused for the wrong reason; an act evidenced only by a %s is UNOBSERVED: %s",
					kind, out.Reason)
			}
		})
	}
}

// The mirror: an observation carrying a matching tree still admits the act, so
// the kind check cannot be satisfied by refusing everything.
func TestReduce_AcceptsWhenTheMatchingTreeComesFromAnObservation(t *testing.T) {
	candidate := read[contracts.ActSpec](t, "candidates/tree-swap.json")
	ev := observing(t, candidate)
	ev.EventKind = contracts.EventObservation

	in := input(t)
	in.Candidate = &candidate
	in.Observations = []contracts.EvidenceEvent{ev}

	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("a matching observation did not admit the act: %s", out.Reason)
	}
}
