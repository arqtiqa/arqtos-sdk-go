package recorduid_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/recorduid"
)

func FuzzDeclaration_WhenRendered_RoundTrips(f *testing.F) {
	for _, id := range []string{"00042", "repo-A/%2d:é", "a_b.~", "e\u0301"} {
		f.Add(id, uint64(42))
	}
	f.Fuzz(func(t *testing.T, id string, ordinal uint64) {
		d := full()
		d.Namespace.RepositoryID = id
		if d.Validate() != nil || ordinal == 0 {
			return
		}
		label, err := d.Render(ordinal)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(label, "-")
		if len(parts) != 5 || label != strings.ToLower(label) {
			t.Fatalf("noncanonical full label %q", label)
		}
		decoded, err := url.PathUnescape(parts[3])
		if err != nil || decoded != id {
			t.Fatalf("native ID changed: %q → %q (%v)", id, decoded, err)
		}
		if got, err := d.Parse(label); err != nil || got != ordinal {
			t.Fatalf("ordinal changed: %d → %d (%v)", ordinal, got, err)
		}
	})
}

func FuzzDeclaration_WhenParsing_DoesNotAcceptAnotherSpelling(f *testing.F) {
	for _, label := range []string{"doc-ex-host-0042-00042", "doc-ex-host-0042-000042", "doc-ex-host-0042-18446744073709551616", "doc-ex-host-%30042-00042", ""} {
		f.Add(label)
	}
	f.Fuzz(func(t *testing.T, label string) {
		d := full()
		n, err := d.Parse(label)
		if err != nil {
			return
		}
		got, err := d.Render(n)
		if err != nil || got != label {
			t.Fatalf("noncanonical label accepted: %q → %q (%v)", label, got, err)
		}
		if err := recorduid.ValidateSet([]recorduid.Declaration{d}); err != nil {
			t.Fatal(err)
		}
	})
}
