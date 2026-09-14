package plugin

import (
	"context"
	"errors"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

type authGRPCClient struct{ *grpcClient }

type batchAuthGRPCClient struct{ *grpcClient }

var (
	_ credential.CredentialLoader = (*authGRPCClient)(nil)
	_ credential.AuthBinder       = (*authGRPCClient)(nil)
	_ credential.CredentialLoader = (*batchAuthGRPCClient)(nil)
	_ credential.BatchResolver    = (*batchAuthGRPCClient)(nil)
	_ credential.AuthBinder       = (*batchAuthGRPCClient)(nil)
)

func (c *authGRPCClient) BindAuth(ctx context.Context, boot *credential.Bootstrap) error {
	return doBindAuth(ctx, c.grpcClient, boot)
}

func (c *batchAuthGRPCClient) BindAuth(ctx context.Context, boot *credential.Bootstrap) error {
	return doBindAuth(ctx, c.grpcClient, boot)
}

func (c *batchAuthGRPCClient) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	return doResolveBatch(ctx, c.grpcClient, refs)
}

func doBindAuth(ctx context.Context, c *grpcClient, boot *credential.Bootstrap) error {
	_, err := c.client.BindAuth(ctx, transport.BootstrapToPB(boot))
	if err != nil {
		return transport.ErrFromStatus(err)
	}
	return nil
}

func (s *grpcServer) BindAuth(ctx context.Context, req *connectorpb.BindAuthRequest) (*connectorpb.BindAuthResponse, error) {
	b, ok := s.impl.(credential.AuthBinder)
	if !ok {
		return nil, transport.ErrToStatus(cerr.New(cerr.KindUnsupported, "BindAuth",
			errors.New("this connector does not implement bind_auth")))
	}
	if err := b.BindAuth(ctx, transport.BootstrapFromPB(req)); err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.BindAuthResponse{}, nil
}
