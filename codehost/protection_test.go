package codehost_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
)

func TestKnownCapabilities_CarriesProtectionInspect(t *testing.T) {
	got := codehost.KnownCapabilities()
	if !slices.Contains(got, codehost.CapProtectionInspect) {
		t.Fatalf("KnownCapabilities() = %v; want it to contain %q, or a manifest declaring it "+
			"is rejected as an unknown capability", got, codehost.CapProtectionInspect)
	}
	if want := 5; len(got) != want {
		t.Errorf("KnownCapabilities() has %d entries, want %d — the count is asserted so that a "+
			"capability cannot be silently dropped while this test still passes on the one it looks for",
			len(got), want)
	}
}

// ⚠️ THE LOAD-BEARING TEST OF THIS TIER.
//
// The assurance claim is "a check pinned to our app is required, AND nothing may
// bypass it". The second half is an assertion about an EMPTY list — and a bypass
// list that was never read is also empty. The two are indistinguishable in a
// plain slice and they are opposite claims: one says nobody can bypass the gate,
// the other says nobody looked.
//
// So an unresolved bypass list must be a typed failure at the boundary, never a
// value a caller can measure the length of.
func TestCheckProtection_RefusesAnUnresolvedBypassList(t *testing.T) {
	checks, err := codehost.Resolved([]codehost.RequiredCheck{{Context: "arqtos/gate", AppID: "12345"}}, codehost.Complete)
	if err != nil {
		t.Fatalf("building the required-check resolution: %v", err)
	}

	p := codehost.Protection{
		Ref:            "refs/heads/main",
		RequiredChecks: checks,
		// BypassActors deliberately left at its zero value: this is what a
		// connector produces when it returns `Protection{...}` on a path that
		// failed to enumerate them.
	}

	err = codehost.CheckProtection(p)
	if err == nil {
		t.Fatal("CheckProtection accepted a Protection whose bypass actors were never resolved; " +
			"a caller would read len(Items()) == 0 as 'nothing can bypass this gate' from a read that " +
			"returned nothing")
	}
	if !strings.Contains(err.Error(), "bypass") {
		t.Errorf("error = %v; want it to name the bypass list, so the reader knows which half is unsupported", err)
	}
}

func TestCheckProtection_RefusesUnresolvedRequiredChecks(t *testing.T) {
	p := codehost.Protection{Ref: "refs/heads/main", BypassActors: codehost.EmptyList[codehost.BypassActor]()}
	if err := codehost.CheckProtection(p); err == nil {
		t.Fatal("CheckProtection accepted a Protection whose required checks were never resolved")
	}
}

// The strong answer — a ref that genuinely permits no bypass — must still be
// expressible, or the guard above would make the good case unreportable.
func TestCheckProtection_AcceptsAnAssertedEmptyBypassList(t *testing.T) {
	checks, err := codehost.Resolved([]codehost.RequiredCheck{{Context: "arqtos/gate", AppID: "12345"}}, codehost.Complete)
	if err != nil {
		t.Fatalf("building the required-check resolution: %v", err)
	}

	p := codehost.Protection{
		Ref:            "refs/heads/main",
		RequiredChecks: checks,
		BypassActors:   codehost.EmptyList[codehost.BypassActor](),
	}
	if err := codehost.CheckProtection(p); err != nil {
		t.Fatalf("CheckProtection rejected an ASSERTED empty bypass list: %v — emptiness must be "+
			"reportable, it just must not be inferable", err)
	}

	actors, err := p.BypassActors.Items()
	if err != nil {
		t.Fatalf("reading an asserted-empty resolution: %v", err)
	}
	if len(actors) != 0 {
		t.Errorf("asserted-empty bypass list yielded %d actor(s)", len(actors))
	}
}

// A ref that carries no protection at all is a real answer, not an error — and
// it is the answer that downgrades an assurance claim, so it must survive the
// guard rather than be refused by it.
func TestCheckProtection_AcceptsARefWithNoRequiredChecks(t *testing.T) {
	p := codehost.Protection{
		Ref:            "refs/heads/main",
		RequiredChecks: codehost.EmptyList[codehost.RequiredCheck](),
		BypassActors:   codehost.EmptyList[codehost.BypassActor](),
	}
	if err := codehost.CheckProtection(p); err != nil {
		t.Fatalf("CheckProtection rejected an unprotected ref: %v — 'no rules' is a finding, not a failure", err)
	}
}

func TestCheckProtection_RefusesAProtectionThatNamesNoRef(t *testing.T) {
	p := codehost.Protection{
		RequiredChecks: codehost.EmptyList[codehost.RequiredCheck](),
		BypassActors:   codehost.EmptyList[codehost.BypassActor](),
	}
	if err := codehost.CheckProtection(p); err == nil {
		t.Fatal("CheckProtection accepted a Protection naming no ref; a caller holding several cannot tell them apart")
	}
}

func TestRequiredCheck_PinnedDistinguishesGatingFromDecoration(t *testing.T) {
	tests := []struct {
		name             string
		check            codehost.RequiredCheck
		pinned           bool
		pinnedToExpected bool
	}{
		{"unpinned context", codehost.RequiredCheck{Context: "arqtos/gate"}, false, false},
		{"pinned to us", codehost.RequiredCheck{Context: "arqtos/gate", AppID: "12345"}, true, true},
		{"pinned to someone else", codehost.RequiredCheck{Context: "arqtos/gate", AppID: "99999"}, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.check.Pinned(); got != tt.pinned {
				t.Errorf("Pinned() = %v, want %v", got, tt.pinned)
			}
			if got := tt.check.PinnedTo("12345"); got != tt.pinnedToExpected {
				t.Errorf("PinnedTo(\"12345\") = %v, want %v", got, tt.pinnedToExpected)
			}
		})
	}
}

// ⚠️ PinnedTo("") must never be true. An empty expected-app id is a caller that
// does not know who it is; answering "yes, pinned to that" would turn a
// misconfiguration into the strongest assurance claim available.
func TestRequiredCheck_PinnedToEmptyIsNeverSatisfied(t *testing.T) {
	for _, c := range []codehost.RequiredCheck{
		{Context: "arqtos/gate"},
		{Context: "arqtos/gate", AppID: "12345"},
	} {
		if c.PinnedTo("") {
			t.Errorf("RequiredCheck{AppID:%q}.PinnedTo(\"\") = true; an unknown expected app must not "+
				"read as a satisfied pin", c.AppID)
		}
	}
}

// ⚠️ The rule this tier exists to obey: a NEW interface, never a method added to
// an existing one. Adding InspectProtection to CodeHost would break every
// implementer — and break them at compile time in THEIR repository, after
// release, where this module never sees it.
//
// Stated against the interface itself rather than against a stub, so the check
// cannot be satisfied by a stub that happens to be written correctly.
func TestProtectionInspector_IsNotAMethodOnTheRequiredInterface(t *testing.T) {
	required := reflect.TypeOf((*codehost.CodeHost)(nil)).Elem()
	if _, found := required.MethodByName("InspectProtection"); found {
		t.Fatal("CodeHost declares InspectProtection: the operation was added to the REQUIRED interface " +
			"instead of landing behind its own optional tier, which breaks every existing implementer")
	}

	tier := reflect.TypeOf((*codehost.ProtectionInspector)(nil)).Elem()
	if _, found := tier.MethodByName("InspectProtection"); !found {
		t.Fatal("ProtectionInspector does not declare InspectProtection")
	}
	if got := tier.NumMethod(); got != 1 {
		t.Errorf("ProtectionInspector declares %d methods, want 1 — a read-only probe that grew a "+
			"second operation is worth looking at, because writing protection is a different authority", got)
	}
}

// ⚠️ The refusal must be classifiable, not merely present. The caller this guard
// exists for branches on cerr.KindOf like every other call in this contract; a
// bare error classifies as KindUnknown, which does not trip a breaker and reads
// as "something odd happened" rather than "this value is not usable".
func TestCheckProtection_RefusalsAreClassifiedInvalid(t *testing.T) {
	tests := map[string]codehost.Protection{
		"no ref":            {BypassActors: codehost.EmptyList[codehost.BypassActor]()},
		"unresolved bypass": {Ref: "refs/heads/main", RequiredChecks: codehost.EmptyList[codehost.RequiredCheck]()},
		"unresolved checks": {Ref: "refs/heads/main", BypassActors: codehost.EmptyList[codehost.BypassActor]()},
	}

	for name, p := range tests {
		t.Run(name, func(t *testing.T) {
			err := codehost.CheckProtection(p)
			if err == nil {
				t.Fatal("CheckProtection accepted an unusable Protection")
			}
			if got := cerr.KindOf(err); got != cerr.KindInvalid {
				t.Errorf("kind = %v, want %v — a caller branches on the kind, never on the message", got, cerr.KindInvalid)
			}
		})
	}
}

// The Items() failure must stay reachable underneath the classification, or the
// guard has replaced a specific diagnosis with a general one.
func TestCheckProtection_WrapsTheUnderlyingResolutionFailure(t *testing.T) {
	p := codehost.Protection{Ref: "refs/heads/main", RequiredChecks: codehost.EmptyList[codehost.RequiredCheck]()}

	err := codehost.CheckProtection(p)
	if err == nil {
		t.Fatal("CheckProtection accepted an unresolved bypass list")
	}
	_, inner := p.BypassActors.Items()
	if inner == nil {
		t.Fatal("an unresolved Resolution reported no error of its own")
	}
	if !strings.Contains(err.Error(), "bypass") {
		t.Errorf("error = %v; want it to name the bypass list", err)
	}
}
