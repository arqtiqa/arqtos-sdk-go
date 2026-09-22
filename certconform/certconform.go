// Package certconform checks a Certificate connector against the parts of
// the contract a compiler cannot enforce: sign-then-verify, and tamper-then-refuse.
package certconform

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/certificate"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
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

func (r Report) Failures() []Result {
	var out []Result
	for _, res := range r.Results {
		if !res.Pass {
			out = append(out, res)
		}
	}
	return out
}

func (r Report) Err() error {
	failed := r.Failures()
	if len(failed) == 0 {
		return nil
	}
	parts := make([]string, 0, len(failed))
	for _, f := range failed {
		parts = append(parts, fmt.Sprintf("%s: %s", f.Name, f.Detail))
	}
	return cerr.New(cerr.KindInvalid, "certconform", fmt.Errorf("connector %q: %s", r.connectorName(), strings.Join(parts, "; ")))
}

func (r Report) connectorName() string {
	if r.Connector == "" {
		return "<unnamed>"
	}
	return r.Connector
}

func (r *Report) add(name string, pass bool, detail string) {
	r.Results = append(r.Results, Result{Name: name, Pass: pass, Detail: detail})
}

// Run drives an in-process Certificate in both directions.
func Run(ctx context.Context, c certificate.Certificate, opts Options) (Report, error) {
	if c == nil {
		return Report{}, cerr.New(cerr.KindInvalid, "certconform.Run", fmt.Errorf("nil connector"))
	}
	if opts.KeyID == "" || len(opts.Payload) == 0 {
		return Report{}, cerr.New(cerr.KindInvalid, "certconform.Run", fmt.Errorf("KeyID and Payload are required"))
	}
	rep := Report{Connector: opts.Manifest.Name}
	checkManifest(&rep, opts.Manifest)
	checkCapabilityHonesty(&rep, c, opts.Manifest)
	sig, ok := checkSignThenVerify(ctx, &rep, c, opts)
	if ok {
		checkTamperRefuses(ctx, &rep, c, opts, sig)
	} else {
		rep.add(CheckTamperRefuses, false, "skipped: sign/verify did not pass")
	}
	checkPublicKey(ctx, &rep, c, opts, sig)
	return rep, nil
}

func checkManifest(rep *Report, m manifest.Doc) {
	if err := m.Validate(); err != nil {
		rep.add(CheckManifest, false, err.Error())
		return
	}
	if m.Implements != connector.ClassCertificate {
		rep.add(CheckManifest, false, fmt.Sprintf("manifest implements %q; this harness checks %q", m.Implements, connector.ClassCertificate))
		return
	}
	rep.add(CheckManifest, true, "")
}

func checkCapabilityHonesty(rep *Report, c certificate.Certificate, m manifest.Doc) {
	runtime := c.Capabilities()
	declared := connector.Capabilities(m.Capabilities)
	if len(runtime) != len(declared) {
		rep.add(CheckCapabilityHonesty, false, fmt.Sprintf("runtime %v declared %v", runtime, declared))
		return
	}
	for _, cap := range declared {
		if !runtime.Has(cap) {
			rep.add(CheckCapabilityHonesty, false, fmt.Sprintf("declared %q missing at runtime", cap))
			return
		}
	}
	rep.add(CheckCapabilityHonesty, true, "")
}

func checkSignThenVerify(ctx context.Context, rep *Report, c certificate.Certificate, opts Options) (certificate.Signature, bool) {
	sig, err := c.Sign(ctx, opts.KeyID, opts.Payload)
	if err != nil {
		rep.add(CheckSignThenVerify, false, err.Error())
		return certificate.Signature{}, false
	}
	if len(sig.Bytes) == 0 {
		rep.add(CheckSignThenVerify, false, "Sign returned an empty signature")
		return certificate.Signature{}, false
	}
	if err := c.Verify(ctx, opts.KeyID, opts.Payload, sig.Bytes); err != nil {
		rep.add(CheckSignThenVerify, false, err.Error())
		return certificate.Signature{}, false
	}
	rep.add(CheckSignThenVerify, true, "")
	return sig, true
}

func checkTamperRefuses(ctx context.Context, rep *Report, c certificate.Certificate, opts Options, sig certificate.Signature) {
	tampered := append(append([]byte{}, opts.Payload...), 'x')
	err := c.Verify(ctx, opts.KeyID, tampered, sig.Bytes)
	if err == nil {
		rep.add(CheckTamperRefuses, false, "tampered payload verified")
		return
	}
	rep.add(CheckTamperRefuses, true, "")
}

func checkPublicKey(ctx context.Context, rep *Report, c certificate.Certificate, opts Options, sig certificate.Signature) {
	pk, err := c.PublicKey(ctx, opts.KeyID)
	if err != nil {
		rep.add(CheckPublicKey, false, err.Error())
		return
	}
	if len(pk.Bytes) == 0 {
		rep.add(CheckPublicKey, false, "empty public key")
		return
	}
	if bytes.Equal(pk.Bytes, sig.Bytes) {
		rep.add(CheckPublicKey, false, "public key equals signature bytes")
		return
	}
	rep.add(CheckPublicKey, true, "")
}
