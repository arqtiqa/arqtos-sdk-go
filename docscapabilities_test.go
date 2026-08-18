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

// A capability constant is a name a connector author reads in the Go source and
// declares in a manifest. Its MEANING lives in docs/CONTRACT.md, and for two of
// them the meaning is not the one the name suggests.
//
// ⚠️ Measured cost, arqtiqa/arqtos-sdk-go#90: the Okta session loader shipped
// declaring `oidc` on the natural reading — "the reference I serve came from an
// OIDC flow". It VALIDATES, no harness objects, and the manifest published
// something false about the connector's own credential posture to the one audience
// that acts on it. CONTRACT.md says the opposite direction: CapOIDC and CapAppRole
// "describe how the connector itself authenticates outward, not a behavior it
// exposes inward — hosts use them to reason about the connector's own credential
// posture, e.g. for audit and rotation policy."
//
// The mistake was made FROM THE GO SOURCE, where those constants sat beside
// CapRead with no comment at all, and was caught only by reading the contract
// afterwards.
//
// Two checks, and the second is the one that cannot go stale:
//
//  1. every capability constant carries a doc comment — so the source is not
//     silent about a name whose meaning is not guessable;
//  2. every capability constant appears in docs/CONTRACT.md — so a capability
//     cannot be added to the vocabulary without the document that defines it.
//
// ⚠️ Direction, and why only one: source → document. The reverse (the document
// naming a capability that does not exist) is deliberately not checked, for the
// same reason docsoperations_test.go gives about method names — capability words
// like "read", "lease" and "watch" appear in ordinary prose throughout the
// document, so a global scan for them would report drift on English.

// capabilityDecl is one `CapX connector.Capability = "y"` constant as declared.
type capabilityDecl struct {
	Pkg    string
	Name   string
	Value  string
	Pos    string
	HasDoc bool
}

// classPackagesWithCapabilities are the published class packages. Hand-listed
// rather than globbed: a new class package with capabilities should fail this list
// deliberately, the way a new class fails docsclaims_test.go's.
var classPackagesWithCapabilities = []string{"codeci", "codehost", "credential", "roster", "tracker"}

func collectCapabilityDecls(t *testing.T) []capabilityDecl {
	t.Helper()
	var out []capabilityDecl
	fset := token.NewFileSet()
	for _, pkg := range classPackagesWithCapabilities {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatalf("reading class package %s: %v — this list is hand-maintained, so a rename must "+
				"update it rather than silently scanning nothing", pkg, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(pkg, e.Name())
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
						continue
					}
					// Only `connector.Capability`-typed constants.
					sel, ok := vs.Type.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Capability" {
						continue
					}
					lit, ok := vs.Values[0].(*ast.BasicLit)
					if !ok {
						continue
					}
					out = append(out, capabilityDecl{
						Pkg:    pkg,
						Name:   vs.Names[0].Name,
						Value:  strings.Trim(lit.Value, `"`),
						Pos:    fset.Position(vs.Pos()).String(),
						HasDoc: vs.Doc != nil && len(vs.Doc.List) > 0,
					})
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no capability constant was found at all, so both checks below would pass by " +
			"examining nothing")
	}
	return out
}

func TestCapabilities_EveryConstantCarriesADocComment(t *testing.T) {
	decls := collectCapabilityDecls(t)
	var bare []string
	for _, d := range decls {
		if !d.HasDoc {
			bare = append(bare, d.Pkg+"."+d.Name+" ("+d.Pos+")")
		}
	}
	sort.Strings(bare)
	// ⚠️ The COUNT is asserted, not just the absence of findings: a parser change
	// that stopped recognising these declarations would report zero bare constants
	// and read as a pass.
	t.Logf("examined %d capability constant(s) across %d class package(s)",
		len(decls), len(classPackagesWithCapabilities))
	if len(bare) > 0 {
		t.Errorf("%d capability constant(s) have no doc comment, so a connector author reading the "+
			"source sees a name and no meaning:\n  %s\n\nCONTRACT.md defines each one; state it here, "+
			"and for CapOIDC/CapAppRole state what they do NOT mean.",
			len(bare), strings.Join(bare, "\n  "))
	}
}

// contractUndocumented lists capability constants that exist in the Go vocabulary
// and are defined NOWHERE in docs/CONTRACT.md, under either spelling.
//
// ⚠️ It is an ALLOWLIST, not an exemption. Each entry is a capability a connector
// author can declare in a manifest with no authoritative statement of what they are
// declaring — the same condition that cost arqtos-sdk-go#90, one class over. The
// list must only ever SHRINK, and the guard below fails on a stale entry.
//
// ⚠️ Measured by running the check with this map EMPTY, not by grepping: a check
// with a populated allowlist reports only the offenders NOT on the list, so a
// populated map hides exactly what you are counting. Run with 4 of 25 undocumented,
// all in tracker — which is also the only class package with no capability-vocabulary
// SECTION in the contract at all (CredentialLoader, Roster, CodeCI and CodeHost each
// have one).
//
// Writing those four definitions is a semantic call about what the Tracker contract
// promises, not a mechanical edit, so it is filed rather than guessed at here.
var contractUndocumented = map[string]bool{}

func TestCapabilities_EveryConstantIsDefinedInTheContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("docs", "CONTRACT.md"))
	if err != nil {
		t.Fatalf("reading docs/CONTRACT.md: %v", err)
	}
	contract := string(body)
	var missing []string
	decls := collectCapabilityDecls(t)
	documented := len(decls)
	for _, d := range decls {
		// ⚠️ EITHER FORM counts, and asserting only one was wrong. The document
		// names credential's and Roster's capabilities by Go NAME (`CapOIDC`) and
		// CodeHost's and Tracker's by STRING VALUE (`native_types`, `file_read`).
		// A check demanding the Go name reported 15 of 25 as undocumented when all
		// 15 were documented under the other spelling — a finding invented by the
		// assertion rather than found in the corpus.
		key := d.Pkg + "." + d.Name
		if !strings.Contains(contract, d.Name) && !strings.Contains(contract, d.Value) {
			if contractUndocumented[key] {
				documented--
				continue
			}
			// ⚠️ Decrement here TOO, not only in the allowlisted branch above.
			// Until this line existed the summary reported "25 of 25 are
			// defined" on a run that FAILED because one was not — a count
			// asserting something it never examined, which is the exact defect
			// this test exists to catch, in the test itself.
			documented--
			missing = append(missing, key+" (neither "+d.Name+" nor "+d.Value+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d capability constant(s) appear nowhere in docs/CONTRACT.md, so the vocabulary grew "+
			"without the document that gives it meaning:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	// ⚠️ The count is REPORTED, so "done" is a number rather than an absence of
	// findings — and the debt is visible on every green run instead of only when
	// somebody opens the allowlist.
	t.Logf("%d of %d capability constant(s) are defined in docs/CONTRACT.md; %d known-undocumented",
		documented, len(decls), len(contractUndocumented))
}

// TestCapabilities_ContractDebtEveryEntryIsStillUndocumented is the allowlist's own
// falsifier: an entry that HAS since been documented makes the debt look larger than
// it is, and an entry naming a constant that no longer exists is silently dead.
func TestCapabilities_ContractDebtEveryEntryIsStillUndocumented(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("docs", "CONTRACT.md"))
	if err != nil {
		t.Fatalf("reading docs/CONTRACT.md: %v", err)
	}
	contract := string(body)
	live := map[string]capabilityDecl{}
	for _, d := range collectCapabilityDecls(t) {
		live[d.Pkg+"."+d.Name] = d
	}
	for key := range contractUndocumented {
		d, exists := live[key]
		if !exists {
			t.Errorf("allowlist entry %q names no capability constant that exists — delete the line", key)
			continue
		}
		if strings.Contains(contract, d.Name) || strings.Contains(contract, d.Value) {
			t.Errorf("allowlist entry %q IS now documented in CONTRACT.md — delete its line, the debt "+
				"shrank", key)
		}
	}
}
