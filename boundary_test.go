package arqtossdk_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The SDK is the public IP boundary: it must never depend on arqtos-cli.
func TestNoArqtosCLIDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "arqtiqa/arqtos-cli") {
		t.Fatalf("arqtos-sdk-go must not import arqtiqa/arqtos-cli (IP boundary)")
	}
}
