package arqtossdk_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// ⚠️ THE DEFECT THIS PREVENTS SHIPPED ONCE, NINE TIMES OVER.
//
// A test whose entire body is a t.Skip is not a pending test. It is a comment
// with a function signature: delete the skip and it passes, because there is
// nothing in it to fail. It asserts nothing, guards nothing, and reports as
// coverage — and it is the natural thing to write when a fixture must exist
// before the code it judges.
//
// The remedy is internal/redfixture, whose Expect runs a REAL body and requires
// it to fail. This check is what stops the empty form coming back.
//
// A conditional skip — "this environment cannot build a subprocess" — is a
// different thing entirely and is untouched: it has other statements around it,
// and it is a statement about the runner rather than about the code.
func TestNoTestIsOnlyASkip(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	examined, offenders := 0, 0

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		examined++

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			// ⚠️ Exactly one statement, and it is a skip. More than one means
			// the skip is guarded by something, which is the legitimate form.
			if len(fn.Body.List) != 1 {
				continue
			}
			expr, ok := fn.Body.List[0].(*ast.ExprStmt)
			if !ok {
				continue
			}
			call, ok := expr.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if sel.Sel.Name != "Skip" && sel.Sel.Name != "Skipf" && sel.Sel.Name != "SkipNow" {
				continue
			}
			offenders++
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s: %s is nothing but a skip, at %s.\n\n"+
				"That is not a red test — delete the skip and it passes, because there is nothing in "+
				"it to fail. Give it a real body and wrap it in internal/redfixture.Expect, which "+
				"requires the body to FAIL and so makes an empty one impossible.",
				rel, fn.Name.Name, fset.Position(fn.Pos()))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}

	// ⚠️ Count-asserted. A walk that found no test files would report no
	// offenders and read exactly like a pass — which is the same shape of
	// mistake this whole check exists to catch.
	if examined < 20 {
		t.Fatalf("examined %d test files, which is too few — this check is not reaching the tree", examined)
	}
	t.Logf("examined %d test files; %d offenders", examined, offenders)
}
