package arqtossdk_test

import (
	"os/exec"
	"testing"
)

// The SDK is the public IP boundary: it must never depend on arqtos-cli, or
// on any other arqtiqa module, directly or transitively. This runs the same
// gate CI runs as an explicit step (scripts/check-ip-isolation.sh), so that
// `go test ./...` alone — run by an external contributor who never sees this
// repo's Makefile or CI workflow — still enforces the boundary standalone.
//
// See the script for why both the require graph (go mod graph) and the
// import graph (go list -deps -test) are walked, why neither alone is the
// full picture, and why a go.mod grep is exactly the check that stays green
// while the graph is dirty.
func TestIPIsolation(t *testing.T) {
	out, err := exec.Command("bash", "scripts/check-ip-isolation.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("IP isolation violated:\n%s", out)
	}
}
