// Package canonical is the encoding boundary: the one way an act's bytes are
// produced, so that two implementations digest the same act to the same identity.
//
// # The format is owned forever
//
// An act's identity is a digest of its canonical bytes, and acts are replayed
// indefinitely. This encoding is therefore not an implementation detail that can
// be improved later — a change to it changes every historical act's identity.
// Decoders are retained indefinitely and reducers are versioned, which is what
// lets an old act be verified under the semantics it was accepted under.
//
// # A RESTRICTED PROFILE of RFC 8785, not full JCS
//
// The encoding is the JSON Canonicalization Scheme with one subtraction:
// ⚠️ JSON NUMBERS ARE FORBIDDEN. Every integer is carried as a string.
//
// That subtraction is the point rather than a simplification. JCS inherits
// JavaScript's number serialization, and an act body carries 64-bit quantities —
// an output index, a resource version, an accepted sequence. A value outside the
// range a double holds exactly can serialize differently on two runtimes, which
// produces TWO DIGESTS FOR ONE ACT with nothing to say which is right, and it
// would be discovered on the disputed act years later. Forbidding numbers removes
// that by construction instead of staying inside a safe range and hoping.
//
// It also removes JCS's hardest and most divergence-prone rules, which is what
// makes a self-contained implementation defensible in an SDK that is
// deliberately dependency-light: what remains is sorted keys, minimal string
// escaping, and no insignificant whitespace — all pinned by committed vectors
// rather than by prose.
//
// # Why JSON at all
//
// DSSE and the in-toto Statement shape are the envelope, and both are
// JSON-shaped. Staying inside that ecosystem means an outsider can decode and
// verify an evidence bundle with a generic tool they already trust, rather than
// one this project ships them — which is what an outsider-replay claim actually
// requires. A more compact binary encoding would be stricter and would cost
// exactly that.
package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// HashName is the digest this package computes, named so a reader never has to
// infer it from a length.
const HashName = "sha256"

// ErrNumber is returned for any JSON number encountered while encoding.
//
// ⚠️ It is a distinct error rather than a generic malformed-input one because
// the fix is specific and non-obvious: carry the value as a string. A caller who
// reads "invalid input" will look at the wrong thing.
var ErrNumber = errors.New("canonical: JSON numbers are forbidden — carry integers as strings")

// ErrUnsupported is returned for a value this profile cannot encode.
var ErrUnsupported = errors.New("canonical: value cannot be canonically encoded")

// A Domain separates one kind of object from another before hashing.
//
// ⚠️ Without domain separation, two different kinds whose fields happen to
// coincide hash to the same identity — so a witness could be presented as an act
// body, or a permit as a receipt. The tag is mixed into the digest, never into
// the encoded bytes, so the bytes stay exactly what a generic JSON reader sees.
type Domain string

const (
	// DomainActBody is the unsigned immutable act body, whose digest is the
	// act_body_id. Witnesses are NOT inside it: if they were, every added
	// signature would change the act's identity.
	DomainActBody Domain = "arqtos.act-body.v1"
	// DomainWitness is an append-only signature or ratification.
	DomainWitness Domain = "arqtos.witness.v1"
	// DomainEvidenceEvent is an append-only evidence event.
	DomainEvidenceEvent Domain = "arqtos.evidence-event.v1"
	// DomainCharter is a charter document being bound by digest.
	DomainCharter Domain = "arqtos.charter.v1"
	// DomainGenesis is the repository-genesis act — the ledger bootstrap.
	DomainGenesis Domain = "arqtos.genesis.v1"
)

var domains = []Domain{
	DomainActBody, DomainWitness, DomainEvidenceEvent, DomainCharter, DomainGenesis,
}

// Domains returns the closed set of domain tags, as a copy.
func Domains() []Domain {
	out := make([]Domain, len(domains))
	copy(out, domains)
	return out
}

// Valid reports whether d is in the closed set.
//
// ⚠️ An unknown domain is refused rather than passed through. A tag the reader
// does not know is a separation it cannot reproduce, and hashing under it would
// produce an identity nothing else can recompute.
func (d Domain) Valid() bool {
	for _, k := range domains {
		if k == d {
			return true
		}
	}
	return false
}

// Encode renders v as canonical bytes.
//
// ⚠️ It refuses JSON numbers, refuses NaN and infinities by refusing numbers at
// all, and refuses anything it cannot encode deterministically. A canonical
// encoder that fell back to a best-effort rendering would produce bytes nobody
// else reproduces, which is worse than refusing.
func Encode(v any) ([]byte, error) {
	// Marshal first so that struct tags, omitempty and custom marshallers are
	// honoured exactly as every other consumer of these types sees them, then
	// canonicalise the RESULT. Canonicalising the Go value directly would make
	// this encoder a second, subtly different view of the same types.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonical: %w", err)
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	// ⚠️ UseNumber, so a large integer is NOT silently turned into a float64 on
	// the way through. Without it this encoder would destroy the very precision
	// it exists to protect, before it ever got to refuse it.
	dec.UseNumber()

	var tree any
	// ⚠️ Unreachable through this function: the bytes were produced by
	// json.Marshal on the line above, so they always decode. It is kept because
	// removing it would mean trusting that invariant silently, and a future
	// change that marshals differently would then fail somewhere less obvious.
	// Do not contrive a test for it — it has no input that reaches it.
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("canonical: %w", err)
	}

	var b strings.Builder
	if err := writeValue(&b, tree); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// Digest returns the domain-separated digest of v's canonical bytes, as
// "<hash>:<hex>".
//
// ⚠️ The domain is mixed in as a length-prefixed prefix rather than concatenated
// raw. Plain concatenation lets a crafted tag and a crafted body swap material
// across the boundary and collide; the length prefix makes the split
// unambiguous. This is the same reasoning DSSE's own pre-authentication
// encoding uses, applied to the digest rather than to the signature.
func Digest(d Domain, v any) (string, error) {
	if !d.Valid() {
		return "", fmt.Errorf("canonical: %w: unknown domain %q", ErrUnsupported, d)
	}
	body, err := Encode(v)
	if err != nil {
		return "", err
	}

	h := sha256.New()
	fmt.Fprintf(h, "%d %s", len(d), d)
	fmt.Fprintf(h, " %d ", len(body))
	h.Write(body)

	return HashName + ":" + hex.EncodeToString(h.Sum(nil)), nil
}

func writeValue(b *strings.Builder, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
		return nil
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil
	case string:
		writeString(b, t)
		return nil
	case json.Number:
		// ⚠️ THE REFUSAL THIS PROFILE EXISTS FOR.
		return fmt.Errorf("%w (found %s)", ErrNumber, t.String())
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeValue(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	case map[string]any:
		return writeObject(b, t)
	}
	// ⚠️ Also unreachable through Encode: json.Marshal emits only the types
	// handled above. Same reasoning as the decode branch — it is the floor under
	// an invariant, not dead code, and it is why a caller reaching writeValue
	// with something new gets a refusal rather than silence.
	return fmt.Errorf("%w: %T", ErrUnsupported, v)
}

func writeObject(b *strings.Builder, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// ⚠️ Sorted by UTF-16 CODE UNITS, which is RFC 8785's rule and is NOT the
	// same as Go's byte-wise string order. They diverge above the basic
	// multilingual plane: a surrogate pair sorts below U+E000..U+FFFF in UTF-16
	// and above it in UTF-8. A vector pins the case, because nothing else would
	// catch it until an act carried an emoji in a key.
	sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })

	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeString(b, k)
		b.WriteByte(':')
		if err := writeValue(b, m[k]); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

// lessUTF16 compares two strings by their UTF-16 code units.
func lessUTF16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// writeString emits a JSON string with RFC 8785's MINIMAL escaping.
//
// ⚠️ Minimal is a requirement, not a preference. Go's encoding/json escapes
// <, > and & by default for HTML safety, and any optional escaping makes two
// conformant encoders disagree byte-for-byte — which is the whole failure this
// package exists to prevent.
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			switch {
			case r < 0x20:
				fmt.Fprintf(b, `\u%04x`, r)
			case r == utf8.RuneError:
				// An invalid byte sequence became U+FFFD on the way in. Emit it
				// literally: it is a real code point, and re-escaping it would
				// make the output depend on how the input was damaged.
				b.WriteRune(r)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
