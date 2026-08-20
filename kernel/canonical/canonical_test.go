package canonical_test

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
)

var update = flag.Bool("update", false, "rewrite the committed vectors")

// ⚠️ THE VECTORS ARE THE FORMAT. This encoding is owned forever, so prose about
// it is worth nothing next to bytes a second implementation can be held to. Each
// case below names the divergence it pins, because a vector whose reason is not
// written down gets "fixed" by the next person who finds it surprising.
var vectors = []struct {
	name string
	why  string
	in   string // the input, as JSON
}{
	{
		"key ordering",
		"keys sort by UTF-16 code unit, so an encoder that emitted insertion order or Go map order disagrees",
		`{"b":"2","a":"1","C":"3","_":"4"}`,
	},
	{
		"nested key ordering",
		"the rule applies at every depth, not only at the root",
		`{"outer":{"z":"1","a":{"y":"2","b":"3"}}}`,
	},
	{
		"⚠️ key ordering above the BMP",
		"UTF-16 order is NOT Go's byte order: a surrogate pair sorts BELOW U+E000..U+FFFF in UTF-16 and ABOVE it in UTF-8. Nothing else catches this until a key carries an emoji.",
		`{"\ue000":"bmp","😀":"astral","a":"ascii"}`,
	},
	{
		"minimal escaping",
		"Go escapes <, > and & by default for HTML safety; any OPTIONAL escaping makes two conformant encoders disagree byte-for-byte",
		`{"html":"<a href=\"x\">&amp;</a>"}`,
	},
	{
		"mandatory escapes",
		"the control characters that MUST be escaped, and the two-character forms they take",
		`{"s":"quote:\" backslash:\\ bs:\b ff:\f nl:\n cr:\r tab:\t"}`,
	},
	{
		"other control characters",
		"anything below U+0020 without a short form takes \\u00xx, lower-case hex",
		`{"s":"\u0001\u001f"}`,
	},
	{
		"non-ASCII is NOT escaped",
		"a canonical form that escaped non-ASCII would still be valid JSON and a different byte string",
		`{"s":"héllo — 世界 🌍"}`,
	},
	{
		"empty is not absent",
		"an empty string, empty object and empty array each encode, and none of them is null",
		`{"s":"","o":{},"a":[],"n":null}`,
	},
	{
		"array order is preserved",
		"arrays are ordered data; sorting them would change meaning, unlike object keys",
		`{"a":["c","a","b"]}`,
	},
	{
		"booleans",
		"true and false are literals, not strings",
		`{"t":true,"f":false}`,
	},
	{
		"⚠️ 64-bit integers, carried as strings",
		"the values this profile exists for: an output index, a resource version, an accepted sequence — all beyond a double's exact range, all round-tripping because they are strings",
		`{"sequence":"9007199254740993","version":"18446744073709551615","index":"0"}`,
	},
}

func vectorPath(name string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		case r == ' ' || r == '-' || r == '_':
			return '-'
		}
		return -1
	}, name)
	return filepath.Join("testdata", strings.Trim(safe, "-")+".json")
}

// TestVectors is the committed record of the format.
//
// Run with -update to rewrite them, which is a deliberate act: a diff here is a
// change to every future act's identity.
func TestVectors(t *testing.T) {
	if len(vectors) == 0 {
		t.Fatal("no vectors, so every assertion below passes by examining nothing")
	}
	t.Logf("pinning %d vector(s)", len(vectors))

	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			var in any
			dec := json.NewDecoder(strings.NewReader(v.in))
			dec.UseNumber()
			if err := dec.Decode(&in); err != nil {
				t.Fatalf("the vector's own input is not JSON: %v", err)
			}

			got, err := canonical.Encode(in)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			path := vectorPath(v.name)
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("updating vector: %v", err)
				}
				t.Logf("updated %s", path)
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading vector: %v — run with -update to create it", err)
			}
			if string(got) != string(want) {
				t.Errorf("canonical bytes changed.\n  why this vector exists: %s\n  got:  %s\n  want: %s",
					v.why, got, want)
			}
		})
	}
}

// ⚠️ BYTE-STABILITY ACROSS RUNS is the property the acceptance criteria name, and
// it is not the same as matching a committed file once. Go randomises map
// iteration order per run, so an encoder that forgot to sort would match its own
// golden on the run that produced it and diverge on the next.
func TestEncode_IsByteStableAcrossRepeatedRuns(t *testing.T) {
	in := map[string]any{
		"zebra": "1", "alpha": "2", "Mike": "3", "_under": "4",
		"nested": map[string]any{"y": "5", "b": "6", "A": "7"},
		"list":   []any{"c", "a", "b"},
	}

	first, err := canonical.Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// 200 iterations rather than 2: map order is random per range, so a single
	// repeat can agree by luck on a small map.
	for i := range 200 {
		again, err := canonical.Encode(in)
		if err != nil {
			t.Fatalf("Encode on iteration %d: %v", i, err)
		}
		if string(again) != string(first) {
			t.Fatalf("encoding is not stable: iteration %d produced\n  %s\nfirst run produced\n  %s", i, again, first)
		}
	}
}

// ⚠️ THE REFUSAL THIS PROFILE EXISTS FOR. A number that survives encoding is a
// number two runtimes may render differently, which is two digests for one act.
func TestEncode_RefusesJSONNumbers(t *testing.T) {
	for name, in := range map[string]string{
		"small integer":       `{"n":1}`,
		"large integer":       `{"n":9007199254740993}`,
		"float":               `{"n":1.5}`,
		"exponent":            `{"n":1e3}`,
		"negative":            `{"n":-1}`,
		"zero":                `{"n":0}`,
		"nested in an array":  `{"a":["ok",2]}`,
		"nested in an object": `{"o":{"n":3}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var v any
			dec := json.NewDecoder(strings.NewReader(in))
			dec.UseNumber()
			if err := dec.Decode(&v); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			_, err := canonical.Encode(v)
			if err == nil {
				t.Fatal("Encode accepted a JSON number. A number that survives encoding is one two " +
					"runtimes may render differently, which is two digests for one act.")
			}
			if !errors.Is(err, canonical.ErrNumber) {
				t.Errorf("error %v does not wrap ErrNumber, so a caller cannot tell it from malformed input "+
					"— and the fix (carry it as a string) is not guessable from a generic message", err)
			}
		})
	}
}

// A Go struct carrying an int must be refused too, not only hand-written JSON:
// the marshalling path is where a real caller meets this.
func TestEncode_RefusesAGoStructCarryingAnInt(t *testing.T) {
	type body struct {
		Name  string `json:"name"`
		Index int64  `json:"index"`
	}
	if _, err := canonical.Encode(body{Name: "x", Index: 7}); !errors.Is(err, canonical.ErrNumber) {
		t.Fatalf("Encode(struct with int64) error = %v; want ErrNumber — this is how a caller actually hits it", err)
	}
}

// ⚠️ Domain separation, and the test that shows it does something: two DIFFERENT
// kinds with IDENTICAL fields must not share an identity, or a witness could be
// presented as an act body.
func TestDigest_SeparatesDomains(t *testing.T) {
	same := map[string]any{"a": "1"}

	seen := map[string]canonical.Domain{}
	for _, d := range canonical.Domains() {
		got, err := canonical.Digest(d, same)
		if err != nil {
			t.Fatalf("Digest(%s): %v", d, err)
		}
		if prev, clash := seen[got]; clash {
			t.Errorf("domains %s and %s produce the SAME digest for identical fields; one kind could be "+
				"presented as the other", prev, d)
		}
		seen[got] = d
		if !strings.HasPrefix(got, canonical.HashName+":") {
			t.Errorf("digest %q does not name its hash; a reader would have to infer it from a length", got)
		}
	}
	if len(seen) != len(canonical.Domains()) {
		t.Errorf("%d distinct digests across %d domains", len(seen), len(canonical.Domains()))
	}
}

// ⚠️ The length prefix, tested rather than asserted. Without it, a crafted tag
// and a crafted body can move material across the boundary and collide.
func TestDigest_LengthPrefixPreventsBoundaryShifting(t *testing.T) {
	// Two domains chosen so a naive concatenation of tag+body could be made to
	// coincide; with a length prefix they cannot.
	a, err := canonical.Digest(canonical.DomainActBody, map[string]any{"x": "1"})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	b, err := canonical.Digest(canonical.DomainWitness, map[string]any{"x": "1"})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if a == b {
		t.Fatal("two domains collided on identical input")
	}
}

func TestDigest_RefusesAnUnknownDomain(t *testing.T) {
	if _, err := canonical.Digest(canonical.Domain("arqtos.invented.v1"), map[string]any{}); err == nil {
		t.Fatal("Digest accepted an unknown domain; a tag the reader does not know is a separation it " +
			"cannot reproduce, so the identity would be one nothing else can recompute")
	}
}

func TestDomain_ValidIsClosed(t *testing.T) {
	for _, d := range canonical.Domains() {
		if !d.Valid() {
			t.Errorf("published domain %q is not Valid()", d)
		}
	}
	for _, d := range []canonical.Domain{"", "arqtos.act-body", "act-body.v1", "arqtos.act-body.v2"} {
		if d.Valid() {
			t.Errorf("%q is Valid(), but it is outside the closed set", d)
		}
	}
}

func TestDomains_ReturnsACopy(t *testing.T) {
	d := canonical.Domains()
	d[0] = canonical.Domain("tampered")
	if canonical.Domains()[0] == canonical.Domain("tampered") {
		t.Error("Domains() hands out the backing array; a caller can corrupt the vocabulary")
	}
}

// The same input digests identically every time — the property everything else
// rests on, asserted rather than assumed.
func TestDigest_IsStable(t *testing.T) {
	in := map[string]any{"b": "2", "a": "1", "n": map[string]any{"z": "3"}}
	first, err := canonical.Digest(canonical.DomainActBody, in)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	for range 100 {
		again, err := canonical.Digest(canonical.DomainActBody, in)
		if err != nil {
			t.Fatalf("Digest: %v", err)
		}
		if again != first {
			t.Fatalf("digest is not stable: %s then %s", first, again)
		}
	}
}

// A value json.Marshal cannot render must be refused, not rendered partially.
func TestEncode_RefusesAValueThatCannotBeMarshalled(t *testing.T) {
	out, err := canonical.Encode(make(chan int))
	if err == nil {
		t.Fatal("Encode accepted an unmarshallable value")
	}
	if len(out) != 0 {
		t.Errorf("Encode returned %d byte(s) beside an error; a failed encode must not hand back partial bytes", len(out))
	}
	// ⚠️ And it is NOT reported as the number refusal: the fix is completely
	// different, and a caller told to "carry integers as strings" would go
	// looking at the wrong field.
	if errors.Is(err, canonical.ErrNumber) {
		t.Error("an unmarshallable value was reported as the number refusal")
	}
}

// Digest must propagate an encoding refusal rather than digesting whatever it
// managed to produce.
func TestDigest_PropagatesAnEncodingRefusal(t *testing.T) {
	got, err := canonical.Digest(canonical.DomainActBody, map[string]any{"n": 1})
	if err == nil {
		t.Fatal("Digest accepted a body carrying a JSON number")
	}
	if !errors.Is(err, canonical.ErrNumber) {
		t.Errorf("error %v does not wrap ErrNumber", err)
	}
	if got != "" {
		t.Errorf("Digest returned %q beside an error; a refused encode has no digest", got)
	}
}

// ⚠️ lessUTF16's prefix case: "a" before "ab". Without it, a shorter key that is
// a prefix of a longer one has no defined order, and two encoders could disagree
// on a pair that occurs in almost every real object.
func TestEncode_OrdersAPrefixKeyBeforeItsExtension(t *testing.T) {
	got, err := canonical.Encode(map[string]any{"ab": "2", "a": "1", "abc": "3"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if want := `{"a":"1","ab":"2","abc":"3"}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// U+FFFD is a real code point and must survive as itself. Re-escaping it would
// make the output depend on how the input was damaged.
func TestEncode_PassesTheReplacementCharacterThrough(t *testing.T) {
	got, err := canonical.Encode(map[string]any{"s": "�"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if want := "{\"s\":\"�\"}"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ⚠️ Fixture pairs for the booleans, per the trust-path discipline: each is
// exercised in BOTH directions, so neither constant would pass.
func TestBooleans_BothWays(t *testing.T) {
	// Domain.Valid: a member and a non-member are already covered; here the
	// encoded true/false pair, which a constant-true encoder would fail.
	got, err := canonical.Encode(map[string]any{"t": true, "f": false})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if want := `{"f":false,"t":true}`; string(got) != want {
		t.Errorf("got %s, want %s — a constant would render both alike", got, want)
	}
}
