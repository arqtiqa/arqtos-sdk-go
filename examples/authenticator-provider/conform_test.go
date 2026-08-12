package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/authconform"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

// referenceMinHostVersion is what this provider's manifest would declare. The
// host version used below is newer, so the negotiation passes;
// TestAProviderThatNeedsANewerHostIsRefused drives the other direction.
const (
	referenceMinHostVersion = "0.4.0"
	hostContractVersion     = "0.5.0"
)

func referenceManifest() manifest.Doc {
	return manifest.Doc{
		Name:           "authenticator-provider",
		Implements:     connector.ClassAuthenticator,
		Kind:           manifest.KindProvider,
		MinHostVersion: referenceMinHostVersion,
	}
}

func referenceOptions() authconform.Options {
	return authconform.Options{
		Manifest:            referenceManifest(),
		ValidCode:           ValidCode,
		RejectedCode:        RejectedCode,
		InactiveCode:        InactiveCode,
		UnknownHandle:       "placeholder-handle-never-issued",
		ExpectedPrincipalID: PrincipalID,
	}
}

// TestReferenceProviderIsConformantInProcess is the always-on guarantee: it
// needs no toolchain and no subprocess, so it runs everywhere.
//
// ⚠️ It is NOT evidence the wire works. Its report records "transport not
// recorded" precisely so a green run here cannot be mistaken for one.
func TestReferenceProviderIsConformantInProcess(t *testing.T) {
	rep, err := authconform.Run(context.Background(), newMemAuth(), referenceOptions())
	if err != nil {
		t.Fatalf("the run could not be carried out: %v", err)
	}
	if err := rep.Err(); err != nil {
		t.Fatalf("the reference provider is not conformant:\n%s", rep)
	}
	if rep.Transport != authconform.TransportUnrecorded {
		t.Errorf("an in-process run recorded transport %q; it cannot know what is behind the interface "+
			"and must say so", rep.Transport)
	}
}

// buildProvider builds this example as a real binary and returns its path. It
// SKIPS rather than fails when the environment cannot build, so a runner
// without a working toolchain does not turn into a red suite — the in-process
// run above is the always-on guarantee.
func buildProvider(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "authenticator-provider")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("skipping real-subprocess test: go build failed in this test env: %v\n%s", err, out)
	}
	return binPath
}

// TestConformantOutOfProcess is the test the whole Track-B wire exists to make
// possible: the reference provider runs as a SEPARATE PROCESS, a host dials it
// over go-plugin/gRPC, and every authconform check runs across that real
// boundary.
//
// For this class the wire is where the dangerous failures live, because proto3
// omits a false bool and an empty string entirely: a verified-but-disabled
// principal losing its Active flag, and a rejection arriving as a
// perfectly-shaped anonymous assertion, are both invisible to an in-process run
// of the same code.
func TestConformantOutOfProcess(t *testing.T) {
	rep, err := authconform.RunOutOfProcess(context.Background(),
		authconform.Provider{Path: buildProvider(t), HostVersion: hostContractVersion},
		referenceOptions())
	if err != nil {
		t.Fatalf("the out-of-process run could not be carried out: %v", err)
	}
	if err := rep.Err(); err != nil {
		t.Fatalf("the reference provider is not conformant across the wire:\n%s", rep)
	}
	if !strings.Contains(rep.Transport, authconform.TransportOutOfProcess) {
		t.Errorf("transport = %q, want it to record the out-of-process run — a report that cannot say the "+
			"wire was exercised is not evidence that it was", rep.Transport)
	}
}

// TestAProviderThatNeedsANewerHostIsRefused drives the negotiation's other
// direction across a real dial.
//
// ⚠️ The refusal is an ERROR from the run, not a Report full of failures. A
// provider the host refused was never dialled, so there is nothing to report
// checks about, and a report of failures would misattribute a correct refusal
// to broken behaviour.
func TestAProviderThatNeedsANewerHostIsRefused(t *testing.T) {
	opts := referenceOptions()
	opts.Manifest.MinHostVersion = "9.0.0"

	rep, err := authconform.RunOutOfProcess(context.Background(),
		authconform.Provider{Path: buildProvider(t), HostVersion: hostContractVersion},
		opts)
	if err == nil {
		t.Fatal("a provider requiring a newer host was dialled anyway")
	}
	if len(rep.Results) != 0 {
		t.Errorf("a refused pairing produced %d check results; it was never dialled, so there is nothing "+
			"to report", len(rep.Results))
	}
}

// TestRunOutOfProcessRefusesAnIncompleteRequest: both fields are load-bearing,
// and an omitted host version would silently skip the negotiation.
func TestRunOutOfProcessRefusesAnIncompleteRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    authconform.Provider
	}{
		{"no path", authconform.Provider{HostVersion: hostContractVersion}},
		{"no host version", authconform.Provider{Path: "/nonexistent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := authconform.RunOutOfProcess(context.Background(), tc.p, referenceOptions()); err == nil {
				t.Fatal("the run was carried out")
			}
		})
	}
}
