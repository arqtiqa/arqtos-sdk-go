package main

import (
	"context"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credconform"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// TestMemLoaderIsConformant is the second thing this template demonstrates,
// alongside the real-subprocess round trip in roundtrip_test.go: a capability
// is not "declared" by being listed in Capabilities() alone. credconform.Run
// checks that the manifest, Capabilities(), and the connector's own
// behaviour all agree — in-process, before anything is ever served over the
// wire — for BOTH capabilities this template carries: the baseline CapRead
// and the second one, CapBatchResolve, that this file exists to demonstrate
// wiring correctly. Copy this alongside main.go's memLoader as the pattern
// for verifying your own capability in your own CI.
func TestMemLoaderIsConformant(t *testing.T) {
	impl := &memLoader{vals: map[string]string{
		referenceRef: referencePlaceholder,
	}}

	resolvable, err := ref.Parse(referenceRef)
	if err != nil {
		t.Fatalf("ref.Parse(%q): %v", referenceRef, err)
	}
	unresolvable, err := ref.Parse("op://<vault>/<no-such-item>/<field>")
	if err != nil {
		t.Fatalf("ref.Parse: %v", err)
	}

	m := manifest.Doc{
		Name:           "credentialloader-provider-example",
		Implements:     connector.ClassCredentialLoader,
		Kind:           manifest.KindProvider,
		MinHostVersion: "0.1.0",
		Capabilities:   []connector.Capability{credential.CapRead, credential.CapBatchResolve},
	}

	rep, err := credconform.Run(context.Background(), impl, credconform.Options{
		Manifest:     m,
		Resolvable:   []ref.Ref{resolvable},
		Unresolvable: unresolvable,
	})
	if err != nil {
		t.Fatalf("credconform.Run could not be carried out: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("the reference provider must be conformant, including CapBatchResolve:\n%s", rep)
	}
}
