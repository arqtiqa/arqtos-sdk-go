package authenticator

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// A VerificationFailure names WHICH check rejected an assertion.
//
// The set is closed and the reasons are distinguishable because an operator
// debugging a refused session needs to know which check said no: a wrong
// audience is a misconfigured application, a replayed nonce is a replay, and an
// expired assertion is a clock or a slow human. Those lead to different fixes.
//
// They all classify as cerr.KindUnauthorized, and that is deliberate rather
// than lossy. The classification is what a HOST routes on, and all five have
// the same routing answer — do not retry, do not trip the breaker, do not start
// the session. Splitting them across cerr Kinds would make a host treat some
// rejected assertions as retryable or as backend load, which is exactly the
// misrouting the closed vocabulary exists to prevent. Distinguishable for the
// operator, uniform for the host.
type VerificationFailure string

const (
	// VerificationBadSignature is an assertion whose signature did not verify
	// against the provider's key set. The key is selected by the assertion's
	// own key id, and the key set is fetched from the provider's discovery
	// document rather than pinned in code.
	VerificationBadSignature VerificationFailure = "bad-signature"
	// VerificationWrongIssuer is an assertion issued by someone other than the
	// configured authorization server.
	VerificationWrongIssuer VerificationFailure = "wrong-issuer"
	// VerificationWrongAudience is an assertion not addressed to this
	// application. Accepting one lets an assertion minted for a different
	// client start a session here.
	VerificationWrongAudience VerificationFailure = "wrong-audience"
	// VerificationReplayedNonce is an assertion whose nonce does not match the
	// one issued with this handle — a replay, or a response to a challenge
	// this connector did not make.
	VerificationReplayedNonce VerificationFailure = "replayed-nonce"
	// VerificationExpired is an assertion outside its validity window, in
	// either direction: expired, or not yet valid.
	VerificationExpired VerificationFailure = "expired"
)

// verificationFailures is the closed reason vocabulary, in a stable order.
var verificationFailures = []VerificationFailure{
	VerificationBadSignature,
	VerificationWrongIssuer,
	VerificationWrongAudience,
	VerificationReplayedNonce,
	VerificationExpired,
}

// VerificationFailures returns the closed set of verification-failure reasons,
// as a copy.
func VerificationFailures() []VerificationFailure { return slices.Clone(verificationFailures) }

// A VerificationError reports that an assertion was REJECTED, naming which
// check rejected it.
//
// It exists as a distinct type so that a rejection can never be expressed as a
// successful [Assertion] carrying Authenticated false. That shape is the worst
// outcome available to this class: an attacker-supplied assertion, reported as
// a legitimate anonymous session, to a host with no way to tell the difference.
type VerificationError struct {
	// Connector is the host's name for the connector that rejected it.
	Connector string
	// Failure is which check said no.
	Failure VerificationFailure
	// Detail is optional context. ⚠️ It MUST NOT carry a claim value, a token,
	// or any part of the assertion — the provider's request id is the useful
	// thing to put here.
	Detail string
}

func (e *VerificationError) Error() string {
	who := "an unnamed connector"
	if e.Connector != "" {
		who = "connector " + strconv.Quote(e.Connector)
	}
	msg := fmt.Sprintf("%s rejected the assertion: %s", who, e.Failure)
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

// Unwrap exposes the rejection to the cerr taxonomy, so classification-only
// host code sees cerr.KindUnauthorized without knowing this type exists.
func (e *VerificationError) Unwrap() error {
	return cerr.New(cerr.KindUnauthorized, "Complete", nil)
}

// A Fault names a way a connector can break the Authenticator contract: not a
// failure it reports (those are cerr Kinds and [VerificationFailure]s, chosen
// by the connector), but a return the contract does not admit at all.
type Fault string

const (
	// FaultIncoherentAssertion is an [Assertion] whose Authenticated and
	// PrincipalID disagree — see [Assertion.Coherent].
	FaultIncoherentAssertion Fault = "incoherent-assertion"
	// FaultAssertionBesideError is a connector returning an assertion that
	// carries something AND an error. A call either answers or fails; one that
	// does both leaves a caller which checked only one of them acting on the
	// other.
	//
	// ⚠️ THE RELATED SHAPE THIS CANNOT CATCH, stated so nobody believes it
	// does. A connector whose verification failed and which returns an
	// anonymous assertion with a NIL error is, from outside, identical to one
	// reporting a genuine anonymous answer: both are the zero [Assertion] and
	// no error. No host-side guard can separate them, because the difference is
	// entirely inside the connector.
	//
	// That obligation therefore rests on the connector, and is caught by the
	// conformance harness driving a stub built to violate it — not here. It is
	// the same class of gap this SDK already records for deliberate
	// misclassification: the type system forces the assertion to be made
	// explicitly, and making it honestly is the author's.
	FaultAssertionBesideError Fault = "assertion-returned-beside-an-error"
	// FaultChallengeIncomplete is a [Challenge] missing the authorization URL
	// or the handle. A host cannot drive the flow with either absent, and the
	// zero value of each is indistinguishable from one the connector forgot to
	// set.
	FaultChallengeIncomplete Fault = "challenge-missing-url-or-handle"
)

// faults is the closed fault vocabulary, in a stable order.
var faults = []Fault{
	FaultIncoherentAssertion,
	FaultAssertionBesideError,
	FaultChallengeIncomplete,
}

// Faults returns the closed set of contract faults, as a copy.
func Faults() []Fault { return slices.Clone(faults) }

// A FaultError reports a CONNECTOR CONTRACT VIOLATION, attributed by name.
//
// It is a distinct type rather than a generic error for the same reason the
// Roster class's is: coercing a broken connector's return into an ordinary
// failure would hide the breakage behind behaviour that looks correct, and the
// operator would go looking for the fault in the identity provider, where it is
// not.
type FaultError struct {
	// Connector is the host's name for the connector at fault.
	Connector string
	// Op is the contract operation, e.g. "Complete".
	Op string
	// Fault is which contract violation occurred.
	Fault Fault
	// Detail is optional context. It never carries assertion contents.
	Detail string
}

func (e *FaultError) Error() string {
	who := "an unnamed connector"
	if e.Connector != "" {
		who = "connector " + strconv.Quote(e.Connector)
	}
	op := e.Op
	if op == "" {
		op = "a contract operation"
	}
	msg := fmt.Sprintf("%s violated the Authenticator contract in %s: %s", who, op, e.Fault)
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

// Unwrap exposes the fault to the cerr taxonomy as cerr.KindContractViolation:
// not retryable, because a broken connector does not improve, and not
// breaker-tripping, because this is not backend load.
func (e *FaultError) Unwrap() error {
	return cerr.New(cerr.KindContractViolation, e.Op, nil)
}

// CheckAssertion is the host-side guard on [Authenticator.Complete].
//
// Pass it whatever the connector returned, with the name the host knows that
// connector by. It returns:
//
//   - a [FaultError] when a verification failure arrives paired with an
//     assertion claiming an anonymous session — the pairing the contract
//     refuses outright;
//   - the connector's own typed failure, unchanged, for any other error. A
//     connector that failed correctly is not at fault;
//   - a [FaultError] when the assertion is incoherent;
//   - the assertion unchanged when it is conformant, INCLUDING a verified
//     principal reported inactive, which is a real answer.
//
// What the guard adds over the type system is ATTRIBUTION — which connector, in
// which operation — because a host holding several connectors otherwise cannot
// tell which one broke.
func CheckAssertion(connectorName string, a Assertion, err error) (Assertion, error) {
	const op = "Complete"

	if err != nil {
		// An error and a populated assertion together is a violation whichever
		// error it is: the call either answers or fails. The ZERO assertion
		// beside an error is the correct shape and passes through — a failing
		// call has nothing to report, and that is what nothing looks like.
		if a != (Assertion{}) {
			return Assertion{}, &FaultError{
				Connector: connectorName, Op: op, Fault: FaultAssertionBesideError,
				Detail: "a call either answers or fails; returning both leaves a caller that checked only one " +
					"of them acting on the other",
			}
		}
		// Attribute a rejection that named no connector, so a host holding
		// several can tell which one refused.
		var ve *VerificationError
		if asVerificationError(err, &ve) && ve.Connector == "" {
			ve.Connector = connectorName
		}
		return Assertion{}, err
	}

	if !a.Coherent() {
		return Assertion{}, &FaultError{
			Connector: connectorName, Op: op, Fault: FaultIncoherentAssertion,
			Detail: "Authenticated and PrincipalID disagree; an authenticated assertion naming nobody cannot " +
				"be acted on, and a principal named while denying authentication is a name nothing verified",
		}
	}
	return a, nil
}

// CheckChallenge is the host-side guard on [Authenticator.Begin]: a challenge
// missing either half cannot drive the flow, and each zero value is
// indistinguishable from one the connector forgot to set.
func CheckChallenge(connectorName string, c Challenge, err error) (Challenge, error) {
	const op = "Begin"

	if err != nil {
		return Challenge{}, err
	}
	if c.AuthorizationURL == "" || c.Handle == "" {
		return Challenge{}, &FaultError{
			Connector: connectorName, Op: op, Fault: FaultChallengeIncomplete,
			Detail: "a challenge needs both an authorization URL to send the operator to and a handle to " +
				"complete against",
		}
	}
	return c, nil
}

// asVerificationError reports whether err is (or wraps) a *VerificationError,
// assigning it to target. It is a small helper so the two call sites in
// [CheckAssertion] read as one question.
func asVerificationError(err error, target **VerificationError) bool {
	for e := err; e != nil; {
		if ve, ok := e.(*VerificationError); ok {
			*target = ve
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
