package reduce_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⭐ THE GUARD AGAINST THE DEFECT THAT PRODUCED THIS FILE.
//
// Three tests in this package claimed to prove the reducer's ALLOW direction —
// each one commented as "the other half, without which the rule is
// indistinguishable from refusing everything" — and each asserted only that a
// particular phrase was ABSENT from the refusal reason. A neighbouring rule's
// refusal satisfied all three. They were green for a whole week over a reducer
// that admitted no candidate at all.
//
// ⚠️ The reason the defect survived review is that the tests LOOKED like the fix
// for it. The comments named the failure mode precisely. What was missing was
// the one assertion that distinguishes an acceptance from a refusal, and no
// amount of reading the comments would surface that.
//
// So the check is mechanical: a test whose NAME claims an acceptance must
// assert one. It reads this package's own test sources.
//
// # ⚠️ AND IT MEASURED A PROXY FOR ITS FIRST WEEK — the defect it exists to catch
//
// The body check was strings.Contains over a raw source slice, looking for the
// text "out.Accepted". A COMMENT satisfies that. Replacing a live assertion with
//
//	// out.Accepted is deliberately not asserted here.
//	_ = out
//
// left the whole package green. The scan already parsed an AST to find the
// function NAMES, then fell back to text for the thing that mattered.
//
// It now requires the field to be READ IN A CONDITION. Comments are absent from
// the tree when parsing without parser.ParseComments, so they cannot satisfy it,
// and a bare `_ = out.Accepted` is not a test of anything.
func TestEveryAllowPathTestAssertsAcceptance(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no test sources found to scan: %v", err)
	}

	// ⚠️ The scan must find the tests it is supposed to police. A glob that
	// silently matched nothing — a rename, a move, a build tag — would leave
	// this test green while checking nothing at all, which is the same shape of
	// failure it exists to catch.
	claimed := 0
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			if !claimsAcceptance(fn.Name.Name) {
				continue
			}
			claimed++
			if !assertsAcceptance(fn.Body) {
				t.Errorf("%s: %s claims an allow-path but never TESTS the accepted field — "+
					"a neighbouring rule's refusal would satisfy it",
					file, fn.Name.Name)
			}
		}
	}
	if claimed < 4 {
		t.Errorf("found only %d allow-path tests; the reducer has more accept cases than that, "+
			"so this scan is not seeing the file it is meant to police", claimed)
	}
}

// claimsAcceptance reports whether a test's NAME promises the reducer admits
// something. "DoesNotRefuse" is included because that was the original phrasing,
// and a rename back to it must not slip past this check.
func claimsAcceptance(name string) bool {
	for _, claim := range []string{"_Accepts", "DoesNotRefuse", "DoesNotReject"} {
		if strings.Contains(name, claim) {
			return true
		}
	}
	return false
}

// assertsAcceptance reports whether a body actually TESTS the accepted field.
//
// ⚠️ Reading it is not enough — `_ = out.Accepted` reads it. The field must
// appear inside a CONDITION: an if, a for, a switch tag or case, or a boolean
// operand. That is the difference between mentioning an outcome and asserting
// one, and it is the whole distinction this file exists to enforce.
func assertsAcceptance(body *ast.BlockStmt) bool {
	tested := false

	inCondition := func(n ast.Node) {
		if tested || n == nil {
			return
		}
		ast.Inspect(n, func(inner ast.Node) bool {
			if sel, ok := inner.(*ast.SelectorExpr); ok && sel.Sel != nil && sel.Sel.Name == acceptedField {
				tested = true
			}
			return !tested
		})
	}

	ast.Inspect(body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.IfStmt:
			inCondition(stmt.Cond)
		case *ast.ForStmt:
			inCondition(stmt.Cond)
		case *ast.SwitchStmt:
			inCondition(stmt.Tag)
		case *ast.CaseClause:
			for _, expr := range stmt.List {
				inCondition(expr)
			}
		case *ast.BinaryExpr:
			// A helper call such as require.True(t, out.Accepted) has no
			// if-statement of its own; the boolean expression is the assertion.
			inCondition(stmt)
		case *ast.UnaryExpr:
			if stmt.Op == token.NOT {
				inCondition(stmt.X)
			}
		}
		return !tested
	})
	return tested
}

// acceptedField is the outcome field an allow-path test must test.
const acceptedField = "Accepted"
