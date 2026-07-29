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
//		Manifest:    myManifest,
//		Repo:        "org/populated-repo",
//		UnknownRepo: "org/does-not-exist",
//		CheckedRef:  "main",
//		DiffPR:      "1",
//		UnknownPR:   "999999",
//		DraftPR:     "2",
//		OpenPR:      "3",
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
// empty list as a success, one that merges a draft, one whose manifest
// declares a capability it does not implement. A harness only ever run
// against compliant input proves nothing about what it would catch.
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
	// CheckOptionalDeclared covers CapCIControl being declared exactly when
	// codeci.CIController is implemented. "Implemented" is a Go interface
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
	// CheckHealth covers Health() answering: a status, or a classified failure.
	CheckHealth = "health/answers"
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
	} {
		if missing.value == "" {
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
	checkHealth(ctx, &rep, c)

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

// checkOptionalDeclared fails in both directions, and "implemented" is a Go
// type assertion against the connector's own type — never derived from
// Capabilities(). Deriving it from the declaration would make this check
// agree with itself no matter what the connector does, which is the bug the
// credential class's Track-B batch check already shipped once (see its own
// doc for the narrower guarantee that leaves behind).
func checkOptionalDeclared(rep *Report, c codeci.CodeCI) {
	runtime := c.Capabilities()
	_, implemented := c.(codeci.CIController)
	declared := runtime.Has(codeci.CapCIControl)
	switch {
	case declared && !implemented:
		rep.add(CheckOptionalDeclared, false, fmt.Sprintf(
			"%s is declared but the connector does not implement codeci.CIController: a host that plans to retry a failed run calls into nothing", codeci.CapCIControl))
	case !declared && implemented:
		rep.add(CheckOptionalDeclared, false, fmt.Sprintf(
			"the connector implements codeci.CIController but does not declare %s: a host reads the manifest before it loads the connector, so it will never use it", codeci.CapCIControl))
	case declared:
		rep.add(CheckOptionalDeclared, true, "declared and implemented")
	default:
		rep.add(CheckOptionalDeclared, true, "not declared, not implemented")
	}
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
