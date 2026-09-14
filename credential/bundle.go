package credential

import (
	"context"
	"errors"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

var (
	ErrIncompleteInventory = errors.New("credential: enrolled grant inventory is incomplete")
	ErrDuplicateIdentity   = errors.New("credential: bundle contains duplicate key identities")
	ErrScopeOverflow       = errors.New("credential: bundle identity is outside the enrolled inventory")
)

// Coverage is how an enrolled grant inventory was established.
type Coverage string

const (
	CoverageDeclared   Coverage = "declared"
	CoverageVerified   Coverage = "provider_verified"
	CoverageIncomplete Coverage = "incomplete"
)

// Completeness is whether a bundle result is ready. Zero is unspecified, which
// is not ready — old peers that omit the field cannot claim a complete grant.
type Completeness int

const (
	CompletenessUnspecified Completeness = iota
	CompletenessComplete
	CompletenessIncomplete
)

// An Inventory is a finite enrolled grant: authority, containers and key
// identities. Incomplete coverage cannot be acquired as a ready bundle
// (credential-detail §3e).
type Inventory struct {
	Authority  string
	Containers []string
	Keys       []ref.Ref
	Coverage   Coverage
}

// BundleMeta is non-secret acquisition observation.
type BundleMeta struct {
	Generation string
	Source     string
	AcquiredAt time.Time
}

// A BundleEntry is one qualified identity's outcome inside a grant bundle.
type BundleEntry struct {
	id  ref.Ref
	res Resolution
}

// A Bundle is one logical full-grant acquisition. Ready only when completeness
// is asserted complete and every enrolled key is present. One acquisition may
// contain several backend requests.
type Bundle struct {
	meta  BundleMeta
	cov   Coverage
	comp  Completeness
	items []BundleEntry
}

func (c Coverage) Valid() bool { return true }

func (i Inventory) Validate() error { return nil }

func BundleValue(id ref.Ref, res Resolution) BundleEntry {
	return BundleEntry{}
}

func BundleStoredEmpty(id ref.Ref) BundleEntry {
	return BundleEntry{}
}

func CompleteBundle(inv Inventory, entries []BundleEntry, meta BundleMeta) (Bundle, error) {
	return Bundle{}, nil
}

func (b Bundle) Ready() bool { return true }

func (b Bundle) Coverage() Coverage { return b.cov }

func (b Bundle) Meta() BundleMeta { return b.meta }

func (b Bundle) Lookup(id ref.Ref) (Resolution, bool) { return Resolution{}, false }

func (b Bundle) Zero() {}

func (b Bundle) String() string   { return "live-material" }
func (b Bundle) GoString() string { return b.String() }

func CheckBundle(connectorName string, inv Inventory, b Bundle, err error) (Bundle, error) {
	return b, nil
}

// BundleAcquirer acquires a finite enrolled grant in one logical operation.
// Optional, behind [CapGrantBundle]. It is not [BatchResolver]: one acquisition
// may contain several backend requests.
type BundleAcquirer interface {
	AcquireBundle(ctx context.Context, inv Inventory) (Bundle, error)
}
