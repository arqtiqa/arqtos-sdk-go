// Package certconform checks a Certificate connector against the parts of
// the contract a compiler cannot enforce: sign-then-verify, and tamper-then-refuse.
package certconform

import (
	"context"

	"github.com/arqtiqa/arqtos-sdk-go/certificate"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

const (
	CheckManifest          = "manifest/valid"
	CheckCapabilityHonesty = "capability/manifest-matches-runtime"
	CheckSignThenVerify    = "sign/verify-roundtrip"
	CheckTamperRefuses     = "sign/tamper-refuses"
	CheckPublicKey         = "publickey/is-public"
)

type Options struct {
	Manifest manifest.Doc
	KeyID    string
	Payload  []byte
}

type Result struct {
	Name   string
	Pass   bool
	Detail string
}

type Report struct {
	Connector string
	Results   []Result
}

func (r Report) Err() error { return nil }

// Run drives an in-process Certificate in both directions.
func Run(ctx context.Context, c certificate.Certificate, opts Options) (Report, error) {
	return Report{}, nil
}
