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

	packages := []string{
		"kernel/canonical",
		"kernel/tapeformat",
		"kernel/reduce",
		"kernel/predicate",
		"kernel/keyhistory",
	}

	for _, pkg := range packages {
		t.Run(pkg, func(t *testing.T) {
			status := derivedStatus(t, pkg)
			name := pkg[strings.LastIndex(pkg, "/")+1:]
			link := "[`" + name + "`](" + pkg + "/)"

			// The claim the README must carry, derived from the tree.
			want := link + " **" + status + "**"
			if status == statusSkeleton {
				// The two skeletons are listed together in one clause, so the
				// bold marker follows both links rather than each one.
				if !strings.Contains(doc, link) {
					t.Fatalf("the README does not mention %s at all", pkg)
				}
				if strings.Contains(doc, link+" **implemented**") {
					t.Errorf("the README calls %s implemented; it exports nothing runnable", pkg)
				}
				return
			}
			if !strings.Contains(doc, want) {
				t.Errorf("%s is implemented, but the README does not say so — it must carry %q.\n"+
					"⚠️ Derived from the package, not from a phrase: this test cannot be satisfied "+
					"by leaving a stale sentence in place.", pkg, want)
			}
			for _, stale := range []string{"**seam only**", "**skeleton**", "**not implemented**"} {
				if strings.Contains(doc, link+" "+stale) {
					t.Errorf("the README still calls %s %s, but the package is implemented", pkg, stale)
				}
			}
		})
	}

	// ⚠️ The "whole kernel is a skeleton" sentence must stay gone — it is the
	// claim that went stale first, and the one this test was written for.
	if strings.Contains(doc, "⚠️ Skeleton: declared and documented, not implemented. |") {
		t.Error("the README still calls the whole kernel a skeleton; canonical and tapeformat are implemented")
	}
	if len(packages) != 5 {
		t.Fatalf("checked %d kernel packages, want 5", len(packages))
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

const (
	statusImplemented = "implemented"
	statusSkeleton    = "skeleton"
)

// derivedStatus reports what the TREE says about a package, so the README can be
// compared against it.
//
// ⚠️ THIS FUNCTION IS THE POINT OF THE FIX. The previous version of this test
// asserted that the README contained a particular claim STRING and otherwise
// only checked that the package exported something. Correcting a stale sentence
// therefore FAILED the test — the guard against drift had become the thing
// enforcing it, and the README described a seam that no longer existed because a
// test required it to.
//
// A package is a skeleton when it exports nothing callable, or when it DECLARES
// ErrNotImplemented — the repo-wide sentinel for a surface that is present and
// deliberately inert. Anything else is implemented. There is no claim string in
// that rule.
//
// ⚠️ THE FIRST VERSION OF THIS RULE COUNTED, and a falsifier caught it: it read
// "skeleton" only when EVERY exported function returned the sentinel. Stub out a
// package's entry point and leave two exported helpers beside it, and the count
// says two-of-three are real — so the README kept claiming "implemented" over a
// reducer that decided nothing, which is the precise state this whole bug was
// filed about. A status is not a majority vote among a package's exports.
func derivedStatus(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	examined, exported := 0, 0
	declaresSentinel := false
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
		if declaresNotImplemented(f) {
			declaresSentinel = true
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() || fn.Recv != nil || fn.Body == nil {
				continue
			}
			exported++
		}
	}
	if examined == 0 {
		t.Fatalf("%s holds no non-test source, so this check looked at nothing", dir)
	}
	if exported == 0 || declaresSentinel {
		return statusSkeleton
	}
	return statusImplemented
}

// declaresNotImplemented reports whether a file DECLARES the inert-surface
// sentinel. A package that has one is announcing that some part of it is a
// placeholder, and the README must not call it implemented.
func declaresNotImplemented(f *ast.File) bool {
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, n := range vs.Names {
				if n.Name == "ErrNotImplemented" {
					return true
				}
			}
		}
	}
	return false
}
