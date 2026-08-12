package codehost

import (
	"slices"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// ⚠️ This test file must NOT import github.com/arqtiqa/arqtos-sdk-go/manifest.
// manifest imports THIS package to register the class's capability vocabulary in
// its classCapabilities map, so an internal test that imports manifest is an
// import cycle — and the compiler reports it as a cycle rather than as an unused
// import, which reads as a much stranger problem than it is. Manifest-level
// behaviour for this class is tested in the manifest package, where the
// registration lives.

// TestClass_IsPublishedBySDK is the forward assertion this package graduated
// into, replacing the REVERSE canary it carried while it lived outside the SDK.
//
// That canary asserted connector.Classes() did NOT carry CodeHost, and its
// failure was the instruction followed here: delete the local class constant in
// favour of the SDK's, drop ValidateManifest's class substitution, and let
// manifest.Doc.Validate check the manifest as written. All three are done, so
// the assertion inverts — this package now contracts for a class the SDK
// publishes, and the thing worth pinning is that it stays published.
//
// A class this package contracts for that fell out of connector.Classes() would
// make every manifest declaring it unvalidatable, while this package continued
// to compile.
func TestClass_IsPublishedBySDK(t *testing.T) {
	if !connector.ClassCodeHost.Valid() {
		t.Fatalf("connector.ClassCodeHost is not in connector.Classes() (%v): this package contracts for a class "+
			"the SDK does not publish, so every manifest declaring it is refused by the schema",
			connector.Classes())
	}
	if !slices.Contains(connector.Classes(), connector.ClassCodeHost) {
		t.Errorf("connector.Classes() (%v) omits ClassCodeHost while Valid() accepts it: the two derive from the "+
			"same slice, so this means one of them stopped doing so", connector.Classes())
	}
}

func TestKnownCapabilities_IsACopy(t *testing.T) {
	got := KnownCapabilities()
	if len(got) == 0 {
		t.Fatal("the class declares no capabilities, so nothing can be checked against its vocabulary")
	}
	got[0] = "mutated-by-caller"
	if KnownCapabilities().Has("mutated-by-caller") {
		t.Error("KnownCapabilities returned the package's own slice: a caller can extend the closed vocabulary by writing to it")
	}
}

// TestResolved_RefusesAnEmptyList proves the fail-closed property reaches this
// package through the alias rather than being asserted about it. A connector
// with nothing to report must fail or assert EmptyList; it must not hand back a
// readable empty success.
func TestResolved_RefusesAnEmptyList(t *testing.T) {
	res, err := Resolved([]Repo{}, Complete)
	if err == nil {
		t.Fatal("Resolved accepted an empty list as a complete success")
	}
	if _, ierr := res.Items(); ierr == nil {
		t.Error("the resolution returned alongside the error is readable: a connector that ignored the error would pass an unread code host off as an empty one")
	}
}

// TestResolved_RefusesAPartialRead is the property issue-scale pagination
// depends on: a loop that broke off partway cannot report a smaller success.
func TestResolved_RefusesAPartialRead(t *testing.T) {
	res, err := Resolved([]Repo{{FullName: "owner/one"}}, Partial)
	if err == nil {
		t.Fatal("Resolved accepted a PARTIAL read as a success; a truncated list must surface as a typed failure")
	}
	if _, ierr := res.Items(); ierr == nil {
		t.Error("a partial resolution is readable")
	}
}

func TestEmptyList_IsReadableAndEmpty(t *testing.T) {
	items, err := EmptyList[Repo]().Items()
	if err != nil {
		t.Fatalf("an asserted-empty list must be readable: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("EmptyList carries %d items", len(items))
	}
}

// TestZeroResolution_IsNotAnEmptyList is the conflation the whole type exists
// to remove, asserted at this package's own boundary because it is what a
// failure path in the connector produces when someone writes `return
// Resolution[Repo]{}, err`.
func TestZeroResolution_IsNotAnEmptyList(t *testing.T) {
	var zero Resolution[Repo]
	if _, err := zero.Items(); err == nil {
		t.Fatal("the zero Resolution read as a list; a failure path returning it would look like a code host with no repositories")
	}
}
