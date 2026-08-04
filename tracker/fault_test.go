package tracker

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

const connectorName = "boardprovider-under-test"

// testBoard is a fully-qualified address, because a partial one is refused and
// every fixture item here has to be addressable.
var testBoard = BoardRef{Provider: "boardprovider", Instance: "example-org", Board: "3"}

// TestCheckResolution_RejectsAnUnresolvedSuccessAndNamesTheConnector: Items
// already refuses to read an unresolved result, so the guard's added value is
// ATTRIBUTION — which connector, in which operation.
func TestCheckResolution_RejectsAnUnresolvedSuccessAndNamesTheConnector(t *testing.T) {
	_, err := CheckResolution(connectorName, "Scan", Resolution[Item]{}, nil)
	if err == nil {
		t.Fatal("a success carrying no list must be refused")
	}
	var fe *FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *tracker.FaultError", err)
	}
	if fe.Connector != connectorName || fe.Op != "Scan" {
		t.Fatalf("the fault must name the connector and the operation, got %+v", fe)
	}
	if fe.Fault != FaultUnresolved {
		t.Fatalf("Fault = %q, want %q", fe.Fault, FaultUnresolved)
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
	if !strings.Contains(err.Error(), connectorName) {
		t.Fatalf("the message must carry the connector name for an operator reading a log: %v", err)
	}
	if !strings.Contains(err.Error(), "Tracker") {
		t.Fatalf("the message must name the contract that was broken, not the one whose Resolution this reuses: %v", err)
	}
}

// TestCheckResolution_PassesAConnectorsOwnTypedFailureThrough: a connector
// that failed CORRECTLY is not at fault, and rewriting its classification
// would destroy the only thing a host acts on.
func TestCheckResolution_PassesAConnectorsOwnTypedFailureThrough(t *testing.T) {
	own := cerr.New(cerr.KindRateLimited, "Scan", nil)
	_, err := CheckResolution(connectorName, "Scan", Resolution[Item]{}, own)
	if !errors.Is(err, own) {
		t.Fatalf("the connector's own failure must pass through unchanged, got %v", err)
	}
	if cerr.KindOf(err) != cerr.KindRateLimited {
		t.Fatalf("KindOf = %v, want KindRateLimited", cerr.KindOf(err))
	}
	var fe *FaultError
	if errors.As(err, &fe) {
		t.Fatal("an honest failure must not be reported as a contract violation")
	}
}

// TestCheckResolution_TranslatesAFaultRaisedInsideTheSDKsResolution is the one
// property this package's fault vocabulary would not have without a guard of
// its own.
//
// [Resolution] IS the SDK's roster resolution, so the fault [Resolved] raises
// is a *roster.FaultError. A host that matched on *tracker.FaultError would
// miss it, and a host told to match on both would be matching two error types
// for one contract. The guard is the single place that resolves this, so it
// must translate rather than forward.
func TestCheckResolution_TranslatesAFaultRaisedInsideTheSDKsResolution(t *testing.T) {
	_, inner := Resolved[Item](nil, Complete)
	var rfe *roster.FaultError
	if !errors.As(inner, &rfe) {
		t.Fatalf("Resolved must raise the SDK's fault: got %T", inner)
	}
	if rfe.Connector != "" {
		t.Fatal("a fault raised inside a connector cannot know the connector's name")
	}

	_, err := CheckResolution(connectorName, "GetItems", Resolution[Item]{}, inner)
	var fe *FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *tracker.FaultError: the guard must translate the SDK's fault onto this contract's vocabulary", err)
	}
	if fe.Connector != connectorName || fe.Op != "GetItems" {
		t.Fatalf("the translated fault must name the connector and the operation, got %+v", fe)
	}
	if fe.Fault != FaultUnresolved {
		t.Fatalf("Fault = %q, want %q", fe.Fault, FaultUnresolved)
	}
	if fe.Detail == "" {
		t.Error("the SDK's detail was dropped: it is the sentence that tells the author what to write instead")
	}
	if rfe.Connector != "" {
		t.Error("the guard mutated the caller's error rather than returning a named copy")
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
}

func TestCheckResolution_PassesAConformantResolutionThrough(t *testing.T) {
	res, err := CheckResolution(connectorName, "Scan", EmptyList[Item](), nil)
	if err != nil {
		t.Fatalf("a deliberately-empty board is conformant: %v", err)
	}
	items, ierr := res.Items()
	if ierr != nil {
		t.Fatalf("the resolution must come back readable: %v", ierr)
	}
	if len(items) != 0 {
		t.Errorf("Items() = %v, want the asserted-empty list", items)
	}
}

// TestCheckResolution_ReturnsTheListItWasGiven asserts the half of this guard
// that had no gate at all: WHAT IT RETURNS on the conformant path.
//
// Every other check here is about the refusals. But the guard's product on the
// path that is not broken is the resolution itself, and a version of it that
// answered EmptyList[Item]() to every conformant call passed the entire suite —
// a silent, total board wipe inside the one function whose stated job is to
// stop an empty list being mistaken for a board. Emptiness would then be
// ASSERTED by the guard, so Items() succeeds and every downstream audit reports
// each item it never saw as compliant.
func TestCheckResolution_ReturnsTheListItWasGiven(t *testing.T) {
	want := []Item{
		{
			Ref:      ItemRef{Board: testBoard, Scope: "example-org/service-api", Number: 54},
			Title:    "the class contract",
			Type:     "Story",
			Open:     true,
			OpenRead: true,
			Fields:   map[string]Value{"Priority": {Kind: ValueOption, Option: "P1"}},
			Selected: Selection{BoardFields: true},
		},
		{
			Ref:      ItemRef{Board: testBoard, Scope: "example-org/web-app", Number: 284},
			Title:    "a child in another scope",
			Type:     "Story",
			Open:     true,
			OpenRead: true,
			// Every member populated, because this is a round-trip assertion: a
			// guard that dropped one field of the parent would otherwise return a
			// list that still compared equal.
			Parent: &ItemParent{
				Ref:      ItemRef{Board: testBoard, Scope: "example-org/service-api", Number: 53},
				Type:     "Feature",
				Open:     true,
				OpenRead: true,
			},
			Unread:   []string{"Effort"},
			Children: []ItemRef{},
		},
	}
	in, rerr := Resolved(want, Complete)
	if rerr != nil {
		t.Fatalf("Resolved(%d items, Complete) = %v", len(want), rerr)
	}

	got, err := CheckResolution(connectorName, "Scan", in, nil)
	if err != nil {
		t.Fatalf("a conformant non-empty read was refused: %v", err)
	}
	items, ierr := got.Items()
	if ierr != nil {
		t.Fatalf("the resolution must come back readable: %v", ierr)
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("Items() round-tripped as %+v, want the list that went in, %+v — the guard replaced the board it was "+
			"handed, and a caller cannot tell that from a board that holds that much", items, want)
	}
}

// TestFaultNames_MatchTheSDKsSoTranslationIsTotal is what keeps the
// translation in attribute honest. It maps a *roster.FaultError onto this
// contract's vocabulary BY NAME, so a rename on either side would silently
// produce a Fault outside the closed set here — a name a host does not
// recognise, which is the one thing a closed vocabulary exists to prevent.
func TestFaultNames_MatchTheSDKsSoTranslationIsTotal(t *testing.T) {
	for _, tc := range []struct {
		mine Fault
		sdk  roster.Fault
	}{
		{FaultUnresolved, roster.FaultUnresolved},
		{FaultPartial, roster.FaultPartial},
	} {
		if string(tc.mine) != string(tc.sdk) {
			t.Errorf("fault %q does not match the SDK's %q: a fault raised inside the aliased Resolution would "+
				"translate to a name outside this contract's vocabulary", tc.mine, tc.sdk)
		}
		if !tc.mine.Valid() {
			t.Errorf("fault %q is not in this contract's closed vocabulary, so attribute would refuse the SDK fault "+
				"it is supposed to translate", tc.mine)
		}
	}
	// The vocabulary itself, so a constant cannot be half-added: a Fault this
	// package publishes but faults does not carry is one attribute refuses.
	for _, f := range []Fault{
		FaultUnresolved, FaultPartial, FaultUnattributed,
		FaultScopeUnaccounted, FaultUnknownAsKnown, FaultCreateUnverified,
	} {
		if !f.Valid() {
			t.Errorf("Fault(%q).Valid() = false: it is published as a constant of this contract and absent from the "+
				"closed set the translation checks", f)
		}
	}
	if Fault("invented-by-a-connector").Valid() {
		t.Error("an unpublished name is Valid: the set is not closed")
	}
}

// TestAttribute_RefusesARosterFaultOutsideThisContractsVocabulary is the
// falsifier for the word "total" in the test above, which proves only that TWO
// names line up.
//
// roster publishes five Fault constants and three of them belong to that class
// alone — membership-is-for-another-group and the two
// undeclared-capability ones. Translating BY NAME would convert any of them
// into a tracker.Fault outside this closed vocabulary: a host that switches on
// the name gets one it has never heard of, from a type whose whole promise is
// that the set is enumerable. So the out-of-set case is REFUSED, and refused
// without losing anything a host acts on.
func TestAttribute_RefusesARosterFaultOutsideThisContractsVocabulary(t *testing.T) {
	foreign := error(&roster.FaultError{
		Op:     "ListMemberships",
		Fault:  roster.FaultMembershipMismatch,
		Detail: "entry 0 is for group \"other\"",
	})

	_, err := CheckResolution(connectorName, "Scan", Resolution[Item]{}, foreign)
	if err == nil {
		t.Fatal("a roster fault was dropped entirely")
	}
	var fe *FaultError
	if errors.As(err, &fe) {
		t.Fatalf("the guard minted %q as a Fault of this contract; %q is not in its closed vocabulary and a host "+
			"switching on the name would not recognise it", fe.Fault, roster.FaultMembershipMismatch)
	}
	if !strings.Contains(err.Error(), string(roster.FaultMembershipMismatch)) {
		t.Errorf("the refusal does not name the fault it refused, so an operator cannot see what arrived: %v", err)
	}
	if !strings.Contains(err.Error(), connectorName) {
		t.Errorf("the refusal does not name the connector: %v", err)
	}
	// Refused, not discarded: classification and the SDK's own error both
	// survive, so a classification-only host is unaffected by the refusal.
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Errorf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
	if !errors.Is(err, foreign) {
		t.Error("the SDK's fault was replaced rather than wrapped: its detail is the only account of what happened")
	}

	// The refusal renders an unnamed connector the way FaultError.Error does,
	// rather than as an empty quoted string: a host is supposed to pass a name,
	// and a message that reads about connector "" tells an operator nothing at
	// exactly the point they are looking for who.
	_, anon := CheckResolution("", "Scan", Resolution[Item]{}, foreign)
	if anon == nil {
		t.Fatal("a roster fault from an unnamed connector was dropped entirely")
	}
	if strings.Contains(anon.Error(), `""`) {
		t.Errorf("an unnamed connector rendered as an empty quoted string: %v", anon)
	}
}

// TestAttribute_KeepsTheSDKsWordsForAFaultThisContractHasNoWordingFor covers
// the detail fallback. faultDetails carries this contract's own wording for the
// two faults an aliased Resolution raises; FaultUnattributed's detail is
// per-instance because it reports arithmetic, so a *roster.FaultError arriving
// under that name has no canned sentence to be given. Dropping the SDK's
// instead would leave an operator a fault name and no account of it.
func TestAttribute_KeepsTheSDKsWordsForAFaultThisContractHasNoWordingFor(t *testing.T) {
	const words = "the SDK's own account of what went wrong"
	inner := error(&roster.FaultError{Op: "List", Fault: roster.Fault(FaultUnattributed), Detail: words})

	_, err := CheckApplyReport(connectorName, "Apply", 3, ApplyReport{}, inner)
	var fe *FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *tracker.FaultError: %q is in this contract's vocabulary and must translate", err,
			FaultUnattributed)
	}
	if fe.Fault != FaultUnattributed {
		t.Fatalf("Fault = %q, want %q", fe.Fault, FaultUnattributed)
	}
	if fe.Detail != words {
		t.Errorf("Detail = %q, want the SDK's %q: this contract has no wording for a fault whose detail is "+
			"per-instance, and none is worse than the SDK's", fe.Detail, words)
	}
	if fe.Connector != connectorName {
		t.Errorf("the translated fault does not name the connector: %+v", fe)
	}
}

// TestAttribute_NamesAFaultThisContractRaisedWithoutOne covers attribute's OWN
// branch — the *tracker.FaultError one, distinct from the SDK translation.
//
// A connector raising a fault of this contract cannot know what the host
// registered it as, so it leaves Connector empty and this is the only place
// that fills it in. Attribution is the stated value of both guards, and with
// the branch deleted the fault still comes back with the right Fault and the
// right Kind — attributed to nobody.
func TestAttribute_NamesAFaultThisContractRaisedWithoutOne(t *testing.T) {
	unnamed := &FaultError{Fault: FaultUnattributed, Detail: "raised inside the connector"}

	_, err := CheckApplyReport(connectorName, "Apply", 3, ApplyReport{}, unnamed)
	var fe *FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *tracker.FaultError", err)
	}
	if fe.Connector != connectorName {
		t.Errorf("Connector = %q, want %q: a fault nobody is named for sends an operator looking in the tracker",
			fe.Connector, connectorName)
	}
	if fe.Op != "Apply" {
		t.Errorf("Op = %q, want %q: the guard fills in the operation the connector could not know", fe.Op, "Apply")
	}
	if fe.Detail != unnamed.Detail {
		t.Errorf("Detail = %q, want the connector's own %q", fe.Detail, unnamed.Detail)
	}
	if unnamed.Connector != "" {
		t.Error("the guard mutated the caller's error rather than returning a named copy")
	}

	// An already-attributed fault passes through untouched: re-attributing one
	// would name the connector that PASSED it on rather than the one at fault,
	// which is the same wrong answer in the other direction.
	named := &FaultError{Connector: "the-connector-actually-at-fault", Op: "Scan", Fault: FaultUnresolved}
	_, err = CheckApplyReport(connectorName, "Apply", 0, ApplyReport{}, named)
	if !errors.Is(err, named) {
		t.Fatalf("an already-attributed fault came back as %v, want the caller's own error unchanged", err)
	}
	if named.Connector != "the-connector-actually-at-fault" || named.Op != "Scan" {
		t.Errorf("the guard re-attributed a fault that already named its connector: %+v", named)
	}
}

// TestCheckResolution_TranslatesAPartialReadFault covers the second of the two
// faults an aliased Resolution can raise, which is the one a real pagination
// loop produces.
func TestCheckResolution_TranslatesAPartialReadFault(t *testing.T) {
	_, inner := Resolved([]Item{{Title: "page one"}}, Partial)
	_, err := CheckResolution(connectorName, "Scan", Resolution[Item]{}, inner)
	var fe *FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *tracker.FaultError", err)
	}
	if fe.Fault != FaultPartial {
		t.Fatalf("Fault = %q, want %q", fe.Fault, FaultPartial)
	}
	if fe.Connector != connectorName {
		t.Errorf("the translated fault does not name the connector: %+v", fe)
	}
}

// TestFaultError_IsNotRetryableAndDoesNotTripTheBreaker pins what a
// classification-only host does with a fault: a broken connector does not
// improve on a retry, and it is not backend load.
func TestFaultError_IsNotRetryableAndDoesNotTripTheBreaker(t *testing.T) {
	err := error(&FaultError{Connector: connectorName, Op: "Apply", Fault: FaultUnattributed})
	if cerr.Retryable(err) {
		t.Error("a contract violation was reported as retryable")
	}
	if cerr.TripsBreaker(err) {
		t.Error("a contract violation trips the circuit breaker: the backend is not the thing that is wrong")
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Errorf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
}

func TestFaultError_Error_NamesAnUnnamedConnectorAndOp(t *testing.T) {
	msg := (&FaultError{Fault: FaultUnresolved}).Error()
	if strings.Contains(msg, `""`) {
		t.Errorf("an unnamed connector rendered as an empty quoted string: %q", msg)
	}
	if !strings.Contains(msg, string(FaultUnresolved)) {
		t.Errorf("the message does not name the fault: %q", msg)
	}
}

// TestCheckApplyReport_AcceptsAReportWhoseArithmeticCloses is the shape the
// contract admits: Requested is a DEMAND figure, so it may exceed Applied —
// but every change it counted must be accounted for either as applied or as
// attributed in Failed.
//
// It also asserts what the guard RETURNS, which is the half that had no gate:
// a version returning ApplyReport{} on every conformant call passed the whole
// suite, and a zeroed report is precisely the shape [CheckApplyReport] exists
// to refuse — handed back, this time, by the check itself.
func TestCheckApplyReport_AcceptsAReportWhoseArithmeticCloses(t *testing.T) {
	for _, rep := range []ApplyReport{
		{Requested: 0, Applied: 0},
		{Requested: 3, Applied: 3},
		{Requested: 3, Applied: 2, Failed: map[int]error{1: cerr.New(cerr.KindInvalid, "Apply", nil)}},
		{Requested: 50, Applied: 48, Failed: map[int]error{
			7:  cerr.New(cerr.KindInvalid, "Apply", nil),
			31: cerr.New(cerr.KindNotFound, "Apply", nil),
		}},
	} {
		got, err := CheckApplyReport(connectorName, "Apply", rep.Requested, rep, nil)
		if err != nil {
			t.Errorf("CheckApplyReport(%+v) = %v, want nil", rep, err)
			continue
		}
		if !reflect.DeepEqual(got, rep) {
			t.Errorf("CheckApplyReport returned %+v, want the report that went in, %+v — the counts and the "+
				"attribution ARE the answer a caller acts on, and a report the guard rewrote is one nobody wrote",
				got, rep)
		}
	}
}

// TestCheckApplyReport_RefusesAReportThatDisagreesWithTheDemand is why the
// guard takes the demand as an argument at all.
//
// Checked against itself, ApplyReport{} with a nil error renders CLEAN: 0
// requested, 0 applied, nothing failed, arithmetic closed. A connector that
// swallowed all fifty changes therefore passed, and the caller read that as a
// clean answer for an outcome nobody determined — the estate's
// unknown-never-clean rule broken on the write path, while the read path is
// fail-closed by construction. Apply is not transactional, so those fifty
// changes are neither known-applied nor known-failed.
func TestCheckApplyReport_RefusesAReportThatDisagreesWithTheDemand(t *testing.T) {
	for name, tc := range map[string]struct {
		requested int
		rep       ApplyReport
	}{
		"a connector that swallowed all fifty changes": {50, ApplyReport{}},
		"a report counting a demand nobody made":       {2, ApplyReport{Requested: 3, Applied: 3}},
		"a report counting fewer than were asked for":  {3, ApplyReport{Requested: 2, Applied: 2}},
		"a negative demand":                            {-1, ApplyReport{Requested: -1}},
	} {
		got, err := CheckApplyReport(connectorName, "Apply", tc.requested, tc.rep, nil)
		if err == nil {
			t.Errorf("%s was accepted: %d change(s) asked for, report %+v", name, tc.requested, tc.rep)
			continue
		}
		var fe *FaultError
		if !errors.As(err, &fe) {
			t.Errorf("%s: error is %T, want *tracker.FaultError", name, err)
			continue
		}
		if fe.Fault != FaultUnattributed {
			t.Errorf("%s: Fault = %q, want %q", name, fe.Fault, FaultUnattributed)
		}
		if fe.Connector != connectorName {
			t.Errorf("%s: the fault does not name the connector: %+v", name, fe)
		}
		if got.Requested != 0 || got.Applied != 0 || got.Failed != nil {
			t.Errorf("%s: the report came back readable: %+v", name, got)
		}
	}
}

// TestCheckApplyReport_RefusesAnUnattributedChange is the fault that matters
// on the write path. Apply is not transactional and "a change not listed as
// failed was applied" is the contract, so a report that accounts for fewer
// changes than it was asked for silently converts a failure into a success.
func TestCheckApplyReport_RefusesAnUnattributedChange(t *testing.T) {
	for name, rep := range map[string]ApplyReport{
		"one change accounted for by nothing": {Requested: 3, Applied: 2},
		"more applied than requested":         {Requested: 1, Applied: 2},
		"a failure index naming no change":    {Requested: 2, Applied: 1, Failed: map[int]error{7: cerr.New(cerr.KindInvalid, "Apply", nil)}},
		"a failure with no reason":            {Requested: 2, Applied: 1, Failed: map[int]error{0: nil}},
		"a negative count":                    {Requested: -1, Applied: 0},
	} {
		// rep.Requested as the demand, so the report agrees with what was asked
		// for and each case is refused on its OWN arithmetic rather than on the
		// demand check.
		got, err := CheckApplyReport(connectorName, "Apply", rep.Requested, rep, nil)
		if err == nil {
			t.Errorf("%s was accepted: %+v", name, rep)
			continue
		}
		var fe *FaultError
		if !errors.As(err, &fe) {
			t.Errorf("%s: error is %T, want *tracker.FaultError", name, err)
			continue
		}
		if fe.Fault != FaultUnattributed {
			t.Errorf("%s: Fault = %q, want %q", name, fe.Fault, FaultUnattributed)
		}
		if fe.Connector != connectorName {
			t.Errorf("%s: the fault does not name the connector: %+v", name, fe)
		}
		if got.Requested != 0 || got.Applied != 0 || got.Failed != nil {
			t.Errorf("%s: the report came back readable: %+v", name, got)
		}
	}
}

// TestCheckApplyReport_AFailedApplyReportsNothing: an Apply that returned a
// typed failure has no report to read. Handing back the counts it happened to
// carry would let a caller act on numbers the connector did not stand behind.
func TestCheckApplyReport_AFailedApplyReportsNothing(t *testing.T) {
	own := cerr.New(cerr.KindUnavailable, "Apply", nil)
	got, err := CheckApplyReport(connectorName, "Apply", 5, ApplyReport{Requested: 5, Applied: 5}, own)
	if !errors.Is(err, own) {
		t.Fatalf("the connector's own failure must pass through unchanged, got %v", err)
	}
	if got.Requested != 0 || got.Applied != 0 {
		t.Errorf("the report survived a failed Apply: %+v", got)
	}
}
