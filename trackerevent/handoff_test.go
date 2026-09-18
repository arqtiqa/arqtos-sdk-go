package trackerevent

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/repoobs"
	"github.com/arqtiqa/arqtos-sdk-go/tracker"
)

func TestTracker_IsStillExactlyFiveOperations(t *testing.T) {
	base := methodNames(reflect.TypeOf((*connector.Connector)(nil)).Elem())
	all := methodNames(reflect.TypeOf((*tracker.Tracker)(nil)).Elem())
	var own []string
	for _, m := range all {
		if !slices.Contains(base, m) {
			own = append(own, m)
		}
	}
	want := []string{"Apply", "Catalogue", "Create", "GetItems", "Scan"}
	if !slices.Equal(own, want) {
		t.Fatalf("Tracker operations=%v, want %v — this package must not add a sixth", own, want)
	}
}

func TestClasses_StaySixAndDoNotGrowAReceiverClass(t *testing.T) {
	got := connector.Classes()
	if len(got) != 6 {
		t.Fatalf("Classes()=%v, want six — a receiver class is a contract change", got)
	}
	if slices.Contains(got, connector.Class("Receiver")) || slices.Contains(got, connector.Class("Event")) {
		t.Fatalf("Classes() grew a receiver/event class: %v", got)
	}
}

func TestGap_RepoobsEnvelopeRefusesUnknownOccurred(t *testing.T) {
	env := repoobs.Envelope{SchemaVersion: repoobs.SchemaVersion}
	err := env.Validate()
	if err == nil || !strings.Contains(err.Error(), "occurred_at") {
		t.Fatalf("repoobs.Envelope is not the tracker-event handoff (err=%v); unknown occurred must stay unknown here", err)
	}
	h := validHandoff()
	h.Occurred = Instant{}
	if err := h.Validate(); err != nil {
		t.Fatalf("unknown occurred must be valid on this handoff: %v", err)
	}
}

func TestGap_CodeHostWebhooksIsRegistrationNotThisHandoff(t *testing.T) {
	if tracker.KnownCapabilities().Has(codehost.CapWebhooks) {
		t.Fatal("Tracker must not borrow CodeHost CapWebhooks")
	}
	if codehost.CapWebhooks != "webhooks" {
		t.Fatalf("CapWebhooks=%q", codehost.CapWebhooks)
	}
}

func TestDedupeKey_IgnoresDeliveryIDAndIsolatesRealm(t *testing.T) {
	a := validHandoff()
	a.EventID = "evt-stable"
	a.DeliveryID = "del-1"
	b := a
	b.DeliveryID = "del-2"
	if a.DedupeKey() != b.DedupeKey() {
		t.Fatalf("retry under a new delivery id changed dedupe key %q vs %q", a.DedupeKey(), b.DedupeKey())
	}
	if strings.Contains(a.DedupeKey(), a.DeliveryID) {
		t.Fatal("dedupe key carried delivery identity")
	}
	c := a
	c.Realm = "https://other.example.test"
	if a.DedupeKey() == c.DedupeKey() {
		t.Fatal("same event id on another instance shared a dedupe key")
	}
	d := a
	d.Workspace = "other-workspace"
	if a.DedupeKey() == d.DedupeKey() {
		t.Fatal("same event id in another workspace shared a dedupe key")
	}
}

func TestValidate_RefusesMalformedAndUnsupportedVersion(t *testing.T) {
	h := validHandoff()
	h.SchemaVersion = 2
	if cerr.KindOf(h.Validate()) != cerr.KindUnsupported {
		t.Fatalf("schema 2: %v", h.Validate())
	}
	h = validHandoff()
	h.EventID = ""
	if cerr.KindOf(h.Validate()) != cerr.KindInvalid {
		t.Fatalf("empty event id: %v", h.Validate())
	}
	h = validHandoff()
	h.Kind = ""
	if cerr.KindOf(h.Validate()) != cerr.KindInvalid {
		t.Fatalf("empty kind: %v", h.Validate())
	}
	h = validHandoff()
	h.Provider = ""
	if cerr.KindOf(h.Validate()) != cerr.KindInvalid {
		t.Fatalf("empty provider: %v", h.Validate())
	}
}

func TestValidate_DeleteWithoutEntityBodyIsValid(t *testing.T) {
	h := validHandoff()
	h.Kind = "issue.deleted"
	h.Entity = Entity{}
	if err := h.Validate(); err != nil {
		t.Fatalf("delete without entity body: %v", err)
	}
}

func TestVerifyHMACSHA256_AcceptsMatchingAndRefusesMismatched(t *testing.T) {
	secret := []byte("placeholder-hmac-secret")
	body := []byte(`{"event_id":"evt-1"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	raw := Raw{PayloadVersion: "2", Signature: sig, Body: body}
	got, err := VerifyHMACSHA256(raw, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Body, body) || got.PayloadVersion != "2" {
		t.Fatalf("verified=%+v", got)
	}
	raw.Signature = hex.EncodeToString(make([]byte, 32))
	if _, err := VerifyHMACSHA256(raw, secret); cerr.KindOf(err) != cerr.KindUnauthorized {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := VerifyHMACSHA256(Raw{Body: body}, secret); cerr.KindOf(err) != cerr.KindUnauthorized {
		t.Fatalf("missing signature: %v", err)
	}
	if _, err := VerifyHMACSHA256(raw, nil); cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("empty secret: %v", err)
	}
}

func TestVerifyHMACSHA256_DoesNotEchoSecret(t *testing.T) {
	secret := []byte("placeholder-hmac-secret-must-not-leak")
	_, err := VerifyHMACSHA256(Raw{Body: []byte("{}"), Signature: "00"}, secret)
	if err == nil {
		t.Fatal("expected refusal")
	}
	if strings.Contains(err.Error(), string(secret)) {
		t.Fatal("error echoed the secret")
	}
}

func TestBind_RequiresVerifiedPayloadVersionToMatchHandoff(t *testing.T) {
	v := Verified{PayloadVersion: "2", Body: []byte(`{}`)}
	h := validHandoff()
	h.PayloadVersion = "2"
	if err := Bind(v, h); err != nil {
		t.Fatal(err)
	}
	h.PayloadVersion = "1"
	if cerr.KindOf(Bind(v, h)) != cerr.KindUnsupported {
		t.Fatalf("version mismatch: %v", Bind(v, h))
	}
}

func TestCheckBodySize_RefusesOversized(t *testing.T) {
	if err := CheckBodySize(make([]byte, MaxBody)); err != nil {
		t.Fatal(err)
	}
	if cerr.KindOf(CheckBodySize(make([]byte, MaxBody+1))) != cerr.KindInvalid {
		t.Fatalf("oversized: %v", CheckBodySize(make([]byte, MaxBody+1)))
	}
}

func TestGoldenFixtures_RetriesAbsentTimesAndCrossInstance(t *testing.T) {
	a := loadHandoff(t, "retry-delivery-a.json")
	b := loadHandoff(t, "retry-delivery-b.json")
	if a.EventID != b.EventID {
		t.Fatalf("retry fixtures split event id %q vs %q", a.EventID, b.EventID)
	}
	if a.DeliveryID == b.DeliveryID {
		t.Fatal("retry fixtures reused delivery id")
	}
	if a.DedupeKey() != b.DedupeKey() {
		t.Fatalf("retry dedupe %q vs %q", a.DedupeKey(), b.DedupeKey())
	}
	absent := loadHandoff(t, "absent-times.json")
	if absent.Occurred.Known || absent.Observed.Known {
		t.Fatal("absent-times invented a timestamp")
	}
	if err := absent.Validate(); err != nil {
		t.Fatal(err)
	}
	cross := loadHandoff(t, "cross-instance.json")
	if a.DedupeKey() == cross.DedupeKey() {
		t.Fatal("cross-instance fixture shared the retry dedupe key")
	}
	bad, err := os.ReadFile(filepath.Join("testdata", "unsupported-version.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := Decode(bad)
	if err != nil {
		t.Fatal(err)
	}
	if cerr.KindOf(h.Validate()) != cerr.KindUnsupported {
		t.Fatalf("unsupported version: %v", h.Validate())
	}
}

func TestProductionImports_ExcludeHTTPAndVendor(t *testing.T) {
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	n := 0
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		n++
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(path, "plane") || path == "net/http" || strings.Contains(path, "arqtos-cli") {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
	if n == 0 {
		t.Fatal("no production files examined")
	}
}

func TestPackage_DoesNotExportHostConcerns(t *testing.T) {
	pkg := reflect.TypeOf(Handoff{})
	for _, name := range []string{"Ack", "Acknowledge", "Persist", "Inbox", "Ledger"} {
		if _, ok := pkg.MethodByName(name); ok {
			t.Errorf("Handoff exported host concern %s", name)
		}
	}
}

func validHandoff() Handoff {
	return Handoff{
		SchemaVersion:  SchemaVersion,
		Provider:       "placeholder-provider",
		Realm:          "https://plane.example.test",
		Workspace:      "placeholder-workspace",
		EventID:        "evt-stable-1",
		DeliveryID:     "del-attempt-a",
		Kind:           "issue.created",
		PayloadVersion: "2",
		Entity:         Entity{Scope: "placeholder-project", Number: 7},
	}
}

func loadHandoff(t *testing.T, name string) Handoff {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	h, err := Decode(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := h.Validate(); err != nil && name != "unsupported-version.json" {
		t.Fatalf("%s validate: %v", name, err)
	}
	return h
}

func methodNames(typ reflect.Type) []string {
	out := make([]string, 0, typ.NumMethod())
	for i := range typ.NumMethod() {
		out = append(out, typ.Method(i).Name)
	}
	slices.Sort(out)
	return out
}

func TestDecode_RejectsUnknownFields(t *testing.T) {
	_, err := Decode([]byte(`{"schema_version":1,"tenant_secret":"nope"}`))
	if err == nil {
		t.Fatal("unknown field accepted")
	}
}
