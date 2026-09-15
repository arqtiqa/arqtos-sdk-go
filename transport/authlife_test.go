package transport_test

import (
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

func TestAuthSessionRoundTripKindAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	s := credential.AuthSession{
		ID:        "auth-handle",
		Kind:      credential.KindAuth,
		ExpiresAt: now.Add(time.Hour),
		Renewable: true,
	}
	got := transport.AuthSessionFromPB(transport.AuthSessionToPB(s))
	if err := credential.CheckAuthSession(got); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if got.ID != s.ID || !got.ExpiresAt.Equal(s.ExpiresAt) || got.Renewable != s.Renewable {
		t.Fatalf("got %+v", got)
	}
}

func TestAuthSessionFromPBRejectsSecretLeaseKind(t *testing.T) {
	got := transport.AuthSessionFromPB(&connectorpb.AuthSession{
		Id:            "dyn-123",
		Kind:          string(credential.KindSecretLease),
		ExpiresAtUnix: time.Now().Unix(),
		Renewable:     true,
	})
	if err := credential.CheckAuthSession(got); err == nil {
		t.Fatal("secret_lease kind survived the wire as an auth session")
	}
}
