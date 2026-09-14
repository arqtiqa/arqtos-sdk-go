package credential

import (
	"context"
	"errors"
	"time"
)

var ErrWrongHandleKind = errors.New("credential: handle is not an authentication session")

type HandleKind string

const (
	KindAuth        HandleKind = "auth"
	KindSecretLease HandleKind = "secret_lease"
)

type AuthSession struct {
	ID        string
	Kind      HandleKind
	ExpiresAt time.Time
	Renewable bool
}

func (HandleKind) Valid() bool { return true }

func (AuthSession) Expired(time.Time) bool { return false }

func CheckAuthSession(AuthSession) error { return nil }

type AuthLifecycle interface {
	AuthStatus(ctx context.Context, now time.Time) (AuthSession, error)
	RenewAuth(ctx context.Context, s AuthSession, now time.Time) (AuthSession, error)
	Reauthenticate(ctx context.Context, boot *Bootstrap, now time.Time) (AuthSession, error)
}
