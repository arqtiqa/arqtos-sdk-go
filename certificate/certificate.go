// Package certificate defines the Certificate connector-class contract:
// data goes in, a signature comes back, and private key material never leaves.
//
// This is the inverted invariant of CredentialLoader. Same vendors often serve
// both; they must never be one class.
//
// doc-arq-00015 §3.1; doc-arq-00016 §10.3; arqtos-sdk-go#138.
package certificate

import (
	"context"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// KnownCapabilities is empty at v1: Sign, Verify and PublicKey are required,
// not optional tiers.
func KnownCapabilities() connector.Capabilities { return nil }

// A Signature is the result of Sign. It never carries a private key.
type Signature struct {
	Bytes     []byte
	Algorithm string
}

// A PublicKey is public material only.
type PublicKey struct {
	Bytes     []byte
	Algorithm string
}

// Certificate signs and verifies without exporting private keys.
type Certificate interface {
	connector.Connector
	Sign(ctx context.Context, keyID string, payload []byte) (Signature, error)
	Verify(ctx context.Context, keyID string, payload, signature []byte) error
	PublicKey(ctx context.Context, keyID string) (PublicKey, error)
}
