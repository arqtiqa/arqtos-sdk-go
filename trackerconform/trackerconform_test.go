package trackerconform

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/tracker"
)

// The fixtures every run in this file is driven with.
//
// They are a MODEL of the live fixtures Options documents, not a shortcut past
// them: the scannable board holds four items across two scopes, the
// unscannable board is a second board on the same instance the stub refuses,
// and the foreign board is a second PROVIDER — which is what makes the
// cross-tracker check's fully-qualified comparison observable.
var (
	fixScannable   = tracker.BoardRef{Provider: "stubtracker", Instance: "stub-org", Board: "1"}
	fixUnscannable = tracker.BoardRef{Provider: "stubtracker", Instance: "stub-org", Board: "2"}
	fixForeign     = tracker.BoardRef{Provider: "otherprovider", Instance: "other-org", Board: "1"}

	fixKnown     = tracker.ItemRef{Board: fixScannable, Scope: "alpha", Number: 101}
	fixParent    = tracker.ItemRef{Board: fixScannable, Scope: "alpha", Number: 102}
	fixChildless = tracker.ItemRef{Board: fixScannable, Scope: "beta", Number: 103}
	fixChild     = tracker.ItemRef{Board: fixScannable, Scope: "alpha", Number: 104}
	fixMissing   = tracker.ItemRef{Board: fixScannable, Scope: "alpha", Number: 999}
	// fixForeignItem shares fixKnown's scope and number on purpose: the only
	// thing that distinguishes it is the fully-qualified board address, which
	// is exactly what the contract says the refusal must compare.
	fixForeignItem = tracker.ItemRef{Board: fixForeign, Scope: "alpha", Number: 101}

	// fixUnreadableScope is a scope the stub's TrainAdmin refuses to read, and
	// it is deliberately NOT one of the item-fixture scopes: those are scopes
	// the connector must be able to read.
	fixUnreadableScope tracker.Scope = "gamma"
)

// stubField is the one field the stub's Catalogue serves. A write to any other
// name is refused, which is what makes the attribution probe's unknown field
// refusable without a network call.
const stubField = "Status"

// stub is a Tracker that passes every check, with one knob per operation so a
// test can break exactly one property and watch the harness catch it.
//
// A harness only ever run against compliant input proves nothing about what it
// would catch, so every check in the table below is driven by a stub
// deliberately built to violate it — see TestRun_EveryCheckHasAViolatingStub,
// which fails if a check ever loses its falsifier.
type stub struct {
	class connector.Class
	caps  connector.Capabilities

	catalogue func(context.Context, tracker.BoardRef, []tracker.Scope) (tracker.Catalogue, error)
	scan      func(context.Context, tracker.BoardRef, tracker.Selection) (tracker.Resolution[tracker.Item], error)
	getItems  func(context.Context, tracker.BoardRef, []tracker.ItemRef, tracker.Selection) (tracker.Resolution[tracker.Item], error)
	apply     func(context.Context, tracker.BoardRef, []tracker.Change) (tracker.ApplyReport, error)
	health    func(context.Context) (connector.Health, error)
}

func newStub() *stub {
	return &stub{class: connector.ClassTracker, caps: connector.Capabilities{tracker.CapNativeHierarchy}}
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

func (s *stub) Catalogue(ctx context.Context, board tracker.BoardRef, scopes []tracker.Scope) (tracker.Catalogue, error) {
	if s.catalogue != nil {
		return s.catalogue(ctx, board, scopes)
	}
	if board != fixScannable {
		return tracker.Catalogue{}, cerr.New(cerr.KindNotFound, "Catalogue", fmt.Errorf("no such board %s", board))
	}
	// Built fresh on every call, which is what "must not be stored" requires of
	// the connector side: a catalogue handed out by reference is one the caller
	// cannot help holding.
	cat := tracker.Catalogue{
		Board:     board,
		ScopeKind: "stub-partition",
		Fields: []tracker.Field{{
			Name: stubField, Class: tracker.FieldClassBoard, Accepts: tracker.ValueOption,
			Options: []string{"Backlog", "In Build"},
		}},
		Types:  []string{"Story"},
		Labels: map[tracker.Scope][]string{},
		Trains: map[tracker.Scope][]tracker.Train{},
	}
	for _, sc := range scopes {
		cat.Labels[sc] = []string{"needs-triage"}
		cat.Trains[sc] = []tracker.Train{{Name: "0.2.0", Open: true}}
	}
	return cat, nil
}

// stubItems is the whole of the stub's board, read under sel.
func stubItems(sel tracker.Selection, caps connector.Capabilities) []tracker.Item {
	item := func(ref tracker.ItemRef, children []tracker.ItemRef) tracker.Item {
		it := tracker.Item{Ref: ref, Title: "item " + ref.String(), Type: "Story", Open: true, Selected: sel}
		if sel.BoardFields {
			it.Fields = map[string]tracker.Value{stubField: {Kind: tracker.ValueOption, Option: "Backlog"}}
		}
		// Children are present ONLY when they were asked for, and only where
		// the backend maintains a traversable link.
		if sel.Children && caps.Has(tracker.CapNativeHierarchy) {
			it.Children = children
		} else if sel.Children {
			it.Children = []tracker.ItemRef{}
		}
		return it
	}
	return []tracker.Item{
		item(fixKnown, []tracker.ItemRef{}),
		item(fixParent, []tracker.ItemRef{fixChild}),
		item(fixChildless, []tracker.ItemRef{}),
		item(fixChild, []tracker.ItemRef{}),
	}
}

func (s *stub) Scan(ctx context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
	if s.scan != nil {
		return s.scan(ctx, board, sel)
	}
	if board != fixScannable {
		return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", fmt.Errorf("no such board %s", board))
	}
	return tracker.Resolved(stubItems(sel, s.caps), tracker.Complete)
}

func (s *stub) GetItems(ctx context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
	if s.getItems != nil {
		return s.getItems(ctx, board, refs, sel)
	}
	if board != fixScannable {
		return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "GetItems", fmt.Errorf("no such board %s", board))
	}
	all := stubItems(sel, s.caps)
	out := make([]tracker.Item, 0, len(refs))
	for _, ref := range refs {
		if ref.Board != board {
			return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindInvalid, "GetItems",
				fmt.Errorf("%s is not on %s", ref, board))
		}
		idx := slices.IndexFunc(all, func(it tracker.Item) bool { return it.Ref == ref })
		if idx < 0 {
			return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "GetItems", fmt.Errorf("no such item %s", ref))
		}
		out = append(out, all[idx])
	}
	if len(out) == 0 {
		return tracker.EmptyList[tracker.Item](), nil
	}
	return tracker.Resolved(out, tracker.Complete)
}

func (s *stub) Create(ctx context.Context, board tracker.BoardRef, drafts []tracker.Draft) (tracker.Resolution[tracker.Item], error) {
	// The harness never files anything, so this exists only to satisfy the
	// contract. It refuses rather than pretending to have filed.
	return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindUnsupported, "Create", errors.New("the conformance stub does not file items"))
}

func (s *stub) Apply(ctx context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
	if s.apply != nil {
		return s.apply(ctx, board, changes)
	}
	// A board that is not fully qualified is the one fault here that cannot be
	// attributed to a change, because it is not about a change. Everything else
	// is per-change and lands in the report — the reading stated on
	// attributedRefusal, which the cross-tracker parent obeys like every other
	// pre-network refusal.
	if !board.Valid() {
		return tracker.ApplyReport{}, cerr.New(cerr.KindInvalid, "Apply", errors.New("board is not fully qualified"))
	}
	rep := stubApplyReport(board, changes, stubRefusal)
	// The pre-network refusals are decided BEFORE the context is consulted: a
	// change set that can never become valid must not come back as a transport
	// problem, which would tell the caller to retry something that will always
	// be refused. Only a change that would actually reach the wire needs a live
	// context.
	if rep.Applied > 0 {
		if err := ctx.Err(); err != nil {
			return tracker.ApplyReport{}, cerr.New(cerr.KindUnavailable, "Apply", err)
		}
	}
	return rep, nil
}

// stubApplyReport attributes every pre-network refusal refuse finds to its own
// change, which is what the contract requires of a fault the connector can name
// the index of.
func stubApplyReport(board tracker.BoardRef, changes []tracker.Change, refuse func(tracker.BoardRef, tracker.Change) error) tracker.ApplyReport {
	failed := map[int]error{}
	for i, ch := range changes {
		if err := refuse(board, ch); err != nil {
			failed[i] = err
		}
	}
	return tracker.ApplyReport{Requested: len(changes), Applied: len(changes) - len(failed), Failed: failed}
}

// stubRefusal is the whole of the stub's per-change, pre-network validation.
func stubRefusal(board tracker.BoardRef, ch tracker.Change) error {
	if err := stubTargetRefusal(board, ch); err != nil {
		return err
	}
	return stubParentRefusal(board, ch)
}

// stubTargetRefusal is every pre-network rule except the cross-tracker parent.
// It is separate so a violating stub can drop the parent rule ALONE and leave
// the rest of the connector conformant.
func stubTargetRefusal(board tracker.BoardRef, ch tracker.Change) error {
	if err := stubPlaceRefusal(ch); err != nil {
		return err
	}
	return stubTargetRefusalIgnoringPlace(board, ch)
}

// stubPlaceRefusal is the board-membership rule for a stub that does NOT
// declare tracker.CapBoardMembership, which every stub built by newStub is.
//
// It is separate for the same reason stubParentRefusal is: so a violating stub
// can drop this rule ALONE — the drop being the real-world defect, a connector
// that takes the flag and silently does nothing with it.
func stubPlaceRefusal(ch tracker.Change) error {
	if ch.Place {
		return cerr.New(cerr.KindUnsupported, "Apply",
			errors.New("this connector does not administer board membership"))
	}
	return nil
}

// stubTargetRefusalIgnoringPlace is stubTargetRefusal without the membership
// rule, for the stub that violates it and for the stub that legitimately serves
// placement.
func stubTargetRefusalIgnoringPlace(board tracker.BoardRef, ch tracker.Change) error {
	if !ch.Target.Valid() || ch.Target.Board != board {
		return cerr.New(cerr.KindInvalid, "Apply", fmt.Errorf("target %s is not an item on %s", ch.Target, board))
	}
	for name, v := range ch.Fields {
		if !v.Kind.Writable() {
			return cerr.New(cerr.KindInvalid, "Apply", fmt.Errorf("field %q was given %s", name, v.Kind))
		}
		if name != stubField {
			return cerr.New(cerr.KindInvalid, "Apply", fmt.Errorf("this board carries no field %q", name))
		}
	}
	if ch.Lifecycle != nil && ch.Lifecycle.Close && !ch.Lifecycle.Reason.UsableAsReason() {
		return cerr.New(cerr.KindInvalid, "Apply", errors.New("a close needs a reason"))
	}
	return nil
}

// stubParentRefusal is the cross-tracker rule: a parent on another tracker is
// cerr.KindInvalid, attributed to the change that carries it.
func stubParentRefusal(board tracker.BoardRef, ch tracker.Change) error {
	if ch.Parent != nil && *ch.Parent != (tracker.ItemRef{}) && ch.Parent.Board != board {
		return cerr.New(cerr.KindInvalid, "Apply",
			fmt.Errorf("parent %s is on another tracker than %s", ch.Parent, board))
	}
	return nil
}

var _ tracker.Tracker = (*stub)(nil)

// trainAdminStub implements the optional operation group behind CapTrains. It
// is a separate type because implementing it is what the harness type-asserts
// for.
//
// It keeps what it CREATED, because that is the only way a test can drive the
// difference between a report and the board: a stub whose ListTrains answered
// from a constant table could not show a create that landed while the report
// said it was refused, which is the whole property the re-read check exists for.
type trainAdminStub struct {
	*stub

	// scoped makes the stub answer per scope, the way a connector declaring
	// CapScopedTrains must, and refuse a spec with no scope. Without it the
	// stub is unpartitioned: one entry under the zero scope, and a spec naming
	// a scope is refused instead.
	scoped bool
	// created is what CreateTrains actually created, keyed the way ListTrains
	// reports it.
	created map[tracker.Scope][]tracker.Train

	listTrains   func(context.Context, []tracker.Scope) (tracker.Resolution[tracker.ScopeTrains], error)
	createTrains func(context.Context, []tracker.TrainSpec) (tracker.ApplyReport, error)
}

// newTrainAdmin is a conformant TrainAdmin, in either partitioning.
func newTrainAdmin(scoped bool) *trainAdminStub {
	s := newStub()
	s.caps = trainCaps(scoped)
	return &trainAdminStub{stub: s, scoped: scoped, created: map[tracker.Scope][]tracker.Train{}}
}

func trainCaps(scoped bool) connector.Capabilities {
	caps := connector.Capabilities{tracker.CapNativeHierarchy, tracker.CapTrains}
	if scoped {
		caps = append(caps, tracker.CapScopedTrains)
	}
	return caps
}

func trainManifest(scoped bool) manifest.Doc { return stubManifest(trainCaps(scoped)...) }

func (a *trainAdminStub) ListTrains(ctx context.Context, scopes []tracker.Scope) (tracker.Resolution[tracker.ScopeTrains], error) {
	if a.listTrains != nil {
		return a.listTrains(ctx, scopes)
	}
	if !a.scoped {
		return tracker.Resolved([]tracker.ScopeTrains{{Trains: a.trainsIn("")}}, tracker.Complete)
	}
	sets := make([]tracker.ScopeTrains, 0, len(scopes))
	for _, sc := range scopes {
		if sc == fixUnreadableScope {
			// Unknown, with nothing readable beside it.
			sets = append(sets, tracker.ScopeTrains{
				Scope: sc,
				Err:   cerr.New(cerr.KindUnauthorized, "ListTrains", fmt.Errorf("no rights in scope %q", sc)),
			})
			continue
		}
		sets = append(sets, tracker.ScopeTrains{Scope: sc, Trains: a.trainsIn(sc)})
	}
	if len(sets) == 0 {
		return tracker.EmptyList[tracker.ScopeTrains](), nil
	}
	return tracker.Resolved(sets, tracker.Complete)
}

func (a *trainAdminStub) trainsIn(sc tracker.Scope) []tracker.Train {
	return append([]tracker.Train{{Name: "0.2.0", Open: true}}, a.created[sc]...)
}

func (a *trainAdminStub) CreateTrains(ctx context.Context, specs []tracker.TrainSpec) (tracker.ApplyReport, error) {
	if a.createTrains != nil {
		return a.createTrains(ctx, specs)
	}
	rep := tracker.ApplyReport{Requested: len(specs), Failed: map[int]error{}}
	for i, sp := range specs {
		// The contract's polarity rule: Scope is required with
		// CapScopedTrains and refused without it, cerr.KindInvalid either way.
		if a.scoped == (sp.Scope == "") {
			rep.Failed[i] = cerr.New(cerr.KindInvalid, "CreateTrains",
				fmt.Errorf("TrainSpec.Scope %q is the wrong polarity for scoped_trains=%v", sp.Scope, a.scoped))
			continue
		}
		a.created[sp.Scope] = append(a.created[sp.Scope], tracker.Train{Name: sp.Name, Open: true})
		rep.Applied++
	}
	return rep, nil
}

// schemaAdminStub implements the optional operation behind CapSchemaAdmin.
type schemaAdminStub struct{ *stub }

func (schemaAdminStub) EnsureFields(context.Context, tracker.BoardRef, []tracker.FieldSpec) (tracker.SchemaPlan, error) {
	return tracker.SchemaPlan{}, nil
}

var (
	_ tracker.TrainAdmin  = (*trainAdminStub)(nil)
	_ tracker.SchemaAdmin = schemaAdminStub{}
)

func stubManifest(caps ...connector.Capability) manifest.Doc {
	return manifest.Doc{
		Name:         "stubtracker",
		Implements:   connector.ClassTracker,
		Kind:         manifest.KindNative,
		Capabilities: caps,
	}
}

func fixtures(m manifest.Doc) Options {
	return Options{
		Manifest:         m,
		ScannableBoard:   fixScannable,
		UnscannableBoard: fixUnscannable,
		KnownItem:        fixKnown,
		MissingItem:      fixMissing,
		ForeignItem:      fixForeignItem,
		ParentItem:       fixParent,
		ChildlessItem:    fixChildless,
		UnreadableScope:  fixUnreadableScope,
	}
}

func run(t *testing.T, c tracker.Tracker, m manifest.Doc) Report {
	t.Helper()
	rep, err := Run(context.Background(), c, fixtures(m))
	if err != nil {
		t.Fatalf("the conformance run could not be carried out: %v", err)
	}
	return rep
}

// failed reports the Detail of the named check, and whether it failed at all.
func failed(rep Report, name string) (string, bool) {
	for _, res := range rep.Results {
		if res.Name == name {
			return res.Detail, !res.Pass
		}
	}
	return "", false
}

// passed is failed's counterpart, for a check whose PASS detail is itself the
// assertion — a check with two arms passes for two different reasons, and a
// report that does not say which one ran cannot be read.
func passed(rep Report, name string) (string, bool) {
	for _, res := range rep.Results {
		if res.Name == name {
			return res.Detail, res.Pass
		}
	}
	return "", false
}

// requireFailed is the assertion every violating-stub test ends in. It checks
// the Detail as well as the verdict: a failure with no detail tells a reviewer
// that something is wrong without saying what, which is a report nobody can
// act on.
func requireFailed(t *testing.T, rep Report, name string) {
	t.Helper()
	detail, did := failed(rep, name)
	if !did {
		t.Fatalf("%s passed against a connector built to violate it\n%s", name, rep)
	}
	if detail == "" {
		t.Errorf("%s failed with no detail, so the report does not say what is wrong", name)
	}
}

func requireGreen(t *testing.T, rep Report) {
	t.Helper()
	if err := rep.Err(); err != nil {
		t.Fatalf("a compliant connector was reported non-conformant: %v\n%s", err, rep)
	}
}

// requirePassed is the other half of requireFailed, for a stub aimed at ONE
// assertion: it pins the checks the violation must NOT reach, so a stub that
// breaks more of the contract than it meant to is a test failure rather than a
// silently over-broad falsifier.
func requirePassed(t *testing.T, rep Report, names ...string) {
	t.Helper()
	for _, name := range names {
		if detail, did := failed(rep, name); did {
			t.Errorf("%s failed too, and this stub is aimed elsewhere: %s\n%s", name, detail, rep)
		}
	}
}

// scanMangling is a Scan that reads the stub's board and then lets fix rewrite
// the items, so a test can break one property of one item and leave everything
// else about the connector conformant.
func scanMangling(s *stub, fix func(sel tracker.Selection, items []tracker.Item)) func(context.Context, tracker.BoardRef, tracker.Selection) (tracker.Resolution[tracker.Item], error) {
	return func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board != fixScannable {
			return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", fmt.Errorf("no such board %s", board))
		}
		items := stubItems(sel, s.caps)
		fix(sel, items)
		return tracker.Resolved(items, tracker.Complete)
	}
}

// itemAt returns a pointer into items so a mangler can rewrite one item in
// place. It fails the test rather than returning nil: a mangler aimed at an item
// the board does not carry would leave the stub conformant, and a conformant
// stub reported as a falsifier is the defect this file exists to prevent.
func itemAt(t *testing.T, items []tracker.Item, ref tracker.ItemRef) *tracker.Item {
	t.Helper()
	i := slices.IndexFunc(items, func(it tracker.Item) bool { return it.Ref == ref })
	if i < 0 {
		t.Fatalf("the stub board does not carry %s, so this violating stub violates nothing", ref)
	}
	return &items[i]
}

// ---------------------------------------------------------------------------
// The compliant path, and the completeness of the run
// ---------------------------------------------------------------------------

func TestConform_CompliantStub_IsGreenOnEveryCheck(t *testing.T) {
	rep := run(t, newStub(), stubManifest(tracker.CapNativeHierarchy))
	requireGreen(t, rep)

	var ran []string
	for _, res := range rep.Results {
		ran = append(ran, res.Name)
	}
	if !slices.Equal(ran, conformChecks()) {
		t.Errorf("the run reported\n  %v\nand the declared check list is\n  %v", ran, conformChecks())
	}
	t.Logf("%s", rep)
}

// TestConform_RunsEveryDeclaredCheck is the anti-drift gate on the check
// table: a Check* constant that is declared and never wired produces a name a
// CI allowlist can reference and a property nothing observes.
func TestConform_RunsEveryDeclaredCheck(t *testing.T) {
	declared := []string{
		CheckManifest, CheckClass, CheckCapabilityHonesty, CheckOptionalDeclared,
		CheckListNoEmptySuccess, CheckListFailClosed, CheckHealth,
		CheckScanPagedToExhaustion, CheckReadUnreadableIsNotEmpty, CheckSelectionEchoed,
		CheckApplyAttribution, CheckApplyCrossTrackerRefused, CheckApplyNoCachedIdentity,
		CheckPlacementHonoursItsCapability,
		CheckTrainsUnion, CheckTrainsCreateVerified,
	}
	got := conformChecks()
	if !slices.Equal(got, declared) {
		t.Fatalf("Run runs\n  %v\nand the class publishes\n  %v", got, declared)
	}
	if len(got) != 16 {
		t.Errorf("the harness runs %d checks; seven are the ones every class in this estate carries, seven are this "+
			"class's own reads and writes, and two are the TrainAdmin properties the class contract holds rather "+
			"than leaving to each backend: 16", len(got))
	}
}

// TestFullSelection_AsksForEverything keeps the "full" read full. A field
// added to Selection and not added here would leave every check that uses it
// reading a narrower payload than the contract allows, and nothing would say
// so.
func TestFullSelection_AsksForEverything(t *testing.T) {
	v := reflect.ValueOf(fullSelection)
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.Type.Kind() != reflect.Bool {
			t.Fatalf("Selection.%s is a %s, and fullSelection can no longer be checked by setting every bool",
				f.Name, f.Type.Kind())
		}
		if !v.Field(i).Bool() {
			t.Errorf("fullSelection leaves Selection.%s off, so no check ever asks for it", f.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// The zero-results guard: a harness that ran nothing is not a pass
// ---------------------------------------------------------------------------

// TestReport_WithNoResults_IsNotConformant is the tautological-gate guard. A
// report whose Results are empty has observed nothing, and
// len(Failures()) == 0 is true of it — which is exactly how a gate reports
// green for having looked at nothing.
func TestReport_WithNoResults_IsNotConformant(t *testing.T) {
	for _, rep := range []Report{
		{},
		{Connector: "named-but-empty"},
		{Connector: "explicitly-empty", Results: []Result{}},
	} {
		if rep.OK() {
			t.Errorf("OK() reported true for a report that ran no checks: %#v", rep)
		}
		err := rep.Err()
		if err == nil {
			t.Fatalf("Err() reported nil for a report that ran no checks: %#v", rep)
		}
		if got := cerr.KindOf(err); got != cerr.KindInvalid {
			t.Errorf("Err() kind = %s, want %s", got, cerr.KindInvalid)
		}
		if !strings.Contains(rep.String(), "no checks ran") {
			t.Errorf("String() does not say the run observed nothing, so a CI log reads it as a pass:\n%s", rep)
		}
	}
}

func TestReport_WithOneFailure_IsNotConformant(t *testing.T) {
	rep := Report{Connector: "c", Results: []Result{
		{Name: CheckClass, Pass: true},
		{Name: CheckHealth, Pass: false, Detail: "no"},
	}}
	if rep.OK() {
		t.Error("OK() reported true for a report with a failure")
	}
	if err := rep.Err(); err == nil {
		t.Fatal("Err() reported nil for a report with a failure")
	}
	// The FAIL line itself, which is what a reviewer reads in a CI log. Nothing
	// else in this file asserts it: every other report rendered here is green,
	// so the failure marker was published and never observed.
	if want := "FAIL " + CheckHealth + ": no"; !strings.Contains(rep.String(), want) {
		t.Errorf("String() does not mark the failed check with %q, so a CI log shows a failure as a pass:\n%s",
			want, rep)
	}
}

// TestRunWith_NoChecks_FailsTheRun drives Run's zero-results guard,
// which no connector and no fixture set can reach: the check table is a
// package-level constant. The guard is the reason a harness whose table was
// emptied by an edit cannot certify anything, and this is the only way to watch
// it fire.
func TestRunWith_NoChecks_FailsTheRun(t *testing.T) {
	for _, table := range [][]check{nil, {}} {
		rep, err := conformWith(context.Background(), newStub(), fixtures(stubManifest(tracker.CapNativeHierarchy)), table)
		if err == nil {
			t.Fatalf("a run over %d checks was reported as a verdict: %#v", len(table), rep)
		}
		if got := cerr.KindOf(err); got != cerr.KindInvalid {
			t.Errorf("kind = %s, want %s", got, cerr.KindInvalid)
		}
		if len(rep.Results) != 0 {
			t.Errorf("the refused run still returned %d result(s): %s", len(rep.Results), rep)
		}
	}
}

func TestReport_AllPassing_IsConformant(t *testing.T) {
	rep := Report{Connector: "c", Results: []Result{{Name: CheckClass, Pass: true}}}
	if !rep.OK() {
		t.Error("OK() reported false for a report whose every check passed")
	}
	if err := rep.Err(); err != nil {
		t.Errorf("Err() reported %v for a report whose every check passed", err)
	}
}

// ---------------------------------------------------------------------------
// The fixture gate: every fixture required, and coherent
// ---------------------------------------------------------------------------

func TestConform_RefusesANilConnector(t *testing.T) {
	if _, err := Run(context.Background(), nil, fixtures(stubManifest())); err == nil {
		t.Fatal("a run against no connector reported something")
	}
}

// TestConform_RefusesEveryMissingFixture zeroes one fixture at a time. An
// absent fixture must fail the RUN: a check that cannot be driven is not a
// check that passed.
//
// It requires the refusal to name the missing fixture IN THE POSITION the gate
// puts it — "Options.<field> is unusable" — and not merely to mention it
// somewhere in the prose. Several rules name ScannableBoard in their reason
// text, so the looser assertion passed for a zeroed ScannableBoard on the
// KnownItem rule's message, and the ScannableBoard rule could be deleted
// outright with this test still green.
func TestConform_RefusesEveryMissingFixture(t *testing.T) {
	for _, tc := range []struct {
		field  string
		break_ func(*Options)
	}{
		{"ScannableBoard", func(o *Options) { o.ScannableBoard = tracker.BoardRef{} }},
		{"UnscannableBoard", func(o *Options) { o.UnscannableBoard = tracker.BoardRef{} }},
		{"KnownItem", func(o *Options) { o.KnownItem = tracker.ItemRef{} }},
		{"MissingItem", func(o *Options) { o.MissingItem = tracker.ItemRef{} }},
		{"ForeignItem", func(o *Options) { o.ForeignItem = tracker.ItemRef{} }},
		{"ParentItem", func(o *Options) { o.ParentItem = tracker.ItemRef{} }},
		{"ChildlessItem", func(o *Options) { o.ChildlessItem = tracker.ItemRef{} }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			opts := fixtures(stubManifest(tracker.CapNativeHierarchy))
			tc.break_(&opts)
			_, err := Run(context.Background(), newStub(), opts)
			if err == nil {
				t.Fatalf("a run with no %s was carried out; a check that cannot be driven must not be skipped", tc.field)
			}
			want := fmt.Sprintf("Options.%s is unusable", tc.field)
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %s as the unusable fixture (%q): %v", tc.field, want, err)
			}
			if got := cerr.KindOf(err); got != cerr.KindInvalid {
				t.Errorf("kind = %s, want %s", got, cerr.KindInvalid)
			}
		})
	}
}

// TestConform_RefusesAPartiallyQualifiedFixture: a board address missing a
// component is refused by every operation in the contract, so a run driven
// with one would report the address refusal as the fail-closed property.
func TestConform_RefusesAPartiallyQualifiedFixture(t *testing.T) {
	opts := fixtures(stubManifest(tracker.CapNativeHierarchy))
	opts.UnscannableBoard.Instance = ""
	if _, err := Run(context.Background(), newStub(), opts); err == nil {
		t.Fatal("a run driven with a partially-qualified board was carried out")
	}
}

// TestConform_RefusesIncoherentFixtures is the gate that keeps the run from
// checking the wrong thing while looking green. Each case is a fixture set
// that is present in full and still cannot exercise the property it names.
func TestConform_RefusesIncoherentFixtures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*Options)
	}{
		{"the foreign item is on the board under test", func(o *Options) {
			o.ForeignItem = tracker.ItemRef{Board: o.ScannableBoard, Scope: "alpha", Number: 500}
		}},
		{"the foreign item differs only below the board address", func(o *Options) {
			o.ForeignItem.Board = o.ScannableBoard
		}},
		{"the unscannable board is the scannable one", func(o *Options) {
			o.UnscannableBoard = o.ScannableBoard
		}},
		{"the known item is on another board", func(o *Options) {
			o.KnownItem.Board = fixForeign
		}},
		{"the missing item is on another board", func(o *Options) {
			o.MissingItem.Board = fixForeign
		}},
		{"the missing item is the known one", func(o *Options) {
			o.MissingItem = o.KnownItem
		}},
		{"the parent item is on another board", func(o *Options) {
			o.ParentItem.Board = fixForeign
		}},
		{"the childless item is on another board", func(o *Options) {
			o.ChildlessItem.Board = fixForeign
		}},
		{"the parent and childless items are the same item", func(o *Options) {
			o.ChildlessItem = o.ParentItem
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := fixtures(stubManifest(tracker.CapNativeHierarchy))
			tc.break_(&opts)
			if _, err := Run(context.Background(), newStub(), opts); err == nil {
				t.Fatal("an incoherent fixture set was accepted; the run would report a property it cannot observe")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// One violating stub per check
// ---------------------------------------------------------------------------

// 1. manifest/valid

func TestConform_CatchesACapabilityOutsideTheVocabulary(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{"native-hierarchy"} // the vocabulary says native_hierarchy
	rep := run(t, s, stubManifest("native-hierarchy"))
	requireFailed(t, rep, CheckManifest)
}

func TestConform_CatchesAManifestForAnotherClass(t *testing.T) {
	m := stubManifest(tracker.CapNativeHierarchy)
	m.Implements = connector.ClassRoster
	rep := run(t, newStub(), m)
	requireFailed(t, rep, CheckManifest)
}

func TestConform_CatchesAManifestTheSDKRejects(t *testing.T) {
	m := stubManifest(tracker.CapNativeHierarchy)
	m.Name = "" // the SDK requires it, and the class substitution must still reach that rule
	rep := run(t, newStub(), m)
	requireFailed(t, rep, CheckManifest)
}

// 2. class/implements

func TestConform_CatchesTheWrongClass(t *testing.T) {
	s := newStub()
	s.class = connector.ClassRoster
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckClass)
}

// 3. capability/manifest-matches-runtime

func TestConform_CatchesADeclarationTheRuntimeDoesNotKeep(t *testing.T) {
	rep := run(t, newStub(), stubManifest(tracker.CapNativeHierarchy, tracker.CapCrossScope))
	requireFailed(t, rep, CheckCapabilityHonesty)
}

func TestConform_CatchesARuntimeCapabilityTheManifestDoesNotDeclare(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{tracker.CapNativeHierarchy, tracker.CapCrossScope}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckCapabilityHonesty)
}

// TestConform_CatchesACapabilitySetThatDisagreesInBothDirections is the third
// branch of that check, and the one a renamed capability actually produces: the
// manifest declares one the runtime does not report AND the runtime reports one
// the manifest does not declare. Its message must name both halves, because an
// implementer told only about the missing one goes looking for a lost
// declaration instead of a typo.
func TestConform_CatchesACapabilitySetThatDisagreesInBothDirections(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{tracker.CapNativeHierarchy, tracker.CapItemFields}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy, tracker.CapCrossScope))
	requireFailed(t, rep, CheckCapabilityHonesty)
	detail, _ := failed(rep, CheckCapabilityHonesty)
	for _, want := range []connector.Capability{tracker.CapCrossScope, tracker.CapItemFields} {
		if !strings.Contains(detail, string(want)) {
			t.Errorf("the failure does not name %s, so it reports half of a two-sided disagreement: %q", want, detail)
		}
	}
}

// 4. optional/declared-is-implemented

func TestConform_CatchesAnOptionalOperationDeclaredButAbsent(t *testing.T) {
	for _, cap := range []connector.Capability{tracker.CapTrains, tracker.CapSchemaAdmin} {
		t.Run(string(cap), func(t *testing.T) {
			s := newStub()
			s.caps = connector.Capabilities{tracker.CapNativeHierarchy, cap}
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy, cap))
			requireFailed(t, rep, CheckOptionalDeclared)
			detail, _ := failed(rep, CheckOptionalDeclared)
			if !strings.Contains(detail, string(cap)) {
				t.Errorf("the failure does not name the capability: %q", detail)
			}
		})
	}
}

func TestConform_CatchesAnOptionalOperationImplementedButUndeclared(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    tracker.Tracker
	}{
		{"trains", &trainAdminStub{stub: newStub(), created: map[tracker.Scope][]tracker.Train{}}},
		{"schema", schemaAdminStub{newStub()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := run(t, tc.c, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckOptionalDeclared)
		})
	}
}

func TestConform_AcceptsAnOptionalOperationDeclaredAndImplemented(t *testing.T) {
	requireGreen(t, run(t, newTrainAdmin(false), trainManifest(false)))
}

// 5. list/no-empty-success

// TestConform_CatchesAnEmptySuccess drives the two shapes of nothing — an
// UNRESOLVED resolution and a deliberately empty one — through every read check
// that can see them, and pins the words each one reports.
//
// The distinction is the whole point of [tracker.Resolution] and the checks must not
// blur it: "reported success with a resolution that carries no list" is a
// connector fault, and "resolved to no items" is a board that says it is empty.
// Every read check has its own copy of that pair of assertions, and each copy
// needs its own stub.
func TestConform_CatchesAnEmptySuccess(t *testing.T) {
	for _, tc := range []struct {
		name       string
		on         func(*stub)
		wantFailed map[string]string
	}{
		{"Scan resolves nothing at all", func(s *stub) {
			s.scan = func(context.Context, tracker.BoardRef, tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				return tracker.Resolution[tracker.Item]{}, nil
			}
		}, map[string]string{
			CheckListNoEmptySuccess:       "Scan(stubtracker/stub-org/1) reported success with a resolution that carries no list",
			CheckScanPagedToExhaustion:    "under the cheapest Selection reported success with a resolution that carries no list",
			CheckReadUnreadableIsNotEmpty: "Scan with Selection.Children reported success with a resolution that carries no list",
			CheckSelectionEchoed:          "Scan(stubtracker/stub-org/1) under the cheapest Selection reported success with a resolution that carries no list",
		}},
		{"Scan asserts an empty board that the fixtures say has items", func(s *stub) {
			s.scan = func(context.Context, tracker.BoardRef, tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				return tracker.EmptyList[tracker.Item](), nil
			}
		}, map[string]string{
			CheckListNoEmptySuccess:    "resolved to no items",
			CheckScanPagedToExhaustion: "the scan resolved 0 item(s) and does not include",
			// The parent fixture's absence from the scan, which nothing else in
			// that check can see.
			CheckReadUnreadableIsNotEmpty: "the scan did not report stubtracker/stub-org/1:alpha#102",
		}},
		{"GetItems resolves nothing at all", func(s *stub) {
			s.getItems = func(context.Context, tracker.BoardRef, []tracker.ItemRef, tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				return tracker.Resolution[tracker.Item]{}, nil
			}
		}, map[string]string{
			CheckListNoEmptySuccess:       "GetItems(stubtracker/stub-org/1:alpha#101) reported success with a resolution that carries no list",
			CheckReadUnreadableIsNotEmpty: "under the cheapest Selection reported success with a resolution that carries no list",
			CheckSelectionEchoed:          "GetItems under the cheapest Selection reported success with a resolution that carries no list",
		}},
		{"GetItems asserts an empty answer for an item the fixtures say exists", func(s *stub) {
			s.getItems = func(context.Context, tracker.BoardRef, []tracker.ItemRef, tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				return tracker.EmptyList[tracker.Item](), nil
			}
		}, map[string]string{
			CheckListNoEmptySuccess: "resolved 0 item(s) and none of them is the one it was asked about",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			tc.on(s)
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckListNoEmptySuccess)
			for name, want := range tc.wantFailed {
				requireFailed(t, rep, name)
				if detail, _ := failed(rep, name); !strings.Contains(detail, want) {
					t.Errorf("%s: want a detail containing %q, got %q", name, want, detail)
				}
			}
		})
	}
}

// TestConform_CatchesAReadThatFailsOnWhatItMustRead is the falsifier for every
// "failed on a board/item the fixtures say it must read" assertion in the
// harness — the branches that fire when the READ ITSELF fails rather than
// returning a wrong shape.
//
// Each case names the checks it must reach, because a read failure is not
// confined to one check and a stub whose blast radius is unstated is one nobody
// has looked at. Each also pins the WORDS of the assertion it drives: a
// neighbouring branch would fail the same check about the wrong thing — "reported
// success with a resolution that carries no list" about a connector that did not
// report success at all — so the verdict alone does not show that this assertion
// is what caught it.
func TestConform_CatchesAReadThatFailsOnWhatItMustRead(t *testing.T) {
	for _, tc := range []struct {
		name       string
		on         func(*stub)
		wantFailed map[string]string
		wantPassed []string
	}{
		{"Scan fails on the board it must scan", func(s *stub) {
			s.scan = func(_ context.Context, board tracker.BoardRef, _ tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindUnauthorized, "Scan",
					fmt.Errorf("the token cannot read %s", board))
			}
		}, map[string]string{
			CheckListNoEmptySuccess:       "failed on a board the fixtures say it must scan",
			CheckScanPagedToExhaustion:    "under the cheapest Selection failed on a board the fixtures say it must scan",
			CheckReadUnreadableIsNotEmpty: "with Selection.Children failed on a board the fixtures say it must scan",
			CheckSelectionEchoed:          "under the cheapest Selection failed on a board the fixtures say it must scan",
		}, []string{CheckListFailClosed}},

		{"Scan fails only under a non-zero Selection", func(s *stub) {
			s.scan = func(ctx context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				if sel != (tracker.Selection{}) {
					// The read that costs a request per item is the one that
					// times out, and the cheap listing is untouched.
					return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindUnavailable, "Scan",
						errors.New("timed out enriching items"))
				}
				return (&stub{caps: s.caps}).Scan(ctx, board, sel)
			}
		}, map[string]string{
			CheckScanPagedToExhaustion:    "under the fullest Selection failed on a board the fixtures say it must scan",
			CheckReadUnreadableIsNotEmpty: "with Selection.Children failed on a board the fixtures say it must scan",
			CheckSelectionEchoed:          "under the fullest Selection failed on a board the fixtures say it must scan",
		}, []string{CheckListNoEmptySuccess, CheckListFailClosed}},

		{"GetItems fails on an item it must read", func(s *stub) {
			s.getItems = func(ctx context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				// The not-found path is left conformant so this stub is aimed at
				// the items the fixtures say the connector MUST read.
				if slices.Contains(refs, fixMissing) {
					return (&stub{caps: s.caps}).GetItems(ctx, board, refs, sel)
				}
				return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindUnavailable, "GetItems",
					errors.New("could not reach the API"))
			}
		}, map[string]string{
			CheckListNoEmptySuccess:       "failed on an item the fixtures say it must read",
			CheckReadUnreadableIsNotEmpty: "under the cheapest Selection failed",
			CheckSelectionEchoed:          "failed on items the fixtures say it must read",
		}, []string{CheckListFailClosed}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			tc.on(s)
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			for name, want := range tc.wantFailed {
				requireFailed(t, rep, name)
				if detail, _ := failed(rep, name); !strings.Contains(detail, want) {
					t.Errorf("%s failed on something other than the read: want a detail containing %q, got %q",
						name, want, detail)
				}
			}
			requirePassed(t, rep, tc.wantPassed...)
		})
	}
}

func TestConform_CatchesAReadThatOmitsTheItemItWasAsked(t *testing.T) {
	s := newStub()
	s.getItems = func(_ context.Context, _ tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		// Silently drops the requested item and answers about another one: the
		// caller cannot learn which question went unanswered.
		return tracker.Resolved([]tracker.Item{{Ref: fixChild, Selected: sel}}, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListNoEmptySuccess)
}

// 6. list/failure-is-typed-and-fail-closed

func TestConform_CatchesAnUnclassifiedFailure(t *testing.T) {
	s := newStub()
	s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board == fixScannable {
			return tracker.Resolved(stubItems(sel, s.caps), tracker.Complete)
		}
		return tracker.Resolution[tracker.Item]{}, errors.New("404 Not Found []") // vendor prose
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListFailClosed)
}

func TestConform_CatchesAReadableResolutionBesideAFailure(t *testing.T) {
	s := newStub()
	s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board == fixScannable {
			return tracker.Resolved(stubItems(sel, s.caps), tracker.Complete)
		}
		return tracker.EmptyList[tracker.Item](), cerr.New(cerr.KindUnauthorized, "Scan", nil)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListFailClosed)
}

func TestConform_CatchesAConnectorThatNeverFailsToScan(t *testing.T) {
	s := newStub()
	s.scan = func(_ context.Context, _ tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		return tracker.Resolved(stubItems(sel, s.caps), tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListFailClosed)
}

// TestConform_CatchesAnUnreadableSuccessOnABoardItCannotRead is the one shape
// only the must-fail-at-all half catches: no error, and a resolution nothing
// can read out of. Every other assertion in the check is satisfied — there is
// no unclassified error, and the resolution is properly unreadable — so a
// connector that answers this way slips past all of them.
//
// It matters because it is what a swallowed error looks like on a failure path:
// the caller gets no error to route on, and Resolution.Items is the only thing
// standing between it and reading a permission failure as an empty board.
func TestConform_CatchesAnUnreadableSuccessOnABoardItCannotRead(t *testing.T) {
	s := newStub()
	s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board == fixScannable {
			return tracker.Resolved(stubItems(sel, s.caps), tracker.Complete)
		}
		return tracker.Resolution[tracker.Item]{}, nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListFailClosed)
}

// The same shape on the item read.
func TestConform_CatchesAnUnreadableSuccessForAnItemThatDoesNotExist(t *testing.T) {
	s := newStub()
	s.getItems = func(_ context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		all := stubItems(sel, s.caps)
		out := make([]tracker.Item, 0, len(refs))
		for _, r := range refs {
			idx := slices.IndexFunc(all, func(it tracker.Item) bool { return it.Ref == r })
			if idx < 0 {
				return tracker.Resolution[tracker.Item]{}, nil
			}
			out = append(out, all[idx])
		}
		return tracker.Resolved(out, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListFailClosed)
}

func TestConform_CatchesAMissingItemReadAsPresent(t *testing.T) {
	s := newStub()
	s.getItems = func(_ context.Context, _ tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		out := make([]tracker.Item, 0, len(refs))
		for _, r := range refs {
			out = append(out, tracker.Item{Ref: r, Selected: sel})
		}
		return tracker.Resolved(out, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListFailClosed)
}

// TestConform_CatchesANotFoundReportedAsSomethingElse: a host routes on the
// classification, and "unavailable" is retryable while an item that does not
// exist will not appear on a retry.
func TestConform_CatchesANotFoundReportedAsSomethingElse(t *testing.T) {
	s := newStub()
	s.getItems = func(_ context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		all := stubItems(sel, s.caps)
		out := make([]tracker.Item, 0, len(refs))
		for _, r := range refs {
			idx := slices.IndexFunc(all, func(it tracker.Item) bool { return it.Ref == r })
			if idx < 0 {
				return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindUnavailable, "GetItems", errors.New("could not reach the API"))
			}
			out = append(out, all[idx])
		}
		return tracker.Resolved(out, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListFailClosed)
}

// TestConform_CatchesAnUnclassifiedNotFound is the item half of the
// unclassified-failure assertion: the Scan half has its own stub above, and this
// branch is the one that tells an implementer their vendor error is coming back
// raw rather than that their kind MAPPING is wrong.
func TestConform_CatchesAnUnclassifiedNotFound(t *testing.T) {
	s := newStub()
	s.getItems = func(ctx context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if slices.Contains(refs, fixMissing) {
			return tracker.Resolution[tracker.Item]{}, errors.New("404 Not Found []") // vendor prose
		}
		return (&stub{caps: s.caps}).GetItems(ctx, board, refs, sel)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListFailClosed)
	// The words matter here: the kind assertion below this branch would fail the
	// same check by reporting the kind as "unknown", which sends an implementer
	// after a MAPPING they have not written rather than after an error they are
	// returning raw.
	if detail, _ := failed(rep, CheckListFailClosed); !strings.Contains(detail, "unclassified error") {
		t.Errorf("the failure does not say the error carries no classification at all: %q", detail)
	}
	requirePassed(t, rep, CheckListNoEmptySuccess, CheckSelectionEchoed, CheckReadUnreadableIsNotEmpty)
}

// TestConform_CatchesAReadableResolutionBesideANotFound is the item half of the
// fail-closed assertion. The classification is right and the resolution is still
// readable, so a caller that ignored the error reads "this item does not exist"
// as an answer carrying no items — which is the same conflation on the item path
// that EmptyList exists to keep off the board path.
func TestConform_CatchesAReadableResolutionBesideANotFound(t *testing.T) {
	s := newStub()
	s.getItems = func(ctx context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if slices.Contains(refs, fixMissing) {
			return tracker.EmptyList[tracker.Item](), cerr.New(cerr.KindNotFound, "GetItems", fmt.Errorf("no such item %s", fixMissing))
		}
		return (&stub{caps: s.caps}).GetItems(ctx, board, refs, sel)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckListFailClosed)
	requirePassed(t, rep, CheckListNoEmptySuccess, CheckSelectionEchoed, CheckReadUnreadableIsNotEmpty)
}

// 7. health/answers

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
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckHealth)
		})
	}
}

func TestConform_AcceptsAClassifiedHealthFailure(t *testing.T) {
	s := newStub()
	s.health = func(context.Context) (connector.Health, error) {
		return connector.Health{}, cerr.New(cerr.KindUnavailable, "Health", nil)
	}
	requireGreen(t, run(t, s, stubManifest(tracker.CapNativeHierarchy)))
}

// 8. scan/paged-to-exhaustion

func TestConform_CatchesAScanThatTruncatesOnTheExpensivePath(t *testing.T) {
	s := newStub()
	s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board != fixScannable {
			return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", nil)
		}
		all := stubItems(sel, s.caps)
		if sel.Children {
			// The read that costs a request per item stops after the first
			// page, and reports what it got as the whole board.
			all = all[:1]
		}
		return tracker.Resolved(all, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckScanPagedToExhaustion)
	detail, _ := failed(rep, CheckScanPagedToExhaustion)
	if !strings.Contains(detail, "cheapest only") {
		t.Errorf("the failure does not say which read is short, so an implementer cannot tell which one to fix: %q",
			detail)
	}
}

func TestConform_CatchesAScanThatStopsBeforeAnItemItMustReport(t *testing.T) {
	s := newStub()
	s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board != fixScannable {
			return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", nil)
		}
		all := stubItems(sel, s.caps)
		// Drops the childless item, which the fixtures place on the board.
		out := slices.DeleteFunc(all, func(it tracker.Item) bool { return it.Ref == fixChildless })
		return tracker.Resolved(out, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckScanPagedToExhaustion)
	// read/unreadable-is-not-empty cannot compare an empty child set against an
	// unreadable one when the empty half is missing, and says so in its own
	// words: this is the only stub that drives that half of the pair.
	requireFailed(t, rep, CheckReadUnreadableIsNotEmpty)
	if detail, _ := failed(rep, CheckReadUnreadableIsNotEmpty); !strings.Contains(detail,
		"the scan did not report "+fixChildless.String()) {
		t.Errorf("the failure does not name the missing childless fixture: %q", detail)
	}
}

// TestConform_CatchesAScanThatTruncatesOnTheCHEAPPath is the other direction of
// the same comparison, and it is not symmetrical decoration: the message has to
// name which read is short, and a connector whose cheap listing endpoint pages
// differently from its enriched one is the ordinary way this happens.
func TestConform_CatchesAScanThatTruncatesOnTheCheapPath(t *testing.T) {
	s := newStub()
	s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board != fixScannable {
			return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", nil)
		}
		all := stubItems(sel, s.caps)
		if sel == (tracker.Selection{}) {
			all = slices.DeleteFunc(all, func(it tracker.Item) bool { return it.Ref == fixChild })
		}
		return tracker.Resolved(all, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckScanPagedToExhaustion)
	detail, _ := failed(rep, CheckScanPagedToExhaustion)
	if !strings.Contains(detail, "fullest only") {
		t.Errorf("the failure does not say which read is short, so an implementer cannot tell which one to fix: %q",
			detail)
	}
}

// 9. read/unreadable-is-not-empty

func TestConform_CatchesASwallowedChildSetFailure(t *testing.T) {
	s := newStub()
	s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board != fixScannable {
			return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", nil)
		}
		items := stubItems(sel, s.caps)
		if sel.Children {
			for i := range items {
				// The child read failed and the failure was swallowed: an empty
				// child set with no error, which a rollup reads as a parent
				// whose children are all done.
				items[i].Children = []tracker.ItemRef{}
				items[i].ChildrenErr = nil
			}
		}
		return tracker.Resolved(items, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckReadUnreadableIsNotEmpty)
}

// TestConform_CatchesAnUnknownChildSetHandedOverAsAList breaks the invariant on
// an item that is NOT one of the two child-set fixtures. That is deliberate: it
// is what makes the board-wide loop the only thing that can catch it, so the
// invariant is proven to bite on its own rather than through the per-fixture
// assertions below it.
func TestConform_CatchesAnUnknownChildSetHandedOverAsAList(t *testing.T) {
	s := newStub()
	s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board != fixScannable {
			return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", nil)
		}
		items := stubItems(sel, s.caps)
		if sel.Children {
			for i := range items {
				if items[i].Ref == fixChild {
					items[i].Children = []tracker.ItemRef{fixKnown}
					items[i].ChildrenErr = cerr.New(cerr.KindUnavailable, "Scan", nil)
				}
			}
		}
		return tracker.Resolved(items, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckReadUnreadableIsNotEmpty)
}

func TestConform_CatchesAChildSetNobodyAskedFor(t *testing.T) {
	s := newStub()
	s.getItems = func(_ context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		all := stubItems(tracker.Selection{Children: true}, s.caps)
		out := make([]tracker.Item, 0, len(refs))
		for _, r := range refs {
			idx := slices.IndexFunc(all, func(it tracker.Item) bool { return it.Ref == r })
			if idx < 0 {
				return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "GetItems", nil)
			}
			it := all[idx]
			it.Selected = sel // claims the cheap read, carries the expensive payload
			out = append(out, it)
		}
		return tracker.Resolved(out, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckReadUnreadableIsNotEmpty)
}

func TestConform_CatchesAFieldThatIsBothUnreadAndPresent(t *testing.T) {
	s := newStub()
	s.getItems = func(_ context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		all := stubItems(sel, s.caps)
		out := make([]tracker.Item, 0, len(refs))
		for _, r := range refs {
			idx := slices.IndexFunc(all, func(it tracker.Item) bool { return it.Ref == r })
			if idx < 0 {
				return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "GetItems", nil)
			}
			it := all[idx]
			it.Fields = map[string]tracker.Value{stubField: {Kind: tracker.ValueOption, Option: "Backlog"}}
			it.Unread = []string{stubField} // unknown AND a value to read
			out = append(out, it)
		}
		return tracker.Resolved(out, tracker.Complete)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckReadUnreadableIsNotEmpty)
}

// TestConform_CatchesEachChildSetFixtureReportedWrongly is one violating stub
// per remaining assertion in this check: the childless item's child-set read
// failing, the childless item carrying children, and the parent's child-set read
// failing. Each is a state the check names in its own words, and none of them is
// caught by any of the others.
func TestConform_CatchesEachChildSetFixtureReportedWrongly(t *testing.T) {
	for _, tc := range []struct {
		name string
		fix  func(t *testing.T, sel tracker.Selection, items []tracker.Item)
		// wantDetail pins the assertion, not just the verdict: the capability
		// branch below these three would fail the same check about a swallowed
		// failure, which is a different mistake in the opposite direction.
		wantDetail string
	}{
		{"the childless item's child-set read failed", func(t *testing.T, sel tracker.Selection, items []tracker.Item) {
			it := itemAt(t, items, fixChildless)
			// Children goes nil with it, which is the CORRECT shape for an
			// unreadable child set — so the whole-board invariant cannot fire and
			// only the childless-item assertion is left to catch this.
			it.Children = nil
			it.ChildrenErr = cerr.New(cerr.KindUnavailable, "Scan", errors.New("sub-issue read timed out"))
		}, "has no children and its child-set read reported a failure"},
		{"the childless item reports children", func(t *testing.T, sel tracker.Selection, items []tracker.Item) {
			itemAt(t, items, fixChildless).Children = []tracker.ItemRef{fixChild}
		}, "the fixtures name it as having none"},
		{"the parent item's child-set read failed", func(t *testing.T, sel tracker.Selection, items []tracker.Item) {
			it := itemAt(t, items, fixParent)
			it.Children = nil
			it.ChildrenErr = cerr.New(cerr.KindUnavailable, "Scan", errors.New("sub-issue read timed out"))
		}, "an item the fixtures say the connector can read the children of, and its child-set read failed"},
		{"an item reports a field as unread and carries its value", func(t *testing.T, sel tracker.Selection, items []tracker.Item) {
			// On fixChild, which is neither child-set fixture: the whole-board
			// loop is the only thing that can see it.
			it := itemAt(t, items, fixChild)
			it.Fields = map[string]tracker.Value{stubField: {Kind: tracker.ValueOption, Option: "Backlog"}}
			it.Unread = []string{stubField}
		}, "names \"Status\" in Unread and also carries a value for it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			s.scan = scanMangling(s, func(sel tracker.Selection, items []tracker.Item) {
				if sel.Children {
					tc.fix(t, sel, items)
				}
			})
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckReadUnreadableIsNotEmpty)
			if detail, _ := failed(rep, CheckReadUnreadableIsNotEmpty); !strings.Contains(detail, tc.wantDetail) {
				t.Errorf("want a detail containing %q, got %q", tc.wantDetail, detail)
			}
		})
	}
}

// TestConform_AcceptsAConnectorWithoutTraversableHierarchy is the other branch
// of the same check: without CapNativeHierarchy a child set is not merely
// empty, it is unavailable, and reporting no children for a parent is the
// correct answer rather than a swallowed failure.
func TestConform_AcceptsAConnectorWithoutTraversableHierarchy(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{}
	rep := run(t, s, stubManifest())
	requireGreen(t, rep)
	// The PASS detail has to say which of the two obligations was observed. A
	// report claiming the parent "reports its children" about a connector that
	// cannot traverse them is a green line that means the opposite of what it
	// says, and this is the only test that reads it.
	detail, _ := failed(rep, CheckReadUnreadableIsNotEmpty)
	if !strings.Contains(detail, "reports no traversable children") {
		t.Errorf("the pass detail does not say the child set is unavailable rather than read: %q", detail)
	}
}

// TestConform_CatchesChildrenClaimedWithoutTheCapability is that branch's own
// falsifier: children a host cannot traverse back are worse than none.
// It hands children to ParentItem ALONE, so no other assertion in the check can
// fire: the childless item is still empty and no item reports a child-set
// error. Only the capability branch is left to catch it.
func TestConform_CatchesChildrenClaimedWithoutTheCapability(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{}
	s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		if board != fixScannable {
			return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", nil)
		}
		items := stubItems(sel, s.caps)
		if sel.Children {
			for i := range items {
				if items[i].Ref == fixParent {
					items[i].Children = []tracker.ItemRef{fixChild}
				}
			}
		}
		return tracker.Resolved(items, tracker.Complete)
	}
	rep := run(t, s, stubManifest())
	requireFailed(t, rep, CheckReadUnreadableIsNotEmpty)
}

// 10. read/selection-echoed

func TestConform_CatchesAnItemThatDoesNotCarryItsSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mangle func(tracker.Selection) tracker.Selection
	}{
		{"never populated", func(tracker.Selection) tracker.Selection { return tracker.Selection{} }},
		{"populated with something else", func(tracker.Selection) tracker.Selection { return tracker.Selection{Labels: true} }},
		{"claims more than was asked", func(sel tracker.Selection) tracker.Selection { return fullSelection }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			s.getItems = func(_ context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				all := stubItems(sel, s.caps)
				out := make([]tracker.Item, 0, len(refs))
				for _, r := range refs {
					idx := slices.IndexFunc(all, func(it tracker.Item) bool { return it.Ref == r })
					if idx < 0 {
						return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "GetItems", nil)
					}
					it := all[idx]
					it.Selected = tc.mangle(sel)
					out = append(out, it)
				}
				return tracker.Resolved(out, tracker.Complete)
			}
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckSelectionEchoed)
		})
	}
}

func TestConform_CatchesAScanThatDoesNotCarryItsSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mangle func(tracker.Selection) tracker.Selection
	}{
		// "claims more than was asked" is caught by the zero-Selection probe:
		// the item claims the full Selection under a read that asked for
		// nothing.
		{"claims more than was asked", func(tracker.Selection) tracker.Selection { return fullSelection }},
		// "never populated" is the one that needs the NON-ZERO probe, and it is
		// the defect this check exists to catch. Under the zero Selection the
		// echo is correct by accident — Selected's zero value IS the answer — so
		// a check that only ever scanned cheaply would report this connector
		// green while every item it returns from an expensive read claims to
		// have been read cheaply.
		{"never populated", func(tracker.Selection) tracker.Selection { return tracker.Selection{} }},
		{"populated with something else", func(tracker.Selection) tracker.Selection { return tracker.Selection{Labels: true} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			s.scan = scanMangling(s, func(sel tracker.Selection, items []tracker.Item) {
				for i := range items {
					items[i].Selected = tc.mangle(sel)
				}
			})
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckSelectionEchoed)
			detail, _ := failed(rep, CheckSelectionEchoed)
			if !strings.HasPrefix(detail, "Scan ") {
				t.Errorf("the failure is not attributed to Scan, which is the operation this stub breaks: %q", detail)
			}
		})
	}
}

// TestSelectionEchoedCheck_DrivesScanUnderANonZeroSelection pins the probe
// matrix itself. The "never populated" case above only fails because a Scan is
// driven under a non-zero Selection, so a future edit that dropped that probe
// would leave that case green — and this asserts the driver directly rather than
// through a connector, so it says which probe went missing.
func TestSelectionEchoedCheck_DrivesScanUnderANonZeroSelection(t *testing.T) {
	type probe struct {
		op  string
		sel tracker.Selection
	}
	var seen []probe
	s := newStub()
	s.scan = func(ctx context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		seen = append(seen, probe{"Scan", sel})
		return (&stub{caps: s.caps}).Scan(ctx, board, sel)
	}
	s.getItems = func(ctx context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		seen = append(seen, probe{"GetItems", sel})
		return (&stub{caps: s.caps}).GetItems(ctx, board, refs, sel)
	}
	if pass, detail := checkSelectionEchoed(context.Background(), s, fixtures(stubManifest(tracker.CapNativeHierarchy))); !pass {
		t.Fatalf("the check failed against a compliant connector: %s", detail)
	}
	for _, want := range []probe{
		{"Scan", tracker.Selection{}}, {"Scan", fullSelection},
		{"GetItems", tracker.Selection{}}, {"GetItems", fullSelection},
	} {
		if !slices.Contains(seen, want) {
			t.Errorf("%s never drove %s under Selection%+v; the echo is checked against the one case that connector "+
				"gets right by accident", CheckSelectionEchoed, want.op, want.sel)
		}
	}
}

// TestAttributionSet_CannotMutateALiveItem is the harness's own safety claim,
// enforced rather than asserted in prose. [Run] says a run is safe to repeat
// against a live board for a CONFORMANT connector, and bounds what a
// non-conformant one can do: so no change in the attribution set may be one that
// a connector ignoring its own refusals could land on a live item.
//
// The close is what that is about. A close cannot be undone by re-opening — the
// closed-at timestamp, the notifications and every rollup that ran in between
// are already gone — so it is aimed at an address no item can have, and this is
// what stops it drifting back onto KnownItem.
func TestAttributionSet_CannotMutateALiveItem(t *testing.T) {
	opts := fixtures(stubManifest())
	if closeProbeTarget(opts).Valid() {
		t.Errorf("closeProbeTarget is a valid address (%s), so a connector that ignored the missing close reason "+
			"would close a real item", closeProbeTarget(opts))
	}
	set := attributionSet(opts)
	if len(set) == 0 {
		t.Fatal("the attribution set is empty, so the check it drives observes nothing")
	}
	for i, ch := range set {
		// A change whose target is not a valid address on the board under test
		// cannot land on an item of it whatever the connector does.
		if !ch.Target.Valid() || ch.Target.Board != opts.ScannableBoard {
			continue
		}
		if ch.Lifecycle != nil {
			t.Errorf("change %d closes or reopens %s, a live item on the board under test; a connector that ignored "+
				"the refusal would carry it out", i, ch.Target)
		}
		for name, v := range ch.Fields {
			if v.Kind.Writable() {
				t.Errorf("change %d writes field %q on live item %s with a writable value, so a connector that "+
					"ignored the refusal would land it", i, name, ch.Target)
			}
		}
	}
}

// 11. apply/attribution

func TestConform_CatchesApplyThatClaimsARefusedChangeSetWasApplied(t *testing.T) {
	s := newStub()
	s.apply = func(_ context.Context, _ tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		return tracker.ApplyReport{Requested: len(changes), Applied: len(changes)}, nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyAttribution)
	if detail, _ := failed(rep, CheckApplyAttribution); !strings.Contains(detail,
		"listed neither as applied nor as failed") {
		t.Errorf("the failure does not say the change went unattributed, which is the fault: %q", detail)
	}
}

func TestConform_CatchesApplyWhoseArithmeticDoesNotClose(t *testing.T) {
	for _, tc := range []struct {
		name string
		// wantDetail is asserted where a NEIGHBOURING branch would fail the same
		// check about something else, so the case pins its own assertion rather
		// than the verdict they share.
		wantDetail string
		apply      func(context.Context, tracker.BoardRef, []tracker.Change) (tracker.ApplyReport, error)
	}{
		{"a demand of its own invention", "does not account for the change set", func(_ context.Context, _ tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
			return tracker.ApplyReport{Requested: 0, Applied: 0}, nil
		}},
		{"a failure attributed to no change", "does not account for the change set", func(_ context.Context, _ tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
			return tracker.ApplyReport{
				Requested: len(changes),
				Failed:    map[int]error{99: cerr.New(cerr.KindInvalid, "Apply", nil)},
			}, nil
		}},
		{"a failure with no reason", "does not account for the change set", func(_ context.Context, _ tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
			failed := map[int]error{}
			for i := range changes {
				failed[i] = nil
			}
			return tracker.ApplyReport{Requested: len(changes), Failed: failed}, nil
		}},
		{"a change left unaccounted for", "does not account for the change set", func(_ context.Context, _ tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
			return tracker.ApplyReport{
				Requested: len(changes),
				Failed:    map[int]error{0: cerr.New(cerr.KindInvalid, "Apply", nil)},
			}, nil
		}},
		{"an unclassified per-change failure", "attributed an unclassified failure", func(_ context.Context, _ tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
			failed := map[int]error{}
			for i := range changes {
				failed[i] = errors.New("422 Unprocessable Entity")
			}
			return tracker.ApplyReport{Requested: len(changes), Failed: failed}, nil
		}},
		{"an attributable refusal folded into a whole-call error", "as a whole (invalid", func(context.Context, tracker.BoardRef, []tracker.Change) (tracker.ApplyReport, error) {
			return tracker.ApplyReport{}, cerr.New(cerr.KindInvalid, "Apply", errors.New("one of your changes is invalid"))
		}},
		// The same fold with no classification either: two faults at once, and
		// the report must name the unclassified one first, because a host can
		// act on "attributed nothing" and cannot act on prose.
		{"a whole-call error that is not even classified", "with an unclassified error", func(context.Context, tracker.BoardRef, []tracker.Change) (tracker.ApplyReport, error) {
			return tracker.ApplyReport{}, errors.New("422 Unprocessable Entity")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			s.apply = tc.apply
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckApplyAttribution)
			if detail, _ := failed(rep, CheckApplyAttribution); !strings.Contains(detail, tc.wantDetail) {
				t.Errorf("the failure is not this case's own: want a detail containing %q, got %q", tc.wantDetail, detail)
			}
		})
	}
}

// 12. apply/cross-tracker-refused

func TestConform_CatchesACrossTrackerParentThatIsAccepted(t *testing.T) {
	s := newStub()
	s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		// The parent rule is dropped and every other pre-network rule kept: a
		// link to another tracker is written, and nothing else about this
		// connector is wrong.
		return stubApplyReport(board, changes, stubTargetRefusal), nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyCrossTrackerRefused)
	if detail, _ := failed(rep, CheckApplyCrossTrackerRefused); !strings.Contains(detail,
		"listed neither as applied nor as failed") {
		t.Errorf("the failure does not say the foreign parent was applied: %q", detail)
	}
	// This is also the violating stub for the repeat-refusal probe's
	// nothing-to-compare branch: apply/no-cached-identity compares two
	// classifications, and a connector that refused nothing produced neither.
	// It must say so rather than pass on two answers it never obtained.
	requireFailed(t, rep, CheckApplyNoCachedIdentity)
	// "the FIRST of two" is asserted, not just "refused nothing": the probe makes
	// the same complaint about either call, so the wording is the only thing that
	// shows which one it examined — and both are examined.
	if detail, _ := failed(rep, CheckApplyNoCachedIdentity); !strings.Contains(detail,
		"the first of two Applies") || !strings.Contains(detail, "refused nothing") {
		t.Errorf("the repeat-refusal probe does not say the FIRST call had nothing to compare: %q", detail)
	}
}

// TestConform_CatchesACrossTrackerParentComparedOnTheBoardNumberAlone is
// The cross-tracker failure mode itself: two boards on one provider and a tracker on another
// beside them all number items from 1, so a refusal that compares anything
// less than the fully-qualified address lets a foreign parent through.
func TestConform_CatchesACrossTrackerParentComparedOnTheBoardNumberAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		same func(tracker.BoardRef, tracker.BoardRef) bool
	}{
		{"compares the board component only", func(a, b tracker.BoardRef) bool { return a.Board == b.Board }},
		{"compares the instance and board only", func(a, b tracker.BoardRef) bool {
			return a.Instance == b.Instance && a.Board == b.Board
		}},
		{"compares the provider and board only", func(a, b tracker.BoardRef) bool {
			return a.Provider == b.Provider && a.Board == b.Board
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			// The parent comparison is narrowed to part of the address, and
			// everything else — including the attribution — stays conformant.
			refuse := func(board tracker.BoardRef, ch tracker.Change) error {
				if ch.Parent != nil && *ch.Parent != (tracker.ItemRef{}) && !tc.same(ch.Parent.Board, board) {
					return cerr.New(cerr.KindInvalid, "Apply", errors.New("foreign parent"))
				}
				return stubTargetRefusal(board, ch)
			}
			s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
				return stubApplyReport(board, changes, refuse), nil
			}
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckApplyCrossTrackerRefused)
		})
	}
}

func TestConform_CatchesACrossTrackerRefusalWithTheWrongClassification(t *testing.T) {
	s := newStub()
	refuse := func(board tracker.BoardRef, ch tracker.Change) error {
		if err := stubParentRefusal(board, ch); err != nil {
			// KindNotFound reads as "it may exist later" and is retryable under
			// some host policies; the link is never valid.
			return cerr.New(cerr.KindNotFound, "Apply", errors.New("no such parent"))
		}
		return stubTargetRefusal(board, ch)
	}
	s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		return stubApplyReport(board, changes, refuse), nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyCrossTrackerRefused)
}

// TestConform_CatchesACrossTrackerRefusalThatIsNotClassifiedAtAll is the other
// half of the same assertion: a vendor error returned raw, attributed to the
// right change and carrying no classification a host can route on.
func TestConform_CatchesACrossTrackerRefusalThatIsNotClassifiedAtAll(t *testing.T) {
	s := newStub()
	refuse := func(board tracker.BoardRef, ch tracker.Change) error {
		if err := stubParentRefusal(board, ch); err != nil {
			return errors.New("422 Unprocessable Entity: Could not resolve to a node") // vendor prose
		}
		return stubTargetRefusal(board, ch)
	}
	s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		return stubApplyReport(board, changes, refuse), nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyCrossTrackerRefused)
	// The repeat-refusal probe has no classification to compare either, and this
	// is its violating stub for that branch: the refusal exists, so
	// "refused nothing" is the wrong answer and "unclassified" is the right one.
	requireFailed(t, rep, CheckApplyNoCachedIdentity)
	if detail, _ := failed(rep, CheckApplyNoCachedIdentity); !strings.Contains(detail, "unclassified error") {
		t.Errorf("the repeat-refusal probe does not say the refusal carries no classification: %q", detail)
	}
}

// TestConform_CatchesACrossTrackerParentRefusedByAWholeCallError is the
// contradiction the two write checks used to enforce between them, now a single
// reading with a single falsifier: this connector refuses the foreign parent
// pre-network, with the required kind, and reports it where a caller of a
// fifty-change sweep cannot tell which change was wrong.
func TestConform_CatchesACrossTrackerParentRefusedByAWholeCallError(t *testing.T) {
	s := newStub()
	s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		for _, ch := range changes {
			if err := stubParentRefusal(board, ch); err != nil {
				return tracker.ApplyReport{}, err
			}
		}
		return stubApplyReport(board, changes, stubTargetRefusal), nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyCrossTrackerRefused)
	// And apply/attribution is NOT what reports it, because the attribution set
	// carries no parent: the two checks read one rule, and each still fails on
	// its own change set.
	if _, did := failed(rep, CheckApplyAttribution); did {
		t.Errorf("%s failed on a connector whose only fault is where it reports a foreign parent; the attribution "+
			"set carries no parent at all\n%s", CheckApplyAttribution, rep)
	}
}

// TestConform_CatchesACrossTrackerRefusalThatHappensAfterTheNetwork is the
// pre-network half. A connector that reaches for the wire before validating
// answers a canceled context with a transport failure, which tells the caller
// to retry a change set that can never be applied.
func TestConform_CatchesACrossTrackerRefusalThatHappensAfterTheNetwork(t *testing.T) {
	s := newStub()
	s.apply = func(ctx context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		// The wire is reached before anything is validated, so a context that is
		// already done answers a permanently-invalid change set with a transport
		// failure.
		if err := ctx.Err(); err != nil {
			return tracker.ApplyReport{}, cerr.New(cerr.KindUnavailable, "Apply", err)
		}
		return stubApplyReport(board, changes, stubRefusal), nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyCrossTrackerRefused)
	if detail, _ := failed(rep, CheckApplyCrossTrackerRefused); !strings.Contains(detail,
		"under a CANCELED context it was answered with unavailable") {
		t.Errorf("the failure does not report the answer the canceled context got: %q", detail)
	}
}

// TestConform_CatchesACrossTrackerRefusalThatEvaporatesUnderACanceledContext is
// the other shape the pre-network half catches: the refusal does not move to
// another classification, it disappears. A connector that treats a done context
// as nothing-to-do reports a change set it never looked at as applied.
func TestConform_CatchesACrossTrackerRefusalThatEvaporatesUnderACanceledContext(t *testing.T) {
	s := newStub()
	s.apply = func(ctx context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		if ctx.Err() != nil {
			return tracker.ApplyReport{Requested: len(changes), Applied: len(changes)}, nil
		}
		return stubApplyReport(board, changes, stubRefusal), nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyCrossTrackerRefused)
	if detail, _ := failed(rep, CheckApplyCrossTrackerRefused); !strings.Contains(detail,
		"refused neither in the report nor by a whole-call error") {
		t.Errorf("the failure does not say the refusal disappeared rather than moved: %q", detail)
	}
}

// TestConform_CatchesACrossTrackerRefusalThatStillClaimsAWrite: the refusal is
// attributed to the right change AND a write is claimed beside it. Applied == 0
// is not asserted on its own — the arithmetic [tracker.CheckApplyReport] closes is what
// catches this, which is why asserting it twice would be an assertion that
// cannot fail.
func TestConform_CatchesACrossTrackerRefusalThatStillClaimsAWrite(t *testing.T) {
	s := newStub()
	s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		rep := stubApplyReport(board, changes, stubRefusal)
		if len(rep.Failed) > 0 {
			rep.Applied++
		}
		return rep, nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyCrossTrackerRefused)
	requireFailed(t, rep, CheckApplyAttribution)
}

// 13. apply/no-cached-identity

func TestConform_CatchesACatalogueHandedOutByReference(t *testing.T) {
	s := newStub()
	stored := tracker.Catalogue{
		Board:     fixScannable,
		ScopeKind: "stub-partition",
		Fields:    []tracker.Field{{Name: stubField, Class: tracker.FieldClassBoard, Accepts: tracker.ValueOption, Options: []string{"Backlog"}}},
		Types:     []string{"Story"},
		Labels:    map[tracker.Scope][]string{fixKnown.Scope: {"needs-triage"}},
		Trains:    map[tracker.Scope][]tracker.Train{fixKnown.Scope: {{Name: "0.2.0", Open: true}}},
	}
	s.catalogue = func(_ context.Context, board tracker.BoardRef, _ []tracker.Scope) (tracker.Catalogue, error) {
		if board != fixScannable {
			return tracker.Catalogue{}, cerr.New(cerr.KindNotFound, "Catalogue", nil)
		}
		return stored, nil // the connector's own state, by reference
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyNoCachedIdentity)
}

// TestConform_CatchesTheFIRSTCatalogueGoingWrong breaks the FIRST read only,
// which is what makes these the first shape assertion's own stubs: the check
// reads the catalogue twice and asserts the shape of each, so a stub that broke
// both would be caught by whichever assertion happened to survive an edit.
func TestConform_CatchesTheFIRSTCatalogueGoingWrong(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fix        func(*tracker.Catalogue)
		wantDetail string
	}{
		{"it declares no ScopeKind", func(cat *tracker.Catalogue) { cat.ScopeKind = "" }, "declares no ScopeKind"},
		{"it is for another board", func(cat *tracker.Catalogue) { cat.Board = fixForeign }, "Catalogue reports it is for"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			calls := 0
			s.catalogue = func(_ context.Context, board tracker.BoardRef, scopes []tracker.Scope) (tracker.Catalogue, error) {
				calls++
				cat, err := (&stub{}).Catalogue(context.Background(), board, scopes)
				if err != nil || calls > 1 {
					return cat, err
				}
				tc.fix(&cat)
				return cat, nil
			}
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckApplyNoCachedIdentity)
			if detail, _ := failed(rep, CheckApplyNoCachedIdentity); !strings.Contains(detail, tc.wantDetail) {
				t.Errorf("want a detail containing %q, got %q", tc.wantDetail, detail)
			}
		})
	}
}

// TestConform_CatchesACatalogueSharingONEOfItsThreeNamedSets is one violating
// stub per probe the check writes and per leak it looks for. A connector that
// caches its field list and rebuilds the rest is the ordinary shape of this bug,
// and a harness that probed only one of the three would miss the other two — so
// each of the three is driven alone, and the failure must name the one that
// leaked.
func TestConform_CatchesACatalogueSharingONEOfItsThreeNamedSets(t *testing.T) {
	for _, tc := range []struct {
		name       string
		share      func(held, fresh *tracker.Catalogue)
		wantDetail string
	}{
		{"its field list", func(held, fresh *tracker.Catalogue) { fresh.Fields = held.Fields },
			"a field named by the probe"},
		{"its label map", func(held, fresh *tracker.Catalogue) { fresh.Labels = held.Labels },
			"a label named by the probe"},
		{"its train map", func(held, fresh *tracker.Catalogue) { fresh.Trains = held.Trains },
			"a train named by the probe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			var held *tracker.Catalogue
			s.catalogue = func(_ context.Context, board tracker.BoardRef, scopes []tracker.Scope) (tracker.Catalogue, error) {
				fresh, err := (&stub{}).Catalogue(context.Background(), board, scopes)
				if err != nil {
					return tracker.Catalogue{}, err
				}
				if held == nil {
					held = &fresh
					return fresh, nil
				}
				// Everything is rebuilt except the one set this connector kept
				// and hands out by reference.
				tc.share(held, &fresh)
				return fresh, nil
			}
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckApplyNoCachedIdentity)
			if detail, _ := failed(rep, CheckApplyNoCachedIdentity); !strings.Contains(detail, tc.wantDetail) {
				t.Errorf("the failure does not name the set that leaked: want %q, got %q", tc.wantDetail, detail)
			}
		})
	}
}

func TestConform_CatchesACatalogueThatCarriesNothing(t *testing.T) {
	s := newStub()
	s.catalogue = func(_ context.Context, board tracker.BoardRef, _ []tracker.Scope) (tracker.Catalogue, error) {
		return tracker.Catalogue{Board: board, ScopeKind: "stub-partition"}, nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyNoCachedIdentity)
}

// TestConform_CatchesARefusalThatChangesItsMindBetweenCalls is the falsifier
// for the harness's second Apply. Nothing about the board changed between the
// two calls, so a different classification came from state the connector
// carried over — which is what a held identity looks like from outside a
// contract that carries none.
func TestConform_CatchesARefusalThatChangesItsMindBetweenCalls(t *testing.T) {
	s := newStub()
	// Diverges on the SECOND of two consecutive refusals rather than on a call
	// count, so the stub is aimed at the property — repeat the same refused
	// change set and get a different answer — and not at this harness's
	// particular call order.
	lastWasLive := false
	s.apply = func(ctx context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		refuse := func(board tracker.BoardRef, ch tracker.Change) error {
			if err := stubParentRefusal(board, ch); err != nil {
				live := ctx.Err() == nil
				repeat := live && lastWasLive
				lastWasLive = live
				if repeat {
					return cerr.New(cerr.KindUnavailable, "Apply", errors.New("stale routing state"))
				}
				return err
			}
			return stubTargetRefusal(board, ch)
		}
		return stubApplyReport(board, changes, refuse), nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyNoCachedIdentity)
}

// TestConform_CatchesTheSECONDCatalogueGoingWrong is one violating stub per
// assertion about the second read. The leak check cannot see either of these:
// the second catalogue has to be READ and SHAPED before it can be compared
// against the first, and a connector whose re-read fails or comes back for
// another board has told the caller nothing about whether it cached anything.
func TestConform_CatchesTheSECONDCatalogueGoingWrong(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fix        func(*tracker.Catalogue)
		err        error
		wantDetail string
	}{
		{"the second read fails where the first succeeded", nil,
			cerr.New(cerr.KindUnavailable, "Catalogue", errors.New("secondary rate limit")),
			"a second Catalogue"},
		{"the second read declares no ScopeKind", func(cat *tracker.Catalogue) { cat.ScopeKind = "" }, nil,
			"declares no ScopeKind"},
		{"the second read is for another board", func(cat *tracker.Catalogue) { cat.Board = fixForeign }, nil,
			"Catalogue reports it is for"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub()
			calls := 0
			s.catalogue = func(_ context.Context, board tracker.BoardRef, scopes []tracker.Scope) (tracker.Catalogue, error) {
				calls++
				cat, err := (&stub{}).Catalogue(context.Background(), board, scopes)
				if err != nil || calls == 1 {
					return cat, err
				}
				if tc.err != nil {
					return tracker.Catalogue{}, tc.err
				}
				tc.fix(&cat)
				return cat, nil
			}
			rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
			requireFailed(t, rep, CheckApplyNoCachedIdentity)
			if calls < 2 {
				t.Errorf("the check read the Catalogue %d time(s), so the second-read assertions were never driven", calls)
			}
			if detail, _ := failed(rep, CheckApplyNoCachedIdentity); !strings.Contains(detail, tc.wantDetail) {
				t.Errorf("the failure is not the second read's own: want a detail containing %q, got %q",
					tc.wantDetail, detail)
			}
		})
	}
}

// TestConform_CatchesARefusalTheSecondApplyDoesNotRepeat is the second call's own
// falsifier, and it is why the probe examines BOTH answers instead of comparing
// whatever it got. This connector refuses the foreign parent once and then
// remembers it has already looked at that change set — so the second Apply
// applies it. Comparing classifications alone cannot see that: the second call
// produced none.
func TestConform_CatchesARefusalTheSecondApplyDoesNotRepeat(t *testing.T) {
	s := newStub()
	// Diverges on the SECOND of two consecutive live refusals, like the
	// changes-its-mind stub above and for the same reason: the property is
	// "repeat one refused change set and get a different answer", not this
	// harness's particular call order. The difference is what the repeat
	// answers — nothing at all, rather than another classification.
	lastWasLive := false
	s.apply = func(ctx context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		refuse := func(board tracker.BoardRef, ch tracker.Change) error {
			if err := stubParentRefusal(board, ch); err != nil {
				live := ctx.Err() == nil
				repeat := live && lastWasLive
				lastWasLive = live
				if repeat {
					return nil // "already validated this set": the link is written
				}
				return err
			}
			return stubTargetRefusal(board, ch)
		}
		return stubApplyReport(board, changes, refuse), nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyNoCachedIdentity)
	detail, _ := failed(rep, CheckApplyNoCachedIdentity)
	if !strings.Contains(detail, "the second of two Applies") {
		t.Errorf("the failure does not say which of the two calls refused nothing: %q", detail)
	}
	// The cross-tracker check alternates a live call with a canceled one, so it
	// never sees two live refusals in a row: this stub is aimed at the repeat
	// probe alone.
	requirePassed(t, rep, CheckApplyCrossTrackerRefused)
}

func TestConform_CatchesACatalogueThatFailsOnABoardItServes(t *testing.T) {
	s := newStub()
	s.catalogue = func(context.Context, tracker.BoardRef, []tracker.Scope) (tracker.Catalogue, error) {
		return tracker.Catalogue{}, cerr.New(cerr.KindUnavailable, "Catalogue", nil)
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckApplyNoCachedIdentity)
	if detail, _ := failed(rep, CheckApplyNoCachedIdentity); !strings.Contains(detail,
		"failed on a board the fixtures say it must serve") {
		t.Errorf("the failure is attributed to the catalogue's SHAPE rather than to the read that failed: %q", detail)
	}
}

// ---------------------------------------------------------------------------
// The gate on the gate
// ---------------------------------------------------------------------------

// TestRun_EveryCheckHasAViolatingStub is the falsifier for this whole
// file. It walks every check the harness runs and asserts that at least one
// test in this package has been seen to FAIL it — a check with no violating
// stub has never been observed to bite, and a gate nobody has watched fail is
// an assertion rather than a gate.
//
// It works by running the violating stubs itself rather than by inspecting
// test names, so a renamed or deleted test cannot leave the claim standing.
func TestRun_EveryCheckHasAViolatingStub(t *testing.T) {
	bitten := map[string]bool{}
	for name, violate := range violatingStubs() {
		t.Run(name, func(t *testing.T) {
			s, m := violate()
			rep := run(t, s, m)
			detail, did := failed(rep, name)
			if !did {
				t.Fatalf("%s passed against a connector built to violate it\n%s", name, rep)
			}
			if detail == "" {
				t.Errorf("%s failed with no detail", name)
			}
			bitten[name] = true
		})
	}
	for _, name := range conformChecks() {
		if !bitten[name] {
			t.Errorf("check %s has no violating stub, so it has never been observed to fail", name)
		}
	}
}

// violatingStubs is one connector per check, each built to break exactly that
// check's property. The map is keyed by check name so a check added without a
// falsifier fails TestRun_EveryCheckHasAViolatingStub.
func violatingStubs() map[string]func() (tracker.Tracker, manifest.Doc) {
	return map[string]func() (tracker.Tracker, manifest.Doc){
		CheckManifest: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.caps = connector.Capabilities{"native-hierarchy"}
			return s, stubManifest("native-hierarchy")
		},
		CheckClass: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.class = connector.ClassRoster
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		CheckCapabilityHonesty: func() (tracker.Tracker, manifest.Doc) {
			return newStub(), stubManifest(tracker.CapNativeHierarchy, tracker.CapCrossScope)
		},
		CheckOptionalDeclared: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.caps = connector.Capabilities{tracker.CapNativeHierarchy, tracker.CapTrains}
			return s, stubManifest(tracker.CapNativeHierarchy, tracker.CapTrains)
		},
		CheckListNoEmptySuccess: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.scan = func(context.Context, tracker.BoardRef, tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				return tracker.Resolution[tracker.Item]{}, nil
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		CheckListFailClosed: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.scan = func(_ context.Context, _ tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				return tracker.Resolved(stubItems(sel, connector.Capabilities{tracker.CapNativeHierarchy}), tracker.Complete)
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		CheckHealth: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.health = func(context.Context) (connector.Health, error) {
				return connector.Health{Status: connector.HealthStatus(42)}, nil
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		CheckScanPagedToExhaustion: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				if board != fixScannable {
					return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", nil)
				}
				all := stubItems(sel, s.caps)
				if sel.Children {
					all = all[:1]
				}
				return tracker.Resolved(all, tracker.Complete)
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		CheckReadUnreadableIsNotEmpty: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.scan = func(_ context.Context, board tracker.BoardRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				if board != fixScannable {
					return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "Scan", nil)
				}
				items := stubItems(sel, s.caps)
				if sel.Children {
					for i := range items {
						items[i].Children = []tracker.ItemRef{}
					}
				}
				return tracker.Resolved(items, tracker.Complete)
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		CheckSelectionEchoed: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.getItems = func(_ context.Context, board tracker.BoardRef, refs []tracker.ItemRef, sel tracker.Selection) (tracker.Resolution[tracker.Item], error) {
				all := stubItems(sel, s.caps)
				out := make([]tracker.Item, 0, len(refs))
				for _, r := range refs {
					idx := slices.IndexFunc(all, func(it tracker.Item) bool { return it.Ref == r })
					if idx < 0 {
						return tracker.Resolution[tracker.Item]{}, cerr.New(cerr.KindNotFound, "GetItems", nil)
					}
					it := all[idx]
					it.Selected = tracker.Selection{}
					out = append(out, it)
				}
				return tracker.Resolved(out, tracker.Complete)
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		CheckApplyAttribution: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.apply = func(_ context.Context, _ tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
				return tracker.ApplyReport{Requested: len(changes), Applied: len(changes)}, nil
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		CheckApplyCrossTrackerRefused: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
				return stubApplyReport(board, changes, stubTargetRefusal), nil
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		CheckApplyNoCachedIdentity: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.catalogue = func(context.Context, tracker.BoardRef, []tracker.Scope) (tracker.Catalogue, error) {
				return tracker.Catalogue{}, cerr.New(cerr.KindUnavailable, "Catalogue", nil)
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		// A connector that takes Change.Place and silently does nothing with
		// it, declaring no board membership. It is the realistic defect rather
		// than a contrived one: the flag is new, ignoring an unknown bool is
		// what a partial implementation does, and the report comes back saying
		// the change was applied. Nothing a caller can read afterwards
		// contradicts it, because GetItems refuses an off-board item.
		CheckPlacementHonoursItsCapability: func() (tracker.Tracker, manifest.Doc) {
			s := newStub()
			s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
				return stubApplyReport(board, changes, func(b tracker.BoardRef, ch tracker.Change) error {
					if err := stubTargetRefusalIgnoringPlace(b, ch); err != nil {
						return err
					}
					return stubParentRefusal(b, ch)
				}), nil
			}
			return s, stubManifest(tracker.CapNativeHierarchy)
		},
		// A scope it cannot read, reported as a scope with no trains. This is
		// the shape the union rule forbids and the one a partial read produces
		// most naturally: the answer is well-formed, complete-looking, and one
		// scope short of the truth.
		CheckTrainsUnion: func() (tracker.Tracker, manifest.Doc) {
			a := newTrainAdmin(true)
			a.listTrains = func(_ context.Context, scopes []tracker.Scope) (tracker.Resolution[tracker.ScopeTrains], error) {
				sets := make([]tracker.ScopeTrains, 0, len(scopes))
				for _, sc := range scopes {
					sets = append(sets, tracker.ScopeTrains{Scope: sc, Trains: []tracker.Train{}})
				}
				return tracker.Resolved(sets, tracker.Complete)
			}
			return a, trainManifest(true)
		},
		// A create that reports every spec as refused and creates one anyway.
		// The report's arithmetic closes, every index is attributed, and the
		// counts agree with the demand — so only the re-read sees it.
		CheckTrainsCreateVerified: func() (tracker.Tracker, manifest.Doc) {
			a := newTrainAdmin(false)
			a.createTrains = func(_ context.Context, specs []tracker.TrainSpec) (tracker.ApplyReport, error) {
				rep := tracker.ApplyReport{Requested: len(specs), Failed: map[int]error{}}
				for i, sp := range specs {
					rep.Failed[i] = cerr.New(cerr.KindInvalid, "CreateTrains", errors.New("scope must be empty"))
					if i == 0 {
						a.created[""] = append(a.created[""], tracker.Train{Name: sp.Name, Open: true})
					}
				}
				return rep, nil
			}
			return a, trainManifest(false)
		},
	}
}

// ---------------------------------------------------------------------------
// 13a. apply/placement-honours-its-capability
// ---------------------------------------------------------------------------

// placingStub is a conformant connector that DOES administer board membership:
// it declares tracker.CapBoardMembership and accepts Change.Place, while
// keeping every other pre-network refusal. It is what the declared arm of the
// check runs against.
func placingStub() (*stub, manifest.Doc) {
	s := newStub()
	s.caps = connector.Capabilities{tracker.CapNativeHierarchy, tracker.CapBoardMembership}
	s.apply = func(ctx context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		if !board.Valid() {
			return tracker.ApplyReport{}, cerr.New(cerr.KindInvalid, "Apply", errors.New("board is not fully qualified"))
		}
		// Place is allowed; everything else is still refused, and the field
		// rules run REGARDLESS of Place — which is the property the declared
		// arm checks.
		rep := stubApplyReport(board, changes, func(b tracker.BoardRef, ch tracker.Change) error {
			if err := stubTargetRefusalIgnoringPlace(b, ch); err != nil {
				return err
			}
			return stubParentRefusal(b, ch)
		})
		if rep.Applied > 0 {
			if err := ctx.Err(); err != nil {
				return tracker.ApplyReport{}, cerr.New(cerr.KindUnavailable, "Apply", err)
			}
		}
		return rep, nil
	}
	return s, stubManifest(tracker.CapNativeHierarchy, tracker.CapBoardMembership)
}

// TestConform_PlacingConnector_IsGreen is the other direction of the check, and
// the one the violating-stub table cannot express: a connector that legitimately
// serves placement must PASS, not merely fail differently. Without this the
// check could be satisfied by any connector that refuses Place unconditionally,
// which would make the capability undeclarable in practice.
func TestConform_PlacingConnector_IsGreen(t *testing.T) {
	s, m := placingStub()
	rep := run(t, s, m)
	requireGreen(t, rep)
	detail, _ := passed(rep, CheckPlacementHonoursItsCapability)
	if !strings.Contains(detail, "unroutable field") {
		t.Errorf("the pass does not say which arm ran, so a connector declaring the capability and one not "+
			"declaring it are indistinguishable in the report: %q", detail)
	}
}

// TestConform_CatchesPlacementThatBypassesValidation is the declared arm's
// failure. A connector that reaches its add-to-board mutation before validating
// the rest of the change places the item and then reports the change refused —
// the surviving half of a rejected write, and the one outcome worse than either
// a clean success or a clean refusal, because the report actively denies it
// happened.
func TestConform_CatchesPlacementThatBypassesValidation(t *testing.T) {
	s, m := placingStub()
	s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		return stubApplyReport(board, changes, func(b tracker.BoardRef, ch tracker.Change) error {
			// The bypass: a change asking for placement skips the field rules
			// entirely, so an unroutable field rides in behind the flag.
			if ch.Place {
				return nil
			}
			if err := stubTargetRefusalIgnoringPlace(b, ch); err != nil {
				return err
			}
			return stubParentRefusal(b, ch)
		}), nil
	}
	rep := run(t, s, m)
	requireFailed(t, rep, CheckPlacementHonoursItsCapability)
	if detail, _ := failed(rep, CheckPlacementHonoursItsCapability); !strings.Contains(detail,
		"listed neither as applied nor as failed") {
		t.Errorf("the failure does not say the bypassed change was applied: %q", detail)
	}
}

// TestConform_CatchesPlacementRefusedAsInvalidInsteadOfUnsupported pins the
// classification, because this is the plausible wrong answer rather than a
// careless one — and a host acts on the difference. Unsupported says
// "membership is not administrable here, write the fields that make the item
// match instead"; Invalid says "your request is malformed", and a host cannot
// reach the fallback from there.
func TestConform_CatchesPlacementRefusedAsInvalidInsteadOfUnsupported(t *testing.T) {
	s := newStub()
	s.apply = func(_ context.Context, board tracker.BoardRef, changes []tracker.Change) (tracker.ApplyReport, error) {
		return stubApplyReport(board, changes, func(b tracker.BoardRef, ch tracker.Change) error {
			if ch.Place {
				return cerr.New(cerr.KindInvalid, "Apply", errors.New("no membership administration here"))
			}
			// Every other rule kept, parent included: this stub is wrong about
			// the KIND and about nothing else.
			return stubRefusal(b, ch)
		}), nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))
	requireFailed(t, rep, CheckPlacementHonoursItsCapability)
	detail, _ := failed(rep, CheckPlacementHonoursItsCapability)
	if !strings.Contains(detail, "refused with invalid instead") {
		t.Errorf("the failure does not name the classification it got: %q", detail)
	}
	if !strings.Contains(detail, "write the") {
		t.Errorf("the failure does not tell the author what a host loses by the wrong kind: %q", detail)
	}
	// Aimed at the classification alone: the flag IS refused pre-network, so
	// nothing else about this connector is wrong.
	requirePassed(t, rep, CheckApplyAttribution, CheckApplyCrossTrackerRefused)
}

// ---------------------------------------------------------------------------
// 14. trains/union-accounts-for-every-scope
// ---------------------------------------------------------------------------

// TestConform_AcceptsAScopedTrainAdmin is the green half. A scoped connector
// answers per scope, reports the scope it cannot read WITH a classified error,
// and reports the rest as read.
func TestConform_AcceptsAScopedTrainAdmin(t *testing.T) {
	requireGreen(t, run(t, newTrainAdmin(true), trainManifest(true)))
}

// TestConform_CatchesAnUnreadableScopeReportedAsEmpty is the property in the
// story's own words: a scope that could not be read comes back with Err set,
// never as a scope with no trains. The set is a union over scopes, so the less
// of it a caller can see the greener a replan looks.
func TestConform_CatchesAnUnreadableScopeReportedAsEmpty(t *testing.T) {
	a, m := violatingStubs()[CheckTrainsUnion]()
	rep := run(t, a, m)
	requireFailed(t, rep, CheckTrainsUnion)
	detail, _ := failed(rep, CheckTrainsUnion)
	if !strings.Contains(detail, string(fixUnreadableScope)) {
		t.Errorf("the failure does not name the scope that was reported as empty: %q", detail)
	}
	if !strings.Contains(detail, "NO error") {
		t.Errorf("the failure does not say what was missing from the entry: %q", detail)
	}
}

// TestConform_CatchesATrainSetThatDropsAScope: a scope with no entry computes
// the same answer as one reported empty — "nothing to create here" — and it is
// what a pagination loop that gave up produces.
func TestConform_CatchesATrainSetThatDropsAScope(t *testing.T) {
	a := newTrainAdmin(true)
	a.listTrains = func(_ context.Context, scopes []tracker.Scope) (tracker.Resolution[tracker.ScopeTrains], error) {
		return tracker.Resolved([]tracker.ScopeTrains{
			{Scope: scopes[0], Trains: []tracker.Train{{Name: "0.2.0", Open: true}}},
		}, tracker.Complete)
	}
	rep := run(t, a, trainManifest(true))
	requireFailed(t, rep, CheckTrainsUnion)
}

// TestConform_CatchesATrainSetThatHandsOutTrainsBesideAnError: unknown
// dominates. An entry carrying both is a caller planning against a set it was
// just told it could not see.
func TestConform_CatchesATrainSetThatHandsOutTrainsBesideAnError(t *testing.T) {
	a := newTrainAdmin(true)
	a.listTrains = func(_ context.Context, scopes []tracker.Scope) (tracker.Resolution[tracker.ScopeTrains], error) {
		sets := make([]tracker.ScopeTrains, 0, len(scopes))
		for _, sc := range scopes {
			e := tracker.ScopeTrains{Scope: sc, Trains: []tracker.Train{{Name: "0.2.0", Open: true}}}
			if sc == fixUnreadableScope {
				e.Err = cerr.New(cerr.KindUnauthorized, "ListTrains", errors.New("no rights"))
			}
			sets = append(sets, e)
		}
		return tracker.Resolved(sets, tracker.Complete)
	}
	rep := run(t, a, trainManifest(true))
	requireFailed(t, rep, CheckTrainsUnion)
}

// TestConform_CatchesATrainSetWhereEveryScopeIsUnknown closes the degenerate
// pass. A connector that reports every scope as unreadable satisfies "the
// unreadable one carries an error" trivially, while declaring a train surface a
// host can do nothing with.
func TestConform_CatchesATrainSetWhereEveryScopeIsUnknown(t *testing.T) {
	a := newTrainAdmin(true)
	a.listTrains = func(_ context.Context, scopes []tracker.Scope) (tracker.Resolution[tracker.ScopeTrains], error) {
		sets := make([]tracker.ScopeTrains, 0, len(scopes))
		for _, sc := range scopes {
			sets = append(sets, tracker.ScopeTrains{
				Scope: sc,
				Err:   cerr.New(cerr.KindUnauthorized, "ListTrains", errors.New("no rights")),
			})
		}
		return tracker.Resolved(sets, tracker.Complete)
	}
	rep := run(t, a, trainManifest(true))
	requireFailed(t, rep, CheckTrainsUnion)
	if detail, _ := failed(rep, CheckTrainsUnion); !strings.Contains(detail, "came back unreadable") {
		t.Errorf("the failure blames the wrong thing: %q", detail)
	}
}

// ---------------------------------------------------------------------------
// 15. trains/create-verified-by-re-reading
// ---------------------------------------------------------------------------

// TestConform_CatchesATrainCreatedWhileReportedAsRefused is the check's reason
// to exist. The report is impeccable — every spec attributed, every count
// closing against the demand — and one bucket is on the board anyway.
func TestConform_CatchesATrainCreatedWhileReportedAsRefused(t *testing.T) {
	a, m := violatingStubs()[CheckTrainsCreateVerified]()
	rep := run(t, a, m)
	requireFailed(t, rep, CheckTrainsCreateVerified)
	detail, _ := failed(rep, CheckTrainsCreateVerified)
	if !strings.Contains(detail, "a re-read finds train") {
		t.Errorf("the failure is not attributed to the re-read: %q", detail)
	}
	if !strings.Contains(detail, trainProbePrefix) {
		t.Errorf("the failure does not name the train it found: %q", detail)
	}
}

// TestConform_CatchesACreateReportedSuccessfulForEveryNameItWasGiven is the
// other half of the same defect, and the sentence the contract uses for it: a
// create loop that iterated once still returns successfully for every name it
// was given, and the count is the only thing that shows it. Here every spec is
// refusable before any network call, so a report claiming them all applied is
// claiming work the contract required it to refuse.
func TestConform_CatchesACreateReportedSuccessfulForEveryNameItWasGiven(t *testing.T) {
	a := newTrainAdmin(false)
	a.createTrains = func(_ context.Context, specs []tracker.TrainSpec) (tracker.ApplyReport, error) {
		return tracker.ApplyReport{Requested: len(specs), Applied: len(specs)}, nil
	}
	rep := run(t, a, trainManifest(false))
	requireFailed(t, rep, CheckTrainsCreateVerified)
}

// TestConform_CatchesACreateThatLeavesASpecUnattributed: a spec this report
// does not list as failed was applied, so one it accounts for nowhere is
// reported as a success.
func TestConform_CatchesACreateThatLeavesASpecUnattributed(t *testing.T) {
	a := newTrainAdmin(false)
	a.createTrains = func(_ context.Context, specs []tracker.TrainSpec) (tracker.ApplyReport, error) {
		return tracker.ApplyReport{
			Requested: len(specs),
			Failed:    map[int]error{0: cerr.New(cerr.KindInvalid, "CreateTrains", errors.New("scope must be empty"))},
		}, nil
	}
	rep := run(t, a, trainManifest(false))
	requireFailed(t, rep, CheckTrainsCreateVerified)
}

// TestConform_CatchesAPreNetworkRefusalClassifiedAsTransient: a host retries
// what is classified as transient, so a spec the contract makes cerr.KindInvalid
// and the connector calls Unavailable becomes an infinite retry of a request
// that can never succeed.
func TestConform_CatchesAPreNetworkRefusalClassifiedAsTransient(t *testing.T) {
	a := newTrainAdmin(false)
	a.createTrains = func(_ context.Context, specs []tracker.TrainSpec) (tracker.ApplyReport, error) {
		rep := tracker.ApplyReport{Requested: len(specs), Failed: map[int]error{}}
		for i := range specs {
			rep.Failed[i] = cerr.New(cerr.KindUnavailable, "CreateTrains", errors.New("try again"))
		}
		return rep, nil
	}
	rep := run(t, a, trainManifest(false))
	requireFailed(t, rep, CheckTrainsCreateVerified)
	if detail, _ := failed(rep, CheckTrainsCreateVerified); !strings.Contains(detail, "cerr.KindInvalid") {
		t.Errorf("the failure does not name the classification the contract requires: %q", detail)
	}
}

// TestConform_TrainChecksHoldTheOppositeObligationWithoutTheCapability: a
// connector declaring no trains is not a connector the train checks skip. The
// obligation in that direction is that it does not implement the interface, and
// stating it is what keeps these two out of the "reported by nothing" category
// every fixture rule in this file exists to prevent.
func TestConform_TrainChecksHoldTheOppositeObligationWithoutTheCapability(t *testing.T) {
	rep := run(t, newStub(), stubManifest(tracker.CapNativeHierarchy))
	for _, name := range []string{CheckTrainsUnion, CheckTrainsCreateVerified} {
		detail, did := failed(rep, name)
		if did {
			t.Fatalf("%s failed against a connector that declares no trains: %s", name, detail)
		}
		if !strings.Contains(detail, "is not declared and TrainAdmin is not implemented") {
			t.Errorf("%s passed without stating the obligation it observed: %q", name, detail)
		}
	}

	// And the reverse: implementing it without declaring it is a failure of
	// both, because a host reads the manifest before it loads anything.
	undeclared := &trainAdminStub{stub: newStub(), created: map[tracker.Scope][]tracker.Train{}}
	rep = run(t, undeclared, stubManifest(tracker.CapNativeHierarchy))
	for _, name := range []string{CheckTrainsUnion, CheckTrainsCreateVerified} {
		requireFailed(t, rep, name)
	}
}

// ---------------------------------------------------------------------------
// Report plumbing
// ---------------------------------------------------------------------------

func TestReport_Err_IsClassifiedAndNamesEveryFailure(t *testing.T) {
	s := newStub()
	s.class = connector.ClassRoster
	s.scan = func(context.Context, tracker.BoardRef, tracker.Selection) (tracker.Resolution[tracker.Item], error) {
		return tracker.Resolution[tracker.Item]{}, nil
	}
	rep := run(t, s, stubManifest(tracker.CapNativeHierarchy))

	err := rep.Err()
	if err == nil {
		t.Fatal("a report with failures returned no error")
	}
	if got := cerr.KindOf(err); got != cerr.KindInvalid {
		t.Errorf("report error kind = %s, want %s: the connector ran, so its behaviour is invalid rather than unavailable", got, cerr.KindInvalid)
	}
	for _, name := range []string{CheckClass, CheckListNoEmptySuccess} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the report error does not name the failed check %s: %v", name, err)
		}
	}
	if rep.OK() {
		t.Error("OK() reported true for a report with failures")
	}
}

func TestReport_String_MarksEveryCheck(t *testing.T) {
	rep := run(t, newStub(), stubManifest(tracker.CapNativeHierarchy))
	out := rep.String()
	if !strings.Contains(out, "connector=stubtracker") {
		t.Errorf("the report does not name the connector:\n%s", out)
	}
	if got := strings.Count(out, "PASS "); got != len(conformChecks()) {
		t.Errorf("the report marks %d checks PASS, and %d ran:\n%s", got, len(conformChecks()), out)
	}
}

func TestReport_String_NamesAnUnnamedConnector(t *testing.T) {
	if !strings.Contains(Report{Results: []Result{{Name: CheckClass, Pass: true}}}.String(), "<unnamed>") {
		t.Error("a report from a manifest with no name does not say so")
	}
}
