package skillspec_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/skillspec"
)

// publishedYAMLNames is the public skill.yml key set from the catalogue
// validator and the keys that actually appear on published artefacts.
// Adding a name here without a matching yaml tag on Skill is the discard
// this suite exists to catch.
var publishedYAMLNames = []string{
	"name",
	"version",
	"visibility",
	"description",
	"capability_class",
	"mount_priority",
	"fine_tune",
	"eligible",
	"required_corpus_classes",
	"retune_triggers",
	"adjacent_skills",
	"sensitivity_required",
	"max_classification",
	"audit_tier",
	"cool_down_turns",
	"churn_class",
	"paths",
	"pinned",
	"structure",
	"triggers",
	"fine_tune_compatible",
}

func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatalf("testdata/%s is empty", rel)
	}
	return b
}

func parseAndValidate(t *testing.T, rel string) (skillspec.Skill, error) {
	t.Helper()
	s, err := skillspec.Parse(fixture(t, rel))
	if err != nil {
		return s, err
	}
	return s, s.Validate()
}

func yamlTags(typ reflect.Type, out map[string]bool) {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
		yamlTags(f.Type, out)
	}
}

func fieldByYAML(v reflect.Value, name string) (reflect.Value, bool) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.Value{}, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
		if tag == name {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

func mustYAML(t *testing.T, v reflect.Value, name string) reflect.Value {
	t.Helper()
	f, ok := fieldByYAML(v, name)
	if !ok {
		t.Fatalf("no yaml:%q field; declared %s would be discarded", name, name)
	}
	return f
}

func yamlString(t *testing.T, v reflect.Value, name string) string {
	t.Helper()
	f := mustYAML(t, v, name)
	if f.Kind() != reflect.String {
		t.Fatalf("yaml:%s is %s, want string", name, f.Kind())
	}
	return f.String()
}

func yamlInt(t *testing.T, v reflect.Value, name string) int {
	t.Helper()
	f := mustYAML(t, v, name)
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			t.Fatalf("yaml:%s is nil; declared integer was discarded", name)
		}
		f = f.Elem()
	}
	switch f.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(f.Int())
	default:
		t.Fatalf("yaml:%s is %s, want integer", name, f.Kind())
		return 0
	}
}

func yamlBool(t *testing.T, v reflect.Value, name string) bool {
	t.Helper()
	f := mustYAML(t, v, name)
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			t.Fatalf("yaml:%s is nil; declared boolean was discarded", name)
		}
		f = f.Elem()
	}
	if f.Kind() != reflect.Bool {
		t.Fatalf("yaml:%s is %s, want bool", name, f.Kind())
	}
	return f.Bool()
}

func yamlStrings(t *testing.T, v reflect.Value, name string) []string {
	t.Helper()
	f := mustYAML(t, v, name)
	if f.Kind() != reflect.Slice {
		t.Fatalf("yaml:%s is %s, want slice", name, f.Kind())
	}
	out := make([]string, f.Len())
	for i := 0; i < f.Len(); i++ {
		elem := f.Index(i)
		if elem.Kind() != reflect.String {
			t.Fatalf("yaml:%s[%d] is %s, want string", name, i, elem.Kind())
		}
		out[i] = elem.String()
	}
	return out
}

func errNamesField(err error, field string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if !strings.Contains(msg, field) {
		return false
	}
	// yaml.v3 KnownFields reports "field <name> not found". That is the
	// unknown-field path, not validation of a supported field's value.
	return !strings.Contains(msg, "not found")
}

func TestSkill_DeclaresPublishedYAMLFields(t *testing.T) {
	tags := map[string]bool{}
	yamlTags(reflect.TypeOf(skillspec.Skill{}), tags)
	if len(tags) == 0 {
		t.Fatal("Skill declares no yaml tags")
	}
	for _, name := range publishedYAMLNames {
		if !tags[name] {
			t.Errorf("yaml:%s is missing from Skill; a published artefact declaring it is rejected or dropped", name)
		}
	}
}

func TestParse_ExistingMinimalSpecimen_RemainsValid(t *testing.T) {
	s, err := parseAndValidate(t, "valid/minimal.yml")
	if err != nil {
		t.Fatalf("existing minimal specimen must remain valid: %v", err)
	}
	if yamlString(t, reflect.ValueOf(s), "name") != "berg-authoring" {
		t.Fatalf("name = %q", yamlString(t, reflect.ValueOf(s), "name"))
	}
	if yamlString(t, reflect.ValueOf(s), "description") != "author berg docs" {
		t.Fatalf("description = %q", yamlString(t, reflect.ValueOf(s), "description"))
	}
}

func TestParse_PublishedCatalogueArtifact_ValidatesAndPreservesMetadata(t *testing.T) {
	s, err := parseAndValidate(t, "valid/catalogue.yml")
	if err != nil {
		t.Fatalf("published catalogue artefact must validate: %v", err)
	}

	root := reflect.ValueOf(s)
	if got := yamlString(t, root, "name"); got != "example-discipline" {
		t.Errorf("name = %q, want example-discipline", got)
	}
	if got := yamlString(t, root, "version"); got != "1.2.0" {
		t.Errorf("version = %q, want 1.2.0 — declared version was discarded", got)
	}
	if got := yamlString(t, root, "visibility"); got != "public" {
		t.Errorf("visibility = %q, want public", got)
	}
	if got := yamlString(t, root, "capability_class"); got != "tooling-discipline" {
		t.Errorf("capability_class = %q, want tooling-discipline — capability metadata was discarded", got)
	}
	if got := yamlInt(t, root, "mount_priority"); got != 7 {
		t.Errorf("mount_priority = %d, want 7", got)
	}
	if got := yamlInt(t, root, "cool_down_turns"); got != 20 {
		t.Errorf("cool_down_turns = %d, want 20", got)
	}
	if got := yamlString(t, root, "audit_tier"); got != "enhanced" {
		t.Errorf("audit_tier = %q, want enhanced", got)
	}
	if got := yamlString(t, root, "churn_class"); got != "medium" {
		t.Errorf("churn_class = %q, want medium", got)
	}
	if got := yamlString(t, root, "structure"); got != "rubric" {
		t.Errorf("structure = %q, want rubric", got)
	}
	if !yamlBool(t, root, "pinned") {
		t.Error("pinned was discarded")
	}
	if !yamlBool(t, root, "fine_tune_compatible") {
		t.Error("fine_tune_compatible was discarded")
	}
	if got := yamlStrings(t, root, "adjacent_skills"); len(got) != 1 || got[0] != "example-adjacent" {
		t.Errorf("adjacent_skills = %v", got)
	}
	if got := yamlStrings(t, root, "paths"); len(got) != 2 || got[0] != "**/*.go" {
		t.Errorf("paths = %v", got)
	}
	if got := yamlStrings(t, root, "triggers"); len(got) != 2 || got[0] != "example skill" {
		t.Errorf("triggers = %v", got)
	}

	ft := mustYAML(t, root, "fine_tune")
	if ft.Kind() == reflect.Map {
		t.Fatal("fine_tune decoded as a map; inner capability metadata is not a typed contract")
	}
	if ft.Kind() == reflect.Pointer {
		if ft.IsNil() {
			t.Fatal("fine_tune is nil; declared mapping was discarded")
		}
	}
	if !yamlBool(t, ft, "eligible") {
		t.Error("fine_tune.eligible was discarded")
	}
	if got := yamlStrings(t, ft, "required_corpus_classes"); len(got) != 1 || got[0] != "published_style_guide" {
		t.Errorf("required_corpus_classes = %v", got)
	}
	if got := yamlStrings(t, ft, "retune_triggers"); len(got) != 1 || got[0] != "schema_version_bump" {
		t.Errorf("retune_triggers = %v", got)
	}

	sens := mustYAML(t, root, "sensitivity_required")
	if sens.Kind() == reflect.Pointer && sens.IsNil() {
		t.Fatal("sensitivity_required is nil; declared mapping was discarded")
	}
	if got := yamlString(t, sens, "max_classification"); got != "confidential" {
		t.Errorf("max_classification = %q, want confidential", got)
	}

	if yamlString(t, root, "description") == "" {
		t.Error("description was discarded")
	}
}

func TestParse_UnsupportedRequiredField_IsRejectedNotDropped(t *testing.T) {
	_, err := skillspec.Parse(fixture(t, "invalid/unknown-field.yml"))
	if err == nil {
		t.Fatal("unsupported required field must not be silently dropped")
	}
	if !strings.Contains(err.Error(), "schema_required_future") {
		t.Fatalf("error must name the unsupported field: %v", err)
	}
}

func TestMalformedRequiredMetadata_ReturnsFieldSpecificError(t *testing.T) {
	cases := []struct {
		file  string
		field string
		want  string
	}{
		{"invalid/empty-name.yml", "name", "required"},
		{"invalid/empty-description.yml", "description", "required"},
		{"invalid/malformed-version.yml", "version", "semver"},
		{"invalid/malformed-visibility.yml", "visibility", "visibility"},
		{"invalid/malformed-mount-priority.yml", "mount_priority", "mount_priority"},
		{"invalid/malformed-classification.yml", "max_classification", "max_classification"},
		{"invalid/sensitivity-missing-classification.yml", "max_classification", "max_classification"},
		{"invalid/malformed-audit-tier.yml", "audit_tier", "audit_tier"},
		{"invalid/malformed-churn.yml", "churn_class", "churn_class"},
		{"invalid/malformed-structure.yml", "structure", "structure"},
		{"invalid/malformed-cool-down.yml", "cool_down_turns", "cool_down_turns"},
		{"invalid/malformed-adjacent.yml", "adjacent_skills", "adjacent_skills"},
		{"invalid/malformed-fine-tune.yml", "fine_tune", "fine_tune"},
		{"invalid/malformed-capability-class.yml", "capability_class", "capability_class"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			_, err := parseAndValidate(t, tc.file)
			if err == nil {
				t.Fatalf("malformed %s must not be accepted", tc.field)
			}
			switch tc.file {
			case "invalid/malformed-adjacent.yml", "invalid/malformed-fine-tune.yml",
				"invalid/malformed-capability-class.yml":
				// Type mismatches are Parse errors. yaml.v3 names the target
				// type more reliably than the field; either is field-specific.
				if !strings.Contains(err.Error(), tc.field) &&
					!strings.Contains(err.Error(), "unmarshal") {
					t.Fatalf("error must name %s or the type mismatch: %v", tc.field, err)
				}
			default:
				if !strings.Contains(err.Error(), tc.field) {
					t.Fatalf("error must name %s: %v", tc.field, err)
				}
			}
			switch tc.file {
			case "invalid/empty-name.yml", "invalid/empty-description.yml",
				"invalid/malformed-adjacent.yml", "invalid/malformed-fine-tune.yml",
				"invalid/malformed-capability-class.yml":
				// empty required keys already fail Validate; type mismatches
				// are Parse errors. Do not require the value-vocabulary form.
			default:
				if !errNamesField(err, tc.field) {
					t.Fatalf("%s is a supported field; the error must be about its value, not an unknown-field rejection: %v", tc.field, err)
				}
			}
			if tc.want == "semver" && !strings.Contains(err.Error(), "semver") && !strings.Contains(err.Error(), "X.Y.Z") {
				t.Fatalf("version error must describe the expected shape: %v", err)
			}
			if tc.want == "required" && !strings.Contains(err.Error(), "required") {
				t.Fatalf("missing required field must say so: %v", err)
			}
		})
	}
}

func TestPackage_IsBytesOnlyContract(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "skillspec.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.Imports == nil {
		t.Fatal("skillspec.go has no imports; Parse could not decode YAML")
	}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if !allowedSkillspecImport(path) {
			t.Errorf("skillspec imports %s; the contract is bytes in, Skill out — no filesystem discovery, version selection, or client-native emission", path)
		}
	}
}

func allowedSkillspecImport(path string) bool {
	if path == "gopkg.in/yaml.v3" {
		return true
	}
	if strings.HasPrefix(path, "github.com/") {
		return false
	}
	switch path {
	case "os", "path", "path/filepath", "io/fs", "embed", "net", "net/http", "os/exec", "plugin":
		return false
	default:
		return true
	}
}

func TestREADME_IdentifiesPublishedSkillContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	if !strings.Contains(doc, "[`skillspec`](skillspec/)") {
		t.Fatal("README does not list the skillspec package")
	}
	idx := strings.Index(doc, "[`skillspec`](skillspec/)")
	if idx < 0 {
		return
	}
	row := doc[idx:]
	if i := strings.Index(row, "\n"); i >= 0 {
		row = row[:i]
	}
	for _, needle := range []string{"version", "capability_class"} {
		if !strings.Contains(row, needle) {
			t.Errorf("README skillspec row does not identify %s as part of the published contract", needle)
		}
	}
}
