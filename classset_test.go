package arqtossdk_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

// The connector-class set is declared in three places that must always agree,
// and connector.go states the rule in its own words:
//
//	classes is the single source of truth for the closed set of connector
//	classes the SDK knows. Classes and Class.Valid both derive from it, and so
//	does the manifest schema's implements enum — a class cannot be half-added
//	(declarable in a manifest but unknown here, or known here and refused by
//	the manifest).
//
// The three places are:
//
//  1. the Class CONSTANTS in connector/connector.go — what Go code names;
//  2. the connector.classes SLICE — what Classes() and Class.Valid() answer
//     from;
//  3. the manifest schema's implements ENUM — what a connector author may
//     declare and what a host accepts before it loads anything.
//
// Each derivation is written to be automatic, and each has been half-added at
// least once anyway. (3) was a hand-written map, and adding a class left it
// undeclarable in a manifest: the symptom was a valid connector rejected with
// "unknown implements", pointing at the manifest, which was correct. (1) and
// (2) are still two edits in one file, which is exactly the kind of pair that
// is trivially correct on the day it is written and silently wrong six months
// later.
//
// These tests live at the repo root rather than in any one package for the
// reason boundary_test.go does: the invariant spans packages, so no single
// package's tests can hold it. connector cannot see manifest's enum without
// importing its own dependent, and manifest cannot see whether a Go constant
// exists at all.
//
// (1) is read out of the SOURCE with go/ast, and that is the only way to read
// it. Go publishes no reflection over constants: a declared-but-unlisted
// constant is invisible to every runtime check, which is precisely the
// half-added state — routable in Go, refused by every manifest.

// classConstants parses connector/connector.go and returns every declared
// constant of type Class, as name → value.
//
// It fails the test rather than returning an error: a parse that did not find
// the declarations would make every assertion below trivially true, which is
// the tautological-gate shape these tests exist to close.
func classConstants(t *testing.T) map[string]string {
	t.Helper()

	const src = "connector/connector.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}

	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		// Within one const group Go repeats the previous type when a spec
		// omits it, so the last type seen is carried forward. Without this a
		// class added as a bare `ClassX = "X"` under an earlier `Class`-typed
		// spec would be invisible here — a constant the test cannot see is a
		// constant the test cannot hold.
		var lastType string
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if id, ok := vs.Type.(*ast.Ident); ok {
				lastType = id.Name
			}
			if lastType != "Class" || len(vs.Values) != len(vs.Names) {
				continue
			}
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: constant %s of type Class is not a string literal; this test reads the class "+
						"set out of the source and cannot evaluate an expression", src, name.Name)
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: constant %s has an unreadable literal %s: %v", src, name.Name, lit.Value, err)
				}
				out[name.Name] = val
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s: found no Class constants at all; every assertion built on this would pass by observing "+
			"nothing", src)
	}
	return out
}

// classesSliceIdents parses the connector.classes slice literal and returns the
// IDENTIFIER of each element.
//
// The identifiers are the point, not the values. A class spelled into the slice
// as a string literal — `classes = []Class{..., "Tracker"}` — makes Classes()
// and the manifest enum agree with each other while agreeing with no constant,
// so a one-character difference from the constant it was meant to be is a class
// Go code names and no manifest accepts.
func classesSliceIdents(t *testing.T) []string {
	t.Helper()

	const src = "connector/connector.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}

	var out []string
	found := false
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "classes" || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				t.Fatalf("%s: classes is not a composite literal; this test reads its elements out of the source",
					src)
			}
			found = true
			for _, el := range lit.Elts {
				id, ok := el.(*ast.Ident)
				if !ok {
					t.Errorf("%s: classes carries an element that is not a plain constant identifier (%T); a class "+
						"spelled as a literal here names no constant, so a one-character difference is a class Go "+
						"code cannot reach and no manifest accepts", src, el)
					continue
				}
				out = append(out, id.Name)
			}
		}
	}
	if !found {
		t.Fatalf("%s: found no classes slice; the single source of truth this test exists to hold is not there", src)
	}
	return out
}

// TestConnectorClassSetCannotBeHalfAdded is the AC this file exists for: ONE
// test that holds all three declarations together, so adding a class produces
// one failure naming the whole triad rather than three failures in three
// packages that each look like a local problem.
//
// The manifest package asserts the (2) → (3) direction from its own side
// (TestEveryKnownClassIsDeclarableWithoutAManifestEdit) and closes each class's
// capability vocabulary (TestEveryKnownClassHasARegisteredCapabilityVocabulary).
// Neither of those can see (1): a Go constant's existence is invisible at
// runtime. This test is where the constants enter, and it carries the manifest
// leg with them so that its forward half cannot be vacuous — a schema that
// accepted every string would satisfy "every known class is declarable" for
// every class the SDK will ever have.
func TestConnectorClassSetCannotBeHalfAdded(t *testing.T) {
	constants := classConstants(t)
	published := connector.Classes()

	t.Run("every declared constant is in classes", func(t *testing.T) {
		for name, value := range constants {
			class := connector.Class(value)
			if !slices.Contains(published, class) {
				t.Errorf("connector.%s = %q is a declared Class constant that connector.Classes() does not carry "+
					"(%v): it is routable in Go and refused by every manifest, which is the half-added state "+
					"connector.go's comment names", name, value, published)
			}
			if !class.Valid() {
				t.Errorf("connector.%s = %q is a declared Class constant that is not Valid(); Class.Valid derives "+
					"from classes, so this is the same divergence read through the other accessor", name, value)
			}
		}
	})

	t.Run("every entry in classes is a declared constant", func(t *testing.T) {
		values := make(map[string]string, len(constants))
		for name, value := range constants {
			values[value] = name
		}
		for _, class := range published {
			if _, ok := values[string(class)]; !ok {
				t.Errorf("connector.Classes() carries %q, and no Class constant in connector/connector.go has that "+
					"value (constants: %v): Go code cannot name the class, so nothing in this SDK can route to it "+
					"while every manifest declaring it validates", class, constants)
			}
		}
	})

	t.Run("classes is spelled with the constants", func(t *testing.T) {
		idents := classesSliceIdents(t)
		if len(idents) != len(published) {
			t.Errorf("classes has %d element(s) in source and Classes() returns %d; a duplicate or a dropped element "+
				"here changes what Class.Valid answers", len(idents), len(published))
		}
		for _, ident := range idents {
			if _, ok := constants[ident]; !ok {
				t.Errorf("classes carries the identifier %s, which is not a declared Class constant", ident)
			}
		}
		for name := range constants {
			if !slices.Contains(idents, name) {
				t.Errorf("the Class constant %s is not spelled into the classes slice; Classes() and Class.Valid "+
					"both derive from that slice, so the constant is unknown to both", name)
			}
		}
	})

	t.Run("the manifest enum accepts exactly the set", func(t *testing.T) {
		for _, class := range published {
			doc := manifest.Doc{Name: "probe", Implements: class, Kind: manifest.KindNative}
			if err := doc.Validate(); err != nil {
				t.Errorf("connector.Classes() carries %q and the manifest schema refuses it: %v; the connector would "+
					"be rejected with an error naming its manifest, which is correct — the schema's implements enum "+
					"is DERIVED from Classes() precisely so this cannot happen", class, err)
			}
		}

		// Each of these is a name the schema must refuse, and each is a way the
		// enum has plausibly been loosened: a class reserved in connector.go's
		// trailing comment but never landed, a miscasing, a near-miss spelling,
		// and the empty class a manifest that omits the field produces.
		for _, name := range []connector.Class{"RecordStore", "CodeHost", "tracker", "TRACKER", "Trackers", ""} {
			if slices.Contains(published, name) {
				t.Fatalf("%q is now a real class, so it cannot serve as the closed-enum probe; pick another name", name)
			}
			doc := manifest.Doc{Name: "probe", Implements: name, Kind: manifest.KindNative}
			if err := doc.Validate(); err == nil {
				t.Errorf("the manifest schema accepted implements: %q, which connector.Classes() does not carry; an "+
					"enum that accepts a class the SDK cannot route is not closed, and without this half the forward "+
					"assertion above is true of a schema that accepts every string", name)
			}
		}
	})
}
