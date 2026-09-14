package credential

import (
	"context"
	"errors"
	"fmt"
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

// A Bundle is one logical full-grant acquisition. Ready reports asserted
// completeness with at least one entry; [CheckBundle] applies the enrolled
// inventory. One acquisition may contain several backend requests.
type Bundle struct {
	meta  BundleMeta
	cov   Coverage
	comp  Completeness
	items []BundleEntry
}

func (c Coverage) Valid() bool {
	switch c {
	case CoverageDeclared, CoverageVerified:
		return true
	default:
		return false
	}
}

func (i Inventory) Validate() error {
	if !i.Coverage.Valid() {
		return fmt.Errorf("%w: coverage %q", ErrIncompleteInventory, i.Coverage)
	}
	if i.Authority == "" || len(i.Keys) == 0 || len(i.Containers) == 0 {
		return fmt.Errorf("%w: authority, containers or keys missing", ErrIncompleteInventory)
	}
	seen := map[string]struct{}{}
	allowed := map[string]struct{}{}
	for _, c := range i.Containers {
		allowed[c] = struct{}{}
	}
	for _, k := range i.Keys {
		s := k.String()
		if _, ok := seen[s]; ok {
			return fmt.Errorf("%w: %s", ErrDuplicateIdentity, s)
		}
		seen[s] = struct{}{}
		if len(allowed) > 0 {
			if _, ok := allowed[k.Vault]; !ok {
				return fmt.Errorf("%w: %s", ErrScopeOverflow, s)
			}
		}
	}
	return nil
}

func BundleValue(id ref.Ref, res Resolution) BundleEntry {
	return BundleEntry{id: id, res: res}
}

func BundleStoredEmpty(id ref.Ref) BundleEntry {
	return BundleEntry{id: id, res: ResolvedEmpty()}
}

func (e BundleEntry) Identity() ref.Ref { return e.id }

func (e BundleEntry) Resolution() Resolution { return e.res }

// RestoreBundle reconstructs a bundle from the wire. Completeness unspecified
// is not ready — CheckBundle refuses it.
func RestoreBundle(meta BundleMeta, cov Coverage, comp Completeness, entries []BundleEntry) Bundle {
	out := make([]BundleEntry, len(entries))
	copy(out, entries)
	return Bundle{meta: meta, cov: cov, comp: comp, items: out}
}

func CompleteBundle(inv Inventory, entries []BundleEntry, meta BundleMeta) (Bundle, error) {
	if err := inv.Validate(); err != nil {
		return Bundle{}, err
	}
	seen := map[string]struct{}{}
	allowed := map[string]struct{}{}
	for _, c := range inv.Containers {
		allowed[c] = struct{}{}
	}
	for _, e := range entries {
		s := e.id.String()
		if _, ok := seen[s]; ok {
			return Bundle{}, fmt.Errorf("%w: %s", ErrDuplicateIdentity, s)
		}
		seen[s] = struct{}{}
		if len(allowed) > 0 {
			if _, ok := allowed[e.id.Vault]; !ok {
				return Bundle{}, fmt.Errorf("%w: %s", ErrScopeOverflow, s)
			}
		}
		if !e.res.present() {
			return Bundle{}, fmt.Errorf("%w: %s", ErrIncompleteInventory, s)
		}
	}
	for _, k := range inv.Keys {
		if _, ok := seen[k.String()]; !ok {
			return Bundle{}, fmt.Errorf("%w: %s", ErrIncompleteInventory, k)
		}
	}
	out := make([]BundleEntry, len(entries))
	copy(out, entries)
	return Bundle{meta: meta, cov: inv.Coverage, comp: CompletenessComplete, items: out}, nil
}

func (b Bundle) Ready() bool {
	return b.comp == CompletenessComplete && len(b.items) > 0
}

func (b Bundle) Coverage() Coverage { return b.cov }

func (b Bundle) Completeness() Completeness { return b.comp }

func (b Bundle) Meta() BundleMeta { return b.meta }

func (b Bundle) Entries() []BundleEntry {
	out := make([]BundleEntry, len(b.items))
	copy(out, b.items)
	return out
}

func (b Bundle) Lookup(id ref.Ref) (Resolution, bool) {
	for _, e := range b.items {
		if e.id == id {
			return e.res, true
		}
	}
	return Resolution{}, false
}

func (b Bundle) Zero() {
	for _, e := range b.items {
		if m, err := e.res.Value(); err == nil && m != nil {
			m.Zero()
		}
	}
}

func (b Bundle) String() string   { return "[REDACTED bundle]" }
func (b Bundle) GoString() string { return b.String() }

func CheckBundle(connectorName string, inv Inventory, b Bundle, err error) (Bundle, error) {
	if err != nil {
		return Bundle{}, err
	}
	if b.Completeness() != CompletenessComplete {
		return Bundle{}, &FaultError{
			Connector: connectorName,
			Op:        "AcquireBundle",
			Fault:     FaultBundleIncomplete,
			Detail:    "the connector reported a grant bundle that is not complete; a partial page or unspecified completeness is not ready",
		}
	}
	rebuilt, berr := CompleteBundle(inv, b.Entries(), b.Meta())
	if berr != nil {
		return Bundle{}, &FaultError{
			Connector: connectorName,
			Op:        "AcquireBundle",
			Fault:     FaultBundleIncomplete,
			Detail:    berr.Error(),
		}
	}
	return rebuilt, nil
}

// BundleAcquirer acquires a finite enrolled grant in one logical operation.
// Optional, behind [CapGrantBundle]. It is not [BatchResolver]: one acquisition
// may contain several backend requests.
type BundleAcquirer interface {
	AcquireBundle(ctx context.Context, inv Inventory) (Bundle, error)
}
