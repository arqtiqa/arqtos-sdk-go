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

type (
	bundleGRPCClient          struct{ *grpcClient }
	batchBundleGRPCClient     struct{ *grpcClient }
	authBundleGRPCClient      struct{ *grpcClient }
	batchAuthBundleGRPCClient struct{ *grpcClient }
)

var (
	_ credential.CredentialLoader = (*bundleGRPCClient)(nil)
	_ credential.BundleAcquirer   = (*bundleGRPCClient)(nil)
	_ credential.CredentialLoader = (*batchBundleGRPCClient)(nil)
	_ credential.BatchResolver    = (*batchBundleGRPCClient)(nil)
	_ credential.BundleAcquirer   = (*batchBundleGRPCClient)(nil)
	_ credential.CredentialLoader = (*authBundleGRPCClient)(nil)
	_ credential.AuthBinder       = (*authBundleGRPCClient)(nil)
	_ credential.BundleAcquirer   = (*authBundleGRPCClient)(nil)
	_ credential.CredentialLoader = (*batchAuthBundleGRPCClient)(nil)
	_ credential.BatchResolver    = (*batchAuthBundleGRPCClient)(nil)
	_ credential.AuthBinder       = (*batchAuthBundleGRPCClient)(nil)
	_ credential.BundleAcquirer   = (*batchAuthBundleGRPCClient)(nil)
)

func (c *bundleGRPCClient) AcquireBundle(ctx context.Context, inv credential.Inventory) (credential.Bundle, error) {
	return doAcquireBundle(ctx, c.grpcClient, inv)
}

func (c *batchBundleGRPCClient) AcquireBundle(ctx context.Context, inv credential.Inventory) (credential.Bundle, error) {
	return doAcquireBundle(ctx, c.grpcClient, inv)
}

func (c *batchBundleGRPCClient) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	return doResolveBatch(ctx, c.grpcClient, refs)
}

func (c *authBundleGRPCClient) AcquireBundle(ctx context.Context, inv credential.Inventory) (credential.Bundle, error) {
	return doAcquireBundle(ctx, c.grpcClient, inv)
}

func (c *authBundleGRPCClient) BindAuth(ctx context.Context, boot *credential.Bootstrap) error {
	return doBindAuth(ctx, c.grpcClient, boot)
}

func (c *batchAuthBundleGRPCClient) AcquireBundle(ctx context.Context, inv credential.Inventory) (credential.Bundle, error) {
	return doAcquireBundle(ctx, c.grpcClient, inv)
}

func (c *batchAuthBundleGRPCClient) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	return doResolveBatch(ctx, c.grpcClient, refs)
}

func (c *batchAuthBundleGRPCClient) BindAuth(ctx context.Context, boot *credential.Bootstrap) error {
	return doBindAuth(ctx, c.grpcClient, boot)
}

func doAcquireBundle(ctx context.Context, c *grpcClient, inv credential.Inventory) (credential.Bundle, error) {
	resp, err := c.client.AcquireBundle(ctx, &connectorpb.AcquireBundleRequest{Inventory: transport.InventoryToPB(inv)})
	if err != nil {
		return credential.Bundle{}, transport.ErrFromStatus(err)
	}
	return credential.CheckBundle(c.name, inv, transport.BundleFromPB(resp), nil)
}

func (s *grpcServer) AcquireBundle(ctx context.Context, req *connectorpb.AcquireBundleRequest) (*connectorpb.AcquireBundleResponse, error) {
	a, ok := s.impl.(credential.BundleAcquirer)
	if !ok {
		return nil, transport.ErrToStatus(cerr.New(cerr.KindUnsupported, "AcquireBundle",
			errors.New("this connector does not implement grant_bundle")))
	}
	b, err := a.AcquireBundle(ctx, transport.InventoryFromPB(req.GetInventory()))
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return transport.BundleToPB(b), nil
}
