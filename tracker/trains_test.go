package tracker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

var (
	scopedCaps   = connector.Capabilities{CapTrains, CapScopedTrains}
	unscopedCaps = connector.Capabilities{CapTrains}
)

func trainSets(t *testing.T, sets ...ScopeTrains) Resolution[ScopeTrains] {
	t.Helper()
	res, err := Resolved(sets, Complete)
	if err != nil {
		t.Fatalf("Resolved(%v): %v", sets, err)
	}
	return res
}

// wantFault asserts the guard raised exactly the named fault, attributed to the
// connector. A guard that refused for a DIFFERENT reason than the test is about
// would otherwise read as a pass.
func wantFault(t *testing.T, err error, want Fault, wantOp string) *FaultError {
	t.Helper()
	var fe *FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %v (%T), want a *FaultError", err, err)
	}
	if fe.Fault != want {
		t.Fatalf("fault = %q, want %q (detail: %s)", fe.Fault, want, fe.Detail)
	}
	if fe.Connector != "probe" {
		t.Errorf("fault names connector %q, want %q: an unattributed fault sends an operator looking in the tracker",
			fe.Connector, "probe")
	}
	if fe.Op != wantOp {
		t.Errorf("fault names op %q, want %q", fe.Op, wantOp)
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Errorf("cerr.KindOf = %v, want KindContractViolation: a classification-only host must still route it right",
			cerr.KindOf(err))
	}
	return fe
}

// TestCheckTrainSets_AcceptsAConformantUnionAndReturnsItUnchanged is the half
// that must not be forgotten: the guard's product on the good path is the
// answer the caller acts on. A version that returned an empty resolution here
// would be a silent, total loss of the train set behind a guard whose stated
// job is to prevent one.
func TestCheckTrainSets_AcceptsAConformantUnionAndReturnsItUnchanged(t *testing.T) {
	unreadable := cerr.New(cerr.KindUnavailable, "ListTrains", errors.New("403"))
	in := trainSets(t,
		ScopeTrains{Scope: "a", Trains: []Train{{Name: "0.1.0", Open: true}}},
		ScopeTrains{Scope: "b", Trains: []Train{}},
		ScopeTrains{Scope: "c", Err: unreadable},
	)
	got, err := CheckTrainSets("probe", "ListTrains", scopedCaps, []Scope{"a", "b", "c"}, in, nil)
	if err != nil {
		t.Fatalf("CheckTrainSets rejected a conformant union: %v", err)
	}
	entries, ierr := got.Items()
	if ierr != nil {
		t.Fatalf("the returned resolution is unreadable: %v", ierr)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entr(ies), want the 3 it was given", len(entries))
	}
	if entries[1].Scope != "b" || entries[1].Trains == nil || len(entries[1].Trains) != 0 {
		t.Errorf("entry for scope b = %+v; a scope that genuinely holds no train is empty AND non-nil, which is how "+
			"it stays distinguishable from one that could not be read", entries[1])
	}
	if !errors.Is(entries[2].Err, unreadable) {
		t.Errorf("entry for scope c lost its error: %+v", entries[2])
	}
}

// TestCheckTrainSets_AMissingScopeIsAFaultNotASmallerUnion is the property the
// class contract exists to hold. Dropping a scope from the answer computes the
// same thing as reporting it empty — "nothing to create here" — and it is the
// shape a partial read most naturally produces.
func TestCheckTrainSets_AMissingScopeIsAFaultNotASmallerUnion(t *testing.T) {
	in := trainSets(t,
		ScopeTrains{Scope: "a", Trains: []Train{{Name: "0.1.0", Open: true}}},
	)
	got, err := CheckTrainSets("probe", "ListTrains", scopedCaps, []Scope{"a", "b", "c"}, in, nil)
	fe := wantFault(t, err, FaultScopeUnaccounted, "ListTrains")
	for _, want := range []string{`"b"`, `"c"`} {
		if !strings.Contains(fe.Detail, want) {
			t.Errorf("detail does not name the unaccounted scope %s: %s", want, fe.Detail)
		}
	}
	if _, ierr := got.Items(); ierr == nil {
		t.Error("the rejected resolution is still readable: a caller that ignored the error would read the short union")
	}
}

// TestCheckTrainSets_EmptyAnswerIsAFaultWhenScopesWereAsked closes the
// degenerate case of the same rule. EmptyList is a legitimate assertion for
// "read successfully, found none" of a THING — it is not an answer about a set
// of scopes, because it accounts for none of them.
func TestCheckTrainSets_EmptyAnswerIsAFaultWhenScopesWereAsked(t *testing.T) {
	_, err := CheckTrainSets("probe", "ListTrains", scopedCaps, []Scope{"a"}, EmptyList[ScopeTrains](), nil)
	wantFault(t, err, FaultScopeUnaccounted, "ListTrains")
}

// TestCheckTrainSets_UnknownDominatesOverAnyTrainsBesideIt: an entry carrying
// both an error and a list is not extra information. A caller that read the
// list would plan against a set it was just told it could not see.
func TestCheckTrainSets_UnknownDominatesOverAnyTrainsBesideIt(t *testing.T) {
	in := trainSets(t, ScopeTrains{
		Scope:  "a",
		Trains: []Train{{Name: "0.1.0", Open: true}},
		Err:    cerr.New(cerr.KindUnavailable, "ListTrains", errors.New("timeout mid-page")),
	})
	fe := wantFault(t,
		mustErr(CheckTrainSets("probe", "ListTrains", scopedCaps, []Scope{"a"}, in, nil)),
		FaultUnknownAsKnown, "ListTrains")
	if !strings.Contains(fe.Detail, "1 train(s)") {
		t.Errorf("detail does not say what was handed out beside the error: %s", fe.Detail)
	}
}

// TestCheckTrainSets_AnUnclassifiedScopeErrorIsAFault: a host routes on the
// classification. An unreadable scope reported with a bare error is a scope the
// host cannot decide anything about — not retryable, not permanent, not
// anything.
func TestCheckTrainSets_AnUnclassifiedScopeErrorIsAFault(t *testing.T) {
	in := trainSets(t, ScopeTrains{Scope: "a", Err: errors.New("403 from the API")})
	wantFault(t,
		mustErr(CheckTrainSets("probe", "ListTrains", scopedCaps, []Scope{"a"}, in, nil)),
		FaultUnknownAsKnown, "ListTrains")
}

// TestCheckTrainSets_DuplicateAndForeignScopesAreFaults: two entries for one
// scope can disagree, and which of them a union sees then depends on iteration
// order; an entry about a scope nobody asked about is an answer to another
// question.
func TestCheckTrainSets_DuplicateAndForeignScopesAreFaults(t *testing.T) {
	dup := trainSets(t,
		ScopeTrains{Scope: "a", Trains: []Train{}},
		ScopeTrains{Scope: "a", Err: cerr.New(cerr.KindUnavailable, "ListTrains", errors.New("403"))},
	)
	fe := wantFault(t,
		mustErr(CheckTrainSets("probe", "ListTrains", scopedCaps, []Scope{"a"}, dup, nil)),
		FaultScopeUnaccounted, "ListTrains")
	if !strings.Contains(fe.Detail, "2 times") {
		t.Errorf("detail does not say how many entries one scope got: %s", fe.Detail)
	}

	foreign := trainSets(t,
		ScopeTrains{Scope: "a", Trains: []Train{}},
		ScopeTrains{Scope: "z", Trains: []Train{}},
	)
	wantFault(t,
		mustErr(CheckTrainSets("probe", "ListTrains", scopedCaps, []Scope{"a"}, foreign, nil)),
		FaultScopeUnaccounted, "ListTrains")
}

// TestCheckTrainSets_ShapeFollowsTheDeclaredPartitioning drives both polarities
// of CapScopedTrains, which is the whole reason the guard takes the
// capabilities rather than a boolean. A per-scope answer from an unpartitioned
// connector tells a host to plan one create per scope against a single bucket
// namespace; the reverse plans one create for a backend that needs eight.
func TestCheckTrainSets_ShapeFollowsTheDeclaredPartitioning(t *testing.T) {
	oneZero := trainSets(t, ScopeTrains{Trains: []Train{{Name: "0.1.0", Open: true}}})
	if _, err := CheckTrainSets("probe", "ListTrains", unscopedCaps, []Scope{"a", "b"}, oneZero, nil); err != nil {
		t.Fatalf("an unpartitioned connector's single zero-scope answer was rejected: %v", err)
	}

	perScope := trainSets(t,
		ScopeTrains{Scope: "a", Trains: []Train{}},
		ScopeTrains{Scope: "b", Trains: []Train{}},
	)
	wantFault(t,
		mustErr(CheckTrainSets("probe", "ListTrains", unscopedCaps, []Scope{"a", "b"}, perScope, nil)),
		FaultScopeUnaccounted, "ListTrains")

	named := trainSets(t, ScopeTrains{Scope: "a", Trains: []Train{}})
	wantFault(t,
		mustErr(CheckTrainSets("probe", "ListTrains", unscopedCaps, []Scope{"a"}, named, nil)),
		FaultScopeUnaccounted, "ListTrains")

	// And the same zero-scope answer is NOT acceptable from a connector that
	// declared the partitioning: it accounts for neither scope asked about.
	wantFault(t,
		mustErr(CheckTrainSets("probe", "ListTrains", scopedCaps, []Scope{"a", "b"}, oneZero, nil)),
		FaultScopeUnaccounted, "ListTrains")
}

// TestCheckTrainSets_PassesAConnectorsOwnFailureThrough: a connector that
// failed correctly is not at fault, and the guard must not convert its typed
// failure into a contract violation.
func TestCheckTrainSets_PassesAConnectorsOwnFailureThrough(t *testing.T) {
	own := cerr.New(cerr.KindUnavailable, "ListTrains", errors.New("backend down"))
	_, err := CheckTrainSets("probe", "ListTrains", scopedCaps, []Scope{"a"}, Resolution[ScopeTrains]{}, own)
	if !errors.Is(err, own) {
		t.Fatalf("err = %v, want the connector's own failure unchanged", err)
	}
	var fe *FaultError
	if errors.As(err, &fe) {
		t.Errorf("a correct failure was reported as contract violation %q", fe.Fault)
	}
}

// TestCheckTrainSets_UnresolvedSuccessIsAFault: the fail-closed rule the
// aliased Resolution carries, reached through this guard so a host has one
// error type to match on.
func TestCheckTrainSets_UnresolvedSuccessIsAFault(t *testing.T) {
	wantFault(t,
		mustErr(CheckTrainSets("probe", "ListTrains", scopedCaps, nil, Resolution[ScopeTrains]{}, nil)),
		FaultUnresolved, "ListTrains")
}

// ---------------------------------------------------------------------------
// CheckTrainsCreated
// ---------------------------------------------------------------------------

// fakeAdmin is a TrainAdmin whose ListTrains answers from a fixed table, so a
// test can say exactly what a re-read would find.
type fakeAdmin struct {
	have  map[Scope][]Train
	fail  error
	calls int
}

func (f *fakeAdmin) ListTrains(_ context.Context, scopes []Scope) (Resolution[ScopeTrains], error) {
	f.calls++
	if f.fail != nil {
		return Resolution[ScopeTrains]{}, f.fail
	}
	sets := make([]ScopeTrains, 0, len(scopes))
	for _, s := range scopes {
		sets = append(sets, ScopeTrains{Scope: s, Trains: append([]Train{}, f.have[s]...)})
	}
	if len(sets) == 0 {
		return EmptyList[ScopeTrains](), nil
	}
	return Resolved(sets, Complete)
}

func (f *fakeAdmin) CreateTrains(context.Context, []TrainSpec) (ApplyReport, error) {
	panic("CreateTrains is the call under test, never made by the guard")
}

func (f *fakeAdmin) CloseTrains(context.Context, []TrainSpec) (ApplyReport, error) {
	panic("CloseTrains is the call under test, never made by the guard")
}

func spec(scope Scope, name string) TrainSpec { return TrainSpec{Scope: scope, Name: name} }

// TestCheckTrainsCreated_TheCountAloneCannotCatchTheLoop is the whole point of
// the guard, stated as a test: the report a create loop that iterated once
// produces closes ARITHMETICALLY. CheckApplyReport accepts it. Only the re-read
// sees that seven of the eight names are not there.
func TestCheckTrainsCreated_TheCountAloneCannotCatchTheLoop(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0"), spec("b", "0.3.0"), spec("c", "0.3.0")}
	looped := ApplyReport{Requested: 3, Applied: 3}

	// The arithmetic guard is satisfied. If this ever stops being true the
	// test below is checking something else.
	if _, err := CheckApplyReport("probe", "CreateTrains", len(specs), looped, nil); err != nil {
		t.Fatalf("CheckApplyReport rejected the looped report, so this test no longer isolates the re-read: %v", err)
	}

	admin := &fakeAdmin{have: map[Scope][]Train{"a": {{Name: "0.3.0", Open: true}}}}
	fe := wantFault(t,
		mustErrReport(CheckTrainsCreated(context.Background(), "probe", admin, scopedCaps, specs, looped, nil)),
		FaultCreateUnverified, "CreateTrains")
	for _, want := range []string{`"b"`, `"c"`} {
		if !strings.Contains(fe.Detail, want) {
			t.Errorf("detail does not name the scope whose bucket is absent (%s): %s", want, fe.Detail)
		}
	}
	if strings.Contains(fe.Detail, `"0.3.0" in scope "a"`) {
		t.Errorf("detail blames the bucket that WAS created: %s", fe.Detail)
	}
	if admin.calls != 1 {
		t.Errorf("ListTrains called %d time(s), want exactly 1", admin.calls)
	}
}

// TestCheckTrainsCreated_AVerifiedCreateReturnsTheReportUnchanged.
func TestCheckTrainsCreated_AVerifiedCreateReturnsTheReportUnchanged(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0"), spec("b", "0.3.0")}
	admin := &fakeAdmin{have: map[Scope][]Train{
		"a": {{Name: "0.3.0", Open: true}},
		"b": {{Name: "0.2.0", Open: false}, {Name: "0.3.0", Open: true}},
	}}
	rep := ApplyReport{Requested: 2, Applied: 2}
	got, err := CheckTrainsCreated(context.Background(), "probe", admin, scopedCaps, specs, rep, nil)
	if err != nil {
		t.Fatalf("a verified create was rejected: %v", err)
	}
	if got.Requested != rep.Requested || got.Applied != rep.Applied || len(got.Failed) != 0 {
		t.Errorf("report = %+v, want the one it was given %+v", got, rep)
	}
}

// TestCheckTrainsCreated_OnlyVerifiesWhatTheReportClaimed: a spec the report
// attributed a failure to is not expected on the tracker, and demanding it
// would fail every partially-refused create.
func TestCheckTrainsCreated_OnlyVerifiesWhatTheReportClaimed(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0"), spec("b", "0.3.0")}
	rep := ApplyReport{
		Requested: 2,
		Applied:   1,
		Failed:    map[int]error{1: cerr.New(cerr.KindUnauthorized, "CreateTrains", errors.New("no rights in b"))},
	}
	admin := &fakeAdmin{have: map[Scope][]Train{"a": {{Name: "0.3.0", Open: true}}}}
	if _, err := CheckTrainsCreated(context.Background(), "probe", admin, scopedCaps, specs, rep, nil); err != nil {
		t.Fatalf("a create whose only absent bucket was reported as failed was rejected: %v", err)
	}
}

// TestCheckTrainsCreated_NothingAppliedMakesNoCall: a create whose every spec
// was refused before any network call has nothing to verify, and a read issued
// anyway is a round trip per rejected batch.
func TestCheckTrainsCreated_NothingAppliedMakesNoCall(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0"), spec("b", "0.3.0")}
	rep := ApplyReport{Requested: 2, Applied: 0, Failed: map[int]error{
		0: cerr.New(cerr.KindInvalid, "CreateTrains", errors.New("scope required")),
		1: cerr.New(cerr.KindInvalid, "CreateTrains", errors.New("scope required")),
	}}
	admin := &fakeAdmin{}
	if _, err := CheckTrainsCreated(context.Background(), "probe", admin, scopedCaps, specs, rep, nil); err != nil {
		t.Fatalf("a wholly-refused create was rejected: %v", err)
	}
	if admin.calls != 0 {
		t.Errorf("ListTrains called %d time(s) with nothing to verify", admin.calls)
	}
}

// TestCheckTrainsCreated_AnUnverifiableCreateIsUnknownNotDone: the re-read is
// what makes the create evidence, so a re-read that failed leaves the outcome
// unknown. Reporting it clean is the estate's unknown-never-clean rule broken
// on the write path; reporting it failed would send a caller to re-create
// buckets that may exist.
func TestCheckTrainsCreated_AnUnverifiableCreateIsUnknownNotDone(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0")}
	readFail := cerr.New(cerr.KindUnavailable, "ListTrains", errors.New("backend down"))
	admin := &fakeAdmin{fail: readFail}
	got, err := CheckTrainsCreated(context.Background(), "probe", admin, scopedCaps, specs,
		ApplyReport{Requested: 1, Applied: 1}, nil)
	if err == nil {
		t.Fatal("an unverifiable create was reported as done")
	}
	if got.Requested != 0 || got.Applied != 0 || got.Failed != nil {
		t.Errorf("report = %+v, want a zeroed one: a create nothing confirmed has no counts a caller may act on", got)
	}
	if !errors.Is(err, readFail) {
		t.Errorf("err = %v, want the read failure wrapped so cerr.KindOf still answers its classification", err)
	}
	if cerr.KindOf(err) != cerr.KindUnavailable {
		t.Errorf("cerr.KindOf = %v, want the read's own KindUnavailable", cerr.KindOf(err))
	}
	var fe *FaultError
	if errors.As(err, &fe) {
		t.Errorf("an unverifiable create was reported as the connector's contract violation %q", fe.Fault)
	}
}

// TestCheckTrainsCreated_AReportThatDoesNotAddUpIsRejectedBeforeAnyReRead: the
// arithmetic is the first half and it needs no network. A report that already
// fails to account for its demand must not cost a read.
func TestCheckTrainsCreated_AReportThatDoesNotAddUpIsRejectedBeforeAnyReRead(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0"), spec("b", "0.3.0")}
	admin := &fakeAdmin{}
	fe := wantFault(t,
		mustErrReport(CheckTrainsCreated(context.Background(), "probe", admin, scopedCaps, specs,
			ApplyReport{Requested: 2, Applied: 1}, nil)),
		FaultUnattributed, "CreateTrains")
	if fe.Detail == "" {
		t.Error("the arithmetic fault carries no detail")
	}
	if admin.calls != 0 {
		t.Errorf("ListTrains called %d time(s) for a report that does not add up", admin.calls)
	}
}

// TestCheckTrainsCreated_UnscopedConnectorVerifiesUnderTheZeroScope: on a
// connector without CapScopedTrains a spec's Scope must be empty, and the
// re-read finds every bucket under the zero scope.
func TestCheckTrainsCreated_UnscopedConnectorVerifiesUnderTheZeroScope(t *testing.T) {
	specs := []TrainSpec{spec("", "0.3.0")}
	admin := &fakeAdmin{have: map[Scope][]Train{"": {{Name: "0.3.0", Open: true}}}}
	if _, err := CheckTrainsCreated(context.Background(), "probe", admin, unscopedCaps, specs,
		ApplyReport{Requested: 1, Applied: 1}, nil); err != nil {
		t.Fatalf("an unpartitioned verified create was rejected: %v", err)
	}

	absent := &fakeAdmin{have: map[Scope][]Train{"": {{Name: "0.2.0", Open: false}}}}
	wantFault(t,
		mustErrReport(CheckTrainsCreated(context.Background(), "probe", absent, unscopedCaps, specs,
			ApplyReport{Requested: 1, Applied: 1}, nil)),
		FaultCreateUnverified, "CreateTrains")
}

// TestCheckTrainsCreated_RefusesToVerifyWithoutAnAdmin: a caller that reported
// creates and handed over no way to re-read them gets a refusal, not a pass. A
// nil-tolerant guard is a guard that silently stops guarding.
func TestCheckTrainsCreated_RefusesToVerifyWithoutAnAdmin(t *testing.T) {
	_, err := CheckTrainsCreated(context.Background(), "probe", nil, scopedCaps,
		[]TrainSpec{spec("a", "0.3.0")}, ApplyReport{Requested: 1, Applied: 1}, nil)
	if err == nil {
		t.Fatal("a create was accepted with no TrainAdmin to verify it against")
	}
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Errorf("cerr.KindOf = %v, want KindInvalid: the caller's arguments are what is wrong", cerr.KindOf(err))
	}
}

func mustErr[T any](_ T, err error) error { return err }

func mustErrReport(_ ApplyReport, err error) error { return err }

// ---------------------------------------------------------------------------
// CheckTrainsClosed — the close guard (arqtos-connectors#144)
// ---------------------------------------------------------------------------

// TestCheckTrainsClosed_APresentButStillOpenTrainIsNotClosed is the whole point
// of this guard as distinct from its create sibling.
//
// ⚠️ Every name IS on the tracker, so the create guard's check — does the name
// exist — passes on this input. Only reading Open catches it. A guard that
// reused the create logic would report a release train retired while its
// buckets are still taking work.
func TestCheckTrainsClosed_APresentButStillOpenTrainIsNotClosed(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0"), spec("b", "0.3.0"), spec("c", "0.3.0")}
	looped := ApplyReport{Requested: 3, Applied: 3}

	if _, err := CheckApplyReport("probe", "CloseTrains", len(specs), looped, nil); err != nil {
		t.Fatalf("CheckApplyReport rejected the looped report, so this test no longer isolates the re-read: %v", err)
	}

	// a closed; b and c present and STILL OPEN — the loop-iterated-once shape.
	admin := &fakeAdmin{have: map[Scope][]Train{
		"a": {{Name: "0.3.0", Open: false}},
		"b": {{Name: "0.3.0", Open: true}},
		"c": {{Name: "0.3.0", Open: true}},
	}}
	fe := wantFault(t,
		mustErrReport(CheckTrainsClosed(context.Background(), "probe", admin, scopedCaps, specs, looped, nil)),
		FaultCloseUnverified, "CloseTrains")

	for _, want := range []string{`"b"`, `"c"`, "still open"} {
		if !strings.Contains(fe.Detail, want) {
			t.Errorf("detail does not name the still-open bucket (%s): %s", want, fe.Detail)
		}
	}
	if strings.Contains(fe.Detail, `"0.3.0" in scope "a"`) {
		t.Errorf("detail blames the bucket that WAS closed: %s", fe.Detail)
	}
	if admin.calls != 1 {
		t.Errorf("ListTrains called %d time(s), want exactly 1", admin.calls)
	}
}

// TestCheckTrainsClosed_AnAbsentTrainIsNotVacuouslyClosed.
//
// ⚠️ "It is not there, so it cannot be open" is the reasoning that would let a
// close of a MISTYPED name report success — the name was never a train, nothing
// was retired, and a sweep records it as done.
func TestCheckTrainsClosed_AnAbsentTrainIsNotVacuouslyClosed(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0")}
	admin := &fakeAdmin{have: map[Scope][]Train{"a": {{Name: "0.4.0", Open: false}}}}

	fe := wantFault(t,
		mustErrReport(CheckTrainsClosed(context.Background(), "probe", admin,
			scopedCaps, specs, ApplyReport{Requested: 1, Applied: 1}, nil)),
		FaultCloseUnverified, "CloseTrains")
	if !strings.Contains(fe.Detail, "absent") {
		t.Errorf("an absent train must be reported as absent, not as closed: %s", fe.Detail)
	}
}

// TestCheckTrainsClosed_AVerifiedCloseReturnsTheReportUnchanged.
func TestCheckTrainsClosed_AVerifiedCloseReturnsTheReportUnchanged(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0"), spec("b", "0.3.0")}
	rep := ApplyReport{Requested: 2, Applied: 2}
	admin := &fakeAdmin{have: map[Scope][]Train{
		"a": {{Name: "0.3.0", Open: false}},
		"b": {{Name: "0.3.0", Open: false}},
	}}

	got, err := CheckTrainsClosed(context.Background(), "probe", admin, scopedCaps, specs, rep, nil)
	if err != nil {
		t.Fatalf("a verified close must pass: %v", err)
	}
	if got.Applied != 2 || got.Requested != 2 {
		t.Errorf("report altered by the guard: %+v", got)
	}
}

// TestCheckTrainsClosed_AlreadyClosedIsSuccess pins the contract's idempotence
// ruling: a sweep that retries after a partial failure re-runs the whole set,
// and a train someone else already closed must not fail it.
func TestCheckTrainsClosed_AlreadyClosedIsSuccess(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0")}
	admin := &fakeAdmin{have: map[Scope][]Train{"a": {{Name: "0.3.0", Open: false}}}}

	if _, err := CheckTrainsClosed(context.Background(), "probe", admin,
		scopedCaps, specs, ApplyReport{Requested: 1, Applied: 1}, nil); err != nil {
		t.Errorf("closing an already-closed train must succeed idempotently: %v", err)
	}
}

// TestCheckTrainsClosed_AFailedReadIsUNKNOWNNotDone.
func TestCheckTrainsClosed_AFailedReadIsUNKNOWNNotDone(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0")}
	admin := &fakeAdmin{fail: errors.New("502 from the tracker")}

	got, err := CheckTrainsClosed(context.Background(), "probe", admin,
		scopedCaps, specs, ApplyReport{Requested: 1, Applied: 1}, nil)
	if err == nil {
		t.Fatal("an unverifiable close must not be reported as done")
	}
	if got.Applied != 0 || got.Requested != 0 {
		t.Errorf("the report must be ZEROED when nothing confirmed the close; got %+v", got)
	}
	if !strings.Contains(err.Error(), "UNKNOWN") {
		t.Errorf("the error must say UNKNOWN rather than failed: %v", err)
	}
}

// TestCheckTrainsClosed_NoAppliedMeansNoReadAtAll — a connector whose specs were
// all refused pre-network costs no round trip.
func TestCheckTrainsClosed_NoAppliedMeansNoReadAtAll(t *testing.T) {
	specs := []TrainSpec{spec("a", "0.3.0")}
	admin := &fakeAdmin{}

	if _, err := CheckTrainsClosed(context.Background(), "probe", admin, scopedCaps, specs,
		ApplyReport{Requested: 1, Applied: 0, Failed: map[int]error{0: errors.New("refused")}}, nil); err != nil {
		t.Fatalf("an all-refused batch is not a fault: %v", err)
	}
	if admin.calls != 0 {
		t.Errorf("ListTrains called %d time(s) with nothing to verify, want 0", admin.calls)
	}
}
