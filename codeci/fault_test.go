package codeci_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codeci"
)

const connectorName = "placeholder-codeci"

// TestCheckResolutionRejectsAnUnresolvedSuccessAndNamesTheConnector: Items
// already refuses to read an unresolved result, so the guard's added value
// is ATTRIBUTION — which connector, in which operation.
func TestCheckResolutionRejectsAnUnresolvedSuccessAndNamesTheConnector(t *testing.T) {
	_, err := codeci.CheckResolution(connectorName, "ListPRs", codeci.Resolution[codeci.PR]{}, nil)
	if err == nil {
		t.Fatalf("a success carrying no list must be refused")
	}
	var fe *codeci.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *codeci.FaultError", err)
	}
	if fe.Connector != connectorName || fe.Op != "ListPRs" {
		t.Fatalf("the fault must name the connector and the operation, got %+v", fe)
	}
	if fe.Fault != codeci.FaultUnresolved {
		t.Fatalf("Fault = %q", fe.Fault)
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
	if !strings.Contains(err.Error(), connectorName) {
		t.Fatalf("the message must carry the connector name for an operator reading a log: %v", err)
	}
}

// TestCheckResolutionPassesAConnectorsOwnTypedFailureThrough: a connector
// that failed CORRECTLY is not at fault, and rewriting its classification
// would destroy the only thing the host acts on.
func TestCheckResolutionPassesAConnectorsOwnTypedFailureThrough(t *testing.T) {
	own := cerr.New(cerr.KindUnauthorized, "ListPRs", nil)
	_, err := codeci.CheckResolution(connectorName, "ListPRs", codeci.Resolution[codeci.PR]{}, own)
	if !errors.Is(err, own) {
		t.Fatalf("the connector's own failure must pass through unchanged, got %v", err)
	}
	if cerr.KindOf(err) != cerr.KindUnauthorized {
		t.Fatalf("KindOf = %v, want KindUnauthorized", cerr.KindOf(err))
	}
	var fe *codeci.FaultError
	if errors.As(err, &fe) {
		t.Fatalf("an honest failure must not be reported as a contract violation")
	}
}

// TestCheckResolutionNamesAnUnnamedFaultRaisedInsideTheConnector: a fault
// raised by Resolved inside the connector carries no name, because the
// connector does not know what it was registered as. The guard fills it in
// without mutating the caller's error.
func TestCheckResolutionNamesAnUnnamedFaultRaisedInsideTheConnector(t *testing.T) {
	_, inner := codeci.Resolved[codeci.PR](nil, codeci.Complete)
	var innerFault *codeci.FaultError
	if !errors.As(inner, &innerFault) {
		t.Fatalf("Resolved must raise a FaultError")
	}
	if innerFault.Connector != "" {
		t.Fatalf("a fault raised inside a connector cannot know the connector's name")
	}

	_, err := codeci.CheckResolution(connectorName, "GetDiff", codeci.Resolution[codeci.PR]{}, inner)
	var named *codeci.FaultError
	if !errors.As(err, &named) {
		t.Fatalf("error is %T, want *codeci.FaultError", err)
	}
	if named.Connector != connectorName {
		t.Fatalf("Connector = %q, want %q", named.Connector, connectorName)
	}
	if innerFault.Connector != "" {
		t.Fatalf("the guard mutated the caller's error instead of returning a named copy")
	}
	if named.Op != "List" {
		t.Fatalf("Op = %q, want the constructor's own op to survive", named.Op)
	}
}

// TestAnAlreadyAttributedFaultIsNotRenamed: a fault that already names a
// connector has been through a guard once — re-attributing it at a second
// layer would blame whichever component happened to run the guard last.
func TestAnAlreadyAttributedFaultIsNotRenamed(t *testing.T) {
	original := &codeci.FaultError{
		Connector: "placeholder-inner-codeci",
		Op:        "ListPRs",
		Fault:     codeci.FaultUnresolved,
	}
	_, err := codeci.CheckResolution("placeholder-outer-wrapper", "ListBranches",
		codeci.Resolution[codeci.PR]{}, original)
	var got *codeci.FaultError
	if !errors.As(err, &got) {
		t.Fatalf("error is %T, want *codeci.FaultError", err)
	}
	if got.Connector != "placeholder-inner-codeci" {
		t.Fatalf("Connector = %q; an already-attributed fault must keep its original name", got.Connector)
	}
	if got.Op != "ListPRs" {
		t.Fatalf("Op = %q; the original operation must survive", got.Op)
	}

	bare := &codeci.FaultError{Fault: codeci.FaultUnresolved}
	_, err = codeci.CheckResolution(connectorName, "ListBranches", codeci.Resolution[codeci.Branch]{}, bare)
	var filled *codeci.FaultError
	if !errors.As(err, &filled) {
		t.Fatalf("error is %T, want *codeci.FaultError", err)
	}
	if filled.Connector != connectorName || filled.Op != "ListBranches" {
		t.Fatalf("an unattributed fault must gain both a connector and an operation, got %+v", filled)
	}
	if bare.Op != "" || bare.Connector != "" {
		t.Fatalf("the guard mutated the caller's error instead of returning a named copy: %+v", bare)
	}
}

// TestCheckResolutionAcceptsAnAssertedEmptyList is the guard on the guard:
// refusing an unresolved result must not become refusing a genuinely-empty
// one — a ref with no CI configured is a real state.
func TestCheckResolutionAcceptsAnAssertedEmptyList(t *testing.T) {
	res, err := codeci.CheckResolution(connectorName, "GetCheckRuns", codeci.EmptyList[codeci.CheckRun](), nil)
	if err != nil {
		t.Fatalf("the host guard must accept a genuinely empty list: %v", err)
	}
	items, err := res.Items()
	if err != nil || len(items) != 0 {
		t.Fatalf("an asserted-empty list must read as an empty list, got %v / %v", items, err)
	}
}

// TestFaultErrorMessageIsReadableWithoutAName covers the shape a fault has
// before a host attributes it — raised inside a connector that does not know
// what it was registered as.
func TestFaultErrorMessageIsReadableWithoutAName(t *testing.T) {
	fe := &codeci.FaultError{Fault: codeci.FaultUnresolved}
	msg := fe.Error()
	for _, want := range []string{"unnamed connector", "a contract operation", string(codeci.FaultUnresolved)} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q is missing %q", msg, want)
		}
	}
	if strings.Contains(msg, "()") {
		t.Fatalf("an empty Detail must not render as empty parentheses: %q", msg)
	}
}

// TestFaultsAreNeitherRetryableNorBreakerTripping: the fault is in the
// connector, not in backend load.
func TestFaultsAreNeitherRetryableNorBreakerTripping(t *testing.T) {
	for _, f := range []codeci.Fault{codeci.FaultUnresolved, codeci.FaultPartial} {
		err := error(&codeci.FaultError{Connector: connectorName, Op: "ListPRs", Fault: f})
		if cerr.KindOf(err) != cerr.KindContractViolation {
			t.Fatalf("%s: KindOf = %v, want KindContractViolation", f, cerr.KindOf(err))
		}
		if cerr.Retryable(err) || cerr.TripsBreaker(err) {
			t.Fatalf("%s must be neither retryable nor breaker-tripping", f)
		}
		if !cerr.Classified(err) {
			t.Fatalf("%s must reach a classification-only host as a classified failure", f)
		}
	}
}
