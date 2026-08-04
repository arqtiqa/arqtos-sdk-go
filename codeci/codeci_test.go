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

// TestIdentityCoherence pins the invariant WhoAmI's whole point rests on: an
// authenticated identity carrying no login is not a smaller answer, it is
// incoherent. A host's identity probe exists to assert "this host authenticated
// with no token in its environment", and an empty login falsifies exactly that
// assertion while still looking like a success.
//
// The reverse direction is pinned too: a login named while denying
// authentication says "I am nobody, and nobody is called octocat".
func TestIdentityCoherence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity codeci.Identity
		coherent bool
	}{
		{"authenticated with a login", codeci.Identity{Login: "placeholder-login", Authenticated: true}, true},
		{"authenticated with no login", codeci.Identity{Authenticated: true}, false},
		{"anonymous with no login", codeci.Identity{}, true},
		{"anonymous with a login", codeci.Identity{Login: "placeholder-login"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.identity.Coherent(); got != tc.coherent {
				t.Fatalf("Identity%+v.Coherent() = %v, want %v", tc.identity, got, tc.coherent)
			}
		})
	}
}

// TestCreatePRRequestValidateNamesEveryMissingField is the guard that makes
// the request STRUCT safe. Six positional strings force a caller to write ""
// on purpose; a struct lets one be forgotten silently, so CreatePR validates
// before it does anything else — the same discipline MergePR applies to
// MergeMethod, for the same reason.
func TestCreatePRRequestValidateNamesEveryMissingField(t *testing.T) {
	complete := codeci.CreatePRRequest{
		FullName: "placeholder-org/placeholder-repo",
		Branch:   "topic",
		Base:     "main",
		Title:    "a title",
	}
	if err := complete.Validate(); err != nil {
		t.Fatalf("a complete request was refused: %v; Body and Draft are both optional", err)
	}
	withBodyAndDraft := complete
	withBodyAndDraft.Body = "prose"
	withBodyAndDraft.Draft = true
	if err := withBodyAndDraft.Validate(); err != nil {
		t.Fatalf("a complete DRAFT request was refused: %v", err)
	}

	for _, tc := range []struct {
		field string
		blank func(*codeci.CreatePRRequest)
	}{
		{"full_name", func(r *codeci.CreatePRRequest) { r.FullName = "" }},
		{"branch", func(r *codeci.CreatePRRequest) { r.Branch = "" }},
		{"base", func(r *codeci.CreatePRRequest) { r.Base = "" }},
		{"title", func(r *codeci.CreatePRRequest) { r.Title = "" }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			req := complete
			tc.blank(&req)
			err := req.Validate()
			if err == nil {
				t.Fatalf("a request with %s unset validated; a zero field in a struct is exactly what a positional signature could not hide", tc.field)
			}
			if got := cerr.KindOf(err); got != cerr.KindInvalid {
				t.Fatalf("KindOf = %v, want %v: bad input, not an unreachable backend", got, cerr.KindInvalid)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("the refusal does not name %s, so a caller cannot tell which field it forgot: %v", tc.field, err)
			}
		})
	}

	// Every missing field is named at once, rather than one per round trip.
	err := codeci.CreatePRRequest{}.Validate()
	if err == nil {
		t.Fatalf("the zero request validated")
	}
	for _, field := range []string{"full_name", "branch", "base", "title"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("the zero request's refusal does not name %s: %v", field, err)
		}
	}
}

// fakeCodeCI is a minimal, in-memory CodeCI + CIController implementation —
// compile-time proof that the ABC is satisfiable by an ordinary struct, and a
// shared fixture for codeciconform's own tests.
type fakeCodeCI struct {
	caps  connector.Capabilities
	login string

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

func (f *fakeCodeCI) WhoAmI(context.Context) (codeci.Identity, error) {
	if f.login == "" {
		return codeci.Identity{}, cerr.New(cerr.KindUnauthorized, "WhoAmI", errors.New("no credential"))
	}
	return codeci.Identity{Login: f.login, Authenticated: true}, nil
}

func (f *fakeCodeCI) CreatePR(_ context.Context, req codeci.CreatePRRequest) (codeci.PR, error) {
	if err := req.Validate(); err != nil {
		return codeci.PR{}, err
	}
	p := codeci.PR{
		ID: "new", FullName: req.FullName, Branch: req.Branch, BaseBranch: req.Base,
		Title: req.Title, Body: req.Body, State: codeci.PRStateOpen, Draft: req.Draft,
		URL: "https://code.example/" + req.FullName + "/pull/new",
	}
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
	f := newFakeCodeCI()
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

func newFakeCodeCI() *fakeCodeCI {
	return &fakeCodeCI{
		caps:  connector.Capabilities{codeci.CapCIControl},
		login: "placeholder-login",
		prs:   map[string]codeci.PR{"1": {ID: "1", FullName: "o/r", State: codeci.PRStateOpen, URL: "https://code.example/o/r/pull/1"}},
		diffs: map[string][]codeci.DiffFile{
			"1": {{Path: "a.go", Status: codeci.FileStatusModified, Additions: 1}},
		},
		branches:    []codeci.Branch{{Name: "main", SHA: "deadbeef", Protected: true}, {Name: "topic", SHA: "cafef00d"}},
		checkRuns:   map[string][]codeci.CheckRun{"main": {{ID: "c1", Name: "build", Status: codeci.RunStatusSuccess}}},
		workflowRun: map[string]codeci.WorkflowRun{"w1": {ID: "w1", Status: codeci.RunStatusSuccess}},
		merged:      map[string]bool{},
	}
}

// TestCreatePRCanProduceEveryDraftStateItReads closes issue #46's second gap in
// one assertion: before this, the contract could READ PR.Draft
// and REFUSE to merge a draft, but had no way to open one — it observed and
// gated a state it could not create. Opening a draft and then having MergePR
// refuse that very pull request is the round trip that proves the gap closed.
func TestCreatePRCanProduceEveryDraftStateItReads(t *testing.T) {
	f := newFakeCodeCI()
	ctx := context.Background()

	pr, err := f.CreatePR(ctx, codeci.CreatePRRequest{
		FullName: "o/r", Branch: "topic", Base: "main", Title: "a title", Draft: true,
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if !pr.Draft {
		t.Fatalf("CreatePR(Draft: true) returned a pull request the host does not hold as a draft: %+v", pr)
	}
	if pr.URL == "" {
		t.Fatalf("the created pull request carries no URL, so a caller must assemble a vendor URL itself: %+v", pr)
	}
	if err := f.MergePR(ctx, "o/r", pr.ID, codeci.MergeMethodMerge); err == nil {
		t.Fatalf("MergePR merged a draft this contract has now opened itself")
	}
	if f.merged[pr.ID] {
		t.Fatalf("MergePR recorded a merge for a draft")
	}

	notDraft, err := f.CreatePR(ctx, codeci.CreatePRRequest{
		FullName: "o/r", Branch: "topic2", Base: "main", Title: "a title",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if notDraft.Draft {
		t.Fatalf("CreatePR with Draft unset opened a draft; the zero value must mean ready-for-review: %+v", notDraft)
	}
}

// TestCreatePRRefusesAnIncompleteRequestBeforeOpeningAnything: the fake is the
// reference implementation of the obligation the contract states, so it is
// asserted here rather than only in codeciconform.
func TestCreatePRRefusesAnIncompleteRequestBeforeOpeningAnything(t *testing.T) {
	f := newFakeCodeCI()
	before := len(f.prs)
	if _, err := f.CreatePR(context.Background(), codeci.CreatePRRequest{FullName: "o/r", Base: "main"}); err == nil {
		t.Fatalf("CreatePR opened a pull request from an incomplete request")
	} else if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("KindOf = %v, want %v", cerr.KindOf(err), cerr.KindInvalid)
	}
	if len(f.prs) != before {
		t.Fatalf("CreatePR recorded a pull request while refusing the request")
	}
}

// TestBranchProtectionIsReportedNotAssumed: always-false is the one always-zero
// field in this package that is actively dangerous, because it
// reads as "this branch is unprotected". A fixture carrying one protected and
// one unprotected branch is what makes the field falsifiable in both
// directions.
func TestBranchProtectionIsReportedNotAssumed(t *testing.T) {
	f := newFakeCodeCI()
	res, err := f.ListBranches(context.Background(), "o/r")
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	items, err := res.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	got := map[string]bool{}
	for _, b := range items {
		got[b.Name] = b.Protected
	}
	if !got["main"] {
		t.Errorf("the protected branch reports Protected=false, which reads as an unprotected branch")
	}
	if got["topic"] {
		t.Errorf("the unprotected branch reports Protected=true; a constant true is as much a lie as a constant false")
	}
}
