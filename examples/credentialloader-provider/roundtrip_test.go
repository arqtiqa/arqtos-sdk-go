package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// TestRoundTripRealSubprocess builds this example as a real binary and
// drives it as an actual host would: launch via goplugin.NewClient (a real
// subprocess, not go-plugin's in-process test harness), Dispense the
// CredentialLoader, Resolve a known ref, then Kill and confirm the
// subprocess is actually gone (dies-with-session, docs/SECURITY.md).
//
// The in-process round-trip in plugin/plugin_test.go (Task 3) is the
// always-on guarantee — this test attempts the real subprocess path and
// skips gracefully (never fails the suite) if building or launching a
// subprocess isn't possible in the current test environment.
func TestRoundTripRealSubprocess(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "credentialloader-provider")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("skipping real-subprocess round-trip: go build failed in this test env: %v\n%s", err, out)
	}

	cmd := exec.Command(binPath)
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: plugin.Handshake,
		// Client side: no Impl (only a host dials in; GRPCClient is what
		// gets exercised, never GRPCServer) — see plugin.PluginMap's doc.
		Plugins:          plugin.PluginMap(nil),
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
	})
	t.Cleanup(client.Kill) // idempotent; guarantees teardown even on t.Fatal/t.Skip below

	rpcClient, err := client.Client()
	if err != nil {
		t.Skipf("skipping real-subprocess round-trip: could not launch/dial the provider subprocess in this test env: %v", err)
	}

	raw, err := rpcClient.Dispense(plugin.CredentialLoaderName)
	if err != nil {
		t.Fatalf("Dispense(%q): %v", plugin.CredentialLoaderName, err)
	}
	loader, ok := raw.(credential.CredentialLoader)
	if !ok {
		t.Fatalf("dispensed value %T does not implement credential.CredentialLoader", raw)
	}

	r, err := ref.Parse(referenceRef)
	if err != nil {
		t.Fatalf("ref.Parse(%q): %v", referenceRef, err)
	}

	mat, err := loader.Resolve(context.Background(), r)
	if err != nil {
		t.Fatalf("Resolve over the real subprocess: %v", err)
	}
	if got := string(mat.Reveal()); got != referencePlaceholder {
		t.Fatalf("Reveal() = %q, want %q", got, referencePlaceholder)
	}
	if formatted := fmt.Sprintf("%v", mat); strings.Contains(formatted, referencePlaceholder) {
		t.Fatalf("formatted material leaked the placeholder value: %q", formatted)
	}

	// cmd.Process is populated once client.Client() has started the
	// subprocess (go-plugin starts the *exec.Cmd we constructed above in
	// place, so this is the real child PID).
	if cmd.Process == nil {
		t.Fatal("cmd.Process is nil after a successful Client() dial")
	}
	pid := cmd.Process.Pid

	client.Kill()

	if !processGone(pid) {
		t.Fatalf("provider subprocess (pid %d) still alive after client.Kill() — dies-with-session violated", pid)
	}
}

// processGone polls briefly for pid to stop existing. client.Kill() already
// waits (gracefully, then forcefully) for the child to exit before
// returning, so this loop is a confirmation rather than the primary wait —
// the short poll only guards against any final OS-level reaping delay.
func processGone(pid int) bool {
	for i := 0; i < 50; i++ {
		if err := syscall.Kill(pid, 0); err != nil {
			// ESRCH: no such process. EPERM would mean a *different*
			// process now holds this pid and we lack permission to signal
			// it — either way, the original child is gone.
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
