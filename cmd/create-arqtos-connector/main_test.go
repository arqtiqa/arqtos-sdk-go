package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noNetworkTidy stands in for a real `go mod tidy` in fast unit tests: it
// never touches the network. Its two behaviours (ok / fails) are picked by
// the test, so run's success/failure reporting is exercised without
// depending on this environment having network access — the real
// network-touching path is proven separately by scaffold's own
// out-of-process test, which runs an actual `go mod tidy` against the
// public arqtos-sdk-go module.
func noNetworkTidy(result []byte, err error) tidyFunc {
	return func(string) ([]byte, error) { return result, err }
}

func TestRunWritesSkeletonAndReportsNextSteps(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "okta-roster-connector")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-name", "okta-roster",
		"-module", "github.com/example/okta-roster-connector",
		"-out", out,
	}, &stdout, &stderr, noNetworkTidy([]byte("go: added github.com/arqtiqa/arqtos-sdk-go v0.2.0"), nil))

	if code != 0 {
		t.Fatalf("run() = %d, stderr:\n%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(out, "main.go")); err != nil {
		t.Fatalf("expected main.go to exist: %v", err)
	}
	if !strings.Contains(stdout.String(), out) {
		t.Fatalf("stdout does not mention the output directory:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "go build ./...") || !strings.Contains(stdout.String(), "go test ./...") {
		t.Fatalf("stdout does not tell the newcomer what to run next:\n%s", stdout.String())
	}
}

func TestRunDefaultsOutDirFromName(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-name", "okta-roster",
		"-module", "github.com/example/okta-roster-connector",
	}, &stdout, &stderr, noNetworkTidy(nil, nil))

	if code != 0 {
		t.Fatalf("run() = %d, stderr:\n%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "okta-roster-connector", "main.go")); err != nil {
		t.Fatalf("expected ./okta-roster-connector/main.go, got: %v", err)
	}
}

func TestRunReportsGenerateFailureWithoutPanicking(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-name", "", // invalid: Options.Validate rejects an empty name
		"-module", "github.com/example/x",
	}, &stdout, &stderr, noNetworkTidy(nil, nil))

	if code == 0 {
		t.Fatalf("run() with an invalid name must not report success; stdout:\n%s", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("run() with an invalid name must explain why on stderr")
	}
}

func TestRunSurvivesTidyFailureAndStillReportsNextSteps(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "conn")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-name", "okta-roster",
		"-module", "github.com/example/okta-roster-connector",
		"-out", out,
	}, &stdout, &stderr, noNetworkTidy(nil, &fakeExecError{"no network"}))

	// Generation itself must still be reported as a success — a failed `go
	// mod tidy` (e.g. no network right now) must not make the CLI claim the
	// skeleton was never written, and must not stop it telling the newcomer
	// what to run themselves.
	if code != 0 {
		t.Fatalf("run() = %d, want 0 (files were written; only the convenience tidy step failed); stderr:\n%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(out, "go.mod")); err != nil {
		t.Fatalf("expected go.mod to exist even though tidy failed: %v", err)
	}
	if !strings.Contains(stderr.String(), "go mod tidy") {
		t.Fatalf("stderr does not explain that tidy failed and must be re-run:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "go build ./...") {
		t.Fatalf("stdout must still show next steps even when tidy failed:\n%s", stdout.String())
	}
}

type fakeExecError struct{ msg string }

func (e *fakeExecError) Error() string { return e.msg }
