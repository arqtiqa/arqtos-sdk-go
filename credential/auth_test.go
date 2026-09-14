package credential_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/credential"
)

func TestAuthProfile_TokenAndTwoKeyUseTheSameHostAPI(t *testing.T) {
	one := credential.AuthProfile{Required: []string{"token"}}
	two := credential.AuthProfile{Required: []string{"client_id", "client_secret"}}
	if err := one.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := two.Validate(); err != nil {
		t.Fatal(err)
	}
	token := credential.NewBootstrap(map[string][]byte{"token": []byte("tok")})
	pair := credential.NewBootstrap(map[string][]byte{
		"client_id":     []byte("id"),
		"client_secret": []byte("sec"),
	})
	if err := credential.CheckBootstrap(one, token); err != nil {
		t.Fatalf("one-key: %v", err)
	}
	if got := token.Get("token"); got == nil || string(got.Reveal()) != "tok" {
		t.Fatalf("one-key material missing after CheckBootstrap")
	}
	if err := credential.CheckBootstrap(two, pair); err != nil {
		t.Fatalf("two-key: %v", err)
	}
	if got := pair.Get("client_secret"); got == nil || string(got.Reveal()) != "sec" {
		t.Fatalf("two-key material missing after CheckBootstrap")
	}
}

func TestCheckBootstrap_RefusesMissingAndDuplicateKeysWithoutExposingMaterial(t *testing.T) {
	secret := []byte("super-secret-value")
	boot := credential.NewBootstrap(map[string][]byte{"client_id": secret})
	err := credential.CheckBootstrap(credential.AuthProfile{Required: []string{"client_id", "client_secret"}}, boot)
	if !errors.Is(err, credential.ErrMissingKey) {
		t.Fatalf("missing key: %v; want ErrMissingKey", err)
	}
	if !strings.Contains(err.Error(), "client_secret") {
		t.Fatalf("missing-key error %q does not name the field", err)
	}
	if strings.Contains(err.Error(), string(secret)) {
		t.Fatalf("missing-key error leaked material: %v", err)
	}
	dup := credential.AuthProfile{Required: []string{"token", "token"}}
	err = credential.CheckBootstrap(dup, credential.NewBootstrap(map[string][]byte{"token": secret}))
	if !errors.Is(err, credential.ErrDuplicateKey) {
		t.Fatalf("duplicate: %v; want ErrDuplicateKey", err)
	}
	if !strings.Contains(err.Error(), "token") {
		t.Fatalf("duplicate error %q does not name the field", err)
	}
	if strings.Contains(err.Error(), string(secret)) {
		t.Fatalf("duplicate error leaked material: %v", err)
	}
}

func TestCheckBootstrap_DoesNotSubstituteAmbientCredentials(t *testing.T) {
	const ambient = "AMBIENT_BOOTSTRAP_VALUE"
	t.Setenv("TOKEN", ambient)
	boot := credential.NewBootstrap(map[string][]byte{})
	err := credential.CheckBootstrap(credential.AuthProfile{Required: []string{"token"}}, boot)
	if !errors.Is(err, credential.ErrMissingKey) {
		t.Fatalf("ambient env was accepted: %v", err)
	}
	if boot.Get("token") != nil {
		t.Fatal("bootstrap absorbed an inherited environment value")
	}
	if v := os.Getenv("TOKEN"); v != ambient {
		t.Fatalf("test env was disturbed: %q", v)
	}
}

func TestBootstrap_StringRedactsAndZeroWipes(t *testing.T) {
	boot := credential.NewBootstrap(map[string][]byte{"token": []byte("live-material")})
	if s := fmt.Sprintf("%v", boot); strings.Contains(s, "live-material") {
		t.Fatalf("String leaked: %q", s)
	}
	if s := fmt.Sprintf("%#v", boot); strings.Contains(s, "live-material") {
		t.Fatalf("GoString leaked: %q", s)
	}
	got := boot.Get("token").Reveal()
	if string(got) != "live-material" {
		t.Fatalf("Reveal = %q", got)
	}
	boot.Zero()
	if len(boot.Get("token").Reveal()) != 0 {
		t.Fatalf("Zero left material: %q", boot.Get("token").Reveal())
	}
}

func TestProfileFromAuth_UsesReleasedManifestAuthKeys(t *testing.T) {
	profile := credential.ProfileFromAuth(map[string]string{
		"client_secret": "op://<vault>/<item>/<field>",
		"client_id":     "INFISICAL_CLIENT_ID",
	})
	if err := credential.CheckBootstrap(profile, credential.NewBootstrap(map[string][]byte{
		"client_id":     []byte("id"),
		"client_secret": []byte("sec"),
	})); err != nil {
		t.Fatalf("released auth map keys must be the required set: %v", err)
	}
	err := credential.CheckBootstrap(profile, credential.NewBootstrap(map[string][]byte{"client_id": []byte("id")}))
	if !errors.Is(err, credential.ErrMissingKey) || !strings.Contains(err.Error(), "client_secret") {
		t.Fatalf("profile from auth map dropped a required key: %v", err)
	}
}
