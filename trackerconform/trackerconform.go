package trackerconform

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/tracker"
)

// conformOp is the op every failure this harness raises is stamped with, so a
// host reading cerr.Error.Op learns which surface refused rather than only
// which kind.
const conformOp = "trackerconform.Run"

// Check names reported by [Run]. They are stable identifiers: a caller may
// switch on them, and a CI job may allowlist a known failure by name.
//
// The first seven are the ones every connector class in this estate carries,
// ported from codehost and codeci so a reviewer who has read one report can
// read this one. The rest are this class's own, and each of them exists
// because a real tracker got the property wrong in a way a compiler cannot
// see.
const (
	// CheckManifest covers the connector's manifest validating, declaring this
	// class, and declaring only capabilities this class defines.
	CheckManifest = "manifest/valid"
	// CheckClass covers the running connector reporting this class from
	// Implements(). A host routes by class, so a connector that reports another
	// one is dispatched as something it is not.
	CheckClass = "class/implements"
	// CheckCapabilityHonesty covers the manifest's declared capabilities and
	// the running connector's Capabilities() being the same set.
	CheckCapabilityHonesty = "capability/manifest-matches-runtime"
	// CheckOptionalDeclared covers each optional operation group being declared
	// exactly when it is implemented. "Implemented" is a Go interface assertion
	// against the connector's own type, deliberately NOT derived from
	// Capabilities(): a check that read "implemented" off the same signal as
	// "declared" would agree with itself whatever the connector does.
	//
	// It fails in both directions. Declared and absent leaves a host planning a
	// train it can never create; implemented and undeclared is behaviour
	// nothing will ever call, because a host reads the manifest before it loads
	// anything.
	CheckOptionalDeclared = "optional/declared-is-implemented"
	// CheckListNoEmptySuccess covers [tracker.Tracker.Scan] over a board the fixtures
	// say has items, and [tracker.Tracker.GetItems] over an item the fixtures say
	// exists, each coming back as a READABLE resolution carrying entries —
	// never as the zero Resolution with a nil error, and never omitting the
	// item it was asked about.
	CheckListNoEmptySuccess = "list/no-empty-success"
	// CheckListFailClosed covers a board the connector cannot scan and an item
	// that is not on the board each failing with a classified error AND an
	// unreadable resolution. Both halves matter: the classification is what a
	// host routes on, and the unreadability is what stops a caller that ignored
	// the error from reading the failure as a board with no items.
	CheckListFailClosed = "list/failure-is-typed-and-fail-closed"
	// CheckHealth covers Health() answering: a status in the SDK's vocabulary,
	// or a classified failure.
	CheckHealth = "health/answers"

	// CheckScanPagedToExhaustion covers [tracker.Selection] narrowing the PAYLOAD and
	// never the reach of the read: the cheapest and the fullest scan of one
	// board must report the same items, and both must include every item the
	// fixtures place on it.
	//
	// A truncating connector is at its most dangerous on the expensive path,
	// because that is the one whose per-item cost tempts an early exit — and a
	// board audit over the shorter list reports every item it never examined as
	// compliant.
	CheckScanPagedToExhaustion = "scan/paged-to-exhaustion"
	// CheckReadUnreadableIsNotEmpty covers unknown never being reported as
	// empty: a child set that could not be read is nil WITH an error, a
	// genuinely childless item is empty WITHOUT one, the two disagree
	// observably, and no field is named in [tracker.Item.Unread] while also carrying a
	// value in [tracker.Item.Fields].
	CheckReadUnreadableIsNotEmpty = "read/unreadable-is-not-empty"
	// CheckSelectionEchoed covers every item a read returns carrying a
	// [tracker.Item.Selected] EQUAL to the [tracker.Selection] that read was given.
	//
	// This check is named as this harness's job by the contract itself (see
	// [tracker.Item.Selected]): the zero Selection is a legitimate cheap read, so a
	// connector that never populates the field is indistinguishable from one
	// answering a zero Selection — in the type alone. The check is what closes
	// that.
	//
	// BOTH read operations are driven under BOTH a zero and a full Selection,
	// and neither half of that is decoration. The zero Selection is the case a
	// connector that never sets the field passes by accident; the non-zero one
	// is the case a connector that hard-codes the zero Selection passes by
	// accident. A read checked under only one of the two is checked against the
	// defect it cannot see.
	CheckSelectionEchoed = "read/selection-echoed"
	// CheckApplyAttribution covers [tracker.Tracker.Apply] attributing an outcome to
	// every change it was asked for. It is driven with a change set in which
	// every change is refusable before any network call, so the run can
	// exercise the attribution WITHOUT writing anything to the board.
	//
	// # The reading of the attribution rule
	//
	// [tracker.Tracker.Apply] makes an error the connector cannot ATTRIBUTE to a change
	// a hard failure of the whole call, and this harness holds a connector to
	// the converse as well: a refusal it CAN attribute belongs in
	// [tracker.ApplyReport.Failed] at that change's index, classified. Every
	// pre-network refusal is attributable by construction — the connector found
	// it while looking at one change and knows that change's index — so a
	// whole-call error for one is a violation, and it discards exactly what the
	// report exists to carry.
	//
	// [CheckApplyCrossTrackerRefused] is held to the SAME reading, through the
	// same code, so no connector is caught between the two: attribute what you
	// can attribute and both checks pass. An earlier revision of this harness
	// demanded the whole-call error for a foreign parent and the attribution
	// for every other pre-network refusal, which is a pair of rules no single
	// principled connector could satisfy.
	//
	// The report's arithmetic is checked by [tracker.CheckApplyReport] — the same guard
	// a host uses — rather than restated here.
	CheckApplyAttribution = "apply/attribution"
	// CheckApplyCrossTrackerRefused covers a [tracker.Change.Parent] naming an item on
	// a FOREIGN tracker being refused with cerr.KindInvalid before any network
	// call.
	//
	// The refusal is required IN THE REPORT, attributed to the change that
	// carries the parent, and NOT as a whole-call error: a fault the connector
	// can name the index of is one it must attribute. That is the same reading
	// of [tracker.Tracker.Apply]'s attribution rule that [CheckApplyAttribution] states
	// in full, and both checks read the refusal out of the report through one
	// helper so the two cannot drift into contradicting each other.
	//
	// It is driven with a parent that differs from this board in the provider
	// alone, in the instance alone, and in the board alone, plus the foreign
	// item the fixtures name. That is the whole point: the estate runs two
	// boards on one provider and a tracker on another, all three numbering
	// items from 1, so a refusal comparing anything less than the
	// fully-qualified address lets a foreign parent through.
	CheckApplyCrossTrackerRefused = "apply/cross-tracker-refused"
	// CheckApplyNoCachedIdentity covers the [tracker.Catalogue] being re-read rather
	// than held: it is valid for one call chain, and a connector that hands out
	// its own state has made "must not be stored" unenforceable from the
	// caller's side.
	//
	// It also covers two successive Applies of one refused change set
	// classifying identically, which is the closest a caller can get to
	// observing a stale identity through a contract that carries none.
	CheckApplyNoCachedIdentity = "apply/no-cached-identity"
	// CheckTrainsUnion covers the UNION rule of
	// [tracker.TrainAdmin.ListTrains]: the answer accounts for every scope it
	// was asked about, exactly once, in the shape the connector's declared
	// partitioning requires — and a scope that could not be read comes back
	// with [tracker.ScopeTrains.Err] set rather than as a scope with no trains.
	//
	// The train set is a union over scopes, so the less of it a caller can see
	// the greener a replan looks. A scope silently dropped from the answer and a
	// scope reported empty compute the same thing: "nothing to create here".
	//
	// It is checked through [tracker.CheckTrainSets] — the same guard a host
	// runs — so the harness's reading of the rule and the host's cannot drift
	// apart. What this check adds on top is the fixture: a scope the connector
	// genuinely cannot read, which is the only way to observe that unknown does
	// not arrive as empty.
	CheckTrainsUnion = "trains/union-accounts-for-every-scope"
	// CheckTrainsCreateVerified covers [tracker.TrainAdmin.CreateTrains]
	// reporting an outcome for every spec it was given, and the tracker agreeing
	// with the report.
	//
	// A create loop that iterated once still returns successfully for every name
	// it was given, and the count is the only thing that shows it. So the report
	// is checked against the demand, and then the board is RE-READ: a connector
	// whose report refused every spec and created one anyway fails here and
	// nowhere else.
	//
	// It is driven with specs that are refusable before any network call — the
	// wrong polarity of [tracker.TrainSpec.Scope] for what the connector
	// declared, which the contract makes cerr.KindInvalid in both directions —
	// so against a conformant connector nothing is created. See [Run]'s
	// account of what a run can touch for what a NON-conformant one can leave
	// behind, and note that a train is created by name and removable by an
	// operator.
	CheckTrainsCreateVerified = "trains/create-verified-by-re-reading"
)

// Options are the fixtures a conformance run needs.
//
// EVERY field is required, and the run refuses to start without any one of
// them. An optional fixture is a check that silently skips, and a check that
// skipped is reported by nothing — which is the same defect this harness
// exists to catch, one level up. [Run] also refuses a fixture set that is
// complete and INCOHERENT: a foreign item that is not actually on another
// tracker, an unscannable board that is the scannable one, a known item on
// another board. Each of those would let a check pass while observing
// something other than the property it names.
type Options struct {
	// Manifest is the connector.yml this connector ships — what its author
	// wrote and what a host reads. The run compares it against the running
	// connector rather than trusting either alone.
	Manifest manifest.Doc

	// ScannableBoard is the fully-qualified board this connector MUST scan.
	//
	// It must hold MORE THAN ONE PAGE of items, and the three item fixtures
	// below must not all sit on the first page: a scan that stops after one
	// page is indistinguishable from a complete one on a board that fits in
	// one.
	ScannableBoard tracker.BoardRef

	// UnscannableBoard is a fully-qualified board this connector MUST NOT
	// scan: a board it does not serve, or one its token cannot read. It drives
	// the fail-closed check, which cannot be run against a connector that never
	// fails.
	//
	// It must be a DIFFERENT board from ScannableBoard, and it must be fully
	// qualified — a partial address is refused by every operation in the
	// contract, so a run driven with one would report the address refusal as
	// the fail-closed property.
	UnscannableBoard tracker.BoardRef

	// KnownItem is an item on ScannableBoard that this connector MUST read.
	//
	// The write checks name it as the TARGET of changes every connector is
	// required to refuse before any network call. A conformant one writes
	// nothing to it; a non-conformant one could land a field write with nowhere
	// to go, or a parent link on another tracker. Point this at an item you are
	// willing to see written to by a connector that is not yet conformant — see
	// [Run]'s account of what a run can touch.
	KnownItem tracker.ItemRef

	// MissingItem is an item address on ScannableBoard that does NOT exist. It
	// drives the not-found half of the fail-closed check.
	MissingItem tracker.ItemRef

	// ForeignItem is an item on ANOTHER tracker — another provider, another
	// instance of this provider, or another board on this instance. It drives
	// the cross-tracker refusal, and its board must differ from
	// ScannableBoard: an item on the board under test is not foreign, and a run
	// configured that way would demand a refusal the contract does not require.
	ForeignItem tracker.ItemRef

	// ParentItem is an item on ScannableBoard that HAS at least one child.
	//
	// It is what makes an unreadable child set distinguishable from an empty
	// one. On a connector declaring [tracker.CapNativeHierarchy] the read must report
	// its children; on one that does not, the child set is not merely empty but
	// unavailable, and the check asserts that instead — never nothing.
	ParentItem tracker.ItemRef

	// ChildlessItem is an item on ScannableBoard that has NO children, and is
	// not ParentItem. It is the other half of the same pair: a genuinely
	// childless item and one whose child set failed must be observably
	// different, and a run holding only one of them cannot see the difference.
	ChildlessItem tracker.ItemRef

	// UnreadableScope is a [tracker.Scope] this connector MUST NOT be able to
	// read the trains of: a scope its token has no rights in, or one the
	// provider does not serve.
	//
	// It is what makes the union rule observable. Every other property of
	// [tracker.TrainAdmin.ListTrains] can be checked from the shape of a
	// successful answer, but "a scope that could not be read comes back with
	// Err set, never as a scope with no trains" needs a scope that cannot be
	// read. Without one, a connector that reports every unreadable scope as
	// empty passes every check in this harness.
	//
	// It must differ from every scope the item fixtures name, because those are
	// scopes the connector must be able to read: a run pointing this at one of
	// them would demand a failure the connector is required not to have.
	//
	// It is required even of a connector that declares no trains at all, for
	// the reason [Options] states: an optional fixture is a check that silently
	// skips, and a check that skipped is reported by nothing.
	UnreadableScope tracker.Scope
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

// OK reports whether the run observed this connector and every check passed.
//
// A report carrying NO results is not OK, and that clause is the whole reason
// this is a method rather than len(r.Failures()) == 0 at each call site: an
// empty report has no failures, so the naive predicate is TRUE of a run that
// looked at nothing. That is the tautological-gate shape — a gate that is
// green for having observed nothing — and it has shipped in this estate
// before. A report is a verdict only about checks that actually ran.
func (r Report) OK() bool {
	if len(r.Results) == 0 {
		return false
	}
	return len(r.Failures()) == 0
}

// Failures returns the failed checks, in run order.
//
// It reports only checks that RAN. A report with no results has no failures
// and is still not conformant — see [Report.OK] and [Report.Err], which are
// what a caller gates on.
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
//
// A report carrying no results is an error too, and says so in its own words:
// nothing was observed, so there is no verdict to report. Gate on this.
func (r Report) Err() error {
	if len(r.Results) == 0 {
		return cerr.New(cerr.KindInvalid, conformOp, fmt.Errorf(
			"connector %q: the report carries no check results, so nothing about this connector was observed; "+
				"a harness that ran zero checks must not report a connector conformant", r.connectorName()))
	}
	failed := r.Failures()
	if len(failed) == 0 {
		return nil
	}
	parts := make([]string, 0, len(failed))
	for _, f := range failed {
		parts = append(parts, fmt.Sprintf("%s: %s", f.Name, f.Detail))
	}
	return cerr.New(cerr.KindInvalid, conformOp,
		fmt.Errorf("connector %q: %s", r.connectorName(), strings.Join(parts, "; ")))
}

// String renders the report as one line per check, for CI logs. A report that
// ran nothing says so on its own line rather than rendering as a bare header,
// which a reader skims as a pass.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "trackerconform: connector=%s", r.connectorName())
	if len(r.Results) == 0 {
		b.WriteString("\n  FAIL no checks ran; nothing about this connector was observed")
		return b.String()
	}
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

// fullSelection asks for every part of an item the contract can carry.
//
// It is spelled out field by field rather than derived, and
// TestFullSelection_AsksForEverything is what keeps it complete: a field added
// to [tracker.Selection] and not added here would leave every check driven by it
// reading a narrower payload than the contract allows, silently.
var fullSelection = tracker.Selection{
	BoardFields: true,
	ItemFields:  true,
	Train:       true,
	Labels:      true,
	Assignees:   true,
	Children:    true,
}

// A check is one named property and the run that observes it.
//
// The (pass, detail) return is what makes the table worth having: a check
// cannot forget to record its verdict, because recording it is [Run]'s job
// and not the check's. In the harnesses this one is modelled on, each check
// appends its own Result, and a check function that returned early on an
// unexpected shape would silently drop out of the report — a check that did
// not run, in a report that reads as a pass.
type check struct {
	name string
	run  func(ctx context.Context, c tracker.Tracker, opts Options) (pass bool, detail string)
}

// checks is the closed, ordered list of everything [Run] runs.
//
// The order is deliberate: the manifest and the declarations first, because
// they need no network and their failures explain everything downstream; then
// the reads; then the writes, which are the only ones that touch the board and
// which are all driven through refusals so that they do not.
var checks = []check{
	{CheckManifest, func(_ context.Context, _ tracker.Tracker, o Options) (bool, string) {
		return checkManifest(o.Manifest)
	}},
	{CheckClass, func(_ context.Context, c tracker.Tracker, _ Options) (bool, string) {
		return checkClass(c)
	}},
	{CheckCapabilityHonesty, func(_ context.Context, c tracker.Tracker, o Options) (bool, string) {
		return checkCapabilityHonesty(c, o.Manifest)
	}},
	{CheckOptionalDeclared, func(_ context.Context, c tracker.Tracker, _ Options) (bool, string) {
		return checkOptionalDeclared(c)
	}},
	{CheckListNoEmptySuccess, checkListNoEmptySuccess},
	{CheckListFailClosed, checkListFailClosed},
	{CheckHealth, func(ctx context.Context, c tracker.Tracker, _ Options) (bool, string) {
		return checkHealth(ctx, c)
	}},
	{CheckScanPagedToExhaustion, checkScanPagedToExhaustion},
	{CheckReadUnreadableIsNotEmpty, checkReadUnreadableIsNotEmpty},
	{CheckSelectionEchoed, checkSelectionEchoed},
	{CheckApplyAttribution, checkApplyAttribution},
	{CheckApplyCrossTrackerRefused, checkApplyCrossTrackerRefused},
	{CheckApplyNoCachedIdentity, checkApplyNoCachedIdentity},
	{CheckTrainsUnion, checkTrainsUnion},
	{CheckTrainsCreateVerified, checkTrainsCreateVerified},
}

// conformChecks returns the names [Run] runs, in run order.
func conformChecks() []string {
	out := make([]string, 0, len(checks))
	for _, ck := range checks {
		out = append(out, ck.name)
	}
	return out
}

// Run checks c against the parts of the [tracker.Tracker] contract a compiler
// cannot enforce.
//
// The type system already refuses several ways of getting this wrong: a
// connector missing an operation does not build, a list resolution carrying
// nothing cannot be constructed as a complete success (see [tracker.Resolution]), and
// an [tracker.ItemRef] on another tracker cannot compare equal to one on this board.
// What is left are the properties that depend on BEHAVIOUR and on what the
// connector's manifest CLAIMS, and those need a run.
//
// # What it can write to the board
//
// Every write check is driven through a REFUSAL the contract requires: a change
// set in which each change is invalid before any network call, and a parent on
// a foreign tracker. A CONFORMANT connector applies none of it, so against one
// the run is safe to repeat against a live board.
//
// A non-conformant connector is what that claim cannot cover, and the harness
// bounds it rather than assuming it away. The change that could actually
// destroy something is the close, and it is aimed at [closeProbeTarget] — an
// item number no tracker can address — so a connector that ignored the missing
// close reason still closes nothing. Of what is left, the two changes that name
// [Options.KnownItem] ask for a field no board carries and a parent on another
// tracker: the first has nowhere to land at all, and the second lands a link
// that is visible and removable. So point KnownItem at an item you are willing
// to see a bad connector re-parent, and read the report — a connector that is
// not conformant fails loudly rather than quietly mutating the fixture.
//
// That is codeci's rule for MergePR, and the reason [tracker.Tracker.Create] is not
// exercised here at all: filing an item cannot be undone by the harness that
// filed it.
//
// # It is driven by non-compliant connectors too
//
// Every check has a stub deliberately built to violate it, and
// TestRun_EveryCheckHasAViolatingStub fails if one ever loses its
// falsifier. A harness only ever run against compliant input proves nothing
// about what it would catch.
//
// The returned error is non-nil only when the run could not be carried out at
// all — no connector, a missing fixture, an incoherent fixture set, or a run
// that produced no results. A connector that ran and is non-conformant yields
// a nil error and a Report whose Err reports the failures. Gate on Report.Err;
// log Report either way, because its per-check lines are what a reviewer
// reads.
func Run(ctx context.Context, c tracker.Tracker, opts Options) (Report, error) {
	return conformWith(ctx, c, opts, checks)
}

// conformWith is [Run] over an explicit check table.
//
// The table is a parameter for one reason: it is the only way the zero-results
// guard below can be OBSERVED to fire. [Run]'s own table is a package-level
// constant, so no connector and no fixture set can empty it — the guard's
// falsifier is a code change, and a guard whose only falsifier is a code change
// is an assertion nobody has watched fail. TestRunWith_NoChecks_FailsTheRun
// drives it here instead.
func conformWith(ctx context.Context, c tracker.Tracker, opts Options, table []check) (Report, error) {
	if c == nil {
		return Report{}, cerr.New(cerr.KindInvalid, conformOp, errors.New("nil connector"))
	}
	for _, rule := range fixtureRules() {
		if !rule.ok(opts) {
			return Report{}, cerr.New(cerr.KindInvalid, conformOp, fmt.Errorf(
				"fixture Options.%s is unusable: %s", rule.fixture, rule.why))
		}
	}

	rep := Report{Connector: opts.Manifest.Name}
	for _, ck := range table {
		pass, detail := ck.run(ctx, c, opts)
		rep.add(ck.name, pass, detail)
	}

	// The zero-results guard, and it is not decoration. A report with no
	// results has no failures, so every naive "is it green" predicate is true
	// of it — and a harness whose check table was emptied by an edit would then
	// certify every connector it was pointed at. A run that observed nothing is
	// a failure of the RUN, reported here, and never a verdict on the
	// connector.
	//
	// SUBSUMED by [Report.Err], deliberately kept, and annotated for the same
	// reason as the other subsumed guards in this file: a caller told to gate on
	// Report.Err would be refused by that method's identical clause even if this
	// one were deleted. What this adds is the RUN failing rather than returning
	// a report — the error a caller cannot forget to read, on the return where
	// an empty report is already known to be an empty report.
	if len(rep.Results) == 0 {
		return Report{}, cerr.New(cerr.KindInvalid, conformOp, errors.New(
			"the run produced no check results, so nothing about this connector was observed; "+
				"a harness that ran zero checks must not report a connector conformant"))
	}
	return rep, nil
}

// A fixtureRule is one thing a run refuses to proceed without, together with
// why an undriven — or misdirected — check is not a passing one.
type fixtureRule struct {
	fixture string
	ok      func(Options) bool
	why     string
}

// fixtureRules is the whole gate on Options, in the order a reader would want
// a failure reported: the board addresses first, then the items on them, then
// the coherence rules that a complete fixture set can still break.
func fixtureRules() []fixtureRule {
	onBoard := func(ref tracker.ItemRef, board tracker.BoardRef) bool { return ref.Valid() && ref.Board == board }
	return []fixtureRule{
		{"ScannableBoard", func(o Options) bool { return o.ScannableBoard.Valid() }, "without a fully-qualified board " +
			"the connector must scan, no read check is exercised at all; and a partially-qualified address is refused " +
			"by every operation in the contract, so a run driven with one would report the address refusal as whatever " +
			"property it was aimed at"},
		{"UnscannableBoard", func(o Options) bool { return o.UnscannableBoard.Valid() }, "without a fully-qualified " +
			"board the connector must fail on, its failure classification is never exercised"},
		{"UnscannableBoard", func(o Options) bool { return o.UnscannableBoard != o.ScannableBoard }, "it is the same " +
			"board as ScannableBoard, and one board cannot both scan and fail to scan; the fail-closed check would " +
			"report whatever the scan already proved"},
		{"KnownItem", func(o Options) bool { return onBoard(o.KnownItem, o.ScannableBoard) }, "an item the connector " +
			"must read has to be a fully-qualified address ON ScannableBoard; one addressed elsewhere is refused for " +
			"being on another board, which is a different property"},
		{"MissingItem", func(o Options) bool { return onBoard(o.MissingItem, o.ScannableBoard) }, "an item address the " +
			"connector must NOT find has to be on ScannableBoard; one on another board conflates not-found with " +
			"not-on-this-tracker, and the not-found half is then never exercised"},
		{"MissingItem", func(o Options) bool { return o.MissingItem != o.KnownItem }, "it is KnownItem, so the same " +
			"address is required both to resolve and to fail; one of the two checks must be wrong"},
		{"ParentItem", func(o Options) bool { return onBoard(o.ParentItem, o.ScannableBoard) }, "an item whose children " +
			"the connector must report has to be on ScannableBoard"},
		{"ChildlessItem", func(o Options) bool { return onBoard(o.ChildlessItem, o.ScannableBoard) }, "an item with no " +
			"children has to be on ScannableBoard"},
		{"ChildlessItem", func(o Options) bool { return o.ChildlessItem != o.ParentItem }, "it is ParentItem, so one " +
			"item is required both to have children and to have none; an unreadable child set and an empty one cannot " +
			"be told apart from a single item"},
		{"ForeignItem", func(o Options) bool { return o.ForeignItem.Valid() }, "without an item on another tracker, the " +
			"cross-tracker refusal is never exercised against a real foreign address"},
		{"ForeignItem", func(o Options) bool { return o.ForeignItem.Board != o.ScannableBoard }, "its board IS " +
			"ScannableBoard, so it is not foreign at all: the run would demand a refusal the contract does not " +
			"require, and the refusal that matters — an item on another provider, another instance, or another board — " +
			"would go unchecked. With three trackers live at once this is the fixture that decides whether the " +
			"comparison is on the fully-qualified address or on a board number"},
		{"UnreadableScope", func(o Options) bool { return o.UnreadableScope != "" }, "without a scope the connector " +
			"cannot read the trains of, the union rule is unobservable: every other property can be checked from the " +
			"shape of a successful answer, and a connector that reports an unreadable scope as a scope with no trains " +
			"passes all of them"},
		{"UnreadableScope", func(o Options) bool { return !slices.Contains(fixtureScopes(o), o.UnreadableScope) },
			"it is one of the scopes the item fixtures name, and those are scopes this connector must be able to " +
				"read; the run would demand a failure the contract requires it not to have"},
	}
}

// ---------------------------------------------------------------------------
// The seven ported checks
// ---------------------------------------------------------------------------

func checkManifest(m manifest.Doc) (bool, string) {
	if err := validateManifest(m); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// validateManifest checks a Tracker connector's manifest the way a host would
// before it loads anything: the class it declares, then every rule
// manifest.Doc.Validate enforces.
//
// # Why the class check comes first and separately
//
// Doc.Validate closes the implements enum against connector.Classes(), which
// carries ClassTracker — so it accepts a manifest declaring ANY class the SDK
// knows, including Roster and CodeCI. That is correct for the schema and wrong
// for this harness: a host routes by what the manifest says before it ever
// loads the connector, so a Tracker connector shipping a manifest that
// declares Roster is dispatched as a Roster and the failure surfaces nowhere
// near here.
//
// Everything AFTER the class is the SDK's own code, not restated: the required
// name, the kind enum, the refs-only auth invariant, the provider version gate,
// and the capability vocabulary — which Doc.Validate closes against
// tracker.KnownCapabilities() because manifest's classCapabilities registers
// this class. An earlier revision of this function re-ran the vocabulary check
// itself, because the SDK did not publish the class and Doc.Validate refused
// the name before reaching any other rule. It does now, and a second copy of a
// published rule is free to drift from the first.
//
// It is unexported because a manifest LOADER for this class does not exist yet
// (the adapter is where one lands). Exporting it before there is a caller would
// publish an API on a guess.
func validateManifest(doc manifest.Doc) error {
	if doc.Implements != connector.ClassTracker {
		return fmt.Errorf("manifest implements %q; this is the %q class, and a host routes by what the manifest says "+
			"before it ever loads the connector", doc.Implements, connector.ClassTracker)
	}
	return doc.Validate()
}

func checkClass(c tracker.Tracker) (bool, string) {
	if got := c.Implements(); got != connector.ClassTracker {
		return false, fmt.Sprintf("Implements() reports %q; a host routes by class and would dispatch this "+
			"connector as something it is not", got)
	}
	return true, ""
}

func checkCapabilityHonesty(c tracker.Tracker, m manifest.Doc) (bool, string) {
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
		return false, fmt.Sprintf("manifest declares %s which Capabilities() does not report, and Capabilities() "+
			"reports %s which the manifest does not declare",
			strings.Join(missing, ", "), strings.Join(undeclared, ", "))
	case len(missing) > 0:
		return false, fmt.Sprintf("manifest declares %s, which the running connector does not report. A host plans "+
			"for what the manifest promises", strings.Join(missing, ", "))
	case len(undeclared) > 0:
		return false, fmt.Sprintf("the running connector reports %s, which the manifest does not declare. The "+
			"manifest is what a host reads before it ever loads the connector", strings.Join(undeclared, ", "))
	}
	return true, ""
}

// optionalOps pairs each capability that has an OPERATION GROUP behind it with
// the interface assertion that answers whether the operation is there.
//
// Five of this class's seven capabilities are deliberately absent, and none of
// them is declaration-only — the class doc says so and means it. They are
// behavioural rather than structural, so there is no interface to assert
// against and this check would have to invent a verdict:
//
//   - CapNativeHierarchy is observed by [CheckReadUnreadableIsNotEmpty],
//     which asserts the OPPOSITE obligation in each direction rather than
//     skipping when it is absent;
//   - CapScopedTrains changes what [tracker.TrainAdmin] ACCEPTS ([tracker.TrainSpec.Scope]
//     required with it, refused without) and what its answers mean, so it is
//     observable only against a connector that has one;
//   - CapNativeTypes, CapCrossScope and CapItemFields each change what a write
//     is refused for or what a [tracker.Catalogue] reports, and are not yet checks
//     here. That is recorded rather than left to look covered: they are
//     closed only by the manifest's vocabulary today.
var optionalOps = []struct {
	capability connector.Capability
	group      string
	implements func(tracker.Tracker) bool
	harm       string
}{
	{tracker.CapTrains, "TrainAdmin", func(c tracker.Tracker) bool { _, ok := c.(tracker.TrainAdmin); return ok },
		"a host that plans a train move calls into nothing"},
	{tracker.CapSchemaAdmin, "SchemaAdmin", func(c tracker.Tracker) bool { _, ok := c.(tracker.SchemaAdmin); return ok },
		"a host that plans to create a missing field calls into nothing"},
}

func checkOptionalDeclared(c tracker.Tracker) (bool, string) {
	runtime := c.Capabilities()
	var problems []string
	for _, op := range optionalOps {
		declared := runtime.Has(op.capability)
		implemented := op.implements(c)
		switch {
		case declared && !implemented:
			problems = append(problems, fmt.Sprintf("%s is declared but the connector does not implement %s: %s",
				op.capability, op.group, op.harm))
		case !declared && implemented:
			problems = append(problems, fmt.Sprintf("the connector implements %s but does not declare %s: a host "+
				"reads the manifest before it loads the connector, so it will never use it", op.group, op.capability))
		}
	}
	if len(problems) > 0 {
		return false, strings.Join(problems, "; ")
	}
	return true, fmt.Sprintf("%d optional operation group(s) checked", len(optionalOps))
}

func checkListNoEmptySuccess(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	res, err := c.Scan(ctx, opts.ScannableBoard, tracker.Selection{})
	if err != nil {
		return false, fmt.Sprintf("Scan(%s) failed on a board the fixtures say it must scan: %v",
			opts.ScannableBoard, err)
	}
	items, ierr := res.Items()
	if ierr != nil {
		return false, fmt.Sprintf("Scan(%s) reported success with a resolution that carries no list: %v",
			opts.ScannableBoard, ierr)
	}
	if len(items) == 0 {
		return false, fmt.Sprintf("Scan(%s) resolved to no items; the fixtures name a board that holds more than a "+
			"page of them, so a run against this connector cannot tell it apart from one that reads nothing",
			opts.ScannableBoard)
	}

	got, err := c.GetItems(ctx, opts.ScannableBoard, []tracker.ItemRef{opts.KnownItem}, tracker.Selection{})
	if err != nil {
		return false, fmt.Sprintf("GetItems(%s) failed on an item the fixtures say it must read: %v",
			opts.KnownItem, err)
	}
	read, ierr := got.Items()
	if ierr != nil {
		return false, fmt.Sprintf("GetItems(%s) reported success with a resolution that carries no list: %v",
			opts.KnownItem, ierr)
	}
	if _, found := findItem(read, opts.KnownItem); !found {
		return false, fmt.Sprintf("GetItems(%s) resolved %d item(s) and none of them is the one it was asked about; "+
			"a caller that asked about an item and got another has no way to learn which question went unanswered",
			opts.KnownItem, len(read))
	}
	return true, fmt.Sprintf("Scan resolved %d item(s) and GetItems resolved %s", len(items), opts.KnownItem)
}

func checkListFailClosed(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	res, err := c.Scan(ctx, opts.UnscannableBoard, tracker.Selection{})
	if err == nil {
		return false, fmt.Sprintf("Scan(%s) succeeded on a board the fixtures say it must not scan",
			opts.UnscannableBoard)
	}
	// err != nil is spelled out rather than relied on. cerr.Classified(nil) is
	// false, so an unguarded assertion here would also catch the connector that
	// never failed — and the branch above, which says so in words a reviewer can
	// act on, could then be deleted without any test noticing.
	if err != nil && !cerr.Classified(err) {
		return false, fmt.Sprintf("Scan(%s) failed with an unclassified error, so a host would have to parse its "+
			"text to act on it: %v", opts.UnscannableBoard, err)
	}
	if _, ierr := res.Items(); ierr == nil {
		return false, fmt.Sprintf("Scan(%s) failed with %s and STILL returned a readable resolution; a caller that "+
			"ignored the error reads the failure as a board with no items", opts.UnscannableBoard, cerr.KindOf(err))
	}

	got, gerr := c.GetItems(ctx, opts.ScannableBoard, []tracker.ItemRef{opts.MissingItem}, tracker.Selection{})
	// SUBSUMED, deliberately kept: the KindNotFound assertion below also
	// rejects a nil error, because cerr.KindOf(nil) is KindUnknown. Deleting
	// this branch would not let a non-conformant connector through — it would
	// report "failed with unknown, and the contract requires not_found" about a
	// connector that did not fail at all, which sends a reader looking for the
	// wrong mistake. The Scan half above has no required kind, so there the
	// equivalent branch is the ONLY thing that catches it.
	if gerr == nil {
		return false, fmt.Sprintf("GetItems(%s) succeeded on an item address the fixtures say does not exist",
			opts.MissingItem)
	}
	// SUBSUMED by the KindNotFound assertion below, deliberately kept, and
	// annotated as its sibling above is: an unclassified failure classifies as
	// KindUnknown, which is not KindNotFound, so the check fails either way.
	// What this branch adds is the difference between a connector that answered
	// with the wrong kind and one that answered with no kind at all — the first
	// is a mapping to fix, the second is a vendor error returned raw, and an
	// implementer sent after the wrong one of those looks in the wrong place.
	if gerr != nil && !cerr.Classified(gerr) {
		return false, fmt.Sprintf("GetItems(%s) failed with an unclassified error, so a host would have to parse its "+
			"text to act on it: %v", opts.MissingItem, gerr)
	}
	if k := cerr.KindOf(gerr); k != cerr.KindNotFound {
		return false, fmt.Sprintf("GetItems(%s) failed with %s, and the contract requires %s for an item that does "+
			"not exist or is not on this board; a host routes on the classification", opts.MissingItem, k, cerr.KindNotFound)
	}
	if _, ierr := got.Items(); ierr == nil {
		return false, fmt.Sprintf("GetItems(%s) failed with %s and STILL returned a readable resolution",
			opts.MissingItem, cerr.KindOf(gerr))
	}
	return true, fmt.Sprintf("Scan(%s) -> %s and GetItems(%s) -> %s, both resolutions unreadable",
		opts.UnscannableBoard, cerr.KindOf(err), opts.MissingItem, cerr.KindOf(gerr))
}

func checkHealth(ctx context.Context, c tracker.Tracker) (bool, string) {
	h, err := c.Health(ctx)
	if err != nil {
		if !cerr.Classified(err) {
			return false, fmt.Sprintf("Health() failed with an unclassified error: %v", err)
		}
		return true, fmt.Sprintf("classified failure: %s", cerr.KindOf(err))
	}
	switch h.Status {
	case connector.Healthy, connector.Degraded, connector.Unavailable:
		return true, fmt.Sprintf("status=%d", int(h.Status))
	default:
		return false, fmt.Sprintf("Health() reported status %d, which is outside the SDK's vocabulary", int(h.Status))
	}
}

// ---------------------------------------------------------------------------
// The tracker-specific checks
// ---------------------------------------------------------------------------

func checkScanPagedToExhaustion(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	cheap, detail := scanRefs(ctx, c, opts.ScannableBoard, tracker.Selection{}, "the cheapest")
	if detail != "" {
		return false, detail
	}
	full, detail := scanRefs(ctx, c, opts.ScannableBoard, fullSelection, "the fullest")
	if detail != "" {
		return false, detail
	}

	if !slices.Equal(cheap, full) {
		return false, fmt.Sprintf("the cheapest Selection scanned %d item(s) and the fullest scanned %d; a Selection "+
			"narrows what each item carries and never how far the read goes, so a read that stopped earlier on the "+
			"expensive path is a truncated scan reported as a success. Only in one: %s",
			len(cheap), len(full), strings.Join(symmetricDifference(cheap, full), ", "))
	}
	var absent []string
	for _, ref := range []tracker.ItemRef{opts.KnownItem, opts.ParentItem, opts.ChildlessItem} {
		if !slices.Contains(cheap, ref.String()) {
			absent = append(absent, ref.String())
		}
	}
	if len(absent) > 0 {
		return false, fmt.Sprintf("the scan resolved %d item(s) and does not include %s, which the fixtures place on "+
			"this board; an audit over the shorter list reports every item it never examined as compliant",
			len(cheap), strings.Join(absent, ", "))
	}
	return true, fmt.Sprintf("%d item(s) under both the cheapest and the fullest Selection, including every item the "+
		"fixtures place on the board", len(cheap))
}

// scanRefs scans a board and returns the item addresses, sorted. The second
// return is a failure detail, empty when the scan was conformant.
func scanRefs(
	ctx context.Context, c tracker.Tracker, board tracker.BoardRef, sel tracker.Selection, which string,
) ([]string, string) {
	res, err := c.Scan(ctx, board, sel)
	if err != nil {
		return nil, fmt.Sprintf("Scan(%s) under %s Selection failed on a board the fixtures say it must scan: %v",
			board, which, err)
	}
	items, ierr := res.Items()
	if ierr != nil {
		return nil, fmt.Sprintf("Scan(%s) under %s Selection reported success with a resolution that carries no "+
			"list: %v", board, which, ierr)
	}
	refs := make([]string, 0, len(items))
	for _, it := range items {
		refs = append(refs, it.Ref.String())
	}
	slices.Sort(refs)
	return refs, ""
}

func symmetricDifference(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x+" (cheapest only)")
		}
	}
	for _, x := range b {
		if !slices.Contains(a, x) {
			out = append(out, x+" (fullest only)")
		}
	}
	slices.Sort(out)
	return out
}

// checkReadUnreadableIsNotEmpty reads the WHOLE board with the child sets on,
// rather than only the two item fixtures.
//
// The two fixtures are what make an unreadable child set distinguishable from
// an empty one — that needs an item known to have children beside one known to
// have none. The board-wide read is what makes the invariant beneath it
// enforceable: an item reporting a child-set failure AND a list of children can
// be any item, and a check that looked only at the two fixtures could never
// observe it on a third.
func checkReadUnreadableIsNotEmpty(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	deep, err := c.Scan(ctx, opts.ScannableBoard, tracker.Selection{Children: true})
	if err != nil {
		return false, fmt.Sprintf("Scan(%s) with Selection.Children failed on a board the fixtures say it must "+
			"scan: %v", opts.ScannableBoard, err)
	}
	items, ierr := deep.Items()
	if ierr != nil {
		return false, fmt.Sprintf("Scan with Selection.Children reported success with a resolution that carries "+
			"no list: %v", ierr)
	}
	for _, it := range items {
		if it.ChildrenErr != nil && it.Children != nil {
			return false, fmt.Sprintf("%s reports a child-set failure (%v) AND hands over a list of %d child(ren); "+
				"unknown dominates, so a caller reading the list acts on a child set nobody could read",
				it.Ref, it.ChildrenErr, len(it.Children))
		}
		if both := unreadAndPresent(it); len(both) > 0 {
			return false, fmt.Sprintf("%s names %s in Unread and also carries a value for it; a name in Unread means "+
				"the value is UNKNOWN, and an audit that read unknown as a value reports an item compliant on "+
				"evidence it never had", it.Ref, strings.Join(both, ", "))
		}
	}

	parent, found := findItem(items, opts.ParentItem)
	if !found {
		return false, fmt.Sprintf("the scan did not report %s, which the fixtures place on this board and say has "+
			"children", opts.ParentItem)
	}
	childless, found := findItem(items, opts.ChildlessItem)
	if !found {
		return false, fmt.Sprintf("the scan did not report %s, which the fixtures place on this board and say has "+
			"none", opts.ChildlessItem)
	}
	if childless.ChildrenErr != nil {
		return false, fmt.Sprintf("%s has no children and its child-set read reported a failure (%v); a childless "+
			"item is a successful read of nothing, and reporting it as unknown makes every rollup over it NOT RUN",
			childless.Ref, childless.ChildrenErr)
	}
	if len(childless.Children) != 0 {
		return false, fmt.Sprintf("%s reports %d child(ren), and the fixtures name it as having none",
			childless.Ref, len(childless.Children))
	}
	if parent.ChildrenErr != nil {
		return false, fmt.Sprintf("%s is an item the fixtures say the connector can read the children of, and its "+
			"child-set read failed: %v", parent.Ref, parent.ChildrenErr)
	}

	traversable := c.Capabilities().Has(tracker.CapNativeHierarchy)
	switch {
	case traversable && len(parent.Children) == 0:
		return false, fmt.Sprintf("%s reports no children and no error, and the fixtures say it HAS at least one; "+
			"with %s declared the link is traversable, so an empty child set beside a nil error is a swallowed "+
			"failure — and a rollup over it completes a parent because it could not see the child that is not done",
			parent.Ref, tracker.CapNativeHierarchy)
	case !traversable && len(parent.Children) > 0:
		return false, fmt.Sprintf("%s reports %d child(ren) and %s is not declared; a hierarchy the backend does not "+
			"maintain cannot be traversed back, so these are children a host would plan a rollup over and never be "+
			"able to re-read", parent.Ref, len(parent.Children), tracker.CapNativeHierarchy)
	}

	// The cheap read must carry no child payload at all. A field excluded by a
	// Selection is a THIRD state — not asked for — and an item that carries it
	// anyway has erased the distinction its own Selected field exists to keep.
	shallow, err := c.GetItems(ctx, opts.ScannableBoard, []tracker.ItemRef{opts.ParentItem}, tracker.Selection{})
	if err != nil {
		return false, fmt.Sprintf("GetItems(%s) under the cheapest Selection failed: %v", opts.ParentItem, err)
	}
	cheapItems, ierr := shallow.Items()
	if ierr != nil {
		return false, fmt.Sprintf("GetItems(%s) under the cheapest Selection reported success with a resolution that "+
			"carries no list: %v", opts.ParentItem, ierr)
	}
	for _, it := range cheapItems {
		if len(it.Children) > 0 || it.ChildrenErr != nil {
			return false, fmt.Sprintf("%s was read with Selection.Children off and still reports %d child(ren) and "+
				"ChildrenErr=%v; children are present only when they were asked for, and a reader cannot tell "+
				"not-asked-for from empty once they are not", it.Ref, len(it.Children), it.ChildrenErr)
		}
		if both := unreadAndPresent(it); len(both) > 0 {
			return false, fmt.Sprintf("%s names %s in Unread and also carries a value for it", it.Ref,
				strings.Join(both, ", "))
		}
	}

	answer := "reports its children"
	if !traversable {
		answer = fmt.Sprintf("reports no traversable children, which is what %s not being declared means",
			tracker.CapNativeHierarchy)
	}
	return true, fmt.Sprintf("%s %s and %s is empty with no error; neither carries a child set it was not asked for",
		parent.Ref, answer, childless.Ref)
}

// unreadAndPresent returns the field names an item reports as unread while also
// carrying a value for them.
func unreadAndPresent(it tracker.Item) []string {
	var both []string
	for _, name := range it.Unread {
		if _, ok := it.Fields[name]; ok {
			both = append(both, strconv.Quote(name))
		}
	}
	slices.Sort(both)
	return both
}

// checkSelectionEchoed drives BOTH read operations under BOTH the zero and the
// full Selection — four probes, and every one of them earns its place.
//
// The zero Selection is the case a connector that never populates
// [tracker.Item.Selected] passes by accident, because the field's zero value is a
// legitimate cheap read. A NON-zero Selection is the case a connector that
// populates the field with the zero Selection — or with a Selection of its own
// invention — passes by accident. An operation driven under only one of the two
// is driven past the defect the check exists to catch, and it has to be both
// operations because they are separate implementations of the same obligation:
// Scan usually builds its items from a list endpoint and GetItems from a
// per-item one.
func checkSelectionEchoed(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	refs := []tracker.ItemRef{opts.KnownItem, opts.ParentItem, opts.ChildlessItem}
	for _, probe := range []struct {
		which string
		sel   tracker.Selection
	}{
		// The zero Selection FIRST, so a connector that fails both probes is
		// reported against the cheap read a reviewer can reproduce in one call.
		{"the cheapest", tracker.Selection{}},
		{"the fullest", fullSelection},
	} {
		got, err := c.GetItems(ctx, opts.ScannableBoard, refs, probe.sel)
		if err != nil {
			return false, fmt.Sprintf("GetItems under %s Selection failed on items the fixtures say it must read: %v",
				probe.which, err)
		}
		items, ierr := got.Items()
		if ierr != nil {
			return false, fmt.Sprintf("GetItems under %s Selection reported success with a resolution that carries "+
				"no list: %v", probe.which, ierr)
		}
		if detail := selectionEcho(items, probe.sel, "GetItems", probe.which); detail != "" {
			return false, detail
		}

		res, serr := c.Scan(ctx, opts.ScannableBoard, probe.sel)
		if serr != nil {
			return false, fmt.Sprintf("Scan(%s) under %s Selection failed on a board the fixtures say it must scan: %v",
				opts.ScannableBoard, probe.which, serr)
		}
		scanned, sierr := res.Items()
		if sierr != nil {
			return false, fmt.Sprintf("Scan(%s) under %s Selection reported success with a resolution that carries "+
				"no list: %v", opts.ScannableBoard, probe.which, sierr)
		}
		if detail := selectionEcho(scanned, probe.sel, "Scan", probe.which); detail != "" {
			return false, detail
		}
	}
	return true, "every item carries the Selection its read was given, from Scan and from GetItems, under the " +
		"cheapest Selection and the fullest"
}

func selectionEcho(items []tracker.Item, want tracker.Selection, op, which string) string {
	for _, it := range items {
		if it.Selected != want {
			return fmt.Sprintf("%s under %s Selection returned %s carrying Selected=%+v, and the read was given "+
				"%+v; an item that travels away from the call that read it can then no longer tell not-asked-for "+
				"from unset", op, which, it.Ref, it.Selected, want)
		}
	}
	return ""
}

// applyProbeField is a field name no board carries, so a write to it is
// refusable before any network call — twice over, since the value it is given
// has no ValueKind either.
const applyProbeField = "arqtos-conformance-probe--no-board-carries-this-field"

// closeProbeTarget is an address on this board that no item can have:
// [tracker.ItemRef.Valid] requires a number above zero and no tracker in this estate
// numbers items from it.
//
// It is what the close in [attributionSet] is aimed at, and that is a safety
// property rather than a stylistic one. A close is the one change in the set
// that could destroy something, so it must not be possible for it to land even
// against a connector that ignored its own refusals: a close with no reason,
// pointed at an item number that cannot exist, closes nothing whatever the
// connector does with it.
func closeProbeTarget(opts Options) tracker.ItemRef {
	return tracker.ItemRef{Board: opts.ScannableBoard, Scope: opts.KnownItem.Scope, Number: 0}
}

// attributionSet is a change set in which EVERY change is refusable before any
// network call, for a different reason each: a target on another board, a field
// nothing can route, and a close with no reason.
//
// Nothing in it can be applied, and nothing in it can mutate a live item even
// if the connector applied it anyway — see [closeProbeTarget] and [Run]'s
// account of what a run can touch. It is still three changes with three
// distinct outcomes, so a report that attributes them is a report whose Failed
// map has actually been exercised.
func attributionSet(opts Options) []tracker.Change {
	offBoard := tracker.ItemRef{Board: opts.ForeignItem.Board, Scope: opts.KnownItem.Scope, Number: opts.KnownItem.Number}
	return []tracker.Change{
		{Target: offBoard, Fields: map[string]tracker.Value{}},
		{Target: opts.KnownItem, Fields: map[string]tracker.Value{applyProbeField: {}}},
		{Target: closeProbeTarget(opts), Lifecycle: &tracker.Lifecycle{Close: true, Reason: tracker.CloseReasonUnspecified}},
	}
}

// attributedRefusal reads the refusal the report attributes to change i, and is
// the SINGLE place both write checks read a pre-network refusal out of.
//
// # The reading it enforces
//
// [tracker.Tracker.Apply] makes an error the connector cannot ATTRIBUTE to a change a
// hard failure of the whole call. The converse binds too, and this is where it
// is enforced: a refusal the connector CAN attribute belongs in
// [tracker.ApplyReport.Failed] at that change's index, classified. A pre-network
// refusal — a target on another board, a field nothing can route, a close with
// no reason, a parent on another tracker — is attributable by construction,
// because the connector found it while looking at one change and knows that
// change's index. Folding one into a whole-call error discards what the report
// exists to carry: a fifty-change sweep answered with "one of your changes is
// invalid" tells an operator nothing about which.
//
// [CheckApplyAttribution] and [CheckApplyCrossTrackerRefused] both go through
// here, and that is deliberate rather than tidy: two checks enforcing one rule
// in two places is how they came to demand opposite things of one connector.
//
// The FIRST return is a failure detail, empty when the shape is conformant; the
// second is the refusal itself, for a caller that has a rule about its kind.
func attributedRefusal(
	opts Options, changes []tracker.Change, rep tracker.ApplyReport, err error, i int,
) (string, error) {
	if err != nil {
		// SUBSUMED by the whole-call branch below, deliberately kept and
		// annotated like the other subsumed guards here: an unclassified
		// whole-call error fails the check either way. What this adds is the
		// worse of the two faults being named first — a host can act on
		// "attributed nothing" and cannot act on prose it has to parse.
		if !cerr.Classified(err) {
			return fmt.Sprintf("Apply refused a change set of %d with an unclassified error: %v",
				len(changes), err), nil
		}
		return fmt.Sprintf("Apply refused a change set of %d as a whole (%s: %v) instead of attributing an "+
			"outcome to change %d; that change is refusable on its own account, before any network call, so the "+
			"connector knows its index — and a whole-call error discards the attribution the report exists to carry, "+
			"leaving a caller unable to say which change was wrong", len(changes), cerr.KindOf(err), err, i), nil
	}

	// The report's arithmetic is the host's guard, not a second copy of it.
	// SUBSUMED by the attribution reads below — CheckApplyReport ZEROES a report
	// whose arithmetic does not close, and a zeroed report attributes nothing,
	// so those fail too. What this branch adds is the arithmetic fault's own
	// words: which count disagreed with which, rather than "change 0 is listed
	// neither as applied nor as failed" about a report that never got that far.
	checked, ferr := tracker.CheckApplyReport(opts.Manifest.Name, "Apply", len(changes), rep, nil)
	if ferr != nil {
		return fmt.Sprintf("the report does not account for the change set: %v", ferr), nil
	}
	reason, listed := checked.Failed[i]
	if !listed {
		return fmt.Sprintf("change %d is listed neither as applied nor as failed, and the report claims %d of "+
			"%d applied; this change is refusable before any network call, and a change this report does not list as "+
			"failed was APPLIED — so this says a write nobody can account for succeeded, which against a live board "+
			"means it may have", i, checked.Applied, len(changes)), nil
	}
	// A listed change with a nil reason is already refused by CheckApplyReport
	// above, so what reaches here is a non-nil error that carries no
	// classification.
	if !cerr.Classified(reason) {
		return fmt.Sprintf("change %d was attributed an unclassified failure, so a host would have to parse its "+
			"text to act on it: %v", i, reason), nil
	}
	return "", reason
}

// applyRefusal is how a connector refused change i, wherever it put the answer:
// the whole-call error when there is one, and otherwise the reason the report
// attributes to that change. The bool is FALSE when the change was refused in
// neither place — which, by [tracker.Tracker.Apply]'s rule, means it was applied.
//
// It is deliberately laxer than [attributedRefusal]: WHERE a pre-network
// refusal belongs is [CheckApplyCrossTrackerRefused]'s verdict to report, and a
// second check asserting it would make one of the two impossible to observe
// failing on its own. What a caller of this one wants is the classification, so
// that is all it takes a position on.
func applyRefusal(rep tracker.ApplyReport, err error, i int) (bool, error) {
	if err != nil {
		return true, err
	}
	reason, listed := rep.Failed[i]
	return listed && reason != nil, reason
}

// checkApplyAttribution holds a connector to the reading of the attribution
// rule stated on [attributedRefusal], and reads every change's outcome through
// it.
func checkApplyAttribution(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	changes := attributionSet(opts)
	rep, err := c.Apply(ctx, opts.ScannableBoard, changes)

	// Every index, not a count. Applied == 0 is IMPLIED by this loop plus the
	// arithmetic CheckApplyReport closes — Applied + len(Failed) == Requested
	// with all three indices in Failed leaves nothing applied — so asserting it
	// separately would be a branch that cannot fail, which is the shape this
	// harness exists to refuse.
	for i := range changes {
		if detail, _ := attributedRefusal(opts, changes, rep, err, i); detail != "" {
			return false, detail
		}
	}
	return true, fmt.Sprintf("%d refusable change(s), all attributed, none applied", len(changes))
}

// A foreignParent is one way a parent can be on another tracker, and the words
// for it.
type foreignParent struct {
	how string
	ref tracker.ItemRef
}

// foreignSuffix makes a board address that cannot be this one. It is appended
// rather than substituted so the resulting address is still plausible: the
// refusal must come from the comparison, not from a component that looks
// obviously fake.
const foreignSuffix = "-another-tracker"

// foreignParents is the set of foreign parents the refusal is driven with.
//
// The first three differ from the board under test in ONE component each and
// carry the SAME scope and number as an item that really is on it. That is
// The cross-tracker failure mode made executable: two boards on one provider plus a
// tracker on another all number items from 1, so a refusal comparing the board
// component alone, or anything short of the whole address, accepts a parent on
// a different tracker. The fourth is the real foreign item the fixtures name.
func foreignParents(opts Options) []foreignParent {
	at := func(b tracker.BoardRef) tracker.ItemRef {
		return tracker.ItemRef{Board: b, Scope: opts.KnownItem.Scope, Number: opts.KnownItem.Number}
	}
	otherProvider := opts.ScannableBoard
	otherProvider.Provider += foreignSuffix
	otherInstance := opts.ScannableBoard
	otherInstance.Instance += foreignSuffix
	otherBoard := opts.ScannableBoard
	otherBoard.Board += foreignSuffix

	return []foreignParent{
		{"another provider, on the same instance and board name", at(otherProvider)},
		{"another instance of this provider, on the same board name", at(otherInstance)},
		{"another board on this instance", at(otherBoard)},
		{"the foreign item the fixtures name", opts.ForeignItem},
	}
}

// checkApplyCrossTrackerRefused holds a connector to the SAME reading of the
// attribution rule as [CheckApplyAttribution] — stated in full on
// [attributedRefusal] — so that one implementation satisfies both: the refusal
// of a foreign parent is required in the report, attributed to the change that
// carries it, and cerr.KindInvalid.
//
// Applied == 0 is not asserted separately. It is implied by the attributed
// refusal plus the arithmetic CheckApplyReport closes — one change, refused, so
// Applied + 1 == 1 — and an assertion that cannot fail is the shape this
// harness exists to refuse.
func checkApplyCrossTrackerRefused(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	for _, probe := range foreignParents(opts) {
		parent := probe.ref
		changes := []tracker.Change{{Target: opts.KnownItem, Parent: &parent}}
		// The harm, said once, so every branch below can name the fault alone.
		// With several trackers live at once a parent on another one is an
		// ordinary mistake, and the link it writes cannot be followed back from
		// either side.
		lead := fmt.Sprintf("a Change.Parent on %s (%s) against board %s must be refused with %s, attributed to the "+
			"change that carries it, before any network call: ", probe.how, parent, opts.ScannableBoard, cerr.KindInvalid)

		rep, err := c.Apply(ctx, opts.ScannableBoard, changes)
		detail, reason := attributedRefusal(opts, changes, rep, err, 0)
		if detail != "" {
			return false, lead + detail
		}
		live := cerr.KindOf(reason)
		if live != cerr.KindInvalid {
			return false, lead + fmt.Sprintf("it was refused with %s instead; %s reads as a condition that may "+
				"change, so a host retries a change set that can never be applied", live, live)
		}

		// The pre-network half, and it compares against the classification the
		// connector just gave rather than re-asserting the required one. That is
		// deliberate: re-asserting cerr.KindInvalid here would make the
		// assertion above redundant, and a duplicated assertion is one that
		// cannot be observed to fail on its own.
		//
		// What this compares is the property the duplicate cannot: whether the
		// context has any bearing on the answer. A refusal that happens before
		// any network call is unaffected by a context that is already done. A
		// connector that reached for the wire first answers with a transport
		// failure instead — retryable, telling a caller to try a change set that
		// will always be refused.
		//
		// It reads the answer through applyRefusal rather than attributedRefusal
		// for the same non-duplication reason: WHERE the refusal is reported is
		// the verdict above, and what is being observed here is only whether the
		// answer moved.
		dead, cancel := context.WithCancel(ctx)
		cancel()
		deadRep, derr := c.Apply(dead, opts.ScannableBoard, changes)
		refused, deadReason := applyRefusal(deadRep, derr, 0)
		if !refused {
			return false, lead + fmt.Sprintf("under a CANCELED context it was refused neither in the report nor by a "+
				"whole-call error, having been refused with %s under a live one; a change this report does not list "+
				"as failed was applied, and a refusal that happens before any network call cannot depend on the "+
				"context", live)
		}
		if k := cerr.KindOf(deadReason); k != live {
			return false, lead + fmt.Sprintf("under a CANCELED context it was answered with %s, and with %s under a "+
				"live one; the refusal is required BEFORE any network call, so the context cannot change the answer — "+
				"a connector whose answer moves reached the wire first and classified a permanently-invalid change "+
				"set as a transport problem", k, live)
		}
	}
	return true, fmt.Sprintf("%d foreign parent(s) refused with %s, attributed to the change that carries them, "+
		"before any network call", len(foreignParents(opts)), cerr.KindInvalid)
}

// catalogueProbe is written INTO a catalogue a connector handed back. If it
// reappears in the next one, the connector returned its own state and the
// caller could not have avoided holding it.
const catalogueProbe = "arqtos-conformance-probe--not-a-real-name"

func checkApplyNoCachedIdentity(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	scopes := fixtureScopes(opts)

	first, err := c.Catalogue(ctx, opts.ScannableBoard, scopes)
	if err != nil {
		return false, fmt.Sprintf("Catalogue(%s) failed on a board the fixtures say it must serve: %v",
			opts.ScannableBoard, err)
	}
	if detail := catalogueShape(first, opts.ScannableBoard); detail != "" {
		return false, detail
	}
	touched := probeCatalogue(&first)
	if len(touched) == 0 {
		return false, fmt.Sprintf("Catalogue(%s) reports no field, no label and no train, so nothing this board "+
			"addresses by name came back and there is nothing to check the second read against; a board the fixtures "+
			"say is scannable serves at least one of the three", opts.ScannableBoard)
	}

	second, err := c.Catalogue(ctx, opts.ScannableBoard, scopes)
	if err != nil {
		return false, fmt.Sprintf("a second Catalogue(%s) failed where the first succeeded: %v",
			opts.ScannableBoard, err)
	}
	if detail := catalogueShape(second, opts.ScannableBoard); detail != "" {
		return false, detail
	}
	if leaked := catalogueLeaks(second); len(leaked) > 0 {
		return false, fmt.Sprintf("a second Catalogue carries %s written into the first; the connector handed back "+
			"its own state, so a caller cannot help holding a catalogue it was told not to store — and on this class "+
			"of backend editing one option of a single-select regenerates every option identity behind it, so a held "+
			"catalogue addresses options that no longer exist and writes land nowhere", strings.Join(leaked, " and "))
	}

	// Two Applies of one refused change set must classify identically. It is
	// the closest a caller can get to observing a stale identity through a
	// contract that carries none: a connector answering differently for the
	// same input, on a board that did not change, is deciding from state it
	// should not be holding.
	//
	// Each call must produce a CLASSIFICATION before the comparison means
	// anything, and that is checked rather than assumed. cerr.KindOf(nil) is
	// KindUnknown, so comparing two answers that were never obtained is
	// KindOf(nil) != KindOf(nil) — false, and reported as an observed pass. That
	// is the tautological gate this harness exists to refuse, and it does not
	// get to live inside the harness.
	parent := opts.ForeignItem
	changes := []tracker.Change{{Target: opts.KnownItem, Parent: &parent}}
	refusalKind := func(which string) (cerr.Kind, string) {
		rep, err := c.Apply(ctx, opts.ScannableBoard, changes)
		refused, reason := applyRefusal(rep, err, 0)
		switch {
		case !refused:
			return cerr.KindUnknown, fmt.Sprintf("the %s of two Applies of one change set naming a parent on another "+
				"tracker refused nothing: no whole-call error, and the report attributes no failure to the change. "+
				"The repeat-refusal probe compares two classifications, and a connector that produced neither is one "+
				"this check has nothing to say about — while %s reports the refusal that is missing",
				which, CheckApplyCrossTrackerRefused)
		case !cerr.Classified(reason):
			return cerr.KindUnknown, fmt.Sprintf("the %s of two Applies of one change set naming a parent on another "+
				"tracker refused it with an unclassified error, so there is no classification to compare against the "+
				"other call: %v", which, reason)
		}
		return cerr.KindOf(reason), ""
	}
	firstKind, detail := refusalKind("first")
	if detail != "" {
		return false, detail
	}
	secondKind, detail := refusalKind("second")
	if detail != "" {
		return false, detail
	}
	if firstKind != secondKind {
		return false, fmt.Sprintf("the same refused change set was classified %s and then %s; the board did not "+
			"change between the two calls, so the second answer came from state the connector carried over from the "+
			"first", firstKind, secondKind)
	}
	return true, fmt.Sprintf("a second Catalogue is free of %s written into the first, and one refused change set "+
		"classifies the same twice (%s)", strings.Join(touched, " and "), firstKind)
}

// fixtureScopes is every scope the fixtures name, deduplicated and sorted so a
// catalogue read is reproducible.
func fixtureScopes(opts Options) []tracker.Scope {
	all := []tracker.Scope{opts.KnownItem.Scope, opts.MissingItem.Scope, opts.ParentItem.Scope, opts.ChildlessItem.Scope}
	slices.Sort(all)
	return slices.Compact(all)
}

func catalogueShape(cat tracker.Catalogue, board tracker.BoardRef) string {
	if cat.Board != board {
		return fmt.Sprintf("Catalogue reports it is for %s, and %s was asked about; a caller holding catalogues for "+
			"two boards of one provider would check one against the other's schema", cat.Board, board)
	}
	if cat.ScopeKind == "" {
		return fmt.Sprintf("Catalogue(%s) declares no ScopeKind; the contract deliberately does not know what a "+
			"scope IS, so a host naming a missing one to an operator has only this to name it with", board)
	}
	return ""
}

// probeCatalogue writes the probe into cat IN PLACE and reports what it was
// able to touch. The writes go through a slice's backing array and a map, which
// are what a connector shares when it returns a catalogue it kept.
func probeCatalogue(cat *tracker.Catalogue) []string {
	var touched []string
	if len(cat.Fields) > 0 {
		cat.Fields[0].Name = catalogueProbe
		touched = append(touched, "a field name")
	}
	for sc, names := range cat.Labels {
		cat.Labels[sc] = append(names, catalogueProbe)
		touched = append(touched, "a label")
		break
	}
	for sc, trains := range cat.Trains {
		cat.Trains[sc] = append(trains, tracker.Train{Name: catalogueProbe})
		touched = append(touched, "a train")
		break
	}
	return touched
}

func catalogueLeaks(cat tracker.Catalogue) []string {
	var leaked []string
	if slices.ContainsFunc(cat.Fields, func(f tracker.Field) bool { return f.Name == catalogueProbe }) {
		leaked = append(leaked, "a field named by the probe")
	}
	for sc, names := range cat.Labels {
		if slices.Contains(names, catalogueProbe) {
			leaked = append(leaked, fmt.Sprintf("a label named by the probe in scope %q", sc))
			break
		}
	}
	for sc, trains := range cat.Trains {
		if slices.ContainsFunc(trains, func(t tracker.Train) bool { return t.Name == catalogueProbe }) {
			leaked = append(leaked, fmt.Sprintf("a train named by the probe in scope %q", sc))
			break
		}
	}
	return leaked
}

// findItem returns the item at ref, and whether the read answered about it at
// all.
func findItem(items []tracker.Item, ref tracker.ItemRef) (tracker.Item, bool) {
	i := slices.IndexFunc(items, func(it tracker.Item) bool { return it.Ref == ref })
	if i < 0 {
		return tracker.Item{}, false
	}
	return items[i], true
}

// ---------------------------------------------------------------------------
// The two TrainAdmin checks
// ---------------------------------------------------------------------------

// trainProbePrefix names every train this harness ever asks a connector to
// create. It is deliberately unusable as a real delivery bucket: every spec
// carrying it is refusable before any network call, and one that appears on a
// board is a connector that created something it reported as refused.
const trainProbePrefix = "arqtos-conformance-probe--not-a-real-train"

// trainAdmin resolves the optional operation group behind
// [tracker.CapTrains], and states the obligation that holds when the
// capability is NOT declared.
//
// Both train checks route through it so that neither can become a silent skip.
// A connector without the capability has an obligation of its own — not to
// implement the interface — and asserting THAT is the same shape
// [CheckReadUnreadableIsNotEmpty] uses for [tracker.CapNativeHierarchy]: the
// opposite obligation in each direction, never nothing.
func trainAdmin(c tracker.Tracker) (admin tracker.TrainAdmin, pass bool, detail string) {
	a, implemented := c.(tracker.TrainAdmin)
	declared := c.Capabilities().Has(tracker.CapTrains)
	switch {
	case declared && !implemented:
		return nil, false, fmt.Sprintf("%s is declared and the connector does not implement TrainAdmin, so there is "+
			"no train surface to observe and a host that plans a train move calls into nothing", tracker.CapTrains)
	case !declared && implemented:
		return nil, false, fmt.Sprintf("the connector implements TrainAdmin and does not declare %s; a host reads the "+
			"manifest before it loads the connector, so it will never call this", tracker.CapTrains)
	case !declared:
		return nil, true, fmt.Sprintf("%s is not declared and TrainAdmin is not implemented: this connector serves no "+
			"train set, which is the obligation that holds in this direction", tracker.CapTrains)
	}
	return a, false, ""
}

func checkTrainsUnion(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	admin, done, detail := trainAdmin(c)
	if admin == nil {
		return done, detail
	}

	caps := c.Capabilities()
	name := opts.Manifest.Name
	scoped := caps.Has(tracker.CapScopedTrains)

	requested := fixtureScopes(opts)
	if scoped {
		requested = append(requested, opts.UnreadableScope)
	}

	res, err := admin.ListTrains(ctx, requested)
	// The host's own guard, not a restatement of it: the accounting, the
	// unknown-dominates rule and the partition shape are all checked there, and
	// a second copy here would be free to drift from the one a host runs.
	sets, gerr := tracker.CheckTrainSets(name, "ListTrains", caps, requested, res, err)
	if gerr != nil {
		return false, fmt.Sprintf("ListTrains over %d scope(s) does not hold the union rule: %v", len(requested), gerr)
	}
	entries, ierr := sets.Items()
	if ierr != nil {
		return false, fmt.Sprintf("ListTrains reported success with a resolution that carries no list: %v", ierr)
	}

	if !scoped {
		return true, fmt.Sprintf("%s is not declared, so trains are not partitioned and the whole answer is one entry "+
			"under the zero scope", tracker.CapScopedTrains)
	}

	// At least one scope the connector CAN read must come back readable.
	// Without this the check is satisfiable by reporting every scope as
	// unknown, which is a connector that cannot read trains at all while
	// declaring that it can — and a union of nothing but unknowns is a replan
	// that never runs rather than one that runs green.
	readable := 0
	var probe *tracker.ScopeTrains
	for i, e := range entries {
		if e.Scope == opts.UnreadableScope {
			probe = &entries[i]
			continue
		}
		if e.Err == nil {
			readable++
		}
	}
	if readable == 0 {
		return false, fmt.Sprintf("every one of the %d readable fixture scope(s) came back unreadable while %s is "+
			"declared; a connector that can read no scope's trains has no train set to union, and declaring the "+
			"capability tells a host otherwise", len(requested)-1, tracker.CapTrains)
	}
	if probe == nil {
		// Unreachable against a conformant connector: CheckTrainSets already
		// required an entry for every requested scope. Kept because a guard
		// whose contract changed underneath this check must fail loudly rather
		// than skip the assertion this check exists for.
		return false, fmt.Sprintf("no entry at all for scope %q, which the fixtures name as one this connector cannot "+
			"read; a scope with no entry is read as a scope needing nothing", opts.UnreadableScope)
	}
	if probe.Err == nil {
		return false, fmt.Sprintf("reported scope %q with %d train(s) and NO error, and the fixtures name it as one "+
			"this connector cannot read; unknown reported as a successful read of nothing is the one shape the union "+
			"rule forbids, because the set is a union over scopes and the less of it a caller can see the greener a "+
			"replan looks", opts.UnreadableScope, len(probe.Trains))
	}
	return true, fmt.Sprintf("%d readable scope(s) resolved and %q came back with a classified error rather than as a "+
		"scope with no trains", readable, opts.UnreadableScope)
}

func checkTrainsCreateVerified(ctx context.Context, c tracker.Tracker, opts Options) (bool, string) {
	admin, done, detail := trainAdmin(c)
	if admin == nil {
		return done, detail
	}

	caps := c.Capabilities()
	name := opts.Manifest.Name
	scoped := caps.Has(tracker.CapScopedTrains)
	scopes := fixtureScopes(opts)

	// Both specs carry the WRONG polarity of TrainSpec.Scope for what this
	// connector declared, which the contract makes cerr.KindInvalid in both
	// directions — required with CapScopedTrains, refused without it. So both
	// are refusable before any network call, and there are two of them because
	// one cannot show a report that succeeds for every name it was given.
	var probeScope tracker.Scope
	if !scoped {
		probeScope = scopes[0]
	}
	specs := []tracker.TrainSpec{
		{Scope: probeScope, Name: trainProbePrefix + "-1"},
		{Scope: probeScope, Name: trainProbePrefix + "-2"},
	}

	rep, err := admin.CreateTrains(ctx, specs)
	checked, gerr := tracker.CheckTrainsCreated(ctx, name, admin, caps, specs, rep, err)
	if gerr != nil {
		return false, fmt.Sprintf("CreateTrains over %d refusable spec(s) does not hold the report rules: %v",
			len(specs), gerr)
	}
	if checked.Applied != 0 {
		return false, fmt.Sprintf("reported %d of %d spec(s) created, and every one of them carries the wrong "+
			"polarity of TrainSpec.Scope for a connector that %s declare %s — a refusal the contract requires before "+
			"any network call", checked.Applied, checked.Requested, declaredWord(scoped), tracker.CapScopedTrains)
	}
	for i := range specs {
		reason, listed := checked.Failed[i]
		if !listed {
			return false, fmt.Sprintf("spec %d is listed neither as applied nor as failed; a spec this report does "+
				"not attribute was applied, so an unaccounted one is reported as a success", i)
		}
		if !cerr.Classified(reason) {
			return false, fmt.Sprintf("spec %d was refused with an unclassified error (%v); a host routes on the "+
				"classification, and a failure the connector cannot classify is cerr.KindUnknown", i, reason)
		}
		if got := cerr.KindOf(reason); got != cerr.KindInvalid {
			return false, fmt.Sprintf("spec %d carries the wrong polarity of TrainSpec.Scope and was refused with "+
				"%v; the contract makes that cerr.KindInvalid, and a host retries what is classified as transient",
				i, got)
		}
	}

	// The RE-READ, and the reason this check is not the arithmetic one. A
	// report that refused every spec and a board that carries one of them
	// anyway is the only shape the counts cannot show, and it is exactly the
	// shape a create loop with a misplaced continue produces.
	res, lerr := admin.ListTrains(ctx, scopes)
	sets, verr := tracker.CheckTrainSets(name, "ListTrains", caps, scopes, res, lerr)
	if verr != nil {
		return false, fmt.Sprintf("the re-read that verifies the create could not be carried out, so the outcome is "+
			"unknown rather than clean: %v", verr)
	}
	entries, ierr := sets.Items()
	if ierr != nil {
		return false, fmt.Sprintf("the verifying ListTrains reported success with a resolution that carries no list: "+
			"%v", ierr)
	}
	for _, e := range entries {
		for _, tr := range e.Trains {
			if !strings.HasPrefix(tr.Name, trainProbePrefix) {
				continue
			}
			return false, fmt.Sprintf("a re-read finds train %q in scope %s, and the report said every spec was "+
				"refused; a create loop that iterated once returns successfully for every name it was given, so the "+
				"report is not evidence and the re-read is", tr.Name, quoteScope(e.Scope))
		}
	}
	return true, fmt.Sprintf("%d refusable spec(s) each attributed as cerr.KindInvalid, and a re-read finds none of "+
		"them on the board", len(specs))
}

func declaredWord(declared bool) string {
	if declared {
		return "does"
	}
	return "does not"
}

func quoteScope(s tracker.Scope) string {
	if s == "" {
		return "the zero scope"
	}
	return strconv.Quote(string(s))
}
