package plugin

import (
	"context"
	"errors"
	"fmt"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/certificate"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

// CertificateName is the go-plugin dispense key for the Certificate class.
const CertificateName = "certificate"

// CertificatePluginMap builds the map a Track-B PROVIDER passes to Serve.
func CertificatePluginMap(impl certificate.Certificate) map[string]goplugin.Plugin {
	return map[string]goplugin.Plugin{
		CertificateName: &CertificatePlugin{Impl: impl},
	}
}

// CertificateHostPluginMap builds the map a HOST passes to ClientConfig.
func CertificateHostPluginMap(name string, provider manifest.Doc, hostVersion string) map[string]goplugin.Plugin {
	return map[string]goplugin.Plugin{
		CertificateName: &CertificatePlugin{Name: name, ProviderManifest: provider, HostVersion: hostVersion},
	}
}

// CertificatePlugin is the go-plugin GRPCPlugin for the Certificate class.
type CertificatePlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl             certificate.Certificate
	Name             string
	ProviderManifest manifest.Doc
	HostVersion      string
}

var _ goplugin.GRPCPlugin = (*CertificatePlugin)(nil)

func (p *CertificatePlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	connectorpb.RegisterCertificateServer(s, &certificateGRPCServer{impl: p.Impl})
	return nil
}

func (p *CertificatePlugin) GRPCClient(ctx context.Context, _ *goplugin.GRPCBroker, conn *grpc.ClientConn) (interface{}, error) {
	if p.HostVersion == "" {
		return nil, cerr.New(cerr.KindInvalid, "Dispense", errors.New(
			"the host did not state its own contract version; dial with plugin.CertificateHostPluginMap"))
	}
	if p.ProviderManifest.Name == "" {
		return nil, cerr.New(cerr.KindInvalid, "Dispense", errors.New(
			"the host supplied no provider manifest; dial with plugin.CertificateHostPluginMap"))
	}
	if err := p.ProviderManifest.RequireHost(p.HostVersion); err != nil {
		return nil, cerr.New(cerr.KindUnsupported, "Dispense", err)
	}
	client := connectorpb.NewCertificateClient(conn)
	caps, err := readCertificateCapabilities(ctx, client)
	if err != nil {
		return nil, cerr.New(cerr.KindUnavailable, "Dispense", fmt.Errorf(
			"could not read the capabilities of connector %q: %w", p.Name, err))
	}
	return &certificateGRPCClient{client: client, name: p.Name, caps: caps}, nil
}

type certificateGRPCServer struct {
	connectorpb.UnimplementedCertificateServer
	impl certificate.Certificate
}

func (s *certificateGRPCServer) Sign(ctx context.Context, req *connectorpb.SignRequest) (*connectorpb.SignResponse, error) {
	sig, err := s.impl.Sign(ctx, req.GetKeyId(), req.GetPayload())
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.SignResponse{Signature: sig.Bytes, Algorithm: sig.Algorithm}, nil
}

func (s *certificateGRPCServer) Verify(ctx context.Context, req *connectorpb.VerifyRequest) (*connectorpb.VerifyResponse, error) {
	if err := s.impl.Verify(ctx, req.GetKeyId(), req.GetPayload(), req.GetSignature()); err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.VerifyResponse{}, nil
}

func (s *certificateGRPCServer) PublicKey(ctx context.Context, req *connectorpb.PublicKeyRequest) (*connectorpb.PublicKeyResponse, error) {
	pk, err := s.impl.PublicKey(ctx, req.GetKeyId())
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.PublicKeyResponse{PublicKey: pk.Bytes, Algorithm: pk.Algorithm}, nil
}

func (s *certificateGRPCServer) Health(ctx context.Context, _ *connectorpb.HealthRequest) (*connectorpb.HealthResponse, error) {
	h, err := s.impl.Health(ctx)
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.HealthResponse{Status: int32(h.Status), Detail: h.Detail}, nil
}

func (s *certificateGRPCServer) Capabilities(_ context.Context, _ *connectorpb.CapabilitiesRequest) (*connectorpb.CapabilitiesResponse, error) {
	caps := s.impl.Capabilities()
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return &connectorpb.CapabilitiesResponse{Capabilities: out}, nil
}

func (s *certificateGRPCServer) Close(_ context.Context, _ *connectorpb.CloseRequest) (*connectorpb.CloseResponse, error) {
	if err := s.impl.Close(); err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.CloseResponse{}, nil
}

type certificateGRPCClient struct {
	client connectorpb.CertificateClient
	name   string
	caps   connector.Capabilities
}

func readCertificateCapabilities(ctx context.Context, client connectorpb.CertificateClient) (connector.Capabilities, error) {
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

var _ certificate.Certificate = (*certificateGRPCClient)(nil)

func (c *certificateGRPCClient) Sign(ctx context.Context, keyID string, payload []byte) (certificate.Signature, error) {
	resp, err := c.client.Sign(ctx, &connectorpb.SignRequest{KeyId: keyID, Payload: payload})
	if err != nil {
		return certificate.Signature{}, transport.ErrFromStatus(err)
	}
	if len(resp.GetSignature()) == 0 {
		return certificate.Signature{}, cerr.New(cerr.KindInvalid, "Sign", fmt.Errorf("connector %q returned an empty signature", c.name))
	}
	return certificate.Signature{Bytes: resp.GetSignature(), Algorithm: resp.GetAlgorithm()}, nil
}

func (c *certificateGRPCClient) Verify(ctx context.Context, keyID string, payload, signature []byte) error {
	_, err := c.client.Verify(ctx, &connectorpb.VerifyRequest{KeyId: keyID, Payload: payload, Signature: signature})
	if err != nil {
		return transport.ErrFromStatus(err)
	}
	return nil
}

func (c *certificateGRPCClient) PublicKey(ctx context.Context, keyID string) (certificate.PublicKey, error) {
	resp, err := c.client.PublicKey(ctx, &connectorpb.PublicKeyRequest{KeyId: keyID})
	if err != nil {
		return certificate.PublicKey{}, transport.ErrFromStatus(err)
	}
	if len(resp.GetPublicKey()) == 0 {
		return certificate.PublicKey{}, cerr.New(cerr.KindInvalid, "PublicKey", fmt.Errorf("connector %q returned an empty public key", c.name))
	}
	return certificate.PublicKey{Bytes: resp.GetPublicKey(), Algorithm: resp.GetAlgorithm()}, nil
}

func (c *certificateGRPCClient) Implements() connector.Class { return connector.ClassCertificate }

func (c *certificateGRPCClient) Capabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), c.caps...)
}

func (c *certificateGRPCClient) Health(ctx context.Context) (connector.Health, error) {
	resp, err := c.client.Health(ctx, &connectorpb.HealthRequest{})
	if err != nil {
		return connector.Health{}, transport.ErrFromStatus(err)
	}
	return connector.Health{Status: connector.HealthStatus(resp.GetStatus()), Detail: resp.GetDetail()}, nil
}

func (c *certificateGRPCClient) Close() error {
	if _, err := c.client.Close(context.Background(), &connectorpb.CloseRequest{}); err != nil {
		return transport.ErrFromStatus(err)
	}
	return nil
}
