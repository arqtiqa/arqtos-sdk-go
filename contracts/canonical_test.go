package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
)

// ⚠️ THE RULE THAT WAS BROKEN, NOW ENFORCED. Every record in this package is
// digested with the canonical encoding, and that encoding forbids JSON numbers.
// An int-typed field makes its record UNENCODABLE, and nothing says so until the
// first time an identity is computed — which is why this shipped once already
// with seven numeric fields on the public act type.
//
// The walk is recursive because the field that gets added is never the one at
// the top level; it is an int inside a slice of structs three levels down.
func TestEveryRecordType_CarriesNoNumericField(t *testing.T) {
	types := map[string]any{
		"ActSpec":           contracts.ActSpec{},
		"Witness":           contracts.Witness{},
		"EvidenceEvent":     contracts.EvidenceEvent{},
		"StatusView":        contracts.StatusView{},
		"RepositoryGenesis": contracts.RepositoryGenesis{},
		"PermitID":          contracts.PermitID{},
		"Footprint":         contracts.Footprint{},
		"AcceptedTime":      contracts.AcceptedTime{},
		"Grant":             contracts.Grant{},
	}

	leaves := 0
	marshalers := 0
	var walk func(t *testing.T, typ reflect.Type, path string, depth int)
	walk = func(t *testing.T, typ reflect.Type, path string, depth int) {
		if depth > 10 {
			t.Fatalf("%s: the type graph is deeper than expected; the walk may not reach every field", path)
		}

		// ⚠️ A type with its OWN MarshalJSON decides its own JSON form, so its
		// Go kind says nothing — ClockProvenance is an int that marshals to a
		// name. Walking into it would be a false positive; trusting it blindly
		// would be a false negative. So the marshaler is CALLED and its output
		// checked for a number, which is the property that actually matters.
		if typ.Implements(reflect.TypeFor[json.Marshaler]()) {
			marshalers++
			raw, err := json.Marshal(reflect.New(typ).Elem().Interface())
			if err != nil {
				// A zero value its own marshaler refuses is fine — the
				// contracts here deliberately refuse unstated values.
				return
			}
			if len(raw) > 0 && (raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9')) {
				t.Errorf("%s marshals to the JSON number %s. The canonical encoding forbids numbers.", path, raw)
			}
			return
		}

		switch typ.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			leaves++
			t.Errorf("%s is %s. The canonical encoding forbids JSON numbers, so this field makes its "+
				"record unencodable — and nothing says so until an identity is computed. Carry it as "+
				"contracts.Number.", path, typ.Kind())
		case reflect.Slice, reflect.Array, reflect.Pointer:
			walk(t, typ.Elem(), path+"[]", depth+1)
		case reflect.Map:
			walk(t, typ.Key(), path+"{key}", depth+1)
			walk(t, typ.Elem(), path+"{}", depth+1)
		case reflect.Struct:
			// time.Time marshals to an RFC 3339 STRING, so it is encodable —
			// and walking into its unexported fields would find int64s that
			// never reach JSON.
			if typ.PkgPath() == "time" {
				leaves++
				return
			}
			for i := range typ.NumField() {
				f := typ.Field(i)
				walk(t, f.Type, path+"."+f.Name, depth+1)
			}
		default:
			leaves++
		}
	}
	for name, v := range types {
		walk(t, reflect.TypeOf(v), name, 0)
	}

	// ⚠️ Count-asserted both ways: a walk that reached nothing would report no
	// numeric fields, and a type dropped from the map would silently stop being
	// checked.
	if len(types) != 9 {
		t.Fatalf("checked %d record types, want 9 — a type was added or dropped without a case", len(types))
	}
	if leaves < 40 {
		t.Errorf("the walk examined %d leaf fields, which is too few to have covered these types", leaves)
	}
	// ⚠️ The marshaler branch must actually fire. If it never did, the check
	// above would be walking into ClockProvenance and reporting a false
	// positive — or, worse, a future type could acquire a marshaler that emits
	// a number and this test would never look at it.
	if marshalers == 0 {
		t.Error("no type with its own MarshalJSON was examined, so that branch is untested")
	}
}

// ⚠️ EVERY TIME A RECORD CARRIES MUST BE AcceptedTime, and this exists because
// fixing them one at a time missed one. EvidenceEvent was moved onto the time
// contract in the record-encoding change; Witness was left holding a bare
// time.Time plus a string, and only an operator review caught it.
//
// A bare timestamp carries no clock provenance, so a reader cannot tell a
// disciplined clock from a virtual machine that has been suspended — and
// keyhistory answers "was this signature valid WHEN IT WAS MADE" from exactly
// these fields.
//
// ⚠️ time.Time is allowed ONLY inside AcceptedTime itself, and in ValidUntil,
// which is an expiry the signer chose rather than an observation of a clock.
func TestNoRecordCarriesABareTimestamp(t *testing.T) {
	timeType := reflect.TypeOf(time.Time{})
	acceptedTime := reflect.TypeOf(contracts.AcceptedTime{})

	// ⚠️ Keyed on the OWNING TYPE and field, not on a path string. A path is
	// ambiguous — Grant.NotAfter.At and Witness.At.At both end in ".At" — and an
	// allow-list that cannot be matched is an allow-list that fails on correct
	// code, which is how this test failed on its first run.
	//
	// The one exception: ValidUntil is an expiry the SIGNER CHOSE, not an
	// observation of a clock, so it has no provenance to carry.
	allowed := map[string]bool{"ActSpec.ValidUntil": true}

	found, bare := 0, 0
	var walk func(typ reflect.Type, owner, field string, depth int)
	walk = func(typ reflect.Type, owner, field string, depth int) {
		if depth > 8 {
			t.Fatalf("%s.%s: deeper than expected", owner, field)
		}
		switch typ.Kind() {
		case reflect.Slice, reflect.Array, reflect.Pointer:
			walk(typ.Elem(), owner, field, depth+1)
		case reflect.Struct:
			// AcceptedTime is the sanctioned carrier. Its own At is the point
			// of it, so the walk stops here rather than reporting itself.
			if typ == acceptedTime {
				found++
				return
			}
			if typ == timeType {
				found++
				if !allowed[owner+"."+field] {
					bare++
					t.Errorf("%s.%s is a bare time.Time. Use contracts.AcceptedTime, which carries the "+
						"authority and its clock provenance — a timestamp of unknown provenance cannot "+
						"answer whether a signature was valid when it was made.", owner, field)
				}
				return
			}
			for i := range typ.NumField() {
				f := typ.Field(i)
				walk(f.Type, typ.Name(), f.Name, depth+1)
			}
		}
	}
	for _, v := range []any{
		contracts.ActSpec{}, contracts.Witness{}, contracts.EvidenceEvent{},
		contracts.StatusView{}, contracts.RepositoryGenesis{}, contracts.Grant{},
	} {
		typ := reflect.TypeOf(v)
		walk(typ, typ.Name(), "", 0)
	}

	// ⚠️ Count-asserted: a walk that found no time fields at all would report
	// no bare ones and read exactly like a clean set.
	if found < 4 {
		t.Errorf("the walk found %d time-carrying field(s), which is too few — it is not reaching them", found)
	}
	if bare == 0 {
		t.Logf("examined %d time-carrying field(s); none bare", found)
	}
}

// ⭐ THE VERTICAL SLICE, which did not exist before: the PUBLIC act type, through
// the canonical encoder, to an act body id.
//
// ⚠️ Until this passed, the DSSE tests were hashing a hand-made all-string map
// and the fixture act_body_ids were literals — so "canonical encoding + public
// contracts" was two systems that had never met.
func TestActSpec_IsCanonicallyEncodableEndToEnd(t *testing.T) {
	spec := goldens[contracts.KindActSpec].value.(contracts.ActSpec)

	id, err := canonical.ActBodyID(spec)
	if err != nil {
		t.Fatalf("the public act type cannot be canonically encoded: %v", err)
	}
	if !strings.HasPrefix(id, canonical.HashName+":") {
		t.Errorf("act body id %q does not name its hash algorithm", id)
	}

	again, err := canonical.ActBodyID(spec)
	if err != nil || again != id {
		t.Fatalf("the act body id is not stable: %q then %q (%v)", id, again, err)
	}

	// Every field must reach the digest, or it is a field an attacker may change
	// under a signature. The two that were integers are checked explicitly.
	changed := spec
	changed.Permit.OutputIndex = contracts.FromInt(1)
	if other, err := canonical.ActBodyID(changed); err != nil || other == id {
		t.Errorf("changing the permit output index did not change the act body id (%v)", err)
	}

	// ⚠️ The READS SLICE IS COPIED before it is mutated. A struct copy shares a
	// slice's backing array, so writing through `changed` would edit the shared
	// golden — and the damage lands in whichever test runs NEXT, as a golden
	// mismatch that looks like an encoding change. This exact mistake failed
	// TestGoldens_RoundTrip while this test passed in isolation.
	changed = spec
	changed.Footprint.Reads = append([]contracts.ResourceRead(nil), spec.Footprint.Reads...)
	changed.Footprint.Reads[0].ExpectedVersion = contracts.FromInt(8)
	if other, err := canonical.ActBodyID(changed); err != nil || other == id {
		t.Errorf("changing a declared read version did not change the act body id (%v)", err)
	}

	// And the golden is unharmed, so a later test cannot inherit this one's edit.
	if spec.Footprint.Reads[0].ExpectedVersion != goldens[contracts.KindActSpec].value.(contracts.ActSpec).Footprint.Reads[0].ExpectedVersion {
		t.Fatal("this test mutated the shared golden")
	}
}

func TestEveryRecordType_IsCanonicallyEncodable(t *testing.T) {
	n := 0
	for kind, g := range goldens {
		t.Run(string(kind), func(t *testing.T) {
			if _, err := canonical.Encode(g.value); err != nil {
				t.Fatalf("%s cannot be canonically encoded: %v", kind, err)
			}
		})
		n++
	}
	if n != 4 {
		t.Fatalf("encoded %d kinds, want 4", n)
	}
}

// ⚠️ The SCHEMA is what a non-Go consumer validates against. A schema admitting
// a JSON number would admit a document this project's own encoder refuses —
// leaving two consumers of one contract disagreeing about what is valid, which
// is the same failure additionalProperties:false already guards against.
func TestNoSchema_AdmitsAJSONNumber(t *testing.T) {
	dir := filepath.Join("schema")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the schema directory: %v", err)
	}
	examined := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parsing %s: %v", e.Name(), err)
		}
		examined++
		var walk func(v any, path string)
		walk = func(v any, path string) {
			switch t2 := v.(type) {
			case map[string]any:
				if s, ok := t2["type"].(string); ok && (s == "integer" || s == "number") {
					t.Errorf("%s%s declares type %q — the canonical encoding forbids JSON numbers, so "+
						"this schema admits a document the encoder refuses", e.Name(), path, s)
				}
				for k, v2 := range t2 {
					walk(v2, path+"/"+k)
				}
			case []any:
				for i, v2 := range t2 {
					walk(v2, path+"/"+string(rune('0'+i)))
				}
			}
		}
		walk(doc, "")
	}
	if examined != 4 {
		t.Fatalf("examined %d schemas, want 4 — a schema was added or renamed without updating this test", examined)
	}
}

// The Number type's own rule: a non-canonical spelling is refused, because two
// spellings of one value in digested bytes is the ambiguity it exists to remove.
func TestNumber_RefusesANonCanonicalSpelling(t *testing.T) {
	for name, n := range map[string]contracts.Number{
		"padded":   "007",
		"plus":     "+7",
		"float":    "1.0",
		"words":    "seven",
		"empty":    "",
		"spaced":   " 7",
		"exponent": "1e3",
	} {
		t.Run(name, func(t *testing.T) {
			if n.Valid() {
				t.Fatalf("Number(%q) reports itself valid", string(n))
			}
		})
	}
	for _, n := range []contracts.Number{"0", "7", "-7", "9223372036854775807"} {
		if !n.Valid() {
			t.Errorf("Number(%q) was refused", string(n))
		}
		if got, err := n.Int(); err != nil || contracts.FromInt(got) != n {
			t.Errorf("Number(%q) does not round-trip: %d %v", string(n), got, err)
		}
	}
}
