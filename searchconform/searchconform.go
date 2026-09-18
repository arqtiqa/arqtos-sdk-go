// Package searchconform is the Search class conformance harness.
package searchconform

import (
	"context"
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/search"
)

const (
	CheckManifest          = "manifest"
	CheckClass             = "class"
	CheckCapabilityHonesty = "capability/honesty"
	CheckQuery             = "query/scoped"
	CheckSQLRefusal        = "query/sql-refused"
	CheckContinuation      = "query/continuation-bound"
	CheckGet               = "get/exact-revision"
	CheckMissingPartition  = "get/missing-unavailable"
	CheckListEmpty         = "list/complete-empty"
	CheckCancel            = "query/cancel"
	CheckStaleGeneration   = "get/stale-generation"
	CheckOptionalDeclared  = "optional/declared-is-implemented"
)

type Options struct {
	Manifest         manifest.Doc
	Scope            search.Scope
	KnownRecord      search.GetRequest
	MissingPartition search.Scope
	EmptyScope       search.Scope
	StaleGeneration  search.GetRequest
}

type Result struct {
	Name   string
	Pass   bool
	Detail string
}

type Report struct {
	Results []Result
}

func (r Report) Err() error {
	var failed []string
	for _, x := range r.Results {
		if !x.Pass {
			failed = append(failed, x.Name+": "+x.Detail)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("searchconform: %s", strings.Join(failed, "; "))
}

func (r Report) String() string {
	var b strings.Builder
	for _, x := range r.Results {
		mark := "PASS"
		if !x.Pass {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "%s %s %s\n", mark, x.Name, x.Detail)
	}
	return b.String()
}

func Run(ctx context.Context, s search.Search, opts Options) (Report, error) {
	var rep Report
	add := func(name string, pass bool, detail string) {
		rep.Results = append(rep.Results, Result{Name: name, Pass: pass, Detail: detail})
	}

	if opts.Manifest.Implements != connector.ClassSearch {
		add(CheckManifest, false, "implements is not Search")
		return rep, nil
	}
	add(CheckManifest, true, "implements Search")
	if s.Implements() != connector.ClassSearch {
		add(CheckClass, false, "Implements() is not Search")
		return rep, nil
	}
	add(CheckClass, true, "class Search")

	caps := s.Capabilities()
	honest := true
	if caps.Has(search.CapFacets) {
		if _, ok := s.(search.Faceter); !ok {
			honest = false
		}
	}
	if caps.Has(search.CapEdges) {
		if _, ok := s.(search.Edger); !ok {
			honest = false
		}
	}
	if caps.Has(search.CapWrite) {
		if _, ok := s.(search.Writer); !ok {
			honest = false
		}
	}
	add(CheckCapabilityHonesty, honest, "optional interfaces match capabilities")

	_, faceter := s.(search.Faceter)
	_, edger := s.(search.Edger)
	_, writer := s.(search.Writer)
	optOK := (!faceter || caps.Has(search.CapFacets)) && (!edger || caps.Has(search.CapEdges)) && (!writer || caps.Has(search.CapWrite))
	add(CheckOptionalDeclared, optOK, "implemented optionals are declared")

	q := search.Query{Scope: opts.Scope, Limit: 8, Selectors: []search.Selector{{Kind: search.SelectorText, Value: "placeholder"}}}
	hits, err := s.Query(ctx, q)
	add(CheckQuery, err == nil && hits.Envelope.Coverage != search.CoverageUnspecified, fmt.Sprintf("Query err=%v coverage=%s", err, hits.Envelope.Coverage))

	sqlQ := q
	sqlQ.Selectors = []search.Selector{{Kind: search.SelectorText, Value: "SELECT * FROM record"}}
	_, err = s.Query(ctx, sqlQ)
	add(CheckSQLRefusal, cerr.KindOf(err) == cerr.KindInvalid, fmt.Sprintf("SQL selector: %v", err))

	wide := q
	wide.Continuation = search.Continuation{Token: "n", ScopeStamp: "other"}
	_, err = s.Query(ctx, wide)
	add(CheckContinuation, cerr.KindOf(err) == cerr.KindInvalid, fmt.Sprintf("widening continuation: %v", err))

	rec, err := s.Get(ctx, opts.KnownRecord)
	add(CheckGet, err == nil && rec.Revision == opts.KnownRecord.Scope.Sources[0].Revision && rec.Fingerprint != "", fmt.Sprintf("Get err=%v rev=%s", err, rec.Revision))

	_, err = s.Get(ctx, search.GetRequest{Scope: opts.MissingPartition, Path: "missing.md"})
	add(CheckMissingPartition, cerr.KindOf(err) == cerr.KindUnavailable, fmt.Sprintf("missing partition: %v", err))

	lst, err := s.List(ctx, search.ListRequest{Scope: opts.EmptyScope, Limit: 8})
	emptyOK := false
	if err == nil {
		items, ierr := lst.Items()
		emptyOK = ierr == nil && len(items) == 0
	}
	add(CheckListEmpty, emptyOK, fmt.Sprintf("complete-empty List err=%v", err))

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.Query(cctx, q)
	add(CheckCancel, cerr.KindOf(err) == cerr.KindTimeout || cerr.KindOf(err) == cerr.KindUnavailable, fmt.Sprintf("cancel: %v", err))

	_, err = s.Get(ctx, opts.StaleGeneration)
	add(CheckStaleGeneration, cerr.KindOf(err) == cerr.KindInvalid || cerr.KindOf(err) == cerr.KindUnavailable, fmt.Sprintf("stale generation: %v", err))

	return rep, nil
}
