package reduce_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// ⚠️ PURITY IS THE REDUCER'S WHOLE CONTRACT, AND IT IS ENFORCED HERE.
//
// The reducer has NO ambient access: no filesystem, no environment, no clock,
// no network. This is not defensive style. Replay is only meaningful if the
// same inputs produce the same outputs on a different machine years later, and
// every ambient read is an input nobody recorded.
//
// The clock is the sharpest case and the most reasonable-looking line anyone
// could add: a reducer that consulted the wall clock would produce a different
// answer on replay than it did at acceptance, which is the one thing replay
// exists to detect. Predicates evaluate a RECORDED acceptance time.
func TestReduce_HasNoAmbientAccess(t *testing.T) {
	// package -> the calls that would be ambient access
	forbidden := map[string][]string{
		"time": {"Now", "Since", "Until", "Tick", "After", "NewTimer", "Sleep"},
		"os":   {"Open", "ReadFile", "WriteFile", "Getenv", "LookupEnv", "Environ", "Create", "Remove"},
		"net":  {"Dial", "Listen", "LookupHost"},
		"http": {"Get", "Post", "NewRequest", "Do"},
		"exec": {"Command", "CommandContext"},
		"rand": {"Int", "Intn", "Float64", "Read", "N"},
	}

	fset := token.NewFileSet()
	examined, calls := 0, 0
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		examined++

		// ⚠️ The IMPORTS are checked too, not only the calls. An aliased import
		// — `import clock "time"` — renames the selector and would slip past a
		// call-site scan entirely.
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			base := path
			if i := strings.LastIndex(path, "/"); i >= 0 {
				base = path[i+1:]
			}
			if _, bad := forbidden[base]; bad {
				t.Errorf("%s imports %q. The reducer has no ambient access: no filesystem, no "+
					"environment, no clock, no network. An ambient read is an input nobody recorded, "+
					"and replay is only meaningful if the recorded inputs are all of them.", name, path)
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			calls++
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			for _, fn := range forbidden[pkg.Name] {
				if sel.Sel.Name == fn {
					t.Errorf("%s calls %s.%s at %s — the reducer must not read %s.",
						name, pkg.Name, fn, fset.Position(call.Pos()), pkg.Name)
				}
			}
			return true
		})
	}

	// ⚠️ Both counts asserted. A walk that examined no files, or a file with no
	// calls in it, would report no ambient access and read exactly like a pure
	// reducer.
	if examined == 0 {
		t.Fatal("examined no source files, so this check passed by looking at nothing")
	}
	if calls == 0 {
		t.Fatal("found no calls at all, so the call scan proved nothing")
	}
	t.Logf("examined %d source file(s), %d call site(s)", examined, calls)
}
