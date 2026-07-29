package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

func validOptions() Options {
	return Options{Name: "okta-roster", Module: "github.com/example/okta-roster-connector"}
}

func TestOptionsValidate(t *testing.T) {
	cases := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{"valid", validOptions(), false},
		{"empty name", Options{Name: "", Module: "github.com/example/x"}, true},
		{"empty module", Options{Name: "okta-roster", Module: ""}, true},
		{"name with space", Options{Name: "okta roster", Module: "github.com/example/x"}, true},
		{"name starting with digit", Options{Name: "2okta", Module: "github.com/example/x"}, true},
		{"name with slash", Options{Name: "okta/roster", Module: "github.com/example/x"}, true},
		{"module with space", Options{Name: "okta-roster", Module: "github.com/ example/x"}, true},
		{"module with quote", Options{Name: "okta-roster", Module: `github.com/"example/x`}, true},
		{"single word module", Options{Name: "okta-roster", Module: "oktaroster"}, false},
		{"underscored name", Options{Name: "okta_roster", Module: "github.com/example/x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestStructNamePascalCase(t *testing.T) {
	cases := map[string]string{
		"okta-roster":  "OktaRosterConnector",
		"okta_roster2": "OktaRoster2Connector",
		"entra":        "EntraConnector",
	}
	for name, want := range cases {
		got := Options{Name: name}.StructName()
		if got != want {
			t.Errorf("Options{Name: %q}.StructName() = %q, want %q", name, got, want)
		}
	}
}

func TestGenerateWritesExpectedFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "conn")
	if err := Generate(target, validOptions()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := []string{"go.mod", "main.go", "connector.yml", "conform_test.go"}
	for _, f := range want {
		p := filepath.Join(target, f)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s is empty", p)
		}
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != len(want) {
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Fatalf("Generate wrote %v, want exactly %v", got, want)
	}
}

func TestGenerateCreatesDirIfAbsent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "does", "not", "exist", "yet")
	if err := Generate(target, validOptions()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "main.go")); err != nil {
		t.Fatalf("Generate did not create the nested directory: %v", err)
	}
}

func TestGenerateRefusesNonEmptyExistingDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep-me.txt"), []byte("pre-existing"), 0o644); err != nil {
		t.Fatalf("seeding dir: %v", err)
	}
	err := Generate(dir, validOptions())
	if err == nil {
		t.Fatal("Generate into a non-empty directory must fail, not overwrite")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "keep-me.txt" {
		t.Fatalf("a refused Generate must not have touched the directory, got entries %v", entries)
	}
}

func TestGenerateRejectsInvalidOptions(t *testing.T) {
	dir := t.TempDir()
	if err := Generate(filepath.Join(dir, "x"), Options{}); err == nil {
		t.Fatal("Generate with empty Options must fail")
	}
}

// TestGenerateReportsWriteFailure proves Generate propagates a real I/O
// failure rather than silently succeeding: an unwritable target directory
// must fail Generate, not produce a partially-written project reported as
// success.
func TestGenerateReportsWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not block writes")
	}
	parent := t.TempDir()
	target := filepath.Join(parent, "conn")
	if err := os.Mkdir(target, 0o500); err != nil { // r-x: readable/listable, not writable
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o700) }) // let t.TempDir clean up
	err := Generate(target, validOptions())
	if err == nil {
		t.Fatal("Generate into an unwritable directory must fail, not silently succeed")
	}
}

// TestGenerateRefusesWhenTargetIsAFile covers ensureEmptyDir's non-NotExist
// error path: a path that exists but is not a directory at all.
func TestGenerateRefusesWhenTargetIsAFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}
	if err := Generate(target, validOptions()); err == nil {
		t.Fatal("Generate must refuse a target path that is a file, not a directory")
	}
}

// TestGeneratedGoModPinsSDKVersion is the constraint the story exists to
// enforce: the generated go.mod pins arqtos-sdk-go at the tag carrying the
// Roster wire protocol (v0.2.0), not the prior tag (v0.1.1) that lacks it —
// and carries no GOPRIVATE / credential setup, because arqtos-sdk-go is
// public and needs none.
func TestGeneratedGoModPinsSDKVersion(t *testing.T) {
	dir := t.TempDir()
	if err := Generate(dir, validOptions()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	mod := string(b)
	if !strings.Contains(mod, "github.com/arqtiqa/arqtos-sdk-go v0.2.0") {
		t.Fatalf("go.mod does not pin arqtos-sdk-go v0.2.0:\n%s", mod)
	}
	if strings.Contains(mod, "v0.1.1") || strings.Contains(mod, "v0.1.0") {
		t.Fatalf("go.mod pins a pre-Roster-wire-protocol SDK tag:\n%s", mod)
	}
	if strings.Contains(mod, "replace") {
		t.Fatalf("go.mod must not carry a replace directive — a real consumer has none:\n%s", mod)
	}
	if strings.Contains(mod, "module "+validOptions().Module) == false {
		t.Fatalf("go.mod does not declare the requested module path:\n%s", mod)
	}
}

// TestGeneratedProjectNamesNoPrivateSetup covers the "arqtos-sdk-go is
// public, emit no GOPRIVATE/credential setup" constraint across every
// generated file, not just go.mod.
func TestGeneratedProjectNamesNoPrivateSetup(t *testing.T) {
	dir := t.TempDir()
	if err := Generate(dir, validOptions()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	forbidden := []string{"GOPRIVATE", "GONOSUMCHECK", "GOINSECURE", "op://", "arqtos-cli"}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		for _, bad := range forbidden {
			if strings.Contains(string(b), bad) {
				t.Fatalf("%s contains forbidden string %q; arqtos-sdk-go is public and a connector must never import "+
					"the host — see the story's constraints", e.Name(), bad)
			}
		}
	}
}

// TestGeneratedManifestValidatesAndExcludesWatch proves connector.yml is a
// real, parseable manifest.Doc that validates against the Roster class, and
// that it never declares CapWatch — CapWatch has no RPC on the Track-B wire,
// so an out-of-process provider that declared it would fail
// rosterconform's watch/declared-is-implemented check the moment anyone ran it.
func TestGeneratedManifestValidatesAndExcludesWatch(t *testing.T) {
	dir := t.TempDir()
	opts := validOptions()
	if err := Generate(dir, opts); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "connector.yml"))
	if err != nil {
		t.Fatalf("reading connector.yml: %v", err)
	}
	doc, err := manifest.Parse(b)
	if err != nil {
		t.Fatalf("connector.yml does not parse: %v", err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("connector.yml does not validate: %v", err)
	}
	if doc.Name != opts.Name {
		t.Fatalf("connector.yml name = %q, want %q", doc.Name, opts.Name)
	}
	if doc.Implements != connector.ClassRoster {
		t.Fatalf("connector.yml implements = %q, want %q", doc.Implements, connector.ClassRoster)
	}
	if doc.Kind != manifest.KindProvider {
		t.Fatalf("connector.yml kind = %q, want %q", doc.Kind, manifest.KindProvider)
	}
	if doc.MinHostVersion == "" {
		t.Fatal("connector.yml must declare min_host_version for kind: provider")
	}
	if doc.Declares(roster.CapWatch) {
		t.Fatal("the generated connector.yml declares watch, which has no RPC on the Track-B wire and can never be honoured out-of-process")
	}
}

// TestGeneratedGoFilesAreGofmtClean guards the newcomer's first impression:
// a scaffold that produced unformatted Go would fail `gofmt -l` in the
// generated project's own CI on the very first commit.
func TestGeneratedGoFilesAreGofmtClean(t *testing.T) {
	gofmtPath, err := exec.LookPath("gofmt")
	if err != nil {
		t.Skip("gofmt not on PATH")
	}
	dir := t.TempDir()
	if err := Generate(dir, validOptions()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range []string{"main.go", "conform_test.go"} {
		out, err := exec.Command(gofmtPath, "-l", filepath.Join(dir, f)).CombinedOutput()
		if err != nil {
			t.Fatalf("gofmt -l %s: %v\n%s", f, err, out)
		}
		if len(out) != 0 {
			t.Fatalf("%s is not gofmt-clean:\n%s", f, out)
		}
	}
}

// TestGenerateIsDeterministic proves two runs against the same Options
// produce byte-identical output — a scaffolder whose output varied run to
// run would be unreviewable and undiffable in the connector's own git history.
func TestGenerateIsDeterministic(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	opts := validOptions()
	if err := Generate(filepath.Join(dir1, "c"), opts); err != nil {
		t.Fatalf("Generate #1: %v", err)
	}
	if err := Generate(filepath.Join(dir2, "c"), opts); err != nil {
		t.Fatalf("Generate #2: %v", err)
	}
	for _, f := range []string{"go.mod", "main.go", "connector.yml", "conform_test.go"} {
		a, err := os.ReadFile(filepath.Join(dir1, "c", f))
		if err != nil {
			t.Fatalf("reading run #1 %s: %v", f, err)
		}
		b, err := os.ReadFile(filepath.Join(dir2, "c", f))
		if err != nil {
			t.Fatalf("reading run #2 %s: %v", f, err)
		}
		if string(a) != string(b) {
			t.Fatalf("%s differs between two Generate runs with identical Options", f)
		}
	}
}
