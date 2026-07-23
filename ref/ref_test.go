package ref_test

import (
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

func TestParse(t *testing.T) {
	r, err := ref.Parse("op://Vault/Item/field")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Vault != "Vault" || r.Item != "Item" || r.Field != "field" {
		t.Fatalf("got %+v", r)
	}
	if r.String() != "op://Vault/Item/field" {
		t.Fatalf("round-trip mismatch: %q", r.String())
	}
}

func TestParseInvalid(t *testing.T) {
	for _, s := range []string{"", "Vault/Item/field", "op://Vault/Item", "op://", "op:///Item/field"} {
		if _, err := ref.Parse(s); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}
