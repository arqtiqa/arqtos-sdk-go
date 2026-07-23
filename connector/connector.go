// Package connector is the base contract every arqtos connector implements,
// independent of connector-class. Class-specific contracts (e.g.
// credential.CredentialLoader) embed connector.Connector.
package connector

import "context"

type Class string

const (
	ClassCredentialLoader Class = "CredentialLoader"
	// ClassRecordStore, ClassTracker, ClassCodeHost, ... land with their designs.
)

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
