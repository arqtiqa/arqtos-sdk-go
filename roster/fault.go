package roster

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// A Fault names a way a connector can break the Roster contract: not a failure
// it reports (those are cerr Kinds, chosen by the connector), but a return the
// contract does not admit at all.
//
// The set is closed for the same reason the failure vocabulary is: a host acts
// on the name, and adding one is a change to a published contract.
type Fault string

const (
	// FaultUnresolved is a connector reporting success while carrying no
	// list — the (empty list, no error) shape this contract exists to
	// eliminate.
	FaultUnresolved Fault = "unresolved-without-error"
	// FaultMembershipMismatch is a membership list containing an entry for a
	// group other than the one that was asked about. A host cannot attribute
	// such an entry, and attributing it to the group it asked about is how
	// people end up in groups they are not in.
	FaultMembershipMismatch Fault = "membership-is-for-another-group"
	// FaultUndeclaredMachinePrincipal is a machine principal reported by a
	// connector that does not declare [CapMachinePrincipals]. The
	// declaration is what makes the ABSENCE of machine principals readable —
	// without it a host cannot tell "this directory has no service accounts"
	// from "this connector cannot see them" — so reporting one undeclared
	// makes both readings wrong at once.
	FaultUndeclaredMachinePrincipal Fault = "machine-principal-without-the-capability"
	// FaultUndeclaredInheritedMembership is an inherited membership
	// ([Membership.Direct] false) reported by a connector that does not
	// declare [CapTransitiveMembership], for the same reason.
	FaultUndeclaredInheritedMembership Fault = "inherited-membership-without-the-capability"
)

// A FaultError reports a CONNECTOR CONTRACT VIOLATION, attributed by name.
//
// It is deliberately a distinct type rather than a generic error: coercing a
// broken connector's return into an ordinary failure would hide the breakage
// behind behaviour that looks correct, and the operator would go looking for
// the fault in the directory, where it is not.
//
// Its Kind is cerr.KindContractViolation, so a host that only classifies —
// cerr.KindOf, cerr.Retryable, cerr.TripsBreaker — still gets the right
// answers: not retryable (a broken connector does not improve), and not
// breaker-tripping (this is not backend load).
type FaultError struct {
	// Connector is the host's name for the connector at fault. It is empty
	// when the fault was raised inside the connector itself, which does not
	// know what it was registered as; [CheckResolution] and [CheckMemberships]
	// fill it in host-side.
	Connector string
	// Op is the contract operation, e.g. "ListPrincipals".
	Op string
	// Fault is which contract violation occurred.
	Fault Fault
	// Detail is optional context. It never carries directory records.
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
	msg := fmt.Sprintf("%s violated the Roster contract in %s: %s", who, op, e.Fault)
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
//   - the connector's own typed failure, unchanged, when err is non-nil and is
//     not a fault — a connector that failed correctly is not at fault;
//   - a [FaultError] naming connectorName, when the connector reported success
//     with nothing resolved, or raised a fault of its own (which carries no
//     name until here);
//   - the resolution unchanged, when it is conformant — including a
//     deliberately-empty one.
//
// A host that skips this guard is not exposed to a roster of nobody:
// [Resolution.Items] refuses to read an unresolved resolution regardless. What
// the guard adds is attribution — which connector, in which operation.
func CheckResolution[T any](connectorName, op string, r Resolution[T], err error) (Resolution[T], error) {
	if err != nil {
		return Resolution[T]{}, attribute(connectorName, op, err)
	}
	if !r.present() {
		return Resolution[T]{}, &FaultError{
			Connector: connectorName,
			Op:        op,
			Fault:     FaultUnresolved,
			Detail: "returned no list and no error; an unresolved roster is a failure, never a roster of nobody, " +
				"and a sweep over the second reading revokes access for everyone",
		}
	}
	return r, nil
}

// CheckPrincipals is [CheckResolution] for a principal list, plus the one
// check that needs the connector's capabilities: a machine principal may be
// reported only by a connector that declares [CapMachinePrincipals].
//
// caps is what the running connector reports from Capabilities(). Pass it
// rather than the manifest's list: this guard is about what the connector
// actually just did, and the manifest is checked against the connector
// separately.
func CheckPrincipals(connectorName string, caps connector.Capabilities, r Resolution[Principal], err error) (Resolution[Principal], error) {
	const op = "ListPrincipals"
	checked, checkErr := CheckResolution(connectorName, op, r, err)
	if checkErr != nil {
		return Resolution[Principal]{}, checkErr
	}
	if caps.Has(CapMachinePrincipals) {
		return checked, nil
	}
	// entries rather than Items: CheckResolution has already proved presence,
	// so there is no error left to handle, and this guard only reads.
	for i, p := range checked.entries() {
		if p.Kind == PrincipalMachine {
			return Resolution[Principal]{}, &FaultError{
				Connector: connectorName,
				Op:        op,
				Fault:     FaultUndeclaredMachinePrincipal,
				Detail: fmt.Sprintf("principal %d is %s, and %s is not declared; a host reads the absence of machine "+
					"principals as a fact about the directory only when the connector has said it can see them",
					i, PrincipalMachine, CapMachinePrincipals),
			}
		}
	}
	return checked, nil
}

// CheckMemberships is [CheckResolution] for a membership list, plus the two
// checks that need context: every entry must be for the group that was asked
// about, and an inherited membership may be reported only by a connector that
// declares [CapTransitiveMembership].
//
// groupID is the group the host asked about. caps is what the running connector
// reports from Capabilities().
func CheckMemberships(connectorName, groupID string, caps connector.Capabilities, r Resolution[Membership], err error) (Resolution[Membership], error) {
	const op = "ListMemberships"
	checked, checkErr := CheckResolution(connectorName, op, r, err)
	if checkErr != nil {
		return Resolution[Membership]{}, checkErr
	}
	transitive := caps.Has(CapTransitiveMembership)
	for i, m := range checked.entries() {
		if m.GroupID != groupID {
			return Resolution[Membership]{}, &FaultError{
				Connector: connectorName,
				Op:        op,
				Fault:     FaultMembershipMismatch,
				Detail: fmt.Sprintf("entry %d is for group %s, but %s was requested",
					i, strconv.Quote(m.GroupID), strconv.Quote(groupID)),
			}
		}
		if !m.Direct && !transitive {
			return Resolution[Membership]{}, &FaultError{
				Connector: connectorName,
				Op:        op,
				Fault:     FaultUndeclaredInheritedMembership,
				Detail: fmt.Sprintf("entry %d is inherited, and %s is not declared; a host reads a membership list "+
					"without inherited entries as flat only when the connector has said it can express nesting",
					i, CapTransitiveMembership),
			}
		}
	}
	return checked, nil
}

// attribute names the connector on a fault that was raised without one, and
// leaves every other error alone. It returns a named copy rather than mutating
// the caller's error.
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
