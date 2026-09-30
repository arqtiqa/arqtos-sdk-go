package codeci_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codeci"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

func TestKnownCapabilities_CarriesAuthenticatedSubject(t *testing.T) {
	got := codeci.KnownCapabilities()
	if !slices.Contains(got, codeci.CapAuthenticatedSubject) {
		t.Fatalf("KnownCapabilities() = %v; want it to contain %q, or a manifest declaring it is rejected as an unknown capability",
			got, codeci.CapAuthenticatedSubject)
	}
}

func TestCapabilities_AuthenticatedSubjectIsADistinctOptionalTier(t *testing.T) {
	if codeci.CapAuthenticatedSubject == "" {
		t.Fatal("CapAuthenticatedSubject is empty")
	}
	if codeci.CapAuthenticatedSubject == codeci.CapCIControl {
		t.Fatal("CapAuthenticatedSubject and CapCIControl are the same value")
	}
	if codeci.CapAuthenticatedSubject == codeci.CapCheckPublish {
		t.Fatal("CapAuthenticatedSubject and CapCheckPublish are the same value")
	}
}

func TestSubjectKind_WhenUnspecified_IsNotAUsableKind(t *testing.T) {
	if !codeci.SubjectKindUnspecified.Valid() {
		t.Fatal("SubjectKindUnspecified must be a valid VALUE")
	}
	if codeci.SubjectKindUnspecified.Specified() {
		t.Fatal("SubjectKindUnspecified must not be Specified; a forgotten kind must not default to PAT")
	}
	for _, k := range []codeci.SubjectKind{codeci.SubjectKindPAT, codeci.SubjectKindApp, codeci.SubjectKindService} {
		if !k.Specified() {
			t.Fatalf("%v must be Specified", k)
		}
	}
	bogus := codeci.SubjectKind(99)
	if bogus.Valid() || bogus.Specified() {
		t.Fatalf("an out-of-vocabulary SubjectKind must be neither Valid nor Specified")
	}
}

func TestSubjectKind_PATIsNotAppOrService(t *testing.T) {
	if codeci.SubjectKindPAT == codeci.SubjectKindApp {
		t.Fatal("PAT and App are the same kind; a host would infer human membership from an App installation")
	}
	if codeci.SubjectKindPAT == codeci.SubjectKindService {
		t.Fatal("PAT and Service are the same kind; a host would infer human membership from a service identity")
	}
}

func TestSubject_Coherent_WhenRequiredFieldsPresent(t *testing.T) {
	s := codeci.Subject{
		NativeID:  "42",
		Authority: "github.com",
		Kind:      codeci.SubjectKindPAT,
		Login:     "placeholder-login",
	}
	if !s.Coherent() {
		t.Fatalf("Subject%+v.Coherent() = false, want true", s)
	}
}

func TestSubject_Coherent_WhenNativeIDMissing(t *testing.T) {
	s := codeci.Subject{Authority: "github.com", Kind: codeci.SubjectKindPAT, Login: "placeholder-login"}
	if s.Coherent() {
		t.Fatalf("Subject%+v.Coherent() = true; a missing native id is not a smaller identity", s)
	}
}

func TestSubject_Coherent_WhenAuthorityMissing(t *testing.T) {
	s := codeci.Subject{NativeID: "42", Kind: codeci.SubjectKindPAT, Login: "placeholder-login"}
	if s.Coherent() {
		t.Fatalf("Subject%+v.Coherent() = true; a native id with no provider authority is not scoped", s)
	}
}

func TestSubject_Coherent_WhenKindUnspecified(t *testing.T) {
	s := codeci.Subject{NativeID: "42", Authority: "github.com", Login: "placeholder-login"}
	if s.Coherent() {
		t.Fatalf("Subject%+v.Coherent() = true; an unspecified kind would default PAT and treat App/service as human", s)
	}
}

func TestSubject_Coherent_WhenAuthorityIsALocator(t *testing.T) {
	for _, authority := range []string{"https://github.com", "github.com/arqtiqa", "github.com/arqtiqa/arqtos"} {
		s := codeci.Subject{NativeID: "42", Authority: authority, Kind: codeci.SubjectKindPAT}
		if s.Coherent() {
			t.Errorf("Authority %q is a locator, not a provider realm; Coherent() = true", authority)
		}
	}
}

func TestSubject_Coherent_WhenNativeIDIsALocatorOrEmail(t *testing.T) {
	for _, id := range []string{"arqtiqa/arqtos", "user@example.com", "https://github.com/users/1"} {
		s := codeci.Subject{NativeID: id, Authority: "github.com", Kind: codeci.SubjectKindPAT}
		if s.Coherent() {
			t.Errorf("NativeID %q is a locator or email, not an opaque native id; Coherent() = true", id)
		}
	}
}

func TestSubject_WhenLoginRenames_PreservesIdentity(t *testing.T) {
	before := codeci.Subject{
		NativeID:  "42",
		Authority: "github.com",
		Kind:      codeci.SubjectKindPAT,
		Login:     "alice",
	}
	after := before
	after.Login = "alice-renamed"
	if !before.SameIdentity(after) {
		t.Fatalf("SameIdentity returned false after a login rename; NativeID %q is the identity, login is display", before.NativeID)
	}
}

func TestSubject_WhenNativeIDFollowsLogin_IsNotTheSameIdentity(t *testing.T) {
	before := codeci.Subject{
		NativeID:  "42",
		Authority: "github.com",
		Kind:      codeci.SubjectKindPAT,
		Login:     "alice",
	}
	after := before
	after.Login = "alice-renamed"
	after.NativeID = after.Login
	if after.SameIdentity(before) {
		t.Fatal("a subject that replaced NativeID with the renamed login still compared equal; login is not identity")
	}
}

func TestSubjectReporter_IsNotAMethodOnAnyExistingInterface(t *testing.T) {
	for _, iface := range []struct {
		name string
		typ  reflect.Type
	}{
		{"CodeCI", reflect.TypeOf((*codeci.CodeCI)(nil)).Elem()},
		{"CIController", reflect.TypeOf((*codeci.CIController)(nil)).Elem()},
		{"CheckPublisher", reflect.TypeOf((*codeci.CheckPublisher)(nil)).Elem()},
	} {
		if _, found := iface.typ.MethodByName("AuthenticatedSubject"); found {
			t.Errorf("%s declares AuthenticatedSubject: the operation was added to an existing interface instead of landing behind its own optional tier", iface.name)
		}
	}

	tier := reflect.TypeOf((*codeci.SubjectReporter)(nil)).Elem()
	if _, found := tier.MethodByName("AuthenticatedSubject"); !found {
		t.Fatal("SubjectReporter does not declare AuthenticatedSubject")
	}
	if got := tier.NumMethod(); got != 1 {
		t.Errorf("SubjectReporter declares %d methods, want 1", got)
	}
}

func TestIdentity_DoesNotGrowNativeSubjectFields(t *testing.T) {
	typ := reflect.TypeOf(codeci.Identity{})
	want := map[string]bool{"Login": true, "Authenticated": true}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !want[name] {
			t.Errorf("Identity grew %s: existing WhoAmI implementers must stay source-compatible; native subject belongs on Subject", name)
		}
	}
	if typ.NumField() != 2 {
		t.Errorf("Identity has %d fields, want 2 (Login, Authenticated)", typ.NumField())
	}
}

func TestCIController_MethodSetUnchangedBySubject(t *testing.T) {
	typ := reflect.TypeOf((*codeci.CIController)(nil)).Elem()
	want := []string{"CancelWorkflow", "RerunWorkflow"}
	got := make([]string, 0, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("CIController methods = %v, want exactly %v", got, want)
	}
}

func TestSubjectKind_StringNamesTheVocabulary(t *testing.T) {
	if got := codeci.SubjectKindPAT.String(); got != "pat" {
		t.Errorf("SubjectKindPAT.String() = %q, want pat", got)
	}
	if got := codeci.SubjectKindApp.String(); got != "app" {
		t.Errorf("SubjectKindApp.String() = %q, want app", got)
	}
	if got := codeci.SubjectKindService.String(); got != "service" {
		t.Errorf("SubjectKindService.String() = %q, want service", got)
	}
	if got := codeci.SubjectKind(99).String(); !strings.Contains(got, "99") {
		t.Errorf("SubjectKind(99).String() = %q, want it to name the out-of-vocabulary value", got)
	}
}

func TestCheckSubject_WhenIncoherent_IsAContractViolation(t *testing.T) {
	_, err := codeci.CheckSubject("placeholder-codeci", codeci.Subject{Login: "alice"}, nil)
	if err == nil {
		t.Fatal("an incoherent subject must be refused")
	}
	if got := cerr.KindOf(err); got != cerr.KindContractViolation {
		t.Fatalf("KindOf = %v, want KindContractViolation", got)
	}
	var fe *codeci.FaultError
	if !errors.As(err, &fe) || fe.Fault != codeci.FaultIncoherentSubject {
		t.Fatalf("want FaultIncoherentSubject, got %v", err)
	}
}

func TestCheckSubject_WhenClassifiedFailure_PassesThrough(t *testing.T) {
	own := cerr.New(cerr.KindUnsupported, "AuthenticatedSubject", nil)
	_, err := codeci.CheckSubject("placeholder-codeci", codeci.Subject{}, own)
	if !errors.Is(err, own) {
		t.Fatalf("the connector's own failure must pass through unchanged, got %v", err)
	}
	if cerr.KindOf(err) != cerr.KindUnsupported {
		t.Fatalf("KindOf = %v, want KindUnsupported", cerr.KindOf(err))
	}
}

func TestKnownCapabilities_AuthenticatedSubjectIsCopied(t *testing.T) {
	got := codeci.KnownCapabilities()
	if !got.Has(codeci.CapAuthenticatedSubject) {
		t.Fatalf("KnownCapabilities() = %v, missing %q", got, codeci.CapAuthenticatedSubject)
	}
	got[0] = connector.Capability("mutated")
	if codeci.KnownCapabilities().Has(connector.Capability("mutated")) {
		t.Fatal("KnownCapabilities() handed out its backing array")
	}
}
