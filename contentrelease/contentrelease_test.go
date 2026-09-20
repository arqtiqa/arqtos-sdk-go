package contentrelease_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/contentrelease"
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
			m, err := contentrelease.Parse(read(t, filepath.Join("testdata", "valid", e.Name())))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if err := m.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if _, err := contentrelease.CanonicalBytes(m); err != nil {
				t.Fatalf("CanonicalBytes: %v", err)
			}
		})
	}
}

func TestInvalidFixtures_Refuse(t *testing.T) {
	want := map[string]string{
		"unknown-version.yaml":       "schema_version",
		"connector-kind.yaml":        "kind",
		"latest-alias.yaml":          "latest",
		"private-path.yaml":          "path",
		"required-unknown-type.yaml": "type",
		"cycle.yaml":                 "cycle",
		"duplicate-id.yaml":          "duplicate",
		"wrong-publisher.yaml":       "publisher",
		"self-trust.yaml":            "trust",
		"secret-ref.yaml":            "op://",
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
			m, err := contentrelease.Parse(read(t, filepath.Join("testdata", "invalid", e.Name())))
			if err == nil {
				err = m.Validate()
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

func TestOptionalUnsupported_IsReportedNotRefused(t *testing.T) {
	m, err := contentrelease.Parse(read(t, filepath.Join("testdata", "valid", "optional-unsupported.yaml")))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("optional unsupported type refused: %v", err)
	}
	got := m.OptionalOmissions()
	if len(got) != 1 || got[0] != "index:embeddings" {
		t.Fatalf("omissions=%v; want the optional embeddings-index reported", got)
	}
}

func TestCanonicalBytes_StableForEquivalentManifests(t *testing.T) {
	a, err := contentrelease.Parse(read(t, filepath.Join("testdata", "valid", "portable.yaml")))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	first, err := contentrelease.CanonicalBytes(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := contentrelease.CanonicalBytes(a)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("CanonicalBytes is not stable")
	}
}

func TestParse_RejectsUnknownField(t *testing.T) {
	_, err := contentrelease.Parse([]byte("schema_version: 1\nkind: content-release\nbogus: x\n"))
	if err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestPackage_DoesNotImportRecordUID(t *testing.T) {
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			p := strings.Trim(im.Path.Value, `"`)
			if strings.Contains(p, "recorduid") {
				t.Fatalf("%s imports %s; artefact identity is not a second UID codec", name, p)
			}
		}
	}
}

func read(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatalf("%s is empty", rel)
	}
	return b
}
