// Package connector is the base contract every arqtos connector implements,
// independent of connector-class. Class-specific contracts (e.g.
// credential.CredentialLoader) embed connector.Connector.
package connector

import (
	"context"
	"slices"
)

type Class string

const (
	ClassCredentialLoader Class = "CredentialLoader"
	// ClassRoster is a read-only directory of principals, groups and the
	// memberships between them (an identity provider, a workspace directory,
	// a code host's teams, a flat file). See the roster package.
	ClassRoster Class = "Roster"
	// ClassCodeCI is pull-request, branch, diff and CI check/workflow-run
	// operations against a code host's PR/CI surface — create/list/merge a
	// pull or merge request, read its diff, list branches, read check status
	// and CI run state. It is distinct from a code-host-administration class
	// (repository creation, webhooks, runner tokens): same vendor, different
	// contract, so an org can pair either half with a different backend. See
	// the codeci package.
	ClassCodeCI Class = "CodeCI"
	// ClassRecordStore, ClassTracker, ClassCodeHost, ... land with their designs.
)

// classes is the single source of truth for the closed set of connector
// classes the SDK knows. [Classes] and [Class.Valid] both derive from it, and
// so does the manifest schema's implements enum — a class cannot be
// half-added (declarable in a manifest but unknown here, or known here and
// refused by the manifest).
var classes = []Class{
	ClassCredentialLoader,
	ClassRoster,
	ClassCodeCI,
}

// Classes returns the closed set of connector classes, sorted, as a copy. A
// caller cannot narrow or extend the contract by mutating it.
func Classes() []Class {
	out := slices.Clone(classes)
	slices.Sort(out)
	return out
}

// Valid reports whether c is a class the SDK knows. A Class that is not is a
// string someone converted — not a routing decision any host can act on.
func (c Class) Valid() bool { return slices.Contains(classes, c) }

type Capability string

type Capabilities []Capability

func (c Capabilities) Has(x Capability) bool {
	for _, v := range c {
		if v == x {
			return true
		}
	}
	return false
}

type HealthStatus int

const (
	Healthy HealthStatus = iota
	Degraded
	Unavailable
)

type Health struct {
	Status HealthStatus
	Detail string
}

// Connector is the base contract. Every connector, whatever its class or runtime
// shape (native in-proc / Track-B out-of-process), implements this.
type Connector interface {
	Implements() Class
	Capabilities() Capabilities
	Health(ctx context.Context) (Health, error)
	Close() error
}
