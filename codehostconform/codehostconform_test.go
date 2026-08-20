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
	// A repository and ref whose protection the stub must NOT be able to read.
	fixtureUnreadableRepo = "listable-owner/private-repo"
	fixtureUnreadableRef  = "refs/heads/unreadable"
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

// protectionInspectingStub implements the optional CapProtectionInspect
// operation. It is a separate type because implementing it is what the
// harness type-asserts for — and because ListBranches.Protected is NOT this
// probe.
type protectionInspectingStub struct{ *stub }

// ⚠️ It distinguishes "not allowed to look" from "no rules here" — the two
// answers that share a shape and mean opposite things. On the unreadable fixture
// it returns a TYPED failure and a Protection whose lists cannot be read; a
// caller that ignored the error therefore cannot conclude that nothing may
// bypass the gate.
func (protectionInspectingStub) InspectProtection(_ context.Context, _, ref string) (codehost.Protection, error) {
	if ref == fixtureUnreadableRef {
		return codehost.Protection{}, cerr.New(cerr.KindUnauthorized, "InspectProtection",
			errors.New("the credential may not read this ref's protection"))
	}
	return codehost.Protection{
		Ref:            ref,
		RequiredChecks: codehost.EmptyList[codehost.RequiredCheck](),
		BypassActors:   codehost.EmptyList[codehost.BypassActor](),
	}, nil
}

var (
	_ codehost.FileReader          = fileReadingStub{}
	_ codehost.WebhookRegistrar    = hookRegisteringStub{}
	_ codehost.RunnerTokenMinter   = tokenMintingStub{}
	_ codehost.ProtectionInspector = protectionInspectingStub{}
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
		// Supplied unconditionally: a connector that does not implement the
		// tier ignores them, and one that does must have them or its failure
		// classification is never exercised.
		UnreadableProtectionRepo: fixtureUnreadableRepo,
		UnreadableProtectionRef:  fixtureUnreadableRef,
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
		{"protection inspect", protectionInspectingStub{newStub()}},
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

// ⚠️ CapProtectionInspect is a NEW optional tier. Declaring it without
// implementing InspectProtection is the dangerous direction: a host plans to
// probe its own assurance and finds no operation to do it with.
func TestConform_CatchesProtectionInspectDeclaredButAbsent(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codehost.CapProtectionInspect}
	rep := run(t, s, stubManifest(codehost.CapProtectionInspect))
	requireFailed(t, rep, codehostconform.CheckOptionalDeclared)
	detail, _ := failed(rep, codehostconform.CheckOptionalDeclared)
	if !strings.Contains(detail, string(codehost.CapProtectionInspect)) {
		t.Errorf("the failure does not name the capability: %q", detail)
	}
}

func TestConform_AcceptsProtectionInspectDeclaredAndImplemented(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codehost.CapProtectionInspect}
	rep := run(t, protectionInspectingStub{s}, stubManifest(codehost.CapProtectionInspect))
	if err := rep.Err(); err != nil {
		t.Fatalf("a connector that declares and implements protection_inspect was failed: %v\n%s", err, rep)
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

// ⚠️ THE DANGEROUS SHAPE THIS CHECK EXISTS FOR. A connector that answers
// "no rules here" when it was simply not allowed to LOOK hands an assurance
// probe the most flattering possible wrong answer: an unprotected ref reported
// as a ref with nothing to bypass.
type blindProtectionStub struct{ *stub }

func (blindProtectionStub) InspectProtection(_ context.Context, _, ref string) (codehost.Protection, error) {
	// Always succeeds, and always with resolved-empty lists.
	return codehost.Protection{
		Ref:            ref,
		RequiredChecks: codehost.EmptyList[codehost.RequiredCheck](),
		BypassActors:   codehost.EmptyList[codehost.BypassActor](),
	}, nil
}

// Fails typed, but STILL hands back a readable bypass list — so a caller that
// logged the error and carried on would read "nobody may bypass this gate" out
// of a read that never happened.
type leakyProtectionStub struct{ *stub }

func (leakyProtectionStub) InspectProtection(_ context.Context, _, ref string) (codehost.Protection, error) {
	return codehost.Protection{
		Ref:            ref,
		RequiredChecks: codehost.EmptyList[codehost.RequiredCheck](),
		BypassActors:   codehost.EmptyList[codehost.BypassActor](),
	}, cerr.New(cerr.KindUnauthorized, "InspectProtection", errors.New("not allowed"))
}

var (
	_ codehost.ProtectionInspector = blindProtectionStub{}
	_ codehost.ProtectionInspector = leakyProtectionStub{}
)

func TestConform_CatchesProtectionSucceedingOnAnUnreadableRef(t *testing.T) {
	rep := run(t, blindProtectionStub{newStub()}, stubManifest(codehost.CapProtectionInspect))

	detail, failedCheck := failed(rep, codehostconform.CheckProtectionFailClosed)
	if !failedCheck {
		t.Fatal("a connector that reports an unreadable ref as having no protection was accepted; " +
			"an assurance probe built on it would report an unprotected ref for an unauthorized read")
	}
	if !strings.Contains(detail, "SUCCEEDED") {
		t.Errorf("detail does not say the call succeeded when it must not have: %s", detail)
	}
}

func TestConform_CatchesProtectionHandingBackAReadableListOnFailure(t *testing.T) {
	rep := run(t, leakyProtectionStub{newStub()}, stubManifest(codehost.CapProtectionInspect))

	detail, failedCheck := failed(rep, codehostconform.CheckProtectionFailClosed)
	if !failedCheck {
		t.Fatal("a connector that failed AND returned a readable bypass list was accepted; a caller " +
			"that ignored the error would read the failure as 'nobody may bypass this gate'")
	}
	if !strings.Contains(detail, "readable bypass-actor list") {
		t.Errorf("detail does not name the readable list: %s", detail)
	}
}

func TestConform_AcceptsAProtectionInspectorThatFailsClosed(t *testing.T) {
	rep := run(t, protectionInspectingStub{newStub()}, stubManifest(codehost.CapProtectionInspect))

	if detail, failedCheck := failed(rep, codehostconform.CheckProtectionFailClosed); failedCheck {
		t.Fatalf("a connector that fails typed and unreadably on an unreadable ref was rejected: %s", detail)
	}
}

// ⚠️ A connector without the tier must not produce a green that examined
// nothing. The check passes — there is genuinely nothing to drive — but its
// detail must say so, or the report cannot be told apart from one where the
// behaviour was verified.
func TestConform_ProtectionCheckSaysWhenItWasNotExercised(t *testing.T) {
	rep := run(t, newStub(), stubManifest())

	var detail string
	var found bool
	for _, res := range rep.Results {
		if res.Name == codehostconform.CheckProtectionFailClosed {
			detail, found = res.Detail, true
		}
	}
	if !found {
		t.Fatal("the check is absent from the report for a connector without the tier; a missing check " +
			"is indistinguishable from one that passed")
	}
	if !strings.Contains(detail, "NOT EXERCISED") {
		t.Errorf("detail %q does not mark the check as not exercised, so a green reads as verified behaviour", detail)
	}
}

// A connector that HAS the tier but whose run supplies no fixture must fail
// rather than skip: the fixture nobody supplied would otherwise become the check
// nobody runs.
func TestConform_ProtectionCheckRefusesToSkipWhenTheTierIsPresent(t *testing.T) {
	rep, err := codehostconform.Run(context.Background(), protectionInspectingStub{newStub()}, codehostconform.Options{
		Manifest:        stubManifest(codehost.CapProtectionInspect),
		ListableOwner:   fixtureListable,
		UnlistableOwner: fixtureUnlistable,
		// fixture deliberately omitted
	})
	if err != nil {
		t.Fatalf("the run could not be carried out: %v", err)
	}
	detail, failedCheck := failed(rep, codehostconform.CheckProtectionFailClosed)
	if !failedCheck {
		t.Fatal("a run with the tier present and no fixture passed; the check was never exercised and said nothing")
	}
	if !strings.Contains(detail, "never exercised") {
		t.Errorf("detail does not explain that the fixture is missing: %s", detail)
	}
}
