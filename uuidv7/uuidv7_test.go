package uuidv7_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/uuidv7"
)

func TestNew_VersionAndVariantBits(t *testing.T) {
	id := uuidv7.New()
	if id.Version() != 7 {
		t.Fatalf("version = %d, want 7", id.Version())
	}
	if id.Variant() != uuidv7.VariantRFC4122 {
		t.Fatalf("variant = %d, want RFC 4122 (%d)", id.Variant(), uuidv7.VariantRFC4122)
	}
	s := id.String()
	if len(s) != 36 || s[14] != '7' {
		t.Fatalf("string %q is not a UUIDv7 spelling", s)
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(raw) != 16 {
		t.Fatalf("string %q is not 16 hex bytes: %v", s, err)
	}
	if raw[6]>>4 != 7 {
		t.Fatalf("version nibble in %q is %d", s, raw[6]>>4)
	}
	if raw[8]>>6 != 2 {
		t.Fatalf("variant bits in %q are %02b, want 10", s, raw[8]>>6)
	}
}

func TestNew_MonotonicWithinOneProcess(t *testing.T) {
	const n = 2000
	ids := make([]uuidv7.ID, n)
	for i := range ids {
		ids[i] = uuidv7.New()
	}
	for i := 1; i < n; i++ {
		if bytes.Compare(ids[i][:], ids[i-1][:]) <= 0 {
			t.Fatalf("id[%d]=%s is not after id[%d]=%s", i, ids[i], i-1, ids[i-1])
		}
		if ids[i].Version() != 7 || ids[i].Variant() != uuidv7.VariantRFC4122 {
			t.Fatalf("id[%d] lost version/variant bits", i)
		}
	}
}

func TestParse_RoundTrip(t *testing.T) {
	id := uuidv7.New()
	got, err := uuidv7.Parse(id.String())
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("Parse(%q) = %v, want %v", id, got, id)
	}
}

func TestParse_RefusesANonV7(t *testing.T) {
	_, err := uuidv7.Parse("00000000-0000-4000-8000-000000000000")
	if err == nil {
		t.Fatal("accepted a version-4 UUID")
	}
}
