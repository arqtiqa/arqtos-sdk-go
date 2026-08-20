package arqtossdk_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⚠️ THE README'S PER-PACKAGE KERNEL STATUS, PINNED AGAINST THE PACKAGES.
//
// The README said the whole kernel was "Skeleton: declared and documented, not
// implemented". That was true when written and false a week later — canonical
// and tapeformat both have real surface. It also attributed unknown-field
// rejection to kernel/canonical, which does not do it: Encode keeps extra map
// keys, and the rejection lives in contracts.Decode and tapeformat.
//
// Both are the same failure: prose has no derivation, so it drifts and nothing
// notices. docsclaims_test.go already gives the connector class set this
// treatment, on the stated grounds that a stale document costs an EXTERNAL
// reader who has no way to know it is stale. The kernel is read by exactly that
// reader.
func TestREADME_KernelStatusMatchesThePackages(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	doc := strings.Join(strings.Fields(string(raw)), " ")

	// The claim the README makes about each kernel package, and what the tree
	// must show for it to be true.
	cases := []struct {
		pkg          string
		claim        string
		wantExported bool
	}{
		{"kernel/canonical", "[`canonical`](kernel/canonical/) **implemented**", true},
		{"kernel/tapeformat", "[`tapeformat`](kernel/tapeformat/) **implemented**", true},
		{"kernel/reduce", "[`reduce`](kernel/reduce/) **seam only**", true},
		{"kernel/predicate", "[`predicate`](kernel/predicate/)", false},
		{"kernel/keyhistory", "[`keyhistory`](kernel/keyhistory/)", false},
	}

	for _, c := range cases {
		t.Run(c.pkg, func(t *testing.T) {
			claim := strings.Join(strings.Fields(c.claim), " ")
			if !strings.Contains(doc, claim) {
				t.Errorf("the README does not carry the claim %q", claim)
			}
			got := hasExportedSurface(t, c.pkg)
			if got != c.wantExported {
				verb := "does not export anything"
				if got {
					verb = "exports something"
				}
				t.Errorf("%s %s, which contradicts the README's claim %q", c.pkg, verb, claim)
			}
		})
	}

	// ⚠️ The two skeletons must still be listed as such, and the "whole kernel
	// is a skeleton" sentence must be gone — it is the claim that went stale.
	if strings.Contains(doc, "⚠️ Skeleton: declared and documented, not implemented. |") {
		t.Error("the README still calls the whole kernel a skeleton; canonical and tapeformat are implemented")
	}
	if len(cases) != 5 {
		t.Fatalf("checked %d kernel packages, want 5", len(cases))
	}
}

// ⚠️ Unknown-field rejection is NOT in kernel/canonical. Encode keeps extra map
// keys. The README once said it was, which would have a reader trust the encoder
// to refuse a widened body — it does not.
func TestREADME_DoesNotAttributeUnknownFieldRejectionToTheEncoder(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(raw)), " ")
	if strings.Contains(doc, "hash of — unknown fields **rejected**") {
		t.Error("the README attributes unknown-field rejection to kernel/canonical, which does not do it — " +
			"Encode keeps extra map keys. The rejection is in contracts.Decode and tapeformat.")
	}

	// And the claim must appear where it IS true.
	if !strings.Contains(doc, "`Decode` **rejects unknown fields**") {
		t.Error("the README no longer says where unknown-field rejection actually happens")
	}

	// Proved, not asserted: the encoder really does keep an unknown key.
	assertEncoderKeepsUnknownKeys(t)
}

func hasExportedSurface(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	examined := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		examined++
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if decl.Name.IsExported() && decl.Recv == nil {
					return true
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch sp := spec.(type) {
					case *ast.TypeSpec:
						if sp.Name.IsExported() {
							return true
						}
					case *ast.ValueSpec:
						for _, n := range sp.Names {
							if n.IsExported() {
								return true
							}
						}
					}
				}
			}
		}
	}
	if examined == 0 {
		t.Fatalf("%s holds no non-test source, so this check looked at nothing", dir)
	}
	return false
}
