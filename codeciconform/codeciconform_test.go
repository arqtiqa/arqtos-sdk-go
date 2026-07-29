package codeciconform_test

import (
	"context"
	"errors"
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
	fixtureRepo        = "placeholder-org/populated-repo"
	fixtureUnknownRepo = "placeholder-org/absent-repo"
	fixtureCheckedRef  = "main"
	fixtureDiffPR      = "1"
	fixtureUnknownPR   = "999"
	fixtureDraftPR     = "2"
	fixtureOpenPR      = "3"
)

// stub is a codeci.CodeCI that passes every check, with one override hook per
// method a test needs to break. A harness only ever run against compliant
// input proves nothing about what it would catch, so every check below is
// driven by a stub deliberately built to violate exactly the property it
// checks.
type stub struct {
	class connector.Class
	caps  connector.Capabilities

	listPRs      func(ctx context.Context, fullName string, state codeci.PRState) (codeci.Resolution[codeci.PR], error)
	mergePR      func(ctx context.Context, fullName, prID string, method codeci.MergeMethod) error
	getDiff      func(ctx context.Context, fullName, prID string) (codeci.Resolution[codeci.DiffFile], error)
	listBranches func(ctx context.Context, fullName string) (codeci.Resolution[codeci.Branch], error)
	getCheckRuns func(ctx context.Context, fullName, ref string) (codeci.Resolution[codeci.CheckRun], error)
	health       func(ctx context.Context) (connector.Health, error)
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

func (s *stub) CreatePR(context.Context, string, string, string, string, string) (codeci.PR, error) {
	return codeci.PR{ID: "new"}, nil
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
	return codeci.Resolved([]codeci.PR{{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen}}, codeci.Complete)
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
	return codeci.Resolved([]codeci.Branch{{Name: "main", SHA: "deadbeef"}}, codeci.Complete)
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

func stubManifest(caps ...connector.Capability) manifest.Doc {
	return manifest.Doc{
		Name:         "stub",
		Implements:   connector.ClassCodeCI,
		Kind:         manifest.KindNative,
		Capabilities: caps,
	}
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
	want := []string{
		codeciconform.CheckManifest, codeciconform.CheckClass, codeciconform.CheckCapabilityHonesty,
		codeciconform.CheckOptionalDeclared, codeciconform.CheckListsNoEmptySuccess, codeciconform.CheckListFailClosed,
		codeciconform.CheckMergeRefusesUnspecifiedMethod, codeciconform.CheckMergeRefusesDraft, codeciconform.CheckHealth,
	}
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
	for _, field := range []string{"Repo", "UnknownRepo", "CheckedRef", "DiffPR", "UnknownPR", "DraftPR", "OpenPR"} {
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
			return codeci.Resolved([]codeci.PR{{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen}}, codeci.Complete)
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
		return codeci.Resolved([]codeci.PR{{ID: fixtureOpenPR, FullName: fullName, State: codeci.PRStateOpen}}, codeci.Complete)
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
