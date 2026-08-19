package connector_test

import (
	"context"
	"slices"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

func TestCapabilitiesHas(t *testing.T) {
	caps := connector.Capabilities{"read", "lease"}
	if !caps.Has("read") || caps.Has("rotate") {
		t.Fatalf("Has wrong: %v", caps)
	}
}

// Compile-time proof that a type can satisfy the base interface.
type stub struct{}

func (stub) Implements() connector.Class          { return connector.ClassCredentialLoader }
func (stub) Capabilities() connector.Capabilities { return connector.Capabilities{"read"} }
func (stub) Health(context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}
func (stub) Close() error { return nil }

var _ connector.Connector = stub{}

func TestClassString(t *testing.T) {
	if connector.ClassCredentialLoader != "CredentialLoader" {
		t.Fatalf("class value = %q", connector.ClassCredentialLoader)
	}
	if connector.ClassRoster != "Roster" {
		t.Fatalf("class value = %q", connector.ClassRoster)
	}
	if connector.ClassCodeCI != "CodeCI" {
		t.Fatalf("class value = %q", connector.ClassCodeCI)
	}
	if connector.ClassTracker != "Tracker" {
		t.Fatalf("class value = %q", connector.ClassTracker)
	}
	if connector.ClassAuthenticator != "Authenticator" {
		t.Fatalf("class value = %q", connector.ClassAuthenticator)
	}
}

// TestClassesIsTheClosedSetAndEveryConstantIsInIt: Classes() is the one place
// the SDK's known classes are listed, and the manifest schema's implements enum
// is derived from it. A class declared as a constant but left out of the set is
// exactly the half-added state that derivation exists to prevent — it would be
// routable in Go and refused by every manifest.
func TestClassesIsTheClosedSetAndEveryConstantIsInIt(t *testing.T) {
	want := []connector.Class{
		connector.ClassCredentialLoader,
		connector.ClassRoster,
		connector.ClassCodeCI,
		// ClassCodeHost graduated in from arqtos-connectors on 2026-08-12,
		// carrying the narrowed eleven-operation contract.
		connector.ClassCodeHost,
		connector.ClassTracker,
		connector.ClassAuthenticator,
	}
	got := connector.Classes()
	if len(got) != len(want) {
		t.Fatalf("Classes() = %v, want exactly %v — adding one is a deliberate contract change", got, want)
	}
	for _, c := range want {
		if !slices.Contains(got, c) {
			t.Fatalf("Classes() = %v, missing %q", got, c)
		}
		if !c.Valid() {
			t.Fatalf("%q is a declared class constant but is not Valid()", c)
		}
	}
	if !slices.IsSorted(got) {
		t.Fatalf("Classes() = %v, want a stable sorted order", got)
	}
}

// TestClassesIsHandedOutAsACopy: a caller must not be able to narrow or extend
// the SDK's class set — and because the manifest enum derives from this, a
// mutation here would change what manifests a host accepts.
func TestClassesIsHandedOutAsACopy(t *testing.T) {
	got := connector.Classes()
	got[0] = connector.Class("Mutated")
	if slices.Contains(connector.Classes(), connector.Class("Mutated")) {
		t.Fatalf("Classes() handed out its backing array")
	}
	if !connector.ClassCredentialLoader.Valid() {
		t.Fatalf("mutating the returned slice unregistered a class")
	}
}

// TestHealthStatusVocabulary pins the published-wire exception to the
// SDK's "zero value means unsaid" rule. Track-B HealthResponse.status is
// 0 = healthy, 1 = degraded, 2 = unavailable. Flipping the iota would
// break every existing provider. New enums in this module still start at
// Unspecified and refuse the zero value as a forgotten argument.
func TestHealthStatusVocabulary(t *testing.T) {
	if got := connector.HealthStatus(0); got != connector.Healthy {
		t.Fatalf("HealthStatus(0) = %v, want Healthy — the published Track-B wire number", got)
	}
	for _, s := range []connector.HealthStatus{connector.Healthy, connector.Degraded, connector.Unavailable} {
		if !s.Valid() {
			t.Fatalf("%v is a vocabulary member but not Valid", s)
		}
	}
	if connector.HealthStatus(42).Valid() {
		t.Fatal("HealthStatus(42) must not be Valid — an out-of-vocabulary status is unsaid, not healthy")
	}
	// The zero value IS Healthy here. That is the exception, not a second
	// rule: a forgotten Status would report healthy, which is why every
	// Health() implementation must set it on purpose. New enums do not
	// copy this numbering.
	var zero connector.HealthStatus
	if zero != connector.Healthy || !zero.Valid() {
		t.Fatalf("zero HealthStatus = %v Valid=%v; the published wire makes 0 a real answer", zero, zero.Valid())
	}
}

// TestAnUnknownClassIsNotValid: a Class is a string type, so any string can be
// converted into one. Valid() is what separates a routing decision a host can
// act on from a string somebody typed.
func TestAnUnknownClassIsNotValid(t *testing.T) {
	if connector.Class("SomethingElse").Valid() {
		t.Fatalf("an unregistered class must not be Valid")
	}
	if connector.Class("").Valid() {
		t.Fatalf("the empty class must not be Valid")
	}
	if connector.Class("roster").Valid() {
		t.Fatalf("class comparison must be exact: a miscased class is not the class")
	}
}
