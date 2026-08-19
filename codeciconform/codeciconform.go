// Package codeciconform checks a [codeci.CodeCI] connector against the parts
// of the contract a compiler cannot enforce.
//
// The Go type system already refuses several ways of getting this wrong: a
// connector missing an operation does not build, and a list resolution
// carrying nothing cannot be constructed as a complete success (see
// [codeci.Resolution]). What is left are the properties that depend on
// BEHAVIOUR and on what the connector's manifest CLAIMS — and those need a
// run, the same reason [github.com/arqtiqa/arqtos-sdk-go/credconform] and
// [github.com/arqtiqa/arqtos-sdk-go/rosterconform] exist for their classes.
//
// Run it in your own CI, against your own connector, before arqtos ever
// loads it:
//
//	rep, err := codeciconform.Run(ctx, myConnector, codeciconform.Options{
//		Manifest:          myManifest,
//		Repo:              "org/populated-repo",
//		UnknownRepo:       "org/does-not-exist",
//		CheckedRef:        "main",
//		DiffPR:            "1",
//		UnknownPR:         "999999",
//		DraftPR:           "2",
//		OpenPR:            "3",
//		ProtectedBranch:   "main",
//		UnprotectedBranch: "topic",
//		OpenIssue:         10,
//		ClosedIssue:       11, // ⚠️ re-closed by this run; see [Options.ClosedIssue]
//		UnknownIssue:      999999,
//	})
//	if err != nil {
//		return err // the check could not be run at all
//	}
//	if err := rep.Err(); err != nil {
//		return err // the connector ran, and is not conformant
//	}
//
// # The harness is driven by non-compliant connectors too
//
// Every check in this package's test suite is driven by a connector
// deliberately built to violate the property it checks — one that resolves an
// empty list as a success, one that merges a draft, one that reports an
// authenticated identity with no login, one whose manifest declares a
// capability it does not implement. A harness only ever run against compliant
// input proves nothing about what it would catch.
//
// That is asserted rather than only stated: TestRun_EveryCheckHasAViolatingStub
// reads the Check* constants out of this file's source (Go publishes no
// reflection over constants) and fails if any one of them has no connector built
// to break it, and its companion fails if one such connector breaks a
// NEIGHBOURING check as well — which would let a new check pass on damage
// another check already reports.
// # ⚠️ What this harness CANNOT check, and why
//
// CreatePR is never driven. A created pull/merge request cannot be undone by the
// harness that created it, and every write check here is driven through a
// REFUSAL for exactly that reason — MergePR with an unspecified method, MergePR
// against a draft, CloseIssue with an unspecified reason. The one create check,
// [CheckCreateRefusesIncompleteRequest], is a refusal too: it asserts validation
// happens before anything is sent, and never inspects a returned PR because none
// is returned.
//
// ⚠️ [CheckCloseIsIdempotent] is the ONE exception, and it is an exception only
// because its write is a no-op: it closes an issue the fixtures name as already
// closed, so the postcondition holds before it runs. It confirms that with a
// read first and refuses to write if the issue is not closed. Nothing else here
// mutates a connector's backing host, and a check that needed to would belong
// somewhere other than a suite that must stay safe to repeat.
//
// Two contract obligations therefore have NO check here, and a connector author
// is on their honour for both:
//
//   - **CreatePR honours [codeci.CreatePRRequest.Draft].** The read half IS
//     checked — [CheckDraftIsReported] asserts the draft fixture comes back as a
//     draft — but nothing proves a connector that can REPORT a draft can also
//     PRODUCE one.
//   - **The PR returned by CreatePR carries a URL.** [codeci.PR.URL]'s doc states
//     the obligation unconditionally; [CheckPRsCarryAURL] enforces it only on the
//     list path.
//
// ⚠️ Do not read a green run as covering these. The alternative would be a
// harness that opens a real pull request every time it runs, against a
// repository it cannot clean up — which would cost more than it proves and would
// end the property that makes this suite safe to repeat against a live fixture.

package codeciconform

import (
	"context"
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codeci"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

// Check names reported by [Run]. They are stable identifiers: a caller may
// switch on them, and a CI job may allowlist a known failure by name.
const (
	// CheckManifest covers the connector's manifest validating, declaring
	// this class, and declaring only capabilities the class defines.
	CheckManifest = "manifest/valid"
	// CheckClass covers the running connector reporting connector.ClassCodeCI
	// from Implements(). A host routes by class, so a connector that reports
	// another one is dispatched as something it is not.
	CheckClass = "class/implements"
	// CheckCapabilityHonesty covers the manifest's declared capabilities and
	// the running connector's Capabilities() being the same set.
	CheckCapabilityHonesty = "capability/manifest-matches-runtime"
	// CheckOptionalDeclared covers each optional operation being declared
	// exactly when it is implemented: CapCIControl ↔ CIController and
	// CapCheckPublish ↔ CheckPublisher. "Implemented" is a Go interface
	// assertion against the connector's own type, independent of anything it
	// declares — the same mechanism codehost's optional operations and
	// roster's Watcher use, and deliberately NOT derived from Capabilities():
	// a check that read "implemented" off the same signal as "declared" would
	// agree with itself whatever the connector does.
	CheckOptionalDeclared = "optional/declared-is-implemented"
	// CheckListsNoEmptySuccess covers ListPRs, GetDiff, ListBranches and
	// GetCheckRuns, run against fixtures the connector MUST find something
	// for, each coming back as a READABLE resolution carrying entries — never
	// as the zero Resolution with a nil error.
	CheckListsNoEmptySuccess = "lists/no-empty-success"
	// CheckListFailClosed covers a repository the connector cannot find
	// (ListPRs) and a pull/merge request the connector cannot find (GetDiff)
	// each failing with a classified error AND an unreadable resolution.
	CheckListFailClosed = "lists/failure-is-typed-and-fail-closed"
	// CheckMergeRefusesUnspecifiedMethod covers MergePR refusing
	// codeci.MergeMethodUnspecified with a classified error, without
	// attempting a merge.
	CheckMergeRefusesUnspecifiedMethod = "merge/refuses-unspecified-method"
	// CheckMergeRefusesDraft covers MergePR refusing to merge a pull/merge
	// request currently reported as a draft, with a classified error.
	CheckMergeRefusesDraft = "merge/refuses-draft"
	// CheckCreateRefusesIncompleteRequest covers CreatePR refusing a
	// codeci.CreatePRRequest missing a required field, with a classified
	// error, without opening anything. It is the guard the request STRUCT
	// needs: positional arguments forced a caller to write "" on purpose, a
	// struct lets a field be forgotten silently.
	CheckCreateRefusesIncompleteRequest = "create/refuses-an-incomplete-request"
	// CheckBranchProtectionReported covers ListBranches reporting
	// codeci.Branch.Protected from the code host rather than asserting a
	// constant, driven by one protected and one unprotected fixture. A
	// connector answering always-false says every branch is unprotected — the
	// one always-zero field in this class that is actively dangerous — and one
	// answering always-true would satisfy a check that only looked at the
	// protected fixture.
	CheckBranchProtectionReported = "branches/protection-is-reported"
	// CheckPRsCarryAURL covers every codeci.PR from ListPRs carrying a
	// non-empty URL, so a caller is not left assembling a vendor URL itself.
	CheckPRsCarryAURL = "prs/carry-a-url"

	// CheckDraftIsReported covers the READ half of the draft contract: the
	// fixtures name a pull/merge request that IS a draft, and a list read must
	// report it as one.
	//
	// ⚠️ It exists because Draft is a bool, so a connector that never populates
	// it reports every draft as NOT a draft — and the contract refuses to MERGE
	// a draft, so a false negative here does not fail loudly. It merges the
	// thing the refusal exists to protect. That is the same always-zero hazard
	// the branch-protection field carries, on the other side of the same struct.
	CheckDraftIsReported = "prs/draft-is-reported"

	// CheckGetPRUnresolvableIsAFailure drives the rule that a pull/merge
	// request which cannot be resolved is a TYPED FAILURE and never a zero PR.
	//
	// ⚠️ The zero value is the whole hazard: it is a syntactically valid
	// pull/merge request numbered 0 with an unset state, so a caller that acts
	// on it operates on nothing while believing it read something. A connector
	// that returns (PR{}, nil) passes every other check in this harness.
	CheckGetPRUnresolvableIsAFailure = "prs/get-unresolvable-is-a-typed-failure"

	// CheckCommentRefusesEmptyBody drives the guard that an empty comment body
	// is refused BEFORE anything is posted.
	//
	// The alternative is an empty comment on someone's change — an artefact a
	// human then has to find and delete — which is the same reasoning CreatePR
	// applies to a request with no title.
	CheckCommentRefusesEmptyBody = "prs/comment-refuses-empty-body"
	// CheckIdentity covers WhoAmI answering with an authenticated identity
	// carrying a non-empty login, or failing with a classified error. An empty
	// login reported as a success is refused: a host publishes this answer as
	// its standing proof that it authenticates with no token in its own
	// environment, and an empty login asserts the opposite while wearing a
	// success's shape.
	CheckIdentity = "identity/answers-with-a-login"
	// CheckHealth covers Health() answering: a status, or a classified failure.
	CheckHealth = "health/answers"

	// CheckIssueStateReported covers GetIssue reporting codeci.IssueState from
	// the code host rather than asserting a constant, driven by one open and one
	// closed fixture — the same pair, for the same reason, as
	// CheckBranchProtectionReported. With only the open fixture, a connector
	// hard-coding IssueStateOpen passes; with only the closed one, one
	// hard-coding IssueStateClosed passes.
	//
	// It also covers the read echoing the address it was asked for: a bare state
	// carries no evidence of which issue it is the state of, so a connector
	// answering about the wrong issue is otherwise indistinguishable from one
	// answering correctly.
	CheckIssueStateReported = "issues/state-is-reported"
	// CheckIssueUnresolvableIsAFailure covers the failure direction that does
	// damage rather than merely being wrong: an issue that cannot be resolved
	// MUST be a classified failure and MUST NOT come back as a state. A caller
	// resolving a blocker reads a wrongly-closed answer as "go ahead" and starts
	// work that is still blocked.
	CheckIssueUnresolvableIsAFailure = "issues/unresolvable-is-a-typed-failure"

	// CheckCloseRefusesUnspecifiedReason covers CloseIssue validating its reason
	// BEFORE it does anything else — MergePR's method guard in another shape, and
	// for the same reason: a close, like a merge, is not conveniently undone.
	//
	// ⚠️ It is driven against the issue Options.UnknownIssue names, which does
	// NOT exist, and that is what makes the ordering observable from outside: a
	// connector that validated first refuses with cerr.KindInvalid, and one that
	// looked the issue up first reports cerr.KindNotFound. It also means this
	// check cannot close anything even against a connector that ignores the
	// guard entirely.
	CheckCloseRefusesUnspecifiedReason = "issues/close-refuses-unspecified-reason"
	// CheckCloseIsIdempotent covers a close of an ALREADY-CLOSED issue being a
	// success. A caller cannot know whether its previous pass completed, so it
	// cannot avoid a second one; a connector that failed here would make an
	// ordinary second sweep report errors that describe nothing wrong.
	//
	// ⚠️ This is the one WRITE this harness performs, and it is safe precisely
	// because it is a no-op: the fixture is already closed, so the postcondition
	// already holds. The check confirms that with a read BEFORE it writes, and
	// refuses to proceed if the fixture is not closed — a mis-specified fixture
	// must fail this run, never be closed by it.
	CheckCloseIsIdempotent = "issues/close-is-idempotent"
)

// Options are the fixtures a conformance run needs. Every field is required:
// a check that cannot be driven is not skipped, because a report that is
// green because nothing looked is the failure this harness exists to avoid.
type Options struct {
	// Manifest is the connector.yml this connector ships.
	Manifest manifest.Doc

	// Repo is a repository (fullName, "<owner>/<name>") this connector MUST
	// find: at least one open pull/merge request, at least one branch.
	Repo string
	// UnknownRepo is a repository this connector MUST NOT find: absent, or
	// outside what this connector serves.
	UnknownRepo string

	// CheckedRef is a ref within Repo this connector MUST report at least one
	// check/status entry for.
	CheckedRef string

	// DiffPR is a pull/merge request id within Repo with at least one changed
	// file.
	DiffPR string
	// UnknownPR is a pull/merge request id this connector MUST NOT find
	// within Repo.
	UnknownPR string

	// DraftPR is a pull/merge request id within Repo that IS currently a
	// draft. MergePR against it, with a valid method, MUST be refused.
	DraftPR string
	// OpenPR is a pull/merge request id within Repo that is open and NOT a
	// draft. MergePR against it, with codeci.MergeMethodUnspecified, MUST be
	// refused before any merge is attempted — this fixture is never merged by
	// this run.
	OpenPR string

	// ProtectedBranch is a branch within Repo the code host DOES protect.
	ProtectedBranch string
	// UnprotectedBranch is a branch within Repo the code host does NOT
	// protect.
	//
	// Both branch fixtures are required, and neither alone is enough: with only
	// the protected one, a connector hard-coding Protected: true passes; with
	// only the unprotected one, one hard-coding false passes. The pair is what
	// makes the field a report rather than an assertion.
	UnprotectedBranch string

	// OpenIssue is an issue number within Repo the code host reports as OPEN.
	OpenIssue int
	// ClosedIssue is an issue number within Repo the code host reports as
	// CLOSED.
	//
	// ⚠️ This run CLOSES it again, to drive the idempotency obligation. That is
	// a no-op on an issue that is already closed — and the run confirms it is
	// closed before writing, so a fixture naming an OPEN issue fails the check
	// rather than being closed by it.
	//
	// Both issue-state fixtures are required, and neither alone is enough: with
	// only the open one, a connector hard-coding IssueStateOpen passes; with only
	// the closed one, one hard-coding IssueStateClosed passes.
	ClosedIssue int
	// UnknownIssue is an issue number within Repo that does NOT exist.
	//
	// It drives the read's unresolvable case, and it is also what the close's
	// reason-guard is driven against — so that guard can be checked without any
	// issue being closeable at all.
	UnknownIssue int
}

// A Result is the outcome of a single named check.
type Result struct {
	// Name is one of the Check* constants.
	Name string
	// Pass reports whether the check succeeded.
	Pass bool
	// Detail explains the outcome. It is always populated for a failure and
	// may be empty for a pass.
	Detail string
}

// A Report is the outcome of a conformance run.
type Report struct {
	// Connector is the name the manifest gives the connector under test, so a
	// failure is attributable without cross-referencing the run.
	Connector string
	// Results holds one entry per check that was run, in run order.
	Results []Result
}

// OK reports whether every check that ran passed.
func (r Report) OK() bool { return len(r.Failures()) == 0 }

// Failures returns the failed checks, in run order.
func (r Report) Failures() []Result {
	var out []Result
	for _, res := range r.Results {
		if !res.Pass {
			out = append(out, res)
		}
	}
	return out
}

// Err returns nil when the run passed, and otherwise a cerr of kind
// cerr.KindInvalid naming the connector and every failed check. The connector
// ran; it is its behaviour that is wrong, which is why this is Invalid rather
// than Unavailable.
func (r Report) Err() error {
	failed := r.Failures()
	if len(failed) == 0 {
		return nil
	}
	parts := make([]string, 0, len(failed))
	for _, f := range failed {
		parts = append(parts, fmt.Sprintf("%s: %s", f.Name, f.Detail))
	}
	return cerr.New(cerr.KindInvalid, "codeciconform",
		fmt.Errorf("connector %q: %s", r.connectorName(), strings.Join(parts, "; ")))
}

// String renders the report as one line per check, for CI logs.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "codeciconform: connector=%s", r.connectorName())
	for _, res := range r.Results {
		status := "PASS"
		if !res.Pass {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "\n  %s %s", status, res.Name)
		if res.Detail != "" {
			fmt.Fprintf(&b, ": %s", res.Detail)
		}
	}
	return b.String()
}

func (r Report) connectorName() string {
	if r.Connector == "" {
		return "<unnamed>"
	}
	return r.Connector
}

func (r *Report) add(name string, pass bool, detail string) {
	r.Results = append(r.Results, Result{Name: name, Pass: pass, Detail: detail})
}

// requiredFixture names one Options field this run refuses to proceed
// without, together with why an undriven check is not a passing one.
type requiredFixture struct{ field, value, why string }

// Run checks c against the parts of the [codeci.CodeCI] contract a compiler
// cannot enforce.
//
// The returned error is non-nil only when the run could not be carried out at
// all — no connector, missing fixtures. A connector that ran and is
// non-conformant yields a nil error and a Report whose Err reports the
// failures. Gate on Report.Err; log Report either way, because its per-check
// lines are what a reviewer reads.
func Run(ctx context.Context, c codeci.CodeCI, opts Options) (Report, error) {
	if c == nil {
		return Report{}, cerr.New(cerr.KindInvalid, "codeciconform.Run", fmt.Errorf("nil connector"))
	}
	for _, missing := range []requiredFixture{
		{"Repo", opts.Repo, "without a repository the connector must find, none of the read checks are ever exercised"},
		{"UnknownRepo", opts.UnknownRepo, "without a repository the connector must fail on, its failure classification is never exercised"},
		{"CheckedRef", opts.CheckedRef, "without a ref with check runs, GetCheckRuns's no-empty-success property is never exercised"},
		{"DiffPR", opts.DiffPR, "without a PR with changed files, GetDiff's no-empty-success property is never exercised"},
		{"UnknownPR", opts.UnknownPR, "without a PR the connector must fail to find, GetDiff's fail-closed property is never exercised"},
		{"DraftPR", opts.DraftPR, "without a draft PR, MergePR's draft-refusal obligation is never exercised"},
		{"OpenPR", opts.OpenPR, "without an open, non-draft PR, MergePR's method-validation obligation is never exercised"},
		{"ProtectedBranch", opts.ProtectedBranch, "without a branch the code host protects, a connector reporting Protected: false for every branch is never caught — and false reads as \"this branch is unprotected\""},
		{"UnprotectedBranch", opts.UnprotectedBranch, "without a branch the code host does NOT protect, a connector reporting Protected: true for every branch is never caught"},
	} {
		if missing.value == "" {
			return Report{}, cerr.New(cerr.KindInvalid, "codeciconform.Run", fmt.Errorf("fixture Options.%s is unset: %s", missing.field, missing.why))
		}
	}
	// The issue fixtures are numbers, so "unset" is a separate test: both code
	// hosts number issues from 1, which makes a non-positive number unset rather
	// than an unusual address.
	for _, missing := range []struct {
		field string
		value int
		why   string
	}{
		{"OpenIssue", opts.OpenIssue, "without an issue the code host reports as open, a connector reporting IssueStateClosed for every issue is never caught — and that reading tells an operator to start work that is still blocked"},
		{"ClosedIssue", opts.ClosedIssue, "without an issue the code host reports as closed, a connector reporting IssueStateOpen for every issue is never caught, and CloseIssue's idempotency obligation is never exercised"},
		{"UnknownIssue", opts.UnknownIssue, "without an issue number the connector must fail to find, neither the read's unresolvable-is-a-failure obligation nor CloseIssue's reason guard is ever exercised"},
	} {
		if missing.value <= 0 {
			return Report{}, cerr.New(cerr.KindInvalid, "codeciconform.Run", fmt.Errorf("fixture Options.%s is unset: %s", missing.field, missing.why))
		}
	}

	rep := Report{Connector: opts.Manifest.Name}

	checkManifest(&rep, opts.Manifest)
	checkClass(&rep, c)
	checkCapabilityHonesty(&rep, c, opts.Manifest)
	checkOptionalDeclared(&rep, c)
	checkListsNoEmptySuccess(ctx, &rep, c, opts)
	checkListFailClosed(ctx, &rep, c, opts)
	checkMergeRefusesUnspecifiedMethod(ctx, &rep, c, opts)
	checkMergeRefusesDraft(ctx, &rep, c, opts)
	checkCreateRefusesIncompleteRequest(ctx, &rep, c, opts)
	checkGetPRUnresolvableIsAFailure(ctx, &rep, c, opts)
	checkCommentRefusesEmptyBody(ctx, &rep, c, opts)
	checkBranchProtectionReported(ctx, &rep, c, opts)
	checkPRsCarryAURL(ctx, &rep, c, opts)
	checkDraftIsReported(ctx, &rep, c, opts)
	checkIdentity(ctx, &rep, c)
	checkHealth(ctx, &rep, c)
	checkIssueStateReported(ctx, &rep, c, opts)
	checkIssueUnresolvableIsAFailure(ctx, &rep, c, opts)
	checkCloseRefusesUnspecifiedReason(ctx, &rep, c, opts)
	checkCloseIsIdempotent(ctx, &rep, c, opts)

	return rep, nil
}

func checkManifest(rep *Report, m manifest.Doc) {
	if err := m.Validate(); err != nil {
		rep.add(CheckManifest, false, err.Error())
		return
	}
	if m.Implements != connector.ClassCodeCI {
		rep.add(CheckManifest, false, fmt.Sprintf(
			"manifest implements %q; this harness checks the %q class", m.Implements, connector.ClassCodeCI))
		return
	}
	// Capability-vocabulary closure is not re-implemented here.
	// manifest.Doc.Validate, called above, rejects a capability outside the
	// vocabulary of the class the manifest implements — which is where the
	// check belongs, because a host runs Validate before it loads anything,
	// with no connector and no fixtures. Duplicating it in the harness would
	// be a second copy of a closed vocabulary, free to drift from the first.
	rep.add(CheckManifest, true, "")
}

func checkClass(rep *Report, c codeci.CodeCI) {
	if got := c.Implements(); got != connector.ClassCodeCI {
		rep.add(CheckClass, false, fmt.Sprintf(
			"Implements() reports %q; a host routes by class and would dispatch this connector as something it is not", got))
		return
	}
	rep.add(CheckClass, true, "")
}

func checkCapabilityHonesty(rep *Report, c codeci.CodeCI, m manifest.Doc) {
	runtime := c.Capabilities()
	declared := connector.Capabilities(m.Capabilities)

	var missing, undeclared []string
	for _, want := range declared {
		if !runtime.Has(want) {
			missing = append(missing, string(want))
		}
	}
	for _, got := range runtime {
		if !declared.Has(got) {
			undeclared = append(undeclared, string(got))
		}
	}
	switch {
	case len(missing) > 0 && len(undeclared) > 0:
		rep.add(CheckCapabilityHonesty, false, fmt.Sprintf(
			"manifest declares %s which Capabilities() does not report, and Capabilities() reports %s which the manifest does not declare",
			strings.Join(missing, ", "), strings.Join(undeclared, ", ")))
	case len(missing) > 0:
		rep.add(CheckCapabilityHonesty, false, fmt.Sprintf(
			"manifest declares %s, which the running connector does not report. A host plans for what the manifest promises",
			strings.Join(missing, ", ")))
	case len(undeclared) > 0:
		rep.add(CheckCapabilityHonesty, false, fmt.Sprintf(
			"the running connector reports %s, which the manifest does not declare. The manifest is what a host reads before it ever loads the connector",
			strings.Join(undeclared, ", ")))
	default:
		rep.add(CheckCapabilityHonesty, true, "")
	}
}

// optionalOps pairs each capability that has an operation behind it with the
// interface assertion that answers whether the operation is there.
//
// ⚠️ CheckPublisher is a NEW optional tier, never a method on CIController:
// adding PublishCheck there would break every existing implementer at compile
// time in THEIR repository, after release. The two stay distinct permissions.
var optionalOps = []struct {
	capability connector.Capability
	implements func(codeci.CodeCI) bool
}{
	{codeci.CapCIControl, func(c codeci.CodeCI) bool { _, ok := c.(codeci.CIController); return ok }},
	{codeci.CapCheckPublish, func(c codeci.CodeCI) bool { _, ok := c.(codeci.CheckPublisher); return ok }},
}

// checkOptionalDeclared fails in both directions, and "implemented" is a Go
// type assertion against the connector's own type — never derived from
// Capabilities(). Deriving it from the declaration would make this check
// agree with itself no matter what the connector does, which is the bug the
// credential class's Track-B batch check already shipped once (see its own
// doc for the narrower guarantee that leaves behind).
func checkOptionalDeclared(rep *Report, c codeci.CodeCI) {
	runtime := c.Capabilities()
	var problems []string
	for _, op := range optionalOps {
		declared := runtime.Has(op.capability)
		implemented := op.implements(c)
		switch {
		case declared && !implemented:
			problems = append(problems, fmt.Sprintf(
				"%s is declared but the operation is not implemented: a host that plans for it calls into nothing", op.capability))
		case !declared && implemented:
			problems = append(problems, fmt.Sprintf(
				"%s is implemented but not declared: a host reads the manifest before it loads the connector, so it will never use it", op.capability))
		}
	}
	if len(problems) > 0 {
		rep.add(CheckOptionalDeclared, false, strings.Join(problems, "; "))
		return
	}
	rep.add(CheckOptionalDeclared, true, fmt.Sprintf("%d optional operations checked", len(optionalOps)))
}

func checkListsNoEmptySuccess(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	prs, err := c.ListPRs(ctx, opts.Repo, codeci.PRStateAny)
	if err != nil {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("ListPRs(%s) failed on a repository the fixtures say it must list: %v", opts.Repo, err))
		return
	}
	if items, ierr := prs.Items(); ierr != nil {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("ListPRs(%s) reported success with a resolution that carries no list: %v", opts.Repo, ierr))
		return
	} else if len(items) == 0 {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("ListPRs(%s) resolved to no pull/merge requests; the fixtures name a repository that has at least one open", opts.Repo))
		return
	}

	branches, err := c.ListBranches(ctx, opts.Repo)
	if err != nil {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("ListBranches(%s) failed on a repository the fixtures say it must list: %v", opts.Repo, err))
		return
	}
	if items, ierr := branches.Items(); ierr != nil {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("ListBranches(%s) reported success with a resolution that carries no list: %v", opts.Repo, ierr))
		return
	} else if len(items) == 0 {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("ListBranches(%s) resolved to no branches; the fixtures name a repository that has at least one", opts.Repo))
		return
	}

	checks, err := c.GetCheckRuns(ctx, opts.Repo, opts.CheckedRef)
	if err != nil {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("GetCheckRuns(%s, %s) failed on a ref the fixtures say has check runs: %v", opts.Repo, opts.CheckedRef, err))
		return
	}
	if items, ierr := checks.Items(); ierr != nil {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("GetCheckRuns(%s, %s) reported success with a resolution that carries no list: %v", opts.Repo, opts.CheckedRef, ierr))
		return
	} else if len(items) == 0 {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("GetCheckRuns(%s, %s) resolved to no check runs; the fixtures name a ref that has at least one", opts.Repo, opts.CheckedRef))
		return
	}

	diff, err := c.GetDiff(ctx, opts.Repo, opts.DiffPR)
	if err != nil {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("GetDiff(%s, %s) failed on a PR the fixtures say has changed files: %v", opts.Repo, opts.DiffPR, err))
		return
	}
	if items, ierr := diff.Items(); ierr != nil {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf("GetDiff(%s, %s) reported success with a resolution that carries no list: %v", opts.Repo, opts.DiffPR, ierr))
		return
	} else if len(items) == 0 {
		rep.add(CheckListsNoEmptySuccess, false, fmt.Sprintf(
			"GetDiff(%s, %s) resolved to no changed files; a real pull/merge request always has at least one, so this is a broken read, not an empty diff", opts.Repo, opts.DiffPR))
		return
	}

	rep.add(CheckListsNoEmptySuccess, true, "ListPRs, ListBranches, GetCheckRuns and GetDiff all resolved with entries")
}

func checkListFailClosed(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	prs, err := c.ListPRs(ctx, opts.UnknownRepo, codeci.PRStateAny)
	if err == nil {
		rep.add(CheckListFailClosed, false, fmt.Sprintf("ListPRs(%s) succeeded on a repository the fixtures say it must not find", opts.UnknownRepo))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckListFailClosed, false, fmt.Sprintf("ListPRs(%s) failed with an unclassified error: %v", opts.UnknownRepo, err))
		return
	}
	if _, ierr := prs.Items(); ierr == nil {
		rep.add(CheckListFailClosed, false, fmt.Sprintf(
			"ListPRs(%s) failed with %s and STILL returned a readable resolution; a caller that ignored the error reads the failure as a repository with no open PRs",
			opts.UnknownRepo, cerr.KindOf(err)))
		return
	}

	diff, err := c.GetDiff(ctx, opts.Repo, opts.UnknownPR)
	if err == nil {
		rep.add(CheckListFailClosed, false, fmt.Sprintf("GetDiff(%s, %s) succeeded on a PR the fixtures say does not exist", opts.Repo, opts.UnknownPR))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckListFailClosed, false, fmt.Sprintf("GetDiff(%s, %s) failed with an unclassified error: %v", opts.Repo, opts.UnknownPR, err))
		return
	}
	if _, ierr := diff.Items(); ierr == nil {
		rep.add(CheckListFailClosed, false, fmt.Sprintf(
			"GetDiff(%s, %s) failed with %s and STILL returned a readable resolution; a caller that ignored the error reads the failure as a PR with no changed files",
			opts.Repo, opts.UnknownPR, cerr.KindOf(err)))
		return
	}

	rep.add(CheckListFailClosed, true, fmt.Sprintf(
		"ListPRs(%s) -> %s, GetDiff(%s, %s) -> %s, both resolutions unreadable",
		opts.UnknownRepo, cerr.KindOf(err), opts.Repo, opts.UnknownPR, cerr.KindOf(err)))
}

// checkMergeRefusesUnspecifiedMethod calls MergePR against a REAL, open,
// non-draft pull/merge request with codeci.MergeMethodUnspecified. It never
// merges OpenPR: the contract requires method validation to happen BEFORE
// any merge is attempted, which is exactly what makes this call safe to run
// repeatedly against a live fixture.
func checkMergeRefusesUnspecifiedMethod(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	err := c.MergePR(ctx, opts.Repo, opts.OpenPR, codeci.MergeMethodUnspecified)
	if err == nil {
		rep.add(CheckMergeRefusesUnspecifiedMethod, false, fmt.Sprintf(
			"MergePR(%s, %s, MergeMethodUnspecified) succeeded; the fixtures name %s as mergeable ONLY so this refusal can be exercised without merging it — an unspecified method must be refused before any merge is attempted",
			opts.Repo, opts.OpenPR, opts.OpenPR))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckMergeRefusesUnspecifiedMethod, false, fmt.Sprintf(
			"MergePR(%s, %s, MergeMethodUnspecified) failed with an unclassified error: %v", opts.Repo, opts.OpenPR, err))
		return
	}
	rep.add(CheckMergeRefusesUnspecifiedMethod, true, fmt.Sprintf("refused: %s", cerr.KindOf(err)))
}

// checkMergeRefusesDraft calls MergePR against a real draft pull/merge
// request with a VALID method. A connector correctly implementing the
// refusal never merges DraftPR, so this call is safe to run repeatedly; a
// connector that does NOT implement it merges the fixture, which is a loud,
// legible failure of this check rather than a silent one.
func checkMergeRefusesDraft(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	err := c.MergePR(ctx, opts.Repo, opts.DraftPR, codeci.MergeMethodMerge)
	if err == nil {
		rep.add(CheckMergeRefusesDraft, false, fmt.Sprintf(
			"MergePR(%s, %s, MergeMethodMerge) succeeded; the fixtures name %s as a DRAFT specifically so this refusal can be exercised — a draft pull/merge request must never be merged",
			opts.Repo, opts.DraftPR, opts.DraftPR))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckMergeRefusesDraft, false, fmt.Sprintf(
			"MergePR(%s, %s, MergeMethodMerge) failed with an unclassified error: %v", opts.Repo, opts.DraftPR, err))
		return
	}
	rep.add(CheckMergeRefusesDraft, true, fmt.Sprintf("refused: %s", cerr.KindOf(err)))
}

// checkCreateRefusesIncompleteRequest calls CreatePR for real, against the live
// Repo fixture, with a request missing its Branch, Base and Title. A conformant
// connector validates the request before it does anything else, which is
// exactly what makes this call non-destructive: it never reaches the code host.
// A connector that skipped the validation opens a real pull/merge request with
// no title — a loud, legible failure of this check rather than a silent one, and
// the reason the contract puts the guard in CreatePR rather than in callers.
//
// The request omits three fields rather than one so the failure names the shape
// (a struct field left unset) rather than one field a connector might special-case.
func checkCreateRefusesIncompleteRequest(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	incomplete := codeci.CreatePRRequest{FullName: opts.Repo}
	pr, err := c.CreatePR(ctx, incomplete)
	if err == nil {
		rep.add(CheckCreateRefusesIncompleteRequest, false, fmt.Sprintf(
			"CreatePR(%s) opened %q from a request carrying no branch, base or title; a request is a struct, so an unset field is silent at the call site, and the connector is where the contract puts the guard",
			opts.Repo, pr.ID))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckCreateRefusesIncompleteRequest, false, fmt.Sprintf(
			"CreatePR(%s) refused an incomplete request with an unclassified error: %v", opts.Repo, err))
		return
	}
	rep.add(CheckCreateRefusesIncompleteRequest, true, fmt.Sprintf("refused: %s", cerr.KindOf(err)))
}

// checkGetPRUnresolvableIsAFailure asks for a pull/merge request that does not
// exist and insists the answer is a classified failure.
//
// ⚠️ It checks the ERROR and the VALUE, because the two failure shapes are
// different bugs: returning nil error with a zero PR is a connector that
// invented a request, and returning an unclassified error is one a host cannot
// act on without reading a message.
func checkGetPRUnresolvableIsAFailure(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	pr, err := c.GetPR(ctx, opts.Repo, opts.UnknownPR)
	if err == nil {
		rep.add(CheckGetPRUnresolvableIsAFailure, false, fmt.Sprintf(
			"GetPR(%s, %s) succeeded for a pull/merge request that does not exist, returning ID %q; the zero PR is a "+
				"valid-looking request numbered 0, so a caller acts on nothing while believing it read something",
			opts.Repo, opts.UnknownPR, pr.ID))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckGetPRUnresolvableIsAFailure, false, fmt.Sprintf(
			"GetPR(%s, %s) failed with an unclassified error, so a host cannot act on it without reading the message: %v",
			opts.Repo, opts.UnknownPR, err))
		return
	}
	rep.add(CheckGetPRUnresolvableIsAFailure, true, fmt.Sprintf("refused: %s", cerr.KindOf(err)))
}

// checkCommentRefusesEmptyBody drives the empty-body guard.
//
// ⚠️ It asserts the refusal came back with no comment identifier, because a
// connector that posted an empty comment and then reported an error would pass a
// check that looked only at err.
func checkCommentRefusesEmptyBody(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	id, err := c.CommentPR(ctx, opts.Repo, opts.OpenPR, "")
	if err == nil {
		rep.add(CheckCommentRefusesEmptyBody, false, fmt.Sprintf(
			"CommentPR(%s, %s, \"\") posted an empty comment (id %q); an empty comment on someone's change is an "+
				"artefact a human has to find and delete", opts.Repo, opts.OpenPR, id))
		return
	}
	if id != "" {
		rep.add(CheckCommentRefusesEmptyBody, false, fmt.Sprintf(
			"CommentPR(%s, %s, \"\") returned an error AND a comment id %q, so something was posted before the refusal",
			opts.Repo, opts.OpenPR, id))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckCommentRefusesEmptyBody, false, fmt.Sprintf(
			"CommentPR(%s, %s, \"\") refused with an unclassified error: %v", opts.Repo, opts.OpenPR, err))
		return
	}
	rep.add(CheckCommentRefusesEmptyBody, true, fmt.Sprintf("refused: %s", cerr.KindOf(err)))
}

// checkBranchProtectionReported drives codeci.Branch.Protected in BOTH
// directions, because either constant is a lie and only one of them is caught
// by looking at a protected branch. always-false is the dangerous one — it reads
// as "this branch is unprotected", so a caller asking whether it may force-push
// gets a yes nothing computed.
func checkBranchProtectionReported(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	res, err := c.ListBranches(ctx, opts.Repo)
	if err != nil {
		rep.add(CheckBranchProtectionReported, false, fmt.Sprintf(
			"ListBranches(%s) failed on a repository the fixtures say it must list: %v", opts.Repo, err))
		return
	}
	items, ierr := res.Items()
	if ierr != nil {
		rep.add(CheckBranchProtectionReported, false, fmt.Sprintf(
			"ListBranches(%s) reported success with a resolution that carries no list: %v", opts.Repo, ierr))
		return
	}

	protection := make(map[string]bool, len(items))
	for _, b := range items {
		protection[b.Name] = b.Protected
	}
	for _, want := range []struct {
		field, branch string
		protected     bool
		why           string
	}{
		{"ProtectedBranch", opts.ProtectedBranch, true,
			"a connector reporting false for a branch the code host protects tells a caller it may force-push, which is the one always-zero field in this class that is actively dangerous"},
		{"UnprotectedBranch", opts.UnprotectedBranch, false,
			"a connector reporting true for every branch has asserted a constant, not read the code host — and a check that only looked at the protected fixture would accept it"},
	} {
		got, listed := protection[want.branch]
		if !listed {
			rep.add(CheckBranchProtectionReported, false, fmt.Sprintf(
				"ListBranches(%s) did not report the branch %q named by Options.%s; the fixture cannot be measured, and an unmeasured field is not a passing one",
				opts.Repo, want.branch, want.field))
			return
		}
		if got != want.protected {
			rep.add(CheckBranchProtectionReported, false, fmt.Sprintf(
				"branch %q (Options.%s) reports Protected=%t, want %t: %s",
				want.branch, want.field, got, want.protected, want.why))
			return
		}
	}
	rep.add(CheckBranchProtectionReported, true, fmt.Sprintf(
		"%s reports protected, %s reports unprotected", opts.ProtectedBranch, opts.UnprotectedBranch))
}

// checkPRsCarryAURL reads codeci.PR.URL off the same fixture ListPRs must
// already find entries for. The field exists so a caller never assembles a
// vendor URL itself; an empty one hands that job straight back.
func checkPRsCarryAURL(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	res, err := c.ListPRs(ctx, opts.Repo, codeci.PRStateAny)
	if err != nil {
		rep.add(CheckPRsCarryAURL, false, fmt.Sprintf(
			"ListPRs(%s) failed on a repository the fixtures say it must list: %v", opts.Repo, err))
		return
	}
	items, ierr := res.Items()
	if ierr != nil {
		rep.add(CheckPRsCarryAURL, false, fmt.Sprintf(
			"ListPRs(%s) reported success with a resolution that carries no list: %v", opts.Repo, ierr))
		return
	}
	for _, pr := range items {
		if pr.URL == "" {
			rep.add(CheckPRsCarryAURL, false, fmt.Sprintf(
				"pull/merge request %q carries no URL; the field exists so a caller never has to assemble a vendor URL, and an empty one pushes that vendor knowledge back out to every caller",
				pr.ID))
			return
		}
	}
	rep.add(CheckPRsCarryAURL, true, fmt.Sprintf("%d pull/merge request(s), each with a URL", len(items)))
}

// checkDraftIsReported reads the list and asserts the fixture named as a draft
// comes back as one.
//
// It is a READ, so it is safe to repeat — and it is the ONLY draft assertion
// this harness can make. See the package doc on what CreatePR's obligations
// cost to check: producing a draft cannot be undone by the harness that
// produced it, so the create half is stated rather than driven.
func checkDraftIsReported(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	res, err := c.ListPRs(ctx, opts.Repo, codeci.PRStateAny)
	if err != nil {
		rep.add(CheckDraftIsReported, false, fmt.Sprintf(
			"ListPRs(%s) failed on a repository the fixtures say it must list: %v", opts.Repo, err))
		return
	}
	items, ierr := res.Items()
	if ierr != nil {
		rep.add(CheckDraftIsReported, false, fmt.Sprintf(
			"ListPRs(%s) reported success with a resolution that carries no list: %v", opts.Repo, ierr))
		return
	}
	for _, pr := range items {
		if pr.ID != opts.DraftPR {
			continue
		}
		if !pr.Draft {
			rep.add(CheckDraftIsReported, false, fmt.Sprintf(
				"pull/merge request %q is named by the fixtures as a draft and came back with Draft=false; the contract "+
					"REFUSES to merge a draft, so a draft reported as ordinary is not a smaller answer — it is the one "+
					"reading under which that refusal can never fire", pr.ID))
			return
		}
		rep.add(CheckDraftIsReported, true, fmt.Sprintf("%s reported as a draft", pr.ID))
		return
	}
	rep.add(CheckDraftIsReported, false, fmt.Sprintf(
		"ListPRs(%s) returned %d pull/merge request(s) and none of them is %q, which the fixtures name as a draft; a "+
			"list that omits it cannot be checked for the draft it was pointed at", opts.Repo, len(items), opts.DraftPR))
}

// checkIdentity is the check issue #46 names: an empty login must be a TYPED
// FAILURE, never a smaller answer.
//
// A classified failure passes, for the same reason a classified Health failure
// does — a credential the code host rejected is unauthorised, not
// non-conformant, and that is precisely the shape the contract requires instead
// of an empty login. What does not pass is a success that names nobody.
//
// This run requires Authenticated to be true. The contract allows a coherent
// anonymous identity, for a code host that serves anonymous reads, but not here:
// every other check in this run drives operations against real pull/merge
// requests with a real credential, so a connector reporting "nobody" has
// answered wrongly rather than reported an unusual code host.
func checkIdentity(ctx context.Context, rep *Report, c codeci.CodeCI) {
	id, err := c.WhoAmI(ctx)
	if err != nil {
		if !cerr.Classified(err) {
			rep.add(CheckIdentity, false, fmt.Sprintf("WhoAmI() failed with an unclassified error: %v", err))
			return
		}
		rep.add(CheckIdentity, true, fmt.Sprintf("classified failure: %s", cerr.KindOf(err)))
		return
	}
	// Coherence is asked of codeci.Identity rather than restated here: the
	// invariant has one home, so the harness and the host-side
	// codeci.CheckIdentity guard cannot drift apart from each other.
	if !id.Coherent() {
		rep.add(CheckIdentity, false, fmt.Sprintf(
			"WhoAmI() reported success with an incoherent identity (login=%q authenticated=%t); an authenticated identity carrying no login asserts the opposite of what an identity probe exists to prove, and it does so wearing a success's shape",
			id.Login, id.Authenticated))
		return
	}
	if !id.Authenticated {
		rep.add(CheckIdentity, false, fmt.Sprintf(
			"WhoAmI() reported an anonymous identity with no login, while every other check in this run drove a real pull/merge request with this connector's credential; a rejected credential is a classified failure (%s), not a success naming nobody",
			cerr.KindUnauthorized))
		return
	}
	rep.add(CheckIdentity, true, fmt.Sprintf("login=%s", id.Login))
}

func checkHealth(ctx context.Context, rep *Report, c codeci.CodeCI) {
	h, err := c.Health(ctx)
	if err != nil {
		if !cerr.Classified(err) {
			rep.add(CheckHealth, false, fmt.Sprintf("Health() failed with an unclassified error: %v", err))
			return
		}
		rep.add(CheckHealth, true, fmt.Sprintf("classified failure: %s", cerr.KindOf(err)))
		return
	}
	switch h.Status {
	case connector.Healthy, connector.Degraded, connector.Unavailable:
		rep.add(CheckHealth, true, fmt.Sprintf("status=%d", int(h.Status)))
	default:
		rep.add(CheckHealth, false, fmt.Sprintf("Health() reported status %d, which is outside the SDK's vocabulary", int(h.Status)))
	}
}

// checkIssueStateReported drives the open and closed fixtures and requires each
// to come back as the state the fixtures name.
//
// The pair is the point. A single fixture cannot distinguish a connector that
// READ the code host from one that returns a constant, and the constant that
// would slip past an open-only check — always-closed — is the reading that tells
// an operator to start work that is still blocked.
func checkIssueStateReported(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	for _, want := range []struct {
		field  string
		number int
		state  codeci.IssueState
		why    string
	}{
		{"OpenIssue", opts.OpenIssue, codeci.IssueStateOpen,
			"a connector reporting closed for an issue the code host reports as open tells a caller its blocker is resolved, which is the one failure direction here that does damage rather than merely being wrong"},
		{"ClosedIssue", opts.ClosedIssue, codeci.IssueStateClosed,
			"a connector reporting open for every issue has asserted a constant, not read the code host — and a check that only looked at the open fixture would accept it"},
	} {
		iss, err := c.GetIssue(ctx, opts.Repo, want.number)
		if err != nil {
			rep.add(CheckIssueStateReported, false, fmt.Sprintf(
				"GetIssue(%s, %d) failed on an issue Options.%s says it must resolve: %v",
				opts.Repo, want.number, want.field, err))
			return
		}
		if !iss.State.Determined() {
			rep.add(CheckIssueStateReported, false, fmt.Sprintf(
				"GetIssue(%s, %d) reported success with State=%s; a connector that could not determine the state must fail the read rather than return a success asserting nothing, because a caller asked a yes-or-no question",
				opts.Repo, want.number, iss.State))
			return
		}
		if iss.State != want.state {
			rep.add(CheckIssueStateReported, false, fmt.Sprintf(
				"issue %d (Options.%s) reports State=%s, want %s: %s",
				want.number, want.field, iss.State, want.state, want.why))
			return
		}
		if iss.FullName != opts.Repo || iss.Number != want.number {
			rep.add(CheckIssueStateReported, false, fmt.Sprintf(
				"GetIssue(%s, %d) answered about %s#%d; the read must echo the address it was asked for, because a bare state carries no evidence of which issue it is the state of",
				opts.Repo, want.number, iss.FullName, iss.Number))
			return
		}
	}
	rep.add(CheckIssueStateReported, true, fmt.Sprintf(
		"%d reports open, %d reports closed", opts.OpenIssue, opts.ClosedIssue))
}

// checkIssueUnresolvableIsAFailure drives the read against an issue that does
// not exist. The obligation is asymmetric on purpose: any classified failure
// passes, and ANY success fails.
//
// A classified failure passes for the same reason a classified Health failure
// does — a code host that has no such issue, or a credential that cannot see
// it, is exactly what the contract asks be reported as a failure. What does not
// pass is a value: a zero-valued Issue is as wrong as a confidently closed one,
// because the caller's next move is to read a field off it.
func checkIssueUnresolvableIsAFailure(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	iss, err := c.GetIssue(ctx, opts.Repo, opts.UnknownIssue)
	if err == nil {
		rep.add(CheckIssueUnresolvableIsAFailure, false, fmt.Sprintf(
			"GetIssue(%s, %d) reported SUCCESS with State=%s for an issue Options.UnknownIssue says does not exist; "+
				"an unresolvable issue must reach the caller as a typed failure, because a caller deciding whether work is "+
				"blocked reads %s as permission to start it",
			opts.Repo, opts.UnknownIssue, iss.State, codeci.IssueStateClosed))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckIssueUnresolvableIsAFailure, false, fmt.Sprintf(
			"GetIssue(%s, %d) failed with an unclassified error: %v; a host acts on the classification, never on the message",
			opts.Repo, opts.UnknownIssue, err))
		return
	}
	switch kind := cerr.KindOf(err); kind {
	case cerr.KindNotFound, cerr.KindUnauthorized:
		rep.add(CheckIssueUnresolvableIsAFailure, true, fmt.Sprintf("classified failure: %s", kind))
	default:
		rep.add(CheckIssueUnresolvableIsAFailure, false, fmt.Sprintf(
			"GetIssue(%s, %d) failed with %s; an issue that does not exist is %s, and one the credential cannot see is %s — "+
				"a caller distinguishes a deleted issue from a wrong token by the classification alone",
			opts.Repo, opts.UnknownIssue, kind, cerr.KindNotFound, cerr.KindUnauthorized))
	}
}

// checkCloseRefusesUnspecifiedReason drives CloseIssue with the zero reason.
//
// ⚠️ It is aimed at Options.UnknownIssue, which does not exist, and that choice
// carries the whole check. It makes the ORDER observable — a connector that
// validated its argument first answers Invalid, and one that resolved the issue
// first answers NotFound — and it means this check cannot close anything even
// against a connector with no guard at all. Driving it at a real open issue
// would test the same obligation while betting a real issue on the answer.
func checkCloseRefusesUnspecifiedReason(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	err := c.CloseIssue(ctx, opts.Repo, opts.UnknownIssue, codeci.CloseReasonUnspecified)
	if err == nil {
		rep.add(CheckCloseRefusesUnspecifiedReason, false, fmt.Sprintf(
			"CloseIssue(%s, %d, %s) reported success; a close naming no reason is a caller that has not decided, and recording one on its behalf writes a guess into an audit trail",
			opts.Repo, opts.UnknownIssue, codeci.CloseReasonUnspecified))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckCloseRefusesUnspecifiedReason, false, fmt.Sprintf(
			"CloseIssue refused with an unclassified error: %v", err))
		return
	}
	if kind := cerr.KindOf(err); kind != cerr.KindInvalid {
		rep.add(CheckCloseRefusesUnspecifiedReason, false, fmt.Sprintf(
			"CloseIssue(%s, %d, %s) failed with %s, want %s. The issue named does not exist, so %s is the answer of a connector that "+
				"resolved the issue BEFORE validating its argument — the guard has to run first, the way MergePR's method guard does, "+
				"because by the time a close is attempted the cheap refusal is no longer available",
			opts.Repo, opts.UnknownIssue, codeci.CloseReasonUnspecified, kind, cerr.KindInvalid, cerr.KindNotFound))
		return
	}
	rep.add(CheckCloseRefusesUnspecifiedReason, true, fmt.Sprintf(
		"refused with %s before resolving the issue", cerr.KindInvalid))
}

// checkCloseIsIdempotent closes an issue that is already closed.
//
// ⚠️ This is the only write this harness performs against a connector's backing
// host, and it is safe because the postcondition already holds: closing a closed
// issue changes nothing. The read that precedes it is a SAFETY INTERLOCK, not a
// convenience — a fixture that names an open issue by mistake must fail this
// check rather than have this harness close a real issue to find out.
func checkCloseIsIdempotent(ctx context.Context, rep *Report, c codeci.CodeCI, opts Options) {
	before, err := c.GetIssue(ctx, opts.Repo, opts.ClosedIssue)
	if err != nil {
		rep.add(CheckCloseIsIdempotent, false, fmt.Sprintf(
			"GetIssue(%s, %d) failed on the issue Options.ClosedIssue names, so this check cannot confirm the close it is about to perform is a no-op, and it will not perform one blind: %v",
			opts.Repo, opts.ClosedIssue, err))
		return
	}
	if before.State != codeci.IssueStateClosed {
		rep.add(CheckCloseIsIdempotent, false, fmt.Sprintf(
			"Options.ClosedIssue names issue %d, which reports State=%s. This check closes that issue, which is only safe while it is already closed — so a fixture pointing at an issue that is not closed fails here rather than being closed by this run",
			opts.ClosedIssue, before.State))
		return
	}

	if err := c.CloseIssue(ctx, opts.Repo, opts.ClosedIssue, codeci.CloseReasonCompleted); err != nil {
		rep.add(CheckCloseIsIdempotent, false, fmt.Sprintf(
			"CloseIssue(%s, %d) failed against an issue that is ALREADY closed: %v. The caller's intent is that the issue end up closed, and it already is — a caller cannot know whether its previous pass completed, so it cannot avoid a second one, and failing here makes an ordinary second sweep report errors that describe nothing wrong",
			opts.Repo, opts.ClosedIssue, err))
		return
	}
	rep.add(CheckCloseIsIdempotent, true, fmt.Sprintf("re-closing %d succeeded", opts.ClosedIssue))
}
