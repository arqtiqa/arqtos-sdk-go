// Command credentialloader-provider is a vendor-free, out-of-process (Track-B)
// reference implementation of the credential.CredentialLoader connector
// class. It serves a fixed, PLACEHOLDER-only set of op:// refs over
// hashicorp/go-plugin's gRPC transport, using nothing from this module beyond
// the public credential/ref/cerr/connector/plugin packages — no vendor SDK.
//
// This file is the template a connector author copies to start a real
// provider (Infisical, Vault, ...): swap memLoader's field and method bodies
// for calls to the actual backing store; the plugin.Handshake +
// plugin.PluginMap(...) + goplugin.Serve wiring in main() does not change. It
// also declares and implements CapBatchResolve, CapBindAuth, CapGrantBundle
// and CapAuthLifecycle,
// so a copier sees optional capabilities wired correctly end to end rather
// than only the baseline CapRead — see conform_test.go. See docs/CONTRACT.md
// ("Track-B: the out-of-process wire contract") for the full picture and
// roundtrip_test.go in this directory for a real-subprocess round-trip
// driving this exact binary the way a host would.
//
// No real secrets: every value below is a placeholder string, never live
// credential material.
package main

import (
	"context"
	"time"

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

// Resolve is the shape a real provider copies. Two things are load-bearing:
//
//   - a failure is TYPED — a cerr.Kind from the closed vocabulary, so the host
//     acts on the classification and never on the message text;
//   - the value goes back through credential.Resolved, which refuses to
//     report a success carrying nothing. A backend that returns empty output
//     with a success exit code (the classic signed-out read) therefore
//     surfaces as a fault instead of as an empty credential, without this
//     function containing a single emptiness check.
func (m *memLoader) Resolve(_ context.Context, r ref.Ref) (credential.Resolution, error) {
	v, ok := m.vals[r.String()]
	if !ok {
		return credential.Resolution{}, cerr.New(cerr.KindNotFound, "Resolve", nil)
	}
	return credential.Resolved(credential.NewMaterial([]byte(v)))
}

// ResolveBatch is the second thing this template copies: a capability
// declared, implemented, and verified — not just CapRead. This reference
// provider has no backend that genuinely answers many refs in one call, so it
// loops; a real batching backend replaces the loop with its own bulk-fetch
// call. What must not change is the RESULT SHAPE: exactly one BatchResult per
// requested ref, in request order, each built through BatchResolved or
// BatchFailed so "resolved and failed" and "neither" stay unconstructible
// here too.
func (m *memLoader) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	out := make([]credential.BatchResult, 0, len(refs))
	for _, r := range refs {
		res, err := m.Resolve(ctx, r)
		if err != nil {
			br, berr := credential.BatchFailed(r, err)
			if berr != nil {
				return nil, berr
			}
			out = append(out, br)
			continue
		}
		br, berr := credential.BatchResolved(r, res)
		if berr != nil {
			return nil, berr
		}
		out = append(out, br)
	}
	return out, nil
}

var _ credential.BatchResolver = (*memLoader)(nil)

func (m *memLoader) BindAuth(_ context.Context, boot *credential.Bootstrap) error {
	defer boot.Zero()
	return credential.CheckBootstrap(credential.AuthProfile{Required: []string{"token"}}, boot)
}

var _ credential.AuthBinder = (*memLoader)(nil)

func (m *memLoader) AcquireBundle(ctx context.Context, inv credential.Inventory) (credential.Bundle, error) {
	entries := make([]credential.BundleEntry, 0, len(inv.Keys))
	for _, k := range inv.Keys {
		res, err := m.Resolve(ctx, k)
		if err != nil {
			return credential.Bundle{}, err
		}
		entries = append(entries, credential.BundleValue(k, res))
	}
	return credential.CompleteBundle(inv, entries, credential.BundleMeta{Generation: "g1", Source: "example"})
}

var _ credential.BundleAcquirer = (*memLoader)(nil)

func (m *memLoader) AuthStatus(_ context.Context, now time.Time) (credential.AuthSession, error) {
	return credential.AuthSession{ID: "auth-handle", Kind: credential.KindAuth, ExpiresAt: now.Add(time.Hour), Renewable: false}, nil
}

func (m *memLoader) RenewAuth(_ context.Context, s credential.AuthSession, now time.Time) (credential.AuthSession, error) {
	if err := credential.CheckAuthSession(s); err != nil {
		return credential.AuthSession{}, cerr.New(cerr.KindInvalid, "RenewAuth", err)
	}
	return credential.AuthSession{}, cerr.New(cerr.KindUnsupported, "RenewAuth", nil)
}

func (m *memLoader) Reauthenticate(_ context.Context, boot *credential.Bootstrap, now time.Time) (credential.AuthSession, error) {
	if err := m.BindAuth(context.Background(), boot); err != nil {
		return credential.AuthSession{}, err
	}
	return m.AuthStatus(context.Background(), now)
}

var _ credential.AuthLifecycle = (*memLoader)(nil)

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
func (m *memLoader) Lease(_ context.Context, _ ref.Ref) (credential.Resolution, credential.Lease, error) {
	return credential.Resolution{}, credential.Lease{}, cerr.New(cerr.KindUnsupported, "Lease", nil)
}

func (m *memLoader) Renew(_ context.Context, _ credential.Lease) (credential.Lease, error) {
	return credential.Lease{}, cerr.New(cerr.KindUnsupported, "Renew", nil)
}

func (m *memLoader) Revoke(_ context.Context, _ credential.Lease) error {
	return cerr.New(cerr.KindUnsupported, "Revoke", nil)
}

func (m *memLoader) Implements() connector.Class { return connector.ClassCredentialLoader }

// Capabilities declares CapBatchResolve, CapBindAuth, CapGrantBundle and CapAuthLifecycle
// alongside CapRead: a capability that is declared but not implemented, or
// implemented but not declared, fails credconform in either direction.
func (m *memLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapBatchResolve, credential.CapBindAuth, credential.CapGrantBundle, credential.CapAuthLifecycle}
}

func (m *memLoader) Health(_ context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}

func (m *memLoader) Close() error { return nil }

var _ credential.CredentialLoader = (*memLoader)(nil)

func main() {
	impl := &memLoader{
		vals: map[string]string{
			referenceRef:                        referencePlaceholder,
			"op://<vault>/<other-item>/<field>": "placeholder-api-token",
		},
	}

	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins:         plugin.PluginMap(impl),
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}
