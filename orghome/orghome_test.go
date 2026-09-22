package orghome_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/orghome"
)

func TestValidFixtures_ParseAndValidate(t *testing.T) {
	ents, err := os.ReadDir(filepath.Join("testdata", "valid"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) == 0 {
		t.Fatal("no valid fixtures")
	}
	for _, e := range ents {
		t.Run(e.Name(), func(t *testing.T) {
			d, err := orghome.Parse(read(t, filepath.Join("testdata", "valid", e.Name())))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if err := d.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			b, err := orghome.CanonicalBytes(d)
			if err != nil {
				t.Fatalf("CanonicalBytes: %v", err)
			}
			if len(b) == 0 || b[len(b)-1] != '\n' {
				t.Fatalf("CanonicalBytes missing trailing newline")
			}
		})
	}
}

func TestInvalidFixtures_Refuse(t *testing.T) {
	want := map[string]string{
		"unknown-version.yaml":     "schema_version",
		"unknown-field.yaml":       "field",
		"name-as-identity.yaml":    "repository name",
		"missing-org-id.yaml":      "org_id",
		"ambiguous-root.yaml":      "ambiguous",
		"lock-drift.yaml":          "independently writable",
		"inline-secret.yaml":       "reference-only",
		"latest-revision.yaml":     "latest",
		"missing-upstream.yaml":    "upstream",
	}
	ents, err := os.ReadDir(filepath.Join("testdata", "invalid"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != len(want) {
		t.Fatalf("invalid fixtures=%d want=%d; every refusal needs a fixture", len(ents), len(want))
	}
	for _, e := range ents {
		sub, ok := want[e.Name()]
		if !ok {
			t.Fatalf("unmapped invalid fixture %s", e.Name())
		}
		t.Run(e.Name(), func(t *testing.T) {
			d, err := orghome.Parse(read(t, filepath.Join("testdata", "invalid", e.Name())))
			if err == nil {
				err = d.Validate()
			}
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(strings.ToLower(err.Error()), sub) {
				t.Fatalf("err=%q; want %q", err, sub)
			}
		})
	}
}

func TestMigrate_RequiresIssuedIdentitiesAndDoesNotInferRepositoryName(t *testing.T) {
	old := read(t, filepath.Join("testdata", "migrate", "v0-org-common.yaml"))
	_, err := orghome.Migrate(old, orghome.Binding{OrgID: "", SourceID: "aqa-arqtos"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "repository name") {
		t.Fatalf("name inference error = %v, want repository name", err)
	}
	got, err := orghome.Migrate(old, orghome.Binding{OrgID: "org_0192f4b5", SourceID: "src_0192f4c0"})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("migrated Validate: %v", err)
	}
	if got.OrgID != "org_0192f4b5" || got.Home.SourceID != "src_0192f4c0" {
		t.Fatalf("migrated identities = %q / %q", got.OrgID, got.Home.SourceID)
	}
	if got.Home.Locator.Name == got.OrgID || got.Home.Locator.Name == got.Home.SourceID {
		t.Fatalf("locator name reused as identity")
	}
	want := read(t, filepath.Join("testdata", "migrate", "v1-org-adoption.yaml"))
	after, err := orghome.Parse(want)
	if err != nil {
		t.Fatalf("after Parse: %v", err)
	}
	if after.Declaration.Revision != got.Declaration.Revision {
		t.Fatalf("declaration revision %q, want %q", got.Declaration.Revision, after.Declaration.Revision)
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
