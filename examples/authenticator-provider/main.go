// Command authenticator-provider is a vendor-free, out-of-process (Track-B)
// reference implementation of the authenticator.Authenticator connector class.
// It serves a fixed, PLACEHOLDER-only identity provider over
// hashicorp/go-plugin's gRPC transport, using nothing from this module beyond
// the public authenticator/cerr/connector/plugin packages — no vendor SDK.
//
// This file is the template a connector author copies to start a real provider
// (Okta, Entra, Google Workspace, Neon Auth): swap memAuth's fields and method
// bodies for calls to the actual OIDC endpoints; the plugin.Handshake +
// plugin.AuthenticatorPluginMap(...) + goplugin.Serve wiring in main() does not
// change.
//
// # The four things worth copying, beyond the wiring
//
//   - The provider NEVER OPENS A BROWSER AND NEVER BINDS A PORT. Begin returns
//     an authorization URL and an opaque handle; the host drives the browser
//     and receives the authorization code on its own loopback listener. A
//     provider that bound the listener would be holding the operator's token,
//     which contract invariant 2 prohibits.
//
//   - The PKCE verifier and the nonce live in the provider's own memory against
//     the handle, and are destroyed when the handle is consumed or Close is
//     called. They never travel — a handle carrying the verifier would put the
//     one value that makes the authorization code useless into the same place
//     as the code.
//
//   - A REJECTED assertion is a typed failure, never Authenticated false. The
//     shape to avoid is returning an empty Assertion with a nil error on a path
//     that failed verification: proto3 omits both defaults entirely, so that is
//     precisely what a forgotten error return produces on the wire, and it
//     reports attacker-supplied input as a legitimate anonymous session.
//
//   - A verified-but-DISABLED principal is an ANSWER — Authenticated true,
//     Active false — not an error. Raising it as a failure removes the host's
//     ability to tell "suspended" from "could not authenticate", which are
//     different sessions.
//
// It declares NO capabilities, because the class's vocabulary is empty at v1.
//
// See conform_test.go in this directory for the in-process conformance run, and
// roundtrip_test.go for a real-subprocess round trip driving this exact binary
// the way a host does.
//
// No real tenant data: every identifier below is a placeholder, and no network
// call is made anywhere in this file.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/arqtiqa/arqtos-sdk-go/authenticator"
	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
)

// The placeholder fixtures this provider serves. A real provider replaces every
// one of these with a call to its identity provider.
const (
	// PrincipalID is the STABLE identifier — not an address. A host joins it
	// onto a directory's Principal.ID, and an address would silently match
	// nobody after a rename.
	PrincipalID = "00u-placeholder-principal"
	// ValidCode completes successfully and resolves to PrincipalID.
	ValidCode = "placeholder-code-valid"
	// RejectedCode fails verification.
	RejectedCode = "placeholder-code-rejected"
	// InactiveCode verifies, and resolves to a principal the provider reports
	// as disabled.
	InactiveCode = "placeholder-code-inactive"
	// AuthorizationURL is where a host would send the operator's browser.
	AuthorizationURL = "https://idp.placeholder.invalid/authorize?client_id=placeholder"
)

// memAuth is an in-memory Authenticator.
//
// The handle table is the part worth copying: a provider must be able to say
// "I never issued this" and "I already consumed this", and it can only do that
// by remembering what it issued.
type memAuth struct {
	mu sync.Mutex
	// issued maps a live handle to the per-exchange secrets held against it.
	// A REAL provider stores the PKCE verifier and the nonce here; they are
	// named rather than stored in this placeholder because there is nothing to
	// verify against.
	issued map[string]exchange
	next   int
	closed bool
}

// exchange is what a provider holds against one handle. In a real provider the
// verifier and nonce are secrets, which is why they live here and never in the
// Challenge.
type exchange struct {
	verifier string
	nonce    string
}

func newMemAuth() *memAuth { return &memAuth{issued: map[string]exchange{}} }

func (m *memAuth) Implements() connector.Class { return connector.ClassAuthenticator }

// Capabilities is empty, because the class's vocabulary is empty at v1. A
// provider declaring anything here is refused by the manifest schema.
func (m *memAuth) Capabilities() connector.Capabilities { return nil }

func (m *memAuth) Health(context.Context) (connector.Health, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return connector.Health{Status: connector.Unavailable, Detail: "closed"}, nil
	}
	return connector.Health{Status: connector.Healthy}, nil
}

// Begin issues a handle and returns the URL the host sends the operator to.
//
// A real provider generates a PKCE verifier and a nonce here, retains them
// against the handle, and derives the code challenge into the authorization
// URL. It does NOT open a browser and does NOT bind a listener.
func (m *memAuth) Begin(context.Context) (authenticator.Challenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return authenticator.Challenge{}, cerr.New(cerr.KindUnavailable, "Begin", errors.New("closed"))
	}
	m.next++
	h := fmt.Sprintf("placeholder-handle-%d", m.next)
	m.issued[h] = exchange{
		verifier: "placeholder-verifier",
		nonce:    fmt.Sprintf("placeholder-nonce-%d", m.next),
	}
	return authenticator.Challenge{AuthorizationURL: AuthorizationURL, Handle: h}, nil
}

// Complete exchanges the code and verifies the result.
//
// Note what each failure is, and what none of them is: not one of them returns
// an Assertion. A rejected exchange has nothing to report.
func (m *memAuth) Complete(_ context.Context, handle, code string) (authenticator.Assertion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return authenticator.Assertion{}, cerr.New(cerr.KindUnavailable, "Complete", errors.New("closed"))
	}

	// A handle this provider never issued, and one already consumed, are
	// distinguishable outcomes for an operator reading the message — and the
	// same classification for a host routing on it.
	if _, live := m.issued[handle]; !live {
		return authenticator.Assertion{}, cerr.New(cerr.KindInvalid, "Complete", fmt.Errorf(
			"handle %q was never issued by this provider, or has already been consumed", handle))
	}

	switch code {
	case RejectedCode:
		// ⚠️ THE SHAPE TO COPY. Verification failed, so this returns a typed
		// error and NO assertion. Returning Assertion{} with a nil error here
		// would report an attacker-supplied assertion as a legitimate anonymous
		// session, and proto3 would put neither default on the wire.
		delete(m.issued, handle)
		return authenticator.Assertion{}, &authenticator.VerificationError{
			Failure: authenticator.VerificationBadSignature,
			Detail:  "placeholder provider: this code is the rejection fixture",
		}
	case InactiveCode:
		// A verified principal who is disabled. This is an ANSWER.
		delete(m.issued, handle)
		return authenticator.Assertion{PrincipalID: PrincipalID, Authenticated: true, Active: false}, nil
	case ValidCode:
		delete(m.issued, handle)
		return authenticator.Assertion{PrincipalID: PrincipalID, Authenticated: true, Active: true}, nil
	default:
		delete(m.issued, handle)
		return authenticator.Assertion{}, cerr.New(cerr.KindInvalid, "Complete", fmt.Errorf(
			"unrecognised authorization code"))
	}
}

// Close destroys every in-flight handle and the secrets held against them.
//
// ⚠️ This is the part a real provider must not skimp on: a verifier outliving
// the session is a credential outliving the session. It is idempotent.
func (m *memAuth) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.issued)
	m.closed = true
	return nil
}

var _ authenticator.Authenticator = (*memAuth)(nil)

func main() {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins:         plugin.AuthenticatorPluginMap(newMemAuth()),
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}
