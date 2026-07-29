package plugin

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// The host-side stub dispensed by RosterPlugin's GRPCClient must satisfy
// roster.Roster.
var _ roster.Roster = (*rosterGRPCClient)(nil)

const (
	// populatedGroup has members; emptyGroup exists and has none; absentGroup
	// does not exist at all. The last two are the pair the wire has to keep
	// apart — see TestAbsentGroupIsNotAnEmptyMembershipList.
	populatedGroup = "grp-engineering"
	emptyGroup     = "grp-just-created"
	absentGroup    = "grp-no-such-thing"

	activePrincipal    = "usr-0001"
	suspendedPrincipal = "usr-0002"
	hostVersion        = "0.4.0"
)

// memRoster is a conformant in-test Roster: a fixed, placeholder-only
// directory holding one active and one SUSPENDED principal, two groups, and
// memberships for one of them.
type memRoster struct {
	// caps is what this connector declares at runtime.
	caps connector.Capabilities
	// machine, when set, adds a machine principal to the directory — used to
	// drive the undeclared-machine-principal guard across the wire.
	machine bool
	// closed records that Close reached the implementation.
	closed bool
}

func (m *memRoster) ListPrincipals(_ context.Context) (roster.Resolution[roster.Principal], error) {
	people := []roster.Principal{
		{
			ID: activePrincipal, Handle: "ada", Email: "ada@example.invalid",
			DisplayName: "Ada", Active: true, Kind: roster.PrincipalHuman,
		},
		{
			// Suspended, and therefore STILL REPORTED. Omitting this is what
			// tells a host somebody left the organisation.
			ID: suspendedPrincipal, Handle: "grace", Email: "grace@example.invalid",
			DisplayName: "Grace", Active: false, Kind: roster.PrincipalHuman,
		},
	}
	if m.machine {
		people = append(people, roster.Principal{
			ID: "svc-0001", Handle: "ci-bot", DisplayName: "CI", Active: true, Kind: roster.PrincipalMachine,
		})
	}
	return roster.Resolved(people, roster.Complete)
}

func (m *memRoster) ListGroups(_ context.Context) (roster.Resolution[roster.Group], error) {
	return roster.Resolved([]roster.Group{
		{ID: populatedGroup, Handle: "engineering", DisplayName: "Engineering", ParentIDs: []string{"grp-all"}},
		{ID: emptyGroup, Handle: "just-created", DisplayName: "Just Created"},
	}, roster.Complete)
}

func (m *memRoster) ListMemberships(_ context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	switch groupID {
	case populatedGroup:
		return roster.Resolved([]roster.Membership{
			{PrincipalID: activePrincipal, GroupID: populatedGroup, Direct: true},
			{PrincipalID: suspendedPrincipal, GroupID: populatedGroup, Direct: true},
		}, roster.Complete)
	case emptyGroup:
		// A real, verifiable state: the group exists and nobody is in it.
		return roster.EmptyRoster[roster.Membership](), nil
	default:
		// A group that does not exist is NOT an empty roster.
		return roster.Resolution[roster.Membership]{}, cerr.New(cerr.KindNotFound, "ListMemberships", nil)
	}
}

func (m *memRoster) Implements() connector.Class { return connector.ClassRoster }

func (m *memRoster) Capabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), m.caps...)
}

func (m *memRoster) Health(_ context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy, Detail: "placeholder directory"}, nil
}

func (m *memRoster) Close() error { m.closed = true; return nil }

var _ roster.Roster = (*memRoster)(nil)

// rosterManifest is the provider manifest a host in these tests has read.
func rosterManifest(caps ...connector.Capability) manifest.Doc {
	return manifest.Doc{
		Name:           "placeholder-roster-provider",
		Implements:     connector.ClassRoster,
		Kind:           manifest.KindProvider,
		MinHostVersion: "0.4.0",
		Capabilities:   caps,
	}
}

// newTestRosterClient stands up an in-process go-plugin gRPC server/client
// pair (no subprocess) serving impl, dispenses the Roster plugin, and returns
// it already asserted to roster.Roster.
//
// One RosterPlugin value serves both sides in-process, so it carries the
// provider's Impl and the host's negotiation inputs at once. A real
// deployment builds the two maps separately, with RosterPluginMap and
// RosterHostPluginMap.
func newTestRosterClient(t *testing.T, impl roster.Roster, m manifest.Doc) roster.Roster {
	t.Helper()
	raw := dispense(t, &RosterPlugin{
		Impl: impl, Name: m.Name, ProviderManifest: m, HostVersion: hostVersion,
	})
	c, ok := raw.(roster.Roster)
	if !ok {
		t.Fatalf("dispensed value %T does not implement roster.Roster", raw)
	}
	return c
}

// dispense wires plug in-process and returns whatever Dispense hands back.
func dispense(t *testing.T, plug *RosterPlugin) interface{} {
	t.Helper()
	// See newTestClient's Cleanup comment in plugin_test.go: client.Close()
	// alone tears the in-process server down, and a second Stop() races
	// go-plugin's own shutdown handler under -race.
	client, _ := goplugin.TestPluginGRPCConn(t, false, map[string]goplugin.Plugin{RosterName: plug})
	t.Cleanup(func() { _ = client.Close() })

	raw, err := client.Dispense(RosterName)
	if err != nil {
		t.Fatalf("Dispense(%q): %v", RosterName, err)
	}
	return raw
}

// rawRosterProvider serves ARBITRARY wire responses, including ones no
// conformant Go connector can produce. It is how the encoding itself gets
// tested: roster.Resolution refuses in-process to report a success carrying
// no list, so the only way to send that shape is to bypass the Go type and
// write the protobuf directly — which is exactly what a foreign provider, or
// a provider written against a naive reading of the proto, does.
type rawRosterProvider struct {
	connectorpb.UnimplementedRosterServer
	caps        []string
	principals  *connectorpb.ListPrincipalsResponse
	groups      *connectorpb.ListGroupsResponse
	memberships *connectorpb.ListMembershipsResponse
}

func (p *rawRosterProvider) ListPrincipals(context.Context, *connectorpb.ListPrincipalsRequest) (*connectorpb.ListPrincipalsResponse, error) {
	return p.principals, nil
}

func (p *rawRosterProvider) ListGroups(context.Context, *connectorpb.ListGroupsRequest) (*connectorpb.ListGroupsResponse, error) {
	return p.groups, nil
}

func (p *rawRosterProvider) ListMemberships(context.Context, *connectorpb.ListMembershipsRequest) (*connectorpb.ListMembershipsResponse, error) {
	return p.memberships, nil
}

func (p *rawRosterProvider) Capabilities(context.Context, *connectorpb.CapabilitiesRequest) (*connectorpb.CapabilitiesResponse, error) {
	return &connectorpb.CapabilitiesResponse{Capabilities: p.caps}, nil
}

func (p *rawRosterProvider) Health(context.Context, *connectorpb.HealthRequest) (*connectorpb.HealthResponse, error) {
	return &connectorpb.HealthResponse{}, nil
}

func (p *rawRosterProvider) Close(context.Context, *connectorpb.CloseRequest) (*connectorpb.CloseResponse, error) {
	return &connectorpb.CloseResponse{}, nil
}

// dialRawRoster serves p over a real gRPC connection and returns the host stub
// RosterPlugin dispenses for it — the same value a host gets from Dispense,
// not a stand-in.
func dialRawRoster(t *testing.T, p *rawRosterProvider, name string) roster.Roster {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	connectorpb.RegisterRosterServer(srv, p)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	m := rosterManifest()
	m.Name = name
	plug := &RosterPlugin{Name: name, ProviderManifest: m, HostVersion: hostVersion}
	raw, err := plug.GRPCClient(context.Background(), nil, conn)
	if err != nil {
		t.Fatalf("GRPCClient: %v", err)
	}
	c, ok := raw.(roster.Roster)
	if !ok {
		t.Fatalf("dispensed %T, which does not implement roster.Roster", raw)
	}
	return c
}

// assertRosterFaultNamed requires err to be a *roster.FaultError attributed to
// name, reporting an unresolved roster.
func assertRosterFaultNamed(t *testing.T, name string, err error) {
	t.Helper()
	var fe *roster.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T (%v), want a *roster.FaultError", err, err)
	}
	if fe.Fault != roster.FaultUnresolved {
		t.Fatalf("Fault = %q, want %q", fe.Fault, roster.FaultUnresolved)
	}
	if fe.Connector != name {
		t.Fatalf("fault names connector %q, want %q — an unattributed fault sends the operator looking in the directory", fe.Connector, name)
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want %v", cerr.KindOf(err), cerr.KindContractViolation)
	}
}

// TestUnresolvedRosterDoesNotCrossAsAnEmptyDirectory is the single most
// important test of this wire, and it is the one a naive protobuf mapping
// fails.
//
// A protobuf `repeated` field DEFAULTS TO EMPTY. Mapped straight onto a
// roster.Resolution, "the provider sent no entries" and "the directory
// genuinely holds nobody" become the same bytes — proto3 does not put an
// empty repeated field on the wire at all, so there is nothing left to tell
// them apart. The in-process contract refuses that conflation with a type
// (roster.Resolution has no constructor producing a readable empty list), and
// a naive mapping hands it straight back at the one boundary where the host
// cannot inspect the sender's code.
//
// What that costs is the reason this test exists rather than a comment: a host
// sweeping for departed principals computes "in arqtos, no longer in the
// directory" and revokes it. Fed an empty list that actually meant "the read
// failed", it deprovisions the entire estate — correctly, according to
// everything it was told.
//
// The fix mirrors credential.Resolution's exactly rather than inventing a
// second pattern: the list travels inside a message whose PRESENCE means
// "resolved at all", and a present-but-empty list means an empty directory
// only when the provider ASSERTS it. The encoding a confused author produces
// by accident — a default-constructed response — therefore means unresolved,
// which is the safe reading.
func TestUnresolvedRosterDoesNotCrossAsAnEmptyDirectory(t *testing.T) {
	const name = "rogue-provider"

	// What a provider that resolved nothing sends by accident: a
	// default-constructed response. Under a naive `repeated` mapping this is
	// indistinguishable from an empty directory.
	t.Run("principals", func(t *testing.T) {
		c := dialRawRoster(t, &rawRosterProvider{principals: &connectorpb.ListPrincipalsResponse{}}, name)
		res, err := c.ListPrincipals(context.Background())
		if err == nil {
			items, ierr := res.Items()
			t.Fatalf("a default-constructed ListPrincipalsResponse was read as a readable roster of %d entries (err=%v); "+
				"an unresolved read must not arrive as an empty directory", len(items), ierr)
		}
		assertRosterFaultNamed(t, name, err)
	})

	t.Run("groups", func(t *testing.T) {
		c := dialRawRoster(t, &rawRosterProvider{groups: &connectorpb.ListGroupsResponse{}}, name)
		res, err := c.ListGroups(context.Background())
		if err == nil {
			items, ierr := res.Items()
			t.Fatalf("a default-constructed ListGroupsResponse was read as a readable roster of %d entries (err=%v)", len(items), ierr)
		}
		assertRosterFaultNamed(t, name, err)
	})

	t.Run("memberships", func(t *testing.T) {
		c := dialRawRoster(t, &rawRosterProvider{memberships: &connectorpb.ListMembershipsResponse{}}, name)
		res, err := c.ListMemberships(context.Background(), populatedGroup)
		if err == nil {
			items, ierr := res.Items()
			t.Fatalf("a default-constructed ListMembershipsResponse was read as a readable roster of %d entries (err=%v)", len(items), ierr)
		}
		assertRosterFaultNamed(t, name, err)
	})

	// A nil response body is the same answer, reached a different way.
	t.Run("a nil response body", func(t *testing.T) {
		c := dialRawRoster(t, &rawRosterProvider{principals: nil}, name)
		_, err := c.ListPrincipals(context.Background())
		if err == nil {
			t.Fatalf("a nil ListPrincipalsResponse must not read as an empty directory")
		}
		assertRosterFaultNamed(t, name, err)
	})
}

// TestConformantRosterCrossesTheWireIntact is the other half: everything the
// guards refuse above must still get through when it is honest, or the wire is
// merely broken in the safe direction.
func TestConformantRosterCrossesTheWireIntact(t *testing.T) {
	impl := &memRoster{caps: connector.Capabilities{roster.CapWatch}}
	c := newTestRosterClient(t, impl, rosterManifest())
	ctx := context.Background()

	principals, err := c.ListPrincipals(ctx)
	if err != nil {
		t.Fatalf("ListPrincipals over the wire: %v", err)
	}
	people, err := principals.Items()
	if err != nil {
		t.Fatalf("reading principals: %v", err)
	}
	if len(people) != 2 {
		t.Fatalf("got %d principals, want 2", len(people))
	}

	groups, err := c.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups over the wire: %v", err)
	}
	gs, err := groups.Items()
	if err != nil {
		t.Fatalf("reading groups: %v", err)
	}
	if len(gs) != 2 {
		t.Fatalf("got %d groups, want 2", len(gs))
	}
	// ParentIDs is the only slice-valued field across the three types, so it
	// is the one that can be silently dropped by a per-field mapping.
	if len(gs[0].ParentIDs) != 1 || gs[0].ParentIDs[0] != "grp-all" {
		t.Fatalf("group ParentIDs = %v, want [grp-all]", gs[0].ParentIDs)
	}

	members, err := c.ListMemberships(ctx, populatedGroup)
	if err != nil {
		t.Fatalf("ListMemberships over the wire: %v", err)
	}
	ms, err := members.Items()
	if err != nil {
		t.Fatalf("reading memberships: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("got %d memberships, want 2", len(ms))
	}

	if got := c.Implements(); got != connector.ClassRoster {
		t.Fatalf("Implements() = %v, want %v", got, connector.ClassRoster)
	}
	if !c.Capabilities().Has(roster.CapWatch) {
		t.Fatalf("Capabilities() = %v, want it to carry %v", c.Capabilities(), roster.CapWatch)
	}
	h, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("Health over the wire: %v", err)
	}
	if h.Status != connector.Healthy || h.Detail != "placeholder directory" {
		t.Fatalf("Health = %+v, want Healthy with the provider's detail", h)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close over the wire: %v", err)
	}
	if !impl.closed {
		t.Fatal("Close did not reach the provider implementation; a Roster provider holds a directory session it must release")
	}
}

// TestSuspendedPrincipalSurvivesTheBoundary pins the most destructive way a
// per-field mapping can be wrong.
//
// A suspended user is STILL IN THE DIRECTORY, reported with Active false. A
// wire that drops the flag — or a mapping that omits inactive principals —
// tells the host the person left the organisation, and the host revokes
// everything belonging to somebody who is on parental leave. `false` is also
// proto3's default, so this is precisely the field a mapping can lose without
// any error anywhere.
func TestSuspendedPrincipalSurvivesTheBoundary(t *testing.T) {
	c := newTestRosterClient(t, &memRoster{}, rosterManifest())

	res, err := c.ListPrincipals(context.Background())
	if err != nil {
		t.Fatalf("ListPrincipals: %v", err)
	}
	people, err := res.Items()
	if err != nil {
		t.Fatalf("reading principals: %v", err)
	}

	var found bool
	for _, p := range people {
		if p.ID != suspendedPrincipal {
			continue
		}
		found = true
		if p.Active {
			t.Fatalf("principal %s arrived with Active true; a suspended identity reported as active is a person "+
				"whose access is never withdrawn", p.ID)
		}
		if p.Handle != "grace" || p.DisplayName != "Grace" || p.Email != "grace@example.invalid" {
			t.Fatalf("suspended principal lost fields across the wire: %+v", p)
		}
		if p.Kind != roster.PrincipalHuman {
			t.Fatalf("principal Kind = %v, want %v", p.Kind, roster.PrincipalHuman)
		}
	}
	if !found {
		t.Fatalf("the suspended principal %s is ABSENT from the list that crossed the wire. Suspended is not absent: "+
			"omitting them tells the host they left, and the host then revokes everything they had", suspendedPrincipal)
	}
}

// TestAbsentGroupIsNotAnEmptyMembershipList is the cerr half of the same
// distinction, one level up: "there is no such group" and "this group has no
// members" lead a reconcile loop to opposite conclusions, and both must
// survive the boundary as themselves.
func TestAbsentGroupIsNotAnEmptyMembershipList(t *testing.T) {
	c := newTestRosterClient(t, &memRoster{}, rosterManifest())
	ctx := context.Background()

	t.Run("a group that does not exist fails NotFound", func(t *testing.T) {
		res, err := c.ListMemberships(ctx, absentGroup)
		if err == nil {
			items, ierr := res.Items()
			t.Fatalf("listing an absent group returned no error (%d items, err=%v); an empty roster is not the answer "+
				"to a group that does not exist — a reconcile loop given one removes the access that group carried",
				len(items), ierr)
		}
		if got := cerr.KindOf(err); got != cerr.KindNotFound {
			t.Fatalf("KindOf = %v, want %v — the classification is what a host acts on, and it must not collapse "+
				"into an empty list across the wire", got, cerr.KindNotFound)
		}
	})

	t.Run("a group that exists and is empty resolves to an empty list", func(t *testing.T) {
		res, err := c.ListMemberships(ctx, emptyGroup)
		if err != nil {
			t.Fatalf("an existing, empty group must resolve rather than fail: %v", err)
		}
		items, err := res.Items()
		if err != nil {
			t.Fatalf("an asserted-empty membership list must stay READABLE across the wire: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("got %d memberships, want 0", len(items))
		}
	})
}

// TestMembershipForAnotherGroupIsRefused covers the correspondence between
// what was asked and what came back. A host cannot attribute a membership for
// a group it did not ask about, and attributing it to the group it did ask
// about is how people end up in groups they are not in.
func TestMembershipForAnotherGroupIsRefused(t *testing.T) {
	const name = "misdirecting-provider"
	c := dialRawRoster(t, &rawRosterProvider{
		memberships: membershipResponse(&connectorpb.Membership{
			PrincipalId: activePrincipal,
			// The wrong group.
			GroupId: emptyGroup,
			Direct:  true,
		}),
	}, name)

	_, err := c.ListMemberships(context.Background(), populatedGroup)
	if err == nil {
		t.Fatalf("a membership for %q returned for a request about %q must be refused", emptyGroup, populatedGroup)
	}
	var fe *roster.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T (%v), want a *roster.FaultError", err, err)
	}
	if fe.Fault != roster.FaultMembershipMismatch {
		t.Fatalf("Fault = %q, want %q", fe.Fault, roster.FaultMembershipMismatch)
	}
	if fe.Connector != name {
		t.Fatalf("fault names connector %q, want %q", fe.Connector, name)
	}
	if !strings.Contains(fe.Error(), populatedGroup) {
		t.Fatalf("the fault must name the group that was requested: %v", fe)
	}
}

// TestUndeclaredMachinePrincipalIsRefusedAcrossTheWire proves the host-side
// capability guard is applied to what a PROVIDER sends, not only to a native
// connector. The declaration is what makes the absence of machine principals
// readable; one reported without it makes that reading wrong for every
// connector.
func TestUndeclaredMachinePrincipalIsRefusedAcrossTheWire(t *testing.T) {
	t.Run("undeclared is refused", func(t *testing.T) {
		c := newTestRosterClient(t, &memRoster{machine: true}, rosterManifest())
		_, err := c.ListPrincipals(context.Background())
		if err == nil {
			t.Fatal("a machine principal from a connector that does not declare machine_principals must be refused")
		}
		var fe *roster.FaultError
		if !errors.As(err, &fe) || fe.Fault != roster.FaultUndeclaredMachinePrincipal {
			t.Fatalf("error = %v, want %q", err, roster.FaultUndeclaredMachinePrincipal)
		}
	})

	t.Run("declared crosses intact", func(t *testing.T) {
		caps := connector.Capabilities{roster.CapMachinePrincipals}
		c := newTestRosterClient(t, &memRoster{caps: caps, machine: true}, rosterManifest(caps...))
		res, err := c.ListPrincipals(context.Background())
		if err != nil {
			t.Fatalf("a declared machine principal must cross the wire: %v", err)
		}
		people, err := res.Items()
		if err != nil {
			t.Fatalf("reading principals: %v", err)
		}
		var machines int
		for _, p := range people {
			if p.Kind == roster.PrincipalMachine {
				machines++
			}
		}
		if machines != 1 {
			t.Fatalf("got %d machine principals across the wire, want 1 — PrincipalKind did not survive", machines)
		}
	})
}

// TestMinHostVersionNegotiationRefusesAnIncompatibleProvider covers the gate
// at dial time. min_host_version was, before this, a string a provider author
// wrote and nothing ever read — a declaration with no consequence.
func TestMinHostVersionNegotiationRefusesAnIncompatibleProvider(t *testing.T) {
	requires := func(min string) manifest.Doc {
		m := rosterManifest()
		m.MinHostVersion = min
		return m
	}

	t.Run("a provider that needs a newer host is refused", func(t *testing.T) {
		raw := dispenseErr(t, &RosterPlugin{
			Impl: &memRoster{}, Name: "future-provider",
			ProviderManifest: requires("9.0.0"), HostVersion: hostVersion,
		})
		if raw == nil {
			t.Fatal("dispensing a provider that requires host 9.0.0 against a 0.4.0 host must fail")
		}
		if got := cerr.KindOf(raw); got != cerr.KindUnsupported {
			t.Fatalf("KindOf = %v, want %v", got, cerr.KindUnsupported)
		}
		if !strings.Contains(raw.Error(), "9.0.0") || !strings.Contains(raw.Error(), hostVersion) {
			t.Fatalf("the refusal must name both versions: %v", raw)
		}
	})

	t.Run("an equal host version is enough", func(t *testing.T) {
		if err := dispenseErr(t, &RosterPlugin{
			Impl: &memRoster{}, Name: "exact-provider",
			ProviderManifest: requires(hostVersion), HostVersion: hostVersion,
		}); err != nil {
			t.Fatalf("a host at exactly min_host_version must be accepted: %v", err)
		}
	})

	t.Run("a newer host is enough", func(t *testing.T) {
		if err := dispenseErr(t, &RosterPlugin{
			Impl: &memRoster{}, Name: "old-provider",
			ProviderManifest: requires("0.1.0"), HostVersion: hostVersion,
		}); err != nil {
			t.Fatalf("a host newer than min_host_version must be accepted: %v", err)
		}
	})

	// Fail-closed, in every direction where the gate cannot be evaluated.
	for _, tc := range []struct {
		name string
		plug *RosterPlugin
	}{
		{"no host version", &RosterPlugin{Impl: &memRoster{}, ProviderManifest: requires("0.1.0")}},
		{"no provider manifest", &RosterPlugin{Impl: &memRoster{}, HostVersion: hostVersion}},
		{"an unparseable min_host_version", &RosterPlugin{
			Impl: &memRoster{}, ProviderManifest: requires("v1.2"), HostVersion: hostVersion,
		}},
		{"an unparseable host version", &RosterPlugin{
			Impl: &memRoster{}, ProviderManifest: requires("0.1.0"), HostVersion: "latest",
		}},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			if err := dispenseErr(t, tc.plug); err == nil {
				t.Fatal("a negotiation that cannot be evaluated must be refused, not assumed satisfied")
			}
		})
	}
}

// dispenseErr wires plug in-process and returns the error Dispense produced
// (nil when it succeeded), rather than failing the test on it.
func dispenseErr(t *testing.T, plug *RosterPlugin) error {
	t.Helper()
	client, _ := goplugin.TestPluginGRPCConn(t, false, map[string]goplugin.Plugin{RosterName: plug})
	t.Cleanup(func() { _ = client.Close() })
	_, err := client.Dispense(RosterName)
	return err
}

// TestRosterHostPluginMapCarriesTheNegotiation pins that the host-side
// constructor is the one that gates: a map built by it refuses an
// incompatible provider, and a map built by the provider-side constructor
// carries an Impl and no negotiation inputs.
func TestRosterHostPluginMapCarriesTheNegotiation(t *testing.T) {
	m := rosterManifest()
	host := RosterHostPluginMap("dialled-provider", m, hostVersion)
	plug, ok := host[RosterName].(*RosterPlugin)
	if !ok {
		t.Fatalf("host map holds %T under %q, want a *RosterPlugin", host[RosterName], RosterName)
	}
	if plug.HostVersion != hostVersion || plug.ProviderManifest.Name != m.Name || plug.Name != "dialled-provider" {
		t.Fatalf("host plugin lost its negotiation inputs: %+v", plug)
	}
	if plug.Impl != nil {
		t.Fatal("the host side must carry no Impl: it dials, it does not serve")
	}

	impl := &memRoster{}
	provider, ok := RosterPluginMap(impl)[RosterName].(*RosterPlugin)
	if !ok {
		t.Fatalf("provider map holds the wrong type under %q", RosterName)
	}
	if provider.Impl != roster.Roster(impl) {
		t.Fatal("the provider side must carry the implementation it serves")
	}
}

// membershipResponse builds a membership response carrying ms, asserting
// nothing about emptiness. It keeps the tests above independent of which
// message the list travels inside.
func membershipResponse(ms ...*connectorpb.Membership) *connectorpb.ListMembershipsResponse {
	return &connectorpb.ListMembershipsResponse{Roster: &connectorpb.MembershipRoster{Memberships: ms}}
}

// TestPrincipalKindNumbersMatchTheGoVocabulary is the drift gate on the one
// enum that crosses this wire as a NUMBER.
//
// roster.PrincipalKind is the single source for the closed vocabulary, and the
// wire carries its numbering rather than a second, parallel enum that would
// have to be kept in step by hand. What makes that safe is this test: every
// kind in the published vocabulary round-trips to itself, so renumbering the
// Go constants without renumbering the wire is caught here rather than by a
// host reading a human as a machine.
func TestPrincipalKindNumbersMatchTheGoVocabulary(t *testing.T) {
	for _, k := range roster.PrincipalKinds() {
		p := roster.Principal{ID: "x", Kind: k}
		got := principalRoundTrip(t, p)
		if got.Kind != k {
			t.Fatalf("PrincipalKind %v round-tripped to %v", k, got.Kind)
		}
	}

	t.Run("a kind outside the vocabulary arrives unclassified", func(t *testing.T) {
		// A foreign provider that invents a kind number must not have it read
		// as a classification. Unknown is the safe default: a host that
		// treats machines differently from humans then knows it must apply
		// neither rule.
		c := dialRawRoster(t, &rawRosterProvider{
			principals: &connectorpb.ListPrincipalsResponse{
				Roster: &connectorpb.PrincipalRoster{
					Principals: []*connectorpb.Principal{{Id: "x", Active: true, Kind: 99}},
				},
			},
		}, "inventive-provider")
		res, err := c.ListPrincipals(context.Background())
		if err != nil {
			t.Fatalf("ListPrincipals: %v", err)
		}
		people, err := res.Items()
		if err != nil {
			t.Fatalf("reading principals: %v", err)
		}
		if people[0].Kind != roster.PrincipalUnknown {
			t.Fatalf("kind 99 arrived as %v, want %v", people[0].Kind, roster.PrincipalUnknown)
		}
	})
}

// principalRoundTrip sends p across a real gRPC connection and returns what
// arrived.
func principalRoundTrip(t *testing.T, p roster.Principal) roster.Principal {
	t.Helper()
	caps := connector.Capabilities{roster.CapMachinePrincipals}
	c := newTestRosterClient(t, &staticRoster{
		memRoster: memRoster{caps: caps},
		people:    []roster.Principal{p},
	}, rosterManifest(caps...))
	res, err := c.ListPrincipals(context.Background())
	if err != nil {
		t.Fatalf("ListPrincipals: %v", err)
	}
	people, err := res.Items()
	if err != nil {
		t.Fatalf("reading principals: %v", err)
	}
	if len(people) != 1 {
		t.Fatalf("got %d principals, want 1", len(people))
	}
	return people[0]
}

// staticRoster serves exactly the principals it is given.
type staticRoster struct {
	memRoster
	people []roster.Principal
}

func (s *staticRoster) ListPrincipals(_ context.Context) (roster.Resolution[roster.Principal], error) {
	return roster.Resolved(s.people, roster.Complete)
}

var _ roster.Roster = (*staticRoster)(nil)
