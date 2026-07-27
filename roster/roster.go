// Package roster defines the Roster connector-class contract: a READ-ONLY
// view of a directory's principals, its groups, and the memberships between
// them.
//
// A roster connector adapts one directory — an identity provider, a workspace
// directory, a code host's teams, a flat file checked into a repo — and it
// reports only what that directory says. It holds no arqtos policy of any
// kind. A connector that answered "this person belongs to venture X with role
// Y" would be encoding the host's own organisational model, and the host would
// then have as many organisational models as it has directories. Mapping
// directory facts onto orgs, teams and entitlements is the host's job, on the
// host's side of this boundary.
//
// # Read-only, on purpose
//
// There is no Create, no Update, no Delete, and none is coming. arqtos
// provisions arqtos-side artifacts; it does not create people in someone
// else's directory. A class that could write to a directory would make every
// reconcile bug a destructive one.
//
// # Three lists, and the one thing that must never be confused
//
// [Roster.ListPrincipals], [Roster.ListGroups] and [Roster.ListMemberships]
// each return a [Resolution], not a slice. That is the whole point of the
// type: a zero-length slice cannot tell "this directory genuinely has nobody"
// apart from "the read failed and I am returning a zero value", and a host
// sweeping for departed people over the second reading deprovisions the entire
// estate. See [Resolution].
//
// # Suspended is not absent
//
// A deactivated directory user is STILL IN THE DIRECTORY. Report them, with
// [Principal.Active] false. Omitting them tells the host they left the
// organisation, and the host revokes everything — for someone who is on
// parental leave. Omission from [Roster.ListPrincipals] means "not in the
// directory at all", and nothing else.
//
// # Optional behaviour is declared, never assumed
//
// The three capabilities in this package are measured differences between real
// directories, not speculative feature flags: one vendor pushes membership
// change events and another cannot, one returns inherited memberships in a
// single call and another cannot express nesting at all, one has machine
// identities as directory objects and another keeps them in a separate system
// where a directory read cannot see them. A host that assumed any of these
// would be wrong about at least one backend, so each is declared — in the
// connector's manifest and by Capabilities() — and checked against the running
// connector by the rosterconform harness.
package roster

import (
	"context"
	"slices"
	"strconv"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

const (
	// CapWatch declares that this connector can report directory CHANGE
	// without being asked — a push subscription rather than a poll — via
	// [Watcher].
	//
	// It is optional because it is not universally available: one major
	// directory can push membership change to a subscribing application,
	// while another's change-notification surface covers users only and
	// explicitly does not extend to groups or their members. A host that
	// assumed push would silently never reconcile against the second one.
	//
	// Events are an OPTIMISATION. Correctness comes from the host's reconcile
	// loop, which re-reads the three lists on a schedule whether or not any
	// event arrived. A connector without this capability is not a degraded
	// connector; it is a connector the host polls.
	CapWatch connector.Capability = "watch"

	// CapTransitiveMembership declares that [Roster.ListMemberships] reports
	// INHERITED memberships — those a principal holds through group nesting —
	// as well as direct ones, marked [Membership.Direct] false.
	//
	// It is optional because nesting is not universally expressible: one
	// directory returns derived members in the same call as direct ones, and
	// another's provisioning path cannot represent group nesting at all. A
	// host that assumed transitivity would under-read the second one and
	// conclude that people had left groups they are still in.
	//
	// A connector without this capability MUST report only direct
	// memberships. Reporting inherited ones without declaring it fails
	// conformance: the host cannot tell whether an absent inherited
	// membership means "not inherited" or "this connector does not do
	// nesting", so the declaration is what makes the absence readable.
	CapTransitiveMembership connector.Capability = "transitive_membership"

	// CapMachinePrincipals declares that this connector reports non-human
	// identities — service accounts, service applications, bots — as
	// principals, with [Principal.Kind] [PrincipalMachine].
	//
	// It is optional because for some directories they are simply not there:
	// one vendor's service applications are directory objects a directory read
	// returns, while another vendor's service accounts live in a separate
	// cloud-IAM system and do NOT appear in the directory at all. A
	// connector for the second one genuinely cannot report machine
	// principals, and must say so rather than leave the host to infer it from
	// an empty result.
	//
	// A connector without this capability MUST report no machine principals.
	CapMachinePrincipals connector.Capability = "machine_principals"
)

// knownCapabilities is the closed capability vocabulary of this connector
// class. A manifest declaring anything outside it fails conformance: the
// capability a host does not recognise is a capability the host will not use,
// and a typo is indistinguishable from a capability that has yet to ship.
var knownCapabilities = connector.Capabilities{
	CapWatch, CapTransitiveMembership, CapMachinePrincipals,
}

// KnownCapabilities returns the closed capability vocabulary for the Roster
// class, as a copy. Adding one is a deliberate contract change.
func KnownCapabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), knownCapabilities...)
}

// A PrincipalKind is what sort of identity a [Principal] is.
//
// The zero value is [PrincipalUnknown], which is the safe default in the same
// way cerr.KindUnknown is: a connector that has not classified an identity
// says so, and a host that treats machine identities differently from human
// ones then knows it must not apply either rule. A string-typed kind would
// have made the zero value the empty string — neither a classification nor an
// admission that one is missing.
type PrincipalKind int

const (
	// PrincipalUnknown is an identity the connector could not classify. It is
	// the zero value, so a connector that forgets to set Kind reports
	// "unclassified" rather than accidentally reporting "human".
	PrincipalUnknown PrincipalKind = iota
	// PrincipalHuman is a person.
	PrincipalHuman
	// PrincipalMachine is a non-human identity: a service account, a service
	// application, a bot. Reporting one requires [CapMachinePrincipals].
	PrincipalMachine
)

// principalKindNames is the single source of truth for the closed
// PrincipalKind vocabulary: [PrincipalKinds], [PrincipalKind.Valid] and
// [PrincipalKind.String] all derive from it.
var principalKindNames = map[PrincipalKind]string{
	PrincipalUnknown: "unknown",
	PrincipalHuman:   "human",
	PrincipalMachine: "machine",
}

// principalKinds is principalKindNames' key set in a stable order, built once
// FROM the map, so a kind cannot be half-added (in the enum but nameless, or
// named but not enumerable).
var principalKinds = func() []PrincipalKind {
	out := make([]PrincipalKind, 0, len(principalKindNames))
	for k := range principalKindNames {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}()

// PrincipalKinds returns the closed PrincipalKind vocabulary, in ascending
// order, as a copy.
func PrincipalKinds() []PrincipalKind { return slices.Clone(principalKinds) }

// Valid reports whether k is in the closed vocabulary. A PrincipalKind that is
// not is an integer someone converted, not a classification.
func (k PrincipalKind) Valid() bool {
	_, ok := principalKindNames[k]
	return ok
}

// String renders k's stable name. A kind outside the vocabulary renders as
// invalid_kind(N) rather than as "unknown", so it cannot hide behind the safe
// default.
func (k PrincipalKind) String() string {
	if name, ok := principalKindNames[k]; ok {
		return name
	}
	return "invalid_principal_kind(" + strconv.Itoa(int(k)) + ")"
}

// A Principal is one identity as the directory holds it.
//
// Every field is a directory fact. None of them is an arqtos concept: there is
// no org, no igloo, no team, no role and no entitlement here, because the
// moment a connector reports one of those it has started deciding host policy
// from inside a vendor adapter.
type Principal struct {
	// ID is the directory's STABLE identifier for this identity — the one
	// that survives a rename and a change of address.
	//
	// It is not the email. Email changes, and a host that keyed on it treats
	// a renamed person as a departed person plus a new hire: the first half
	// of that is a full deprovision. Where a directory offers a numeric or
	// opaque id alongside a login, the id is what belongs here.
	ID string
	// Handle is the login as the directory spells it, unnormalised. It is for
	// display and for correlating with vendor UIs — not for identity.
	Handle string
	// Email MAY be empty, and an empty one is not an error: machine
	// identities frequently have no mailbox, and some directories hold people
	// with no address either. A host that requires an address requires it of
	// itself, not of this contract.
	Email string
	// DisplayName is the human-readable name, as the directory spells it.
	DisplayName string
	// Active is whether the directory considers this identity currently
	// enabled.
	//
	// A deactivated identity is STILL REPORTED, with Active false. It has not
	// left the directory, and a host that sees it disappear concludes that it
	// has — then revokes everything belonging to someone who is suspended,
	// on leave, or mid-transfer. Absence from the list means absence from the
	// directory; this field is how "present but disabled" is said.
	Active bool
	// Kind is whether this is a human or a machine identity, or unclassified.
	// Reporting [PrincipalMachine] requires [CapMachinePrincipals].
	Kind PrincipalKind
}

// A Group is one group as the directory holds it.
type Group struct {
	// ID is the directory's stable identifier for the group, for the same
	// reason [Principal.ID] is.
	ID string
	// Handle is the group's name as the directory spells it.
	Handle string
	// DisplayName is the human-readable label.
	DisplayName string
	// ParentIDs are the groups this group is nested inside, where the
	// directory supports nesting. It is empty for a flat directory, and that
	// emptiness is not a failure — see [CapTransitiveMembership] for how a
	// host tells "no nesting here" from "this connector cannot express
	// nesting".
	ParentIDs []string
}

// A Membership is one principal's membership of one group.
type Membership struct {
	// PrincipalID is the [Principal.ID] of the member.
	//
	// It need not name a principal from [Roster.ListPrincipals]: some
	// directories admit groups as members of groups, and external or guest
	// identities that a directory read does not enumerate. A host resolves
	// what it recognises and reports what it does not; it must not treat an
	// unrecognised member as an absent one.
	PrincipalID string
	// GroupID is the [Group.ID] the membership is in.
	GroupID string
	// Direct is whether the membership is held directly. False means it is
	// inherited through group nesting, and a connector reports inherited
	// memberships only if it declares [CapTransitiveMembership].
	Direct bool
}

// Roster is the read-only directory connector-class contract.
//
// Every failure it returns is typed: a *cerr.Error whose Kind comes from
// cerr's closed vocabulary, so a host acts on the classification and never on
// the message. An unclassifiable failure is cerr.KindUnknown, which fails the
// call and escalates nothing.
//
// None of the three list operations can report a success that carries no
// list — see [Resolution]. That is the load-bearing property of this contract,
// because the host operation downstream of it removes access.
//
// Optional operations live behind capabilities rather than in this interface:
// [Watcher] behind [CapWatch]. A host type-asserts for them, and a connector
// that declares one without implementing it fails conformance.
type Roster interface {
	connector.Connector

	// ListPrincipals returns every identity in the directory this connector
	// serves, INCLUDING deactivated ones (with [Principal.Active] false).
	//
	// A directory that genuinely holds nobody is reported with
	// [EmptyRoster]; a read that failed is reported as a typed error. There
	// is no third option, and in particular no empty success.
	ListPrincipals(ctx context.Context) (Resolution[Principal], error)

	// ListGroups returns every group in the directory this connector serves.
	ListGroups(ctx context.Context) (Resolution[Group], error)

	// ListMemberships returns the memberships of the single group named by
	// groupID.
	//
	// It is per-group because that is the shape every directory actually
	// offers: each of the backends this class was measured against exposes
	// members-of-a-group and none exposes a whole-directory membership dump.
	// A host that wants the estate iterates [Roster.ListGroups] and calls
	// this for each.
	//
	// Every returned [Membership.GroupID] MUST equal groupID. A result for a
	// group other than the one asked about cannot be attributed by the host,
	// and guessing the correspondence is how the wrong people end up in the
	// wrong group — [CheckMemberships] is the host-side guard that proves it.
	//
	// A group that does not exist is cerr.KindNotFound, NOT an empty roster:
	// "this group has no members" and "there is no such group" lead a
	// reconcile loop to opposite conclusions.
	ListMemberships(ctx context.Context, groupID string) (Resolution[Membership], error)
}

// A Subject is what kind of directory object a [Change] refers to.
type Subject int

const (
	// SubjectUnknown is the zero value: a change the connector could not
	// attribute to a principal, a group or a membership. A host treats it as
	// "something changed" and re-reads everything, which is the same thing it
	// does on a schedule anyway.
	SubjectUnknown Subject = iota
	// SubjectPrincipal is a change to an identity.
	SubjectPrincipal
	// SubjectGroup is a change to a group.
	SubjectGroup
	// SubjectMembership is a change to who is in a group.
	SubjectMembership
)

// A Change is a HINT that the directory has moved on. It deliberately carries
// no before/after state.
//
// Carrying the change itself would make the event stream a second source of
// truth, and a host applying deltas is one dropped, reordered or duplicated
// event away from a roster that disagrees with the directory — with no way to
// notice. So a Change says only what to go and re-read. Correctness stays in
// the reconcile loop, which reads the three lists and would have converged
// anyway; the event only makes it converge sooner.
type Change struct {
	// Subject is what kind of object changed.
	Subject Subject
	// ID is the directory identifier of the object that changed, where the
	// directory names one. It may be empty: some notification surfaces say
	// only that a collection changed.
	ID string
	// GroupID is the group a [SubjectMembership] change is in, where the
	// directory names one. It is empty for other subjects.
	GroupID string
}

// Watcher is the optional contract operation behind [CapWatch]: be told that
// the directory changed, instead of asking.
//
// # Why it is optional, and why it is declared
//
// Push notification of membership change exists on some directories and not
// others — one vendor lets an application subscribe to it, while another's
// change-notification surface covers users only and explicitly excludes
// groups and their members. A host that assumed push would poll one backend
// and silently never reconcile the other.
//
// So a connector that can watch does two things, and must do both:
//
//   - implement this interface, and
//   - declare [CapWatch] in its manifest and from Capabilities().
//
// Declaring without implementing is worse than declaring nothing: the host
// lengthens its poll interval because it believes it will be told, and then is
// not. Conformance fails it, in both directions.
type Watcher interface {
	// Watch delivers a hint on every directory change until ctx is done.
	//
	// The returned error is a failure to ESTABLISH the watch, and it is
	// typed like every other failure in this contract. Once established, the
	// channel is closed when ctx is done or when the connector can no longer
	// sustain the subscription — and a closed channel is not an emergency: a
	// host that loses its watch falls back to the poll it was always doing.
	// That is why this contract has no error channel. A watch that cannot be
	// re-established degrades a host to its reconcile interval, which is the
	// same place a connector without [CapWatch] already leaves it.
	//
	// A connector MUST NOT block on a slow consumer in a way that stalls its
	// own reads. Drop hints rather than back up: a dropped hint costs one
	// reconcile interval of staleness, while a stalled connector costs the
	// host its directory.
	Watch(ctx context.Context) (<-chan Change, error)
}
