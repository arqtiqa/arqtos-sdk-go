package codehostconform_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
	"github.com/arqtiqa/arqtos-sdk-go/codehostconform"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

// The fixtures every run in this file is driven with. The listable owner has
// one repository; the other is absent from the stub entirely.
const (
	fixtureListable   = "listable-owner"
	fixtureUnlistable = "absent-owner"
)

// stub is a codehost.CodeHost that passes every check, with one knob per check so a test
// can break exactly one property and watch the harness catch it.
//
// A harness only ever run against compliant input proves nothing about what it
// would catch, so every check below is driven by a stub deliberately built to
// violate it.
type stub struct {
	class connector.Class
	caps  connector.Capabilities

	// listRepos overrides the default listing behaviour when non-nil.
	listRepos func(ctx context.Context, owner string) (codehost.Resolution[codehost.Repo], error)
	// health overrides the default healthy answer when non-nil.
	health func(ctx context.Context) (connector.Health, error)
}

func newStub() *stub {
	return &stub{class: connector.ClassCodeHost, caps: connector.Capabilities{codehost.CapNativeReview}}
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

func (s *stub) ListRepos(ctx context.Context, owner string) (codehost.Resolution[codehost.Repo], error) {
	if s.listRepos != nil {
		return s.listRepos(ctx, owner)
	}
	if owner != fixtureListable {
		return codehost.Resolution[codehost.Repo]{}, cerr.New(cerr.KindNotFound, "ListRepos", nil)
	}
	return codehost.Resolved([]codehost.Repo{{FullName: fixtureListable + "/one", Owner: fixtureListable, Name: "one"}}, codehost.Complete)
}

func (s *stub) RepoExists(context.Context, string) (bool, error)       { return true, nil }
func (s *stub) GetRepo(context.Context, string) (codehost.Repo, error) { return codehost.Repo{}, nil }
func (s *stub) CreateRepo(context.Context, codehost.CreateRepoOpts) (codehost.Repo, error) {
	return codehost.Repo{}, nil
}
func (s *stub) SetTopics(context.Context, string, []string) error { return nil }
func (s *stub) CloneRepo(context.Context, string, string) error   { return nil }
func (s *stub) PushBranch(context.Context, string, string, string) error {
	return nil
}
func (s *stub) ListBranches(context.Context, string) (codehost.Resolution[codehost.Branch], error) {
	return codehost.Resolved([]codehost.Branch{{Name: "main"}}, codehost.Complete)
}

var _ codehost.CodeHost = (*stub)(nil)

// fileReadingStub implements the optional codehost.CapFileRead operation. It is a
// separate type because implementing it is what the harness type-asserts for.
type fileReadingStub struct{ *stub }

func (fileReadingStub) ReadFile(context.Context, string, string) ([]byte, error) {
	return []byte("x"), nil
}

// hookRegisteringStub implements the optional codehost.CapWebhooks operation.
type hookRegisteringStub struct{ *stub }

func (hookRegisteringStub) WebhookRegister(context.Context, string, string, []string) error {
	return nil
}

// tokenMintingStub implements the optional codehost.CapRunnerTokens operation.
type tokenMintingStub struct{ *stub }

func (tokenMintingStub) RunnerToken(context.Context, string) (string, time.Time, error) {
	return "placeholder-runner-registration-token", time.Now().Add(time.Hour), nil
}

var (
	_ codehost.FileReader        = fileReadingStub{}
	_ codehost.WebhookRegistrar  = hookRegisteringStub{}
	_ codehost.RunnerTokenMinter = tokenMintingStub{}
)

func stubManifest(caps ...connector.Capability) manifest.Doc {
	return manifest.Doc{
		Name:         "stub",
		Implements:   connector.ClassCodeHost,
		Kind:         manifest.KindNative,
		Capabilities: caps,
	}
}

func run(t *testing.T, c codehost.CodeHost, m manifest.Doc) codehostconform.Report {
	t.Helper()
	rep, err := codehostconform.Run(context.Background(), c, codehostconform.Options{
		Manifest:        m,
		ListableOwner:   fixtureListable,
		UnlistableOwner: fixtureUnlistable,
	})
	if err != nil {
		t.Fatalf("the conformance run could not be carried out: %v", err)
	}
	return rep
}

// failed reports the Detail of the named check, and whether it failed at all.
func failed(rep codehostconform.Report, name string) (string, bool) {
	for _, res := range rep.Results {
		if res.Name == name {
			return res.Detail, !res.Pass
		}
	}
	return "", false
}

func requireFailed(t *testing.T, rep codehostconform.Report, name string) {
	t.Helper()
	detail, did := failed(rep, name)
	if !did {
		t.Fatalf("%s passed against a connector built to violate it\n%s", name, rep)
	}
	if detail == "" {
		t.Errorf("%s failed with no detail, so the report does not say what is wrong", name)
	}
}

func TestConform_CompliantStub_IsGreenOnEveryCheck(t *testing.T) {
	rep := run(t, newStub(), stubManifest(codehost.CapNativeReview))
	if err := rep.Err(); err != nil {
		t.Fatalf("a compliant stub was reported non-conformant: %v\n%s", err, rep)
	}
	// A report that ran no checks is green for the wrong reason.
	want := []string{
		codehostconform.CheckManifest, codehostconform.CheckClass, codehostconform.CheckCapabilityHonesty, codehostconform.CheckOptionalDeclared,
		codehostconform.CheckListNoEmptySuccess, codehostconform.CheckListFailClosed, codehostconform.CheckHealth,
	}
	for _, name := range want {
		if _, found := failed(rep, name); !found {
			// found==false covers both "passed" and "absent"; distinguish.
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
	}
	t.Logf("%s", rep)
}

func TestConform_RefusesToRunWithoutFixtures(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts codehostconform.Options
	}{
		{"no listable owner", codehostconform.Options{Manifest: stubManifest(), UnlistableOwner: fixtureUnlistable}},
		{"no unlistable owner", codehostconform.Options{Manifest: stubManifest(), ListableOwner: fixtureListable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := codehostconform.Run(context.Background(), newStub(), tc.opts); err == nil {
				t.Fatal("a run with a missing fixture was carried out; a check that cannot be driven must not be skipped")
			}
		})
	}
}

func TestConform_RefusesANilConnector(t *testing.T) {
	if _, err := codehostconform.Run(context.Background(), nil, codehostconform.Options{
		Manifest: stubManifest(), ListableOwner: fixtureListable, UnlistableOwner: fixtureUnlistable,
	}); err == nil {
		t.Fatal("a run against no connector reported something")
	}
}

func TestConform_CatchesAManifestOutsideTheVocabulary(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{"webhook"} // misspelt: the vocabulary says "webhooks"
	rep := run(t, s, stubManifest("webhook"))
	requireFailed(t, rep, codehostconform.CheckManifest)
}

func TestConform_CatchesTheWrongClass(t *testing.T) {
	s := newStub()
	s.class = connector.ClassRoster
	rep := run(t, s, stubManifest(codehost.CapNativeReview))
	requireFailed(t, rep, codehostconform.CheckClass)
}

func TestConform_CatchesADeclarationTheRuntimeDoesNotKeep(t *testing.T) {
	s := newStub() // reports codehost.CapNativeReview only
	rep := run(t, s, stubManifest(codehost.CapNativeReview, codehost.CapWebhooks))
	requireFailed(t, rep, codehostconform.CheckCapabilityHonesty)
}

func TestConform_CatchesARuntimeCapabilityTheManifestDoesNotDeclare(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codehost.CapNativeReview, codehost.CapFileRead}
	rep := run(t, s, stubManifest(codehost.CapNativeReview))
	requireFailed(t, rep, codehostconform.CheckCapabilityHonesty)
}

// TestConform_CatchesAnOptionalOperationDeclaredButAbsent is the dangerous
// direction: a host that believes it can read one file without cloning plans
// for a cheap call and finds nothing behind it.
func TestConform_CatchesAnOptionalOperationDeclaredButAbsent(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codehost.CapFileRead} // *stub has no ReadFile
	rep := run(t, s, stubManifest(codehost.CapFileRead))
	requireFailed(t, rep, codehostconform.CheckOptionalDeclared)
	detail, _ := failed(rep, codehostconform.CheckOptionalDeclared)
	if !strings.Contains(detail, string(codehost.CapFileRead)) {
		t.Errorf("the failure does not name the capability: %q", detail)
	}
}

// TestConform_CatchesAnOptionalOperationImplementedButUndeclared covers the
// other direction, which is not harmless: a host reads the manifest before it
// loads the connector, so behaviour that is there and undeclared is behaviour
// nothing will ever call.
func TestConform_CatchesAnOptionalOperationImplementedButUndeclared(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    codehost.CodeHost
	}{
		{"file read", fileReadingStub{newStub()}},
		{"webhooks", hookRegisteringStub{newStub()}},
		{"runner tokens", tokenMintingStub{newStub()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := run(t, tc.c, stubManifest(codehost.CapNativeReview))
			requireFailed(t, rep, codehostconform.CheckOptionalDeclared)
		})
	}
}

func TestConform_AcceptsAnOptionalOperationDeclaredAndImplemented(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codehost.CapFileRead}
	rep := run(t, fileReadingStub{s}, stubManifest(codehost.CapFileRead))
	if err := rep.Err(); err != nil {
		t.Fatalf("a connector that declares and implements an optional operation was failed: %v\n%s", err, rep)
	}
}

// TestConform_CatchesAnEmptySuccess drives the shape this whole contract is
// built to refuse: a nil error beside a resolution that carries nothing.
func TestConform_CatchesAnEmptySuccess(t *testing.T) {
	s := newStub()
	s.listRepos = func(context.Context, string) (codehost.Resolution[codehost.Repo], error) {
		return codehost.Resolution[codehost.Repo]{}, nil
	}
	rep := run(t, s, stubManifest(codehost.CapNativeReview))
	requireFailed(t, rep, codehostconform.CheckListNoEmptySuccess)
}

// TestConform_CatchesAnAssertedEmptyListWhereTheFixtureSaysThereAreRepos
// covers the connector that asserts codehost.EmptyList on an owner that has
// repositories — a legitimate assertion made about the wrong thing.
func TestConform_CatchesAnAssertedEmptyListWhereTheFixtureSaysThereAreRepos(t *testing.T) {
	s := newStub()
	s.listRepos = func(context.Context, string) (codehost.Resolution[codehost.Repo], error) {
		return codehost.EmptyList[codehost.Repo](), nil
	}
	rep := run(t, s, stubManifest(codehost.CapNativeReview))
	requireFailed(t, rep, codehostconform.CheckListNoEmptySuccess)
}

func TestConform_CatchesAnUnclassifiedFailure(t *testing.T) {
	s := newStub()
	s.listRepos = func(_ context.Context, owner string) (codehost.Resolution[codehost.Repo], error) {
		if owner == fixtureListable {
			return codehost.Resolved([]codehost.Repo{{FullName: "listable-owner/one"}}, codehost.Complete)
		}
		// Vendor prose. A host would have to parse it to act on it.
		return codehost.Resolution[codehost.Repo]{}, errors.New("404 Not Found []")
	}
	rep := run(t, s, stubManifest(codehost.CapNativeReview))
	requireFailed(t, rep, codehostconform.CheckListFailClosed)
}

// TestConform_CatchesAReadableResolutionBesideAFailure is the fail-open shape a
// classified error does not save you from: the caller that logged the error and
// carried on reads an unlisted code host as one with no repositories.
func TestConform_CatchesAReadableResolutionBesideAFailure(t *testing.T) {
	s := newStub()
	s.listRepos = func(_ context.Context, owner string) (codehost.Resolution[codehost.Repo], error) {
		if owner == fixtureListable {
			return codehost.Resolved([]codehost.Repo{{FullName: "listable-owner/one"}}, codehost.Complete)
		}
		return codehost.EmptyList[codehost.Repo](), cerr.New(cerr.KindUnauthorized, "ListRepos", nil)
	}
	rep := run(t, s, stubManifest(codehost.CapNativeReview))
	requireFailed(t, rep, codehostconform.CheckListFailClosed)
}

func TestConform_CatchesAConnectorThatNeverFails(t *testing.T) {
	s := newStub()
	s.listRepos = func(context.Context, string) (codehost.Resolution[codehost.Repo], error) {
		return codehost.Resolved([]codehost.Repo{{FullName: "listable-owner/one"}}, codehost.Complete)
	}
	rep := run(t, s, stubManifest(codehost.CapNativeReview))
	requireFailed(t, rep, codehostconform.CheckListFailClosed)
}

func TestConform_CatchesUnhealthyReporting(t *testing.T) {
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
			rep := run(t, s, stubManifest(codehost.CapNativeReview))
			requireFailed(t, rep, codehostconform.CheckHealth)
		})
	}
}

// TestConform_AcceptsAClassifiedHealthFailure: a connector whose backend is
// down is unreachable, not non-conformant. The check is that it SAYS so in the
// closed vocabulary.
func TestConform_AcceptsAClassifiedHealthFailure(t *testing.T) {
	s := newStub()
	s.health = func(context.Context) (connector.Health, error) {
		return connector.Health{}, cerr.New(cerr.KindUnavailable, "Health", nil)
	}
	rep := run(t, s, stubManifest(codehost.CapNativeReview))
	if err := rep.Err(); err != nil {
		t.Fatalf("a connector reporting a classified health failure was called non-conformant: %v\n%s", err, rep)
	}
}

func TestReport_Err_IsClassifiedAndNamesEveryFailure(t *testing.T) {
	s := newStub()
	s.class = connector.ClassRoster
	s.listRepos = func(context.Context, string) (codehost.Resolution[codehost.Repo], error) {
		return codehost.Resolution[codehost.Repo]{}, nil
	}
	rep := run(t, s, stubManifest(codehost.CapNativeReview))

	err := rep.Err()
	if err == nil {
		t.Fatal("a report with failures returned no error")
	}
	if got := cerr.KindOf(err); got != cerr.KindInvalid {
		t.Errorf("report error kind = %s, want %s: the connector ran, so its behaviour is invalid rather than unavailable", got, cerr.KindInvalid)
	}
	for _, name := range []string{codehostconform.CheckClass, codehostconform.CheckListNoEmptySuccess} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the report error does not name the failed check %s: %v", name, err)
		}
	}
	if rep.OK() {
		t.Error("OK() reported true for a report with failures")
	}
}
