package search

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

func TestClass_IsThePublishedOne(t *testing.T) {
	if !slices.Contains(connector.Classes(), connector.ClassSearch) {
		t.Fatalf("ClassSearch not in Classes()=%v", connector.Classes())
	}
	if !connector.ClassSearch.Valid() {
		t.Fatal("ClassSearch.Valid is false")
	}
}

func TestSearch_IsExactlyQueryGetList(t *testing.T) {
	base := methodNames(reflect.TypeOf((*connector.Connector)(nil)).Elem())
	all := methodNames(reflect.TypeOf((*Search)(nil)).Elem())
	var own []string
	for _, m := range all {
		if !slices.Contains(base, m) {
			own = append(own, m)
		}
	}
	want := []string{"Get", "List", "Query"}
	if !slices.Equal(own, want) {
		t.Fatalf("Search operations=%v, want %v", own, want)
	}
}

func TestQuery_RefusesSQLSelectorAndScopeWiden(t *testing.T) {
	s := validScope()
	q := Query{Scope: s, Limit: 10, Selectors: []Selector{{Kind: SelectorText, Value: "SELECT * FROM record"}}}
	if cerr.KindOf(q.Validate()) != cerr.KindInvalid {
		t.Fatalf("SQL selector: %v", q.Validate())
	}
	q = Query{Scope: s, Limit: 10, Continuation: Continuation{Token: "n", ScopeStamp: "other"}}
	if cerr.KindOf(q.Validate()) != cerr.KindInvalid {
		t.Fatalf("widening continuation: %v", q.Validate())
	}
}

func TestGet_RefusesEmptyIdentity(t *testing.T) {
	err := GetRequest{Scope: validScope()}.Validate()
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("empty get: %v", err)
	}
}

func TestPublish_IsConditional(t *testing.T) {
	err := PublishRequest{Handle: GenerationHandle{ID: "g1"}}.Validate()
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("unconditional publish: %v", err)
	}
}

func TestKnownCapabilities_IncludeAbsentVectorAndRemote(t *testing.T) {
	caps := KnownCapabilities()
	for _, c := range []connector.Capability{CapVectors, CapRemoteServing, CapWrite, CapFacets, CapEdges} {
		if !caps.Has(c) {
			t.Errorf("missing %s", c)
		}
	}
}

func TestMappingExamples_ExposeBackendDifferencesWithoutSQLInContract(t *testing.T) {
	sqlite, err := os.ReadFile(filepath.Join("testdata", "sqlite-mapping.yml"))
	if err != nil {
		t.Fatal(err)
	}
	pg, err := os.ReadFile(filepath.Join("testdata", "postgres-mapping.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tokenizer", "rank", "transaction", "generation"} {
		if !strings.Contains(strings.ToLower(string(sqlite)), want) {
			t.Errorf("sqlite mapping missing %s", want)
		}
		if !strings.Contains(strings.ToLower(string(pg)), want) {
			t.Errorf("postgres mapping missing %s", want)
		}
	}
	if !strings.Contains(string(pg), "pgvector") || !strings.Contains(string(pg), "ANN") {
		t.Fatal("postgres mapping must name pgvector ANN lossiness")
	}
	if strings.Contains(string(sqlite), "vectors: declared") {
		t.Fatal("sqlite mapping must not declare vectors")
	}
}

func validScope() Scope {
	return Scope{
		Handle:        "scope-1",
		ProfileDigest: "digest-1",
		Generation:    "gen-1",
		Sources:       []SourceRev{{TrustDomain: "td", SourceIdentity: "src", Revision: "rev"}},
	}
}

func methodNames(typ reflect.Type) []string {
	out := make([]string, 0, typ.NumMethod())
	for i := range typ.NumMethod() {
		out = append(out, typ.Method(i).Name)
	}
	slices.Sort(out)
	return out
}
