package roster_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

const connectorName = "placeholder-roster"

func member(principalID string, direct bool) roster.Membership {
	return roster.Membership{PrincipalID: principalID, GroupID: idGroup, Direct: direct}
}

func mustResolve[T any](t *testing.T, items []T) roster.Resolution[T] {
	t.Helper()
	res, err := roster.Resolved(items)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	return res
}

// TestCheckResolutionRejectsAnUnresolvedSuccessAndNamesTheConnector is the
// host-side half of REQ-ARQ-P-17. Resolution.Items already refuses to read an
// unresolved roster, so the guard is not what keeps an empty list out of the
// host; what it adds is ATTRIBUTION — which connector, in which operation —
// so an operator does not go looking for the fault in the directory.
func TestCheckResolutionRejectsAnUnresolvedSuccessAndNamesTheConnector(t *testing.T) {
	_, err := roster.CheckResolution(connectorName, "ListPrincipals", roster.Resolution[roster.Principal]{}, nil)
	if err == nil {
		t.Fatalf("a success carrying no list must be refused")
	}
	var fe *roster.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *roster.FaultError", err)
	}
	if fe.Connector != connectorName || fe.Op != "ListPrincipals" {
		t.Fatalf("the fault must name the connector and the operation, got %+v", fe)
	}
	if fe.Fault != roster.FaultUnresolved {
		t.Fatalf("Fault = %q", fe.Fault)
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
	}
	if !strings.Contains(err.Error(), connectorName) {
		t.Fatalf("the message must carry the connector name for an operator reading a log: %v", err)
	}
}

// TestCheckResolutionPassesAConnectorsOwnTypedFailureThrough: a connector that
// failed CORRECTLY is not at fault, and rewriting its classification would
// destroy the only thing the host acts on.
func TestCheckResolutionPassesAConnectorsOwnTypedFailureThrough(t *testing.T) {
	own := cerr.New(cerr.KindUnauthorized, "ListPrincipals", nil)
	_, err := roster.CheckResolution(connectorName, "ListPrincipals", roster.Resolution[roster.Principal]{}, own)
	if !errors.Is(err, own) {
		t.Fatalf("the connector's own failure must pass through unchanged, got %v", err)
	}
	if cerr.KindOf(err) != cerr.KindUnauthorized {
		t.Fatalf("KindOf = %v, want KindUnauthorized", cerr.KindOf(err))
	}
	var fe *roster.FaultError
	if errors.As(err, &fe) {
		t.Fatalf("an honest failure must not be reported as a contract violation")
	}
}

// TestCheckResolutionNamesAnUnnamedFaultRaisedInsideTheConnector: a fault
// raised by Resolved inside the connector carries no name, because the
// connector does not know what it was registered as. The guard fills it in
// without mutating the caller's error.
func TestCheckResolutionNamesAnUnnamedFaultRaisedInsideTheConnector(t *testing.T) {
	_, inner := roster.Resolved[roster.Principal](nil)
	var innerFault *roster.FaultError
	if !errors.As(inner, &innerFault) {
		t.Fatalf("Resolved must raise a FaultError")
	}
	if innerFault.Connector != "" {
		t.Fatalf("a fault raised inside a connector cannot know the connector's name")
	}

	_, err := roster.CheckResolution(connectorName, "ListGroups", roster.Resolution[roster.Principal]{}, inner)
	var named *roster.FaultError
	if !errors.As(err, &named) {
		t.Fatalf("error is %T, want *roster.FaultError", err)
	}
	if named.Connector != connectorName {
		t.Fatalf("Connector = %q, want %q", named.Connector, connectorName)
	}
	if innerFault.Connector != "" {
		t.Fatalf("the guard mutated the caller's error instead of returning a named copy")
	}
	// Op was already set by the constructor, so it is preserved rather than
	// overwritten with the guard's own op.
	if named.Op != "List" {
		t.Fatalf("Op = %q, want the constructor's own op to survive", named.Op)
	}
}

// TestAnAlreadyAttributedFaultIsNotRenamed: a fault that already names a
// connector has been through a guard once — re-attributing it at a second layer
// would blame whichever component happened to run the guard last, and the whole
// value of the fault type is that it points at the connector actually at fault.
func TestAnAlreadyAttributedFaultIsNotRenamed(t *testing.T) {
	original := &roster.FaultError{
		Connector: "placeholder-inner-roster",
		Op:        "ListPrincipals",
		Fault:     roster.FaultUnresolved,
	}
	_, err := roster.CheckResolution("placeholder-outer-wrapper", "ListGroups",
		roster.Resolution[roster.Principal]{}, original)
	var got *roster.FaultError
	if !errors.As(err, &got) {
		t.Fatalf("error is %T, want *roster.FaultError", err)
	}
	if got.Connector != "placeholder-inner-roster" {
		t.Fatalf("Connector = %q; an already-attributed fault must keep its original name", got.Connector)
	}
	if got.Op != "ListPrincipals" {
		t.Fatalf("Op = %q; the original operation must survive", got.Op)
	}

	// The converse: a fault carrying NEITHER name nor operation gets both from
	// the guard. Without the operation an author is told a contract was broken
	// and not which call broke it, which is a bug report nobody can act on.
	bare := &roster.FaultError{Fault: roster.FaultUnresolved}
	_, err = roster.CheckResolution(connectorName, "ListGroups", roster.Resolution[roster.Group]{}, bare)
	var filled *roster.FaultError
	if !errors.As(err, &filled) {
		t.Fatalf("error is %T, want *roster.FaultError", err)
	}
	if filled.Connector != connectorName || filled.Op != "ListGroups" {
		t.Fatalf("an unattributed fault must gain both a connector and an operation, got %+v", filled)
	}
	if bare.Op != "" || bare.Connector != "" {
		t.Fatalf("the guard mutated the caller's error instead of returning a named copy: %+v", bare)
	}
}

// TestCheckResolutionAcceptsAnAssertedEmptyRoster is the guard on the guard.
// Refusing an unresolved roster must not become refusing an empty DIRECTORY: a
// group that genuinely has no members is a real state, and a host has to be
// able to see it in order to remove the access that group carried.
func TestCheckResolutionAcceptsAnAssertedEmptyRoster(t *testing.T) {
	res, err := roster.CheckResolution(connectorName, "ListMemberships", roster.EmptyRoster[roster.Membership](), nil)
	if err != nil {
		t.Fatalf("the host guard must accept a genuinely empty directory: %v", err)
	}
	items, err := res.Items()
	if err != nil || len(items) != 0 {
		t.Fatalf("an asserted-empty roster must read as an empty list, got %v / %v", items, err)
	}
}

// TestCheckMembershipsRejectsAnEntryForAnotherGroup: a host cannot attribute a
// membership for a group it did not ask about, and attributing it to the group
// it DID ask about is how people end up in groups they are not in. This is the
// membership analogue of the batch-correspondence guard in the credential
// class.
func TestCheckMembershipsRejectsAnEntryForAnotherGroup(t *testing.T) {
	res := mustResolve(t, []roster.Membership{
		member(idPersonA, true),
		{PrincipalID: idPersonB, GroupID: "placeholder-other-group", Direct: true},
	})
	_, err := roster.CheckMemberships(connectorName, idGroup, nil, res, nil)
	if err == nil {
		t.Fatalf("a membership for another group must be refused")
	}
	var fe *roster.FaultError
	if !errors.As(err, &fe) || fe.Fault != roster.FaultMembershipMismatch {
		t.Fatalf("error = %v, want a FaultError of %q", err, roster.FaultMembershipMismatch)
	}
	if !strings.Contains(fe.Detail, "placeholder-other-group") || !strings.Contains(fe.Detail, idGroup) {
		t.Fatalf("the fault must name both the group returned and the group requested, got: %s", fe.Detail)
	}
}

// TestCheckMembershipsRejectsAnInheritedEntryFromAConnectorThatDoesNotDeclareNesting
// is REQ-ARQ-P-20 enforced at the data rather than at the declaration. The
// declaration is what makes the ABSENCE of inherited memberships readable: a
// host reads a flat list as a fact about the directory only when the connector
// has said it can express nesting, so an undeclared inherited entry makes both
// readings wrong at once.
func TestCheckMembershipsRejectsAnInheritedEntryFromAConnectorThatDoesNotDeclareNesting(t *testing.T) {
	res := mustResolve(t, []roster.Membership{member(idPersonA, true), member(idPersonB, false)})

	_, err := roster.CheckMemberships(connectorName, idGroup, connector.Capabilities{roster.CapWatch}, res, nil)
	var fe *roster.FaultError
	if !errors.As(err, &fe) || fe.Fault != roster.FaultUndeclaredInheritedMembership {
		t.Fatalf("error = %v, want a FaultError of %q", err, roster.FaultUndeclaredInheritedMembership)
	}

	// Declared, and the same data is conformant.
	if _, err := roster.CheckMemberships(connectorName, idGroup,
		connector.Capabilities{roster.CapTransitiveMembership}, res, nil); err != nil {
		t.Fatalf("a declared transitive connector may report inherited memberships: %v", err)
	}
}

// TestCheckPrincipalsRejectsAMachinePrincipalFromAConnectorThatDoesNotDeclareThem
// is the same rule for non-human identities. One vendor's service applications
// are directory objects; another's service accounts live in a separate
// cloud-IAM system and a directory read cannot see them at all. A host reads
// "no machine principals" as a fact about the directory only when the connector
// has declared it can see them.
func TestCheckPrincipalsRejectsAMachinePrincipalFromAConnectorThatDoesNotDeclareThem(t *testing.T) {
	bot := roster.Principal{ID: idMachine, Handle: idMachine, Active: true, Kind: roster.PrincipalMachine}
	res := mustResolve(t, []roster.Principal{person(idPersonA), bot})

	_, err := roster.CheckPrincipals(connectorName, connector.Capabilities{roster.CapWatch}, res, nil)
	var fe *roster.FaultError
	if !errors.As(err, &fe) || fe.Fault != roster.FaultUndeclaredMachinePrincipal {
		t.Fatalf("error = %v, want a FaultError of %q", err, roster.FaultUndeclaredMachinePrincipal)
	}
	if !strings.Contains(fe.Detail, string(roster.CapMachinePrincipals)) {
		t.Fatalf("the fault must name the capability the author should have declared, got: %s", fe.Detail)
	}

	if _, err := roster.CheckPrincipals(connectorName,
		connector.Capabilities{roster.CapMachinePrincipals}, res, nil); err != nil {
		t.Fatalf("a declared connector may report machine principals: %v", err)
	}
}

// TestCheckPrincipalsAndCheckMembershipsStillRefuseAnUnresolvedSuccess: the
// capability-aware guards must not have lost the property they wrap. Each one
// composes CheckResolution rather than reimplementing it, and this is what
// proves the composition is wired up.
func TestCheckPrincipalsAndCheckMembershipsStillRefuseAnUnresolvedSuccess(t *testing.T) {
	if _, err := roster.CheckPrincipals(connectorName, nil, roster.Resolution[roster.Principal]{}, nil); err == nil {
		t.Fatalf("CheckPrincipals must refuse a success carrying no list")
	}
	if _, err := roster.CheckMemberships(connectorName, idGroup, nil, roster.Resolution[roster.Membership]{}, nil); err == nil {
		t.Fatalf("CheckMemberships must refuse a success carrying no list")
	}
	own := cerr.New(cerr.KindNotFound, "ListMemberships", nil)
	if _, err := roster.CheckMemberships(connectorName, idNoGroup, nil, roster.Resolution[roster.Membership]{}, own); !errors.Is(err, own) {
		t.Fatalf("a typed not-found must pass through unchanged, got %v", err)
	}
	// An asserted-empty membership list is conformant for both guards.
	if _, err := roster.CheckMemberships(connectorName, idGroup, nil, roster.EmptyRoster[roster.Membership](), nil); err != nil {
		t.Fatalf("an empty group is a real state: %v", err)
	}
	if _, err := roster.CheckPrincipals(connectorName, nil, roster.EmptyRoster[roster.Principal](), nil); err != nil {
		t.Fatalf("an empty directory is a real state: %v", err)
	}
}

// TestFaultErrorMessageIsReadableWithoutAName covers the shape a fault has
// before a host attributes it — raised inside a connector that does not know
// what it was registered as.
func TestFaultErrorMessageIsReadableWithoutAName(t *testing.T) {
	fe := &roster.FaultError{Fault: roster.FaultUnresolved}
	msg := fe.Error()
	for _, want := range []string{"unnamed connector", "a contract operation", string(roster.FaultUnresolved)} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q is missing %q", msg, want)
		}
	}
	if strings.Contains(msg, "()") {
		t.Fatalf("an empty Detail must not render as empty parentheses: %q", msg)
	}
}

// TestFaultsAreNeitherRetryableNorBreakerTripping: the fault is in the
// connector, not in backend load. Retrying a broken connector does not fix it,
// and opening a breaker on it would blame the directory.
func TestFaultsAreNeitherRetryableNorBreakerTripping(t *testing.T) {
	for _, f := range []roster.Fault{
		roster.FaultUnresolved,
		roster.FaultMembershipMismatch,
		roster.FaultUndeclaredMachinePrincipal,
		roster.FaultUndeclaredInheritedMembership,
	} {
		err := error(&roster.FaultError{Connector: connectorName, Op: "ListPrincipals", Fault: f})
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
