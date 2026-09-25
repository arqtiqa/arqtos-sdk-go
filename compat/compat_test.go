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
	got := slices.Clone(compat.Line5Runtime())
	published := slices.Clone(connector.Classes())
	slices.Sort(got)
	slices.Sort(want)
	slices.Sort(published)
	if !slices.Equal(got, want) {
		t.Fatalf("Line5Runtime() = %v, want %v", got, want)
	}
	if !slices.Equal(got, published) {
		t.Fatalf("Line5Runtime() = %v, Classes() = %v", got, published)
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

func TestCheckTagFreeze_MovedSourceRefused(t *testing.T) {
	err := compat.CheckTagFreeze("v0.5.0", "aaa", "bbb", "")
	if err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("moved source: %v", err)
	}
}

func TestCheckTagFreeze_ExistingTagElsewhereRefused(t *testing.T) {
	err := compat.CheckTagFreeze("v0.5.0", "aaa", "aaa", "bbb")
	if err == nil || !strings.Contains(err.Error(), "already points") {
		t.Fatalf("existing tag: %v", err)
	}
}

func TestCheckTagFreeze_MatchingOriginAccepted(t *testing.T) {
	if err := compat.CheckTagFreeze("v0.5.0", "aaa", "aaa", ""); err != nil {
		t.Fatal(err)
	}
	if err := compat.CheckTagFreeze("v0.5.0", "aaa", "aaa", "aaa"); err != nil {
		t.Fatal(err)
	}
}

func TestConsumer_RequiresStableModuleVersion(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "consumer", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	want := compat.ModulePath + " " + compat.ModuleVersion
	if !strings.Contains(string(b), want) {
		t.Fatalf("consumer go.mod does not pin %s:\n%s", want, b)
	}
	if strings.Contains(string(b), "alpha") {
		t.Fatalf("consumer still pins an alpha: %s", b)
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
