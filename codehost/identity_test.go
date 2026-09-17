package codehost

import (
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

func TestRepo_NativeIdentity_WhenMissing_IsTyped(t *testing.T) {
	_, err := Repo{FullName: "owner/one"}.NativeIdentity("")
	if err == nil {
		t.Fatal("missing native id was accepted")
	}
	if cerr.KindOf(err) != cerr.KindContractViolation {
		t.Fatalf("kind=%v; want contract_violation so a caller cannot treat emptiness as identity", cerr.KindOf(err))
	}
}

func TestMutation_PathFallbackAfterMissingIdentityFails(t *testing.T) {
	id, err := Repo{FullName: "owner/one"}.NativeIdentity("")
	if err == nil && (id.ID == "owner/one" || strings.Contains(id.ID, "/")) {
		t.Fatalf("missing identity fell back to path %q", id.ID)
	}
	if err == nil {
		t.Fatal("missing identity succeeded")
	}
}

func TestRepo_NativeIdentity_WhenUnsupported_IsTyped(t *testing.T) {
	_, err := Repo{FullName: "owner/one", Realm: "host-a"}.NativeIdentity("host-a")
	if err == nil {
		t.Fatal("unsupported native id was accepted")
	}
	if cerr.KindOf(err) != cerr.KindUnsupported {
		t.Fatalf("kind=%v; want unsupported", cerr.KindOf(err))
	}
}

func TestRepo_NativeIdentity_WhenRealmMismatched_IsTyped(t *testing.T) {
	r := Repo{FullName: "owner/one", NativeID: "42", Realm: "host-a"}
	_, err := r.NativeIdentity("host-b")
	if err == nil {
		t.Fatal("mismatched realm was accepted")
	}
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("kind=%v; want invalid", cerr.KindOf(err))
	}
}

func TestRepo_NativeIdentity_WhenPathShaped_IsTyped(t *testing.T) {
	r := Repo{FullName: "owner/one", NativeID: "owner/one", Realm: "host-a"}
	_, err := r.NativeIdentity("host-a")
	if err == nil {
		t.Fatal("path-shaped native id was accepted")
	}
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("kind=%v; want invalid", cerr.KindOf(err))
	}
}

func TestRepo_NativeIdentity_WhenPresent_ReturnsOpaqueIDAndRealm(t *testing.T) {
	r := Repo{FullName: "owner/one", NativeID: "42", Realm: "host-a"}
	got, err := r.NativeIdentity("host-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "42" || got.Realm != "host-a" {
		t.Fatalf("got %+v", got)
	}
}

func TestRepo_NativeIdentity_LargeNumericID_StaysString(t *testing.T) {
	const large = "18446744073709551615"
	r := Repo{FullName: "owner/one", NativeID: large, Realm: "host-a"}
	got, err := r.NativeIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != large {
		t.Fatalf("id=%q; want the unparsed decimal string", got.ID)
	}
}

func TestRepo_NativeIdentity_SameIDDifferentRealms_AreDistinct(t *testing.T) {
	a, err := Repo{NativeID: "42", Realm: "host-a", FullName: "acme/one"}.NativeIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Repo{NativeID: "42", Realm: "host-b", FullName: "acme/one"}.NativeIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("identical native ids on two realms collapsed: %+v", a)
	}
	if a.ID != b.ID {
		t.Fatalf("id moved: %q vs %q", a.ID, b.ID)
	}
}

func TestRepo_NativeIdentity_RenameDoesNotChangeID(t *testing.T) {
	r := Repo{FullName: "owner/old", NativeID: "9007199254740993", Realm: "host-a"}
	before, err := r.NativeIdentity("")
	if err != nil || before.ID == "" {
		t.Fatalf("before rename: %+v err=%v", before, err)
	}
	r.FullName = "owner/new"
	after, err := r.NativeIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != before.ID || after.ID != "9007199254740993" || after.Realm != "host-a" {
		t.Fatalf("rename moved identity: before=%+v after=%+v", before, after)
	}
}
