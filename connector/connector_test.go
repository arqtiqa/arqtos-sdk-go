package connector_test

import (
	"context"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

func TestCapabilitiesHas(t *testing.T) {
	caps := connector.Capabilities{"read", "lease"}
	if !caps.Has("read") || caps.Has("rotate") {
		t.Fatalf("Has wrong: %v", caps)
	}
}

// Compile-time proof that a type can satisfy the base interface.
type stub struct{}

func (stub) Implements() connector.Class          { return connector.ClassCredentialLoader }
func (stub) Capabilities() connector.Capabilities { return connector.Capabilities{"read"} }
func (stub) Health(context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}
func (stub) Close() error { return nil }

var _ connector.Connector = stub{}

func TestClassString(t *testing.T) {
	if connector.ClassCredentialLoader != "CredentialLoader" {
		t.Fatalf("class value = %q", connector.ClassCredentialLoader)
	}
}
