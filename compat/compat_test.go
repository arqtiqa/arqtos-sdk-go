package compat_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/compat"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/contentrelease"
	"github.com/arqtiqa/arqtos-sdk-go/orghome"
)

func TestLine5Runtime_MatchesPublishedClasses(t *testing.T) {
	want := []connector.Class{
		connector.ClassCredentialLoader,
		connector.ClassRoster,
		connector.ClassCodeCI,
		connector.ClassTracker,
		connector.ClassAuthenticator,
		connector.ClassCodeHost,
		connector.ClassSearch,
		connector.ClassCertificate,
	}
	got := compat.Line5Runtime()
	if !slices.Equal(got, want) {
		t.Fatalf("Line5Runtime() = %v, want %v", got, want)
	}
	if !slices.Equal(got, connector.Classes()) {
		t.Fatalf("Line5Runtime() = %v, Classes() = %v", got, connector.Classes())
	}
}

func TestLine5Runtime_IncludesContentReleaseAndOrgHome(t *testing.T) {
	if contentrelease.KindContentRelease != "content-release" {
		t.Fatalf("contentrelease.KindContentRelease = %q", contentrelease.KindContentRelease)
	}
	if orghome.KindOrgAdoption != "org-adoption" {
		t.Fatalf("orghome kind = %q", orghome.KindOrgAdoption)
	}
}

func TestConsumer_ResolvesThisModuleViaReplace(t *testing.T) {
	dir := filepath.Join("testdata", "consumer")
	cmd := exec.Command("go", "list", "-m", compat.ModulePath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("consumer go list: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "arqtos-sdk-go") {
		t.Fatalf("consumer list = %q", out)
	}
}

func TestConsumer_UntrackedVersionFails(t *testing.T) {
	dir := filepath.Join("testdata", "incompatible")
	cmd := exec.Command("go", "list", "-m", compat.ModulePath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOPRIVATE=github.com/arqtiqa/*")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("incompatible pin resolved: %s", out)
	}
	if !strings.Contains(strings.ToLower(string(out)+err.Error()), "v0.4.0") && !strings.Contains(strings.ToLower(string(out)), "module") {
		t.Fatalf("incompatible error = %v\n%s", err, out)
	}
}
