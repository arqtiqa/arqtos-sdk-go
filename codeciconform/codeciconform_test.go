package codeciconform_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codeci"
	"github.com/arqtiqa/arqtos-sdk-go/codeciconform"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

// The fixtures every run in this file is driven with, matching
// codeciconform.Options's required fields.
const (
	fixtureRepo              = "placeholder-org/populated-repo"
	fixtureUnknownRepo       = "placeholder-org/absent-repo"
	fixtureCheckedRef        = "main"
	fixtureDiffPR            = "1"
	fixtureUnknownPR         = "999"
	fixtureDraftPR           = "2"
	fixtureOpenPR            = "3"
	fixtureProtectedBranch   = "main"
	fixtureUnprotectedBranch = "topic"
	fixtureLogin             = "placeholder-login"
	fixturePRURL             = "https://code.example/placeholder-org/populated-repo/pull/3"

	fixtureOpenIssue    = 10
	fixtureClosedIssue  = 11
	fixtureUnknownIssue = 999999
)

// stub is a codeci.CodeCI that passes every check, with one override hook per
// method a test needs to break. A harness only ever run against compliant
// input proves nothing about what it would catch, so every check below is
// driven by a stub deliberately built to violate exactly the property it
// checks.
type stub struct {
	class connector.Class
	caps  connector.Capabilities

	createPR     func(ctx context.Context, req codeci.CreatePRRequest) (codeci.PR, error)
	listPRs      func(ctx context.Context, fullName string, state codeci.PRState) (codeci.Resolution[codeci.PR], error)
	mergePR      func(ctx context.Context, fullName, prID string, method codeci.MergeMethod) error
	getPR        func(ctx context.Context, fullName, prID string) (codeci.PR, error)
	commentPR    func(ctx context.Context, fullName, prID, body string) (string, error)
	getDiff      func(ctx context.Context, fullName, prID string) (codeci.Resolution[codeci.DiffFile], error)
	listBranches func(ctx context.Context, fullName string) (codeci.Resolution[codeci.Branch], error)
	getCheckRuns func(ctx context.Context, fullName, ref string) (codeci.Resolution[codeci.CheckRun], error)
	whoAmI       func(ctx context.Context) (codeci.Identity, error)
	health       func(ctx context.Context) (connector.Health, error)
	getIssue     func(ctx context.Context, fullName string, number int) (codeci.Issue, error)
	closeIssue   func(ctx context.Context, fullName string, number int, reason codeci.CloseReason) error
}

// newStub returns a bare *stub: it implements only the required CodeCI
// interface, never codeci.CIController, and declares no capabilities by
// default — so a test that does not care about CapCIControl gets a
// consistent (not-declared, not-implemented) baseline rather than an
// incidental capability-honesty or optional-declared failure alongside the
// one property it means to break.
func newStub() *stub {
	return &stub{class: connector.ClassCodeCI, caps: connector.Capabilities{}}
}

func (s *stub) Implements() connector.Class          { return s.class }
func (s *stub) Capabilities() connector.Capabilities { return s.caps }
func (s *stub) Close() error                         { return nil }
func (s *stub) Health(ctx context.Context) (connector.Health, error) {
	if s.health != nil {
		return s.health(ctx)
	}
	return connector.Health{Status: connector.Healthy}, nil
}

func (s *stub) CreatePR(ctx context.Context, req codeci.CreatePRRequest) (codeci.PR, error) {
	if s.createPR != nil {
		return s.createPR(ctx, req)
	}
	if err := req.Validate(); err != nil {
		return codeci.PR{}, err
	}
	return codeci.PR{
		ID: "new", FullName: req.FullName, Branch: req.Branch, BaseBranch: req.Base,
		Title: req.Title, Body: req.Body, Draft: req.Draft, State: codeci.PRStateOpen,
		URL: fixturePRURL,
	}, nil
}

func (s *stub) WhoAmI(ctx context.Context) (codeci.Identity, error) {
	if s.whoAmI != nil {
		return s.whoAmI(ctx)
	}
	return codeci.Identity{Login: fixtureLogin, Authenticated: true}, nil
}

func (s *stub) ListPRs(ctx context.Context, fullName string, state codeci.PRState) (codeci.Resolution[codeci.PR], error) {
	if s.listPRs != nil {
		return s.listPRs(ctx, fullName, state)
	}
	if !state.UsableAsFilter() {
		return codeci.Resolution[codeci.PR]{}, cerr.New(cerr.KindInvalid, "ListPRs", errors.New("state is not usable as a filter"))
	}
	if fullName != fixtureRepo {
		return codeci.Resolution[codeci.PR]{}, cerr.New(cerr.KindNotFound, "ListPRs", nil)
	}
	// ⚠️ The draft fixture is listed HERE as well as merged against. Before
	// prs/draft-is-reported existed, this list carried only the open PR while
	// MergePR was driven against the draft one — so the stub's own list and its
	// own merge target disagreed about what is on the repository, and nothing
	// noticed.
	return codeci.Resolved([]codeci.PR{
		{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen, URL: fixturePRURL},
		{ID: fixtureDraftPR, FullName: fullName, State: codeci.PRStateOpen, Draft: true, URL: fixturePRURL},
	}, codeci.Complete)
}

func (s *stub) MergePR(ctx context.Context, fullName, prID string, method codeci.MergeMethod) error {
	if s.mergePR != nil {
		return s.mergePR(ctx, fullName, prID, method)
	}
	if !method.Specified() {
		return cerr.New(cerr.KindInvalid, "MergePR", errors.New("method is not Specified"))
	}
	if prID == fixtureDraftPR {
		return cerr.New(cerr.KindInvalid, "MergePR", errors.New("refusing to merge a draft"))
	}
	return nil
}

func (s *stub) GetPR(ctx context.Context, fullName, prID string) (codeci.PR, error) {
	if s.getPR != nil {
		return s.getPR(ctx, fullName, prID)
	}
	if fullName != fixtureRepo {
		return codeci.PR{}, cerr.New(cerr.KindNotFound, "GetPR", errors.New("no such repo"))
	}
	// A typed failure, never a zero PR — the contract's own rule.
	return codeci.PR{}, cerr.New(cerr.KindNotFound, "GetPR", errors.New("no such PR"))
}

func (s *stub) CommentPR(ctx context.Context, fullName, prID, body string) (string, error) {
	if s.commentPR != nil {
		return s.commentPR(ctx, fullName, prID, body)
	}
	if body == "" {
		return "", cerr.New(cerr.KindInvalid, "CommentPR", errors.New("body is empty"))
	}
	return "comment-1", nil
}

func (s *stub) GetDiff(ctx context.Context, fullName, prID string) (codeci.Resolution[codeci.DiffFile], error) {
	if s.getDiff != nil {
		return s.getDiff(ctx, fullName, prID)
	}
	if fullName != fixtureRepo || prID != fixtureDiffPR {
		return codeci.Resolution[codeci.DiffFile]{}, cerr.New(cerr.KindNotFound, "GetDiff", nil)
	}
	return codeci.Resolved([]codeci.DiffFile{{Path: "a.go", Status: codeci.FileStatusModified, Additions: 1}}, codeci.Complete)
}

func (s *stub) ListBranches(ctx context.Context, fullName string) (codeci.Resolution[codeci.Branch], error) {
	if s.listBranches != nil {
		return s.listBranches(ctx, fullName)
	}
	return codeci.Resolved([]codeci.Branch{
		{Name: fixtureProtectedBranch, SHA: "deadbeef", Protected: true},
		{Name: fixtureUnprotectedBranch, SHA: "cafef00d"},
	}, codeci.Complete)
}

func (s *stub) GetCheckRuns(ctx context.Context, fullName, ref string) (codeci.Resolution[codeci.CheckRun], error) {
	if s.getCheckRuns != nil {
		return s.getCheckRuns(ctx, fullName, ref)
	}
	if ref != fixtureCheckedRef {
		return codeci.EmptyList[codeci.CheckRun](), nil
	}
	return codeci.Resolved([]codeci.CheckRun{{ID: "c1", Name: "build", Status: codeci.RunStatusSuccess}}, codeci.Complete)
}

func (s *stub) GetWorkflowRun(context.Context, string, string) (codeci.WorkflowRun, error) {
	return codeci.WorkflowRun{ID: "w1", Status: codeci.RunStatusSuccess}, nil
}

var _ codeci.CodeCI = (*stub)(nil)

// ciControllingStub implements the optional CapCIControl operation. It is a
// separate type because implementing it is what the harness type-asserts
// for.
type ciControllingStub struct{ *stub }

func (ciControllingStub) RerunWorkflow(context.Context, string, string) error  { return nil }
func (ciControllingStub) CancelWorkflow(context.Context, string, string) error { return nil }

var _ codeci.CIController = ciControllingStub{}

// checkPublishingStub implements the optional CapCheckPublish operation. It is
// a separate type because implementing it is what the harness type-asserts
// for — and because that assertion must stay independent of CIController.
type checkPublishingStub struct{ *stub }

func (checkPublishingStub) PublishCheck(context.Context, string, codeci.CheckPublication) (codeci.CheckRun, error) {
	return codeci.CheckRun{ID: "c1", Name: "arqtos/gate", Status: codeci.RunStatusSuccess}, nil
}

var _ codeci.CheckPublisher = checkPublishingStub{}

func stubManifest(caps ...connector.Capability) manifest.Doc {
	return manifest.Doc{
		Name:         "stub",
		Implements:   connector.ClassCodeCI,
		Kind:         manifest.KindNative,
		Capabilities: caps,
	}
}

// GetIssue is the compliant read: it answers for the two fixtures the harness
// names, echoes the address it was asked for, and fails — typed — for anything
// else. An unresolvable issue is never a state.
func (s *stub) GetIssue(ctx context.Context, fullName string, number int) (codeci.Issue, error) {
	if s.getIssue != nil {
		return s.getIssue(ctx, fullName, number)
	}
	if fullName != fixtureRepo {
		return codeci.Issue{}, cerr.New(cerr.KindNotFound, "GetIssue", errors.New("no such repository"))
	}
	switch number {
	case fixtureOpenIssue:
		return codeci.Issue{FullName: fullName, Number: number, State: codeci.IssueStateOpen, Title: "a placeholder title"}, nil
	case fixtureClosedIssue:
		return codeci.Issue{FullName: fullName, Number: number, State: codeci.IssueStateClosed}, nil
	}
	return codeci.Issue{}, cerr.New(cerr.KindNotFound, "GetIssue", errors.New("no such issue"))
}

// CloseIssue is the compliant write: it validates its reason BEFORE resolving
// the issue, and closing an already-closed issue is a success.
func (s *stub) CloseIssue(ctx context.Context, fullName string, number int, reason codeci.CloseReason) error {
	if s.closeIssue != nil {
		return s.closeIssue(ctx, fullName, number, reason)
	}
	if !reason.UsableAsReason() {
		return cerr.New(cerr.KindInvalid, "CloseIssue", errors.New("reason is not UsableAsReason"))
	}
	if fullName != fixtureRepo {
		return cerr.New(cerr.KindNotFound, "CloseIssue", errors.New("no such repository"))
	}
	switch number {
	case fixtureOpenIssue, fixtureClosedIssue:
		return nil
	}
	return cerr.New(cerr.KindNotFound, "CloseIssue", errors.New("no such issue"))
}

func baseOptions(m manifest.Doc) codeciconform.Options {
	return codeciconform.Options{
		Manifest:    m,
		Repo:        fixtureRepo,
		UnknownRepo: fixtureUnknownRepo,
		CheckedRef:  fixtureCheckedRef,
		DiffPR:      fixtureDiffPR,
		UnknownPR:   fixtureUnknownPR,
		DraftPR:     fixtureDraftPR,
		OpenPR:      fixtureOpenPR,

		ProtectedBranch:   fixtureProtectedBranch,
		UnprotectedBranch: fixtureUnprotectedBranch,

		OpenIssue:    fixtureOpenIssue,
		ClosedIssue:  fixtureClosedIssue,
		UnknownIssue: fixtureUnknownIssue,
	}
}

func run(t *testing.T, c codeci.CodeCI, m manifest.Doc) codeciconform.Report {
	t.Helper()
	rep, err := codeciconform.Run(context.Background(), c, baseOptions(m))
	if err != nil {
		t.Fatalf("the conformance run could not be carried out: %v", err)
	}
	return rep
}

func failed(rep codeciconform.Report, name string) (string, bool) {
	for _, res := range rep.Results {
		if res.Name == name {
			return res.Detail, !res.Pass
		}
	}
	return "", false
}

func requireFailed(t *testing.T, rep codeciconform.Report, name string) {
	t.Helper()
	detail, did := failed(rep, name)
	if !did {
		t.Fatalf("%s passed against a connector built to violate it\n%s", name, rep)
	}
	if detail == "" {
		t.Errorf("%s failed with no detail, so the report does not say what is wrong", name)
	}
}

func TestRun_CompliantStub_IsGreenOnEveryCheck(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codeci.CapCIControl}
	rep := run(t, ciControllingStub{s}, stubManifest(codeci.CapCIControl))
	if err := rep.Err(); err != nil {
		t.Fatalf("a compliant stub was reported non-conformant: %v\n%s", err, rep)
	}
	// Read out of the source rather than restated here. A hand-maintained list
	// is a list that drifts: this one had already lost CheckDraftIsReported, so
	// the assertion "every check ran" was quietly not asking about one of them.
	want := checkConstants(t)
	for _, name := range want {
		var present bool
		for _, res := range rep.Results {
			if res.Name == name {
				present = true
			}
		}
		if !present {
			t.Errorf("check %s did not run", name)
		}
	}
	t.Logf("%s", rep)
}

func TestRun_RefusesToRunWithoutFixtures(t *testing.T) {
	full := baseOptions(stubManifest())
	for _, field := range []string{
		"Repo", "UnknownRepo", "CheckedRef", "DiffPR", "UnknownPR", "DraftPR", "OpenPR",
		"ProtectedBranch", "UnprotectedBranch",
		"OpenIssue", "ClosedIssue", "UnknownIssue",
	} {
		t.Run(field, func(t *testing.T) {
			opts := full
			switch field {
			case "Repo":
				opts.Repo = ""
			case "UnknownRepo":
				opts.UnknownRepo = ""
			case "CheckedRef":
				opts.CheckedRef = ""
			case "DiffPR":
				opts.DiffPR = ""
			case "UnknownPR":
				opts.UnknownPR = ""
			case "DraftPR":
				opts.DraftPR = ""
			case "OpenPR":
				opts.OpenPR = ""
			case "ProtectedBranch":
				opts.ProtectedBranch = ""
			case "UnprotectedBranch":
				opts.UnprotectedBranch = ""
			case "OpenIssue":
				opts.OpenIssue = 0
			case "ClosedIssue":
				opts.ClosedIssue = 0
			case "UnknownIssue":
				opts.UnknownIssue = 0
			}
			if _, err := codeciconform.Run(context.Background(), newStub(), opts); err == nil {
				t.Fatalf("a run with %s unset was carried out; a check that cannot be driven must not be skipped", field)
			}
		})
	}
}

func TestRun_RefusesANilConnector(t *testing.T) {
	if _, err := codeciconform.Run(context.Background(), nil, baseOptions(stubManifest())); err == nil {
		t.Fatal("a run against no connector reported something")
	}
}

func TestRun_CatchesAManifestOutsideTheVocabulary(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{"ci-control"} // misspelt: the vocabulary says "ci_control"
	rep := run(t, s, stubManifest("ci-control"))
	requireFailed(t, rep, codeciconform.CheckManifest)
}

func TestRun_CatchesTheWrongClass(t *testing.T) {
	s := newStub()
	s.class = connector.ClassRoster
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckClass)
}

// TestRun_CatchesADeclarationTheRuntimeDoesNotKeep: the manifest declares
// CapCIControl; the bare stub's default (empty) Capabilities() does not
// report it.
func TestRun_CatchesADeclarationTheRuntimeDoesNotKeep(t *testing.T) {
	s := newStub() // caps stays empty: reports nothing
	rep := run(t, s, stubManifest(codeci.CapCIControl))
	requireFailed(t, rep, codeciconform.CheckCapabilityHonesty)
}

// TestRun_CatchesARuntimeCapabilityTheManifestDoesNotDeclare: the runtime
// reports CapCIControl; the manifest declares nothing.
func TestRun_CatchesARuntimeCapabilityTheManifestDoesNotDeclare(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codeci.CapCIControl}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckCapabilityHonesty)
}

// TestRun_CatchesAnOptionalOperationDeclaredButAbsent is the dangerous
// direction: a host that plans to retry a failed run calls into nothing.
func TestRun_CatchesAnOptionalOperationDeclaredButAbsent(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codeci.CapCIControl} // *stub does not implement CIController
	rep := run(t, s, stubManifest(codeci.CapCIControl))
	requireFailed(t, rep, codeciconform.CheckOptionalDeclared)
	detail, _ := failed(rep, codeciconform.CheckOptionalDeclared)
	if !strings.Contains(detail, string(codeci.CapCIControl)) {
		t.Errorf("the failure does not name the capability: %q", detail)
	}
}

// TestRun_CatchesAnOptionalOperationImplementedButUndeclared: a host reads
// the manifest before it loads the connector, so behaviour that is there and
// undeclared is behaviour nothing will ever call.
func TestRun_CatchesAnOptionalOperationImplementedButUndeclared(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{} // ciControllingStub implements CIController regardless
	rep := run(t, ciControllingStub{s}, stubManifest())
	requireFailed(t, rep, codeciconform.CheckOptionalDeclared)
}

func TestRun_AcceptsAnOptionalOperationDeclaredAndImplemented(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codeci.CapCIControl}
	rep := run(t, ciControllingStub{s}, stubManifest(codeci.CapCIControl))
	if err := rep.Err(); err != nil {
		t.Fatalf("a connector that declares and implements an optional operation was failed: %v\n%s", err, rep)
	}
}

// ⚠️ CapCheckPublish is a NEW optional tier, not a method on CIController.
// The declared-is-implemented check must cover it independently: a host that
// read only ci_control would plan to publish a check it cannot publish.
func TestRun_CatchesCheckPublishDeclaredButAbsent(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codeci.CapCheckPublish} // *stub does not implement CheckPublisher
	rep := run(t, s, stubManifest(codeci.CapCheckPublish))
	requireFailed(t, rep, codeciconform.CheckOptionalDeclared)
	detail, _ := failed(rep, codeciconform.CheckOptionalDeclared)
	if !strings.Contains(detail, string(codeci.CapCheckPublish)) {
		t.Errorf("the failure does not name the capability: %q", detail)
	}
}

func TestRun_CatchesCheckPublishImplementedButUndeclared(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{}
	rep := run(t, checkPublishingStub{s}, stubManifest())
	requireFailed(t, rep, codeciconform.CheckOptionalDeclared)
}

func TestRun_AcceptsCheckPublishDeclaredAndImplemented(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codeci.CapCheckPublish}
	rep := run(t, checkPublishingStub{s}, stubManifest(codeci.CapCheckPublish))
	if err := rep.Err(); err != nil {
		t.Fatalf("a connector that declares and implements check_publish was failed: %v\n%s", err, rep)
	}
}

// TestRun_CatchesAnEmptySuccess drives the shape this whole contract is built
// to refuse, across each of the four list operations in turn.
func TestRun_CatchesAnEmptySuccess(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*stub)
	}{
		{"ListPRs", func(s *stub) {
			s.listPRs = func(context.Context, string, codeci.PRState) (codeci.Resolution[codeci.PR], error) {
				return codeci.Resolution[codeci.PR]{}, nil
			}
		}},
		{"ListBranches", func(s *stub) {
			s.listBranches = func(context.Context, string) (codeci.Resolution[codeci.Branch], error) {
				return codeci.Resolution[codeci.Branch]{}, nil
			}
		}},
		{"GetCheckRuns", func(s *stub) {
			s.getCheckRuns = func(context.Context, string, string) (codeci.Resolution[codeci.CheckRun], error) {
				return codeci.Resolution[codeci.CheckRun]{}, nil
			}
		}},
		{"GetDiff", func(s *stub) {
			s.getDiff = func(context.Context, string, string) (codeci.Resolution[codeci.DiffFile], error) {
				return codeci.Resolution[codeci.DiffFile]{}, nil
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			tc.build(s)
			rep := run(t, s, stubManifest())
			requireFailed(t, rep, codeciconform.CheckListsNoEmptySuccess)
		})
	}
}

// TestRun_CatchesAnAssertedEmptyDiff covers GetDiff specifically: a real
// pull/merge request always has at least one changed file, so EmptyList is
// never legitimate there.
func TestRun_CatchesAnAssertedEmptyDiff(t *testing.T) {
	s := newStub()
	s.getDiff = func(context.Context, string, string) (codeci.Resolution[codeci.DiffFile], error) {
		return codeci.EmptyList[codeci.DiffFile](), nil
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckListsNoEmptySuccess)
}

func TestRun_CatchesAnUnclassifiedFailure(t *testing.T) {
	s := newStub()
	s.listPRs = func(_ context.Context, fullName string, state codeci.PRState) (codeci.Resolution[codeci.PR], error) {
		if fullName == fixtureRepo {
			return codeci.Resolved([]codeci.PR{{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen, URL: fixturePRURL}}, codeci.Complete)
		}
		return codeci.Resolution[codeci.PR]{}, errors.New("404 Not Found []") // vendor prose
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckListFailClosed)
}

// TestRun_CatchesAReadableResolutionBesideAFailure is the fail-open shape a
// classified error does not save you from.
func TestRun_CatchesAReadableResolutionBesideAFailure(t *testing.T) {
	s := newStub()
	s.getDiff = func(_ context.Context, fullName, prID string) (codeci.Resolution[codeci.DiffFile], error) {
		if prID == fixtureDiffPR {
			return codeci.Resolved([]codeci.DiffFile{{Path: "a.go"}}, codeci.Complete)
		}
		return codeci.EmptyList[codeci.DiffFile](), cerr.New(cerr.KindNotFound, "GetDiff", nil)
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckListFailClosed)
}

func TestRun_CatchesAConnectorThatNeverFails(t *testing.T) {
	s := newStub()
	s.listPRs = func(_ context.Context, fullName string, state codeci.PRState) (codeci.Resolution[codeci.PR], error) {
		return codeci.Resolved([]codeci.PR{{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen, URL: fixturePRURL}}, codeci.Complete)
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckListFailClosed)
}

// TestRun_CatchesAMergeThatIgnoresMethodValidation is the point of the
// merge-method obligation: a caller that forgot to set MergeMethod must not
// have its request silently accepted.
func TestRun_CatchesAMergeThatIgnoresMethodValidation(t *testing.T) {
	s := newStub()
	s.mergePR = func(context.Context, string, string, codeci.MergeMethod) error { return nil } // merges anything
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckMergeRefusesUnspecifiedMethod)
}

// TestRun_CatchesAMergeThatIgnoresDraftRefusal is issue #26's preserved
// guard, exercised against a connector that dropped it.
func TestRun_CatchesAMergeThatIgnoresDraftRefusal(t *testing.T) {
	s := newStub()
	s.mergePR = func(_ context.Context, _, _ string, method codeci.MergeMethod) error {
		if !method.Specified() {
			return cerr.New(cerr.KindInvalid, "MergePR", errors.New("method is not Specified"))
		}
		return nil // merges a draft
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckMergeRefusesDraft)
}

func TestRun_CatchesAnUnclassifiedMergeRefusal(t *testing.T) {
	s := newStub()
	s.mergePR = func(context.Context, string, string, codeci.MergeMethod) error {
		return errors.New("cannot merge") // vendor prose, not a cerr
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckMergeRefusesUnspecifiedMethod)
	requireFailed(t, rep, codeciconform.CheckMergeRefusesDraft)
}

// TestRun_CatchesACreateThatIgnoresRequestValidation is the falsifier for the
// options-struct shape of CreatePR. A struct lets a caller forget a field where
// six positional strings forced an explicit "", so the contract requires
// CreatePR to validate before it opens anything — and a connector that skips
// the validation opens a pull request from a half-filled request.
func TestRun_CatchesACreateThatIgnoresRequestValidation(t *testing.T) {
	s := newStub()
	s.createPR = func(_ context.Context, req codeci.CreatePRRequest) (codeci.PR, error) {
		return codeci.PR{ID: "opened-anyway", FullName: req.FullName, URL: fixturePRURL}, nil
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckCreateRefusesIncompleteRequest)
}

// TestRun_CatchesAnUnclassifiedCreateRefusal: refusing is not enough — a host
// acts on the classification, never on the message.
func TestRun_CatchesAnUnclassifiedCreateRefusal(t *testing.T) {
	s := newStub()
	s.createPR = func(context.Context, codeci.CreatePRRequest) (codeci.PR, error) {
		return codeci.PR{}, errors.New("422 Unprocessable Entity") // vendor prose, not a cerr
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckCreateRefusesIncompleteRequest)
}

// TestRun_CatchesBranchProtectionAssertedRatherThanReported drives both
// directions. always-false is the dangerous one —
// it reads as "this branch is unprotected" — but always-true is equally a
// constant masquerading as an answer, and a check that only asserted the
// protected branch would be satisfied by one.
func TestRun_CatchesBranchProtectionAssertedRatherThanReported(t *testing.T) {
	for _, tc := range []struct {
		name     string
		branches []codeci.Branch
	}{
		{"always false", []codeci.Branch{
			{Name: fixtureProtectedBranch, SHA: "deadbeef"},
			{Name: fixtureUnprotectedBranch, SHA: "cafef00d"},
		}},
		{"always true", []codeci.Branch{
			{Name: fixtureProtectedBranch, SHA: "deadbeef", Protected: true},
			{Name: fixtureUnprotectedBranch, SHA: "cafef00d", Protected: true},
		}},
		{"the protected fixture is not listed at all", []codeci.Branch{
			{Name: fixtureUnprotectedBranch, SHA: "cafef00d"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			s.listBranches = func(context.Context, string) (codeci.Resolution[codeci.Branch], error) {
				return codeci.Resolved(tc.branches, codeci.Complete)
			}
			rep := run(t, s, stubManifest())
			requireFailed(t, rep, codeciconform.CheckBranchProtectionReported)
		})
	}
}

// TestRun_CatchesAPRWithNoURL: PR.URL exists because a contract that forces
// every caller to assemble a vendor URL has pushed vendor knowledge back out to
// callers. An empty one puts it straight back.
func TestRun_CatchesAPRWithNoURL(t *testing.T) {
	s := newStub()
	s.listPRs = func(_ context.Context, fullName string, state codeci.PRState) (codeci.Resolution[codeci.PR], error) {
		if fullName != fixtureRepo {
			return codeci.Resolution[codeci.PR]{}, cerr.New(cerr.KindNotFound, "ListPRs", nil)
		}
		return codeci.Resolved([]codeci.PR{{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen}}, codeci.Complete)
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckPRsCarryAURL)
}

// TestRun_CatchesAnIdentityWithNoLogin is the check issue #46 demands by name.
// A host cites its identity probe as the standing proof that it authenticates
// with NO token in its environment; an empty login re-served as a success
// falsifies precisely that assertion while looking like an answer. It must be a
// typed failure, never a smaller answer — so the harness refuses the connector
// rather than the host discovering it in production.
func TestRun_CatchesAnIdentityWithNoLogin(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity codeci.Identity
	}{
		{"authenticated with no login", codeci.Identity{Authenticated: true}},
		{"neither authenticated nor named", codeci.Identity{}},
		{"a login while denying authentication", codeci.Identity{Login: fixtureLogin}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			s.whoAmI = func(context.Context) (codeci.Identity, error) { return tc.identity, nil }
			rep := run(t, s, stubManifest())
			requireFailed(t, rep, codeciconform.CheckIdentity)
			detail, _ := failed(rep, codeciconform.CheckIdentity)
			if !strings.Contains(detail, "login") {
				t.Errorf("the failure does not say the login is the problem: %q", detail)
			}
		})
	}
}

func TestRun_CatchesAnUnclassifiedIdentityFailure(t *testing.T) {
	s := newStub()
	s.whoAmI = func(context.Context) (codeci.Identity, error) {
		return codeci.Identity{}, errors.New("401 Bad credentials") // vendor prose, not a cerr
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckIdentity)
}

// TestRun_AcceptsAClassifiedIdentityFailure: a connector whose credential the
// code host rejected is unauthorised, not non-conformant. That is the shape the
// contract requires INSTEAD of an empty login.
func TestRun_AcceptsAClassifiedIdentityFailure(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codeci.CapCIControl}
	s.whoAmI = func(context.Context) (codeci.Identity, error) {
		return codeci.Identity{}, cerr.New(cerr.KindUnauthorized, "WhoAmI", nil)
	}
	rep := run(t, ciControllingStub{s}, stubManifest(codeci.CapCIControl))
	if err := rep.Err(); err != nil {
		t.Fatalf("a connector reporting a classified identity failure was called non-conformant: %v\n%s", err, rep)
	}
}

func TestRun_CatchesUnhealthyReporting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		health func(context.Context) (connector.Health, error)
	}{
		{"status outside the vocabulary", func(context.Context) (connector.Health, error) {
			return connector.Health{Status: connector.HealthStatus(42)}, nil
		}},
		{"unclassified failure", func(context.Context) (connector.Health, error) {
			return connector.Health{}, errors.New("could not reach the API")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			s.health = tc.health
			rep := run(t, s, stubManifest())
			requireFailed(t, rep, codeciconform.CheckHealth)
		})
	}
}

// TestRun_AcceptsAClassifiedHealthFailure: a connector whose backend is down
// is unreachable, not non-conformant.
func TestRun_AcceptsAClassifiedHealthFailure(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codeci.CapCIControl}
	s.health = func(context.Context) (connector.Health, error) {
		return connector.Health{}, cerr.New(cerr.KindUnavailable, "Health", nil)
	}
	rep := run(t, ciControllingStub{s}, stubManifest(codeci.CapCIControl))
	if err := rep.Err(); err != nil {
		t.Fatalf("a connector reporting a classified health failure was called non-conformant: %v\n%s", err, rep)
	}
}

// violators is one connector per check, each built to violate exactly that
// check and nothing else. It is the machine-readable form of this package's
// own claim — "every check in this package's test suite is driven by a
// connector deliberately built to violate the property it checks" — which was
// prose only until now, and prose cannot fail.
//
// The tests above are not redundant with this table: they carry the reasoning
// for each violation and cover more than one shape of it. What this table adds
// is COVERAGE — that no check exists without one.
var violators = map[string]func() (codeci.CodeCI, manifest.Doc){
	// Reports the OPEN fixture as IssueStateUnspecified — the always-zero shape,
	// a connector that returned a success without ever looking.
	//
	// It leaves the CLOSED fixture correct on purpose: issues/close-is-idempotent
	// reads that issue as its safety interlock before writing, so breaking both
	// would take that check down too and make the attribution meaningless.
	codeciconform.CheckIssueStateReported: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.getIssue = func(_ context.Context, fullName string, number int) (codeci.Issue, error) {
			switch number {
			case fixtureOpenIssue:
				return codeci.Issue{FullName: fullName, Number: number}, nil // State left unset
			case fixtureClosedIssue:
				return codeci.Issue{FullName: fullName, Number: number, State: codeci.IssueStateClosed}, nil
			}
			return codeci.Issue{}, cerr.New(cerr.KindNotFound, "GetIssue", errors.New("no such issue"))
		}
		return s, stubManifest()
	},
	// Answers CLOSED for an issue that does not exist: the exact reading that
	// tells an operator a blocker is resolved and the work can start. Both real
	// fixtures stay correct, so it breaks the unresolvable check alone.
	codeciconform.CheckIssueUnresolvableIsAFailure: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.getIssue = func(_ context.Context, fullName string, number int) (codeci.Issue, error) {
			state := codeci.IssueStateClosed
			if number == fixtureOpenIssue {
				state = codeci.IssueStateOpen
			}
			return codeci.Issue{FullName: fullName, Number: number, State: state}, nil
		}
		return s, stubManifest()
	},
	// Resolves the issue BEFORE validating the reason, so the zero reason comes
	// back as NotFound instead of Invalid — the guard ran second, which is the
	// ordering this check exists to detect. It still refuses the close, so it
	// breaks nothing else.
	codeciconform.CheckCloseRefusesUnspecifiedReason: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.closeIssue = func(_ context.Context, fullName string, number int, reason codeci.CloseReason) error {
			if fullName != fixtureRepo || (number != fixtureOpenIssue && number != fixtureClosedIssue) {
				return cerr.New(cerr.KindNotFound, "CloseIssue", errors.New("no such issue"))
			}
			if !reason.UsableAsReason() {
				return cerr.New(cerr.KindInvalid, "CloseIssue", errors.New("reason is not UsableAsReason"))
			}
			return nil
		}
		return s, stubManifest()
	},
	// Fails when asked to close an issue that is already closed. It keeps the
	// reason guard, and keeps ordering it first, so the refusal check still
	// passes and this stub breaks idempotency alone.
	codeciconform.CheckCloseIsIdempotent: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.closeIssue = func(_ context.Context, _ string, number int, reason codeci.CloseReason) error {
			if !reason.UsableAsReason() {
				return cerr.New(cerr.KindInvalid, "CloseIssue", errors.New("reason is not UsableAsReason"))
			}
			if number == fixtureClosedIssue {
				return cerr.New(cerr.KindInvalid, "CloseIssue", errors.New("issue is already closed"))
			}
			return nil
		}
		return s, stubManifest()
	},
	// Reports the draft fixture as an ordinary PR — the always-false shape.
	// Everything else about it is compliant, so it breaks prs/draft-is-reported
	// and nothing else.
	codeciconform.CheckDraftIsReported: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.listPRs = func(_ context.Context, fullName string, state codeci.PRState) (codeci.Resolution[codeci.PR], error) {
			if !state.UsableAsFilter() {
				return codeci.Resolution[codeci.PR]{}, cerr.New(cerr.KindInvalid, "ListPRs", errors.New("state is not usable as a filter"))
			}
			if fullName != fixtureRepo {
				return codeci.Resolution[codeci.PR]{}, cerr.New(cerr.KindNotFound, "ListPRs", nil)
			}
			return codeci.Resolved([]codeci.PR{
				{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen, URL: fixturePRURL},
				{ID: fixtureDraftPR, FullName: fullName, State: codeci.PRStateOpen, Draft: false, URL: fixturePRURL},
			}, codeci.Complete)
		}
		return s, stubManifest()
	},
	codeciconform.CheckManifest: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.caps = connector.Capabilities{"ci-control"} // misspelt: the vocabulary says "ci_control"
		return s, stubManifest("ci-control")
	},
	codeciconform.CheckClass: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.class = connector.ClassRoster
		return s, stubManifest()
	},
	codeciconform.CheckCapabilityHonesty: func() (codeci.CodeCI, manifest.Doc) {
		return newStub(), stubManifest(codeci.CapCIControl) // declared, never reported
	},
	codeciconform.CheckOptionalDeclared: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.caps = connector.Capabilities{codeci.CapCIControl} // *stub does not implement CIController
		return s, stubManifest(codeci.CapCIControl)
	},
	codeciconform.CheckListsNoEmptySuccess: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		// GetCheckRuns, rather than one of the other three lists: no other
		// check reads its result, so this stub breaks exactly one check. A
		// ListBranches or ListPRs violation would take the branch-protection or
		// PR-URL check down with it and make the attribution below meaningless.
		s.getCheckRuns = func(context.Context, string, string) (codeci.Resolution[codeci.CheckRun], error) {
			return codeci.Resolution[codeci.CheckRun]{}, nil
		}
		return s, stubManifest()
	},
	codeciconform.CheckListFailClosed: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.listPRs = func(_ context.Context, fullName string, _ codeci.PRState) (codeci.Resolution[codeci.PR], error) {
			// Carries the draft fixture so this stub breaks fail-closed and
			// NOTHING else — prs/draft-is-reported reads the same list.
			return codeci.Resolved([]codeci.PR{
				{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen, URL: fixturePRURL},
				{ID: fixtureDraftPR, FullName: fullName, State: codeci.PRStateOpen, Draft: true, URL: fixturePRURL},
			}, codeci.Complete)
		}
		return s, stubManifest()
	},
	codeciconform.CheckMergeRefusesUnspecifiedMethod: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		// Keeps the draft refusal, so this stub drops the method validation and
		// nothing else. A mergePR that returned nil unconditionally would fail
		// both merge checks at once.
		s.mergePR = func(_ context.Context, _, prID string, _ codeci.MergeMethod) error {
			if prID == fixtureDraftPR {
				return cerr.New(cerr.KindInvalid, "MergePR", errors.New("refusing to merge a draft"))
			}
			return nil
		}
		return s, stubManifest()
	},
	codeciconform.CheckMergeRefusesDraft: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.mergePR = func(_ context.Context, _, _ string, method codeci.MergeMethod) error {
			if !method.Specified() {
				return cerr.New(cerr.KindInvalid, "MergePR", errors.New("method is not Specified"))
			}
			return nil // merges a draft
		}
		return s, stubManifest()
	},
	codeciconform.CheckCreateRefusesIncompleteRequest: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.createPR = func(_ context.Context, req codeci.CreatePRRequest) (codeci.PR, error) {
			return codeci.PR{ID: "opened-anyway", FullName: req.FullName, URL: fixturePRURL}, nil
		}
		return s, stubManifest()
	},
	// Returns a zero PR with a nil error for a request that does not exist — the
	// exact shape the check exists to catch, and the one that passes every other
	// check in the harness because nothing else reads GetPR.
	codeciconform.CheckGetPRUnresolvableIsAFailure: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.getPR = func(context.Context, string, string) (codeci.PR, error) {
			return codeci.PR{}, nil
		}
		return s, stubManifest()
	},
	// Posts the empty comment and reports success. The id it returns is what makes
	// the violation unambiguous: something was created.
	codeciconform.CheckCommentRefusesEmptyBody: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.commentPR = func(context.Context, string, string, string) (string, error) {
			return "posted-anyway", nil
		}
		return s, stubManifest()
	},
	codeciconform.CheckBranchProtectionReported: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.listBranches = func(context.Context, string) (codeci.Resolution[codeci.Branch], error) {
			return codeci.Resolved([]codeci.Branch{
				{Name: fixtureProtectedBranch, SHA: "deadbeef"}, // always false
				{Name: fixtureUnprotectedBranch, SHA: "cafef00d"},
			}, codeci.Complete)
		}
		return s, stubManifest()
	},
	codeciconform.CheckPRsCarryAURL: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.listPRs = func(_ context.Context, fullName string, _ codeci.PRState) (codeci.Resolution[codeci.PR], error) {
			if fullName != fixtureRepo {
				return codeci.Resolution[codeci.PR]{}, cerr.New(cerr.KindNotFound, "ListPRs", nil)
			}
			// The draft carries its Draft flag and, like the open one, no URL —
			// so the single defect stays "no URL" rather than becoming two.
			return codeci.Resolved([]codeci.PR{
				{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen},
				{ID: fixtureDraftPR, FullName: fullName, State: codeci.PRStateOpen, Draft: true},
			}, codeci.Complete)
		}
		return s, stubManifest()
	},
	codeciconform.CheckIdentity: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.whoAmI = func(context.Context) (codeci.Identity, error) {
			return codeci.Identity{Authenticated: true}, nil // an empty login, reported as a success
		}
		return s, stubManifest()
	},
	codeciconform.CheckHealth: func() (codeci.CodeCI, manifest.Doc) {
		s := newStub()
		s.health = func(context.Context) (connector.Health, error) {
			return connector.Health{Status: connector.HealthStatus(42)}, nil
		}
		return s, stubManifest()
	},
}

// checkConstants parses codeciconform.go and returns every declared Check*
// constant's VALUE.
//
// It is read out of the source with go/ast because that is the only way to read
// it: Go publishes no reflection over constants, so a check added to Run
// without an entry in violators is invisible to every runtime enumeration —
// which is precisely the state this test exists to make impossible. It fails
// rather than returning an error, because a parse that found nothing would make
// the assertion below trivially true.
func checkConstants(t *testing.T) []string {
	t.Helper()

	const src = "codeciconform.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}

	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != len(vs.Names) {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Check") {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: %s is not a string literal; this test reads the check set out of the source and cannot evaluate an expression", src, name.Name)
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %s has an unreadable literal %s: %v", src, name.Name, lit.Value, err)
				}
				out = append(out, val)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s: found no Check constants at all; every assertion built on this would pass by observing nothing", src)
	}
	return out
}

// TestRun_EveryCheckHasAViolatingStub is the assertion behind this package's
// doc comment. A check never seen fail is not a check: it may be asserting
// nothing, or asserting it against the wrong value, and a harness whose whole
// purpose is catching non-conformant connectors cannot discover that in
// production.
func TestRun_EveryCheckHasAViolatingStub(t *testing.T) {
	declared := checkConstants(t)

	for _, name := range declared {
		build, ok := violators[name]
		if !ok {
			t.Errorf("check %q has no entry in violators: nothing in this suite has ever seen it fail, so nothing shows it can", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			c, m := build()
			requireFailed(t, run(t, c, m), name)
		})
	}

	for name := range violators {
		if !slices.Contains(declared, name) {
			t.Errorf("violators carries %q, which is not a declared Check constant in codeciconform.go: it drives a check that does not exist", name)
		}
	}
}

// TestRun_AViolatingStubBreaksOnlyItsOwnCheck holds the other half: an entry in
// violators that also broke a neighbouring check would make the table above
// pass while proving nothing about which check caught what — and it is the
// property that keeps a new check from being satisfied by damage another check
// already reports.
func TestRun_AViolatingStubBreaksOnlyItsOwnCheck(t *testing.T) {
	for name, build := range violators {
		t.Run(name, func(t *testing.T) {
			c, m := build()
			rep := run(t, c, m)
			for _, f := range rep.Failures() {
				if f.Name != name {
					t.Errorf("the stub built to violate %s also failed %s (%s); this table cannot attribute a catch to a check when one stub breaks two", name, f.Name, f.Detail)
				}
			}
		})
	}
}

func TestReport_Err_IsClassifiedAndNamesEveryFailure(t *testing.T) {
	s := newStub()
	s.class = connector.ClassRoster
	s.listPRs = func(context.Context, string, codeci.PRState) (codeci.Resolution[codeci.PR], error) {
		return codeci.Resolution[codeci.PR]{}, nil
	}
	rep := run(t, s, stubManifest())

	err := rep.Err()
	if err == nil {
		t.Fatal("a report with failures returned no error")
	}
	if got := cerr.KindOf(err); got != cerr.KindInvalid {
		t.Errorf("report error kind = %s, want %s: the connector ran, so its behaviour is invalid rather than unavailable", got, cerr.KindInvalid)
	}
	for _, name := range []string{codeciconform.CheckClass, codeciconform.CheckListsNoEmptySuccess} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the report error does not name the failed check %s: %v", name, err)
		}
	}
	if rep.OK() {
		t.Error("OK() reported true for a report with failures")
	}
}

// TestCloseIsIdempotentWillNotCloseAnIssueThatIsOpen pins the safety interlock
// [codeciconform.CheckCloseIsIdempotent] documents, rather than trusting the
// prose that describes it.
//
// That check performs the only write in this harness, and it is safe ONLY
// because the issue it writes to is already closed. A fixture that names an open
// issue by mistake must therefore fail the check — and, critically, must do so
// WITHOUT the close being attempted. Asserting the failure alone would pass
// against a harness that closed the issue and then complained about it.
func TestCloseIsIdempotentWillNotCloseAnIssueThatIsOpen(t *testing.T) {
	var closed []int
	s := newStub()
	s.closeIssue = func(_ context.Context, _ string, number int, reason codeci.CloseReason) error {
		if !reason.UsableAsReason() {
			return cerr.New(cerr.KindInvalid, "CloseIssue", errors.New("reason is not UsableAsReason"))
		}
		closed = append(closed, number)
		return nil
	}

	// The fixture points ClosedIssue at the issue the stub reports as OPEN.
	opts := baseOptions(stubManifest())
	opts.ClosedIssue = fixtureOpenIssue
	rep, err := codeciconform.Run(context.Background(), s, opts)
	if err != nil {
		t.Fatalf("the conformance run could not be carried out: %v", err)
	}

	requireFailed(t, rep, codeciconform.CheckCloseIsIdempotent)
	if slices.Contains(closed, fixtureOpenIssue) {
		t.Fatalf("the harness CLOSED issue %d, which its own fixtures reported as open; the read before the write is a safety interlock and it did not hold", fixtureOpenIssue)
	}
}

// TestCloseIsIdempotentDoesCloseTheAlreadyClosedFixture is the other half, and
// it exists because the test above would also pass against a check that never
// wrote at all — a check that skipped its write would satisfy the interlock
// vacuously while proving nothing about idempotency.
func TestCloseIsIdempotentDoesCloseTheAlreadyClosedFixture(t *testing.T) {
	var closed []int
	s := newStub()
	s.closeIssue = func(_ context.Context, _ string, number int, reason codeci.CloseReason) error {
		if !reason.UsableAsReason() {
			return cerr.New(cerr.KindInvalid, "CloseIssue", errors.New("reason is not UsableAsReason"))
		}
		closed = append(closed, number)
		return nil
	}

	rep := run(t, s, stubManifest())
	if detail, did := failed(rep, codeciconform.CheckCloseIsIdempotent); did {
		t.Fatalf("%s failed against a compliant stub: %s", codeciconform.CheckCloseIsIdempotent, detail)
	}
	if !slices.Contains(closed, fixtureClosedIssue) {
		t.Fatalf("the harness never called CloseIssue on the already-closed fixture; the idempotency obligation was reported green without being driven")
	}
}

// TestCloseRefusesUnspecifiedReasonIsDrivenAtAnIssueThatCannotBeClosed pins the
// choice that makes the reason-guard check safe: it is aimed at an issue that
// does not exist, so even a connector with NO guard cannot close anything real
// while being measured for it.
func TestCloseRefusesUnspecifiedReasonIsDrivenAtAnIssueThatCannotBeClosed(t *testing.T) {
	var closedWithoutReason []int
	s := newStub()
	// A connector with no guard at all: it closes whatever it is pointed at,
	// whatever the reason.
	s.closeIssue = func(_ context.Context, _ string, number int, reason codeci.CloseReason) error {
		if !reason.UsableAsReason() {
			closedWithoutReason = append(closedWithoutReason, number)
		}
		return nil
	}

	rep := run(t, s, stubManifest())
	requireFailed(t, rep, codeciconform.CheckCloseRefusesUnspecifiedReason)
	if slices.Contains(closedWithoutReason, fixtureOpenIssue) || slices.Contains(closedWithoutReason, fixtureClosedIssue) {
		t.Fatalf("the guard check was driven at a real fixture (%v); against a connector with no guard that closes a live issue to discover the guard is missing", closedWithoutReason)
	}
}

// The tests below assert WHY each new check failed, not merely that it did.
//
// ⚠️ They exist because a mutation pass showed the gap. `violators` proves every
// check has a stub that trips it — but a check can record a violation for the
// WRONG REASON and that table cannot tell. Removing the nil-error branch from
// prs/get-unresolvable-is-a-typed-failure still produced a violation, attributed
// to "unclassified error" instead, and the whole suite stayed green.
//
// So each branch gets a stub that reaches only that branch, and the assertion is
// on the detail string.

func TestGetPRUnresolvable_ZeroPRWithNilErrorIsNamedAsSuch(t *testing.T) {
	s := newStub()
	s.getPR = func(context.Context, string, string) (codeci.PR, error) {
		return codeci.PR{}, nil // the invented-request shape
	}
	rep := run(t, s, stubManifest())
	detail, did := failed(rep, codeciconform.CheckGetPRUnresolvableIsAFailure)
	if !did {
		t.Fatalf("a connector that invents a pull request passed the check\n%s", rep)
	}
	if !strings.Contains(detail, "succeeded") {
		t.Errorf("the detail blames the wrong thing — a nil error must be reported as a SUCCESS that should\n"+
			"not have happened, not as an unclassified error. got: %s", detail)
	}
}

func TestGetPRUnresolvable_UnclassifiedErrorIsNamedAsSuch(t *testing.T) {
	s := newStub()
	s.getPR = func(context.Context, string, string) (codeci.PR, error) {
		return codeci.PR{}, errors.New("bare error, no cerr.Kind")
	}
	rep := run(t, s, stubManifest())
	detail, did := failed(rep, codeciconform.CheckGetPRUnresolvableIsAFailure)
	if !did {
		t.Fatalf("an unclassified failure passed the check; a host cannot act on it\n%s", rep)
	}
	if !strings.Contains(detail, "unclassified") {
		t.Errorf("the detail must name the error as unclassified, or the two failure shapes are\n"+
			"indistinguishable in a report. got: %s", detail)
	}
}

func TestCommentRefusesEmptyBody_PostedThenErroredIsCaught(t *testing.T) {
	s := newStub()
	s.commentPR = func(context.Context, string, string, string) (string, error) {
		// The shape a check looking only at err would miss: something WAS posted.
		return "posted-then-errored", cerr.New(cerr.KindInvalid, "CommentPR", errors.New("body is empty"))
	}
	rep := run(t, s, stubManifest())
	detail, did := failed(rep, codeciconform.CheckCommentRefusesEmptyBody)
	if !did {
		t.Fatalf("a connector that posted an empty comment and THEN refused passed the check — the refusal\n"+
			"does not undo the comment\n%s", rep)
	}
	if !strings.Contains(detail, "comment id") {
		t.Errorf("the detail must say a comment id came back, which is what proves something was posted.\n"+
			"got: %s", detail)
	}
}
