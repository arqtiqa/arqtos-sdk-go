package credential

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrWrongHandleKind = errors.New("credential: handle is not an authentication session")

// HandleKind distinguishes provider-auth sessions from dynamic-secret leases.
// The two must not be interchangeable (credential-detail §3e).
type HandleKind string

const (
	KindAuth        HandleKind = "auth"
	KindSecretLease HandleKind = "secret_lease"
)

// An AuthSession is the connector's own outward-auth lifetime, not a
// CredentialLoader.Lease for a dynamic secret.
type AuthSession struct {
	ID        string
	Kind      HandleKind
	ExpiresAt time.Time
	Renewable bool
}

func (k HandleKind) Valid() bool { return k == KindAuth }

func (s AuthSession) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

func CheckAuthSession(s AuthSession) error {
	if s.Kind != KindAuth || !s.Kind.Valid() {
		return fmt.Errorf("%w: %s", ErrWrongHandleKind, s.Kind)
	}
	if s.ID == "" {
		return fmt.Errorf("%w: empty id", ErrWrongHandleKind)
	}
	return nil
}

// AuthLifecycle is host-driven provider authentication expiry, renewal and
// reauthentication. Optional, behind [CapAuthLifecycle]. The host supplies
// `now`; the connector must not run a hidden renewal loop.
type AuthLifecycle interface {
	AuthStatus(ctx context.Context, now time.Time) (AuthSession, error)
	RenewAuth(ctx context.Context, s AuthSession, now time.Time) (AuthSession, error)
	Reauthenticate(ctx context.Context, boot *Bootstrap, now time.Time) (AuthSession, error)
}
