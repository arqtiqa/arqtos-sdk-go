// This file is the Track-B (out-of-process) wiring for the Authenticator
// connector class, symmetric with the CredentialLoader wiring in plugin.go and
// the Roster wiring in roster.go: a provider serves an implementation via
// goplugin.Serve using [AuthenticatorPluginMap]; a host dials it with
// [AuthenticatorHostPluginMap] and Dispense()s a value that itself satisfies
// authenticator.Authenticator.
//
// # The host keeps the browser and the loopback listener
//
// Nothing here dials a browser, binds a port, or carries a token. The provider
// is handed an authorization code the HOST already received on its own redirect
// listener, and returns an assertion that has nowhere to put a token.
//
// That division was ruled rather than assumed, and the reason is the contract's
// second invariant: a connector never forwards a raw credential to another
// party. A provider that obtained the operator's token and handed it onward
// would be brokering a credential for a third party, and putting the redirect
// listener inside a third-party subprocess changes the trust boundary rather
// than the wiring. The authorization code is the one credential-shaped value
// that crosses, in one direction, and it is useless without the verifier the
// provider holds.
//
// # What the host stub adds beyond calling the provider
//
// A provider is someone else's binary — possibly not written in Go, certainly
// not built against a conformant SDK by assumption. So the dispensed client is
// a GUARD as well as a transport. Everything a provider sends passes through
// the authenticator package's own host-side checks before a host sees it, so a
// provider returning an incoherent assertion, or an assertion beside an error,
// produces a NAMED contract fault instead of a session started for nobody.
//
// # There is no capability on this wire, and the stub is not conditional
//
// The Authenticator class's capability vocabulary is EMPTY at v1, so there is
// no optional interface to satisfy and nothing about this stub's SHAPE depends
// on what the provider declares.
//
// ⚠️ That is worth stating because the day a capability is added, the temptation
// is to make the stub conditional on the Capabilities RPC. Do not: the
// conformance harness's declared-is-implemented checks are only worth anything
// because "declared" and "implemented" come from INDEPENDENT signals, and
// deriving the stub's shape from the declaration would make that check agree
// with itself whatever the provider does. This SDK has already shipped that bug
// once, in the credential class's batch capability. Probe BEHAVIOUR if a
// conditional stub is ever unavoidable.
package plugin

import (
	"context"
	"errors"
	"fmt"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/arqtiqa/arqtos-sdk-go/authenticator"
	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

// AuthenticatorName is the plugin key both the provider (server) and the host
// (client) register the Authenticator plugin under.
//
// It is the go-plugin DISPENSE KEY, not the connector class: a wire key in
// snake_case, alongside [CredentialLoaderName] and [RosterName].
// connector.Classes() remains the single source for the class vocabulary, and
// nothing here restates it — the class this binding is for is named exactly
// once, by authenticatorGRPCClient.Implements.
const AuthenticatorName = "authenticator"

// AuthenticatorPluginMap builds the map[string]goplugin.Plugin a Track-B
// PROVIDER passes to goplugin.ServeConfig.Plugins.
//
// A provider needs no manifest or host version here: it is not the side doing
// the dialling, and GRPCClient is never called on it.
func AuthenticatorPluginMap(impl authenticator.Authenticator) map[string]goplugin.Plugin {
	return map[string]goplugin.Plugin{
		AuthenticatorName: &AuthenticatorPlugin{Impl: impl},
	}
}

// AuthenticatorHostPluginMap builds the map[string]goplugin.Plugin a HOST
// passes to goplugin.ClientConfig.Plugins.
//
// It takes the negotiation inputs rather than leaving them optional, because
// this is the only host-side entry point: name is what a contract fault will be
// attributed to, provider is the connector.yml the host READ before launching
// anything, and hostVersion is the contract version this host implements.
func AuthenticatorHostPluginMap(name string, provider manifest.Doc, hostVersion string) map[string]goplugin.Plugin {
	return map[string]goplugin.Plugin{
		AuthenticatorName: &AuthenticatorPlugin{Name: name, ProviderManifest: provider, HostVersion: hostVersion},
	}
}

// AuthenticatorPlugin is the go-plugin GRPCPlugin for the Authenticator class.
// Impl is set on the server (provider) side; the remaining fields are host-side
// only, where only GRPCClient is ever called.
type AuthenticatorPlugin struct {
	goplugin.NetRPCUnsupportedPlugin

	// Impl is the provider's own implementation. Provider side only.
	Impl authenticator.Authenticator

	// Name is the host's name for the provider being dialled.
	Name string

	// ProviderManifest is the provider's connector.yml as the host read it, and
	// HostVersion is the contract version this host implements. Together they
	// are the min_host_version negotiation.
	ProviderManifest manifest.Doc
	HostVersion      string
}

var _ goplugin.GRPCPlugin = (*AuthenticatorPlugin)(nil)

// GRPCServer registers the Authenticator gRPC service against s, backed by
// p.Impl. Called once, on the provider (server) side.
func (p *AuthenticatorPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	connectorpb.RegisterAuthenticatorServer(s, &authenticatorGRPCServer{impl: p.Impl})
	return nil
}

// GRPCClient returns an authenticator.Authenticator host-stub backed by conn.
//
// It refuses before it hands anything back, in one respect:
//
//   - min_host_version NEGOTIATION. A missing manifest or an empty host version
//     is refused too, because a gate a caller can omit is a gate that is off by
//     default, and the failure it prevents — running a connector against a
//     contract it has already said it cannot work with — is silent when it
//     happens.
//
//   - a CAPABILITIES read, once. Capabilities are a static declaration in this
//     contract, and Capabilities() returns NO ERROR — so a stub calling live
//     would have to swallow a failed probe silently, which is the shape this
//     SDK refuses everywhere else. Reading once here means a failed probe
//     refuses the DIAL, loudly, instead of degrading into a wrong answer.
//
// ⚠️ The vocabulary is empty at v1, so a conformant provider answers with
// nothing and the cached set is empty. The probe is not therefore ceremony: it
// is what makes "this provider answers the base contract at all" a dial-time
// fact rather than something discovered on the first real call.
func (p *AuthenticatorPlugin) GRPCClient(ctx context.Context, _ *goplugin.GRPCBroker, conn *grpc.ClientConn) (interface{}, error) {
	if p.HostVersion == "" {
		return nil, cerr.New(cerr.KindInvalid, "Dispense", errors.New(
			"the host did not state its own contract version, so this provider's min_host_version cannot be "+
				"negotiated; dial with plugin.AuthenticatorHostPluginMap, which requires it"))
	}
	if p.ProviderManifest.Name == "" {
		return nil, cerr.New(cerr.KindInvalid, "Dispense", errors.New(
			"the host supplied no provider manifest, so this provider's min_host_version cannot be negotiated; "+
				"dial with plugin.AuthenticatorHostPluginMap, which requires it"))
	}
	if err := p.ProviderManifest.RequireHost(p.HostVersion); err != nil {
		return nil, cerr.New(cerr.KindUnsupported, "Dispense", err)
	}

	client := connectorpb.NewAuthenticatorClient(conn)
	caps, err := readAuthenticatorCapabilities(ctx, client)
	if err != nil {
		return nil, cerr.New(cerr.KindUnavailable, "Dispense", fmt.Errorf(
			"could not read the capabilities of connector %q: %w", p.Name, err))
	}
	return &authenticatorGRPCClient{client: client, name: p.Name, caps: caps}, nil
}

// authenticatorGRPCServer adapts an authenticator.Authenticator implementation
// to the generated connectorpb.AuthenticatorServer interface, mapping every
// error through transport.ErrToStatus so the client reconstructs a *cerr.Error
// rather than a bare string.
//
// It does NOT check its own provider's conformance. A server that vetted its
// own returns would be the provider deciding it is conformant, which is the one
// party that cannot be asked — the checks live host-side.
type authenticatorGRPCServer struct {
	connectorpb.UnimplementedAuthenticatorServer
	impl authenticator.Authenticator
}

func (s *authenticatorGRPCServer) Begin(ctx context.Context, _ *connectorpb.BeginRequest) (*connectorpb.BeginResponse, error) {
	c, err := s.impl.Begin(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.BeginResponse{Challenge: transport.ChallengeToPB(c)}, nil
}

func (s *authenticatorGRPCServer) Complete(ctx context.Context, req *connectorpb.CompleteRequest) (*connectorpb.CompleteResponse, error) {
	a, err := s.impl.Complete(ctx, req.GetHandle(), req.GetCode())
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.CompleteResponse{Assertion: transport.AssertionToPB(a)}, nil
}

func (s *authenticatorGRPCServer) Health(ctx context.Context, _ *connectorpb.HealthRequest) (*connectorpb.HealthResponse, error) {
	h, err := s.impl.Health(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.HealthResponse{Status: int32(h.Status), Detail: h.Detail}, nil
}

func (s *authenticatorGRPCServer) Capabilities(_ context.Context, _ *connectorpb.CapabilitiesRequest) (*connectorpb.CapabilitiesResponse, error) {
	caps := s.impl.Capabilities()
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return &connectorpb.CapabilitiesResponse{Capabilities: out}, nil
}

func (s *authenticatorGRPCServer) Close(_ context.Context, _ *connectorpb.CloseRequest) (*connectorpb.CloseResponse, error) {
	if err := s.impl.Close(); err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.CloseResponse{}, nil
}

// authenticatorGRPCClient is the host-side stub dispensed by
// [AuthenticatorPlugin.GRPCClient]: it satisfies authenticator.Authenticator by
// calling the provider over gRPC and reconstructing errors via
// transport.ErrFromStatus.
//
// Every return then passes through the authenticator package's host-side guard
// before the host sees it. That is the whole reason this type exists rather
// than a bare generated client: the guards are what turn a foreign provider's
// incoherent answer into a named fault instead of a session started for the
// wrong person, or for nobody.
type authenticatorGRPCClient struct {
	client connectorpb.AuthenticatorClient
	// name is the host's name for this connector, used to attribute a contract
	// fault.
	name string
	// caps is what the provider declared at dial time. See
	// AuthenticatorPlugin.GRPCClient for why it is read once.
	caps connector.Capabilities
}

// readAuthenticatorCapabilities performs the one dial-time Capabilities call.
func readAuthenticatorCapabilities(ctx context.Context, client connectorpb.AuthenticatorClient) (connector.Capabilities, error) {
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

var _ authenticator.Authenticator = (*authenticatorGRPCClient)(nil)

// Begin starts an exchange over the wire.
//
// The guard refuses a challenge missing either half: a host cannot drive the
// flow without both, and proto3's default for a string is exactly what a
// provider that forgot to set one produces.
func (c *authenticatorGRPCClient) Begin(ctx context.Context) (authenticator.Challenge, error) {
	resp, err := c.client.Begin(ctx, &connectorpb.BeginRequest{})
	if err != nil {
		return authenticator.Challenge{}, transport.ErrFromStatus(err)
	}
	return authenticator.CheckChallenge(c.name, transport.ChallengeFromPB(resp.GetChallenge()), nil)
}

// Complete exchanges the authorization code over the wire.
//
// authenticator.CheckAssertion is what makes the contract's refusals real
// across the boundary: an incoherent assertion becomes a named fault, and an
// assertion returned beside an error is refused rather than read.
func (c *authenticatorGRPCClient) Complete(ctx context.Context, handle, code string) (authenticator.Assertion, error) {
	resp, err := c.client.Complete(ctx, &connectorpb.CompleteRequest{Handle: handle, Code: code})
	if err != nil {
		return authenticator.CheckAssertion(c.name, authenticator.Assertion{}, transport.ErrFromStatus(err))
	}
	return authenticator.CheckAssertion(c.name, transport.AssertionFromPB(resp.GetAssertion()), nil)
}

// Implements reports the connector class this plugin binding is for. It is a
// compile-time constant of the Authenticator binding, not an RPC: the proto
// deliberately has no Implements method, because the class is fixed by which
// plugin key dispensed this client.
func (c *authenticatorGRPCClient) Implements() connector.Class {
	return connector.ClassAuthenticator
}

// Capabilities returns what the provider declared at dial time, as a copy.
//
// It is the cached read rather than a fresh RPC because capabilities are a
// static declaration in this contract, and because Capabilities() cannot report
// an error — a live call would have to swallow a failed probe, and a silently
// empty capability set reads as a conformant answer for this class.
func (c *authenticatorGRPCClient) Capabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), c.caps...)
}

func (c *authenticatorGRPCClient) Health(ctx context.Context) (connector.Health, error) {
	resp, err := c.client.Health(ctx, &connectorpb.HealthRequest{})
	if err != nil {
		return connector.Health{}, transport.ErrFromStatus(err)
	}
	return connector.Health{Status: connector.HealthStatus(resp.GetStatus()), Detail: resp.GetDetail()}, nil
}

// Close forwards to the provider.
//
// A Roster provider holds a session against someone else's directory API; an
// Authenticator provider holds something sharper — in-flight handles and the
// PKCE verifiers and nonces held against them. Releasing those is work only the
// provider can do, and a verifier outliving the session is a credential
// outliving the session.
//
// Process teardown (dies-with-session) is still the go-plugin Client's Kill();
// this call is best-effort on top of it — a Close after the process is already
// gone reports the transport failure, typed, rather than pretending it
// succeeded.
func (c *authenticatorGRPCClient) Close() error {
	if _, err := c.client.Close(context.Background(), &connectorpb.CloseRequest{}); err != nil {
		return transport.ErrFromStatus(err)
	}
	return nil
}
