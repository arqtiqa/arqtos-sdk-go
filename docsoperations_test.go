package arqtossdk_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// docs/CONTRACT.md documents each class's operations as a table, one row per
// method. That table is a SECOND copy of the interface, and a second copy of a
// fact is a second thing to drift.
//
// It has drifted, measured 2026-08-12: the CodeCI contract gained GetIssue and
// CloseIssue, and an external task specification derived from an older reading
// still presented eight operations as "the whole required surface" — so a
// connector written from it would not satisfy the interface it was written
// against. That specification also asked for "all 14 checks" while the harness
// ran twenty.
//
// The sibling docsclaims_test.go pins WHICH CLASSES exist. This pins WHAT EACH
// CLASS REQUIRES, which is the half that goes stale faster: a class is added
// rarely and loudly, while a method is added to an existing interface routinely
// and quietly.
//
// ⚠️ One inconsistency this check surfaced and does NOT fix: Tracker is the only
// published class documented in prose rather than a per-operation table. Its five
// operations are named and described, so it passes — but it is the newest class
// and the odd one out, and a table would make the next added method visible as a
// missing row rather than as an absent sentence.
//
// Direction, and why only one: every method declared on a published class
// interface MUST appear in CONTRACT.md. The reverse — the document naming a
// method that does not exist — is deliberately NOT checked here, because method
// names appear in prose throughout the document and a global scan for them
// would report drift on ordinary English. The costly direction is this one: a
// contract obligation nobody documented.

// classContract maps each published class to the package directory and the
// interface that declares its operations.
//
// ⚠️ This map is itself a second place the class set is written down, which is
// the exact defect this file exists to catch. TestClassContractMapCoversEveryClass
// below is the guard: it fails if the map and connector.Classes() disagree, so a
// new class cannot be silently skipped by every check here.
// An entry with an empty dir declares the class PENDING: published in the
// closed set, with its contract package still landing. That state is real —
// the manifest's implements enum derives from the class set, so a class must
// be published before a harness can compile a check against it — and the
// alternative was a circular dependency in which neither could land first.
//
// ⚠️ Pending is not a hiding place. TestClassContractMapCoversEveryClass still
// requires an explicit entry, so a new class cannot be skipped silently, and
// TestPendingClassesHaveNoContractPackageYet fails if a "pending" class turns
// out to have a package on disk — which would be a landed contract quietly
// exempted from every operation check.
var classContracts = map[connector.Class]struct {
	dir   string
	iface string
}{
	connector.ClassCredentialLoader: {"credential", "CredentialLoader"},
	connector.ClassRoster:           {"roster", "Roster"},
	connector.ClassCodeCI:           {"codeci", "CodeCI"},
	connector.ClassTracker:          {"tracker", "Tracker"},
	connector.ClassAuthenticator:    {"authenticator", "Authenticator"},
	connector.ClassCodeHost:         {"codehost", "CodeHost"},
}

// TestPendingClassesHaveNoContractPackageYet is the anti-abuse guard on the
// pending marker above: the moment a pending class's package exists, the marker
// is stale and its operations are going unchecked.
func TestPendingClassesHaveNoContractPackageYet(t *testing.T) {
	for class, c := range classContracts {
		if c.dir != "" {
			continue
		}
		for _, candidate := range []string{strings.ToLower(string(class)), string(class)} {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				t.Errorf("class %s is marked pending in classContracts, but %q exists. "+
					"Its contract has landed and its operations are checked by nothing — "+
					"replace the pending marker with the package and interface names.",
					class, candidate)
			}
		}
	}
}

// TestClassContractMapCoversEveryClass keeps the map above honest. Without it,
// adding a class to the SDK and forgetting this file would leave the new class
// entirely unchecked while every test here still passed — a green run that
// covers less than it did yesterday.
func TestClassContractMapCoversEveryClass(t *testing.T) {
	var mapped []connector.Class
	for c := range classContracts {
		mapped = append(mapped, c)
	}
	slices.Sort(mapped)

	published := slices.Clone(connector.Classes())
	slices.Sort(published)

	if !slices.Equal(mapped, published) {
		t.Fatalf("classContracts covers %v; connector.Classes() publishes %v.\n"+
			"Add the new class here as well, or its operations are documented by nothing and "+
			"checked by nothing.", mapped, published)
	}
}

// interfaceMethods returns the methods declared DIRECTLY on the named interface
// in the given package directory. Embedded interfaces are skipped on purpose:
// connector.Connector's four base operations are documented once, in
// CONTRACT.md's base-contract section, rather than repeated per class.
func interfaceMethods(t *testing.T, dir, iface string) []string {
	t.Helper()

	// Files are enumerated and parsed one at a time rather than with
	// parser.ParseDir, which is deprecated: it does not consider build tags when
	// associating files with packages. The documented replacement pulls in
	// golang.org/x/tools/go/packages, and this repository gates its dependency
	// boundary — so for a task this small, reading the directory is the cheaper
	// correct answer than taking a dependency to find one interface.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var methods []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Join(dir, name), err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != iface {
				return true
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				return true
			}
			for _, field := range it.Methods.List {
				// A field with no name is an embedded interface.
				for _, fieldName := range field.Names {
					methods = append(methods, fieldName.Name)
				}
			}
			return false
		})
	}

	if len(methods) == 0 {
		t.Fatalf("found no directly-declared methods on %s.%s — either the interface was renamed "+
			"or the parse stopped matching, and this check silently stopped checking. Fix the "+
			"lookup, do not delete the test.", dir, iface)
	}
	slices.Sort(methods)
	return methods
}

// TestContractDocDocumentsEveryClassOperation is the check that would have
// caught GetIssue and CloseIssue going undocumented.
func TestContractDocDocumentsEveryClassOperation(t *testing.T) {
	doc, err := os.ReadFile(contractDoc)
	if err != nil {
		t.Fatalf("read %s: %v", contractDoc, err)
	}
	text := string(doc)

	for class, c := range classContracts {
		if c.dir == "" {
			// Pending: no contract package to read operations from yet. The
			// marker's honesty is enforced by TestPendingClassesHaveNoContractPackageYet.
			continue
		}
		for _, method := range interfaceMethods(t, c.dir, c.iface) {
			// Two documentation styles are both accepted, because the document
			// genuinely uses both:
			//
			//   `Name(args) (returns)`  — a per-method table row. Four of the
			//                             five classes are written this way.
			//   `Name`                  — a backticked identifier in prose. The
			//                             Tracker contract names its five
			//                             operations this way and describes each
			//                             in the paragraphs that follow.
			//
			// The backticks are what carry the weight: they mark a deliberate
			// reference to the identifier rather than an English word that
			// happens to collide with a method name. Requiring the open paren
			// would report the entire Tracker class as undocumented, which is
			// false — see the note in this file's header about that section's
			// format differing from its siblings.
			if !strings.Contains(text, "`"+method+"(") && !strings.Contains(text, "`"+method+"`") {
				t.Errorf("%s: class %s requires %s.%s.%s, and %s documents no operation named %q.\n"+
					"A required operation nobody documented is one an implementer does not build — "+
					"which is how a connector comes to be written against eight operations when the "+
					"interface has ten.",
					contractDoc, class, filepath.Base(c.dir), c.iface, method, contractDoc, method)
			}
		}
	}
}
