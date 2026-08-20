package contracts_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
)

var update = flag.Bool("update", false, "rewrite the golden fixtures")

// goldens pairs each record kind with a hand-written specimen and the files that
// describe it. ⚠️ The map is the one place a kind is listed for testing, and
// TestGoldens_CoverEveryKind fails if it and contracts.Kinds() disagree — so a
// fifth kind cannot be added and silently tested by nothing.
var goldens = map[contracts.Kind]struct {
	fixture string
	schema  string
	value   any
}{
	contracts.KindActSpec: {"actspec.v1.json", "actspec.v1.schema.json", contracts.ActSpec{
		SchemaVersion:    contracts.SchemaVersionNumber,
		Kind:             contracts.KindActSpec,
		ActBodyID:        "sha256:0f7c3a91d2b4e5f60718293a4b5c6d7e8f9012345678abcdef0123456789abcd",
		ActKind:          "governed_merge",
		RepositoryID:     "repo:example/governed",
		ChangeRequestID:  "cr:example/governed/41",
		HeadSHA:          "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
		BaseConstraint:   "refs/heads/main@9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c",
		MergeMethod:      "squash",
		CandidateTreeOID: "tree:5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7081",
		CharterDigest:    "sha256:abc0000000000000000000000000000000000000000000000000000000000001",
		CharterRuleID:    "rule:low-risk-docs",
		Manifest: []contracts.ManifestEntry{
			{Path: "docs/threat-model.md", ContentDigest: "sha256:def0000000000000000000000000000000000000000000000000000000000002"},
		},
		Permit: contracts.PermitID{
			IssuerActBodyID: "sha256:1110000000000000000000000000000000000000000000000000000000000003",
			OutputIndex:     contracts.FromInt(0),
		},
		Footprint: contracts.Footprint{
			Reads:          []contracts.ResourceRead{{ResourceID: "charter:example/governed", ExpectedVersion: contracts.FromInt(7)}},
			Writes:         []contracts.ResourceWrite{{ResourceID: "ref:refs/heads/main", ExpectedVersion: contracts.FromInt(41), NewDigest: "sha256:2220000000000000000000000000000000000000000000000000000000000004"}},
			NamespaceRoots: []string{"lane:docs"},
		},
		Nonce:          "01a01578-ec47-7209-9200-cac2a1f75c7f",
		ValidUntil:     time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC),
		ReducerVersion: "reduce/v0",
	}},

	contracts.KindWitness: {"witness.v1.json", "witness.v1.schema.json", contracts.Witness{
		SchemaVersion: contracts.SchemaVersionNumber,
		Kind:          contracts.KindWitness,
		ActBodyID:     "sha256:0f7c3a91d2b4e5f60718293a4b5c6d7e8f9012345678abcdef0123456789abcd",
		WitnessKind:   contracts.WitnessRatification,
		Principal:     "principal:operator",
		KeyID:         "key:operator-2026-08",
		Signature:     "base64:c2lnbmF0dXJlLXBsYWNlaG9sZGVy",
		At:            time.Date(2026, 8, 20, 9, 30, 0, 0, time.UTC),
		TimeSource:    "authority:host-clock",
	}},

	contracts.KindEvidenceEvent: {"evidence_event.v1.json", "evidence_event.v1.schema.json", contracts.EvidenceEvent{
		SchemaVersion: contracts.SchemaVersionNumber,
		Kind:          contracts.KindEvidenceEvent,
		EventKind:     contracts.EventReceipt,
		ActBodyID:     "sha256:0f7c3a91d2b4e5f60718293a4b5c6d7e8f9012345678abcdef0123456789abcd",
		Sequence:      contracts.FromInt(42),
		AcceptedTime: contracts.AcceptedTime{
			At: time.Date(2026, 8, 20, 9, 31, 0, 0, time.UTC),
			Authority: contracts.TimeAuthority{
				Name:       "authority:host-clock",
				Provenance: contracts.ClockSynchronised,
			},
		},
		// ⚠️ Bound at effect time. The final commit does not exist before a
		// squash merge, which is why binding it at decision time was
		// unimplementable.
		FinalSHA:        "7f8e9d0c1b2a3948576675849302f1e0d9c8b7a6",
		HostOperationID: "host-op:merge-9931",
		Detail:          map[string]string{"outcome": "settled"},
	}},

	contracts.KindStatusView: {"status_view.v1.json", "status_view.v1.schema.json", contracts.StatusView{
		SchemaVersion:          contracts.SchemaVersionNumber,
		Kind:                   contracts.KindStatusView,
		RebuiltThroughSequence: contracts.FromInt(42),
		Entries: []contracts.StatusEntry{{
			ActBodyID:      "sha256:0f7c3a91d2b4e5f60718293a4b5c6d7e8f9012345678abcdef0123456789abcd",
			State:          "settled",
			DesiredSpecID:  "sha256:3330000000000000000000000000000000000000000000000000000000000005",
			ObservedSpecID: "sha256:3330000000000000000000000000000000000000000000000000000000000005",
		}},
	}},
}

func fixturePath(name string) string { return filepath.Join("testdata", name) }
func schemaPath(name string) string  { return filepath.Join("schema", name) }

// ⚠️ The map above is a SECOND place the kind set is written down, which is the
// exact defect this file exists to catch elsewhere. This is the guard.
func TestGoldens_CoverEveryKind(t *testing.T) {
	var covered []string
	for k := range goldens {
		covered = append(covered, string(k))
	}
	var published []string
	for _, k := range contracts.Kinds() {
		published = append(published, string(k))
	}
	sort.Strings(covered)
	sort.Strings(published)

	if !slices.Equal(covered, published) {
		t.Fatalf("goldens covers %v; contracts.Kinds() publishes %v.\nAdd the new kind here as well, "+
			"or it ships with no fixture, no schema check and no round-trip test.", covered, published)
	}
	t.Logf("examined %d kind(s)", len(published))
}

// TestGoldens_RoundTrip is the byte-level contract: the committed fixture is
// exactly what the type produces, so a field rename or a reordering that would
// change the wire form cannot land unnoticed.
//
// Run with -update to rewrite the fixtures deliberately.
func TestGoldens_RoundTrip(t *testing.T) {
	for kind, g := range goldens {
		t.Run(string(kind), func(t *testing.T) {
			encoded, err := contracts.Encode(g.value)
			if err != nil {
				t.Fatalf("encoding: %v", err)
			}

			if *update {
				if err := os.WriteFile(fixturePath(g.fixture), encoded, 0o644); err != nil {
					t.Fatalf("updating fixture: %v", err)
				}
				t.Logf("updated %s", g.fixture)
				return
			}

			want, err := os.ReadFile(fixturePath(g.fixture))
			if err != nil {
				t.Fatalf("reading fixture: %v — run `go test ./contracts -update` to create it", err)
			}
			if string(encoded) != string(want) {
				t.Errorf("%s does not match the type's encoding.\n--- got ---\n%s\n--- want ---\n%s",
					g.fixture, encoded, want)
			}
		})
	}
}

// ⚠️ THE ONE THAT MATTERS MOST. A field the verifier skips is a field the runtime
// may have ACTED ON, so the two agree on a hash while disagreeing on meaning.
// Rejection turns that into a loud failure at the boundary rather than a silent
// one at the conclusion.
func TestDecode_RejectsAnUnknownField(t *testing.T) {
	raw, err := os.ReadFile(fixturePath("actspec.v1.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	var loose map[string]any
	if err := json.Unmarshal(raw, &loose); err != nil {
		t.Fatalf("loosening fixture: %v", err)
	}
	loose["a_field_from_the_future"] = "surprise"
	mutated, err := json.Marshal(loose)
	if err != nil {
		t.Fatalf("re-marshalling: %v", err)
	}

	if _, err := contracts.Decode[contracts.ActSpec](mutated); err == nil {
		t.Fatal("Decode accepted a record carrying an unknown field. That field may be one the " +
			"producer acted on, so accepting it means agreeing on the bytes while disagreeing on " +
			"the meaning — which surfaces on the disputed act, months later.")
	}

	// The unmutated fixture must still decode, or the test above proves nothing.
	if _, err := contracts.Decode[contracts.ActSpec](raw); err != nil {
		t.Fatalf("Decode rejected the unmodified fixture: %v", err)
	}
}

// Two concatenated records must not decode as the first one: a reader that
// silently dropped the remainder would report a partial bundle as a whole one.
func TestDecode_RejectsTrailingContent(t *testing.T) {
	raw, err := os.ReadFile(fixturePath("witness.v1.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	doubled := append(append([]byte{}, raw...), raw...)

	if _, err := contracts.Decode[contracts.Witness](doubled); err == nil {
		t.Fatal("Decode accepted two concatenated records as one")
	}
}

// ⚠️ The Go type and the JSON Schema are a SECOND copy of one fact, and a second
// copy of a fact is a second thing to drift. This checks them against each other
// in BOTH directions — a property in the schema with no field, and a field with
// no property, are both drift, and only one of them is the direction people
// remember to check.
func TestSchema_AgreesWithTheGoType(t *testing.T) {
	for kind, g := range goldens {
		t.Run(string(kind), func(t *testing.T) {
			rawSchema, err := os.ReadFile(schemaPath(g.schema))
			if err != nil {
				t.Fatalf("reading schema: %v", err)
			}
			var doc struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
				Additional *bool                      `json:"additionalProperties"`
			}
			if err := json.Unmarshal(rawSchema, &doc); err != nil {
				t.Fatalf("parsing schema: %v", err)
			}

			// ⚠️ additionalProperties MUST be false. The schema is what a
			// non-Go consumer validates against, and a permissive schema would
			// admit exactly the unknown field Decode refuses — leaving two
			// consumers of one contract disagreeing about what is valid.
			if doc.Additional == nil || *doc.Additional {
				t.Errorf("%s does not set additionalProperties:false — a non-Go consumer would accept "+
					"an unknown field that Decode rejects", g.schema)
			}

			fields := jsonFieldNames(reflect.TypeOf(g.value))
			var inSchema []string
			for p := range doc.Properties {
				inSchema = append(inSchema, p)
			}
			sort.Strings(fields)
			sort.Strings(inSchema)

			if !slices.Equal(fields, inSchema) {
				t.Errorf("%s and the Go type disagree.\n  schema: %v\n  go:     %v", g.schema, inSchema, fields)
			}

			for _, r := range doc.Required {
				if !slices.Contains(fields, r) {
					t.Errorf("%s requires %q, which the Go type does not have", g.schema, r)
				}
			}
		})
	}
}

// Every kind carries a schema_version, and it is the package's version. A record
// without one cannot be migrated, because nothing says what it is.
func TestEveryKind_CarriesItsSchemaVersion(t *testing.T) {
	for kind, g := range goldens {
		t.Run(string(kind), func(t *testing.T) {
			raw, err := os.ReadFile(fixturePath(g.fixture))
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("parsing fixture: %v", err)
			}
			v, ok := m["schema_version"]
			if !ok {
				t.Fatalf("%s has no schema_version", g.fixture)
			}
			// ⚠️ A STRING, not a number. The canonical encoding forbids JSON
			// numbers, so a fixture carrying schema_version as one would be a
			// document this package's own encoder refuses.
			str, ok := v.(string)
			if !ok {
				t.Fatalf("%s carries schema_version as %T; it must be a string — the canonical encoding "+
					"forbids JSON numbers, so a numeric value here cannot be digested", g.fixture, v)
			}
			if contracts.Number(str) != contracts.SchemaVersionNumber {
				t.Errorf("%s carries schema_version %q, want %q", g.fixture, str, contracts.SchemaVersionNumber)
			}
			if m["kind"] != string(kind) {
				t.Errorf("%s carries kind %v, want %q", g.fixture, m["kind"], kind)
			}
		})
	}
}

// The record-layer mutation matrix, as a test rather than as prose. ⚠️ An
// unknown kind must report Unspecified rather than inheriting the loosest rule
// in the table, which is what a permissive default would do.
func TestMutationMatrix(t *testing.T) {
	want := map[contracts.Kind]contracts.Mutability{
		contracts.KindActSpec:       contracts.Immutable,
		contracts.KindWitness:       contracts.AppendOnly,
		contracts.KindEvidenceEvent: contracts.AppendOnly,
		contracts.KindStatusView:    contracts.Disposable,
	}

	for _, k := range contracts.Kinds() {
		got := contracts.MutabilityOf(k)
		if got != want[k] {
			t.Errorf("MutabilityOf(%s) = %s, want %s", k, got, want[k])
		}
		if got == contracts.MutabilityUnspecified {
			t.Errorf("%s has no mutability — the matrix forgot a published kind", k)
		}
	}

	if got := contracts.MutabilityOf(contracts.Kind("something_new")); got != contracts.MutabilityUnspecified {
		t.Errorf("an unknown kind reported %s; it must report unspecified rather than inherit a default", got)
	}

	// ⚠️ Only the projection is disposable. If a second layer ever becomes
	// disposable, verification stops being reproducible from the layers that
	// remain — so this is asserted as a count, not read off by eye.
	var disposable int
	for _, k := range contracts.Kinds() {
		if contracts.MutabilityOf(k) == contracts.Disposable {
			disposable++
		}
	}
	if disposable != 1 {
		t.Errorf("%d kinds are disposable; exactly one (the projection) may be", disposable)
	}
}

func TestKind_ValidRejectsAnythingOutsideTheClosedSet(t *testing.T) {
	for _, k := range contracts.Kinds() {
		if !k.Valid() {
			t.Errorf("published kind %q is not Valid()", k)
		}
	}
	for _, k := range []contracts.Kind{"", "actspecs", "ActSpec", "meter_row"} {
		if contracts.Kind(k).Valid() {
			t.Errorf("%q is Valid(), but it is outside the closed set", k)
		}
	}
}

// jsonFieldNames returns the wire names of t's exported fields, in declaration
// order, skipping anything marked "-".
func jsonFieldNames(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out = append(out, name)
	}
	return out
}

// String must never return the empty string. A blank would print as an ABSENT
// field rather than a broken one — and a mutation rule that renders as nothing
// reads, on a report, as a record with no mutation rule at all.
func TestMutability_StringNamesEveryValueAndMarksTheRest(t *testing.T) {
	named := map[contracts.Mutability]string{
		contracts.MutabilityUnspecified: "unspecified",
		contracts.Immutable:             "immutable",
		contracts.AppendOnly:            "append_only",
		contracts.Disposable:            "disposable",
	}
	for m, want := range named {
		if got := m.String(); got != want {
			t.Errorf("Mutability(%d).String() = %q, want %q", int(m), got, want)
		}
	}

	for _, m := range []contracts.Mutability{99, -1} {
		got := m.String()
		if got == "" {
			t.Errorf("Mutability(%d).String() is empty — it would print as an absent field", int(m))
		}
		if !strings.HasPrefix(got, "invalid") {
			t.Errorf("Mutability(%d).String() = %q; want an explicit invalid marker", int(m), got)
		}
	}
}

// Encode must report a value it cannot render rather than returning empty bytes
// and a nil error — the empty-success shape this estate refuses everywhere else.
func TestEncode_ReportsAValueItCannotRender(t *testing.T) {
	out, err := contracts.Encode(make(chan int))
	if err == nil {
		t.Fatal("Encode accepted an unencodable value")
	}
	if len(out) != 0 {
		t.Errorf("Encode returned %d byte(s) alongside an error; a failed encode must not hand back partial output", len(out))
	}
}
