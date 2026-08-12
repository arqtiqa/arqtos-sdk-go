package transport

import (
	"github.com/arqtiqa/arqtos-sdk-go/authenticator"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
)

// ChallengeToPB marshals a challenge for the wire.
func ChallengeToPB(c authenticator.Challenge) *connectorpb.Challenge {
	return &connectorpb.Challenge{AuthorizationUrl: c.AuthorizationURL, Handle: c.Handle}
}

// ChallengeFromPB unmarshals a challenge.
//
// A nil message yields the zero Challenge, which the host-side guard refuses as
// an incomplete challenge — a provider that sent nothing and a provider that
// sent an empty message are the same failure and should read the same way.
func ChallengeFromPB(pb *connectorpb.Challenge) authenticator.Challenge {
	if pb == nil {
		return authenticator.Challenge{}
	}
	return authenticator.Challenge{AuthorizationURL: pb.GetAuthorizationUrl(), Handle: pb.GetHandle()}
}

// AssertionToPB marshals an assertion for the wire.
//
// ⚠️ There is deliberately no token, id token or claim-set field to carry, on
// either side of this function. If a future change adds one to either type,
// this function is where the omission would be noticed — and the answer is to
// refuse the field, not to marshal it.
func AssertionToPB(a authenticator.Assertion) *connectorpb.Assertion {
	return &connectorpb.Assertion{
		PrincipalId:   a.PrincipalID,
		Authenticated: a.Authenticated,
		Active:        a.Active,
	}
}

// AssertionFromPB unmarshals an assertion.
//
// A nil message yields the zero Assertion — authenticated false, no principal —
// which is COHERENT, and therefore passes the coherence guard. That is correct
// rather than a hole: an assertion arriving beside an error is refused by the
// guard whatever it contains, and an assertion arriving with no error and no
// content is the genuine anonymous answer the contract admits.
func AssertionFromPB(pb *connectorpb.Assertion) authenticator.Assertion {
	if pb == nil {
		return authenticator.Assertion{}
	}
	return authenticator.Assertion{
		PrincipalID:   pb.GetPrincipalId(),
		Authenticated: pb.GetAuthenticated(),
		Active:        pb.GetActive(),
	}
}
