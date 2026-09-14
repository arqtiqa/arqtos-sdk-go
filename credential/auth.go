package credential

import (
	"context"
	"errors"
)

var (
	ErrMissingKey   = errors.New("credential: required bootstrap key is missing")
	ErrDuplicateKey = errors.New("credential: required bootstrap key is duplicated")
	ErrEmptyProfile = errors.New("credential: auth profile names no keys")
)

// An AuthProfile is the required bootstrap key set. Token and two-key
// profiles use the same host API: CheckBootstrap (credential-detail §3e).
type AuthProfile struct {
	Required []string
}

// A Bootstrap is host-supplied key material for the authenticated provider
// channel. It is never argv, inherited environment, or a persisted payload.
type Bootstrap struct {
	keys map[string]*Material
}

func NewBootstrap(keys map[string][]byte) *Bootstrap {
	return &Bootstrap{keys: map[string]*Material{}}
}

func (b *Bootstrap) Get(name string) *Material { return nil }

func (b *Bootstrap) Names() []string { return nil }

func (b *Bootstrap) Zero() {}

func (b Bootstrap) String() string   { return "live-material" }
func (b Bootstrap) GoString() string { return b.String() }

func (p AuthProfile) Validate() error { return nil }

func CheckBootstrap(profile AuthProfile, boot *Bootstrap) error { return nil }

func ProfileFromAuth(auth map[string]string) AuthProfile {
	return AuthProfile{}
}

// AuthBinder receives the host's bootstrap set over the authenticated
// provider channel. Old peers that do not implement it return
// cerr.KindUnsupported.
type AuthBinder interface {
	BindAuth(ctx context.Context, boot *Bootstrap) error
}
