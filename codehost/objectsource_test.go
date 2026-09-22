package codehost

import (
	"reflect"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

func TestCASRequest_WithoutObjectSource_RefusesPublication(t *testing.T) {
	req := CASRequest{Realm: "forge", NativeID: "42", Ref: "refs/heads/main", New: ObjectID(oidA)}
	err := req.Validate()
	if cerr.KindOf(err) != cerr.KindInvalid || !strings.Contains(err.Error(), "source") {
		t.Fatalf("missing source must be invalid before publication: %v", err)
	}
}

func TestCASRequest_ExplicitSource_ValidatesWithoutReadingFilesystem(t *testing.T) {
	for _, path := range []string{"", ".", "relative/repo", "/", "/tmp/../repo", "/tmp/repo\x00", "/tmp/repo\n", "https://example.com/repo", "/tmp/explicit-repository"} {
		req := CASRequest{Realm: "forge", NativeID: "42", Ref: "refs/heads/main", New: ObjectID(oidA)}
		field := reflect.ValueOf(&req).Elem().FieldByName("SourceDir")
		if !field.IsValid() || field.Kind() != reflect.String {
			t.Fatal("CASRequest has no explicit SourceDir string")
		}
		field.SetString(path)
		err := req.Validate()
		if path == "/tmp/explicit-repository" {
			if err != nil {
				t.Fatalf("explicit local path refused: %v", err)
			}
		} else if cerr.KindOf(err) != cerr.KindInvalid || !strings.Contains(err.Error(), "source") {
			t.Fatalf("unsafe source %q: %v", path, err)
		}
	}
}

func TestExactReadRequest_UnsafeRefOrIdentity_Refuses(t *testing.T) {
	for _, ref := range []string{"HEAD", "main", "refs/heads/a:refs/heads/b", "refs/heads/a..b", "refs/heads/a@{b", "refs/heads/a b", "refs/heads/.hidden", "refs/heads/a.lock", "refs//heads/a", "refs/heads/a\\b", "refs/heads/a?b", "refs/heads/a*", "refs/heads/a[", "refs/heads/a~1", "refs/heads/a^", "refs/heads/a.", "refs/heads/a/", "refs/heads/a\x00", "refs/heads/a\x7f"} {
		err := (ExactReadRequest{Realm: "forge", NativeID: "42", Ref: ref}).Validate()
		if cerr.KindOf(err) != cerr.KindInvalid {
			t.Fatalf("unsafe ref %q: %v", ref, err)
		}
	}
	for _, id := range []string{"-option", "42\n", "42\x00", "group/project", " "} {
		err := (ExactReadRequest{Realm: "forge", NativeID: id, Ref: "refs/heads/main"}).Validate()
		if cerr.KindOf(err) != cerr.KindInvalid {
			t.Fatalf("unsafe native identity %q: %v", id, err)
		}
	}
	for _, realm := range []string{"forge\n", "https://user:password@example.com", "https://example.com/path", "https://example.com?credential=hidden"} {
		err := (ExactReadRequest{Realm: realm, NativeID: "42", Ref: "refs/heads/main"}).Validate()
		if cerr.KindOf(err) != cerr.KindInvalid {
			t.Fatalf("unsafe realm: %v", err)
		}
	}
}
