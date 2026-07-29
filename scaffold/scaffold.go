// Package scaffold generates a Track-B (out-of-process) arqtos Roster
// connector skeleton — a spawnable provider binary — from the same proven
// shape as arqtos-sdk-go's examples/roster-provider.
//
// # Why out-of-process, and why Roster
//
// A third-party connector author is, by definition, writing someone else's
// binary: not built against arqtos-cli, not sharing this module's build. The
// out-of-process (Track-B) shape — a goplugin.Serve provider dialled over
// gRPC — is what that actually looks like, and rosterconform.RunOutOfProcess
// is the harness that shape is measured against. A scaffold that produced an
// in-process, natively-compiled skeleton would teach a shape the audience it
// is for cannot use.
//
// # Why it has to pass conformance on generation, before any real logic
//
// [Generate] writes a complete, buildable project: a go.mod pinned to a real
// published arqtos-sdk-go tag, a main.go that implements roster.Roster
// against a fixed placeholder directory, a connector.yml manifest, and an
// in-process conformance test. Build it and it passes rosterconform
// immediately. A skeleton that failed its own conformance harness on
// generation would teach exactly the wrong lesson to the one audience this
// package exists to save time for — a first-time external contributor with
// no other reference to check their work against.
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// SDKVersion is the arqtos-sdk-go tag every generated go.mod pins against.
//
// It is v0.2.0, not the latest tag at whatever moment this package was built,
// and not a floating "@latest": v0.2.0 is the first tag carrying the Roster
// wire protocol and the out-of-process rosterconform harness this skeleton
// is built to pass (see arqtos-sdk-go's CHANGELOG / release notes — v0.1.1
// predates both). A generated project pinned to an older tag would compile
// against a Roster contract that does not exist yet.
const SDKVersion = "v0.2.0"

// goDirective is the `go` directive every generated go.mod carries. It
// matches arqtos-sdk-go's own go.mod so the generated module's toolchain
// requirement is never looser than the dependency it pins.
const goDirective = "1.26"

// The fixture directory identifiers baked into every generated main.go and
// conform_test.go. They are exported, rather than private to a template, so
// a caller driving the generated binary against arqtos-sdk-go's own
// rosterconform harness (as this package's own tests do) names the exact
// same strings the generated code serves — one source, so the two cannot
// drift apart.
const (
	// FixtureGroupID is a populated group with an INHERITED member, which is
	// what makes the generated transitive_membership declaration checkable
	// rather than merely stated.
	FixtureGroupID = "example-directory-group-primary"
	// FixtureNestedGroupID is nested inside FixtureGroupID.
	FixtureNestedGroupID = "example-directory-group-nested"
	// FixtureParentGroupID is the group FixtureGroupID is nested inside.
	FixtureParentGroupID = "example-directory-group-parent"
	// FixtureEmptyGroupID EXISTS and has no members — a real state, distinct
	// from a group that does not exist.
	FixtureEmptyGroupID = "example-directory-group-empty"
	// FixtureAbsentGroupID is served by nothing: it must fail NotFound.
	FixtureAbsentGroupID = "example-directory-group-absent"
	// FixtureActivePrincipalID is an ordinary, active human principal.
	FixtureActivePrincipalID = "example-directory-principal-active"
	// FixtureSuspendedPrincipalID is deactivated, and still present in the
	// generated ListPrincipals — never omitted.
	FixtureSuspendedPrincipalID = "example-directory-principal-suspended"
	// FixtureMachinePrincipalID is a non-human identity.
	FixtureMachinePrincipalID = "example-directory-principal-machine"
	// FixtureMinHostVersion is the min_host_version the generated
	// connector.yml (and main.go's matching constant) declare.
	FixtureMinHostVersion = "0.1.0"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// nameRE restricts Options.Name to a plain identifier-shaped string: it
// becomes a YAML scalar (connector.yml's name:) and is embedded verbatim
// into generated Go doc comments, so restricting the input domain removes
// any need for a YAML or comment escaper rather than adding one.
var nameRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// moduleRE restricts Options.Module to characters a Go module path is
// actually built from, for the same reason: it is written verbatim into
// go.mod's module line.
var moduleRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

// Options are the inputs to [Generate].
type Options struct {
	// Name is the connector's name: it becomes connector.yml's `name:`
	// field and appears in generated doc comments. Lower-kebab-case is
	// conventional (e.g. "okta-roster"), and required: letters, digits,
	// hyphen and underscore only, starting with a letter.
	Name string
	// Module is the Go module path for the generated project (e.g.
	// "github.com/you/okta-roster-connector"). Required.
	Module string
}

// Validate reports whether o is usable by [Generate]. Generate calls it, so
// a caller need not call it separately — it is exported so a CLI can surface
// a usage error before attempting any I/O.
func (o Options) Validate() error {
	if o.Name == "" {
		return errors.New("scaffold: Name is required")
	}
	if !nameRE.MatchString(o.Name) {
		return fmt.Errorf("scaffold: Name %q must start with a letter and contain only letters, digits, '-' or '_'", o.Name)
	}
	if o.Module == "" {
		return errors.New("scaffold: Module is required (a Go module path, e.g. github.com/you/okta-roster-connector)")
	}
	if !moduleRE.MatchString(o.Module) {
		return fmt.Errorf("scaffold: Module %q is not a plain Go module path (letters, digits, '.', '/', '-', '_' only)", o.Module)
	}
	return nil
}

// StructName is the exported Go type name [Generate] gives the placeholder
// roster.Roster implementation: a PascalCase rendering of Name with a
// "Connector" suffix, so "okta-roster" becomes "OktaRosterConnector".
func (o Options) StructName() string {
	return pascalCase(o.Name) + "Connector"
}

// templateData is what the embedded templates render against. It is built
// once, from Options plus this package's fixed fixture identifiers, so every
// generated file reads the same values.
type templateData struct {
	Name               string
	ConnectorName      string
	Module             string
	BinaryName         string
	StructName         string
	Group              string
	NestedGroup        string
	ParentGroup        string
	EmptyGroup         string
	AbsentGroup        string
	ActivePrincipal    string
	SuspendedPrincipal string
	MachinePrincipal   string
	MinHostVersion     string
	SDKVersion         string
	GoDirective        string
}

func (o Options) data() templateData {
	return templateData{
		Name:               o.Name,
		ConnectorName:      o.Name,
		Module:             o.Module,
		BinaryName:         o.Name,
		StructName:         o.StructName(),
		Group:              FixtureGroupID,
		NestedGroup:        FixtureNestedGroupID,
		ParentGroup:        FixtureParentGroupID,
		EmptyGroup:         FixtureEmptyGroupID,
		AbsentGroup:        FixtureAbsentGroupID,
		ActivePrincipal:    FixtureActivePrincipalID,
		SuspendedPrincipal: FixtureSuspendedPrincipalID,
		MachinePrincipal:   FixtureMachinePrincipalID,
		MinHostVersion:     FixtureMinHostVersion,
		SDKVersion:         SDKVersion,
		GoDirective:        goDirective,
	}
}

// generatedFile is one (template name, output filename) pair [Generate]
// renders. Order is the order files are written, which is also the order a
// mid-write failure leaves behind — deliberately go.mod first, so a reader
// of a partial output sees the module declaration first.
var generatedFiles = []struct {
	template string
	output   string
}{
	{"go.mod.tmpl", "go.mod"},
	{"main.go.tmpl", "main.go"},
	{"connector.yml.tmpl", "connector.yml"},
	{"conform_test.go.tmpl", "conform_test.go"},
}

// Generate writes a complete, buildable Roster connector skeleton into dir.
//
// dir is created if it does not exist. If it exists, it MUST be empty:
// Generate refuses to write into a directory that already holds files,
// rather than silently overwriting or merging into someone's existing
// project.
//
// The returned error is non-nil only when generation could not be carried
// out (bad Options, an unwritable dir, ...). Generate performs no build, no
// `go mod tidy`, and no network access — it writes files, nothing else; see
// cmd/create-arqtos-connector for the CLI that also runs `go mod tidy` for
// convenience after calling this.
func Generate(dir string, opts Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	if dir == "" {
		return errors.New("scaffold: output directory is required")
	}

	if err := ensureEmptyDir(dir); err != nil {
		return err
	}

	data := opts.data()
	for _, f := range generatedFiles {
		rendered, err := render(f.template, data)
		if err != nil {
			return fmt.Errorf("scaffold: rendering %s: %w", f.template, err)
		}
		outPath := filepath.Join(dir, f.output)
		if err := os.WriteFile(outPath, rendered, 0o644); err != nil {
			return fmt.Errorf("scaffold: writing %s: %w", outPath, err)
		}
	}
	return nil
}

// ensureEmptyDir creates dir if absent, and refuses if it exists and already
// contains anything — Generate must never silently overwrite or merge into
// an existing project.
func ensureEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case err == nil:
		if len(entries) > 0 {
			return fmt.Errorf("scaffold: %s already exists and is not empty; choose an empty or new directory", dir)
		}
		return nil
	case os.IsNotExist(err):
		return os.MkdirAll(dir, 0o755)
	default:
		return fmt.Errorf("scaffold: checking %s: %w", dir, err)
	}
}

// render executes the named embedded template against data.
func render(name string, data templateData) ([]byte, error) {
	tmpl, err := template.New(name).ParseFS(templateFS, "templates/"+name)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pascalCase renders s — letters, digits, '-' and '_' — as PascalCase: each
// run of letters/digits between separators is title-cased and concatenated.
// "okta-roster" -> "OktaRoster", "okta_roster2" -> "OktaRoster2".
func pascalCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_'
	})
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}
