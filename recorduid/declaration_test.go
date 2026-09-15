package recorduid_test

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/recorduid"
)

func TestDeclaration_WhenGoldenWireInputsAreDecoded_PreservesContract(t *testing.T) {
	data, err := os.ReadFile("testdata/declarations.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Declaration recorduid.Declaration `json:"declaration"`
		Label       string                `json:"label"`
		Ordinal     string                `json:"ordinal"`
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 3 {
		t.Fatalf("fixture count %d; want 3", len(fixtures))
	}
	for _, f := range fixtures {
		want, err := strconv.ParseUint(f.Ordinal, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.Declaration.Parse(f.Label)
		if err != nil || got != want {
			t.Fatalf("golden Parse %q = %d, %v; want %d", f.Label, got, err, want)
		}
		if f.Declaration.Mode == recorduid.Issuing {
			label, err := f.Declaration.Render(want)
			if err != nil || label != f.Label {
				t.Fatalf("golden Render = %q, %v; want %q", label, err, f.Label)
			}
		}
	}
}

func TestDeclaration_WhenJSONRepositoryIDIsNumeric_RefusesTypeCoercion(t *testing.T) {
	for _, token := range []string{"42", "9007199254740993", "1e3", "42.0", "true", "[]", "{}"} {
		var d recorduid.Declaration
		err := json.Unmarshal([]byte(`{"namespace":{"repository_id":`+token+`}}`), &d)
		requireReason(t, err, "cannot unmarshal")
	}
}

func TestValidateSet_WhenRevisionIsInconsistent_Refuses(t *testing.T) {
	d := simple()
	other := d
	other.Kind = "custom"
	requireReason(t, recorduid.ValidateSet([]recorduid.Declaration{d, other}), "declaration revision")
	other = d
	other.Namespace.ProjectRef = "project-other"
	requireReason(t, recorduid.ValidateSet([]recorduid.Declaration{d, other}), "declaration revision")
	other.Schema = ""
	requireReason(t, recorduid.ValidateSet([]recorduid.Declaration{d, other}), "schema")
}

func TestValidateSet_WhenRepositoryKindGetsSecondSequence_Refuses(t *testing.T) {
	d := full()
	other := d
	other.SequenceID, other.Namespace.KindPrefix = "seq-other", "another"
	requireReason(t, recorduid.ValidateSet([]recorduid.Declaration{d, other}), "repository/kind")
}

func TestDeclarationRender_WhenHostOrRepositoryDiffers_SeparatesLabels(t *testing.T) {
	d := full()
	other := d
	other.SequenceID, other.Namespace.HostKey = "seq-other", "another"
	third := d
	third.SequenceID, third.Namespace.RepositoryID = "seq-third", "42"
	declarations := []recorduid.Declaration{d, other, third}
	if err := recorduid.ValidateSet(declarations); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, candidate := range declarations {
		label, err := candidate.Render(1)
		if err != nil || seen[label] {
			t.Fatalf("independent namespace collision: %q, %v", label, err)
		}
		seen[label] = true
	}
}

func TestDeclarationRender_WhenNativeSpellingsAreDistinct_EncodingIsInjective(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range []string{"a-b", "a%2db", "a%2Db", "A", "a", "é", "e\u0301", "%41"} {
		d := full()
		d.Namespace.RepositoryID = id
		label, err := d.Render(1)
		if err != nil || seen[label] {
			t.Fatalf("native %q collides at %q, %v", id, label, err)
		}
		seen[label] = true
	}
}
