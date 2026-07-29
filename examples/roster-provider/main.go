// Command roster-provider is a vendor-free, out-of-process (Track-B) reference
// implementation of the roster.Roster connector class. It serves a fixed,
// PLACEHOLDER-only directory over hashicorp/go-plugin's gRPC transport, using
// nothing from this module beyond the public
// roster/cerr/connector/plugin packages — no vendor SDK.
//
// This file is the template a connector author copies to start a real provider
// (Okta, Entra, Google Workspace, a code host's teams, a flat file): swap
// memRoster's fields and method bodies for calls to the actual directory API;
// the plugin.Handshake + plugin.RosterPluginMap(...) + goplugin.Serve wiring in
// main() does not change.
//
// # The three things worth copying, beyond the wiring
//
//   - Every list goes back through roster.Resolved or roster.EmptyRoster, so a
//     read that produced nothing CANNOT be reported as a directory with nobody
//     in it. That is the whole point of the class: the host operation
//     downstream of a principal list removes access, so an empty list that
//     meant "the read failed" deprovisions the estate.
//
//   - The SUSPENDED principal is reported, with Active false — not omitted.
//     Omitting a deactivated identity tells the host the person left the
//     organisation, and the host then revokes everything belonging to somebody
//     who is on leave.
//
//   - A group that does not exist is cerr.KindNotFound, and a group that
//     exists with no members is roster.EmptyRoster. Two different answers,
//     because a reconcile loop draws opposite conclusions from them.
//
// It also declares and implements two capabilities beyond the baseline
// (transitive_membership and machine_principals) so a copier sees a capability
// wired correctly end to end. It deliberately does NOT declare watch: watch has
// no RPC on the Track-B wire, so an out-of-process provider cannot declare it
// and is polled — see plugin/roster.go.
//
// See conform_test.go in this directory for the in-process conformance run,
// and roundtrip_test.go for a real-subprocess round trip driving this exact
// binary the way a host does.
//
// No real directory data: every identifier below is a placeholder, and no
// value here names anybody.
package main

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// The fixture identifiers the tests in this directory drive this provider
// with. They live here, rather than duplicated in the tests, so the served
// directory and the conformance fixtures can never drift apart.
const (
	// referenceGroup is populated, and has an INHERITED member — which is what
	// makes the transitive_membership declaration checkable rather than merely
	// stated.
	referenceGroup = "placeholder-group-engineering"
	// referenceNestedGroup is nested inside referenceGroup; its direct member
	// is referenceGroup's inherited one.
	referenceNestedGroup = "placeholder-group-platform"
	// referenceParentGroup is the group referenceGroup is nested inside.
	referenceParentGroup = "placeholder-group-all"
	// referenceEmptyGroup EXISTS and has no members — a real state, and not
	// the same answer as a group that does not exist.
	referenceEmptyGroup = "placeholder-group-just-created"
	// referenceAbsentGroup is served by nothing: it must fail NotFound.
	referenceAbsentGroup = "placeholder-group-no-such-thing"

	referenceActive    = "placeholder-principal-active"
	referenceSuspended = "placeholder-principal-suspended"
	referenceMachine   = "placeholder-principal-machine"

	// referenceMinHostVersion is what this provider's manifest declares. A
	// host older than this is refused at dial time — see
	// manifest.Doc.RequireHost.
	referenceMinHostVersion = "0.1.0"
)

// memRoster is the reference Roster: a fixed, in-memory placeholder directory
// standing in for whatever a real provider's backing directory would be. A real
// provider replaces the method bodies with calls to its own API — and keeps the
// roster.Resolved / roster.EmptyRoster / cerr.New shapes exactly as they are.
type memRoster struct{}

// ListPrincipals is the shape a real provider copies. Three things are
// load-bearing:
//
//   - the SUSPENDED principal is present, with Active false;
//   - the list goes back through roster.Resolved, which refuses to report a
//     success carrying no list, so a directory read that came back empty
//     because it failed surfaces as a fault rather than as a directory with
//     nobody in it — without this function containing a single emptiness check;
//   - roster.Complete is ASSERTED. A real provider paginating a directory of
//     any size must not assert it for a loop that broke off partway: return a
//     typed failure instead (cerr.KindUnavailable, cerr.KindTimeout, ...). A
//     truncated principal list is readable, non-empty and indistinguishable
//     from a complete one to everything downstream, and a sweep run against
//     one revokes everyone past the failure point.
func (m *memRoster) ListPrincipals(_ context.Context) (roster.Resolution[roster.Principal], error) {
	return roster.Resolved([]roster.Principal{
		{
			ID: referenceActive, Handle: "placeholder-active",
			Email: "placeholder-active@example.invalid", DisplayName: "Placeholder Active",
			Active: true, Kind: roster.PrincipalHuman,
		},
		{
			// Deactivated, and STILL IN THE DIRECTORY.
			ID: referenceSuspended, Handle: "placeholder-suspended",
			Email: "placeholder-suspended@example.invalid", DisplayName: "Placeholder Suspended",
			Active: false, Kind: roster.PrincipalHuman,
		},
		{
			// A machine identity: no mailbox, and that is not an error.
			// Reporting one requires roster.CapMachinePrincipals, declared in
			// Capabilities below and in this provider's manifest.
			ID: referenceMachine, Handle: "placeholder-service-identity",
			DisplayName: "Placeholder Service Identity",
			Active:      true, Kind: roster.PrincipalMachine,
		},
	}, roster.Complete)
}

func (m *memRoster) ListGroups(_ context.Context) (roster.Resolution[roster.Group], error) {
	return roster.Resolved([]roster.Group{
		{
			ID: referenceGroup, Handle: "placeholder-engineering", DisplayName: "Placeholder Engineering",
			ParentIDs: []string{referenceParentGroup},
		},
		{ID: referenceParentGroup, Handle: "placeholder-all", DisplayName: "Placeholder All"},
		{
			ID: referenceNestedGroup, Handle: "placeholder-platform", DisplayName: "Placeholder Platform",
			ParentIDs: []string{referenceGroup},
		},
		{ID: referenceEmptyGroup, Handle: "placeholder-just-created", DisplayName: "Placeholder Just Created"},
	}, roster.Complete)
}

// ListMemberships is where the two answers that must never be confused live
// side by side: an existing group with no members is roster.EmptyRoster, and a
// group that does not exist is cerr.KindNotFound.
//
// Every returned Membership.GroupID equals the requested groupID. A host
// refuses the whole list otherwise, and it is right to: it cannot attribute an
// entry for a group it did not ask about.
func (m *memRoster) ListMemberships(_ context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	switch groupID {
	case referenceGroup:
		return roster.Resolved([]roster.Membership{
			{PrincipalID: referenceActive, GroupID: referenceGroup, Direct: true},
			{PrincipalID: referenceSuspended, GroupID: referenceGroup, Direct: true},
			{
				// INHERITED, through referenceNestedGroup's nesting. Reporting
				// one requires roster.CapTransitiveMembership.
				PrincipalID: referenceMachine, GroupID: referenceGroup, Direct: false,
			},
		}, roster.Complete)
	case referenceNestedGroup:
		return roster.Resolved([]roster.Membership{
			{PrincipalID: referenceMachine, GroupID: referenceNestedGroup, Direct: true},
		}, roster.Complete)
	case referenceParentGroup:
		return roster.Resolved([]roster.Membership{
			{PrincipalID: referenceActive, GroupID: referenceParentGroup, Direct: false},
		}, roster.Complete)
	case referenceEmptyGroup:
		// This backend can tell "read successfully, found nobody" from "could
		// not read", so it may assert emptiness. One that cannot has a
		// failure on its hands, not an empty group.
		return roster.EmptyRoster[roster.Membership](), nil
	default:
		// Not an empty roster. A reconcile loop given one for a group that
		// does not exist removes the access that group carried.
		return roster.Resolution[roster.Membership]{}, cerr.New(cerr.KindNotFound, "ListMemberships", nil)
	}
}

func (m *memRoster) Implements() connector.Class { return connector.ClassRoster }

// Capabilities declares the two capabilities this provider actually
// implements, matching its manifest. A capability declared but not reported —
// or reported but not declared — fails rosterconform in either direction; see
// conform_test.go.
//
// roster.CapWatch is deliberately absent: it has no RPC on the Track-B wire,
// so an out-of-process provider cannot honour it and must be polled.
func (m *memRoster) Capabilities() connector.Capabilities {
	return connector.Capabilities{roster.CapTransitiveMembership, roster.CapMachinePrincipals}
}

func (m *memRoster) Health(_ context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy, Detail: "placeholder directory, always reachable"}, nil
}

// Close releases what the provider holds. A real provider closes its directory
// API session here; it is called by the host over the wire, and it is
// idempotent.
func (m *memRoster) Close() error { return nil }

var _ roster.Roster = (*memRoster)(nil)

func main() {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins:         plugin.RosterPluginMap(&memRoster{}),
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}
