// Package authenticator defines the Authenticator connector-class contract:
// establishing WHO IS DRIVING THIS SESSION, interactively and verifiably,
// against an identity provider.
//
// # Why this is not a Roster capability
//
// A [github.com/arqtiqa/arqtos-sdk-go/roster.Roster] reads a directory with a
// service credential and answers "who exists". This class asks "who is here
// right now, and did they prove it". They differ in credential, in lifetime and
// in failure mode, and the last difference is the one that matters: a failed
// directory read is a stale roster, whereas a failed authentication is A
// SESSION THAT MUST NOT START.
//
// Adding an authenticate operation behind a Roster capability would put an
// authentication credential inside a class whose documentation says it reports
// directory facts and holds no policy — and this SDK's capability vocabularies
// are justified by measured differences BETWEEN DIRECTORY READS. An
// authentication flow is not a variation in a directory read.
//
// There is a path that needs no new class at all, and it is rejected knowingly:
// listing every principal and matching the operator's known address yields a
// principal id with no SDK change. That is a LOOKUP, not an authorization. It
// proves nothing about the person at the keyboard, and an unverified identity
// is the silent failure this class exists to prevent.
//
// # Two operations, and the boundary between them
//
// [Authenticator.Begin] returns an authorization URL and an opaque handle;
// [Authenticator.Complete] exchanges an authorization code for an [Assertion].
//
// THE HOST OPENS THE BROWSER AND OWNS THE LOOPBACK REDIRECT LISTENER. The
// connector holds the vendor knowledge and does the verification. It never
// opens a browser and never binds a port.
//
// That division is not a wiring preference. The contract's second invariant is
// passthrough-prohibited: a connector never forwards a raw credential to
// another party. A connector that obtained the operator's token and handed it
// onward would be brokering a credential for a third party.
//
// The single credential-shaped value crossing host to connector is the
// AUTHORIZATION CODE, and that is unavoidable — the host receives it on its own
// redirect and the connector must exchange it. It is acceptable because the
// code is single-use, short-lived, PKCE-bound and useless without the verifier
// the connector holds. NOTHING OBTAINED FROM THE EXCHANGE CROSSES BACK: see
// [Assertion], which has nowhere to put a token.
//
// # A rejected assertion is a failure, never an anonymous session
//
// Every verification failure is a typed error — see [VerificationError]. An
// unverified assertion is not a smaller answer; it is an ATTACKER-SUPPLIED one,
// and reporting it as a legitimate anonymous session is the worst outcome
// available to this class.
//
// [Assertion.Authenticated] false IS a permitted answer: the provider answered
// and identified the caller as nobody. What is refused is reaching that answer
// by failing to verify — [CheckAssertion] raises
// [FaultUnverifiedReportedAsAnonymous] for exactly that pairing.
//
// # The capability vocabulary is empty at v1, deliberately
//
// [KnownCapabilities] returns nothing, and that is a decision rather than an
// omission. Four extensions were each identified and NOT added:
//
//   - a DEVICE-CODE path, for a floe with no browser;
//   - a MACHINE-PRINCIPAL path, for a non-interactive identity;
//   - a REFRESH path, for renewing without re-prompting;
//   - a STEP-UP path, for re-authenticating at higher assurance.
//
// Each is a one-line addition plus a version bump when a consumer appears. A
// capability nothing gates on is the speculative feature flag this SDK's
// capability documentation argues against, and the flow in scope today is
// operator-interactive only.
//
// This reasoning lives here rather than only in a design document so that it is
// not re-litigated by the first person who wants a device-code flow.
package authenticator

import (
	"context"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// knownCapabilities is the closed capability vocabulary of this connector
// class. It is EMPTY at v1 — see the package documentation for the four
// extensions that were deliberately not added.
//
// Empty is not the same as unregistered. The manifest schema closes a
// declaration against this set, so a connector declaring any capability for
// this class is refused; a class with no registered vocabulary would instead
// have its capabilities accepted unchecked.
var knownCapabilities = connector.Capabilities{}

// KnownCapabilities returns the closed capability vocabulary for the
// Authenticator class, as a copy. Adding one is a deliberate contract change.
func KnownCapabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), knownCapabilities...)
}

// A Challenge is what a host needs in order to send the operator to the
// identity provider, and nothing more.
type Challenge struct {
	// AuthorizationURL is where the host sends the operator's browser.
	AuthorizationURL string
	// Handle is an OPAQUE reference to the exchange this challenge began. The
	// host passes it back to [Authenticator.Complete] and never parses it.
	//
	// It is deliberately not the PKCE verifier, not the nonce and not any
	// other secret: those live in the connector's own memory against this
	// handle, and are destroyed when the handle is consumed or Close is
	// called. A handle that carried the verifier would put the one value that
	// makes the authorization code useless into the same place as the code.
	Handle string
}

// An Assertion is the answer to "who is driving this session", after the
// connector has verified it.
//
// It carries NO TOKEN and NO RAW CLAIM SET, and the absence is structural
// rather than advisory: there is nowhere to put one, so passthrough is
// unrepresentable rather than merely discouraged.
type Assertion struct {
	// PrincipalID is the provider's STABLE identifier for the operator — the
	// same identifier space a Roster reports in Principal.ID, so a host can
	// join a just-authenticated operator onto the groups a directory reports.
	//
	// If the two connectors report different spaces the join silently matches
	// nobody while every individual call succeeds, which is why a vendor
	// shipping both is expected to pin the correspondence with a test against
	// a shared fixture.
	PrincipalID string
	// Authenticated is whether the provider verified the operator.
	//
	// It is false only when the provider ANSWERED and identified the caller as
	// nobody. Reaching false by failing to verify is a contract violation —
	// see [VerificationError] and [FaultUnverifiedReportedAsAnonymous].
	Authenticated bool
	// Active is whether the provider considers this principal currently
	// enabled.
	//
	// Active false with Authenticated true is a REAL ANSWER, not an error and
	// not a default: a verified principal who is suspended, on leave or
	// mid-transfer. The host decides what to do with it; the connector reports
	// it.
	Active bool
}

// Coherent reports whether this assertion is one the contract admits.
//
// The invariant is that Authenticated and PrincipalID agree. Both incoherent
// shapes are refused for the same underlying reason — a claim without the thing
// that would substantiate it:
//
//   - Authenticated true with no PrincipalID is a success that identifies no
//     one, which a host cannot act on and cannot detect;
//   - a PrincipalID while Authenticated is false is a name nothing verified,
//     which is worse, because it looks exactly like an answer.
//
// It mirrors the identity probe the CodeCI class publishes, deliberately: one
// definition of coherence per class, checked in one place.
func (a Assertion) Coherent() bool { return a.Authenticated == (a.PrincipalID != "") }

// Authenticator is the interactive identity connector-class contract.
//
// Every failure it returns is typed: a *cerr.Error whose Kind comes from cerr's
// closed vocabulary, so a host acts on the classification and never on the
// message. Verification failures are [VerificationError], which classifies as
// cerr.KindUnauthorized while naming WHICH check rejected the assertion.
//
// There are no optional operations and no capabilities at v1 — see the package
// documentation.
type Authenticator interface {
	connector.Connector

	// Begin starts an exchange and returns the [Challenge] the host needs to
	// drive it.
	//
	// The connector generates and retains the PKCE verifier and the nonce
	// against the returned handle. It does not open a browser and does not
	// bind a listener; the host does both.
	Begin(ctx context.Context) (Challenge, error)

	// Complete exchanges the authorization code the host received on its own
	// redirect, verifies the result, and returns the [Assertion].
	//
	// Before returning anything it MUST verify the signature against the
	// provider's key set, the issuer, the audience, the nonce it issued with
	// this handle, and expiry. Any of those failing is a
	// [VerificationError] — never an Assertion with Authenticated false.
	//
	// An unknown, already-consumed or expired handle is cerr.KindInvalid: three
	// distinguishable outcomes, none of which is a successful anonymous
	// assertion.
	Complete(ctx context.Context, handle, code string) (Assertion, error)
}
