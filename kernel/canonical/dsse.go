package canonical

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The DSSE envelope.
//
// # Why this lives beside the encoding rather than in its own package
//
// An envelope is the form the SAME bytes take once they are signed, and
// [ActBodyID] is a digest — already this package's business. The public-half
// decision names five kernel packages, and a sixth for a wrapper over this one's
// output would be a deviation from a ratified list for no reader's benefit.
//
// # ⚠️ THIS PACKAGE DOES NOT VERIFY SIGNATURES
//
// It verifies the BINDING: that an envelope's payload really is the body it
// claims to be. Verifying a SIGNATURE is a different question — which key, and
// was it valid at the time the signature was made — and it needs the key
// registry's validity intervals. That is separate work.
//
// The distinction matters because the two are easy to confuse and the confusion
// is dangerous in one direction: a caller who checked the binding and concluded
// "verified" would be asserting something nobody checked. [Envelope] therefore
// carries signatures opaquely and [VerifyBinding] says in its name exactly how
// much it establishes.

// PayloadType is the DSSE payload type for an act body.
//
// ⚠️ It is a URI-shaped string carrying the version, because DSSE's whole
// pre-authentication design rests on the type being bound into what is signed. A
// type that did not change with the format would let a v1 body be presented as a
// v2 one.
const PayloadType = "application/vnd.arqtos.act-body+json;v=1"

// dssePrefix is the constant DSSE pre-authentication encodings begin with.
const dssePrefix = "DSSEv1"

var (
	// ErrEnvelope is a malformed envelope: a shape no DSSE reader would accept.
	ErrEnvelope = errors.New("canonical: malformed DSSE envelope")
	// ErrBindingMismatch is an envelope whose payload is not the body it claims.
	//
	// ⚠️ Distinct from ErrEnvelope on purpose. A malformed envelope is a
	// transport or authoring bug; a binding mismatch is someone having changed
	// the body under a signature, and the two deserve different reactions.
	ErrBindingMismatch = errors.New("canonical: envelope payload does not match the act body id it carries")
)

// A Signature is one signature over the envelope's pre-authentication encoding.
//
// ⚠️ Carried OPAQUELY. This package neither produces nor checks Sig — see the
// note at the top of this file — and a field this package cannot validate is a
// field it must not appear to have validated.
type Signature struct {
	// KeyID names the key. Whether that key was valid WHEN THE SIGNATURE WAS
	// MADE is the key registry's question, not this package's.
	KeyID string `json:"keyid"`
	// Sig is base64, per DSSE.
	Sig string `json:"sig"`
}

// An Envelope is a DSSE envelope.
//
// ⚠️ The field names and shapes are DSSE's, not ours. Staying standards-shaped
// is the point: generic DSSE decoders and verifiers exist and are in common use,
// so an outsider can read an evidence bundle with a tool they already trust
// rather than one this project ships them. A bespoke envelope would make the
// outsider-replay claim depend on our software being available and trusted,
// which is most of what that claim exists to avoid.
type Envelope struct {
	// Payload is the canonical act body, base64 (standard encoding, padded).
	Payload string `json:"payload"`
	// PayloadType is the type bound into the pre-authentication encoding.
	PayloadType string `json:"payloadType"`
	// Signatures may be empty: an unsigned envelope is a real intermediate
	// state, and it is honest for it to say so rather than be unrepresentable.
	Signatures []Signature `json:"signatures"`
}

// ActBodyID returns the identity of an act body: the domain-separated digest of
// its canonical bytes.
//
// ⚠️ IT IS COMPUTED OVER THE UNSIGNED BODY, and witnesses live outside it. If
// the id covered the witnesses, every added signature or ratification would
// RENAME the act — so a co-signer could invalidate every existing reference to
// it just by co-signing, and an act's identity would depend on how many people
// had looked at it.
func ActBodyID(body any) (string, error) { return Digest(DomainActBody, body) }

// PAE returns the DSSE pre-authentication encoding of a payload.
//
//	DSSEv1 SP LEN(type) SP type SP LEN(payload) SP payload
//
// ⚠️ The lengths are what make it safe, and they are why a signature cannot be
// replayed across types. Without them a crafted type and a crafted payload can
// be split differently by a reader than by the signer, so the bytes that were
// signed and the bytes that are interpreted differ while the concatenation
// matches.
func PAE(payloadType string, payload []byte) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d %s %d ", dssePrefix, len(payloadType), payloadType, len(payload))
	out := append([]byte(b.String()), payload...)
	return out
}

// Enclose canonically encodes body and returns the unsigned envelope carrying
// it, together with the act body id that envelope is bound to.
//
// The caller signs [PAE] of the returned envelope's payload and appends the
// result — this package deliberately does not hold a key.
func Enclose(body any) (Envelope, string, error) {
	raw, err := Encode(body)
	if err != nil {
		return Envelope{}, "", err
	}
	id, err := ActBodyID(body)
	if err != nil {
		return Envelope{}, "", err
	}
	return Envelope{
		Payload:     base64.StdEncoding.EncodeToString(raw),
		PayloadType: PayloadType,
		// ⚠️ An empty, non-nil slice: it marshals as [] rather than null, so an
		// unsigned envelope is still a well-formed DSSE document a generic
		// decoder accepts.
		Signatures: []Signature{},
	}, id, nil
}

// Open decodes an envelope's payload after checking its shape.
//
// ⚠️ It refuses a wrong payload type rather than decoding anyway. The type is
// bound into the pre-authentication encoding, so a reader that accepted a
// mismatched type would be interpreting bytes under a contract nobody signed.
func (e Envelope) Open() ([]byte, error) {
	if e.PayloadType != PayloadType {
		return nil, fmt.Errorf("%w: payloadType is %q, want %q", ErrEnvelope, e.PayloadType, PayloadType)
	}
	if e.Payload == "" {
		return nil, fmt.Errorf("%w: empty payload", ErrEnvelope)
	}
	raw, err := base64.StdEncoding.DecodeString(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not base64: %v", ErrEnvelope, err)
	}
	return raw, nil
}

// VerifyBinding reports whether the envelope's payload really is the act body
// whose id is wantBodyID.
//
// ⚠️ READ THE NAME. This establishes that the bytes are the body they claim to
// be. It establishes NOTHING about who signed them, or whether that signer's key
// was valid at the time. A caller that treated a nil return here as "verified"
// would be asserting something no code checked — which is why this is not called
// Verify.
func (e Envelope) VerifyBinding(wantBodyID string) error {
	raw, err := e.Open()
	if err != nil {
		return err
	}

	// Re-decode and re-encode rather than digesting the payload bytes directly.
	// ⚠️ That is the point of a canonical form: a payload that is merely VALID
	// JSON for the right body but not CANONICAL must be refused, or two
	// different byte strings would both satisfy one id.
	var tree any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return fmt.Errorf("%w: payload is not JSON: %v", ErrEnvelope, err)
	}
	recoded, err := Encode(tree)
	if err != nil {
		return err
	}
	if string(recoded) != string(raw) {
		return fmt.Errorf("%w: payload is valid JSON but is not in canonical form", ErrEnvelope)
	}

	got, err := Digest(DomainActBody, tree)
	if err != nil {
		return err
	}
	if got != wantBodyID {
		return fmt.Errorf("%w: payload digests to %s, envelope is presented as %s", ErrBindingMismatch, got, wantBodyID)
	}
	return nil
}
