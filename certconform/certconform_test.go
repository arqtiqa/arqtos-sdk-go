package certconform_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/certconform"
	"github.com/arqtiqa/arqtos-sdk-go/certificate"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

func TestRun_SignThenVerifyAndTamperRefuses(t *testing.T) {
	c := newFake(t)
	rep, err := certconform.Run(context.Background(), c, certconform.Options{
		Manifest: manifest.Doc{Name: "mem-cert", Implements: connector.ClassCertificate, Kind: manifest.KindNative},
		KeyID:    "key-1",
		Payload:  []byte("payload-v1"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := rep.Err(); err != nil {
		t.Fatalf("report: %v", err)
	}
	want := []string{
		certconform.CheckManifest,
		certconform.CheckCapabilityHonesty,
		certconform.CheckSignThenVerify,
		certconform.CheckTamperRefuses,
		certconform.CheckPublicKey,
	}
	if len(rep.Results) != len(want) {
		t.Fatalf("results = %d, want %d (%v)", len(rep.Results), len(want), names(rep))
	}
	for i, n := range want {
		if rep.Results[i].Name != n || !rep.Results[i].Pass {
			t.Fatalf("result[%d] = %+v, want pass %s", i, rep.Results[i], n)
		}
	}
}

func TestRun_NilConnectorIsInvalid(t *testing.T) {
	_, err := certconform.Run(context.Background(), nil, certconform.Options{})
	if err == nil {
		t.Fatal("nil connector accepted")
	}
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("err = %v, want KindInvalid", err)
	}
}

type fakeCert struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newFake(t *testing.T) *fakeCert {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeCert{pub: pub, priv: priv}
}

func (f *fakeCert) Implements() connector.Class { return connector.ClassCertificate }
func (f *fakeCert) Capabilities() connector.Capabilities {
	return certificate.KnownCapabilities()
}
func (f *fakeCert) Health(context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}
func (f *fakeCert) Close() error { return nil }

func (f *fakeCert) Sign(_ context.Context, keyID string, payload []byte) (certificate.Signature, error) {
	if keyID != "key-1" {
		return certificate.Signature{}, cerr.New(cerr.KindNotFound, "Sign", nil)
	}
	return certificate.Signature{Bytes: ed25519.Sign(f.priv, payload), Algorithm: "ed25519"}, nil
}

func (f *fakeCert) Verify(_ context.Context, keyID string, payload, sig []byte) error {
	if keyID != "key-1" {
		return cerr.New(cerr.KindNotFound, "Verify", nil)
	}
	if !ed25519.Verify(f.pub, payload, sig) {
		return cerr.New(cerr.KindInvalid, "Verify", nil)
	}
	return nil
}

func (f *fakeCert) PublicKey(_ context.Context, keyID string) (certificate.PublicKey, error) {
	if keyID != "key-1" {
		return certificate.PublicKey{}, cerr.New(cerr.KindNotFound, "PublicKey", nil)
	}
	return certificate.PublicKey{Bytes: append(ed25519.PublicKey(nil), f.pub...), Algorithm: "ed25519"}, nil
}

func names(r certconform.Report) []string {
	out := make([]string, len(r.Results))
	for i, res := range r.Results {
		out[i] = res.Name
	}
	return out
}
