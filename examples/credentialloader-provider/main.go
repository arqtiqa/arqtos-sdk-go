// Command credentialloader-provider is a vendor-free, out-of-process (Track-B)
// reference implementation of the credential.CredentialLoader connector
// class. It serves a fixed, PLACEHOLDER-only set of op:// refs over
// hashicorp/go-plugin's gRPC transport, using nothing from this module beyond
// the public credential/ref/cerr/connector/plugin packages — no vendor SDK.
//
// This file is the template a connector author copies to start a real
// provider (Infisical, Vault, ...): swap memLoader's field and method bodies
// for calls to the actual backing store; the plugin.Handshake +
// plugin.PluginMap(...) + goplugin.Serve wiring in main() does not change.
// See docs/CONTRACT.md ("Track-B: the out-of-process wire contract") for the
// full picture and roundtrip_test.go in this directory for a real-subprocess
// round-trip driving this exact binary the way a host would.
//
// No real secrets: every value below is a placeholder string, never live
// credential material.
package main

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// referenceRef and referencePlaceholder are the one ref/value pair
// roundtrip_test.go resolves against the real subprocess. They live here
// (rather than duplicated in the test) so the fixture and the served map can
// never drift apart.
const (
	referenceRef         = "op://<vault>/<item>/<field>"
	referencePlaceholder = "placeholder-database-password"
)

// memLoader is the reference CredentialLoader: a fixed, in-memory map of
// op:// refs to PLACEHOLDER values, standing in for whatever a real
// provider's backing store would be. A real provider replaces vals (and the
// method bodies below) with calls to its actual store — nothing else in this
// file changes.
type memLoader struct {
	vals map[string]string // ref.String() -> placeholder value
}

func (m *memLoader) Resolve(_ context.Context, r ref.Ref) (*credential.Material, error) {
	v, ok := m.vals[r.String()]
	if !ok {
		return nil, cerr.New(cerr.KindNotFound, "Resolve", nil)
	}
	return credential.NewMaterial([]byte(v)), nil
}

func (m *memLoader) List(_ context.Context, _ string) ([]ref.Ref, error) {
	refs := make([]ref.Ref, 0, len(m.vals))
	for k := range m.vals {
		r, err := ref.Parse(k)
		if err != nil {
			continue
		}
		refs = append(refs, r)
	}
	return refs, nil
}

// Lease is unsupported by this reference provider: it only models a static
// secret store, not one that issues dynamic, time-bounded credentials.
func (m *memLoader) Lease(_ context.Context, _ ref.Ref) (*credential.Material, credential.Lease, error) {
	return nil, credential.Lease{}, cerr.New(cerr.KindUnsupported, "Lease", nil)
}

func (m *memLoader) Renew(_ context.Context, _ credential.Lease) (credential.Lease, error) {
	return credential.Lease{}, cerr.New(cerr.KindUnsupported, "Renew", nil)
}

func (m *memLoader) Revoke(_ context.Context, _ credential.Lease) error {
	return cerr.New(cerr.KindUnsupported, "Revoke", nil)
}

func (m *memLoader) Implements() connector.Class { return connector.ClassCredentialLoader }

func (m *memLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead}
}

func (m *memLoader) Health(_ context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}

func (m *memLoader) Close() error { return nil }

var _ credential.CredentialLoader = (*memLoader)(nil)

func main() {
	impl := &memLoader{
		vals: map[string]string{
			referenceRef:                   referencePlaceholder,
			"op://<vault>/<other-item>/<field>": "placeholder-api-token",
		},
	}

	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins:         plugin.PluginMap(impl),
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}
