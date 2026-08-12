// Package codehostconform checks a [codehost.CodeHost] connector against the
// parts of the contract a compiler cannot enforce.
//
// The Go type system already refuses several ways of getting this wrong: a
// connector missing an operation does not build, and a list resolution carrying
// nothing cannot be constructed as a complete success (see
// [codehost.Resolution]). What is left are the properties that depend on
// BEHAVIOUR and on what the connector's manifest CLAIMS — and those need a run,
// the same reason [github.com/arqtiqa/arqtos-sdk-go/codeciconform] and
// [github.com/arqtiqa/arqtos-sdk-go/credconform] exist for their classes.
//
// Run it in your own CI, against your own connector, before arqtos ever loads
// it:
//
//	rep, err := codehostconform.Run(ctx, myConnector, codehostconform.Options{
//		Manifest:        myManifest,
//		ListableOwner:   "org-whose-repositories-this-connector-can-list",
//		UnlistableOwner: "org-it-cannot-list-absent-or-outside-what-it-serves",
//	})
//	if err != nil {
//		// the run could not complete — not the same as a failing check
//	}
//	if !rep.OK() {
//		log.Fatal(rep.Err())
//	}
//
// ⚠️ Options.Manifest takes the manifest the connector SHIPS, parsed from its
// connector.yml — not a [manifest.Doc] built in the test. Those two can
// disagree, and when they do this run is green while the file a host actually
// reads is wrong. This package deliberately does no file I/O, so honouring that
// is the caller's job.
//
// ⚠️ A LoadManifest(path) helper and a per-class ValidateManifest lived beside
// this harness while the class sat in arqtos-connectors, and NEITHER graduated.
// LoadManifest could not: it parsed the file through connectorkit, and an SDK
// package importing a connector repository inverts the dependency.
// ValidateManifest was the workaround for the class being unknown to
// [manifest] — capability closure is registered centrally per class now, so
// [manifest.Doc.Validate] rejects a capability outside this class's vocabulary
// on its own. Do not reintroduce either; a second copy of a closed vocabulary is
// free to drift from the first.
//
// # It graduated with its class
//
// This package and [codehost] moved here from
// arqtos-connectors/connectorkit/codehost on 2026-08-12, when CodeHost joined
// [github.com/arqtiqa/arqtos-sdk-go/connector.Classes]. The entry point was
// named Conform there and is [Run] here, matching every sibling conform package
// in this SDK.
package codehostconform

import (
	"context"
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

// Check names reported by [Conform]. They are stable identifiers: a caller may
// switch on them, and a CI job may allowlist a known failure by name.
const (
	// CheckManifest covers the connector's manifest validating and declaring
	// only capabilities this class defines.
	CheckManifest = "manifest/valid"
	// CheckClass covers the running connector reporting this class from
	// Implements(). A host routes by class, so a connector that reports
	// another one is dispatched as something it is not.
	CheckClass = "class/implements"
	// CheckCapabilityHonesty covers the manifest's declared capabilities and
	// the running connector's Capabilities() being the same set.
	CheckCapabilityHonesty = "capability/manifest-matches-runtime"
	// CheckOptionalDeclared covers each optional operation being declared
	// exactly when it is implemented. It fails in both directions: declared
	// and absent leaves a host calling into nothing, and implemented but
	// undeclared is behaviour the host will never reach for, because it reads
	// the manifest before it loads anything.
	CheckOptionalDeclared = "optional/declared-is-implemented"
	// CheckListNoEmptySuccess covers an owner the connector can list coming
	// back as a READABLE resolution carrying repositories — never as the zero
	// codehost.Resolution with a nil error, which is a success carrying nothing.
	CheckListNoEmptySuccess = "list/no-empty-success"
	// CheckListFailClosed covers an owner the connector cannot list failing
	// with a classified error AND with an unreadable resolution. Both halves
	// matter: the classification is what a host routes on, and the
	// unreadability is what stops a caller that ignored the error from reading
	// the failure as an empty code host.
	CheckListFailClosed = "list/failure-is-typed-and-fail-closed"
	// CheckHealth covers Health() answering: a status, or a classified failure.
	CheckHealth = "health/answers"
)

// Options are the fixtures a conformance run needs. Every field is required: a
// check that cannot be driven is not skipped, because a report that is green
// because nothing looked is the failure this harness exists to avoid.
type Options struct {
	// Manifest is the connector.yml this connector ships — what its author
	// wrote and what a host reads. The run compares it against the running
	// connector rather than trusting either alone.
	Manifest manifest.Doc

	// ListableOwner is an owner this connector MUST list repositories for,
	// with at least one repository. It drives the no-empty-success check,
	// which cannot be run against a connector that lists nothing.
	ListableOwner string

	// UnlistableOwner is an owner this connector MUST NOT list: absent from
	// the host, or outside what this connector serves. It drives the
	// fail-closed check, which cannot be run against a connector that never
	// fails.
	UnlistableOwner string
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
	return cerr.New(cerr.KindInvalid, "codehost.Conform",
		fmt.Errorf("connector %q: %s", r.connectorName(), strings.Join(parts, "; ")))
}

// String renders the report as one line per check, for CI logs.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "codehostconform: connector=%s", r.connectorName())
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

// Conform checks c against the parts of the [codehost.CodeHost] contract a compiler
// cannot enforce.
//
// The type system already refuses several ways of getting this wrong: a
// connector missing an operation does not build, and a list resolution carrying
// nothing cannot be constructed as a complete success (see [codehost.Resolution]). What
// is left are the properties that depend on BEHAVIOUR and on what the
// connector's manifest CLAIMS, and those need a run.
//
// The returned error is non-nil only when the run could not be carried out at
// all — no connector, missing fixtures. A connector that ran and is
// non-conformant yields a nil error and a Report whose Err reports the
// failures. Gate on Report.Err; log Report either way, because its per-check
// lines are what a reviewer reads.
func Run(ctx context.Context, c codehost.CodeHost, opts Options) (Report, error) {
	if c == nil {
		return Report{}, cerr.New(cerr.KindInvalid, "codehost.Conform", fmt.Errorf("nil connector"))
	}
	if opts.ListableOwner == "" {
		return Report{}, cerr.New(cerr.KindInvalid, "codehost.Conform", fmt.Errorf(
			"Options.ListableOwner is empty: without an owner the connector must list, "+
				"a run cannot tell a conformant connector from one that lists nothing"))
	}
	if opts.UnlistableOwner == "" {
		return Report{}, cerr.New(cerr.KindInvalid, "codehost.Conform", fmt.Errorf(
			"Options.UnlistableOwner is empty: without an owner the connector must fail on, "+
				"its failure classification is never exercised"))
	}

	rep := Report{Connector: opts.Manifest.Name}

	checkManifest(&rep, opts.Manifest)
	checkClass(&rep, c)
	checkCapabilityHonesty(&rep, c, opts.Manifest)
	checkOptionalDeclared(&rep, c)
	checkListNoEmptySuccess(ctx, &rep, c, opts)
	checkListFailClosed(ctx, &rep, c, opts)
	checkHealth(ctx, &rep, c)

	return rep, nil
}

func checkManifest(rep *Report, m manifest.Doc) {
	if err := m.Validate(); err != nil {
		rep.add(CheckManifest, false, err.Error())
		return
	}
	if m.Implements != connector.ClassCodeHost {
		rep.add(CheckManifest, false, fmt.Sprintf(
			"manifest implements %q; this harness checks the %q class", m.Implements, connector.ClassCodeHost))
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

func checkClass(rep *Report, c codehost.CodeHost) {
	if got := c.Implements(); got != connector.ClassCodeHost {
		rep.add(CheckClass, false, fmt.Sprintf(
			"Implements() reports %q; a host routes by class and would dispatch this connector as something it is not", got))
		return
	}
	rep.add(CheckClass, true, "")
}

func checkCapabilityHonesty(rep *Report, c codehost.CodeHost, m manifest.Doc) {
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
// CapNativeReview is deliberately absent: it has no operation, so there is
// nothing to assert about it and this check would have to invent a verdict.
// It is covered only by the manifest's vocabulary closure — recorded in its
// own doc comment rather than left to look checked here.
var optionalOps = []struct {
	capability connector.Capability
	implements func(codehost.CodeHost) bool
}{
	{codehost.CapFileRead, func(c codehost.CodeHost) bool { _, ok := c.(codehost.FileReader); return ok }},
	{codehost.CapWebhooks, func(c codehost.CodeHost) bool { _, ok := c.(codehost.WebhookRegistrar); return ok }},
	{codehost.CapRunnerTokens, func(c codehost.CodeHost) bool { _, ok := c.(codehost.RunnerTokenMinter); return ok }},
}

func checkOptionalDeclared(rep *Report, c codehost.CodeHost) {
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

func checkListNoEmptySuccess(ctx context.Context, rep *Report, c codehost.CodeHost, opts Options) {
	res, err := c.ListRepos(ctx, opts.ListableOwner)
	if err != nil {
		rep.add(CheckListNoEmptySuccess, false, fmt.Sprintf(
			"ListRepos(%s) failed on an owner the fixtures say it must list: %v", opts.ListableOwner, err))
		return
	}
	repos, ierr := res.Items()
	if ierr != nil {
		rep.add(CheckListNoEmptySuccess, false, fmt.Sprintf(
			"ListRepos(%s) reported success with a resolution that carries no list: %v", opts.ListableOwner, ierr))
		return
	}
	if len(repos) == 0 {
		rep.add(CheckListNoEmptySuccess, false, fmt.Sprintf(
			"ListRepos(%s) resolved to no repositories; the fixtures name an owner that has at least one, "+
				"so a run against this connector cannot tell it apart from one that lists nothing", opts.ListableOwner))
		return
	}
	rep.add(CheckListNoEmptySuccess, true, fmt.Sprintf("%d repositories listed", len(repos)))
}

func checkListFailClosed(ctx context.Context, rep *Report, c codehost.CodeHost, opts Options) {
	res, err := c.ListRepos(ctx, opts.UnlistableOwner)
	if err == nil {
		rep.add(CheckListFailClosed, false, fmt.Sprintf(
			"ListRepos(%s) succeeded on an owner the fixtures say it must not list", opts.UnlistableOwner))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckListFailClosed, false, fmt.Sprintf(
			"ListRepos(%s) failed with an unclassified error, so a host would have to parse its text to act on it: %v",
			opts.UnlistableOwner, err))
		return
	}
	if _, ierr := res.Items(); ierr == nil {
		rep.add(CheckListFailClosed, false, fmt.Sprintf(
			"ListRepos(%s) failed with %s and STILL returned a readable resolution; a caller that ignored the error "+
				"reads the failure as a code host with no repositories", opts.UnlistableOwner, cerr.KindOf(err)))
		return
	}
	rep.add(CheckListFailClosed, true, fmt.Sprintf("%s -> %s, resolution unreadable", opts.UnlistableOwner, cerr.KindOf(err)))
}

func checkHealth(ctx context.Context, rep *Report, c codehost.CodeHost) {
	h, err := c.Health(ctx)
	if err != nil {
		if !cerr.Classified(err) {
			rep.add(CheckHealth, false, fmt.Sprintf(
				"Health() failed with an unclassified error: %v", err))
			return
		}
		rep.add(CheckHealth, true, fmt.Sprintf("classified failure: %s", cerr.KindOf(err)))
		return
	}
	switch h.Status {
	case connector.Healthy, connector.Degraded, connector.Unavailable:
		rep.add(CheckHealth, true, fmt.Sprintf("status=%d", int(h.Status)))
	default:
		rep.add(CheckHealth, false, fmt.Sprintf(
			"Health() reported status %d, which is outside the SDK's vocabulary", int(h.Status)))
	}
}
