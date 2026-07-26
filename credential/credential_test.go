package credential_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// leaksSecretBytes reports whether s carries the raw secret bytes in any form
// fmt might render them in: the literal ASCII text, fmt's decimal []byte
// rendering (e.g. "[115 51 99 114 101 116]"), or Go-syntax hex byte literals
// (e.g. "0x73, 0x33, ..."). A pointer-receiver-only String()/GoString() on
// Material means a *value* (or one embedded by value in another struct)
// falls back to fmt's default struct/field rendering, which prints the
// unexported b []byte field in one of these forms instead of redacting it.
func leaksSecretBytes(s string, secret []byte) bool {
	if strings.Contains(s, string(secret)) {
		return true
	}
	dec := strings.Trim(fmt.Sprintf("%v", secret), "[]")
	if dec != "" && strings.Contains(s, dec) {
		return true
	}
	hexParts := make([]string, len(secret))
	for i, b := range secret {
		hexParts[i] = fmt.Sprintf("0x%02x", b)
	}
	hex := strings.Join(hexParts, ", ")
	if hex != "" && strings.Contains(s, hex) {
		return true
	}
	return false
}

func TestMaterialRedactsAndWipes(t *testing.T) {
	secret := []byte("s3cret")
	m := credential.NewMaterial(secret)
	if strings.Contains(m.String(), "s3cret") {
		t.Fatalf("String() must not reveal material: %q", m.String())
	}
	if got := fmt.Sprintf("%v", *m); leaksSecretBytes(got, secret) {
		t.Fatalf("%%v of a Material value must not reveal material: %q", got)
	}
	if got := fmt.Sprintf("%+v", *m); leaksSecretBytes(got, secret) {
		t.Fatalf("%%+v of a Material value must not reveal material: %q", got)
	}
	if got := fmt.Sprintf("%#v", *m); leaksSecretBytes(got, secret) {
		t.Fatalf("%%#v of a Material value must not reveal material: %q", got)
	}
	if got := fmt.Sprintf("%+v", struct{ M credential.Material }{M: *m}); leaksSecretBytes(got, secret) {
		t.Fatalf("%%+v of a struct embedding a Material value must not reveal material: %q", got)
	}
	if string(m.Reveal()) != "s3cret" {
		t.Fatalf("Reveal() wrong")
	}
	m.Zero()
	if len(m.Reveal()) != 0 {
		t.Fatalf("Zero() must wipe material")
	}
}

func TestLeaseExpired(t *testing.T) {
	now := time.Unix(1000, 0)
	l := credential.Lease{ExpiresAt: now.Add(time.Minute)}
	if l.Expired(now) {
		t.Fatalf("not expired yet")
	}
	if !l.Expired(now.Add(2 * time.Minute)) {
		t.Fatalf("should be expired")
	}
}

// Compile-time proof a type can satisfy CredentialLoader.
type stubLoader struct{ connector.Connector }

func (stubLoader) Resolve(context.Context, ref.Ref) (credential.Resolution, error) {
	return credential.Resolved(credential.NewMaterial([]byte("placeholder")))
}
func (stubLoader) List(context.Context, string) ([]ref.Ref, error) { return nil, nil }
func (stubLoader) Lease(context.Context, ref.Ref) (credential.Resolution, credential.Lease, error) {
	return credential.Resolution{}, credential.Lease{}, cerr.New(cerr.KindUnsupported, "Lease", nil)
}
func (stubLoader) Renew(context.Context, credential.Lease) (credential.Lease, error) {
	return credential.Lease{}, nil
}
func (stubLoader) Revoke(context.Context, credential.Lease) error { return nil }

var _ credential.CredentialLoader = stubLoader{}
