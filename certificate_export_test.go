package arqtossdk_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCertificateServiceHasNoExport is the mutation named by arqtos-sdk-go#138:
// adding an Export RPC to certificate.proto must turn this red.
func TestCertificateServiceHasNoExport(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("proto", "connector", "v1", "certificate.proto"))
	if err != nil {
		t.Fatalf("read certificate.proto: %v", err)
	}
	text := string(body)
	rpc := regexp.MustCompile(`(?m)^\s*rpc\s+(\w+)\s*\(`)
	var names []string
	for _, m := range rpc.FindAllStringSubmatch(text, -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		t.Fatal("no rpc names found")
	}
	want := []string{"Sign", "Verify", "PublicKey", "Health", "Capabilities", "Close"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("Certificate RPCs = %v, want %v", names, want)
	}
	for _, n := range names {
		if strings.EqualFold(n, "Export") {
			t.Fatalf("Certificate service carries Export; private key material must not leave")
		}
	}
}
