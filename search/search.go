// Package search is the Search connector-class contract: revision-scoped
// lexical retrieval over one index partition.
//
// Core constructs scope, fuses evidence and owns principal authority. The
// adapter never receives vendor SQL from the caller, never decides who may
// read, and never silently substitutes latest for a pinned revision.
//
// The class is [github.com/arqtiqa/arqtos-sdk-go/connector.ClassSearch]. This
// package publishes no second constant for it.
//
// Mandatory operations are [Search.Query], [Search.Get] and [Search.List].
// Optional operations sit behind capabilities: [Faceter] / CapFacets,
// [Edger] / CapEdges, [Writer] / CapWrite. CapVectors and CapRemoteServing
// may be undeclared; MVP lexical adapters leave them off.
package search

import (
	"context"
	"errors"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

const (
	CapFacets        connector.Capability = "facets"
	CapEdges         connector.Capability = "edges"
	CapWrite         connector.Capability = "write"
	CapVectors       connector.Capability = "vectors"
	CapRemoteServing connector.Capability = "remote_serving"
)

var knownCapabilities = connector.Capabilities{
	CapFacets, CapEdges, CapWrite, CapVectors, CapRemoteServing,
}

func KnownCapabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), knownCapabilities...)
}

type Search interface {
	connector.Connector
	Query(context.Context, Query) (Hits, error)
	Get(context.Context, GetRequest) (Record, error)
	List(context.Context, ListRequest) (Resolution[Record], error)
}

type Faceter interface {
	Facets(context.Context, FacetRequest) (FacetReport, error)
}

type Edger interface {
	Edges(context.Context, EdgeRequest) (EdgeReport, error)
}

type Writer interface {
	Stage(context.Context, StageRequest) (GenerationHandle, error)
	Publish(context.Context, PublishRequest) error
}

type Resolution[T any] = roster.Resolution[T]
type Completeness = roster.Completeness

const (
	Complete = roster.Complete
	Partial  = roster.Partial
)

func Resolved[T any](items []T, c Completeness) (Resolution[T], error) {
	return roster.Resolved(items, c)
}

func EmptyList[T any]() Resolution[T] { return roster.EmptyRoster[T]() }

type SourceRev struct {
	TrustDomain    string
	SourceIdentity string
	Revision       string
}

type Scope struct {
	Handle        string
	Sources       []SourceRev
	ProfileDigest string
	Generation    string
}

func (s Scope) Validate(op string) error {
	if strings.TrimSpace(s.Handle) == "" || strings.TrimSpace(s.ProfileDigest) == "" || strings.TrimSpace(s.Generation) == "" {
		return cerr.New(cerr.KindInvalid, op, errors.New("scope handle, profile digest and generation are required"))
	}
	if len(s.Sources) == 0 {
		return cerr.New(cerr.KindInvalid, op, errors.New("scope requires at least one source revision"))
	}
	for _, src := range s.Sources {
		if src.TrustDomain == "" || src.SourceIdentity == "" || src.Revision == "" {
			return cerr.New(cerr.KindInvalid, op, errors.New("source trust domain, identity and revision are required"))
		}
	}
	return nil
}

type SelectorKind string

const (
	SelectorUnspecified SelectorKind = ""
	SelectorText        SelectorKind = "text"
	SelectorUID         SelectorKind = "uid"
	SelectorPath        SelectorKind = "path"
	SelectorFacet       SelectorKind = "facet"
)

type Selector struct {
	Kind  SelectorKind
	Value string
	Name  string
}

func (s Selector) Validate(op string) error {
	if s.Kind == SelectorUnspecified {
		return cerr.New(cerr.KindInvalid, op, errors.New("selector kind is required"))
	}
	if s.Kind == SelectorText && looksLikeSQL(s.Value) {
		return cerr.New(cerr.KindInvalid, op, errors.New("vendor SQL is not a selector"))
	}
	if s.Value == "" && s.Kind != SelectorFacet {
		return cerr.New(cerr.KindInvalid, op, errors.New("selector value is required"))
	}
	return nil
}

func looksLikeSQL(v string) bool {
	u := strings.ToUpper(strings.TrimSpace(v))
	return strings.Contains(u, "SELECT ") || strings.Contains(u, " INSERT ") || strings.HasPrefix(u, "SELECT")
}

type Continuation struct {
	Token      string
	ScopeStamp string
}

func (c Continuation) Widens(s Scope) bool {
	if c.Token == "" {
		return false
	}
	return c.ScopeStamp != s.Handle+"|"+s.Generation+"|"+s.ProfileDigest
}

type Query struct {
	Scope        Scope
	Selectors    []Selector
	Limit        int
	Continuation Continuation
}

func (q Query) Validate() error {
	if err := q.Scope.Validate("Query"); err != nil {
		return err
	}
	if q.Limit <= 0 {
		return cerr.New(cerr.KindInvalid, "Query", errors.New("limit must be positive"))
	}
	if q.Continuation.Widens(q.Scope) {
		return cerr.New(cerr.KindInvalid, "Query", errors.New("continuation cannot change scope, generation or profile"))
	}
	for _, s := range q.Selectors {
		if err := s.Validate("Query"); err != nil {
			return err
		}
	}
	return nil
}

type Coverage string

const (
	CoverageUnspecified Coverage = ""
	CoverageComplete    Coverage = "complete"
	CoveragePartial     Coverage = "partial"
	CoverageUnavailable Coverage = "unavailable"
	CoverageDenied      Coverage = "denied"
)

type Envelope struct {
	Coverage   Coverage
	Truncated  bool
	Generation string
}

type RankEvidence struct {
	Backend string
	Score   float64
}

type Hit struct {
	SourceIdentity string
	ResourceID     string
	Revision       string
	RecordID       string
	Path           string
	Section        string
	Fingerprint    string
	Generation     string
	Kind           string
	Rank           RankEvidence
}

type Hits struct {
	Items    []Hit
	Envelope Envelope
}

type Record struct {
	SourceIdentity string
	ResourceID     string
	Revision       string
	RecordID       string
	Path           string
	Title          string
	Body           string
	Fingerprint    string
	Generation     string
	Kind           string
}

type GetRequest struct {
	Scope    Scope
	RecordID string
	Path     string
}

func (r GetRequest) Validate() error {
	if err := r.Scope.Validate("Get"); err != nil {
		return err
	}
	if r.RecordID == "" && r.Path == "" {
		return cerr.New(cerr.KindInvalid, "Get", errors.New("record id or path is required"))
	}
	return nil
}

type ListRequest struct {
	Scope Scope
	Limit int
}

func (r ListRequest) Validate() error {
	if err := r.Scope.Validate("List"); err != nil {
		return err
	}
	if r.Limit <= 0 {
		return cerr.New(cerr.KindInvalid, "List", errors.New("limit must be positive"))
	}
	return nil
}

type FacetRequest struct {
	Scope Scope
	Names []string
}

type FacetReport struct {
	Counts   map[string]int
	Envelope Envelope
}

type EdgeRequest struct {
	Scope    Scope
	RecordID string
}

type EdgeReport struct {
	Targets  []string
	Envelope Envelope
}

type StageRequest struct {
	Scope Scope
	Key   PartitionKey
}

type PartitionKey struct {
	TrustDomain    string
	SourceIdentity string
	Revision       string
	ProfileDigest  string
}

func (k PartitionKey) Validate(op string) error {
	if k.TrustDomain == "" || k.SourceIdentity == "" || k.Revision == "" || k.ProfileDigest == "" {
		return cerr.New(cerr.KindInvalid, op, errors.New("partition key omits trust, source, revision or profile"))
	}
	return nil
}

type GenerationHandle struct {
	ID            string
	ProfileDigest string
	Expected      string
}

type PublishRequest struct {
	Handle   GenerationHandle
	Expected string
}

func (r PublishRequest) Validate() error {
	if r.Handle.ID == "" {
		return cerr.New(cerr.KindInvalid, "Publish", errors.New("generation handle is required"))
	}
	if r.Expected == "" {
		return cerr.New(cerr.KindInvalid, "Publish", errors.New("publish is conditional on an expected generation"))
	}
	return nil
}
