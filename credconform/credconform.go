// Package credconform checks a CredentialLoader connector against the parts
// of the contract a compiler cannot enforce.
//
// The Go type system already refuses several ways of getting this wrong: a
// connector that does not implement every operation does not build, and a
// resolution carrying no value cannot be constructed as a success (see
// credential.Resolution). What is left are the properties that depend on
// BEHAVIOUR and on what the connector's manifest CLAIMS — and those need a
// harness.
//
// Run it in your own CI, against your own connector, before arqtos ever loads
// it:
//
//	rep, err := credconform.Run(ctx, myLoader, credconform.Options{
//		Manifest:     myManifest,
//		Resolvable:   []ref.Ref{presentRef},
//		Unresolvable: absentRef,
//	})
//	if err != nil {
//		return err // the check could not be run at all
//	}
//	if err := rep.Err(); err != nil {
//		return err // the connector ran, and is not conformant
//	}
//
// # What it checks, and why each one exists
//
//   - [CheckManifest] — the manifest validates and declares only capabilities
//     this connector class defines.
//   - [CheckCapabilityHonesty] — what the manifest declares and what the
//     running connector reports are the same set.
//   - [CheckBatchDeclared] — batch resolution is declared exactly when it is
//     implemented.
//   - [CheckResolveNoEmptySuccess] — a reference the connector can resolve
//     comes back carrying material, never as a success carrying nothing and
//     never as a deliberately-empty assertion.
//   - [CheckFailureTyped] — a reference the connector cannot resolve fails
//     with a classified error from the closed vocabulary, so a host never
//     parses vendor prose.
//   - [CheckBatchShape] — batch results correspond one-for-one, in order,
//     with the references requested.
//
// # The harness is driven by non-compliant connectors too
//
// Every check in this package has a test that drives it with a connector
// deliberately built to violate the property it checks — a connector that
// resolves to empty with no error, one that fails with untyped vendor text,
// one whose manifest declares a batch operation it does not have. A harness
// only ever run against compliant input proves nothing about what it would
// catch.
//
// # Scope
//
// This package covers the three contract obligations above. Contract shape,
// secret-handling (no material to logs, disk or wire; dies-with-session) and
// protocol-version negotiation are the rest of the conformance story and land
// alongside these checks.
package credconform

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// Check names reported by [Run]. They are stable identifiers: a caller may
// switch on them, and a CI job may allowlist a known failure by name.
const (
	// CheckManifest covers the connector's manifest validating, declaring
	// this class, and declaring only capabilities the class defines.
	CheckManifest = "manifest/valid"
	// CheckCapabilityHonesty covers the manifest's declared capabilities and
	// the running connector's Capabilities() being the same set.
	CheckCapabilityHonesty = "capability/manifest-matches-runtime"
	// CheckBatchDeclared covers batch resolution being declared exactly when
	// it is implemented.
	CheckBatchDeclared = "batch/declared-is-implemented"
	// CheckResolveNoEmptySuccess covers a resolvable reference coming back
	// carrying material, rather than as a success carrying nothing.
	CheckResolveNoEmptySuccess = "resolve/no-empty-success"
	// CheckFailureTyped covers an unresolvable reference failing with a
	// classified error from the closed vocabulary.
	CheckFailureTyped = "failure/typed"
	// CheckBatchShape covers batch results corresponding one-for-one, in
	// order, with the references requested. It is reported only for a
	// connector that implements batch resolution.
	CheckBatchShape = "batch/results-match-request"
)

// Options are the fixtures a conformance run needs. Every field is required:
// a check that cannot be driven is not skipped, because a report that is
// green because nothing looked is the failure this package exists to avoid.
type Options struct {
	// Manifest is the connector.yml this connector ships. It is what an
	// external author encodes and what a host reads, so the run compares it
	// against the running connector rather than trusting either alone.
	Manifest manifest.Doc

	// Resolvable are references this connector MUST resolve — at least one.
	// They drive both the no-empty-success check and, where batch is
	// implemented, the batch shape check.
	Resolvable []ref.Ref

	// Unresolvable is a reference this connector MUST NOT resolve: absent
	// from the backend, or outside what this connector serves. It drives the
	// typed-failure check, which cannot be run against a connector that
	// never fails.
	Unresolvable ref.Ref
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
	// Connector is the name the manifest gives the connector under test, so
	// a failure is attributable without cross-referencing the run.
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
// [cerr.KindInvalid] naming the connector and every failed check. The
// connector ran; it is its behaviour that is wrong, which is why this is
// Invalid rather than Unavailable.
func (r Report) Err() error {
	failed := r.Failures()
	if len(failed) == 0 {
		return nil
	}
	parts := make([]string, 0, len(failed))
	for _, f := range failed {
		parts = append(parts, fmt.Sprintf("%s: %s", f.Name, f.Detail))
	}
	return cerr.New(cerr.KindInvalid, "credconform", fmt.Errorf("connector %q: %s", r.connectorName(), strings.Join(parts, "; ")))
}

// String renders the report as one line per check, for CI logs.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "credconform: connector=%s", r.connectorName())
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

// Run checks c against the contract obligations this package covers.
//
// The returned error is non-nil only when the run could not be carried out —
// no connector, or missing fixtures. A connector that runs and is
// non-conformant yields a nil error and a Report whose Err reports the
// failures; gate on Report.Err, not on the returned error alone.
func Run(ctx context.Context, c credential.CredentialLoader, opts Options) (Report, error) {
	if c == nil {
		return Report{}, cerr.New(cerr.KindInvalid, "credconform.Run", fmt.Errorf("nil connector"))
	}
	if len(opts.Resolvable) == 0 {
		return Report{}, cerr.New(cerr.KindInvalid, "credconform.Run", fmt.Errorf(
			"Options.Resolvable is empty: without a reference the connector must resolve, "+
				"a run cannot tell a conformant connector from one that resolves nothing"))
	}
	if (opts.Unresolvable == ref.Ref{}) {
		return Report{}, cerr.New(cerr.KindInvalid, "credconform.Run", fmt.Errorf(
			"Options.Unresolvable is unset: without a reference the connector must fail on, "+
				"its failure classification is never exercised"))
	}

	rep := Report{Connector: opts.Manifest.Name}

	checkManifest(&rep, opts.Manifest)
	checkCapabilityHonesty(&rep, c, opts.Manifest)
	batcher, isBatcher := c.(credential.BatchResolver)
	checkBatchDeclared(&rep, opts.Manifest, c.Capabilities(), isBatcher)
	checkResolveNoEmptySuccess(ctx, &rep, c, opts)
	checkFailureTyped(ctx, &rep, c, opts)
	if isBatcher {
		checkBatchShape(ctx, &rep, batcher, opts)
	}

	return rep, nil
}

func checkManifest(rep *Report, m manifest.Doc) {
	if err := m.Validate(); err != nil {
		rep.add(CheckManifest, false, err.Error())
		return
	}
	if m.Implements != connector.ClassCredentialLoader {
		rep.add(CheckManifest, false, fmt.Sprintf(
			"manifest implements %q; this harness checks the %q class",
			m.Implements, connector.ClassCredentialLoader))
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

func checkCapabilityHonesty(rep *Report, c credential.CredentialLoader, m manifest.Doc) {
	runtime := c.Capabilities()
	declared := connector.Capabilities(m.Capabilities)

	var missing, undeclared []string
	for _, c := range declared {
		if !runtime.Has(c) {
			missing = append(missing, string(c))
		}
	}
	for _, c := range runtime {
		if !declared.Has(c) {
			undeclared = append(undeclared, string(c))
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

// checkBatchDeclared fails in BOTH directions, because both are a manifest
// that does not describe the connector. Declared-but-absent is the dangerous
// one — the host plans one call and the operation is not there — but
// implemented-but-undeclared leaves a host resolving one reference at a time
// against a connector that could have done it in a single backend call, which
// is how a quota gets spent.
func checkBatchDeclared(rep *Report, m manifest.Doc, runtime connector.Capabilities, implemented bool) {
	inManifest := m.Declares(credential.CapBatchResolve)
	atRuntime := runtime.Has(credential.CapBatchResolve)

	switch {
	case (inManifest || atRuntime) && !implemented:
		rep.add(CheckBatchDeclared, false, fmt.Sprintf(
			"%s is declared %s, but the connector does not implement credential.BatchResolver. "+
				"A declared capability that is absent is worse than an undeclared one: the host plans one backend call "+
				"for N references and finds no operation to make it with",
			credential.CapBatchResolve, declaredIn(inManifest, atRuntime)))
	case implemented && !(inManifest && atRuntime):
		rep.add(CheckBatchDeclared, false, fmt.Sprintf(
			"the connector implements credential.BatchResolver, but %s is declared %s. "+
				"A host reads the declaration before it calls anything, so a batch that is not declared in both places "+
				"is never used and every reference costs its own backend call",
			credential.CapBatchResolve, declaredIn(inManifest, atRuntime)))
	case implemented:
		rep.add(CheckBatchDeclared, true, "declared in the manifest and by Capabilities(), and implemented")
	default:
		rep.add(CheckBatchDeclared, true, "not declared, not implemented")
	}
}

func declaredIn(inManifest, atRuntime bool) string {
	switch {
	case inManifest && atRuntime:
		return "in the manifest and by Capabilities()"
	case inManifest:
		return "in the manifest but not by Capabilities()"
	case atRuntime:
		return "by Capabilities() but not in the manifest"
	default:
		return "nowhere"
	}
}

// checkResolveNoEmptySuccess requires every reference the run declares
// resolvable to come back carrying ACTUAL BYTES.
//
// Presence alone is not enough, and the gap is not theoretical. A connector
// can answer every read with credential.ResolvedEmpty() — present, readable,
// zero bytes — and that is exactly the move an author reaches for when
// credential.Resolved refuses their signed-out backend: it makes the error go
// away without making the credential exist. Checked for presence only, such a
// connector scored a fully green report while serving "" to every caller.
//
// ResolvedEmpty is a legitimate assertion about a secret an operator really
// did store empty. It is not a legitimate answer for a reference the author
// nominated as the proof that this connector resolves things. The fixture is
// the one place the run gets to say "this must work", so it must demand the
// strongest form of working.
func checkResolveNoEmptySuccess(ctx context.Context, rep *Report, c credential.CredentialLoader, opts Options) {
	name := opts.Manifest.Name
	for _, r := range opts.Resolvable {
		got, resolveErr := c.Resolve(ctx, r)
		// CheckResolution is the host's own guard, run here against the
		// connector under test: if it rejects the return, so would every host
		// that loads this connector.
		res, err := credential.CheckResolution(name, "Resolve", got, resolveErr)
		if err != nil {
			var fe *credential.FaultError
			if errors.As(err, &fe) {
				rep.add(CheckResolveNoEmptySuccess, false, fmt.Sprintf(
					"resolving %s: %v. An unresolved credential must be reported as a failure — a backend that returns "+
						"empty output with a success exit code is signed out, not holding an empty secret", r, fe))
				return
			}
			rep.add(CheckResolveNoEmptySuccess, false, fmt.Sprintf(
				"%s is declared resolvable by this run, and the connector failed on it: %v", r, err))
			return
		}
		mat, valueErr := res.Value()
		if valueErr != nil || len(mat.Reveal()) == 0 {
			rep.add(CheckResolveNoEmptySuccess, false, fmt.Sprintf(
				"%s is declared resolvable by this run, and the connector resolved it to NO MATERIAL. "+
					"credential.ResolvedEmpty asserts that a secret is genuinely stored empty; it is not an answer for a "+
					"reference this run nominates as one the connector must resolve, and a connector that answers every "+
					"read that way serves \"\" to every caller while passing a presence-only check. Point Resolvable at a "+
					"reference that holds a value, or fix the read", r))
			return
		}
	}
	rep.add(CheckResolveNoEmptySuccess, true, fmt.Sprintf("%d reference(s) resolved to non-empty material", len(opts.Resolvable)))
}

func checkFailureTyped(ctx context.Context, rep *Report, c credential.CredentialLoader, opts Options) {
	res, err := c.Resolve(ctx, opts.Unresolvable)
	switch {
	case err == nil && !resolutionReadable(res):
		rep.add(CheckFailureTyped, false, fmt.Sprintf(
			"resolving %s returned neither a value nor an error. That is the shape a signed-out backend produces, "+
				"and it must be a typed failure", opts.Unresolvable))
	case err == nil:
		rep.add(CheckFailureTyped, false, fmt.Sprintf(
			"%s is declared unresolvable by this run, and the connector resolved it", opts.Unresolvable))
	case !cerr.Classified(err):
		rep.add(CheckFailureTyped, false, fmt.Sprintf(
			"failure is not classified: %v. A host must act on a cerr.Kind from the closed vocabulary; "+
				"returning the backend's own text leaves it string-matching, and a vendor rewording silently changes host behaviour", err))
	case cerr.KindOf(err) == cerr.KindContractViolation:
		rep.add(CheckFailureTyped, false, fmt.Sprintf(
			"failure is classified as %s, which is what a host reports ABOUT a connector, not a failure a connector returns: %v",
			cerr.KindContractViolation, err))
	default:
		rep.add(CheckFailureTyped, true, fmt.Sprintf("%s -> %s", opts.Unresolvable, cerr.KindOf(err)))
	}
}

func checkBatchShape(ctx context.Context, rep *Report, b credential.BatchResolver, opts Options) {
	name := opts.Manifest.Name
	results, err := b.ResolveBatch(ctx, opts.Resolvable)
	results, err = credential.CheckBatch(name, opts.Resolvable, results, err)
	if err != nil {
		rep.add(CheckBatchShape, false, err.Error())
		return
	}
	// credential.CheckBatch has already proved every element carries either a
	// value or a failure, and that the elements correspond to the request.
	// What is left is that a reference this run declares resolvable actually
	// resolved in the batch: a batch that reports NotFound for a reference
	// the single Resolve returns is a second code path with a different
	// answer, and the host would get whichever one it happened to call.
	for i, got := range results {
		if err := got.Err(); err != nil {
			rep.add(CheckBatchShape, false, fmt.Sprintf(
				"%s is declared resolvable by this run, and the batch failed on it: %v", opts.Resolvable[i], err))
			return
		}
	}
	rep.add(CheckBatchShape, true, fmt.Sprintf("%d reference(s) in one call", len(results)))
}

// resolutionReadable reports whether res carries a value.
func resolutionReadable(res credential.Resolution) bool {
	_, err := res.Value()
	return err == nil
}
