package main

import (
	"context"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
	"github.com/arqtiqa/arqtos-sdk-go/rosterconform"
)

// referenceManifest is the connector.yml this provider ships — the file a host
// reads BEFORE it launches anything. It lives here so the manifest, the served
// directory and both conformance runs in this directory are one source.
func referenceManifest() manifest.Doc {
	return manifest.Doc{
		Name:           "roster-provider-example",
		Implements:     connector.ClassRoster,
		Kind:           manifest.KindProvider,
		MinHostVersion: referenceMinHostVersion,
		Capabilities:   []connector.Capability{roster.CapTransitiveMembership, roster.CapMachinePrincipals},
	}
}

// referenceOptions are the fixtures a conformance run against this provider
// needs. Every one of them is required by rosterconform: a check that cannot be
// driven is not skipped, because a report that is green because nothing looked
// is the failure that harness exists to avoid.
func referenceOptions() rosterconform.Options {
	return rosterconform.Options{
		Manifest:           referenceManifest(),
		Group:              referenceGroup,
		AbsentGroup:        referenceAbsentGroup,
		SuspendedPrincipal: referenceSuspended,
	}
}

// TestMemRosterIsConformantInProcess is the fast, always-on half: it checks the
// connector's own behaviour with no subprocess and no serialisation involved.
//
// It is NOT sufficient on its own, and that is the point of pairing it with
// roundtrip_test.go. Every marshalling failure this class can suffer — an
// unresolved read arriving as an empty directory, a suspended principal losing
// its Active flag, a membership arriving for the wrong group — is invisible
// here, because in-process there is nothing doing the marshalling. A provider
// checked only this way has had that whole class of bug go unexamined.
func TestMemRosterIsConformantInProcess(t *testing.T) {
	rep, err := rosterconform.Run(context.Background(), &memRoster{}, referenceOptions())
	if err != nil {
		t.Fatalf("rosterconform.Run could not be carried out: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("the reference provider must be conformant, including both declared capabilities:\n%s", rep)
	}
	if rep.Transport != rosterconform.TransportUnrecorded {
		t.Fatalf("Transport = %q; an in-process Run cannot know what is behind a roster.Roster and must not claim it does",
			rep.Transport)
	}
}

// TestReferenceManifestValidates covers what a host checks before it loads
// anything: the manifest parses its own enums, its capabilities are in the
// Roster class vocabulary, and — because this is a kind: provider — it declares
// a min_host_version at all.
func TestReferenceManifestValidates(t *testing.T) {
	if err := referenceManifest().Validate(); err != nil {
		t.Fatalf("the shipped manifest must validate: %v", err)
	}
}
