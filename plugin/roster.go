// This file is the Track-B (out-of-process) wiring for the Roster connector
// class, symmetric with the CredentialLoader wiring in plugin.go: a provider
// serves an implementation via goplugin.Serve using [RosterPluginMap]; a host
// dials it with [RosterHostPluginMap] and Dispense()s a value that itself
// satisfies roster.Roster.
//
// # What the host stub adds beyond calling the provider
//
// A provider is someone else's binary — possibly not written in Go, certainly
// not built against a conformant SDK by assumption. So the dispensed client is
// a GUARD as well as a transport. Everything a provider sends passes through
// the roster package's own host-side checks (roster.CheckPrincipals,
// roster.CheckMemberships, roster.CheckResolution) before a host sees it, so
// that a provider which reports success while reading nothing produces a NAMED
// contract fault instead of a directory with nobody in it — which is the return
// that makes a host revoke every access in the estate.
//
// # Watch is not on this wire, and that is a known gap
//
// roster.CapWatch and roster.Watcher have no RPC here, so a dispensed stub
// never satisfies roster.Watcher and an out-of-process Roster provider
// therefore CANNOT declare the watch capability: rosterconform's
// watch/declared-is-implemented check fails it in the "declared, not
// implemented" direction. An out-of-process provider polls, which is the
// fallback the capability's own contract already defines for a connector
// without it.
//
// Whoever adds the missing RPC has one constraint that is not stylistic:
// "implements roster.Watcher" must NOT be computed from the Capabilities RPC.
// rosterconform's declared-is-implemented checks are only worth anything
// because "declared" and "implemented" come from independent signals, and
// deriving the stub's Watcher-ness from the declaration would make that check
// agree with itself whatever the provider does. This SDK has already shipped
// that bug once, in the credential class's batch capability — see
// CredentialLoaderPlugin.GRPCClient and credconform.CheckBatchDeclared for
// what it cost. Probe the provider's BEHAVIOUR (does calling Watch establish a
// stream, or answer Unimplemented) if the stub's shape has to be conditional
// at all.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

// RosterName is the plugin key both the provider (server) and the host
// (client) register the Roster plugin under.
//
// It is the go-plugin DISPENSE KEY, not the connector class: a wire key in
// snake_case, alongside [CredentialLoaderName]. connector.Classes() remains
// the single source for the class vocabulary, and nothing here restates it —
// the class this binding is for is named exactly once, by
// rosterGRPCClient.Implements returning connector.ClassRoster.
const RosterName = "roster"

// capabilityProbeTimeout bounds the single Capabilities call a host makes at
// dial time. Dispense must not be able to hang on an unresponsive provider,
// and the call is one round trip to a subprocess that has already completed
// its handshake, so the budget is generous rather than tight.
const capabilityProbeTimeout = 10 * time.Second

// RosterPluginMap builds the map[string]goplugin.Plugin a Track-B PROVIDER
// passes to goplugin.ServeConfig.Plugins. impl is the provider's own
// roster.Roster implementation.
//
// A provider needs no manifest or host version here: it is not the side doing
// the dialling, and GRPCClient is never called on it.
func RosterPluginMap(impl roster.Roster) map[string]goplugin.Plugin {
	return map[string]goplugin.Plugin{
		RosterName: &RosterPlugin{Impl: impl},
	}
}

// RosterHostPluginMap builds the map[string]goplugin.Plugin a HOST passes to
// goplugin.ClientConfig.Plugins.
//
// It takes the negotiation inputs rather than leaving them optional, because
// this is the only host-side entry point: name is what a contract fault will
// be attributed to, provider is the connector.yml the host READ before
// launching anything, and hostVersion is the contract version this host
// implements. Dispensing refuses a provider whose min_host_version this host
// does not satisfy — see [RosterPlugin.GRPCClient].
func RosterHostPluginMap(name string, provider manifest.Doc, hostVersion string) map[string]goplugin.Plugin {
	return map[string]goplugin.Plugin{
		RosterName: &RosterPlugin{Name: name, ProviderManifest: provider, HostVersion: hostVersion},
	}
}

// RosterPlugin is the go-plugin GRPCPlugin for the Roster connector class.
// Impl is set on the server (provider) side; the remaining fields are
// host-side only, where only GRPCClient is ever called.
type RosterPlugin struct {
	goplugin.NetRPCUnsupportedPlugin

	// Impl is the provider's own implementation. Provider side only.
	Impl roster.Roster

	// Name is the host's name for the provider being dialled. It names the
	// connector in a contract fault raised by the dispensed client, so a
	// broken provider is attributable without the host re-wrapping every
	// call.
	Name string

	// ProviderManifest is the provider's connector.yml as the host read it,
	// and HostVersion is the contract version this host implements. Together
	// they are the min_host_version negotiation, and GRPCClient refuses to
	// dispense without both — see its doc for why that is fail-closed rather
	// than merely strict.
	ProviderManifest manifest.Doc
	HostVersion      string
}

var _ goplugin.GRPCPlugin = (*RosterPlugin)(nil)

// GRPCServer registers the Roster gRPC service against s, backed by p.Impl.
// Called once, on the provider (server) side.
func (p *RosterPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	connectorpb.RegisterRosterServer(s, &rosterGRPCServer{impl: p.Impl})
	return nil
}

// GRPCClient returns a roster.Roster host-stub backed by conn. Called on the
// host (client) side.
//
// It does two things before it hands anything back, and both are refusals:
//
//   - min_host_version NEGOTIATION. p.ProviderManifest.RequireHost(p.HostVersion)
//     must pass. A missing manifest or an empty host version is refused too:
//     the negotiation inputs are not optional, because a gate a caller can
//     omit is a gate that is off by default, and the failure it prevents —
//     running a connector against a contract it has already said it cannot
//     work with — is silent when it happens.
//
//   - a CAPABILITIES read. The stub's guards need to know what this connector
//     declares: whether a machine principal or an inherited membership is
//     something it is allowed to report at all. Capabilities are a static
//     declaration in this contract, so they are read ONCE here rather than on
//     every list call. A failed probe refuses the dial rather than assuming an
//     empty set: assuming would make every declared capability look
//     undeclared, and the resulting fault would be reported against the
//     provider for something the host failed to ask.
func (p *RosterPlugin) GRPCClient(ctx context.Context, _ *goplugin.GRPCBroker, conn *grpc.ClientConn) (interface{}, error) {
	if p.HostVersion == "" {
		return nil, cerr.New(cerr.KindInvalid, "Dispense", errors.New(
			"the host did not state its own contract version, so this provider's min_host_version cannot be "+
				"negotiated; dial with plugin.RosterHostPluginMap, which requires it"))
	}
	if p.ProviderManifest.Name == "" {
		return nil, cerr.New(cerr.KindInvalid, "Dispense", errors.New(
			"the host supplied no provider manifest, so this provider's min_host_version cannot be negotiated; "+
				"dial with plugin.RosterHostPluginMap, which requires it"))
	}
	if err := p.ProviderManifest.RequireHost(p.HostVersion); err != nil {
		return nil, cerr.New(cerr.KindUnsupported, "Dispense", err)
	}

	client := connectorpb.NewRosterClient(conn)
	caps, err := readCapabilities(ctx, client)
	if err != nil {
		return nil, cerr.New(cerr.KindUnavailable, "Dispense", fmt.Errorf(
			"could not read the capabilities of connector %q, and the host-side guards cannot be applied without "+
				"them: %w", p.Name, err))
	}
	return &rosterGRPCClient{client: client, name: p.Name, caps: caps}, nil
}

// readCapabilities performs the one dial-time Capabilities call.
func readCapabilities(ctx context.Context, client connectorpb.RosterClient) (connector.Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, capabilityProbeTimeout)
	defer cancel()
	resp, err := client.Capabilities(ctx, &connectorpb.CapabilitiesRequest{})
	if err != nil {
		return nil, transport.ErrFromStatus(err)
	}
	caps := make(connector.Capabilities, len(resp.GetCapabilities()))
	for i, s := range resp.GetCapabilities() {
		caps[i] = connector.Capability(s)
	}
	return caps, nil
}

// rosterGRPCServer adapts a roster.Roster implementation to the generated
// connectorpb.RosterServer interface, mapping every error through
// transport.ErrToStatus so the client reconstructs a *cerr.Error rather than a
// bare string.
//
// It does NOT check its own provider's conformance. A server that vetted its
// own returns would be the provider deciding it is conformant, which is the
// one party that cannot be asked — the checks live host-side, in
// rosterGRPCClient.
type rosterGRPCServer struct {
	connectorpb.UnimplementedRosterServer
	impl roster.Roster
}

func (s *rosterGRPCServer) ListPrincipals(ctx context.Context, _ *connectorpb.ListPrincipalsRequest) (*connectorpb.ListPrincipalsResponse, error) {
	res, err := s.impl.ListPrincipals(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.ListPrincipalsResponse{Roster: transport.PrincipalRosterToPB(res)}, nil
}

func (s *rosterGRPCServer) ListGroups(ctx context.Context, _ *connectorpb.ListGroupsRequest) (*connectorpb.ListGroupsResponse, error) {
	res, err := s.impl.ListGroups(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.ListGroupsResponse{Roster: transport.GroupRosterToPB(res)}, nil
}

func (s *rosterGRPCServer) ListMemberships(ctx context.Context, req *connectorpb.ListMembershipsRequest) (*connectorpb.ListMembershipsResponse, error) {
	res, err := s.impl.ListMemberships(ctx, req.GetGroupId())
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.ListMembershipsResponse{Roster: transport.MembershipRosterToPB(res)}, nil
}

func (s *rosterGRPCServer) Health(ctx context.Context, _ *connectorpb.HealthRequest) (*connectorpb.HealthResponse, error) {
	h, err := s.impl.Health(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.HealthResponse{Status: int32(h.Status), Detail: h.Detail}, nil
}

func (s *rosterGRPCServer) Capabilities(_ context.Context, _ *connectorpb.CapabilitiesRequest) (*connectorpb.CapabilitiesResponse, error) {
	caps := s.impl.Capabilities()
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return &connectorpb.CapabilitiesResponse{Capabilities: out}, nil
}

func (s *rosterGRPCServer) Close(_ context.Context, _ *connectorpb.CloseRequest) (*connectorpb.CloseResponse, error) {
	if err := s.impl.Close(); err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.CloseResponse{}, nil
}

// rosterGRPCClient is the host-side stub dispensed by RosterPlugin.GRPCClient:
// it satisfies roster.Roster by calling the provider over gRPC, rebuilding
// each returned list into a roster.Resolution and reconstructing errors via
// transport.ErrFromStatus.
//
// Every list return then passes through the roster package's host-side guard
// before the host sees it. That is the whole reason this type exists rather
// than a bare generated client: the guards are what turn a foreign provider's
// unreadable answer into a named fault instead of a directory with nobody in
// it.
type rosterGRPCClient struct {
	client connectorpb.RosterClient
	// name is the host's name for this connector, used to attribute a
	// contract fault.
	name string
	// caps is what the provider declared at dial time. See
	// RosterPlugin.GRPCClient for why it is read once.
	caps connector.Capabilities
}

var _ roster.Roster = (*rosterGRPCClient)(nil)

// ListPrincipals reads the directory's identities over the wire.
//
// The guard is roster.CheckPrincipals rather than the bare presence check: it
// additionally refuses a machine principal from a provider that has not
// declared it can see one, which is a judgement about what came back and not
// about what the provider says.
func (c *rosterGRPCClient) ListPrincipals(ctx context.Context) (roster.Resolution[roster.Principal], error) {
	resp, err := c.client.ListPrincipals(ctx, &connectorpb.ListPrincipalsRequest{})
	if err != nil {
		return roster.Resolution[roster.Principal]{}, transport.ErrFromStatus(err)
	}
	return roster.CheckPrincipals(c.name, c.caps, transport.PrincipalRosterFromPB(resp.GetRoster()), nil)
}

func (c *rosterGRPCClient) ListGroups(ctx context.Context) (roster.Resolution[roster.Group], error) {
	resp, err := c.client.ListGroups(ctx, &connectorpb.ListGroupsRequest{})
	if err != nil {
		return roster.Resolution[roster.Group]{}, transport.ErrFromStatus(err)
	}
	return roster.CheckResolution(c.name, "ListGroups", transport.GroupRosterFromPB(resp.GetRoster()), nil)
}

// ListMemberships reads one group's memberships over the wire.
//
// roster.CheckMemberships is what makes the requested group id load-bearing
// across the boundary: every returned membership must be for the group that
// was asked about, and a mismatch is refused rather than attributed to the
// group the host asked about. Guessing that correspondence is how people end
// up in groups they are not in.
func (c *rosterGRPCClient) ListMemberships(ctx context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	resp, err := c.client.ListMemberships(ctx, &connectorpb.ListMembershipsRequest{GroupId: groupID})
	if err != nil {
		return roster.Resolution[roster.Membership]{}, transport.ErrFromStatus(err)
	}
	return roster.CheckMemberships(c.name, groupID, c.caps, transport.MembershipRosterFromPB(resp.GetRoster()), nil)
}

// Implements reports the connector class this plugin binding is for. It is a
// compile-time constant of the Roster binding, not an RPC: the proto
// deliberately has no Implements method, because the class is fixed by which
// plugin key dispensed this client.
func (c *rosterGRPCClient) Implements() connector.Class { return connector.ClassRoster }

// Capabilities returns what the provider declared at dial time.
//
// It is the cached read rather than a fresh RPC because capabilities are a
// static declaration in this contract, and because the guards on every list
// call read the same set: a provider whose answer changed mid-life would
// otherwise have its principals judged against one set and its memberships
// against another.
func (c *rosterGRPCClient) Capabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), c.caps...)
}

func (c *rosterGRPCClient) Health(ctx context.Context) (connector.Health, error) {
	resp, err := c.client.Health(ctx, &connectorpb.HealthRequest{})
	if err != nil {
		return connector.Health{}, transport.ErrFromStatus(err)
	}
	return connector.Health{Status: connector.HealthStatus(resp.GetStatus()), Detail: resp.GetDetail()}, nil
}

// Close forwards to the provider, unlike the CredentialLoader stub's no-op.
//
// The difference is not an inconsistency: a Roster provider holds a session
// against someone else's directory API, and releasing it is work only the
// provider can do. Process teardown (dies-with-session) is still the go-plugin
// Client's Kill(), and this call is best-effort on top of it — a Close after
// the process is already gone reports the transport failure, typed, rather
// than pretending it succeeded.
func (c *rosterGRPCClient) Close() error {
	if _, err := c.client.Close(context.Background(), &connectorpb.CloseRequest{}); err != nil {
		return transport.ErrFromStatus(err)
	}
	return nil
}
