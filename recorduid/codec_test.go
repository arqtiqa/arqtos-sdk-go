package recorduid_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/recorduid"
)

func simple() recorduid.Declaration {
	return recorduid.Declaration{
		Schema: recorduid.Schema, OrgID: "org-example", SequenceID: "seq-docs", Revision: "revision-one",
		Kind: "docs", Mode: recorduid.Issuing,
		Namespace: recorduid.Namespace{Profile: recorduid.Simple, KindPrefix: "doc", ProjectRef: "project-example", ProjectSlug: "example"},
	}
}

func full() recorduid.Declaration {
	d := simple()
	d.Namespace = recorduid.Namespace{Profile: recorduid.Full, KindPrefix: "doc", OrgSlug: "ex", HostKey: "host", RepositoryID: "0042"}
	return d
}

func TestDeclarationRender_WhenProfilesAreDeclared_UsesGoldenLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    recorduid.Declaration
		n    uint64
		want string
	}{
		{"simple", simple(), 1, "doc-example-00001"},
		{"full", full(), 1, "doc-ex-host-0042-00001"},
		{"five_digits", full(), 99999, "doc-ex-host-0042-99999"},
		{"six_digits", full(), 100000, "doc-ex-host-0042-100000"},
		{"largest", simple(), math.MaxUint64, "doc-example-18446744073709551615"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.d.Render(tc.n)
			if err != nil || got != tc.want {
				t.Fatalf("Render(%d) = %q, %v; want %q", tc.n, got, err, tc.want)
			}
			n, err := tc.d.Parse(got)
			if err != nil || n != tc.n {
				t.Fatalf("Parse(%q) = %d, %v; want %d", got, n, err, tc.n)
			}
		})
	}
}

func TestDeclarationRender_WhenKindIsCustom_UsesDeclaredPrefix(t *testing.T) {
	for _, pair := range [][2]string{{"docs", "doc"}, {"dcns", "dcn"}, {"clms", "clm"}, {"rdrs", "rdr"}, {"field-notes", "field-note"}} {
		for _, d := range []recorduid.Declaration{simple(), full()} {
			d.Kind, d.Namespace.KindPrefix = pair[0], pair[1]
			label, err := d.Render(1)
			want := strings.ReplaceAll(pair[1], "-", "%2d") + "-"
			if err != nil || !strings.HasPrefix(label, want) {
				t.Errorf("%s/%s: Render = %q, %v; want prefix %q", pair[0], d.Namespace.Profile, label, err, want)
			}
		}
	}
}

func TestDeclarationRender_WhenNativeIDIsOpaque_PreservesExactBytes(t *testing.T) {
	for _, tc := range []struct{ id, segment string }{
		{"00042", "00042"}, {"9007199254740993", "9007199254740993"},
		{"184467440737095516160000", "184467440737095516160000"},
		{"repo-A/%2d:é", "repo%2d%41%2f%252d%3a%c3%a9"}, {"a_b.~", "a_b.~"},
	} {
		d := full()
		d.Namespace.RepositoryID = tc.id
		before := d
		label, err := d.Render(7)
		if err != nil || label != "doc-ex-host-"+tc.segment+"-00007" {
			t.Fatalf("Render repository %q = %q, %v", tc.id, label, err)
		}
		if label != strings.ToLower(label) {
			t.Fatalf("new UID is not lowercase: %q", label)
		}
		if n, err := d.Parse(label); err != nil || n != 7 {
			t.Fatalf("Parse(%q) = %d, %v", label, n, err)
		}
		data, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		var decoded recorduid.Declaration
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if d != before || decoded != before {
			t.Fatal("codec or JSON round trip changed namespace metadata")
		}
	}
}

func TestDeclarationParse_WhenLabelIsNoncanonical_Refuses(t *testing.T) {
	for _, tc := range []struct{ suffix, reason string }{
		{"00000", "ordinal"}, {"1", "canonical"}, {"000001", "canonical"},
		{"+0001", "canonical"}, {"-0001", "ordinal"}, {"1e005", "ordinal"},
		{"1.000", "ordinal"}, {"00001 ", "ordinal"}, {"18446744073709551616", "overflow"},
	} {
		_, err := simple().Parse("doc-example-" + tc.suffix)
		requireReason(t, err, tc.reason)
	}
	for _, label := range []string{"doc-other-00001", "doc%2Dexample-00001", "doc-%65xample-00001", "doc-example-00001-more", "DOC-example-00001"} {
		if _, err := simple().Parse(label); err == nil {
			t.Errorf("Parse(%q) accepted wrong/noncanonical namespace", label)
		}
	}
	d := full()
	d.Namespace.RepositoryID = "repo-id"
	for _, label := range []string{"doc-ex-host-repo-id-00001", "doc-ex-host-repo%2Did-00001", "doc-ex-host-repo%252did-00001"} {
		_, err := d.Parse(label)
		requireReason(t, err, "namespace")
	}
}

func TestDeclaration_WhenMetadataIsMalformed_RefusesAtEveryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(*recorduid.Declaration)
	}{
		{"absent_schema", "schema", func(d *recorduid.Declaration) { d.Schema = "" }},
		{"old_schema", "schema", func(d *recorduid.Declaration) { d.Schema = "arqtos-identity-reservations/v1" }},
		{"future_schema", "schema", func(d *recorduid.Declaration) { d.Schema = "arqtos-uid-declaration/v2" }},
		{"absent_org", "org_id", func(d *recorduid.Declaration) { d.OrgID = "" }},
		{"absent_sequence", "sequence_id", func(d *recorduid.Declaration) { d.SequenceID = "" }},
		{"absent_revision", "revision", func(d *recorduid.Declaration) { d.Revision = "" }},
		{"absent_kind", "kind", func(d *recorduid.Declaration) { d.Kind = "" }},
		{"absent_mode", "mode", func(d *recorduid.Declaration) { d.Mode = "" }},
		{"unknown_mode", "mode", func(d *recorduid.Declaration) { d.Mode = "new" }},
		{"unknown_profile", "profile", func(d *recorduid.Declaration) { d.Namespace.Profile = "default" }},
		{"absent_prefix", "kind_prefix", func(d *recorduid.Declaration) { d.Namespace.KindPrefix = "" }},
		{"uppercase_prefix", "kind_prefix", func(d *recorduid.Declaration) { d.Namespace.KindPrefix = "DOC" }},
		{"empty_segment", "kind_prefix", func(d *recorduid.Declaration) { d.Namespace.KindPrefix = "doc--note" }},
		{"escape_in_slug", "kind_prefix", func(d *recorduid.Declaration) { d.Namespace.KindPrefix = "doc%2Dnote" }},
		{"absent_project", "project_ref", func(d *recorduid.Declaration) { d.Namespace.ProjectRef = "" }},
		{"absent_slug", "project_slug", func(d *recorduid.Declaration) { d.Namespace.ProjectSlug = "" }},
		{"display_label", "project_slug", func(d *recorduid.Declaration) { d.Namespace.ProjectSlug = "My Project" }},
		{"wrong_profile_coordinate", "full coordinates", func(d *recorduid.Declaration) { d.Namespace.RepositoryID = "42" }},
		{"historical_prefix_on_issuer", "historical_prefix", func(d *recorduid.Declaration) { d.HistoricalPrefix = "doc-legacy" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := simple()
			tc.change(&d)
			requireReason(t, d.Validate(), tc.reason)
			_, err := d.Render(1)
			requireReason(t, err, tc.reason)
			_, err = d.Parse("doc-example-00001")
			requireReason(t, err, tc.reason)
		})
	}
	for _, tc := range []struct {
		field  string
		change func(*recorduid.Namespace)
	}{
		{"org_slug", func(n *recorduid.Namespace) { n.OrgSlug = "" }},
		{"host_key", func(n *recorduid.Namespace) { n.HostKey = "" }},
		{"repository_id", func(n *recorduid.Namespace) { n.RepositoryID = "" }},
		{"repository_id", func(n *recorduid.Namespace) { n.RepositoryID = "id\n" }},
		{"repository_id", func(n *recorduid.Namespace) { n.RepositoryID = "id\x00" }},
		{"repository_id", func(n *recorduid.Namespace) { n.RepositoryID = "a b" }},
		{"repository_id", func(n *recorduid.Namespace) { n.RepositoryID = string([]byte{0xff}) }},
		{"simple coordinates", func(n *recorduid.Namespace) { n.ProjectSlug = "example" }},
	} {
		d := full()
		tc.change(&d.Namespace)
		requireReason(t, d.Validate(), tc.field)
	}
	_, err := simple().Render(0)
	requireReason(t, err, "ordinal")
}

func TestDeclaration_WhenHistorical_ReadsWithoutIssuing(t *testing.T) {
	d := simple()
	d.Mode, d.Kind = recorduid.Historical, "rdrs"
	d.Namespace, d.HistoricalPrefix = recorduid.Namespace{}, "rdr-legacy"
	if n, err := d.Parse("rdr-legacy-00042"); err != nil || n != 42 {
		t.Fatalf("historical Parse = %d, %v", n, err)
	}
	_, err := d.Render(43)
	requireReason(t, err, "historical")
	d.Mode = recorduid.Issuing
	requireReason(t, d.Validate(), "historical_prefix")
	for _, original := range []recorduid.Declaration{simple(), full()} {
		label, err := original.Render(42)
		if err != nil {
			t.Fatal(err)
		}
		original.Mode = recorduid.Historical
		original.Namespace, original.HistoricalPrefix = recorduid.Namespace{}, strings.TrimSuffix(label, "-00042")
		if n, err := original.Parse(label); err != nil || n != 42 {
			t.Fatalf("cutover history changed %q: %d, %v", label, n, err)
		}
	}
	for _, prefix := range []string{"doc-some-project", "DOC-Legacy", "rdr-legacy%2Dproject", "doc-équipe"} {
		d.Mode, d.HistoricalPrefix = recorduid.Historical, prefix
		for _, ordinal := range []string{"00042", "000042"} {
			label := prefix + "-" + ordinal
			if n, err := d.Parse(label); err != nil || n != 42 {
				t.Fatalf("admitted %q changed/refused: %d, %v", label, n, err)
			}
		}
	}
	d.HistoricalPrefix = ""
	requireReason(t, d.Validate(), "historical_prefix")
	d.HistoricalPrefix, d.Namespace = "doc-legacy", simple().Namespace
	requireReason(t, d.Validate(), "namespace")
}

func requireReason(t *testing.T, err error, reason string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("error = %v; want reason %q", err, reason)
	}
}

func TestValidateSet_WhenNamespacesOverlap_RefusesIndependentIssuers(t *testing.T) {
	for _, d := range []recorduid.Declaration{simple(), full()} {
		other := d
		other.SequenceID = "seq-other"
		requireReason(t, recorduid.ValidateSet([]recorduid.Declaration{d, other}), "collision")
		other.Kind = "custom"
		requireReason(t, recorduid.ValidateSet([]recorduid.Declaration{d, other}), "collision")
		other.OrgID = "org-other"
		if err := recorduid.ValidateSet([]recorduid.Declaration{d, other}); err != nil {
			t.Fatal(err)
		}
		if err := recorduid.ValidateSet([]recorduid.Declaration{d, d}); err != nil {
			t.Fatalf("shared sequence: %v", err)
		}
	}
	d := simple()
	other := d
	other.Revision, other.Namespace.ProjectSlug = "revision-two", "other"
	requireReason(t, recorduid.ValidateSet([]recorduid.Declaration{d, other}), "issuing revision")
	d.Mode, d.HistoricalPrefix, d.Namespace = recorduid.Historical, "doc-example", recorduid.Namespace{}
	if err := recorduid.ValidateSet([]recorduid.Declaration{d, other}); err != nil {
		t.Fatalf("explicit cutover: %v", err)
	}
	other = d
	other.SequenceID = "seq-other"
	if err := recorduid.ValidateSet([]recorduid.Declaration{d, other}); err != nil {
		t.Fatalf("explicit ambiguous history: %v", err)
	}
	other.Mode = recorduid.Issuing
	other.Namespace, other.HistoricalPrefix = simple().Namespace, ""
	requireReason(t, recorduid.ValidateSet([]recorduid.Declaration{d, other}), "collision")
}

func TestValidateSet_WhenRawTuplesCollide_EncodingSeparatesThem(t *testing.T) {
	a, b := simple(), simple()
	a.Kind, a.Namespace.KindPrefix, a.Namespace.ProjectSlug = "custom", "doc-note", "project"
	b.Namespace.ProjectSlug, b.SequenceID = "note-project", "seq-other"
	for _, pair := range [][2]recorduid.Declaration{{a, b}, fullDelimiterPair()} {
		aLabel, aErr := pair[0].Render(1)
		bLabel, bErr := pair[1].Render(1)
		if aErr != nil || bErr != nil || aLabel == bLabel {
			t.Fatalf("distinct tuples collide: %q/%q (%v/%v)", aLabel, bLabel, aErr, bErr)
		}
		if err := recorduid.ValidateSet(pair[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := pair[0].Parse(bLabel); err == nil {
			t.Fatal("namespace mismatch accepted")
		}
	}
}

func fullDelimiterPair() [2]recorduid.Declaration {
	a, b := full(), full()
	a.Namespace.HostKey, a.Namespace.RepositoryID = "host-a", "b"
	b.Namespace.HostKey, b.Namespace.RepositoryID, b.SequenceID = "host", "a-b", "seq-other"
	return [2]recorduid.Declaration{a, b}
}

func TestDeclarationParse_WhenMetadataDiffers_DoesNotInferAuthority(t *testing.T) {
	a, b := simple(), simple()
	b.OrgID, b.SequenceID, b.Namespace.ProjectRef = "org-other", "seq-other", "project-other"
	label, err := a.Render(42)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := b.Parse(label); err != nil || n != 42 {
		t.Fatalf("same text in another org = %d, %v", n, err)
	}
	if reflect.DeepEqual(a, b) {
		t.Fatal("fixture must have distinct org/sequence authority metadata")
	}
}
