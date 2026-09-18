package searchconform

import (
	"context"
	"errors"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/search"
)

func TestRun_GoodAdapterPasses(t *testing.T) {
	s := &goodSearch{}
	rep, err := Run(context.Background(), s, goodOpts())
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.Err(); err != nil {
		t.Fatalf("not conformant:\n%s", rep)
	}
	if len(rep.Results) != 12 {
		t.Fatalf("checks=%d, want 12\n%s", len(rep.Results), rep)
	}
}

func TestRun_SQLPassthroughFails(t *testing.T) {
	s := &goodSearch{passthroughSQL: true}
	rep, err := Run(context.Background(), s, goodOpts())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Err() == nil {
		t.Fatal("SQL passthrough must fail conformance")
	}
}

func TestRun_MissingPartitionAsEmptyFails(t *testing.T) {
	s := &goodSearch{missingIsEmpty: true}
	rep, err := Run(context.Background(), s, goodOpts())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Err() == nil {
		t.Fatal("missing partition as empty must fail")
	}
}

type goodSearch struct {
	passthroughSQL bool
	missingIsEmpty bool
}

func (g *goodSearch) Implements() connector.Class          { return connector.ClassSearch }
func (g *goodSearch) Capabilities() connector.Capabilities { return nil }
func (g *goodSearch) Health(context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}
func (g *goodSearch) Close() error { return nil }

func (g *goodSearch) Query(ctx context.Context, q search.Query) (search.Hits, error) {
	if err := ctx.Err(); err != nil {
		return search.Hits{}, cerr.New(cerr.KindTimeout, "Query", err)
	}
	if err := q.Validate(); err != nil {
		if g.passthroughSQL {
			return search.Hits{Envelope: search.Envelope{Coverage: search.CoverageComplete}}, nil
		}
		return search.Hits{}, err
	}
	return search.Hits{
		Items:    []search.Hit{{Path: "a.md", Revision: q.Scope.Sources[0].Revision, Fingerprint: "fp", Generation: q.Scope.Generation}},
		Envelope: search.Envelope{Coverage: search.CoverageComplete, Generation: q.Scope.Generation},
	}, nil
}

func (g *goodSearch) Get(ctx context.Context, req search.GetRequest) (search.Record, error) {
	if err := req.Validate(); err != nil {
		return search.Record{}, err
	}
	if req.Scope.Handle == "missing" {
		if g.missingIsEmpty {
			return search.Record{}, nil
		}
		return search.Record{}, cerr.New(cerr.KindUnavailable, "Get", errors.New("partition missing"))
	}
	if req.Scope.Generation == "stale" {
		return search.Record{}, cerr.New(cerr.KindInvalid, "Get", errors.New("stale generation"))
	}
	return search.Record{
		Path: req.Path, RecordID: req.RecordID, Revision: req.Scope.Sources[0].Revision,
		Fingerprint: "fp", Generation: req.Scope.Generation,
	}, nil
}

func (g *goodSearch) List(_ context.Context, req search.ListRequest) (search.Resolution[search.Record], error) {
	if err := req.Validate(); err != nil {
		return search.Resolution[search.Record]{}, err
	}
	if req.Scope.Handle == "empty" {
		return search.EmptyList[search.Record](), nil
	}
	return search.Resolved([]search.Record{{Path: "a.md", Revision: req.Scope.Sources[0].Revision}}, search.Complete)
}

func goodOpts() Options {
	src := search.SourceRev{TrustDomain: "td", SourceIdentity: "src", Revision: "rev"}
	scope := search.Scope{Handle: "scope-1", Sources: []search.SourceRev{src}, ProfileDigest: "digest", Generation: "gen-1"}
	return Options{
		Manifest:         manifest.Doc{Name: "placeholder-search", Implements: connector.ClassSearch, Kind: "native"},
		Scope:            scope,
		KnownRecord:      search.GetRequest{Scope: scope, Path: "a.md"},
		MissingPartition: search.Scope{Handle: "missing", Sources: []search.SourceRev{src}, ProfileDigest: "digest", Generation: "gen-1"},
		EmptyScope:       search.Scope{Handle: "empty", Sources: []search.SourceRev{src}, ProfileDigest: "digest", Generation: "gen-1"},
		StaleGeneration:  search.GetRequest{Scope: search.Scope{Handle: "scope-1", Sources: []search.SourceRev{src}, ProfileDigest: "digest", Generation: "stale"}, Path: "a.md"},
	}
}
