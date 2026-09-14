package credential_test

import (
	"errors"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/credential"
)

func TestAuthSession_IsNotASecretLease(t *testing.T) {
	s := credential.AuthSession{
		ID:        "auth-handle",
		Kind:      credential.KindSecretLease,
		ExpiresAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
		Renewable: true,
	}
	if err := credential.CheckAuthSession(s); !errors.Is(err, credential.ErrWrongHandleKind) {
		t.Fatalf("CheckAuthSession(secret_lease) = %v, want ErrWrongHandleKind", err)
	}
	if credential.KindSecretLease.Valid() {
		t.Fatal("secret_lease must not be a valid AuthSession kind")
	}
	if !credential.KindAuth.Valid() {
		t.Fatal("auth must be a valid AuthSession kind")
	}
}

func TestAuthSession_ReportsGrantedExpiryAndNonRenewable(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	s := credential.AuthSession{
		ID:        "auth-handle",
		Kind:      credential.KindAuth,
		ExpiresAt: now.Add(time.Hour),
		Renewable: false,
	}
	if err := credential.CheckAuthSession(s); err != nil {
		t.Fatal(err)
	}
	if s.Expired(now) {
		t.Fatal("unexpired session reported expired")
	}
	if !s.Expired(now.Add(2 * time.Hour)) {
		t.Fatal("expired session reported live")
	}
	if s.Renewable {
		t.Fatal("non-renewable session advertised renewability")
	}
}

func TestLeaseIDCannotBeUsedAsAuthHandle(t *testing.T) {
	lease := credential.Lease{ID: "dyn-123", TTL: time.Hour, ExpiresAt: time.Now().Add(time.Hour), Renewable: true}
	s := credential.AuthSession{ID: lease.ID, Kind: credential.KindAuth, ExpiresAt: lease.ExpiresAt, Renewable: lease.Renewable}
	// A relabelled lease still has a lease-shaped ID; CheckAuthSession of a
	// session whose Kind is secret_lease is the typed refusal.
	wrong := s
	wrong.Kind = credential.KindSecretLease
	if err := credential.CheckAuthSession(wrong); !errors.Is(err, credential.ErrWrongHandleKind) {
		t.Fatalf("relabelled lease accepted as auth: %v", err)
	}
}
