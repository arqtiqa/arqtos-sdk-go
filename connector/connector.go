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
	// ClassTracker is one work tracker — one board on one instance of one
	// provider — and its items, their fields, their hierarchy and their
	// lifecycle. It is batch-first and addresses everything by NAME, because
	// no backend identity may cross the boundary. See the tracker package.
	ClassTracker Class = "Tracker"
	// ClassAuthenticator establishes WHO IS DRIVING THIS SESSION, interactively
	// and verifiably, against an identity provider. It is deliberately distinct
	// from ClassRoster: a Roster reads a directory with a service credential and
	// answers "who exists"; this class asks "who is here right now, and did they
	// prove it". They differ in credential, in lifetime and in failure mode — a
	// failed directory read is a stale roster, whereas a failed authentication
	// is a session that must not start. See the authenticator package.
	ClassAuthenticator Class = "Authenticator"
	// ClassCodeHost is a code host's REPOSITORY and GIT surface — repository
	// existence, lookup, listing and creation, topics, clone, push and branch
	// listing. It is deliberately distinct from [ClassCodeCI], which owns the
	// change-proposal surface: both classes once carried open/list/comment on
	// change requests under different names, and neither was a subset of the
	// other, so the overlap was a decision rather than a duplication to tidy.
	// It was resolved in CodeCI's favour on 2026-08-12 — a code host owns
	// repositories and git, and a change proposal is neither of those — leaving
	// this class at eleven operations. Same vendor, different contract, so an
	// org can pair either half with a different backend. See the codehost
	// package.
	ClassCodeHost Class = "CodeHost"
	// ClassRecordStore, ... land with their designs.
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
	ClassTracker,
	ClassAuthenticator,
	ClassCodeHost,
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

// HealthStatus is reachability of the backing store.
//
// ⚠️ This is the published-wire EXCEPTION to the SDK's "zero value means
// unsaid" rule. Track-B HealthResponse.status numbering is 0 = healthy, 1 =
// degraded, 2 = unavailable (proto/connector/v1/connector.proto), and
// flipping the iota would break every existing provider. New enums in this
// module still start at Unspecified and refuse a forgotten argument rather
// than defaulting it.
type HealthStatus int

const (
	Healthy HealthStatus = iota
	Degraded
	Unavailable
)

// Valid reports whether s is in the closed vocabulary. Healthy is Valid:
// it is a real published answer (wire 0), not "nothing was said".
func (s HealthStatus) Valid() bool {
	switch s {
	case Healthy, Degraded, Unavailable:
		return true
	default:
		return false
	}
}

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
