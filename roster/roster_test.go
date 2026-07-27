package roster_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// stubRoster is compile-time proof that a type can satisfy the class contract,
// and the shape an external author starts from.
type stubRoster struct{}

func (stubRoster) Implements() connector.Class { return connector.ClassRoster }

func (stubRoster) Capabilities() connector.Capabilities { return nil }

func (stubRoster) Health(context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}

func (stubRoster) Close() error { return nil }

func (stubRoster) ListPrincipals(context.Context) (roster.Resolution[roster.Principal], error) {
	return roster.Resolved([]roster.Principal{person(idPersonA)})
}

func (stubRoster) ListGroups(context.Context) (roster.Resolution[roster.Group], error) {
	return roster.Resolved([]roster.Group{{ID: idGroup, Handle: idGroup}})
}

func (stubRoster) ListMemberships(_ context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	if groupID != idGroup {
		return roster.Resolution[roster.Membership]{}, cerr.New(cerr.KindNotFound, "ListMemberships", nil)
	}
	return roster.Resolved([]roster.Membership{member(idPersonA, true)})
}

var _ roster.Roster = stubRoster{}

// TestRosterIsReadOnly pins the scope decision that keeps every reconcile bug
// non-destructive. arqtos provisions arqtos-side artifacts; it does not create,
// update or delete people in somebody else's directory, and a class that could
// would turn a mis-mapped org model into a directory mutation.
//
// It is asserted through the interface rather than by reading the source, so it
// fails on an added method rather than on a reworded comment.
func TestRosterIsReadOnly(t *testing.T) {
	var c roster.Roster = stubRoster{}
	for _, probe := range []struct {
		name string
		has  bool
	}{
		{"CreatePrincipal", hasMethod[interface {
			CreatePrincipal(context.Context, roster.Principal) error
		}](c)},
		{"DeletePrincipal", hasMethod[interface {
			DeletePrincipal(context.Context, string) error
		}](c)},
		{"AddMembership", hasMethod[interface {
			AddMembership(context.Context, roster.Membership) error
		}](c)},
	} {
		if probe.has {
			t.Fatalf("the Roster class grew a %s: rosters are read-only, and a write path makes every reconcile bug a destructive one", probe.name)
		}
	}
}

func hasMethod[I any](c roster.Roster) bool {
	_, ok := any(c).(I)
	return ok
}

// TestKnownCapabilitiesIsTheClosedVocabularyAndIsCopied: the vocabulary is
// closed because a host acts on the name — a capability it does not recognise
// is one it will not use, so a typo is indistinguishable from a capability
// that has yet to ship. And it is handed out as a copy, so a caller cannot
// narrow or extend a published contract by mutating it.
func TestKnownCapabilitiesIsTheClosedVocabularyAndIsCopied(t *testing.T) {
	got := roster.KnownCapabilities()
	want := connector.Capabilities{roster.CapWatch, roster.CapTransitiveMembership, roster.CapMachinePrincipals}
	if len(got) != len(want) {
		t.Fatalf("KnownCapabilities() = %v, want exactly %v — a capability added or removed here is a contract change", got, want)
	}
	for _, c := range want {
		if !got.Has(c) {
			t.Fatalf("KnownCapabilities() is missing %q", c)
		}
	}

	got[0] = connector.Capability("mutated")
	if roster.KnownCapabilities().Has("mutated") {
		t.Fatalf("KnownCapabilities() handed out its backing array: a caller must not be able to edit the vocabulary")
	}
}

// TestCapabilityWireNamesArePinned: these strings are what an external author
// writes in a connector.yml and what a host matches on. Renaming one silently
// unregisters every connector that declared it, and the repo went public for
// third parties to encode against exactly these spellings.
func TestCapabilityWireNamesArePinned(t *testing.T) {
	for declared, want := range map[connector.Capability]string{
		roster.CapWatch:                "watch",
		roster.CapTransitiveMembership: "transitive_membership",
		roster.CapMachinePrincipals:    "machine_principals",
	} {
		if string(declared) != want {
			t.Fatalf("capability spelling changed: %q, want %q", declared, want)
		}
	}
}

// TestPrincipalKindZeroValueIsUnknown is the safe default, and it is the reason
// PrincipalKind is an int rather than a string. A connector that forgets to set
// Kind must report "unclassified", so a host that treats machine identities
// differently from human ones knows it may apply neither rule. A string-typed
// kind would have made the zero value "", which is neither a classification nor
// an admission that one is missing.
func TestPrincipalKindZeroValueIsUnknown(t *testing.T) {
	var forgotten roster.Principal
	if forgotten.Kind != roster.PrincipalUnknown {
		t.Fatalf("the zero Principal.Kind = %v, want PrincipalUnknown", forgotten.Kind)
	}
	if forgotten.Kind == roster.PrincipalHuman || forgotten.Kind == roster.PrincipalMachine {
		t.Fatalf("an unset Kind must not read as a classification")
	}
	if forgotten.Kind.String() != "unknown" {
		t.Fatalf("the zero Kind renders as %q", forgotten.Kind.String())
	}
	// Absence of a mailbox is not an error either: machine identities
	// frequently have none, and neither does everybody in every directory.
	if forgotten.Email != "" {
		t.Fatalf("the zero Principal must carry no email")
	}
}

// TestPrincipalKindVocabularyIsClosedAndDerivedFromOneSource: PrincipalKinds,
// Valid and String all come from one map, so a kind cannot be half-added — in
// the enum but nameless, or named but not enumerable.
func TestPrincipalKindVocabularyIsClosedAndDerivedFromOneSource(t *testing.T) {
	kinds := roster.PrincipalKinds()
	want := []roster.PrincipalKind{roster.PrincipalUnknown, roster.PrincipalHuman, roster.PrincipalMachine}
	if !slices.Equal(kinds, want) {
		t.Fatalf("PrincipalKinds() = %v, want %v in ascending order", kinds, want)
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		if !k.Valid() {
			t.Fatalf("%v is enumerated but not Valid", k)
		}
		name := k.String()
		if name == "" || strings.HasPrefix(name, "invalid_") {
			t.Fatalf("%d is enumerated but has no name (%q)", int(k), name)
		}
		if seen[name] {
			t.Fatalf("two kinds render as %q", name)
		}
		seen[name] = true
	}

	kinds[0] = roster.PrincipalMachine
	if !slices.Equal(roster.PrincipalKinds(), want) {
		t.Fatalf("PrincipalKinds() handed out its backing array")
	}
}

// TestAnOutOfVocabularyKindCannotHideBehindTheSafeDefault: an integer somebody
// converted must not render as "unknown". Unknown is a classification a
// connector chose; invalid_principal_kind(9) is a bug, and rendering them
// identically buries the second inside the first.
func TestAnOutOfVocabularyKindCannotHideBehindTheSafeDefault(t *testing.T) {
	bogus := roster.PrincipalKind(9)
	if bogus.Valid() {
		t.Fatalf("PrincipalKind(9) must not be Valid")
	}
	if got := bogus.String(); got != "invalid_principal_kind(9)" {
		t.Fatalf("PrincipalKind(9).String() = %q", got)
	}
	if bogus.String() == roster.PrincipalUnknown.String() {
		t.Fatalf("an invalid kind must not render like the safe default")
	}
	if got := roster.PrincipalKind(-1).String(); got != "invalid_principal_kind(-1)" {
		t.Fatalf("PrincipalKind(-1).String() = %q", got)
	}
}

// TestSubjectZeroValueIsUnknown: an unattributed change hint tells a host to
// re-read everything, which is what it does on a schedule anyway. The zero
// value must therefore be the widest, not the narrowest.
func TestSubjectZeroValueIsUnknown(t *testing.T) {
	var c roster.Change
	if c.Subject != roster.SubjectUnknown {
		t.Fatalf("the zero Change.Subject = %v, want SubjectUnknown", c.Subject)
	}
}

// TestClassRosterIsRegisteredAndSpelled: the class string is what a manifest's
// implements field carries and what a host routes on.
func TestClassRosterIsRegisteredAndSpelled(t *testing.T) {
	if connector.ClassRoster != "Roster" {
		t.Fatalf("class value = %q, want %q", connector.ClassRoster, "Roster")
	}
	if !connector.ClassRoster.Valid() {
		t.Fatalf("ClassRoster must be in the SDK's closed class set")
	}
	if !slices.Contains(connector.Classes(), connector.ClassRoster) {
		t.Fatalf("Classes() = %v, missing ClassRoster", connector.Classes())
	}
	if got := (stubRoster{}).Implements(); got != connector.ClassRoster {
		t.Fatalf("Implements() = %q", got)
	}
}

// TestWatcherIsSeparateFromTheBaseContract: the watch operation lives behind a
// capability rather than in the Roster interface, so a connector for a
// directory that cannot push change is not forced to carry a method it must
// fail. A host type-asserts.
func TestWatcherIsSeparateFromTheBaseContract(t *testing.T) {
	var c roster.Roster = stubRoster{}
	if _, ok := c.(roster.Watcher); ok {
		t.Fatalf("a bare Roster must not satisfy Watcher: the capability would then be unfalsifiable")
	}
}
