package reduce_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// ⚠️ THE PACKAGE DOC AND THE FUNCTION DOC MUST NOT DISAGREE ABOUT THE DEFAULT.
//
// doc.go was corrected when the reducer gained an accept path; the comment on
// Reduce itself was not, and kept saying "IT REFUSES BY DEFAULT. An act no rule
// admits is refused, never accepted" for a week afterwards. `go doc
// reduce.Reduce` prints the function comment, so the surface a caller actually
// reads was the stale one.
//
// This is the same class as the README check one level down, and the reason it
// needs a test rather than care: the two comments are twenty lines apart, both
// look authored, and nothing brings them into view together.
func TestDoc_PackageAndFunctionAgreeAboutTheDefault(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	pkg, ok := pkgs["reduce"]
	if !ok {
		t.Fatal("package reduce not found; this check is not seeing its own source")
	}

	var pkgDoc, funcDoc string
	for _, file := range pkg.Files {
		if file.Doc != nil && pkgDoc == "" {
			pkgDoc = file.Doc.Text()
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == "Reduce" && fn.Doc != nil {
				funcDoc = fn.Doc.Text()
			}
		}
	}
	if pkgDoc == "" || funcDoc == "" {
		t.Fatal("package doc or Reduce's doc is missing, so this check compares nothing")
	}

	// ⚠️ COLLAPSE WHITESPACE BEFORE MATCHING. A doc comment wraps, so a phrase
	// the author wrote as one sentence arrives with a newline in the middle of
	// it and a raw Contains misses — which is how this test first reported a
	// correction that was already in the file. Same normalisation the README
	// check uses, for the same reason.
	flatten := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	pkgDoc, funcDoc = flatten(pkgDoc), flatten(funcDoc)

	// ⚠️ THE STALE CLAIM IS NAMED, NOT INFERRED. A check that tried to detect
	// "agreement" in general would be a language model, not a test. What it can
	// do is refuse the one sentence that was actually wrong, in either document.
	const retired = "refuses by default"
	for name, doc := range map[string]string{"the package doc": pkgDoc, "Reduce's doc": funcDoc} {
		if strings.Contains(doc, retired) {
			t.Errorf("%s still says the reducer %q — that semantics was removed when the "+
				"accept path landed, and a caller reading it will expect a cautious reducer "+
				"that does not exist", name, retired)
		}
	}

	// And both must state what actually holds, so correcting one and emptying
	// the other is not a fix.
	const holds = "refuse more"
	for name, doc := range map[string]string{"the package doc": pkgDoc, "Reduce's doc": funcDoc} {
		if !strings.Contains(doc, holds) {
			t.Errorf("%s does not state the property that replaced refuse-by-default: "+
				"adding a rule can only ever REFUSE MORE", name)
		}
	}
}
