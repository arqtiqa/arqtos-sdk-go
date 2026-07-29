package transport

import (
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// PrincipalKindToPB converts a roster.PrincipalKind to its wire number.
func PrincipalKindToPB(k roster.PrincipalKind) int32 { return int32(k) }

// PrincipalKindFromPB converts a wire kind number back to a
// roster.PrincipalKind. A number outside the closed vocabulary becomes
// roster.PrincipalUnknown — the safe default.
func PrincipalKindFromPB(v int32) roster.PrincipalKind {
	k := roster.PrincipalKind(v)
	if !k.Valid() {
		return roster.PrincipalUnknown
	}
	return k
}

// PrincipalToPB converts a roster.Principal to its wire representation.
func PrincipalToPB(p roster.Principal) *connectorpb.Principal {
	return &connectorpb.Principal{
		Id:          p.ID,
		Handle:      p.Handle,
		Email:       p.Email,
		DisplayName: p.DisplayName,
		Active:      p.Active,
		Kind:        PrincipalKindToPB(p.Kind),
	}
}

// PrincipalFromPB converts a wire Principal back to roster.Principal.
func PrincipalFromPB(pb *connectorpb.Principal) roster.Principal {
	if pb == nil {
		return roster.Principal{}
	}
	return roster.Principal{
		ID:          pb.GetId(),
		Handle:      pb.GetHandle(),
		Email:       pb.GetEmail(),
		DisplayName: pb.GetDisplayName(),
		Active:      pb.GetActive(),
		Kind:        PrincipalKindFromPB(pb.GetKind()),
	}
}

// GroupToPB converts a roster.Group to its wire representation.
func GroupToPB(g roster.Group) *connectorpb.Group {
	return &connectorpb.Group{
		Id:          g.ID,
		Handle:      g.Handle,
		DisplayName: g.DisplayName,
		ParentIds:   g.ParentIDs,
	}
}

// GroupFromPB converts a wire Group back to roster.Group.
func GroupFromPB(pb *connectorpb.Group) roster.Group {
	if pb == nil {
		return roster.Group{}
	}
	return roster.Group{
		ID:          pb.GetId(),
		Handle:      pb.GetHandle(),
		DisplayName: pb.GetDisplayName(),
		ParentIDs:   pb.GetParentIds(),
	}
}

// MembershipToPB converts a roster.Membership to its wire representation.
func MembershipToPB(m roster.Membership) *connectorpb.Membership {
	return &connectorpb.Membership{
		PrincipalId: m.PrincipalID,
		GroupId:     m.GroupID,
		Direct:      m.Direct,
	}
}

// MembershipFromPB converts a wire Membership back to roster.Membership.
func MembershipFromPB(pb *connectorpb.Membership) roster.Membership {
	if pb == nil {
		return roster.Membership{}
	}
	return roster.Membership{
		PrincipalID: pb.GetPrincipalId(),
		GroupID:     pb.GetGroupId(),
		Direct:      pb.GetDirect(),
	}
}

// PrincipalRosterToPB converts a resolution of principals to the wire.
//
// The three in-process states cross as three distinct encodings, exactly as
// [ResolutionToPB] does for secret material:
//
//	unresolved           -> nil message (no PrincipalRoster at all)
//	deliberately empty   -> present message, no entries, EmptyByAssertion set
//	a list               -> present message carrying the entries
//
// The assertion flag is what makes the distinction survive a foreign sender.
// Presence alone cannot: proto3 does not put an empty repeated field on the
// wire, so a conformant EmptyRoster and a provider that read nothing and sent a
// default-constructed message would be byte-identical. Writing the flag here is
// the sending half of that; [PrincipalRosterFromPB] is the half that matters.
func PrincipalRosterToPB(r roster.Resolution[roster.Principal]) *connectorpb.PrincipalRoster {
	items, ok := readable(r)
	if !ok {
		return nil
	}
	if len(items) == 0 {
		return &connectorpb.PrincipalRoster{EmptyByAssertion: true}
	}
	out := make([]*connectorpb.Principal, len(items))
	for i, p := range items {
		out[i] = PrincipalToPB(p)
	}
	return &connectorpb.PrincipalRoster{Principals: out}
}

// PrincipalRosterFromPB converts a wire principal list back to a resolution.
//
// It reads FOUR cases, and the fourth is the point:
//
//	nil message                        -> unresolved (unreadable)
//	entries present                    -> a list
//	no entries, EmptyByAssertion set   -> a genuinely empty directory
//	no entries, NO assertion           -> unresolved (unreadable)
//
// That last case is what a confused or hurried foreign provider sends when it
// read nothing: an empty, default-constructed message. Reading it as an empty
// DIRECTORY reopens, at the one boundary where the host cannot inspect the
// sender's code, precisely the conflation roster.Resolution exists to
// eliminate — and the host operation downstream of it removes access for
// everybody the list does not mention. Emptiness has to be ASSERTED, never
// inferred, or the dangerous meaning is the one an author reaches by accident.
//
// The host does not merely refuse the value: roster.CheckPrincipals turns the
// unresolved reading into a NAMED fault, so the operator learns which connector
// did it rather than watching an estate lose its access.
func PrincipalRosterFromPB(pb *connectorpb.PrincipalRoster) roster.Resolution[roster.Principal] {
	if pb == nil {
		return roster.Resolution[roster.Principal]{}
	}
	if len(pb.GetPrincipals()) == 0 {
		return emptyOrUnresolved[roster.Principal](pb.GetEmptyByAssertion())
	}
	items := make([]roster.Principal, len(pb.GetPrincipals()))
	for i, p := range pb.GetPrincipals() {
		items[i] = PrincipalFromPB(p)
	}
	return resolved(items)
}

// GroupRosterToPB converts a resolution of groups to the wire, under
// [PrincipalRosterToPB]'s presence rules.
func GroupRosterToPB(r roster.Resolution[roster.Group]) *connectorpb.GroupRoster {
	items, ok := readable(r)
	if !ok {
		return nil
	}
	if len(items) == 0 {
		return &connectorpb.GroupRoster{EmptyByAssertion: true}
	}
	out := make([]*connectorpb.Group, len(items))
	for i, g := range items {
		out[i] = GroupToPB(g)
	}
	return &connectorpb.GroupRoster{Groups: out}
}

// GroupRosterFromPB converts a wire group list back to a resolution, under
// [PrincipalRosterFromPB]'s four cases.
func GroupRosterFromPB(pb *connectorpb.GroupRoster) roster.Resolution[roster.Group] {
	if pb == nil {
		return roster.Resolution[roster.Group]{}
	}
	if len(pb.GetGroups()) == 0 {
		return emptyOrUnresolved[roster.Group](pb.GetEmptyByAssertion())
	}
	items := make([]roster.Group, len(pb.GetGroups()))
	for i, g := range pb.GetGroups() {
		items[i] = GroupFromPB(g)
	}
	return resolved(items)
}

// MembershipRosterToPB converts a resolution of memberships to the wire, under
// [PrincipalRosterToPB]'s presence rules.
func MembershipRosterToPB(r roster.Resolution[roster.Membership]) *connectorpb.MembershipRoster {
	items, ok := readable(r)
	if !ok {
		return nil
	}
	if len(items) == 0 {
		return &connectorpb.MembershipRoster{EmptyByAssertion: true}
	}
	out := make([]*connectorpb.Membership, len(items))
	for i, m := range items {
		out[i] = MembershipToPB(m)
	}
	return &connectorpb.MembershipRoster{Memberships: out}
}

// MembershipRosterFromPB converts a wire membership list back to a resolution,
// under [PrincipalRosterFromPB]'s four cases.
func MembershipRosterFromPB(pb *connectorpb.MembershipRoster) roster.Resolution[roster.Membership] {
	if pb == nil {
		return roster.Resolution[roster.Membership]{}
	}
	if len(pb.GetMemberships()) == 0 {
		return emptyOrUnresolved[roster.Membership](pb.GetEmptyByAssertion())
	}
	items := make([]roster.Membership, len(pb.GetMemberships()))
	for i, m := range pb.GetMemberships() {
		items[i] = MembershipFromPB(m)
	}
	return resolved(items)
}

// readable reports the entries a resolution carries, and whether it carries
// any at all. It is the sending side's single presence decision, shared by all
// three list types so that one of them cannot quietly acquire a different rule.
func readable[T any](r roster.Resolution[T]) ([]T, bool) {
	items, err := r.Items()
	if err != nil {
		// Nothing was resolved. Sending an empty list here is exactly the
		// conflation the contract forbids, so nothing is sent.
		return nil, false
	}
	return items, true
}

// emptyOrUnresolved is the receiving side's reading of a present-but-empty
// list: an empty DIRECTORY only where the sender asserted one, and unresolved
// otherwise.
func emptyOrUnresolved[T any](asserted bool) roster.Resolution[T] {
	if !asserted {
		return roster.Resolution[T]{}
	}
	return roster.EmptyRoster[T]()
}

// resolved rebuilds a non-empty list host-side.
//
// roster.Complete is the only completeness this wire can carry, and that is
// deliberate rather than a simplification: a read that stopped early is a typed
// failure on this contract, not a smaller success, so there is no encoding for
// one to arrive in. See roster.Completeness and the "There is no way to say
// partial" section of roster.proto.
func resolved[T any](items []T) roster.Resolution[T] {
	res, err := roster.Resolved(items, roster.Complete)
	if err != nil {
		// Unreachable: items is non-empty and the completeness is Complete.
		// Returning the zero Resolution keeps the failure unreadable rather
		// than inventing a list.
		return roster.Resolution[T]{}
	}
	return res
}
