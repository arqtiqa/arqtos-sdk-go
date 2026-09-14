package credential_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

func TestInventory_IncompleteCoverageCannotBeAcquired(t *testing.T) {
	inv := credential.Inventory{
		Authority:  "sa-placeholder",
		Containers: []string{"<vault>"},
		Keys:       []ref.Ref{mustRef(t, "op://<vault>/<item>/<field>")},
		Coverage:   credential.CoverageIncomplete,
	}
	if err := inv.Validate(); !errors.Is(err, credential.ErrIncompleteInventory) {
		t.Fatalf("Validate = %v, want ErrIncompleteInventory", err)
	}
	if credential.Coverage("github").Valid() {
		t.Fatal("provider-specific coverage names must not be valid")
	}
}

func TestCompleteBundle_IncludesEveryEnrolledKeyAndDistinguishesEmptyFromMissing(t *testing.T) {
	a := mustRef(t, "op://<vault>/<item>/<field>")
	b := mustRef(t, "op://<vault>/<other-item>/<field>")
	inv := credential.Inventory{
		Authority:  "sa-placeholder",
		Containers: []string{"<vault>"},
		Keys:       []ref.Ref{a, b},
		Coverage:   credential.CoverageDeclared,
	}
	if err := inv.Validate(); err != nil {
		t.Fatal(err)
	}
	va, err := credential.Resolved(credential.NewMaterial([]byte("alpha-secret")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := credential.CompleteBundle(inv, []credential.BundleEntry{
		credential.BundleValue(a, va),
		credential.BundleStoredEmpty(b),
	}, credential.BundleMeta{Generation: "g1", Source: "fixture", AcquiredAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("CompleteBundle: %v", err)
	}
	if !got.Ready() {
		t.Fatal("a complete declared inventory must be Ready")
	}
	if got.Coverage() != credential.CoverageDeclared {
		t.Fatalf("Coverage = %q", got.Coverage())
	}
	res, ok := got.Lookup(a)
	if !ok {
		t.Fatal("missing enrolled key a")
	}
	mat, err := res.Value()
	if err != nil || string(mat.Reveal()) != "alpha-secret" {
		t.Fatalf("a = %v %v", mat, err)
	}
	empty, ok := got.Lookup(b)
	if !ok {
		t.Fatal("stored-empty enrolled key treated as missing")
	}
	emat, err := empty.Value()
	if err != nil || len(emat.Reveal()) != 0 {
		t.Fatalf("stored empty must be readable and empty, got %v %v", emat, err)
	}
}

func TestCompleteBundle_OmittingAGrantedKeyIsNotReady(t *testing.T) {
	a := mustRef(t, "op://<vault>/<item>/<field>")
	b := mustRef(t, "op://<vault>/<other-item>/<field>")
	inv := credential.Inventory{
		Authority:  "sa-placeholder",
		Containers: []string{"<vault>"},
		Keys:       []ref.Ref{a, b},
		Coverage:   credential.CoverageVerified,
	}
	va, err := credential.Resolved(credential.NewMaterial([]byte("alpha-secret")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := credential.CompleteBundle(inv, []credential.BundleEntry{credential.BundleValue(a, va)}, credential.BundleMeta{Generation: "g1"})
	if err == nil && got.Ready() {
		t.Fatal("omitting an unreferenced granted key must not yield a ready bundle")
	}
	if err != nil && !errors.Is(err, credential.ErrIncompleteInventory) {
		t.Fatalf("omit: %v, want ErrIncompleteInventory", err)
	}
	_, err = credential.CheckBundle("placeholder-loader", inv, got, nil)
	if err == nil {
		t.Fatal("CheckBundle accepted a partial bundle as ready")
	}
}

func TestCompleteBundle_DuplicateAndOverflowFailExplicitly(t *testing.T) {
	a := mustRef(t, "op://<vault>/<item>/<field>")
	extra := mustRef(t, "op://<other-vault>/<item>/<field>")
	inv := credential.Inventory{
		Authority:  "sa-placeholder",
		Containers: []string{"<vault>"},
		Keys:       []ref.Ref{a},
		Coverage:   credential.CoverageDeclared,
	}
	va, err := credential.Resolved(credential.NewMaterial([]byte("alpha-secret")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = credential.CompleteBundle(inv, []credential.BundleEntry{
		credential.BundleValue(a, va),
		credential.BundleValue(a, va),
	}, credential.BundleMeta{})
	if !errors.Is(err, credential.ErrDuplicateIdentity) {
		t.Fatalf("duplicate: %v, want ErrDuplicateIdentity", err)
	}
	ve, err := credential.Resolved(credential.NewMaterial([]byte("overflow-secret")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = credential.CompleteBundle(inv, []credential.BundleEntry{
		credential.BundleValue(a, va),
		credential.BundleValue(extra, ve),
	}, credential.BundleMeta{})
	if !errors.Is(err, credential.ErrScopeOverflow) {
		t.Fatalf("overflow: %v, want ErrScopeOverflow", err)
	}
}

func TestCheckBundle_EnforcesCompleteBundleRules(t *testing.T) {
	a := mustRef(t, "op://<vault>/<item>/<field>")
	extra := mustRef(t, "op://<other-vault>/<item>/<field>")
	inv := credential.Inventory{
		Authority:  "sa-placeholder",
		Containers: []string{"<vault>"},
		Keys:       []ref.Ref{a},
		Coverage:   credential.CoverageDeclared,
	}
	va, err := credential.Resolved(credential.NewMaterial([]byte("alpha-secret")))
	if err != nil {
		t.Fatal(err)
	}
	ve, err := credential.Resolved(credential.NewMaterial([]byte("overflow-secret")))
	if err != nil {
		t.Fatal(err)
	}
	overflow := credential.RestoreBundle(credential.BundleMeta{Generation: "g1"}, inv.Coverage, credential.CompletenessComplete, []credential.BundleEntry{
		credential.BundleValue(a, va),
		credential.BundleValue(extra, ve),
	})
	if _, err := credential.CheckBundle("placeholder-loader", inv, overflow, nil); err == nil {
		t.Fatal("CheckBundle accepted a scope overflow")
	}
	dup := credential.RestoreBundle(credential.BundleMeta{Generation: "g1"}, inv.Coverage, credential.CompletenessComplete, []credential.BundleEntry{
		credential.BundleValue(a, va),
		credential.BundleValue(a, va),
	})
	if _, err := credential.CheckBundle("placeholder-loader", inv, dup, nil); err == nil {
		t.Fatal("CheckBundle accepted duplicate identities")
	}
	blank := credential.RestoreBundle(credential.BundleMeta{Generation: "g1"}, inv.Coverage, credential.CompletenessComplete, []credential.BundleEntry{
		credential.BundleValue(a, credential.Resolution{}),
	})
	if _, err := credential.CheckBundle("placeholder-loader", inv, blank, nil); err == nil {
		t.Fatal("CheckBundle accepted an unresolved identity")
	}
	badInv := inv
	badInv.Coverage = credential.CoverageIncomplete
	ok, err := credential.CompleteBundle(inv, []credential.BundleEntry{credential.BundleValue(a, va)}, credential.BundleMeta{Generation: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credential.CheckBundle("placeholder-loader", badInv, ok, nil); err == nil {
		t.Fatal("CheckBundle accepted incomplete coverage")
	}
}

func TestBundle_StringRedactsAndZeroWipes(t *testing.T) {
	a := mustRef(t, "op://<vault>/<item>/<field>")
	inv := credential.Inventory{
		Authority:  "sa-placeholder",
		Containers: []string{"<vault>"},
		Keys:       []ref.Ref{a},
		Coverage:   credential.CoverageDeclared,
	}
	va, err := credential.Resolved(credential.NewMaterial([]byte("live-material")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := credential.CompleteBundle(inv, []credential.BundleEntry{credential.BundleValue(a, va)}, credential.BundleMeta{Generation: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if s := fmt.Sprintf("%v %#v", got, got); strings.Contains(s, "live-material") {
		t.Fatalf("bundle leaked: %q", s)
	}
	got.Zero()
	res, ok := got.Lookup(a)
	if !ok {
		t.Fatal("zeroed identity disappeared")
	}
	mat, err := res.Value()
	if err != nil || len(mat.Reveal()) != 0 {
		t.Fatalf("Zero left material: %v %v", mat, err)
	}
}
