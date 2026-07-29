package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
	"github.com/arqtiqa/arqtos-sdk-go/rosterconform"
)

// hostContractVersion is the contract version the "host" in these tests
// implements. It is newer than this provider's referenceMinHostVersion, so the
// negotiation passes — and TestAProviderThatNeedsANewerHostIsRefused below
// drives the other direction.
const hostContractVersion = "0.4.0"

// buildProvider builds this example as a real binary and returns its path.
// It SKIPS rather than fails when the environment cannot build, so a runner
// without a working toolchain does not turn into a red suite — the in-process
// runs in conform_test.go are the always-on guarantee.
func buildProvider(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "roster-provider")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("skipping real-subprocess test: go build failed in this test env: %v\n%s", err, out)
	}
	return binPath
}

// TestConformantOutOfProcess is the test the whole Track-B wire exists to make
// possible: the reference provider runs as a SEPARATE PROCESS, a host dials it
// over go-plugin/gRPC, and every rosterconform check runs across that real
// boundary.
//
// This is the run a connector shipped as a provider has to pass. The in-process
// run in conform_test.go cannot see a marshalling bug, because in-process
// nothing is marshalled.
func TestConformantOutOfProcess(t *testing.T) {
	binPath := buildProvider(t)

	rep, err := rosterconform.RunOutOfProcess(context.Background(), rosterconform.Provider{
		Path:        binPath,
		HostVersion: hostContractVersion,
	}, referenceOptions())
	if err != nil {
		// Launching a subprocess is not possible in every environment; the
		// harness classifies that as Unavailable, and only that is skippable.
		if cerr.KindOf(err) == cerr.KindUnavailable {
			t.Skipf("skipping real-subprocess conformance: could not launch/dial the provider here: %v", err)
		}
		t.Fatalf("rosterconform.RunOutOfProcess could not be carried out: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("the reference provider must be conformant OUT OF PROCESS, not only in-process:\n%s", rep)
	}
	// A green report that never crossed a wire would prove nothing about the
	// wire, so the report has to say which it was.
	if !strings.Contains(rep.Transport, rosterconform.TransportOutOfProcess) {
		t.Fatalf("Transport = %q, want it to record %q", rep.Transport, rosterconform.TransportOutOfProcess)
	}
	// Green because the checks RAN, not because the report was empty.
	if len(rep.Results) == 0 {
		t.Fatalf("the out-of-process report ran no checks:\n%s", rep)
	}
}

// TestAProviderThatNeedsANewerHostIsRefused drives the min_host_version
// negotiation against a REAL subprocess: the provider is launched, and the host
// then refuses to dispense it because the manifest requires more than the host
// implements.
//
// The refusal is an error rather than a red report on purpose. A provider that
// was never dialled has demonstrated nothing about its conformance, and a
// report full of failures would blame it for a pairing the host correctly
// declined.
func TestAProviderThatNeedsANewerHostIsRefused(t *testing.T) {
	binPath := buildProvider(t)

	opts := referenceOptions()
	opts.Manifest.MinHostVersion = "99.0.0"

	rep, err := rosterconform.RunOutOfProcess(context.Background(), rosterconform.Provider{
		Path:        binPath,
		HostVersion: hostContractVersion,
	}, opts)
	if err == nil {
		t.Fatalf("a provider requiring host 99.0.0 must be refused by a %s host; got report:\n%s",
			hostContractVersion, rep)
	}
	if cerr.KindOf(err) == cerr.KindUnavailable {
		t.Skipf("skipping: could not launch the provider subprocess here: %v", err)
	}
	if got := cerr.KindOf(err); got != cerr.KindUnsupported {
		t.Fatalf("KindOf = %v, want %v: %v", got, cerr.KindUnsupported, err)
	}
	if !strings.Contains(err.Error(), "99.0.0") {
		t.Fatalf("the refusal must name the version required: %v", err)
	}
	if len(rep.Results) != 0 {
		t.Fatalf("a refused provider must report no checks, got %d", len(rep.Results))
	}
}

// TestHostDialAndTeardown drives the host-side API directly — the wiring a real
// host writes, rather than the harness's convenience wrapper — and then
// confirms the provider process is actually gone after Kill
// (dies-with-session, docs/SECURITY.md).
func TestHostDialAndTeardown(t *testing.T) {
	binPath := buildProvider(t)
	m := referenceManifest()

	cmd := exec.Command(binPath)
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: plugin.Handshake,
		// The HOST-side map: it carries the manifest the host read and the
		// version the host implements, which is what Dispense negotiates on.
		Plugins:          plugin.RosterHostPluginMap(m.Name, m, hostContractVersion),
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
	})
	t.Cleanup(client.Kill) // idempotent; guarantees teardown even on t.Fatal/t.Skip

	rpcClient, err := client.Client()
	if err != nil {
		t.Skipf("skipping: could not launch/dial the provider subprocess here: %v", err)
	}

	raw, err := rpcClient.Dispense(plugin.RosterName)
	if err != nil {
		t.Fatalf("Dispense(%q): %v", plugin.RosterName, err)
	}
	r, ok := raw.(roster.Roster)
	if !ok {
		t.Fatalf("dispensed value %T does not implement roster.Roster", raw)
	}

	// One read of each shape, over the real boundary.
	res, err := r.ListPrincipals(context.Background())
	if err != nil {
		t.Fatalf("ListPrincipals over the subprocess: %v", err)
	}
	people, err := res.Items()
	if err != nil {
		t.Fatalf("reading principals: %v", err)
	}
	var suspended bool
	for _, p := range people {
		if p.ID == referenceSuspended && !p.Active {
			suspended = true
		}
	}
	if !suspended {
		t.Fatalf("the suspended principal did not survive a real subprocess round trip: %+v", people)
	}

	empty, err := r.ListMemberships(context.Background(), referenceEmptyGroup)
	if err != nil {
		t.Fatalf("an existing empty group must resolve over the wire: %v", err)
	}
	if items, ierr := empty.Items(); ierr != nil || len(items) != 0 {
		t.Fatalf("an asserted-empty group must stay readable and empty across a subprocess: %v %v", items, ierr)
	}

	if _, err := r.ListMemberships(context.Background(), referenceAbsentGroup); cerr.KindOf(err) != cerr.KindNotFound {
		t.Fatalf("an absent group must fail NotFound across a subprocess, got %v (%v)", cerr.KindOf(err), err)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close over the subprocess: %v", err)
	}

	// cmd.Process is populated once client.Client() has started the
	// subprocess, so this is the real child PID.
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
// waits for the child to exit before returning, so this is a confirmation
// rather than the primary wait — the short poll only guards against a final
// OS-level reaping delay.
func processGone(pid int) bool {
	for i := 0; i < 50; i++ {
		if err := syscall.Kill(pid, 0); err != nil {
			// ESRCH: no such process. EPERM would mean a different process now
			// holds this pid — either way the original child is gone.
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
