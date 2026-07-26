package credential

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// A Fault names a way a connector can break the CredentialLoader contract:
// not a failure it reports (those are cerr Kinds, chosen by the connector),
// but a return the contract does not admit at all.
//
// The set is closed for the same reason the failure vocabulary is: a host
// acts on the name, and adding one is a change to a published contract.
type Fault string

const (
	// FaultUnresolved is a connector reporting success while carrying no
	// value — the shape REQ-ARQ-P-17 exists to eliminate.
	FaultUnresolved Fault = "unresolved-without-error"
	// FaultBatchMismatch is a batch whose results do not correspond to the
	// references that were requested: a different count, or a different
	// order. A host cannot attribute such results to the refs it asked
	// about, and guessing the correspondence is how the wrong secret reaches
	// the wrong caller.
	FaultBatchMismatch Fault = "batch-results-do-not-match-request"
)

// A FaultError reports a CONNECTOR CONTRACT VIOLATION, attributed by name.
//
// It is deliberately a distinct type rather than a generic error: coercing a
// broken connector's return into an ordinary failure would hide the breakage
// behind behaviour that looks correct, and the operator would go looking for
// the fault in the backend, where it is not.
//
// Its Kind is cerr.KindContractViolation, so a host that only classifies —
// cerr.KindOf, cerr.Retryable, cerr.TripsBreaker — still gets the right
// answers: not retryable (a broken connector does not improve), and not
// breaker-tripping (this is not backend load).
type FaultError struct {
	// Connector is the host's name for the connector at fault. It is empty
	// when the fault was raised inside the connector itself, which does not
	// know what it was registered as; [CheckResolution] and [CheckBatch] fill
	// it in host-side.
	Connector string
	// Op is the contract operation, e.g. "Resolve".
	Op string
	// Fault is which contract violation occurred.
	Fault Fault
	// Detail is optional context. It never carries secret material.
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
	msg := fmt.Sprintf("%s violated the CredentialLoader contract in %s: %s", who, op, e.Fault)
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

// Unwrap exposes the fault to the cerr taxonomy, so classification-only host
// code sees cerr.KindContractViolation without knowing this type exists.
func (e *FaultError) Unwrap() error { return cerr.New(cerr.KindContractViolation, e.Op, nil) }

// CheckResolution is the host-side guard REQ-ARQ-P-17 requires: it rejects a
// return the contract does not admit, and names the connector that made it.
//
// Pass it whatever a connector returned from Resolve or Lease, together with
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
// A host that skips this guard is not exposed to an empty credential:
// [Resolution.Value] refuses to read an unresolved resolution regardless.
// What the guard adds is attribution — which connector, in which operation.
func CheckResolution(connectorName, op string, r Resolution, err error) (Resolution, error) {
	if err != nil {
		return Resolution{}, attribute(connectorName, op, err)
	}
	if !r.present() {
		return Resolution{}, &FaultError{
			Connector: connectorName,
			Op:        op,
			Fault:     FaultUnresolved,
			Detail:    "returned no value and no error; an unresolved credential is a failure, never an empty one",
		}
	}
	return r, nil
}

// CheckBatch is [CheckResolution] for a batch: it verifies that what came
// back corresponds to what was asked for, and that no element is a silent
// non-resolution.
//
// results must have one entry per requested reference, in the requested
// order, each carrying either a readable resolution or a failure. err is the
// whole-call failure and is returned unchanged when set (a batch call that
// failed outright is not a contract violation).
func CheckBatch(connectorName string, refs []ref.Ref, results []BatchResult, err error) ([]BatchResult, error) {
	const op = "ResolveBatch"
	if err != nil {
		return nil, attribute(connectorName, op, err)
	}
	if len(results) != len(refs) {
		return nil, &FaultError{
			Connector: connectorName,
			Op:        op,
			Fault:     FaultBatchMismatch,
			Detail:    fmt.Sprintf("returned %d result(s) for %d reference(s)", len(results), len(refs)),
		}
	}
	for i, got := range results {
		if got.Ref != refs[i] {
			return nil, &FaultError{
				Connector: connectorName,
				Op:        op,
				Fault:     FaultBatchMismatch,
				Detail:    fmt.Sprintf("result %d is for %s, but %s was requested at that position", i, got.Ref, refs[i]),
			}
		}
		if got.Err != nil {
			continue
		}
		if !got.Resolution.present() {
			return nil, &FaultError{
				Connector: connectorName,
				Op:        op,
				Fault:     FaultUnresolved,
				Detail:    fmt.Sprintf("result %d (%s) carries neither a value nor a failure", i, got.Ref),
			}
		}
	}
	return results, nil
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
