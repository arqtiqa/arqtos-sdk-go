package plugin

import (
	"context"
	"errors"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

type (
	lifeClient                struct{ *grpcClient }
	lifeBatchClient           struct{ *batchGRPCClient }
	lifeAuthClient            struct{ *authGRPCClient }
	lifeBatchAuthClient       struct{ *batchAuthGRPCClient }
	lifeBundleClient          struct{ *bundleGRPCClient }
	lifeBatchBundleClient     struct{ *batchBundleGRPCClient }
	lifeAuthBundleClient      struct{ *authBundleGRPCClient }
	lifeBatchAuthBundleClient struct{ *batchAuthBundleGRPCClient }
)

var (
	_ credential.AuthLifecycle    = (*lifeClient)(nil)
	_ credential.AuthLifecycle    = (*lifeBatchClient)(nil)
	_ credential.AuthLifecycle    = (*lifeAuthClient)(nil)
	_ credential.AuthLifecycle    = (*lifeBatchAuthClient)(nil)
	_ credential.AuthLifecycle    = (*lifeBundleClient)(nil)
	_ credential.AuthLifecycle    = (*lifeBatchBundleClient)(nil)
	_ credential.AuthLifecycle    = (*lifeAuthBundleClient)(nil)
	_ credential.AuthLifecycle    = (*lifeBatchAuthBundleClient)(nil)
	_ credential.CredentialLoader = (*lifeClient)(nil)
	_ credential.BatchResolver    = (*lifeBatchClient)(nil)
	_ credential.AuthBinder       = (*lifeAuthClient)(nil)
	_ credential.BundleAcquirer   = (*lifeBundleClient)(nil)
)

func wrapLifecycle(base interface{}) interface{} {
	switch b := base.(type) {
	case *batchAuthBundleGRPCClient:
		return &lifeBatchAuthBundleClient{b}
	case *authBundleGRPCClient:
		return &lifeAuthBundleClient{b}
	case *batchBundleGRPCClient:
		return &lifeBatchBundleClient{b}
	case *bundleGRPCClient:
		return &lifeBundleClient{b}
	case *batchAuthGRPCClient:
		return &lifeBatchAuthClient{b}
	case *authGRPCClient:
		return &lifeAuthClient{b}
	case *batchGRPCClient:
		return &lifeBatchClient{b}
	case *grpcClient:
		return &lifeClient{b}
	default:
		return base
	}
}

func (c *lifeClient) AuthStatus(ctx context.Context, now time.Time) (credential.AuthSession, error) {
	return doAuthStatus(ctx, c.grpcClient, now)
}

func (c *lifeClient) RenewAuth(ctx context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	return doRenewAuth(ctx, c.grpcClient, s, now)
}

func (c *lifeClient) Reauthenticate(ctx context.Context, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	return doReauthenticate(ctx, c.grpcClient, boot, now)
}

func (c *lifeBatchClient) AuthStatus(ctx context.Context, now time.Time) (credential.AuthSession, error) {
	return doAuthStatus(ctx, c.grpcClient, now)
}

func (c *lifeBatchClient) RenewAuth(ctx context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	return doRenewAuth(ctx, c.grpcClient, s, now)
}

func (c *lifeBatchClient) Reauthenticate(ctx context.Context, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	return doReauthenticate(ctx, c.grpcClient, boot, now)
}

func (c *lifeAuthClient) AuthStatus(ctx context.Context, now time.Time) (credential.AuthSession, error) {
	return doAuthStatus(ctx, c.grpcClient, now)
}

func (c *lifeAuthClient) RenewAuth(ctx context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	return doRenewAuth(ctx, c.grpcClient, s, now)
}

func (c *lifeAuthClient) Reauthenticate(ctx context.Context, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	return doReauthenticate(ctx, c.grpcClient, boot, now)
}

func (c *lifeBatchAuthClient) AuthStatus(ctx context.Context, now time.Time) (credential.AuthSession, error) {
	return doAuthStatus(ctx, c.grpcClient, now)
}

func (c *lifeBatchAuthClient) RenewAuth(ctx context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	return doRenewAuth(ctx, c.grpcClient, s, now)
}

func (c *lifeBatchAuthClient) Reauthenticate(ctx context.Context, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	return doReauthenticate(ctx, c.grpcClient, boot, now)
}

func (c *lifeBundleClient) AuthStatus(ctx context.Context, now time.Time) (credential.AuthSession, error) {
	return doAuthStatus(ctx, c.grpcClient, now)
}

func (c *lifeBundleClient) RenewAuth(ctx context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	return doRenewAuth(ctx, c.grpcClient, s, now)
}

func (c *lifeBundleClient) Reauthenticate(ctx context.Context, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	return doReauthenticate(ctx, c.grpcClient, boot, now)
}

func (c *lifeBatchBundleClient) AuthStatus(ctx context.Context, now time.Time) (credential.AuthSession, error) {
	return doAuthStatus(ctx, c.grpcClient, now)
}

func (c *lifeBatchBundleClient) RenewAuth(ctx context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	return doRenewAuth(ctx, c.grpcClient, s, now)
}

func (c *lifeBatchBundleClient) Reauthenticate(ctx context.Context, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	return doReauthenticate(ctx, c.grpcClient, boot, now)
}

func (c *lifeAuthBundleClient) AuthStatus(ctx context.Context, now time.Time) (credential.AuthSession, error) {
	return doAuthStatus(ctx, c.grpcClient, now)
}

func (c *lifeAuthBundleClient) RenewAuth(ctx context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	return doRenewAuth(ctx, c.grpcClient, s, now)
}

func (c *lifeAuthBundleClient) Reauthenticate(ctx context.Context, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	return doReauthenticate(ctx, c.grpcClient, boot, now)
}

func (c *lifeBatchAuthBundleClient) AuthStatus(ctx context.Context, now time.Time) (credential.AuthSession, error) {
	return doAuthStatus(ctx, c.grpcClient, now)
}

func (c *lifeBatchAuthBundleClient) RenewAuth(ctx context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	return doRenewAuth(ctx, c.grpcClient, s, now)
}

func (c *lifeBatchAuthBundleClient) Reauthenticate(ctx context.Context, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	return doReauthenticate(ctx, c.grpcClient, boot, now)
}

func doAuthStatus(ctx context.Context, c *grpcClient, now time.Time) (credential.AuthSession, error) {
	resp, err := c.client.AuthStatus(ctx, &connectorpb.AuthStatusRequest{NowUnix: unixOrZero(now)})
	if err != nil {
		return credential.AuthSession{}, transport.ErrFromStatus(err)
	}
	s := transport.AuthSessionFromPB(resp.GetSession())
	if err := credential.CheckAuthSession(s); err != nil {
		return credential.AuthSession{}, cerr.New(cerr.KindInvalid, "AuthStatus", err)
	}
	return s, nil
}

func doRenewAuth(ctx context.Context, c *grpcClient, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	if err := credential.CheckAuthSession(s); err != nil {
		return credential.AuthSession{}, cerr.New(cerr.KindInvalid, "RenewAuth", err)
	}
	resp, err := c.client.RenewAuth(ctx, &connectorpb.RenewAuthRequest{Session: transport.AuthSessionToPB(s), NowUnix: unixOrZero(now)})
	if err != nil {
		return credential.AuthSession{}, transport.ErrFromStatus(err)
	}
	got := transport.AuthSessionFromPB(resp.GetSession())
	if err := credential.CheckAuthSession(got); err != nil {
		return credential.AuthSession{}, cerr.New(cerr.KindInvalid, "RenewAuth", err)
	}
	return got, nil
}

func doReauthenticate(ctx context.Context, c *grpcClient, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	resp, err := c.client.Reauthenticate(ctx, &connectorpb.ReauthenticateRequest{
		Bootstrap: transport.BootstrapToPB(boot),
		NowUnix:   unixOrZero(now),
	})
	if err != nil {
		return credential.AuthSession{}, transport.ErrFromStatus(err)
	}
	got := transport.AuthSessionFromPB(resp.GetSession())
	if err := credential.CheckAuthSession(got); err != nil {
		return credential.AuthSession{}, cerr.New(cerr.KindInvalid, "Reauthenticate", err)
	}
	return got, nil
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func (s *grpcServer) AuthStatus(ctx context.Context, req *connectorpb.AuthStatusRequest) (*connectorpb.AuthStatusResponse, error) {
	a, ok := s.impl.(credential.AuthLifecycle)
	if !ok {
		return nil, transport.ErrToStatus(cerr.New(cerr.KindUnsupported, "AuthStatus",
			errors.New("this connector does not implement auth_lifecycle")))
	}
	sess, err := a.AuthStatus(ctx, time.Unix(req.GetNowUnix(), 0).UTC())
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.AuthStatusResponse{Session: transport.AuthSessionToPB(sess)}, nil
}

func (s *grpcServer) RenewAuth(ctx context.Context, req *connectorpb.RenewAuthRequest) (*connectorpb.RenewAuthResponse, error) {
	a, ok := s.impl.(credential.AuthLifecycle)
	if !ok {
		return nil, transport.ErrToStatus(cerr.New(cerr.KindUnsupported, "RenewAuth",
			errors.New("this connector does not implement auth_lifecycle")))
	}
	in := transport.AuthSessionFromPB(req.GetSession())
	if err := credential.CheckAuthSession(in); err != nil {
		return nil, transport.ErrToStatus(cerr.New(cerr.KindInvalid, "RenewAuth", err))
	}
	sess, err := a.RenewAuth(ctx, in, time.Unix(req.GetNowUnix(), 0).UTC())
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.RenewAuthResponse{Session: transport.AuthSessionToPB(sess)}, nil
}

func (s *grpcServer) Reauthenticate(ctx context.Context, req *connectorpb.ReauthenticateRequest) (*connectorpb.ReauthenticateResponse, error) {
	a, ok := s.impl.(credential.AuthLifecycle)
	if !ok {
		return nil, transport.ErrToStatus(cerr.New(cerr.KindUnsupported, "Reauthenticate",
			errors.New("this connector does not implement auth_lifecycle")))
	}
	sess, err := a.Reauthenticate(ctx, transport.BootstrapFromPB(req.GetBootstrap()), time.Unix(req.GetNowUnix(), 0).UTC())
	if err != nil {
		return nil, transport.ErrToStatus(err)
	}
	return &connectorpb.ReauthenticateResponse{Session: transport.AuthSessionToPB(sess)}, nil
}
