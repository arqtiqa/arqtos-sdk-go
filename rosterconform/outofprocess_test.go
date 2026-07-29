package rosterconform_test

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
	"github.com/arqtiqa/arqtos-sdk-go/rosterconform"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

const hostVersion = "0.4.0"

// providerManifest is manifestFor with the runtime kind an out-of-process
// connector ships as, which is what brings min_host_version into play.
func providerManifest(caps ...connector.Capability) manifest.Doc {
	m := manifestFor(caps...)
	m.Kind = manifest.KindProvider
	m.MinHostVersion = "0.1.0"
	return m
}

// TestRunOutOfProcessRefusesARunItCannotCarryOut covers the inputs that make
// the run impossible rather than non-conformant. Each returns an error and no
// report: a Report full of failures would blame the connector for something
// the caller or the environment did.
func TestRunOutOfProcessRefusesARunItCannotCarryOut(t *testing.T) {
	m := providerManifest()

	for _, tc := range []struct {
		name string
		p    rosterconform.Provider
		want cerr.Kind
		says string
	}{
		{
			name: "no provider path",
			p:    rosterconform.Provider{HostVersion: hostVersion},
			want: cerr.KindInvalid,
			says: "Provider.Path",
		},
		{
			name: "no host version",
			p:    rosterconform.Provider{Path: "/nonexistent/roster-provider"},
			want: cerr.KindInvalid,
			says: "min_host_version",
		},
		{
			name: "a binary that will not launch",
			p: rosterconform.Provider{
				Path:        filepath.Join(t.TempDir(), "no-such-provider"),
				HostVersion: hostVersion,
			},
			want: cerr.KindUnavailable,
			says: "could not launch or dial",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := rosterconform.RunOutOfProcess(context.Background(), tc.p, rosterconform.Options{
				Manifest:           m,
				Group:              idGroup,
				AbsentGroup:        idNoGroup,
				SuspendedPrincipal: idSuspended,
			})
			if err == nil {
				t.Fatalf("want an error the run could not be carried out; got report %v", rep)
			}
			if got := cerr.KindOf(err); got != tc.want {
				t.Fatalf("KindOf = %v, want %v (%v)", got, tc.want, err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("error should mention %q: %v", tc.says, err)
			}
			if len(rep.Results) != 0 {
				t.Fatalf("a run that could not be carried out must report no checks, got %d", len(rep.Results))
			}
		})
	}
}

// TestAConnectorThatOnlyPassesInProcessFailsOverTheWire is the reason
// [rosterconform.RunOutOfProcess] exists, made falsifiable.
//
// The connector here is CONFORMANT. Its Go code returns the suspended
// principal, with Active false, exactly as the contract requires — and a
// rosterconform run against it in-process is fully green, as the first half of
// this test proves rather than assumes.
//
// What is broken is its ENCODER: the provider-side server drops deactivated
// identities on the way out. That is not a contrived fixture, it is the shape
// of a real Track-B bug — an author who wrote their own gRPC server, or a
// provider in another language whose SDK-equivalent loses a bool that is
// false, which is proto3's default and therefore the field a hand-written
// encoder loses most easily.
//
// In-process that bug cannot exist, because there is no encoder. So a
// connector shipped as an out-of-process provider and checked only in-process
// has had this entire class of failure go unexamined — and the failure it
// hides is the most destructive one this contract has: the host reads the
// suspended person as departed and revokes everything belonging to somebody
// who is on leave.
func TestAConnectorThatOnlyPassesInProcessFailsOverTheWire(t *testing.T) {
	ctx := context.Background()
	impl := &baseRoster{}
	m := providerManifest()
	o := opts(m)

	// Half one: the connector itself is conformant. If this ever goes red the
	// second half proves nothing, because the failure would not be the wire's.
	inProcess, err := rosterconform.Run(ctx, impl, o)
	if err != nil {
		t.Fatalf("the in-process run could not be carried out: %v", err)
	}
	if !inProcess.OK() {
		t.Fatalf("this fixture must be conformant in-process, or the wire comparison below means nothing:\n%s", inProcess)
	}
	if inProcess.Transport != rosterconform.TransportUnrecorded {
		t.Fatalf("Run recorded transport %q; it cannot know what is behind a roster.Roster and must say so",
			inProcess.Transport)
	}

	// Half two: the same connector, reached across a real gRPC boundary
	// through an encoder that drops deactivated identities.
	overTheWire, err := rosterconform.Run(ctx, dialRoster(t, &droppingRosterServer{faithfulRosterServer{impl: impl}}, m), o)
	if err != nil {
		t.Fatalf("the over-the-wire run could not be carried out: %v", err)
	}
	if overTheWire.OK() {
		t.Fatalf("a provider whose encoder drops deactivated identities must FAIL over the wire; the report is green:\n%s",
			overTheWire)
	}

	failed := make(map[string]string, len(overTheWire.Failures()))
	for _, f := range overTheWire.Failures() {
		failed[f.Name] = f.Detail
	}
	detail, ok := failed[rosterconform.CheckSuspendedIsPresent]
	if !ok {
		t.Fatalf("the wire run failed, but not on %s — so this test is not demonstrating what it claims:\n%s",
			rosterconform.CheckSuspendedIsPresent, overTheWire)
	}
	if !strings.Contains(detail, "ABSENT") {
		t.Fatalf("%s should report the principal as absent: %s", rosterconform.CheckSuspendedIsPresent, detail)
	}
	// Exactly one property is broken, so exactly one check may flip. More than
	// one would mean the fixture is generally broken and the demonstration is
	// not attributable to the encoder.
	if len(failed) != 1 {
		t.Fatalf("want exactly one failed check (%s); got %d:\n%s",
			rosterconform.CheckSuspendedIsPresent, len(failed), overTheWire)
	}
}

// TestRunOverTheWireIsGreenForAFaithfulEncoder is the control for the test
// above: the same harness, the same connector, over the same boundary, with an
// encoder that loses nothing. Without this, "fails over the wire" could just
// mean the wire path is broken for everybody.
func TestRunOverTheWireIsGreenForAFaithfulEncoder(t *testing.T) {
	m := providerManifest()
	c := dialRoster(t, &faithfulRosterServer{impl: &baseRoster{}}, m)

	rep, err := rosterconform.Run(context.Background(), c, opts(m))
	if err != nil {
		t.Fatalf("the run could not be carried out: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("a faithful provider must pass every check across the wire:\n%s", rep)
	}
}

// dialRoster serves srv over a real gRPC connection and returns the host stub
// RosterPlugin dispenses for it — the same value a host gets from Dispense.
func dialRoster(t *testing.T, srv connectorpb.RosterServer, m manifest.Doc) roster.Roster {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	connectorpb.RegisterRosterServer(s, srv)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	plug := &plugin.RosterPlugin{Name: m.Name, ProviderManifest: m, HostVersion: hostVersion}
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

// faithfulRosterServer is a hand-written provider-side server that loses
// nothing — the control fixture. It is deliberately written against the
// exported transport helpers rather than reusing the SDK's own server, because
// what it stands in for is an author who wrote their own.
type faithfulRosterServer struct {
	connectorpb.UnimplementedRosterServer
	impl roster.Roster
}

func (s *faithfulRosterServer) ListPrincipals(ctx context.Context, _ *connectorpb.ListPrincipalsRequest) (*connectorpb.ListPrincipalsResponse, error) {
	res, err := s.impl.ListPrincipals(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.ListPrincipalsResponse{Roster: transport.PrincipalRosterToPB(res)}, nil
}

func (s *faithfulRosterServer) ListGroups(ctx context.Context, _ *connectorpb.ListGroupsRequest) (*connectorpb.ListGroupsResponse, error) {
	res, err := s.impl.ListGroups(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.ListGroupsResponse{Roster: transport.GroupRosterToPB(res)}, nil
}

func (s *faithfulRosterServer) ListMemberships(ctx context.Context, req *connectorpb.ListMembershipsRequest) (*connectorpb.ListMembershipsResponse, error) {
	res, err := s.impl.ListMemberships(ctx, req.GetGroupId())
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.ListMembershipsResponse{Roster: transport.MembershipRosterToPB(res)}, nil
}

func (s *faithfulRosterServer) Health(ctx context.Context, _ *connectorpb.HealthRequest) (*connectorpb.HealthResponse, error) {
	h, err := s.impl.Health(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.HealthResponse{Status: int32(h.Status), Detail: h.Detail}, nil
}

func (s *faithfulRosterServer) Capabilities(_ context.Context, _ *connectorpb.CapabilitiesRequest) (*connectorpb.CapabilitiesResponse, error) {
	caps := s.impl.Capabilities()
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return &connectorpb.CapabilitiesResponse{Capabilities: out}, nil
}

func (s *faithfulRosterServer) Close(_ context.Context, _ *connectorpb.CloseRequest) (*connectorpb.CloseResponse, error) {
	if err := s.impl.Close(); err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.CloseResponse{}, nil
}

// droppingRosterServer is faithfulRosterServer with ONE property broken: it
// omits deactivated identities when it encodes the principal list. Everything
// else is faithful, so a failing check is attributable to that property.
type droppingRosterServer struct {
	faithfulRosterServer
}

func (s *droppingRosterServer) ListPrincipals(ctx context.Context, _ *connectorpb.ListPrincipalsRequest) (*connectorpb.ListPrincipalsResponse, error) {
	res, err := s.impl.ListPrincipals(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	pb := transport.PrincipalRosterToPB(res)
	if pb != nil {
		kept := make([]*connectorpb.Principal, 0, len(pb.GetPrincipals()))
		for _, p := range pb.GetPrincipals() {
			if p.GetActive() {
				kept = append(kept, p)
			}
		}
		pb.Principals = kept
	}
	return &connectorpb.ListPrincipalsResponse{Roster: pb}, nil
}

var (
	_ connectorpb.RosterServer = (*faithfulRosterServer)(nil)
	_ connectorpb.RosterServer = (*droppingRosterServer)(nil)
)
