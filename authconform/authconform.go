// Package authconform is the conformance harness for the Authenticator
// connector class: it checks the parts of the contract a compiler cannot
// enforce.
//
// Go already refuses several ways of getting an Authenticator wrong — a missing
// operation does not build, and an Assertion has nowhere to put a token. What
// is left are the properties that depend on BEHAVIOUR and on what the manifest
// CLAIMS, and those need a run.
//
// # Every check is driven by a connector built to violate it
//
// A harness only ever run against compliant input proves nothing about what it
// would catch. This package ships that as a GATE rather than a habit: a test
// reads the check names out of this file's own source and fails if any one has
// no violating stub, plus a companion that fails if a stub breaks a
// NEIGHBOURING check — which would let a new check pass on damage another check
// already reports.
//
// That gate is here from the first commit deliberately. Three of this SDK's
// five harnesses have a violating-input test per check and nothing that fails
// if one is deleted, and the difference between those two states is the
// difference between a habit and a guard.
//
// # What this harness cannot check
//
// ⚠️ A connector whose verification FAILED and which returns an anonymous
// assertion with a NIL error is indistinguishable, from outside, from one
// reporting a genuine anonymous answer. Both are the zero Assertion and no
// error, and the difference is entirely inside the connector.
//
// [CheckRejectionIsTyped] drives the rejected fixture and requires a typed
// failure, which catches the connector that reports the anonymous shape FOR A
// CODE THE FIXTURES SAY MUST BE REJECTED. It cannot catch a connector that
// rejects correctly here and reports anonymously somewhere else. The contract
// names this gap too; it is the author's honesty, not the harness's reach.
//
// ⚠️ Nothing here drives a real browser or a real identity provider. Every check
// runs against the connector's own returns.
package authconform

import (
	"context"
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/authenticator"
	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

// TransportUnrecorded is what [Run] records: it was handed an
// authenticator.Authenticator and cannot know what is behind it.
const TransportUnrecorded = "transport not recorded"

// The checks this harness runs. Each is driven by a violating stub; see the
// package documentation for the gate that enforces it.
const (
	// CheckManifest covers the manifest parsing, declaring this class, and
	// declaring only capabilities the class defines — which at v1 is none.
	CheckManifest = "manifest/valid"
	// CheckClass covers the RUNNING connector reporting its own class. A host
	// routes by class, so one reporting another is dispatched as something it
	// is not.
	CheckClass = "class/implements"
	// CheckCapabilityHonesty covers the manifest's declaration and
	// Capabilities() being the same set, in both directions.
	CheckCapabilityHonesty = "capability/manifest-matches-runtime"
	// CheckHealth covers Health answering with a status from the SDK's
	// vocabulary, or failing with a classified error.
	CheckHealth = "health/answers"
	// CheckChallengeComplete covers Begin returning BOTH an authorization URL
	// and a handle. A host cannot drive the flow with either absent, and each
	// zero value is indistinguishable from one the connector forgot to set.
	CheckChallengeComplete = "challenge/carries-url-and-handle"
	// CheckAssertionCoherent covers Complete returning an assertion whose
	// Authenticated and PrincipalID agree, and whose principal is the one the
	// fixtures name.
	//
	// The identity half matters as much as the coherence half: a connector
	// returning a coherent assertion about SOMEBODY ELSE is coherent and wrong,
	// and a host joining it onto directory groups would size the session from
	// another person's memberships.
	CheckAssertionCoherent = "assertion/coherent"
	// CheckAssertionPrincipal covers the assertion naming the principal the
	// fixtures expect.
	//
	// ⚠️ It is a SEPARATE check from coherence, and the split was forced by
	// measurement rather than taste: while both lived in one check, a stub
	// violating coherence was caught by whichever assertion ran first, and
	// mutating the identity assertion away left the run green. A check
	// asserting two independent properties cannot attribute its own failure
	// and cannot be proven by one stub.
	//
	// A connector returning a coherent assertion about SOMEBODY ELSE is
	// coherent and wrong: a host joining it onto directory groups would size
	// the session from another person's memberships.
	CheckAssertionPrincipal = "assertion/names-the-expected-principal"
	// CheckRejectionIsTyped covers the rule that gives this class its teeth: a
	// code the fixtures say must be rejected yields a TYPED FAILURE, never a
	// successful assertion carrying Authenticated false.
	//
	// An unverified assertion reported as a legitimate anonymous session is
	// attacker-supplied input wearing a success's shape, and nothing
	// downstream questions it because it arrived with no error.
	CheckRejectionIsTyped = "rejection/is-typed-not-anonymous"
	// CheckUnknownHandleRefused covers a handle the connector never issued
	// being refused with a classified error. A connector that completes
	// against any handle has no session binding at all.
	CheckUnknownHandleRefused = "handle/unknown-is-refused"
	// CheckHandleSingleUse covers a handle being consumed by the exchange it
	// completes: completing the SAME handle twice must be refused.
	//
	// A handle is bound to a PKCE verifier and a nonce, so accepting it twice
	// is accepting a replay — the second completion presents an authorization
	// code against state the provider should no longer hold.
	//
	// ⚠️ This check exists because a provider that never consumed a handle
	// passed every other check. The obligation is stated in the wire contract
	// and was driven by nothing, which is the same "green because nothing
	// looked" shape the rest of this harness is built to refuse.
	CheckHandleSingleUse = "handle/is-single-use"
	// CheckInactiveIsReported covers a verified-but-disabled principal coming
	// back AS AN ANSWER — Authenticated true, Active false — rather than as an
	// error.
	//
	// It is a real answer and the host decides what to do with it. A connector
	// that raises it as a failure removes the host's ability to distinguish
	// "suspended" from "could not authenticate", which are different sessions.
	CheckInactiveIsReported = "assertion/inactive-is-reported"
)

// Options carries the fixtures a run needs.
//
// ⚠️ EVERY FIELD IS REQUIRED. An optional fixture is a check that silently
// skips, and a check that skipped is reported by nothing — which is the same
// defect this harness exists to catch, one level up.
type Options struct {
	// Manifest is the connector.yml as the host read it.
	Manifest manifest.Doc
	// ValidCode is an authorization code that completes successfully and
	// resolves to ExpectedPrincipalID.
	ValidCode string
	// RejectedCode is an authorization code whose verification must FAIL.
	RejectedCode string
	// InactiveCode is an authorization code that verifies successfully and
	// resolves to a principal the provider reports as disabled.
	InactiveCode string
	// UnknownHandle is a handle this connector never issued.
	UnknownHandle string
	// ExpectedPrincipalID is the principal ValidCode and InactiveCode resolve
	// to — the provider's STABLE identifier, not an address.
	ExpectedPrincipalID string
}

// A Result is one check's outcome.
type Result struct {
	// Name is one of the Check* constants.
	Name string
	// Pass reports whether the check succeeded.
	Pass bool
	// Detail explains the outcome. It is always populated for a failure.
	Detail string
}

// A Report is the outcome of a conformance run.
type Report struct {
	// Connector is the name the manifest gives the connector under test.
	Connector string
	// Transport records HOW the connector under test was reached, because a
	// green report does not mean the same thing in both cases.
	//
	// [Run] takes an authenticator.Authenticator and cannot tell a
	// natively-compiled connector from a host stub talking to a subprocess — so
	// it records [TransportUnrecorded] rather than guessing, and a report
	// carrying that is NOT evidence the wire was exercised. [RunOutOfProcess]
	// knows, and says so.
	//
	// For this class the distinction is sharp: proto3 omits a false bool and an
	// empty string entirely, so the wire failures are exactly the ones an
	// in-process run cannot see.
	Transport string
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
// RAN; it is its behaviour that is wrong, which is why this is Invalid rather
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
	return cerr.New(cerr.KindInvalid, "authconform",
		fmt.Errorf("connector %q: %s", r.connectorName(), strings.Join(parts, "; ")))
}

// String renders the report as one line per check, for CI logs.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "authconform: connector=%s transport=%s", r.connectorName(), r.transport())
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

func (r Report) transport() string {
	if r.Transport == "" {
		return TransportUnrecorded
	}
	return r.Transport
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

// Run checks a against the parts of the [authenticator.Authenticator] contract
// a compiler cannot enforce.
//
// The returned error is non-nil only when the run could not be CARRIED OUT —
// no connector, a missing fixture, an incoherent fixture set. A connector that
// ran and is non-conformant yields a nil error and a Report whose Err reports
// the failures. Gate on Report.Err; log the Report either way, because its
// per-check lines are what a reviewer reads.
func Run(ctx context.Context, a authenticator.Authenticator, opts Options) (Report, error) {
	const op = "authconform.Run"

	if a == nil {
		return Report{}, cerr.New(cerr.KindInvalid, op, fmt.Errorf("nil connector"))
	}
	for _, missing := range []struct{ field, value, why string }{
		{"ValidCode", opts.ValidCode,
			"without a code that completes, no assertion is ever observed and every assertion check passes by never running"},
		{"RejectedCode", opts.RejectedCode,
			"without a code that must be rejected, the rule that a rejection is never an anonymous session is never exercised"},
		{"InactiveCode", opts.InactiveCode,
			"without a verified-but-disabled principal, a connector that raises one as an error is never caught"},
		{"UnknownHandle", opts.UnknownHandle,
			"without a handle the connector never issued, a connector that completes against any handle is never caught"},
		{"ExpectedPrincipalID", opts.ExpectedPrincipalID,
			"without the principal the codes resolve to, a coherent assertion about somebody else passes"},
	} {
		if missing.value == "" {
			return Report{}, cerr.New(cerr.KindInvalid, op,
				fmt.Errorf("fixture Options.%s is unset: %s", missing.field, missing.why))
		}
	}
	// A fixture set can be COMPLETE and still describe an impossible provider.
	// Each pair below would let a check observe something other than the
	// property it names — one code cannot be both accepted and rejected, and a
	// run built on that would report whichever the connector happened to do.
	for _, clash := range []struct{ a, b, aName, bName string }{
		{opts.ValidCode, opts.RejectedCode, "ValidCode", "RejectedCode"},
		{opts.ValidCode, opts.InactiveCode, "ValidCode", "InactiveCode"},
		{opts.RejectedCode, opts.InactiveCode, "RejectedCode", "InactiveCode"},
	} {
		if clash.a == clash.b {
			return Report{}, cerr.New(cerr.KindInvalid, op, fmt.Errorf(
				"fixtures Options.%s and Options.%s are the same code %q: one code cannot be both, and a "+
					"run built on it reports whichever the connector happened to do",
				clash.aName, clash.bName, clash.a))
		}
	}

	rep := Report{Connector: opts.Manifest.Name, Transport: TransportUnrecorded}

	checkManifest(&rep, opts.Manifest)
	checkClass(&rep, a)
	checkCapabilityHonesty(&rep, a, opts.Manifest)
	checkHealth(ctx, &rep, a)
	checkChallenge(ctx, &rep, a)
	checkAssertion(ctx, &rep, a, opts)
	checkAssertionPrincipal(ctx, &rep, a, opts)
	checkRejection(ctx, &rep, a, opts)
	checkUnknownHandle(ctx, &rep, a, opts)
	checkHandleSingleUse(ctx, &rep, a, opts)
	checkInactive(ctx, &rep, a, opts)

	return rep, nil
}

func checkManifest(rep *Report, m manifest.Doc) {
	if err := m.Validate(); err != nil {
		rep.add(CheckManifest, false, fmt.Sprintf("manifest is invalid: %v", err))
		return
	}
	if m.Implements != connector.ClassAuthenticator {
		rep.add(CheckManifest, false, fmt.Sprintf("manifest declares implements %q, not %q",
			m.Implements, connector.ClassAuthenticator))
		return
	}
	rep.add(CheckManifest, true, "")
}

func checkClass(rep *Report, a authenticator.Authenticator) {
	if got := a.Implements(); got != connector.ClassAuthenticator {
		rep.add(CheckClass, false, fmt.Sprintf(
			"the running connector reports class %q; a host routes by class and would dispatch it as "+
				"something it is not", got))
		return
	}
	rep.add(CheckClass, true, "")
}

func checkCapabilityHonesty(rep *Report, a authenticator.Authenticator, m manifest.Doc) {
	runtime := a.Capabilities()
	for _, c := range runtime {
		if !capsHas(m.Capabilities, c) {
			rep.add(CheckCapabilityHonesty, false, fmt.Sprintf(
				"Capabilities() reports %q, which the manifest does not declare", c))
			return
		}
	}
	for _, c := range m.Capabilities {
		if !capsHas(runtime, c) {
			rep.add(CheckCapabilityHonesty, false, fmt.Sprintf(
				"the manifest declares %q, which Capabilities() does not report", c))
			return
		}
	}
	rep.add(CheckCapabilityHonesty, true, fmt.Sprintf("%d capabilit(ies)", len(runtime)))
}

func capsHas(list connector.Capabilities, want connector.Capability) bool {
	for _, c := range list {
		if c == want {
			return true
		}
	}
	return false
}

func checkHealth(ctx context.Context, rep *Report, a authenticator.Authenticator) {
	h, err := a.Health(ctx)
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
		rep.add(CheckHealth, false, fmt.Sprintf(
			"Health() reported status %d, which is outside the SDK's vocabulary", int(h.Status)))
	}
}

// checkChallenge drives Begin and reports on what it returned.
func checkChallenge(ctx context.Context, rep *Report, a authenticator.Authenticator) {
	c, err := a.Begin(ctx)
	if err != nil {
		rep.add(CheckChallengeComplete, false, fmt.Sprintf("Begin failed: %v", err))
		return
	}
	switch {
	case c.AuthorizationURL == "":
		rep.add(CheckChallengeComplete, false,
			"Begin returned no authorization URL; a host has nowhere to send the operator")
	case c.Handle == "":
		rep.add(CheckChallengeComplete, false,
			"Begin returned no handle; a host has nothing to complete against")
	default:
		rep.add(CheckChallengeComplete, true, "")
	}
}

// freshHandle begins a NEW exchange for a check that is about to complete one.
//
// ⚠️ Every Complete-driving check calls this rather than sharing one handle,
// and the reason is a contract property rather than tidiness: A HANDLE IS
// SINGLE-USE. It is bound to a PKCE verifier and a nonce, and a provider that
// let one be completed twice would be accepting a replay.
//
// The first version of this harness began ONE exchange and reused its handle
// across four completions. Every stub in its own test suite passed, because
// none of them consumed a handle — the defect surfaced the moment a reference
// provider that consumes correctly was pointed at it, and the harness blamed
// the provider. A harness whose fixtures are more permissive than the contract
// reports conformant connectors as broken.
func freshHandle(ctx context.Context, a authenticator.Authenticator) (string, error) {
	c, err := a.Begin(ctx)
	if err != nil {
		return "", err
	}
	return c.Handle, nil
}

// checkAssertion and checkAssertionPrincipal each drive Complete themselves
// rather than one consuming the other's result.
//
// ⚠️ Chaining them was the first shape and it was wrong: a stub violating
// coherence made the dependent check report "not reached", so ONE stub failed
// TWO checks and the companion meta-test — which requires a stub to break only
// its own check — went red. A check that cannot be driven independently cannot
// be attributed independently either. Two Complete calls is the price.
func checkAssertion(ctx context.Context, rep *Report, a authenticator.Authenticator, opts Options) {
	handle, err := freshHandle(ctx, a)
	if err != nil {
		rep.add(CheckAssertionCoherent, false, fmt.Sprintf("Begin failed, so this check could not be driven: %v", err))
		return
	}
	var got authenticator.Assertion
	got, err = a.Complete(ctx, handle, opts.ValidCode)
	if err != nil {
		rep.add(CheckAssertionCoherent, false, fmt.Sprintf(
			"Complete failed on the code the fixtures say completes: %v", err))
		return
	}
	if !got.Coherent() {
		rep.add(CheckAssertionCoherent, false, fmt.Sprintf(
			"assertion is incoherent (Authenticated=%v, PrincipalID=%q): an authenticated assertion naming "+
				"nobody cannot be acted on, and a principal named while denying authentication is a name "+
				"nothing verified", got.Authenticated, got.PrincipalID))
		return
	}
	rep.add(CheckAssertionCoherent, true, "")
}

func checkAssertionPrincipal(ctx context.Context, rep *Report, a authenticator.Authenticator, opts Options) {
	handle, err := freshHandle(ctx, a)
	if err != nil {
		rep.add(CheckAssertionPrincipal, false, fmt.Sprintf("Begin failed, so this check could not be driven: %v", err))
		return
	}
	var got authenticator.Assertion
	got, err = a.Complete(ctx, handle, opts.ValidCode)
	if err != nil {
		rep.add(CheckAssertionPrincipal, false, fmt.Sprintf(
			"Complete failed on the code the fixtures say completes: %v", err))
		return
	}
	if got.PrincipalID != opts.ExpectedPrincipalID {
		rep.add(CheckAssertionPrincipal, false, fmt.Sprintf(
			"assertion names principal %q, not the fixture's %q: coherent and about somebody else is still "+
				"the wrong session", got.PrincipalID, opts.ExpectedPrincipalID))
		return
	}
	rep.add(CheckAssertionPrincipal, true, "")
}

func checkRejection(ctx context.Context, rep *Report, a authenticator.Authenticator, opts Options) {
	handle, err := freshHandle(ctx, a)
	if err != nil {
		rep.add(CheckRejectionIsTyped, false, fmt.Sprintf("Begin failed, so this check could not be driven: %v", err))
		return
	}
	var got authenticator.Assertion
	got, err = a.Complete(ctx, handle, opts.RejectedCode)
	if err == nil {
		rep.add(CheckRejectionIsTyped, false, fmt.Sprintf(
			"the code the fixtures say must be rejected completed with no error (Authenticated=%v): an "+
				"unverified assertion reported as an anonymous session is attacker-supplied input wearing "+
				"a success's shape", got.Authenticated))
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckRejectionIsTyped, false, fmt.Sprintf(
			"the rejection is unclassified (%v); a host routes on the classification, never the message", err))
		return
	}
	if got != (authenticator.Assertion{}) {
		rep.add(CheckRejectionIsTyped, false,
			"an assertion was returned beside the rejection; a call either answers or fails")
		return
	}
	rep.add(CheckRejectionIsTyped, true, fmt.Sprintf("classified rejection: %s", cerr.KindOf(err)))
}

func checkUnknownHandle(ctx context.Context, rep *Report, a authenticator.Authenticator, opts Options) {
	_, err := a.Complete(ctx, opts.UnknownHandle, opts.ValidCode)
	if err == nil {
		rep.add(CheckUnknownHandleRefused, false,
			"a handle this connector never issued completed successfully; the connector has no session binding")
		return
	}
	if !cerr.Classified(err) {
		rep.add(CheckUnknownHandleRefused, false, fmt.Sprintf(
			"an unknown handle failed with an unclassified error: %v", err))
		return
	}
	rep.add(CheckUnknownHandleRefused, true, fmt.Sprintf("classified refusal: %s", cerr.KindOf(err)))
}

func checkHandleSingleUse(ctx context.Context, rep *Report, a authenticator.Authenticator, opts Options) {
	handle, err := freshHandle(ctx, a)
	if err != nil {
		rep.add(CheckHandleSingleUse, false, fmt.Sprintf("Begin failed, so this check could not be driven: %v", err))
		return
	}
	if _, err := a.Complete(ctx, handle, opts.ValidCode); err != nil {
		rep.add(CheckHandleSingleUse, false, fmt.Sprintf(
			"Complete failed on the code the fixtures say completes: %v", err))
		return
	}
	if _, err := a.Complete(ctx, handle, opts.ValidCode); err == nil {
		rep.add(CheckHandleSingleUse, false,
			"the same handle completed twice; a handle is bound to a verifier and a nonce, so accepting it "+
				"again is accepting a replay")
		return
	}
	rep.add(CheckHandleSingleUse, true, "")
}

func checkInactive(ctx context.Context, rep *Report, a authenticator.Authenticator, opts Options) {
	handle, err := freshHandle(ctx, a)
	if err != nil {
		rep.add(CheckInactiveIsReported, false, fmt.Sprintf("Begin failed, so this check could not be driven: %v", err))
		return
	}
	var got authenticator.Assertion
	got, err = a.Complete(ctx, handle, opts.InactiveCode)
	if err != nil {
		rep.add(CheckInactiveIsReported, false, fmt.Sprintf(
			"a verified-but-disabled principal was raised as an error (%v); it is a real answer, and "+
				"reporting it as a failure removes the host's ability to tell \"suspended\" from "+
				"\"could not authenticate\"", err))
		return
	}
	if !got.Authenticated {
		rep.add(CheckInactiveIsReported, false,
			"a verified-but-disabled principal came back unauthenticated; the provider verified them")
		return
	}
	if got.Active {
		rep.add(CheckInactiveIsReported, false,
			"the disabled fixture came back Active; a connector that never sets Active reports every "+
				"principal as enabled")
		return
	}
	rep.add(CheckInactiveIsReported, true, "")
}
