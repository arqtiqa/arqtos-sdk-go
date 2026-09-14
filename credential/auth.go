package credential

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	out := &Bootstrap{keys: make(map[string]*Material, len(keys))}
	for name, b := range keys {
		out.keys[name] = NewMaterial(b)
	}
	return out
}

func (b *Bootstrap) Get(name string) *Material {
	if b == nil {
		return nil
	}
	return b.keys[name]
}

func (b *Bootstrap) Names() []string {
	if b == nil {
		return nil
	}
	names := make([]string, 0, len(b.keys))
	for n := range b.keys {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (b *Bootstrap) Zero() {
	if b == nil {
		return
	}
	for _, m := range b.keys {
		if m != nil {
			m.Zero()
		}
	}
}

func (b Bootstrap) String() string   { return "[REDACTED bootstrap]" }
func (b Bootstrap) GoString() string { return b.String() }

func (p AuthProfile) Validate() error {
	if len(p.Required) == 0 {
		return ErrEmptyProfile
	}
	seen := map[string]struct{}{}
	for _, k := range p.Required {
		if k == "" {
			return fmt.Errorf("%w: empty field", ErrMissingKey)
		}
		if _, ok := seen[k]; ok {
			return fmt.Errorf("%w: %s", ErrDuplicateKey, k)
		}
		seen[k] = struct{}{}
	}
	return nil
}

// CheckBootstrap verifies boot covers profile. It does not read the process
// environment. A missing key is named; material never appears in the error.
func CheckBootstrap(profile AuthProfile, boot *Bootstrap) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	for _, k := range profile.Required {
		if boot == nil || boot.Get(k) == nil {
			return fmt.Errorf("%w: %s", ErrMissingKey, k)
		}
	}
	return nil
}

// ProfileFromAuth builds the required-key set from a released refs-or-ENV_NAME
// manifest Auth map. The map values stay references; they are not material.
func ProfileFromAuth(auth map[string]string) AuthProfile {
	names := make([]string, 0, len(auth))
	for k := range auth {
		names = append(names, k)
	}
	sort.Strings(names)
	return AuthProfile{Required: names}
}

// AuthBinder receives the host's bootstrap set over the authenticated
// provider channel. Old peers that do not implement it return
// cerr.KindUnsupported.
type AuthBinder interface {
	BindAuth(ctx context.Context, boot *Bootstrap) error
}
