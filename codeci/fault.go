package codeci

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// A Fault names a way a connector can break the CodeCI contract: not a
// failure it reports (those are cerr Kinds, chosen by the connector), but a
// return the contract does not admit at all.
//
// The set is closed for the same reason the failure vocabulary is: a host
// acts on the name, and adding one is a change to a published contract.
type Fault string

const (
	// FaultUnresolved is a connector reporting success while carrying no
	// list — the (empty list, no error) shape this contract exists to
	// eliminate.
	FaultUnresolved Fault = "unresolved-without-error"
	// FaultPartial is a connector asserting [Partial] on a call to
	// [Resolved] — a read that stopped before covering everything it is
	// meant to, reported as a success rather than as the typed failure the
	// contract requires for one.
	FaultPartial Fault = "partial-read-reported-as-success"
)

// A FaultError reports a CONNECTOR CONTRACT VIOLATION, attributed by name.
//
// It is deliberately a distinct type rather than a generic error: coercing a
// broken connector's return into an ordinary failure would hide the breakage
// behind behaviour that looks correct, and the operator would go looking for
// the fault in the code host, where it is not.
//
// Its Kind is cerr.KindContractViolation, so a host that only classifies —
// cerr.KindOf, cerr.Retryable, cerr.TripsBreaker — still gets the right
// answers: not retryable (a broken connector does not improve), and not
// breaker-tripping (this is not backend load).
type FaultError struct {
	// Connector is the host's name for the connector at fault. It is empty
	// when the fault was raised inside the connector itself, which does not
	// know what it was registered as; [CheckResolution] fills it in
	// host-side.
	Connector string
	// Op is the contract operation, e.g. "ListPRs".
	Op string
	// Fault is which contract violation occurred.
	Fault Fault
	// Detail is optional context.
	Detail string
}

func (e *FaultError) Error() string {
	who := "an unnamed connector"
	if e.Connector != "" {
		who = "connector " + strconv.Quote(e.Connector)
	}
	op := e.Op
	if op == "" {
		op = "a contract operation"
	}
	msg := fmt.Sprintf("%s violated the CodeCI contract in %s: %s", who, op, e.Fault)
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

// Unwrap exposes the fault to the cerr taxonomy, so classification-only host
// code sees cerr.KindContractViolation without knowing this type exists.
func (e *FaultError) Unwrap() error { return cerr.New(cerr.KindContractViolation, e.Op, nil) }

// CheckResolution is the host-side guard: it rejects a return the contract
// does not admit, and names the connector that made it.
//
// Pass it whatever a connector returned from a list operation, together with
// the name the host knows that connector by. It returns:
//
//   - the connector's own typed failure, unchanged, when err is non-nil and
//     is not a fault — a connector that failed correctly is not at fault;
//   - a [FaultError] naming connectorName, when the connector reported
//     success with nothing resolved, or raised a fault of its own (which
//     carries no name until here);
//   - the resolution unchanged, when it is conformant — including a
//     deliberately-empty one.
//
// A host that skips this guard is not exposed to a success carrying nothing:
// [Resolution.Items] refuses to read an unresolved resolution regardless.
// What the guard adds is attribution — which connector, in which operation.
func CheckResolution[T any](connectorName, op string, r Resolution[T], err error) (Resolution[T], error) {
	if err != nil {
		return Resolution[T]{}, attribute(connectorName, op, err)
	}
	if !r.present() {
		return Resolution[T]{}, &FaultError{
			Connector: connectorName,
			Op:        op,
			Fault:     FaultUnresolved,
			Detail:    "returned no list and no error; an unresolved result is a failure, never an empty one",
		}
	}
	return r, nil
}

// attribute names the connector on a fault that was raised without one, and
// leaves every other error alone. It returns a named copy rather than
// mutating the caller's error.
func attribute(connectorName, op string, err error) error {
	var fe *FaultError
	if !errors.As(err, &fe) || fe.Connector != "" {
		return err
	}
	named := *fe
	named.Connector = connectorName
	if named.Op == "" {
		named.Op = op
	}
	return &named
}
