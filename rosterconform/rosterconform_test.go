package rosterconform_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
	"github.com/arqtiqa/arqtos-sdk-go/rosterconform"
)

// Placeholder directory identifiers. No estate identifiers: these are shapes,
// not addresses of anybody.
const (
	idPersonA   = "placeholder-principal-a"
	idInherited = "placeholder-principal-inherited"
	idSuspended = "placeholder-principal-suspended"
	idMachine   = "placeholder-principal-machine"
	idGroup     = "placeholder-group"
	idParent    = "placeholder-parent-group"
	idNoGroup   = "placeholder-absent-group"
	name        = "placeholder-roster"
)

func opts(m manifest.Doc) rosterconform.Options {
	return rosterconform.Options{
		Manifest:           m,
		Group:              idGroup,
		AbsentGroup:        idNoGroup,
		SuspendedPrincipal: idSuspended,
	}
}

func manifestFor(caps ...connector.Capability) manifest.Doc {
	return manifest.Doc{
		Name:         name,
		Implements:   connector.ClassRoster,
		Kind:         manifest.KindNative,
		Capabilities: caps,
	}
}

// ---------------------------------------------------------------------------
// The compliant baseline. Every non-compliant fixture below is this connector
// with ONE property broken, so a failing check is attributable to that
// property and not to the fixture being generally broken.
// ---------------------------------------------------------------------------

// baseRoster is a conformant in-memory directory. Its three switches are the
// three declarable capabilities' observable consequences, so a fixture can vary
// what the connector DOES independently of what it DECLARES — which is what
// makes the declaration checks falsifiable.
type baseRoster struct {
	caps          connector.Capabilities
	withMachine   bool
	withInherited bool
}

func (r *baseRoster) directory() []roster.Principal {
	out := []roster.Principal{
		{
			ID: idPersonA, Handle: idPersonA, Email: idPersonA + "@example.invalid",
			DisplayName: "Placeholder A", Active: true, Kind: roster.PrincipalHuman,
		},
		// Suspended, and STILL IN THE DIRECTORY. A connector that drops this
		// entry tells the host the person left.
		{
			ID: idSuspended, Handle: idSuspended, Email: idSuspended + "@example.invalid",
			DisplayName: "Placeholder Suspended", Active: false, Kind: roster.PrincipalHuman,
		},
	}
	if r.withInherited {
		out = append(out, roster.Principal{
			ID: idInherited, Handle: idInherited, DisplayName: "Placeholder Inherited",
			Active: true, Kind: roster.PrincipalHuman,
		})
	}
	if r.withMachine {
		// No mailbox: a machine identity frequently has none, and that is not
		// an error.
		out = append(out, roster.Principal{
			ID: idMachine, Handle: idMachine, DisplayName: "Placeholder Service Identity",
			Active: true, Kind: roster.PrincipalMachine,
		})
	}
	return out
}

func (r *baseRoster) groupMembers() []roster.Membership {
	out := []roster.Membership{
		{PrincipalID: idPersonA, GroupID: idGroup, Direct: true},
		{PrincipalID: idSuspended, GroupID: idGroup, Direct: true},
	}
	if r.withInherited {
		out = append(out, roster.Membership{PrincipalID: idInherited, GroupID: idGroup, Direct: false})
	}
	return out
}

func (r *baseRoster) ListPrincipals(context.Context) (roster.Resolution[roster.Principal], error) {
	return roster.Resolved(r.directory(), roster.Complete)
}

func (r *baseRoster) ListGroups(context.Context) (roster.Resolution[roster.Group], error) {
	return roster.Resolved([]roster.Group{
		{ID: idGroup, Handle: idGroup, DisplayName: "Placeholder Group", ParentIDs: []string{idParent}},
		{ID: idParent, Handle: idParent, DisplayName: "Placeholder Parent Group"},
	}, roster.Complete)
}

func (r *baseRoster) ListMemberships(_ context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	switch groupID {
	case idGroup:
		return roster.Resolved(r.groupMembers(), roster.Complete)
	case idParent:
		return roster.EmptyRoster[roster.Membership](), nil
	default:
		return roster.Resolution[roster.Membership]{}, cerr.New(cerr.KindNotFound, "ListMemberships", nil)
	}
}

func (r *baseRoster) Implements() connector.Class { return connector.ClassRoster }

func (r *baseRoster) Capabilities() connector.Capabilities { return r.caps }

func (r *baseRoster) Health(context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}

func (r *baseRoster) Close() error { return nil }

var _ roster.Roster = (*baseRoster)(nil)

// watchingRoster is the compliant watch-capable connector: it implements
// roster.Watcher, and its fixtures declare the capability in both places.
type watchingRoster struct{ baseRoster }

func (r *watchingRoster) Watch(ctx context.Context) (<-chan roster.Change, error) {
	ch := make(chan roster.Change)
	go func() {
		defer close(ch)
		select {
		case ch <- roster.Change{Subject: roster.SubjectMembership, ID: idPersonA, GroupID: idGroup}:
		case <-ctx.Done():
		}
		<-ctx.Done()
	}()
	return ch, nil
}

var _ roster.Watcher = (*watchingRoster)(nil)

// compliant builds the fully-conformant connector: every capability declared,
// implemented, and substantiated by the data.
func compliant() *watchingRoster {
	return &watchingRoster{baseRoster{
		caps:          roster.KnownCapabilities(),
		withMachine:   true,
		withInherited: true,
	}}
}

// ---------------------------------------------------------------------------
// The deliberately NON-COMPLIANT connectors. Each violates exactly one
// obligation, and each exists to prove the corresponding check bites. A harness
// only ever run against compliant input proves nothing.
// ---------------------------------------------------------------------------

// unreadDirectoryRoster is REQ-ARQ-P-17's failure with the worst blast radius
// in the SDK: the read produced nothing — unauthenticated, throttled,
// misdirected — and the connector forwards that faithfully as a success
// carrying no list. A host sweeping for departures over this deprovisions the
// entire estate.
type unreadDirectoryRoster struct{ baseRoster }

func (r *unreadDirectoryRoster) ListPrincipals(context.Context) (roster.Resolution[roster.Principal], error) {
	return roster.Resolution[roster.Principal]{}, nil // nothing, and no error
}

// emptyEverythingRoster is the escape hatch a presence-only check cannot see.
// It answers every list with roster.EmptyRoster() — present, readable, nothing
// in it — which is the move an author reaches for when roster.Resolved refuses
// their failing backend: it silences the constructor without reading a
// directory. Nothing else about it is wrong, which is what makes it dangerous.
type emptyEverythingRoster struct{ baseRoster }

func (r *emptyEverythingRoster) ListPrincipals(context.Context) (roster.Resolution[roster.Principal], error) {
	return roster.EmptyRoster[roster.Principal](), nil
}

func (r *emptyEverythingRoster) ListGroups(context.Context) (roster.Resolution[roster.Group], error) {
	return roster.EmptyRoster[roster.Group](), nil
}

func (r *emptyEverythingRoster) ListMemberships(context.Context, string) (roster.Resolution[roster.Membership], error) {
	return roster.EmptyRoster[roster.Membership](), nil
}

// unreadGroupsRoster reads principals correctly and reports an unresolved
// group list as a success. A host with no groups reconciles every group
// membership away.
type unreadGroupsRoster struct{ baseRoster }

func (r *unreadGroupsRoster) ListGroups(context.Context) (roster.Resolution[roster.Group], error) {
	return roster.Resolution[roster.Group]{}, nil
}

// unreadMembershipsRoster is the same failure on the membership path.
type unreadMembershipsRoster struct{ baseRoster }

func (r *unreadMembershipsRoster) ListMemberships(context.Context, string) (roster.Resolution[roster.Membership], error) {
	return roster.Resolution[roster.Membership]{}, nil
}

// emptyGroupsRoster is Finding 2: principals and memberships are REAL, and
// ListGroups reports the directory as having no groups at all — readably.
// A host reconciling group-derived access reads "this directory has no
// groups" and revokes what every group carried.
type emptyGroupsRoster struct{ baseRoster }

func (r *emptyGroupsRoster) ListGroups(context.Context) (roster.Resolution[roster.Group], error) {
	return roster.EmptyRoster[roster.Group](), nil
}

// truncatedNotTouchingTheFixtureRoster is Finding 1's precise shape: a THIRD
// principal beyond the two baseRoster fixtures exists in the full directory,
// the connector's pagination stopped before reaching it, and the connector
// reports what it has — PersonA and the suspended fixture both intact —
// asserted Complete, as though the read had finished. Nothing already checked
// (readability, substance, the suspended check) can tell this from a genuine
// complete read, because the ONE principal it drops is not one any fixture
// nominates. roster.Resolved(items, roster.Complete) cannot be told apart
// from the truth from the OUTSIDE; the type-level fix narrows the mistake to
// a conscious misclassification at the call site rather than an accidental
// one. See truncatedAssertedPartialRoster for the case the fix DOES catch.
type truncatedNotTouchingTheFixtureRoster struct{ baseRoster }

func (r *truncatedNotTouchingTheFixtureRoster) ListPrincipals(context.Context) (roster.Resolution[roster.Principal], error) {
	full := r.directory()
	full = append(full, roster.Principal{
		ID: "placeholder-principal-c", Handle: "placeholder-principal-c", Active: true, Kind: roster.PrincipalHuman,
	})
	return roster.Resolved(full[:2], roster.Complete) // the third principal is silently lost
}

// truncatedAssertedPartialRoster is the same truncation as
// truncatedNotTouchingTheFixtureRoster, but the connector's pagination loop
// notices it stopped early and says so, the way an author following the doc
// is expected to. Saying so is what the fix requires: roster.Resolved
// refuses to build a readable Resolution for a Partial assertion at all, and
// that refusal surfaces through the same conformance checks that already
// treat any other resolution failure as non-green.
type truncatedAssertedPartialRoster struct{ baseRoster }

func (r *truncatedAssertedPartialRoster) ListPrincipals(context.Context) (roster.Resolution[roster.Principal], error) {
	full := r.directory()
	full = append(full, roster.Principal{
		ID: "placeholder-principal-c", Handle: "placeholder-principal-c", Active: true, Kind: roster.PrincipalHuman,
	})
	return roster.Resolved(full[:2], roster.Partial)
}

// stubWatchRoster declares and implements roster.Watcher, but Watch never
// actually establishes anything — the shape an author reaches for as a
// placeholder while a real subscription is still unimplemented. A bare type
// assertion cannot tell this from a working watch.
type stubWatchRoster struct{ baseRoster }

func (r *stubWatchRoster) Watch(context.Context) (<-chan roster.Change, error) {
	return nil, cerr.New(cerr.KindUnsupported, "Watch", nil)
}

// nilChannelWatchRoster is worse: Watch reports success with a nil channel. A
// host that ranges over it blocks forever and never reconciles again.
type nilChannelWatchRoster struct{ baseRoster }

func (r *nilChannelWatchRoster) Watch(context.Context) (<-chan roster.Change, error) {
	return nil, nil
}

// neverClosingWatchRoster establishes a genuine, non-nil channel but never
// closes it, even once its context is cancelled — the contract's documented
// lifecycle broken at the one point a bare type assertion cannot see either.
// A host that never sees this channel close cannot tell a lost watch from
// one still live, and never falls back to its poll.
type neverClosingWatchRoster struct{ baseRoster }

func (r *neverClosingWatchRoster) Watch(context.Context) (<-chan roster.Change, error) {
	return make(chan roster.Change), nil // deliberately never closed
}

// alreadyClosedWatchRoster is the most idiomatic-LOOKING of the three stubs,
// and the one a cancel-then-wait-for-close check cannot tell from a working
// watch: it hands back a channel that is closed before ctx is ever touched.
// Cancelling ctx and then observing the channel closed is consistent with
// EITHER "this channel closes because ctx was cancelled" or "this channel was
// already closed and always will be" — establishesWatch has to observe the
// channel open BEFORE cancelling to tell the two apart at all.
type alreadyClosedWatchRoster struct{ baseRoster }

func (r *alreadyClosedWatchRoster) Watch(context.Context) (<-chan roster.Change, error) {
	ch := make(chan roster.Change)
	close(ch) // "established", and instantly gone. Pushes nothing, ever.
	return ch, nil
}

// droppingSuspendedRoster is the trap most likely to cause real harm. The
// deactivated identity is still in the directory; this connector omits it, the
// host reads "left the organisation", and everything belonging to somebody on
// parental leave is revoked.
type droppingSuspendedRoster struct{ baseRoster }

func (r *droppingSuspendedRoster) ListPrincipals(context.Context) (roster.Resolution[roster.Principal], error) {
	kept := make([]roster.Principal, 0, 2)
	for _, p := range r.directory() {
		if p.Active {
			kept = append(kept, p)
		}
	}
	return roster.Resolved(kept, roster.Complete)
}

// deactivationBlindRoster is the opposite error from the same missing field: it
// reports the suspended identity as active, so a host leaves a disabled account
// holding everything it had.
type deactivationBlindRoster struct{ baseRoster }

func (r *deactivationBlindRoster) ListPrincipals(context.Context) (roster.Resolution[roster.Principal], error) {
	all := r.directory()
	for i := range all {
		all[i].Active = true
	}
	return roster.Resolved(all, roster.Complete)
}

// wrongGroupRoster returns a membership for a group nobody asked about. A host
// that attributes it to the group it DID ask about puts a person in a group
// they are not in.
type wrongGroupRoster struct{ baseRoster }

func (r *wrongGroupRoster) ListMemberships(_ context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	if groupID != idGroup {
		return roster.Resolution[roster.Membership]{}, cerr.New(cerr.KindNotFound, "ListMemberships", nil)
	}
	out := r.groupMembers()
	out[0].GroupID = idParent
	return roster.Resolved(out, roster.Complete)
}

// emptyNominatedGroupRoster reports the nominated group as having no members.
// That is a legitimate state for a real group, and precisely why the run must
// refuse it as a FIXTURE: nothing about correspondence was exercised.
type emptyNominatedGroupRoster struct{ baseRoster }

func (r *emptyNominatedGroupRoster) ListMemberships(_ context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	if groupID != idGroup {
		return roster.Resolution[roster.Membership]{}, cerr.New(cerr.KindNotFound, "ListMemberships", nil)
	}
	return roster.EmptyRoster[roster.Membership](), nil
}

// groupOmittedFromListGroupsRoster is the gap a shape-only membership check
// cannot see: ListMemberships answers correctly for idGroup — real members,
// every one correctly attributed — but ListGroups never reports idGroup as
// existing at all. Every entry names the right group; nothing about the
// SHAPE of the membership list is wrong. Only correlating Options.Group
// against what ListGroups itself returned catches a connector fabricating
// members for a group id its own ListGroups does not know about.
type groupOmittedFromListGroupsRoster struct{ baseRoster }

func (r *groupOmittedFromListGroupsRoster) ListGroups(context.Context) (roster.Resolution[roster.Group], error) {
	return roster.Resolved([]roster.Group{
		{ID: idParent, Handle: idParent, DisplayName: "Placeholder Parent Group"},
	}, roster.Complete) // idGroup, the one Options.Group nominates, is missing
}

// vendorTextRoster is REQ-ARQ-P-19's failure: it fails with the backend's own
// prose, untyped, leaving the host nothing to act on but the message — which is
// precisely the string-matching the typed vocabulary replaces.
type vendorTextRoster struct{ baseRoster }

func (r *vendorTextRoster) ListMemberships(ctx context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	if groupID == idNoGroup {
		return roster.Resolution[roster.Membership]{}, errors.New("group lookup failed: resource not available at this time")
	}
	return r.baseRoster.ListMemberships(ctx, groupID)
}

// answersForEveryGroupRoster finds a group that does not exist. A connector
// that never fails leaves its failure classification untested, which is how a
// harness comes back green having checked nothing.
type answersForEveryGroupRoster struct{ baseRoster }

func (r *answersForEveryGroupRoster) ListMemberships(context.Context, string) (roster.Resolution[roster.Membership], error) {
	return roster.Resolved(r.groupMembers(), roster.Complete)
}

// emptyForAbsentGroupRoster answers a nonexistent group with an ASSERTED empty
// roster. "This group has no members" and "there is no such group" lead a
// reconcile loop to opposite conclusions, and the first one removes the access
// the group carried.
type emptyForAbsentGroupRoster struct{ baseRoster }

func (r *emptyForAbsentGroupRoster) ListMemberships(ctx context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	if groupID == idNoGroup {
		return roster.EmptyRoster[roster.Membership](), nil
	}
	return r.baseRoster.ListMemberships(ctx, groupID)
}

// unresolvedForAbsentGroupRoster answers a nonexistent group with neither a
// list nor an error — the shape an unauthenticated read produces.
type unresolvedForAbsentGroupRoster struct{ baseRoster }

func (r *unresolvedForAbsentGroupRoster) ListMemberships(ctx context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	if groupID == idNoGroup {
		return roster.Resolution[roster.Membership]{}, nil
	}
	return r.baseRoster.ListMemberships(ctx, groupID)
}

// selfAccusingRoster classifies its own failure as a contract violation — the
// kind a HOST reports about a connector, not one a connector returns. It would
// let a broken connector present itself as an already-diagnosed one.
type selfAccusingRoster struct{ baseRoster }

func (r *selfAccusingRoster) ListMemberships(ctx context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	if groupID == idNoGroup {
		return roster.Resolution[roster.Membership]{}, cerr.New(cerr.KindContractViolation, "ListMemberships", nil)
	}
	return r.baseRoster.ListMemberships(ctx, groupID)
}

// ---------------------------------------------------------------------------

func TestNonCompliantConnectorsFailTheCheckTheyViolate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		conn     roster.Roster
		manifest manifest.Doc
		wantFail string
	}{
		{
			name:     "success carrying no principal list (REQ-ARQ-P-17)",
			conn:     &unreadDirectoryRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckListsResolve,
		},
		{
			name:     "answers every list with EmptyRoster",
			conn:     &emptyEverythingRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckListsResolve,
		},
		{
			name:     "success carrying no group list",
			conn:     &unreadGroupsRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckListsResolve,
		},
		{
			name:     "success carrying no membership list",
			conn:     &unreadMembershipsRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckListsResolve,
		},
		{
			name:     "ListGroups returns a readable EMPTY set while groups are nominated (Finding 2)",
			conn:     &emptyGroupsRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckListsResolve,
		},
		{
			name:     "ListPrincipals asserts Partial on a truncated read (Finding 1)",
			conn:     &truncatedAssertedPartialRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckListsResolve,
		},
		{
			name:     "omits the suspended principal",
			conn:     &droppingSuspendedRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckSuspendedIsPresent,
		},
		{
			name:     "reports the suspended principal as active",
			conn:     &deactivationBlindRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckSuspendedIsPresent,
		},
		{
			name:     "returns a membership for another group",
			conn:     &wrongGroupRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckMembershipShape,
		},
		{
			name:     "the nominated group has no members, so nothing was exercised",
			conn:     &emptyNominatedGroupRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckMembershipShape,
		},
		{
			name:     "ListGroups omits the nominated group while ListMemberships answers for it",
			conn:     &groupOmittedFromListGroupsRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckMembershipShape,
		},
		{
			name:     "untyped, vendor-text failure (REQ-ARQ-P-19)",
			conn:     &vendorTextRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckFailureTyped,
		},
		{
			name:     "answers for a group this run declares absent",
			conn:     &answersForEveryGroupRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckFailureTyped,
		},
		{
			name:     "answers an absent group with an asserted empty roster",
			conn:     &emptyForAbsentGroupRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckFailureTyped,
		},
		{
			name:     "answers an absent group with neither a list nor an error",
			conn:     &unresolvedForAbsentGroupRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckFailureTyped,
		},
		{
			name:     "classifies its own failure as a contract violation",
			conn:     &selfAccusingRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckFailureTyped,
		},
		{
			name:     "declares watch and does not implement roster.Watcher (REQ-ARQ-P-20)",
			conn:     &baseRoster{caps: connector.Capabilities{roster.CapWatch}},
			manifest: manifestFor(roster.CapWatch),
			wantFail: rosterconform.CheckWatchDeclared,
		},
		{
			name:     "implements roster.Watcher and declares it nowhere",
			conn:     &watchingRoster{},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckWatchDeclared,
		},
		{
			name:     "declares and implements watch, but Watch never establishes one (Finding 3)",
			conn:     &stubWatchRoster{baseRoster{caps: connector.Capabilities{roster.CapWatch}}},
			manifest: manifestFor(roster.CapWatch),
			wantFail: rosterconform.CheckWatchDeclared,
		},
		{
			name:     "declares and implements watch, but Watch returns a nil channel (Finding 3)",
			conn:     &nilChannelWatchRoster{baseRoster{caps: connector.Capabilities{roster.CapWatch}}},
			manifest: manifestFor(roster.CapWatch),
			wantFail: rosterconform.CheckWatchDeclared,
		},
		{
			name:     "declares and implements watch, but Watch returns an already-closed channel",
			conn:     &alreadyClosedWatchRoster{baseRoster{caps: connector.Capabilities{roster.CapWatch}}},
			manifest: manifestFor(roster.CapWatch),
			wantFail: rosterconform.CheckWatchDeclared,
		},
		{
			name:     "declares machine principals and reports none",
			conn:     &baseRoster{caps: connector.Capabilities{roster.CapMachinePrincipals}},
			manifest: manifestFor(roster.CapMachinePrincipals),
			wantFail: rosterconform.CheckMachinePrincipalsDeclared,
		},
		{
			name:     "reports a machine principal and declares it nowhere",
			conn:     &baseRoster{withMachine: true},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckMachinePrincipalsDeclared,
		},
		{
			name:     "declares transitive membership and reports none",
			conn:     &baseRoster{caps: connector.Capabilities{roster.CapTransitiveMembership}},
			manifest: manifestFor(roster.CapTransitiveMembership),
			wantFail: rosterconform.CheckTransitiveDeclared,
		},
		{
			name:     "reports an inherited membership and declares it nowhere",
			conn:     &baseRoster{withInherited: true},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckTransitiveDeclared,
		},
		{
			name:     "manifest is invalid",
			conn:     &baseRoster{},
			manifest: manifest.Doc{Implements: connector.ClassRoster, Kind: manifest.KindNative},
			wantFail: rosterconform.CheckManifest,
		},
		{
			name: "manifest is for another connector class",
			conn: &baseRoster{},
			manifest: manifest.Doc{
				Name: name, Kind: manifest.KindNative,
				Implements: connector.ClassCredentialLoader,
			},
			wantFail: rosterconform.CheckManifest,
		},
		{
			name:     "manifest declares a capability outside the vocabulary",
			conn:     &baseRoster{},
			manifest: manifestFor(connector.Capability("wach")),
			wantFail: rosterconform.CheckManifest,
		},
		{
			name:     "manifest and running connector disagree about capabilities",
			conn:     &baseRoster{caps: connector.Capabilities{roster.CapWatch}},
			manifest: manifestFor(),
			wantFail: rosterconform.CheckCapabilityHonesty,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := rosterconform.Run(context.Background(), tc.conn, opts(tc.manifest))
			if err != nil {
				t.Fatalf("the harness could not run: %v", err)
			}
			if rep.OK() {
				t.Fatalf("a deliberately non-compliant connector passed conformance:\n%s", rep)
			}
			if !failed(rep, tc.wantFail) {
				t.Fatalf("check %q did not fail; the harness caught something else, or nothing that names this property:\n%s", tc.wantFail, rep)
			}
			if rep.Err() == nil {
				t.Fatalf("Report.Err() must be non-nil when a check failed")
			}
			if cerr.KindOf(rep.Err()) != cerr.KindInvalid {
				t.Fatalf("Report.Err() Kind = %v, want KindInvalid", cerr.KindOf(rep.Err()))
			}
		})
	}
}

// TestCompliantConnectorsPass is the control. Without it, a harness that failed
// everything would look just as convincing as one that works.
func TestCompliantConnectorsPass(t *testing.T) {
	t.Run("no capabilities declared", func(t *testing.T) {
		rep, err := rosterconform.Run(context.Background(), &baseRoster{}, opts(manifestFor()))
		if err != nil {
			t.Fatalf("the harness could not run: %v", err)
		}
		if !rep.OK() {
			t.Fatalf("a compliant connector must pass:\n%s", rep)
		}
	})

	t.Run("every capability declared, implemented and substantiated", func(t *testing.T) {
		m := manifestFor(roster.KnownCapabilities()...)
		rep, err := rosterconform.Run(context.Background(), compliant(), opts(m))
		if err != nil {
			t.Fatalf("the harness could not run: %v", err)
		}
		if !rep.OK() {
			t.Fatalf("a fully capable compliant connector must pass:\n%s", rep)
		}
	})
}

// TestEveryObligationIsChecked pins the check set. A check that stops running
// is a hole in the harness, and a report that is green because nothing looked
// is the failure mode this asserts against.
//
// It also pins that EVERY check runs on EVERY run: none of them is skipped for
// a connector that lacks a capability. A skipped check is indistinguishable
// from a passing one in a report, and that is how a harness comes back green
// having examined less than it appears to.
func TestEveryObligationIsChecked(t *testing.T) {
	all := []string{
		rosterconform.CheckManifest,
		rosterconform.CheckCapabilityHonesty,
		rosterconform.CheckWatchDeclared,
		rosterconform.CheckMachinePrincipalsDeclared,
		rosterconform.CheckTransitiveDeclared,
		rosterconform.CheckListsResolve,
		rosterconform.CheckSuspendedIsPresent,
		rosterconform.CheckMembershipShape,
		rosterconform.CheckFailureTyped,
	}
	for _, tc := range []struct {
		name string
		conn roster.Roster
		m    manifest.Doc
	}{
		{"a connector with no capabilities", &baseRoster{}, manifestFor()},
		{"a connector with every capability", compliant(), manifestFor(roster.KnownCapabilities()...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := rosterconform.Run(context.Background(), tc.conn, opts(tc.m))
			if err != nil {
				t.Fatalf("the harness could not run: %v", err)
			}
			for _, check := range all {
				if !ran(rep, check) {
					t.Fatalf("check %q did not run:\n%s", check, rep)
				}
			}
			if len(rep.Results) != len(all) {
				t.Fatalf("the report has %d results for %d known checks; an unlisted check is one nothing pins:\n%s",
					len(rep.Results), len(all), rep)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The anti-tautology proofs.
//
// A declared-is-implemented check is worthless if "implemented" is computed
// from the same signal as "declared": the check then agrees with itself
// whatever the connector does, and reports PASS on both of the two failures it
// exists to catch. That is not hypothetical — it happened in this SDK's
// credential harness over an out-of-process provider, where the host-side
// stub's shape was derived from the very capability declaration the check was
// verifying (sdk#19 re-review, Finding 2).
//
// A comment claiming independence proves nothing. Each table below drives one
// check through all FOUR combinations of (declared, actually the case) and pins
// the verdict of each. A check whose two inputs are one signal can only ever
// see the diagonal, so it cannot produce four correct verdicts — the off-
// diagonal rows are the proof, and they are the rows that fail if the
// independence is ever lost.
// ---------------------------------------------------------------------------

// TestWatchDeclaredIsIndependentOfTheDeclaration drives the interface-shaped
// capability. "Implemented" is a Go type assertion against the connector's own
// type, which no manifest can influence.
func TestWatchDeclaredIsIndependentOfTheDeclaration(t *testing.T) {
	for _, tc := range truthTable() {
		t.Run(tc.name, func(t *testing.T) {
			var conn roster.Roster
			caps := declaredCaps(tc.declared, roster.CapWatch)
			if tc.actual {
				conn = &watchingRoster{baseRoster{caps: caps}}
			} else {
				conn = &baseRoster{caps: caps}
			}
			assertVerdict(t, conn, manifestFor(caps...), rosterconform.CheckWatchDeclared, tc)
		})
	}
}

// TestMachinePrincipalsDeclaredIsIndependentOfTheDeclaration drives the first
// data-shaped capability. "Actually the case" is read from the principals the
// connector returned — a Kind of machine in the data — which is not derived
// from a manifest either.
func TestMachinePrincipalsDeclaredIsIndependentOfTheDeclaration(t *testing.T) {
	for _, tc := range truthTable() {
		t.Run(tc.name, func(t *testing.T) {
			caps := declaredCaps(tc.declared, roster.CapMachinePrincipals)
			conn := &baseRoster{caps: caps, withMachine: tc.actual}
			assertVerdict(t, conn, manifestFor(caps...), rosterconform.CheckMachinePrincipalsDeclared, tc)
		})
	}
}

// TestTransitiveDeclaredIsIndependentOfTheDeclaration drives the second
// data-shaped capability, read from Direct false in the membership list.
func TestTransitiveDeclaredIsIndependentOfTheDeclaration(t *testing.T) {
	for _, tc := range truthTable() {
		t.Run(tc.name, func(t *testing.T) {
			caps := declaredCaps(tc.declared, roster.CapTransitiveMembership)
			conn := &baseRoster{caps: caps, withInherited: tc.actual}
			assertVerdict(t, conn, manifestFor(caps...), rosterconform.CheckTransitiveDeclared, tc)
		})
	}
}

type verdictCase struct {
	name     string
	declared bool
	actual   bool
	wantPass bool
}

// truthTable is the four combinations, with the two off-diagonal rows being the
// ones a tautological check gets wrong.
func truthTable() []verdictCase {
	return []verdictCase{
		{name: "not declared, not the case", declared: false, actual: false, wantPass: true},
		{name: "declared and the case", declared: true, actual: true, wantPass: true},
		{name: "declared, NOT the case", declared: true, actual: false, wantPass: false},
		{name: "the case, NOT declared", declared: false, actual: true, wantPass: false},
	}
}

func declaredCaps(declared bool, c connector.Capability) connector.Capabilities {
	if declared {
		return connector.Capabilities{c}
	}
	return nil
}

// assertVerdict runs the harness and pins ONE named check's verdict. It also
// pins that the capability-honesty check stays green in every row, so a table
// row cannot pass or fail for the wrong reason: the manifest and the running
// connector always agree here, and the only thing varying is whether the
// declaration matches reality.
func assertVerdict(t *testing.T, conn roster.Roster, m manifest.Doc, check string, tc verdictCase) {
	t.Helper()
	rep, err := rosterconform.Run(context.Background(), conn, opts(m))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	if failed(rep, rosterconform.CheckCapabilityHonesty) {
		t.Fatalf("the fixture is wrong, not the check: the manifest and Capabilities() must agree in every row:\n%s", rep)
	}
	if !ran(rep, check) {
		t.Fatalf("check %q did not run:\n%s", check, rep)
	}
	gotPass := !failed(rep, check)
	if gotPass != tc.wantPass {
		t.Fatalf("declared=%v actual=%v: %q reported pass=%v, want pass=%v. A check that cannot tell these two "+
			"apart is computing both of its inputs from one signal:\n%s", tc.declared, tc.actual, check, gotPass, tc.wantPass, rep)
	}
}

// TestNoDeclarationCheckAgreesWithItself is the table's conclusion stated
// directly: across the four rows, each declaration check must produce BOTH
// verdicts. A check that returned the same verdict for all four — the shape a
// tautology has — passes every individual row assertion above only if the table
// happens to expect it, so this asserts the discrimination itself.
func TestNoDeclarationCheckAgreesWithItself(t *testing.T) {
	for _, probe := range []struct {
		check string
		build func(declared, actual bool) (roster.Roster, manifest.Doc)
	}{
		{
			check: rosterconform.CheckWatchDeclared,
			build: func(declared, actual bool) (roster.Roster, manifest.Doc) {
				caps := declaredCaps(declared, roster.CapWatch)
				if actual {
					return &watchingRoster{baseRoster{caps: caps}}, manifestFor(caps...)
				}
				return &baseRoster{caps: caps}, manifestFor(caps...)
			},
		},
		{
			check: rosterconform.CheckMachinePrincipalsDeclared,
			build: func(declared, actual bool) (roster.Roster, manifest.Doc) {
				caps := declaredCaps(declared, roster.CapMachinePrincipals)
				return &baseRoster{caps: caps, withMachine: actual}, manifestFor(caps...)
			},
		},
		{
			check: rosterconform.CheckTransitiveDeclared,
			build: func(declared, actual bool) (roster.Roster, manifest.Doc) {
				caps := declaredCaps(declared, roster.CapTransitiveMembership)
				return &baseRoster{caps: caps, withInherited: actual}, manifestFor(caps...)
			},
		},
	} {
		t.Run(probe.check, func(t *testing.T) {
			verdicts := map[bool]int{}
			for _, declared := range []bool{false, true} {
				for _, actual := range []bool{false, true} {
					conn, m := probe.build(declared, actual)
					rep, err := rosterconform.Run(context.Background(), conn, opts(m))
					if err != nil {
						t.Fatalf("the harness could not run: %v", err)
					}
					verdicts[!failed(rep, probe.check)]++
				}
			}
			if verdicts[true] != 2 || verdicts[false] != 2 {
				t.Fatalf("%q reported %d pass / %d fail across the four combinations, want 2/2. "+
					"A check that cannot separate the diagonal from the off-diagonal is reading one signal twice",
					probe.check, verdicts[true], verdicts[false])
			}
		})
	}
}

// ---------------------------------------------------------------------------

// TestAllEmptyConnectorIsNotGreen is the presence-versus-substance hole closed,
// stated on its own because a table row is easy to lose.
//
// A connector that answers every list with roster.EmptyRoster() is present,
// readable and completely empty. Under a check that asked only whether the read
// succeeded it produced a REPORT WITH NO FAILURES — every check green — while
// telling the host that the entire organisation had left. Nothing else about it
// is wrong: its manifest is honest, its failures are typed, it fails on the
// group it should fail on. That is what made it dangerous.
func TestAllEmptyConnectorIsNotGreen(t *testing.T) {
	rep, err := rosterconform.Run(context.Background(), &emptyEverythingRoster{}, opts(manifestFor()))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	if rep.OK() {
		t.Fatalf("a connector that reads every list as empty scored a green report:\n%s", rep)
	}
	if !failed(rep, rosterconform.CheckListsResolve) {
		t.Fatalf("%q must be the check that fails:\n%s", rosterconform.CheckListsResolve, rep)
	}
	// The report has to tell the author what to do about it, not just that
	// something is wrong.
	detail := detailOf(rep, rosterconform.CheckListsResolve)
	if !strings.Contains(detail, "EmptyRoster") {
		t.Fatalf("the failure must name the construct the author reached for, got: %s", detail)
	}
}

// TestSuspendedPrincipalMustBeReportedNotDropped is the destructive trap,
// likewise stated on its own. A deactivated identity is still in the directory,
// and omission means departure — after which a host sweeping for departures
// revokes everything belonging to somebody on leave.
func TestSuspendedPrincipalMustBeReportedNotDropped(t *testing.T) {
	rep, err := rosterconform.Run(context.Background(), &droppingSuspendedRoster{}, opts(manifestFor()))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	if !failed(rep, rosterconform.CheckSuspendedIsPresent) {
		t.Fatalf("dropping a deactivated principal must fail %q:\n%s", rosterconform.CheckSuspendedIsPresent, rep)
	}
	// Note what did NOT save it: the list is non-empty, every entry is
	// well-formed, and the resolution check is green. Substance in aggregate
	// says nothing about whether the one entry that matters survived.
	if failed(rep, rosterconform.CheckListsResolve) {
		t.Fatalf("this fixture reads its directory correctly; the resolve check must not be what catches it:\n%s", rep)
	}
	detail := detailOf(rep, rosterconform.CheckSuspendedIsPresent)
	if !strings.Contains(detail, "ABSENT") {
		t.Fatalf("the failure must say the principal was absent, got: %s", detail)
	}
}

// TestReadableEmptyGroupListIsNotGreen is Finding 2, stated on its own.
// Principals and memberships are real; ListGroups reports the directory as
// having no groups at all, readably. A host reconciling group-derived access
// reads "this directory has no groups" and revokes what every group carried.
func TestReadableEmptyGroupListIsNotGreen(t *testing.T) {
	rep, err := rosterconform.Run(context.Background(), &emptyGroupsRoster{}, opts(manifestFor()))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	if rep.OK() {
		t.Fatalf("a connector reporting no groups at all, while principals and memberships are real, scored a green report:\n%s", rep)
	}
	if !failed(rep, rosterconform.CheckListsResolve) {
		t.Fatalf("%q must be the check that fails:\n%s", rosterconform.CheckListsResolve, rep)
	}
	detail := detailOf(rep, rosterconform.CheckListsResolve)
	if !strings.Contains(detail, "NO GROUPS") {
		t.Fatalf("the failure must say what was missing, got: %s", detail)
	}
}

// TestNonexistentGroupCannotScoreGreen proves the no-false-positive property
// TestReadableEmptyGroupListIsNotGreen used to gesture at with a strictly
// weaker check: it asserted only that Run refuses an EMPTY Options.Group
// string (already covered by TestRunRefusesToPretend), and treated that as
// proof that "nothing reaching this check can legitimately be groupless." It
// is not: Run validates only that the string is non-empty, never that it
// names a real or populated group — a NON-EMPTY, merely-nonexistent group
// sails straight past Run and into the checks.
//
// The property still holds, by the mechanism the package doc now names: a
// group that does not exist must fail ListMemberships typed, which trips
// lists/resolve-not-empty and memberships/match-requested-group before the
// group-substance branch is ever reached — and memberships/match-requested-group
// separately correlates Options.Group against what ListGroups itself
// reported, so a connector that instead fabricates members for a group id
// its own ListGroups does not know about is caught too.
func TestNonexistentGroupCannotScoreGreen(t *testing.T) {
	bogusGroup := "placeholder-group-that-does-not-exist"
	rep, err := rosterconform.Run(context.Background(), &baseRoster{}, rosterconform.Options{
		Manifest: manifestFor(), AbsentGroup: idNoGroup, SuspendedPrincipal: idSuspended, Group: bogusGroup,
	})
	if err != nil {
		t.Fatalf("Run must PROCEED with a non-empty, merely-nonexistent Options.Group — it validates only that the "+
			"string is non-empty — so this property can even be exercised: %v", err)
	}
	if rep.OK() {
		t.Fatalf("a compliant connector fed a NONEXISTENT (but non-empty) Options.Group scored a green report; the "+
			"no-false-positive property does not hold:\n%s", rep)
	}
	if !failed(rep, rosterconform.CheckListsResolve) {
		t.Fatalf("%q must be among the checks that fail a nonexistent group:\n%s", rosterconform.CheckListsResolve, rep)
	}
	if !failed(rep, rosterconform.CheckMembershipShape) {
		t.Fatalf("%q must be among the checks that fail a nonexistent group:\n%s", rosterconform.CheckMembershipShape, rep)
	}
}

// TestGroupNotInListGroupsCannotScoreGreen is the correlation check added
// alongside the fix above, proved directly: a connector whose ListMemberships
// answers real, correctly-attributed members for Options.Group while its own
// ListGroups never reports that group as existing is exactly the shape
// nothing about membership SHAPE alone catches — every entry names the right
// group. Only correlating Options.Group against ListGroups closes it, and
// that correlation is what makes "Options.Group names a real group" hold by
// construction rather than by the coincidence of the failure paths above.
func TestGroupNotInListGroupsCannotScoreGreen(t *testing.T) {
	rep, err := rosterconform.Run(context.Background(), &groupOmittedFromListGroupsRoster{}, opts(manifestFor()))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	if rep.OK() {
		t.Fatalf("a connector whose ListGroups omits the group its ListMemberships answers for scored a green report:\n%s", rep)
	}
	if !failed(rep, rosterconform.CheckMembershipShape) {
		t.Fatalf("%q must be the check that fails:\n%s", rosterconform.CheckMembershipShape, rep)
	}
	// This must not be caught by the resolve check instead: ListGroups is
	// non-empty and readable (it reports idParent). If lists/resolve-not-empty
	// is what failed, the correlation check itself is not being exercised.
	if failed(rep, rosterconform.CheckListsResolve) {
		t.Fatalf("lists/resolve-not-empty must not be what catches this; ListGroups is readable and non-empty:\n%s", rep)
	}
	detail := detailOf(rep, rosterconform.CheckMembershipShape)
	if !strings.Contains(detail, "not among") {
		t.Fatalf("the failure must say the group is not among what ListGroups reported, got: %s", detail)
	}
}

// TestTruncatedPrincipalReadCannotBeExpressedAsASuccess is Finding 1, stated
// on its own: a connector whose own pagination stops before the whole
// directory is covered can no longer report what it has as a success at all
// once it says so honestly.
func TestTruncatedPrincipalReadCannotBeExpressedAsASuccess(t *testing.T) {
	rep, err := rosterconform.Run(context.Background(), &truncatedAssertedPartialRoster{}, opts(manifestFor()))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	if rep.OK() {
		t.Fatalf("a connector asserting roster.Partial on a truncated read scored a green report:\n%s", rep)
	}
	if !failed(rep, rosterconform.CheckListsResolve) {
		t.Fatalf("%q must be the check that fails:\n%s", rosterconform.CheckListsResolve, rep)
	}
}

// TestTruncatedReadNotAssertingPartialIsTheKnownResidualLimitation documents,
// rather than hides, the boundary of Finding 1's fix. A connector whose
// pagination truncates a read and then asserts roster.Complete anyway — a
// conscious misclassification, not the accidental one the fix targets — is
// not distinguishable from a genuine complete read by anything outside the
// connector. This is expected to keep passing; the fix narrows the mistake
// from "the natural, careless thing to write" to "a lie at the call site",
// it does not claim to catch the lie.
func TestTruncatedReadNotAssertingPartialIsTheKnownResidualLimitation(t *testing.T) {
	rep, err := rosterconform.Run(context.Background(), &truncatedNotTouchingTheFixtureRoster{}, opts(manifestFor()))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("this fixture asserts roster.Complete honestly-shaped data (from the harness's point of view); "+
			"if this now fails, some check started reading ground truth this fixture does not provide — "+
			"re-diagnose before treating this as a fix:\n%s", rep)
	}
}

// TestWatchThatNeverEstablishesIsNotGreen is Finding 3, stated on its own. A
// Go type assertion proves roster.Watcher's method exists; it proves nothing
// about what calling it does.
//
// The already-closed case is the one a cancel-then-wait-for-close check
// cannot catch at all: it is the most idiomatic-LOOKING Go stub of the four
// here, and passed this check before establishesWatch started proving the
// channel was open BEFORE cancelling it.
func TestWatchThatNeverEstablishesIsNotGreen(t *testing.T) {
	for _, tc := range []struct {
		name string
		conn roster.Roster
	}{
		{"Watch always fails to establish", &stubWatchRoster{baseRoster{caps: connector.Capabilities{roster.CapWatch}}}},
		{"Watch returns a nil channel", &nilChannelWatchRoster{baseRoster{caps: connector.Capabilities{roster.CapWatch}}}},
		{"Watch never closes its channel", &neverClosingWatchRoster{baseRoster{caps: connector.Capabilities{roster.CapWatch}}}},
		{"Watch returns an already-closed channel", &alreadyClosedWatchRoster{baseRoster{caps: connector.Capabilities{roster.CapWatch}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := rosterconform.Run(context.Background(), tc.conn, opts(manifestFor(roster.CapWatch)))
			if err != nil {
				t.Fatalf("the harness could not run: %v", err)
			}
			if rep.OK() {
				t.Fatalf("a connector declaring watch whose Watch does not actually establish one scored a green report:\n%s", rep)
			}
			if !failed(rep, rosterconform.CheckWatchDeclared) {
				t.Fatalf("%q must be the check that fails:\n%s", rosterconform.CheckWatchDeclared, rep)
			}
		})
	}
}

// TestRunRefusesToPretend: without fixtures there is nothing to drive the
// connector with, and a harness that reports success in that state is worse
// than one that refuses.
func TestRunRefusesToPretend(t *testing.T) {
	valid := opts(manifestFor())

	t.Run("nil connector", func(t *testing.T) {
		if _, err := rosterconform.Run(context.Background(), nil, valid); err == nil {
			t.Fatalf("Run(nil connector) must error")
		}
	})
	for _, tc := range []struct {
		name  string
		mutid func(*rosterconform.Options)
		want  string
	}{
		{"no group", func(o *rosterconform.Options) { o.Group = "" }, "Group"},
		{"no absent group", func(o *rosterconform.Options) { o.AbsentGroup = "" }, "AbsentGroup"},
		{"no suspended principal", func(o *rosterconform.Options) { o.SuspendedPrincipal = "" }, "SuspendedPrincipal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := valid
			tc.mutid(&o)
			_, err := rosterconform.Run(context.Background(), &baseRoster{}, o)
			if err == nil {
				t.Fatalf("Run without %s must error rather than report a green run", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the error must name the missing fixture: %v", err)
			}
			if cerr.KindOf(err) != cerr.KindInvalid {
				t.Fatalf("Kind = %v, want KindInvalid", cerr.KindOf(err))
			}
		})
	}
}

// TestBothDirectionsOfCapabilityDisagreementAreReported: a report that names
// only one side of a mismatch sends an author round the loop twice.
func TestBothDirectionsOfCapabilityDisagreementAreReported(t *testing.T) {
	conn := &baseRoster{caps: connector.Capabilities{roster.CapMachinePrincipals}, withMachine: true}
	rep, err := rosterconform.Run(context.Background(), conn, opts(manifestFor(roster.CapWatch)))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	detail := detailOf(rep, rosterconform.CheckCapabilityHonesty)
	if !strings.Contains(detail, string(roster.CapWatch)) || !strings.Contains(detail, string(roster.CapMachinePrincipals)) {
		t.Fatalf("both sides of the mismatch must be named, got: %s", detail)
	}
}

func TestReportStringNamesFailedChecks(t *testing.T) {
	rep, err := rosterconform.Run(context.Background(), &unreadDirectoryRoster{}, opts(manifestFor()))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	s := rep.String()
	if !strings.Contains(s, rosterconform.CheckListsResolve) || !strings.Contains(s, "FAIL") {
		t.Fatalf("Report.String() must name the failed check for a CI log:\n%s", s)
	}
	if !strings.Contains(s, "PASS") {
		t.Fatalf("Report.String() must render the checks that passed too:\n%s", s)
	}
	if !strings.Contains(rep.Err().Error(), name) {
		t.Fatalf("the failure must name the connector: %v", rep.Err())
	}
	// A report with no connector name still renders, rather than producing an
	// empty attribution an operator cannot act on.
	unnamed := rosterconform.Report{}
	if !strings.Contains(unnamed.String(), "<unnamed>") {
		t.Fatalf("an unnamed report renders as %q", unnamed.String())
	}
	if unnamed.Err() != nil || !unnamed.OK() {
		t.Fatalf("a report with no results has nothing to fail on")
	}
}

// TestWatchClosesItsChannelWhenTheContextIsDone pins the documented lifecycle:
// a lost watch is not an emergency, so there is no error channel — the channel
// simply closes and the host falls back to the poll it was always doing. A
// Watch that leaked its goroutine past cancellation would strand it for the
// life of the process.
func TestWatchClosesItsChannelWhenTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := compliant().Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	got, ok := <-ch
	if !ok {
		t.Fatalf("the watch closed before delivering anything")
	}
	if got.Subject != roster.SubjectMembership {
		t.Fatalf("Change.Subject = %v", got.Subject)
	}
	cancel()
	for range ch { //nolint:revive // draining until close is the assertion
	}
}

func detailOf(rep rosterconform.Report, name string) string {
	for _, r := range rep.Results {
		if r.Name == name {
			return r.Detail
		}
	}
	return ""
}

func failed(rep rosterconform.Report, name string) bool {
	return slices.ContainsFunc(rep.Failures(), func(r rosterconform.Result) bool { return r.Name == name })
}

func ran(rep rosterconform.Report, name string) bool {
	return slices.ContainsFunc(rep.Results, func(r rosterconform.Result) bool { return r.Name == name })
}
