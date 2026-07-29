package scaffold

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
	"github.com/arqtiqa/arqtos-sdk-go/rosterconform"
)

// hostContractVersion is the contract version the "host" in these tests
// implements — newer than the generated connector.yml's min_host_version, so
// the negotiation passes.
const hostContractVersion = "0.4.0"

// generateAndTidy generates a connector into a fresh subdirectory of t.TempDir,
// then runs `go mod tidy` in it — required because arqtos-sdk-go is a real
// dependency the generated go.mod only PINS a version for; go.sum has to be
// populated before `go build` will succeed. This is exactly what a newcomer's
// first `go mod tidy` (or `go build`, which the Go toolchain nudges towards
// the same command for) does; it is not a workaround specific to this test.
func generateAndTidy(t *testing.T, opts Options) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "conn")
	if err := Generate(dir, opts); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("skipping: `go mod tidy` failed in this environment (likely no network to fetch arqtos-sdk-go@%s): %v\n%s",
			SDKVersion, err, out)
	}
	return dir
}

// buildConnector builds the generated project's main package into a binary
// and returns its path. It skips, rather than fails, when the environment
// cannot build — the in-process conformance test the generated project ships
// (conform_test.go) is the always-on guarantee; this one additionally proves
// the SAME output is buildable and conformant OUT OF PROCESS, on a runner
// that has a toolchain and network.
func buildConnector(t *testing.T, dir string) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "connector-provider")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("skipping: `go build` failed in this test env: %v\n%s", err, out)
	}
	return binPath
}

func loadManifest(t *testing.T, dir string) manifest.Doc {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "connector.yml"))
	if err != nil {
		t.Fatalf("reading connector.yml: %v", err)
	}
	doc, err := manifest.Parse(b)
	if err != nil {
		t.Fatalf("connector.yml does not parse: %v", err)
	}
	return doc
}

func fixtureOptions(doc manifest.Doc) rosterconform.Options {
	return rosterconform.Options{
		Manifest:           doc,
		Group:              FixtureGroupID,
		AbsentGroup:        FixtureAbsentGroupID,
		SuspendedPrincipal: FixtureSuspendedPrincipalID,
	}
}

// TestScaffoldedConnectorBuildsAndConformsOutOfProcess is the story's
// end-to-end acceptance test: scaffold a connector into a temp dir, build
// it, and run rosterconform AGAINST THE SPAWNED BINARY — the out-of-process
// gate a real third-party provider is measured against. A scaffolder whose
// output was never run is not verified; this is what runs it.
func TestScaffoldedConnectorBuildsAndConformsOutOfProcess(t *testing.T) {
	dir := generateAndTidy(t, validOptions())
	binPath := buildConnector(t, dir)
	doc := loadManifest(t, dir)

	rep, err := rosterconform.RunOutOfProcess(context.Background(), rosterconform.Provider{
		Path:        binPath,
		HostVersion: hostContractVersion,
	}, fixtureOptions(doc))
	if err != nil {
		if cerr.KindOf(err) == cerr.KindUnavailable {
			t.Skipf("skipping real-subprocess conformance: could not launch/dial the provider here: %v", err)
		}
		t.Fatalf("rosterconform.RunOutOfProcess could not be carried out: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("a freshly generated connector, before any real logic is written, must already be conformant OUT OF PROCESS:\n%s", rep)
	}
	if !strings.Contains(rep.Transport, rosterconform.TransportOutOfProcess) {
		t.Fatalf("Transport = %q, want it to record %q", rep.Transport, rosterconform.TransportOutOfProcess)
	}
	if len(rep.Results) == 0 {
		t.Fatalf("the out-of-process report ran no checks:\n%s", rep)
	}
	t.Logf("scaffold -> build -> rosterconform.RunOutOfProcess:\n%s", rep)
}

// TestScaffoldedConnectorInProcessTestPasses builds and runs the generated
// project's OWN test suite (`go test ./...`) exactly as a newcomer would,
// proving conform_test.go — the fast in-process signal the generated project
// ships — is itself green on generation.
func TestScaffoldedConnectorInProcessTestPasses(t *testing.T) {
	dir := generateAndTidy(t, validOptions())
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`go test ./...` failed in the freshly generated connector:\n%s", out)
	}
	t.Logf("generated project `go test ./...`:\n%s", out)
}

// TestDishonestManifestFailsOutOfProcessConformance is the "tests must bite"
// proof for mistake #2 (Capabilities() honesty): a connector.yml hand-edited
// to drop a capability the running binary still reports MUST fail
// rosterconform, not pass it. If this test ever went green, the
// capability-honesty check the generated skeleton exists to demonstrate
// would have stopped meaning anything.
func TestDishonestManifestFailsOutOfProcessConformance(t *testing.T) {
	dir := generateAndTidy(t, validOptions())
	binPath := buildConnector(t, dir)
	doc := loadManifest(t, dir)

	// The generated main.go's Capabilities() reports BOTH
	// transitive_membership and machine_principals (see its template). Drop
	// the second from the manifest ONLY — exactly the mistake of editing
	// connector.yml without touching the code that backs it.
	if len(doc.Capabilities) != 2 {
		t.Fatalf("expected the generated manifest to declare exactly 2 capabilities, got %v", doc.Capabilities)
	}
	doc.Capabilities = doc.Capabilities[:1]

	rep, err := rosterconform.RunOutOfProcess(context.Background(), rosterconform.Provider{
		Path:        binPath,
		HostVersion: hostContractVersion,
	}, fixtureOptions(doc))
	if err != nil {
		if cerr.KindOf(err) == cerr.KindUnavailable {
			t.Skipf("skipping real-subprocess conformance: could not launch/dial the provider here: %v", err)
		}
		t.Fatalf("rosterconform.RunOutOfProcess could not be carried out: %v", err)
	}
	if rep.OK() {
		t.Fatalf("a manifest missing a capability the running connector still reports must FAIL rosterconform, "+
			"not pass it — the capability-honesty check has stopped biting:\n%s", rep)
	}
	failed := rep.Failures()
	var got string
	for _, f := range failed {
		if f.Name == rosterconform.CheckCapabilityHonesty {
			got = f.Detail
		}
	}
	if got == "" {
		t.Fatalf("expected %s to be among the failures, got:\n%s", rosterconform.CheckCapabilityHonesty, rep)
	}
	t.Logf("observed failure (capability dishonesty): %s: %s", rosterconform.CheckCapabilityHonesty, got)
	if !strings.Contains(got, "machine_principals") {
		t.Fatalf("failure detail does not name the undeclared capability: %s", got)
	}
}

// TestWatchDeclaredWithoutImplementationFailsConformance is the "tests must
// bite" proof for the CapWatch trap: hand-adding "watch" to connector.yml —
// exactly the mistake the package comment in main.go warns against — must
// fail rosterconform, because an out-of-process provider has no RPC to
// honour it with however its manifest reads.
func TestWatchDeclaredWithoutImplementationFailsConformance(t *testing.T) {
	dir := generateAndTidy(t, validOptions())
	binPath := buildConnector(t, dir)
	doc := loadManifest(t, dir)

	doc.Capabilities = append(doc.Capabilities, roster.CapWatch)

	rep, err := rosterconform.RunOutOfProcess(context.Background(), rosterconform.Provider{
		Path:        binPath,
		HostVersion: hostContractVersion,
	}, fixtureOptions(doc))
	if err != nil {
		if cerr.KindOf(err) == cerr.KindUnavailable {
			t.Skipf("skipping real-subprocess conformance: could not launch/dial the provider here: %v", err)
		}
		t.Fatalf("rosterconform.RunOutOfProcess could not be carried out: %v", err)
	}
	if rep.OK() {
		t.Fatalf("declaring watch on an out-of-process provider must FAIL rosterconform — there is no RPC for it "+
			"to honour, whatever the manifest says:\n%s", rep)
	}
	var got string
	for _, f := range rep.Failures() {
		if f.Name == rosterconform.CheckWatchDeclared {
			got = f.Detail
		}
	}
	if got == "" {
		t.Fatalf("expected %s to be among the failures, got:\n%s", rosterconform.CheckWatchDeclared, rep)
	}
	t.Logf("observed failure (watch declared, not implemented): %s: %s", rosterconform.CheckWatchDeclared, got)
}
