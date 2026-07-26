// Package plugin wires credential.CredentialLoader onto hashicorp/go-plugin's
// gRPC transport: a Track-B provider serves an implementation via
// goplugin.Serve using PluginMap(impl); the host dials it and Dispense()s a
// value that itself satisfies credential.CredentialLoader (grpcClient). Only
// the material the host requested ever crosses the wire (SECURITY.md
// invariant); the client re-wraps returned bytes into a credential.Resolution
// so redaction/Zero() hold host-side; errors cross as gRPC status via the
// transport package, never as strings.
//
// The dispensed client is also the boundary where a provider's returns are
// checked: a provider is someone else's binary, so what it sends passes
// through credential.CheckResolution before a host ever sees it. A provider
// that reports success while resolving nothing yields a named contract fault,
// not an empty credential.
package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

// Handshake is the go-plugin magic-cookie handshake every Track-B
// CredentialLoader provider and host must share. It is a UX guard, not a
// security boundary.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "ARQTOS_CONNECTOR",
	MagicCookieValue: "arqtos-connector-v1",
}

// CredentialLoaderName is the plugin key both the provider (server) and the
// host (client) register the CredentialLoader plugin under.
const CredentialLoaderName = "credential_loader"

// PluginMap builds the map[string]goplugin.Plugin a Track-B provider passes
// to goplugin.ServeConfig.Plugins. impl is the provider's own
// credential.CredentialLoader implementation.
func PluginMap(impl credential.CredentialLoader) map[string]goplugin.Plugin {
	return map[string]goplugin.Plugin{
		CredentialLoaderName: &CredentialLoaderPlugin{Impl: impl},
	}
}

// CredentialLoaderPlugin is the go-plugin GRPCPlugin for the CredentialLoader
// connector class. Impl is set on the server (provider) side; it is left nil
// on the host side, where only GRPCClient is ever called.
type CredentialLoaderPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl credential.CredentialLoader
	// Name is the host's name for the provider being dialled. It is optional
	// and host-side only: it names the connector in a contract fault raised
	// by the dispensed client, so a broken provider is attributable without
	// the host having to re-wrap every call.
	Name string
}

var _ goplugin.GRPCPlugin = (*CredentialLoaderPlugin)(nil)

// GRPCServer registers the CredentialLoader gRPC service against s, backed
// by p.Impl. Called once, on the provider (server) side.
func (p *CredentialLoaderPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	connectorpb.RegisterCredentialLoaderServer(s, &grpcServer{impl: p.Impl})
	return nil
}

// GRPCClient returns a credential.CredentialLoader host-stub backed by conn.
// Called on the host (client) side.
func (p *CredentialLoaderPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, conn *grpc.ClientConn) (interface{}, error) {
	return &grpcClient{client: connectorpb.NewCredentialLoaderClient(conn), name: p.Name}, nil
}

// grpcServer adapts a credential.CredentialLoader implementation to the
// generated connectorpb.CredentialLoaderServer gRPC interface. It returns
// only the material the caller requested, and maps every error through
// transport.ErrToStatus so the client reconstructs a *cerr.Error, never a
// bare string.
type grpcServer struct {
	connectorpb.UnimplementedCredentialLoaderServer
	impl credential.CredentialLoader
}

func (s *grpcServer) Resolve(ctx context.Context, req *connectorpb.ResolveRequest) (*connectorpb.ResolveResponse, error) {
	res, err := s.impl.Resolve(ctx, transport.RefFromPB(req.GetRef()))
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	// transport.ResolutionToPB sends NO material message for a resolution
	// that carries no value, rather than an empty one. The distinction is the
	// host's to act on, and grpcClient below is where it is enforced — the
	// server does not get to decide that its own provider is conformant.
	return &connectorpb.ResolveResponse{Material: transport.ResolutionToPB(res)}, nil
}

func (s *grpcServer) List(ctx context.Context, req *connectorpb.ListRequest) (*connectorpb.ListResponse, error) {
	refs, err := s.impl.List(ctx, req.GetScope())
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	pbRefs := make([]*connectorpb.Ref, len(refs))
	for i, r := range refs {
		pbRefs[i] = transport.RefToPB(r)
	}
	return &connectorpb.ListResponse{Refs: pbRefs}, nil
}

func (s *grpcServer) Lease(ctx context.Context, req *connectorpb.LeaseRequest) (*connectorpb.LeaseResponse, error) {
	res, lease, err := s.impl.Lease(ctx, transport.RefFromPB(req.GetRef()))
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.LeaseResponse{
		Material: transport.ResolutionToPB(res),
		Lease:    transport.LeaseToPB(lease),
	}, nil
}

func (s *grpcServer) Renew(ctx context.Context, req *connectorpb.RenewRequest) (*connectorpb.RenewResponse, error) {
	newLease, err := s.impl.Renew(ctx, transport.LeaseFromPB(req.GetLease()))
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.RenewResponse{Lease: transport.LeaseToPB(newLease)}, nil
}

func (s *grpcServer) Revoke(ctx context.Context, req *connectorpb.RevokeRequest) (*connectorpb.RevokeResponse, error) {
	if err := s.impl.Revoke(ctx, transport.LeaseFromPB(req.GetLease())); err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.RevokeResponse{}, nil
}

func (s *grpcServer) Health(ctx context.Context, _ *connectorpb.HealthRequest) (*connectorpb.HealthResponse, error) {
	h, err := s.impl.Health(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.HealthResponse{Status: int32(h.Status), Detail: h.Detail}, nil
}

func (s *grpcServer) Capabilities(_ context.Context, _ *connectorpb.CapabilitiesRequest) (*connectorpb.CapabilitiesResponse, error) {
	caps := s.impl.Capabilities()
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return &connectorpb.CapabilitiesResponse{Capabilities: out}, nil
}

// grpcClient is the host-side stub dispensed by CredentialLoaderPlugin's
// GRPCClient: it satisfies credential.CredentialLoader by calling the
// provider over gRPC, re-wrapping returned bytes through the credential
// package (so redaction/Zero() hold host-side) and reconstructing errors via
// transport.ErrFromStatus.
//
// It is also where an out-of-process provider's returns are GUARDED. A
// provider is someone else's binary: the host cannot rely on it having been
// built against a conformant SDK, or on it being written in Go at all. Every
// resolution it sends therefore passes through credential.CheckResolution
// here, so a provider that reports success while resolving nothing produces a
// named contract fault on the host side rather than an empty credential.
type grpcClient struct {
	client connectorpb.CredentialLoaderClient
	// name is the host's name for this connector, used to attribute a
	// contract fault. It is empty unless the host set it on
	// CredentialLoaderPlugin; a fault then carries no name until the host
	// adds one via credential.CheckResolution.
	name string
}

var _ credential.CredentialLoader = (*grpcClient)(nil)

func (c *grpcClient) Resolve(ctx context.Context, r ref.Ref) (credential.Resolution, error) {
	resp, err := c.client.Resolve(ctx, &connectorpb.ResolveRequest{Ref: transport.RefToPB(r)})
	if err != nil {
		return credential.Resolution{}, transport.ErrFromStatus(err)
	}
	return credential.CheckResolution(c.name, "Resolve", transport.ResolutionFromPB(resp.GetMaterial()), nil)
}

func (c *grpcClient) List(ctx context.Context, scope string) ([]ref.Ref, error) {
	resp, err := c.client.List(ctx, &connectorpb.ListRequest{Scope: scope})
	if err != nil {
		return nil, transport.ErrFromStatus(err)
	}
	refs := make([]ref.Ref, len(resp.GetRefs()))
	for i, pb := range resp.GetRefs() {
		refs[i] = transport.RefFromPB(pb)
	}
	return refs, nil
}

func (c *grpcClient) Lease(ctx context.Context, r ref.Ref) (credential.Resolution, credential.Lease, error) {
	resp, err := c.client.Lease(ctx, &connectorpb.LeaseRequest{Ref: transport.RefToPB(r)})
	if err != nil {
		return credential.Resolution{}, credential.Lease{}, transport.ErrFromStatus(err)
	}
	res, err := credential.CheckResolution(c.name, "Lease", transport.ResolutionFromPB(resp.GetMaterial()), nil)
	if err != nil {
		return credential.Resolution{}, credential.Lease{}, err
	}
	return res, transport.LeaseFromPB(resp.GetLease()), nil
}

func (c *grpcClient) Renew(ctx context.Context, l credential.Lease) (credential.Lease, error) {
	resp, err := c.client.Renew(ctx, &connectorpb.RenewRequest{Lease: transport.LeaseToPB(l)})
	if err != nil {
		return credential.Lease{}, transport.ErrFromStatus(err)
	}
	return transport.LeaseFromPB(resp.GetLease()), nil
}

func (c *grpcClient) Revoke(ctx context.Context, l credential.Lease) error {
	if _, err := c.client.Revoke(ctx, &connectorpb.RevokeRequest{Lease: transport.LeaseToPB(l)}); err != nil {
		return transport.ErrFromStatus(err)
	}
	return nil
}

// Implements reports the connector class this plugin binding is for. It is
// a compile-time constant of the CredentialLoader binding, not an RPC: the
// proto deliberately has no Implements method (the class is fixed by which
// plugin key/binding dispensed this client).
func (c *grpcClient) Implements() connector.Class { return connector.ClassCredentialLoader }

// Capabilities calls the provider's Capabilities RPC. The
// connector.Connector interface method takes no context, so a background
// context is used for the call; an RPC error yields no capabilities rather
// than a panic, since the interface has no error return here.
func (c *grpcClient) Capabilities() connector.Capabilities {
	resp, err := c.client.Capabilities(context.Background(), &connectorpb.CapabilitiesRequest{})
	if err != nil {
		return nil
	}
	caps := make(connector.Capabilities, len(resp.GetCapabilities()))
	for i, s := range resp.GetCapabilities() {
		caps[i] = connector.Capability(s)
	}
	return caps
}

func (c *grpcClient) Health(ctx context.Context) (connector.Health, error) {
	resp, err := c.client.Health(ctx, &connectorpb.HealthRequest{})
	if err != nil {
		return connector.Health{}, transport.ErrFromStatus(err)
	}
	return connector.Health{Status: connector.HealthStatus(resp.GetStatus()), Detail: resp.GetDetail()}, nil
}

// Close is a no-op: the proto has no Close RPC, and process teardown
// (dies-with-session) is owned by the go-plugin Client's Kill(), not by the
// dispensed interface value.
func (c *grpcClient) Close() error { return nil }
