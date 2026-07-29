package codeci_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codeci"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// TestKnownCapabilitiesIsAClosedCopy: a caller must not be able to narrow or
// extend the class's vocabulary by mutating what it was handed.
func TestKnownCapabilitiesIsAClosedCopy(t *testing.T) {
	got := codeci.KnownCapabilities()
	if !got.Has(codeci.CapCIControl) {
		t.Fatalf("KnownCapabilities() = %v, missing %q", got, codeci.CapCIControl)
	}
	got[0] = connector.Capability("mutated")
	if codeci.KnownCapabilities().Has(connector.Capability("mutated")) {
		t.Fatalf("KnownCapabilities() handed out its backing array")
	}
}

// TestPRStateVocabulary covers Valid/String/UsableAsFilter, and pins that the
// zero value is a real value (Valid) but not a usable filter — the same
// asymmetry codehost.MRState and roster.Completeness both rely on.
func TestPRStateVocabulary(t *testing.T) {
	for _, s := range codeci.PRStates() {
		if !s.Valid() {
			t.Fatalf("%v is a vocabulary member but not Valid", s)
		}
		if name := s.String(); name == "" || strings.HasPrefix(name, "invalid_") {
			t.Fatalf("%d renders as %q, which is not a real name", int(s), name)
		}
	}
	if !codeci.PRStateUnspecified.Valid() {
		t.Fatalf("PRStateUnspecified must be a valid VALUE")
	}
	if codeci.PRStateUnspecified.UsableAsFilter() {
		t.Fatalf("PRStateUnspecified must not be usable as a filter")
	}
	for _, s := range []codeci.PRState{codeci.PRStateOpen, codeci.PRStateClosed, codeci.PRStateMerged, codeci.PRStateAny} {
		if !s.UsableAsFilter() {
			t.Fatalf("%v must be usable as a filter", s)
		}
	}
	bogus := codeci.PRState(99)
	if bogus.Valid() || bogus.UsableAsFilter() {
		t.Fatalf("an out-of-vocabulary PRState must be neither Valid nor usable as a filter")
	}
	if got := bogus.String(); got != "invalid_pr_state(99)" {
		t.Fatalf("PRState(99).String() = %q", got)
	}
}

// TestMergeMethodVocabulary covers Valid/String/Specified. Specified is what
// MergePR gates on: MergeMethodUnspecified must be refused as an argument
// even though it is a valid enum VALUE.
func TestMergeMethodVocabulary(t *testing.T) {
	for _, m := range codeci.MergeMethods() {
		if !m.Valid() {
			t.Fatalf("%v is a vocabulary member but not Valid", m)
		}
	}
	if !codeci.MergeMethodUnspecified.Valid() {
		t.Fatalf("MergeMethodUnspecified must be a valid VALUE")
	}
	if codeci.MergeMethodUnspecified.Specified() {
		t.Fatalf("MergeMethodUnspecified must not be Specified")
	}
	for _, m := range []codeci.MergeMethod{codeci.MergeMethodMerge, codeci.MergeMethodSquash, codeci.MergeMethodRebase} {
		if !m.Specified() {
			t.Fatalf("%v must be Specified", m)
		}
	}
	bogus := codeci.MergeMethod(99)
	if bogus.Valid() || bogus.Specified() {
		t.Fatalf("an out-of-vocabulary MergeMethod must be neither Valid nor Specified")
	}
	if got := bogus.String(); got != "invalid_merge_method(99)" {
		t.Fatalf("MergeMethod(99).String() = %q", got)
	}
}

// TestFileStatusAndRunStatusVocabulariesAreClosed covers the two remaining
// enums the same way: Valid/String derive from one map, and an out-of-range
// value cannot claim a real name.
func TestFileStatusAndRunStatusVocabulariesAreClosed(t *testing.T) {
	for _, s := range codeci.FileStatuses() {
		if !s.Valid() {
			t.Fatalf("FileStatus %v is a vocabulary member but not Valid", s)
		}
	}
	if got := codeci.FileStatus(99).String(); got != "invalid_file_status(99)" {
		t.Fatalf("FileStatus(99).String() = %q", got)
	}

	seen := map[string]bool{}
	for _, s := range codeci.RunStatuses() {
		if !s.Valid() {
			t.Fatalf("RunStatus %v is a vocabulary member but not Valid", s)
		}
		if seen[s.String()] {
			t.Fatalf("RunStatus %v renders the same as another status: %q", s, s.String())
		}
		seen[s.String()] = true
	}
	if got := codeci.RunStatus(99).String(); got != "invalid_run_status(99)" {
		t.Fatalf("RunStatus(99).String() = %q", got)
	}
}

// TestVocabulariesAreSortedAndCopied pins the same discipline
// connector.Classes() and roster.PrincipalKinds() already pin: a caller
// cannot narrow or extend a closed vocabulary by mutating what it was
// handed, and the order is stable.
func TestVocabulariesAreSortedAndCopied(t *testing.T) {
	got := codeci.PRStates()
	if !slices.IsSorted(got) {
		t.Fatalf("PRStates() = %v, want a stable sorted order", got)
	}
	got[0] = codeci.PRState(-1)
	if slices.Contains(codeci.PRStates(), codeci.PRState(-1)) {
		t.Fatalf("PRStates() handed out its backing array")
	}
}

// fakeCodeCI is a minimal, in-memory CodeCI + CIController implementation —
// compile-time proof that the ABC is satisfiable by an ordinary struct, and a
// shared fixture for codeciconform's own tests.
type fakeCodeCI struct {
	caps connector.Capabilities

	prs         map[string]codeci.PR
	diffs       map[string][]codeci.DiffFile
	branches    []codeci.Branch
	checkRuns   map[string][]codeci.CheckRun
	workflowRun map[string]codeci.WorkflowRun

	merged map[string]bool
}

func (f *fakeCodeCI) Implements() connector.Class          { return connector.ClassCodeCI }
func (f *fakeCodeCI) Capabilities() connector.Capabilities { return f.caps }
func (f *fakeCodeCI) Health(context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}
func (f *fakeCodeCI) Close() error { return nil }

func (f *fakeCodeCI) CreatePR(_ context.Context, fullName, branch, base, title, body string) (codeci.PR, error) {
	p := codeci.PR{ID: "new", FullName: fullName, Branch: branch, BaseBranch: base, Title: title, Body: body, State: codeci.PRStateOpen}
	f.prs[p.ID] = p
	return p, nil
}

func (f *fakeCodeCI) ListPRs(_ context.Context, _ string, state codeci.PRState) (codeci.Resolution[codeci.PR], error) {
	if !state.UsableAsFilter() {
		return codeci.Resolution[codeci.PR]{}, cerr.New(cerr.KindInvalid, "ListPRs", errors.New("state is not usable as a filter"))
	}
	var out []codeci.PR
	for _, p := range f.prs {
		if state == codeci.PRStateAny || p.State == state {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return codeci.EmptyList[codeci.PR](), nil
	}
	return codeci.Resolved(out, codeci.Complete)
}

func (f *fakeCodeCI) MergePR(_ context.Context, _, prID string, method codeci.MergeMethod) error {
	if !method.Specified() {
		return cerr.New(cerr.KindInvalid, "MergePR", errors.New("method is not Specified"))
	}
	p, ok := f.prs[prID]
	if !ok {
		return cerr.New(cerr.KindNotFound, "MergePR", errors.New("no such PR"))
	}
	if p.Draft {
		return cerr.New(cerr.KindInvalid, "MergePR", errors.New("refusing to merge a draft"))
	}
	f.merged[prID] = true
	return nil
}

func (f *fakeCodeCI) GetDiff(_ context.Context, _, prID string) (codeci.Resolution[codeci.DiffFile], error) {
	files, ok := f.diffs[prID]
	if !ok || len(files) == 0 {
		return codeci.Resolution[codeci.DiffFile]{}, cerr.New(cerr.KindNotFound, "GetDiff", errors.New("no such PR"))
	}
	return codeci.Resolved(files, codeci.Complete)
}

func (f *fakeCodeCI) ListBranches(context.Context, string) (codeci.Resolution[codeci.Branch], error) {
	if len(f.branches) == 0 {
		return codeci.EmptyList[codeci.Branch](), nil
	}
	return codeci.Resolved(f.branches, codeci.Complete)
}

func (f *fakeCodeCI) GetCheckRuns(_ context.Context, _, ref string) (codeci.Resolution[codeci.CheckRun], error) {
	runs, ok := f.checkRuns[ref]
	if !ok || len(runs) == 0 {
		return codeci.EmptyList[codeci.CheckRun](), nil
	}
	return codeci.Resolved(runs, codeci.Complete)
}

func (f *fakeCodeCI) GetWorkflowRun(_ context.Context, _, runID string) (codeci.WorkflowRun, error) {
	wr, ok := f.workflowRun[runID]
	if !ok {
		return codeci.WorkflowRun{}, cerr.New(cerr.KindNotFound, "GetWorkflowRun", errors.New("no such run"))
	}
	return wr, nil
}

func (f *fakeCodeCI) RerunWorkflow(context.Context, string, string) error  { return nil }
func (f *fakeCodeCI) CancelWorkflow(context.Context, string, string) error { return nil }

var (
	_ codeci.CodeCI       = (*fakeCodeCI)(nil)
	_ codeci.CIController = (*fakeCodeCI)(nil)
)

// TestFakeCodeCISatisfiesTheInterface is a smoke test over the fixture above,
// exercised directly (rather than only relied upon transitively by
// codeciconform) so a change to fakeCodeCI's behaviour that breaks its own
// documented contract is caught here first.
func TestFakeCodeCISatisfiesTheInterface(t *testing.T) {
	f := &fakeCodeCI{
		caps: connector.Capabilities{codeci.CapCIControl},
		prs:  map[string]codeci.PR{"1": {ID: "1", FullName: "o/r", State: codeci.PRStateOpen}},
		diffs: map[string][]codeci.DiffFile{
			"1": {{Path: "a.go", Status: codeci.FileStatusModified, Additions: 1}},
		},
		branches:    []codeci.Branch{{Name: "main", SHA: "deadbeef"}},
		checkRuns:   map[string][]codeci.CheckRun{"main": {{ID: "c1", Name: "build", Status: codeci.RunStatusSuccess}}},
		workflowRun: map[string]codeci.WorkflowRun{"w1": {ID: "w1", Status: codeci.RunStatusSuccess}},
		merged:      map[string]bool{},
	}
	ctx := context.Background()

	if err := f.MergePR(ctx, "o/r", "1", codeci.MergeMethodSquash); err != nil {
		t.Fatalf("MergePR: %v", err)
	}
	if !f.merged["1"] {
		t.Fatalf("MergePR did not record the merge")
	}
	if err := f.MergePR(ctx, "o/r", "1", codeci.MergeMethodUnspecified); err == nil {
		t.Fatalf("MergePR must refuse an unspecified method")
	}
}
