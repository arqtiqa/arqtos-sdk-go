package canonical_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
)

// actBody is a stand-in for the signed intent: every value a string, because the
// canonical profile forbids JSON numbers.
func actBody() map[string]any {
	return map[string]any{
		"act_kind":           "governed_merge",
		"repository_id":      "repo:example/governed",
		"change_request_id":  "cr:example/governed/41",
		"head_sha":           "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
		"base_constraint":    "refs/heads/main@9f8e7d6c",
		"merge_method":       "squash",
		"candidate_tree_oid": "tree:5e6f7081",
		"charter_digest":     "sha256:abc0001",
		"charter_rule_id":    "rule:low-risk-docs",
		"nonce":              "01a01578-ec47-7209-9200-cac2a1f75c7f",
		"valid_until":        "2026-08-21T12:00:00Z",
	}
}

// ⚠️ THE TEST THIS WHOLE STORY EXISTS FOR. Witnesses live outside the signed
// body, so adding one must not move the act's identity. If it did, a co-signer
// could RENAME an act simply by co-signing it, and every existing reference to it
// would break — an act's identity would depend on how many people had looked at
// it.
func TestActBodyID_DoesNotMoveWhenWitnessesAreAdded(t *testing.T) {
	env, id, err := canonical.Enclose(actBody())
	if err != nil {
		t.Fatalf("Enclose: %v", err)
	}
	if err := env.VerifyBinding(id); err != nil {
		t.Fatalf("a freshly enclosed body does not bind: %v", err)
	}

	// One signature, then a second, then a ratification-shaped third.
	for i, sig := range []canonical.Signature{
		{KeyID: "key:builder", Sig: base64.StdEncoding.EncodeToString([]byte("first"))},
		{KeyID: "key:reviewer", Sig: base64.StdEncoding.EncodeToString([]byte("second"))},
		{KeyID: "key:operator", Sig: base64.StdEncoding.EncodeToString([]byte("ratification"))},
	} {
		env.Signatures = append(env.Signatures, sig)

		again, err := canonical.ActBodyID(actBody())
		if err != nil {
			t.Fatalf("ActBodyID: %v", err)
		}
		if again != id {
			t.Fatalf("the act body id moved after %d witness(es): %s then %s", i+1, id, again)
		}
		if err := env.VerifyBinding(id); err != nil {
			t.Fatalf("the binding broke after %d witness(es): %v", i+1, err)
		}
	}
}

// Every field of the intent is covered, so a change to any of them is a
// different act. A body id that ignored a field would let that field be swapped
// under a signature.
func TestActBodyID_ChangesWhenAnyFieldChanges(t *testing.T) {
	base, err := canonical.ActBodyID(actBody())
	if err != nil {
		t.Fatalf("ActBodyID: %v", err)
	}

	seen := map[string]string{base: "(unmodified)"}
	fields := actBody()
	if len(fields) == 0 {
		t.Fatal("the fixture has no fields, so this test examines nothing")
	}
	for field := range fields {
		b := actBody()
		b[field] = "tampered"
		got, err := canonical.ActBodyID(b)
		if err != nil {
			t.Fatalf("ActBodyID with %s changed: %v", field, err)
		}
		if prev, clash := seen[got]; clash {
			t.Errorf("changing %q produced the same id as %s — that field is not bound, so it could be "+
				"swapped under a signature", field, prev)
		}
		seen[got] = field
	}
	t.Logf("checked %d field(s), all bound", len(fields))
}

// Adding or removing a field is a different act too — not only changing one.
func TestActBodyID_ChangesWhenAFieldIsAddedOrRemoved(t *testing.T) {
	base, _ := canonical.ActBodyID(actBody())

	added := actBody()
	added["extra"] = ""
	gotAdded, err := canonical.ActBodyID(added)
	if err != nil {
		t.Fatalf("ActBodyID: %v", err)
	}
	if gotAdded == base {
		t.Error("adding an empty-valued field did not change the id; an empty value is not an absent one")
	}

	removed := actBody()
	delete(removed, "nonce")
	gotRemoved, err := canonical.ActBodyID(removed)
	if err != nil {
		t.Fatalf("ActBodyID: %v", err)
	}
	if gotRemoved == base {
		t.Error("removing the nonce did not change the id")
	}
}

// ⚠️ The envelope must be shaped so a GENERIC DSSE decoder can read it. That is
// what makes an outsider able to verify with a tool they already trust rather
// than one we ship them — most of what the outsider-replay claim is for.
func TestEnvelope_IsStandardsShaped(t *testing.T) {
	env, _, err := canonical.Enclose(actBody())
	if err != nil {
		t.Fatalf("Enclose: %v", err)
	}
	env.Signatures = append(env.Signatures, canonical.Signature{KeyID: "key:builder", Sig: "c2ln"})

	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshalling the envelope: %v", err)
	}

	// Read it back the way a foreign decoder would: by DSSE's field names, with
	// no knowledge of our Go types.
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("a generic reader could not parse the envelope: %v", err)
	}
	for _, field := range []string{"payload", "payloadType", "signatures"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("the envelope has no %q field; a generic DSSE decoder would reject it", field)
		}
	}
	if _, err := base64.StdEncoding.DecodeString(generic["payload"].(string)); err != nil {
		t.Errorf("payload is not standard base64: %v", err)
	}
	sigs, ok := generic["signatures"].([]any)
	if !ok || len(sigs) != 1 {
		t.Fatalf("signatures is not a one-element array: %#v", generic["signatures"])
	}
	sig := sigs[0].(map[string]any)
	for _, field := range []string{"keyid", "sig"} {
		if _, ok := sig[field]; !ok {
			t.Errorf("a signature has no %q field; DSSE names it that", field)
		}
	}
}

// An UNSIGNED envelope must still be a well-formed DSSE document: signatures
// marshals as [] rather than null, because an unsigned envelope is a real
// intermediate state and should not be unrepresentable.
func TestEnvelope_UnsignedIsStillWellFormed(t *testing.T) {
	env, _, err := canonical.Enclose(actBody())
	if err != nil {
		t.Fatalf("Enclose: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if strings.Contains(string(raw), `"signatures":null`) {
		t.Error("an unsigned envelope marshals signatures as null; a generic decoder expecting an array may refuse it")
	}
	if !strings.Contains(string(raw), `"signatures":[]`) {
		t.Errorf("expected an empty signatures array, got %s", raw)
	}
}

// ⚠️ PAE's length prefixes are what stop a signature being replayed across
// types. Without them a crafted type and payload can be split differently by a
// reader than by the signer.
func TestPAE_LengthPrefixesPreventBoundaryShifting(t *testing.T) {
	// Two (type, payload) pairs whose naive concatenation is identical.
	a := canonical.PAE("ab", []byte("cd"))
	b := canonical.PAE("abc", []byte("d"))

	if string(a) == string(b) {
		t.Fatal("two different (type, payload) splits produced the same pre-authentication encoding; a " +
			"signature over one would verify over the other")
	}
	if !strings.HasPrefix(string(a), "DSSEv1 ") {
		t.Errorf("PAE does not begin with the DSSE marker: %q", a)
	}
	// The exact shape, pinned: DSSEv1 SP len(type) SP type SP len(payload) SP payload
	if want := "DSSEv1 2 ab 2 cd"; string(a) != want {
		t.Errorf("PAE = %q, want %q", a, want)
	}
}

func TestPAE_CoversAnEmptyPayload(t *testing.T) {
	if want, got := "DSSEv1 1 t 0 ", string(canonical.PAE("t", nil)); got != want {
		t.Errorf("PAE with an empty payload = %q, want %q", got, want)
	}
}

// A payload that is valid JSON for the right body but NOT canonical must be
// refused: otherwise two different byte strings satisfy one id, which is the
// whole thing a canonical form exists to prevent.
func TestVerifyBinding_RefusesANonCanonicalPayload(t *testing.T) {
	_, id, err := canonical.Enclose(actBody())
	if err != nil {
		t.Fatalf("Enclose: %v", err)
	}

	// Same data, different bytes: keys in a different order, with whitespace.
	loose, err := json.MarshalIndent(actBody(), "", "  ")
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	env := canonical.Envelope{
		Payload:     base64.StdEncoding.EncodeToString(loose),
		PayloadType: canonical.PayloadType,
		Signatures:  []canonical.Signature{},
	}

	err = env.VerifyBinding(id)
	if err == nil {
		t.Fatal("VerifyBinding accepted a payload that is valid JSON but not canonical; two different " +
			"byte strings would then satisfy one act body id")
	}
	if !errors.Is(err, canonical.ErrEnvelope) {
		t.Errorf("error %v does not wrap ErrEnvelope", err)
	}
}

// A tampered payload is a BINDING MISMATCH, not a malformed envelope — a
// different diagnosis deserving a different reaction.
func TestVerifyBinding_ReportsATamperedBodyDistinctly(t *testing.T) {
	_, id, err := canonical.Enclose(actBody())
	if err != nil {
		t.Fatalf("Enclose: %v", err)
	}

	tampered := actBody()
	tampered["head_sha"] = "0000000000000000000000000000000000000000"
	env, _, err := canonical.Enclose(tampered)
	if err != nil {
		t.Fatalf("Enclose: %v", err)
	}

	err = env.VerifyBinding(id)
	if err == nil {
		t.Fatal("VerifyBinding accepted an envelope carrying a different body than the id it was presented as")
	}
	if !errors.Is(err, canonical.ErrBindingMismatch) {
		t.Errorf("error %v does not wrap ErrBindingMismatch; a changed body under a signature is a "+
			"different diagnosis from a malformed envelope", err)
	}
	if errors.Is(err, canonical.ErrEnvelope) {
		t.Error("a tampered body was reported as a malformed envelope")
	}
}

func TestEnvelope_OpenRefusesAWrongPayloadType(t *testing.T) {
	env, _, err := canonical.Enclose(actBody())
	if err != nil {
		t.Fatalf("Enclose: %v", err)
	}
	env.PayloadType = "application/vnd.arqtos.act-body+json;v=2"

	if _, err := env.Open(); !errors.Is(err, canonical.ErrEnvelope) {
		t.Fatalf("Open accepted a mismatched payload type (err=%v); the type is bound into the "+
			"pre-authentication encoding, so interpreting under another one reads bytes nobody signed", err)
	}
}

func TestEnvelope_OpenRefusesMalformedPayloads(t *testing.T) {
	for name, env := range map[string]canonical.Envelope{
		"empty payload":   {PayloadType: canonical.PayloadType, Payload: ""},
		"not base64":      {PayloadType: canonical.PayloadType, Payload: "!!!not-base64!!!"},
		"no payload type": {Payload: "e30="},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := env.Open(); !errors.Is(err, canonical.ErrEnvelope) {
				t.Fatalf("Open accepted %s: err=%v", name, err)
			}
		})
	}
}

func TestVerifyBinding_RefusesAPayloadThatIsNotJSON(t *testing.T) {
	env := canonical.Envelope{
		PayloadType: canonical.PayloadType,
		Payload:     base64.StdEncoding.EncodeToString([]byte("not json at all")),
	}
	if err := env.VerifyBinding("sha256:whatever"); !errors.Is(err, canonical.ErrEnvelope) {
		t.Fatalf("VerifyBinding accepted a non-JSON payload: %v", err)
	}
}

// ⚠️ THE SURFACE THAT MUST NOT EXIST. There is exactly one signed body per act
// kind and no selectable signature masks — a mask is the door effect-substitution
// returns through, because it lets a signer cover less than the whole intent
// while still producing a signature that looks complete.
func TestNoSignatureMaskSurfaceExists(t *testing.T) {
	env := reflect.TypeOf(canonical.Envelope{})
	for i := range env.NumField() {
		name := strings.ToLower(env.Field(i).Name)
		for _, banned := range []string{"mask", "sighash", "covered", "fields", "scope"} {
			if strings.Contains(name, banned) {
				t.Errorf("Envelope has a field %q, which reads as a signature mask; there is one signed "+
					"body per act kind and no selectable coverage", env.Field(i).Name)
			}
		}
	}

	sig := reflect.TypeOf(canonical.Signature{})
	if sig.NumField() != 2 {
		t.Errorf("Signature has %d fields; DSSE names exactly keyid and sig, and anything else is a place "+
			"for coverage metadata to grow", sig.NumField())
	}

	// Enclose takes the body and nothing else: no options struct, no field
	// selector, no variadic through which coverage could be narrowed.
	fn := reflect.TypeOf(canonical.Enclose)
	if fn.NumIn() != 1 {
		t.Errorf("Enclose takes %d arguments; a second one is where a mask would arrive", fn.NumIn())
	}
	if fn.IsVariadic() {
		t.Error("Enclose is variadic; options are how a coverage selector gets added without anyone noticing")
	}
}

// ⚠️ The binding check must NOT be mistakable for signature verification. This is
// a naming test, and it is deliberate: the danger is a caller reading a nil
// return as "verified" when no key was consulted at all.
func TestThePackageOffersNoSignatureVerification(t *testing.T) {
	env := reflect.TypeOf(canonical.Envelope{})
	for i := range env.NumMethod() {
		if name := env.Method(i).Name; name == "Verify" {
			t.Error("Envelope has a method called Verify. Nothing here checks a signature — that needs " +
				"the key registry — and a method with that name invites a caller to conclude more than " +
				"was established. The binding check is called VerifyBinding for exactly this reason.")
		}
	}
}
