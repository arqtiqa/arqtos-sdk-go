package arqtossdk_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The kernel family and contracts/ ship as DECLARED, DOCUMENTED, EMPTY packages.
// That is deliberate sequencing rather than unfinished work: the canonical
// encoding's committed test vectors and reduce's first failing test are written
// BEFORE anything satisfies them, so an implementation landing early would be
// judged by tests written to match it — which is the one failure the ordering
// exists to prevent.
//
// ⚠️ The obvious way to give these packages a test is a TestPackageExists per
// package, and it is worthless: it asserts something the compiler already
// guarantees, it can never fail for a reason anyone cares about, and it would
// tick the "every package has a test" box while measuring nothing. A test that
// cannot fail is not coverage.
//
// So this is the falsifier that IS meaningful for an intentionally-empty
// package: it asserts the package is still empty, and fails the moment it stops
// being. The claim is real, checkable, and it goes red on exactly the change
// that matters — the one where somebody lands kernel semantics.
//
// # What to do when this test fails
//
// It is not an obstacle and it is not asking you to keep the package empty. It
// is asking that the change which adds surface ALSO add that surface's tests,
// and then remove the package from skeletonPackages below. Deleting the entry is
// part of the work, not a workaround for it — and doing it in the same change is
// what stops a package quietly acquiring an API with no test behind it, which is
// exactly what an empty skeleton makes easy.
// ⚠️ "contracts" and "kernel/canonical" were each removed on the change that
// gave them real surface — the
// four record layers, their JSON Schemas and their golden fixtures — and that
// removal is what this guard is FOR. It went red naming all 36 exported
// identifiers and demanding the entry come out in the same change as the
// tests. It did.
// ⚠️ "kernel/tapeformat" came out the same way, on the change that gave it the
// entry shape, the chain check and the stream reader. The guard went red naming
// all 11 exported identifiers; the tests landed with them.
// ⚠️ "kernel/reduce" came out when it gained the reducer SEAM — Reduce, Input,
// Outcome and ErrNotImplemented. The reducer itself is still unbuilt, and that
// is precisely why the seam had to land: the kill-gate fixtures beside it had no
// function to call, so they were empty t.Skip placeholders that could not fail.
// The seam is what let them become assertions. See internal/redfixture.
var skeletonPackages = []string{
	"kernel",
	"kernel/keyhistory",
	"kernel/predicate",
}

// exportedDecls returns the exported top-level identifiers declared in dir.
func exportedDecls(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading skeleton package %s: %v — this list is hand-maintained, so a rename or a "+
			"deletion must update it rather than silently scanning nothing", dir, err)
	}

	fset := token.NewFileSet()
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", filepath.Join(dir, name), err)
		}
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if decl.Name.IsExported() {
					out = append(out, "func "+decl.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							out = append(out, "type "+s.Name.Name)
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								out = append(out, "var/const "+n.Name)
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestSkeletonPackages_ExportNothingYet(t *testing.T) {
	// ⚠️ The COUNT is asserted, not merely the absence of findings. A path typo
	// or a parser change that stopped recognising declarations would report zero
	// exports across zero packages and read as a pass — the shape every silent
	// truncation takes.
	if len(skeletonPackages) == 0 {
		t.Fatal("skeletonPackages is empty, so this test would pass by examining nothing")
	}
	t.Logf("examined %d skeleton package(s)", len(skeletonPackages))

	for _, pkg := range skeletonPackages {
		t.Run(pkg, func(t *testing.T) {
			if got := exportedDecls(t, pkg); len(got) > 0 {
				t.Errorf("%s now exports %d identifier(s): %s\n\n"+
					"This package is listed as an intentionally-empty skeleton. If that is no longer "+
					"true — good — then the same change must carry this surface's tests and remove %q "+
					"from skeletonPackages in this file. Do not remove the entry on its own: the entry "+
					"is what stops an API landing here with nothing behind it.",
					pkg, len(got), strings.Join(got, ", "), pkg)
			}
		})
	}
}

// Every skeleton package must still carry its package documentation. The doc is
// the entire content of these packages, so a skeleton that lost it would be an
// empty directory asserting nothing at all — and nothing else would notice.
func TestSkeletonPackages_StillDocumentTheirContract(t *testing.T) {
	for _, pkg := range skeletonPackages {
		t.Run(pkg, func(t *testing.T) {
			entries, err := os.ReadDir(pkg)
			if err != nil {
				t.Fatalf("reading %s: %v", pkg, err)
			}

			fset := token.NewFileSet()
			documented := false
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				f, err := parser.ParseFile(fset, filepath.Join(pkg, name), nil, parser.ParseComments)
				if err != nil {
					t.Fatalf("parsing %s: %v", filepath.Join(pkg, name), err)
				}
				if f.Doc != nil && len(f.Doc.List) > 0 {
					documented = true
				}
			}
			if !documented {
				t.Errorf("%s has no package doc comment. The documentation IS the content of a "+
					"skeleton package — without it the directory declares a name and says nothing "+
					"about the contract it is holding open.", pkg)
			}
		})
	}
}
