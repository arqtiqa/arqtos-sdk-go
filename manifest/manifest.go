// Package manifest is the connector manifest schema (a connector.yml a connector
// author ships alongside their code) declaring the connector's name, the
// connector-class it Implements, its runtime Kind (declarative | provider |
// native), capabilities/supports, its refs-only Auth wiring, and — for
// out-of-process providers — the minimum host version it requires. Parse is
// strict (unknown fields rejected, mirroring skillspec.Parse); Validate closes
// the Kind/Implements enums and enforces the refs-only Auth invariant: an Auth
// value must be an op:// secret reference or a bare environment-variable NAME,
// never literal secret material.
package manifest

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// Kind is a connector's runtime shape.
type Kind string

const (
	KindDeclarative Kind = "declarative"
	KindProvider    Kind = "provider"
	KindNative      Kind = "native"
)

// knownImplements is the closed set of connector classes a manifest may
// declare in Implements. It composes connector.Class's own constants so the
// manifest enum can never drift out of sync with the SDK's known classes.
var knownImplements = map[connector.Class]bool{
	connector.ClassCredentialLoader: true,
}

// envNameRE matches a bare environment-variable NAME (e.g. INFISICAL_TOKEN):
// upper-case letters, digits, and underscores, not starting with a digit.
// It is deliberately strict so literal secret material (mixed case,
// punctuation, base64/hex-looking tokens) never slips through as an "env
// name" — Auth values are refs-or-names only, never the material itself.
var envNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// Doc is a connector manifest.
type Doc struct {
	Name           string            `yaml:"name"`
	Implements     connector.Class   `yaml:"implements"`
	Kind           Kind              `yaml:"kind"`
	SchemaVersion  string            `yaml:"schema_version,omitempty"`
	Capabilities   []string          `yaml:"capabilities,omitempty"`
	Supports       map[string]string `yaml:"supports,omitempty"`
	Auth           map[string]string `yaml:"auth,omitempty"`
	MinHostVersion string            `yaml:"min_host_version,omitempty"`
}

// Parse strictly decodes a connector manifest, rejecting unknown fields.
func Parse(b []byte) (Doc, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var d Doc
	if err := dec.Decode(&d); err != nil {
		return Doc{}, fmt.Errorf("manifest: %w", err)
	}
	return d, nil
}

// Validate requires Name, closes the Kind and Implements enums, enforces the
// refs-only Auth invariant on every entry, and requires MinHostVersion when
// Kind is provider (an out-of-process connector the host must dial by
// contract version; declarative/native connectors carry no such gate).
func (d Doc) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	switch d.Kind {
	case KindDeclarative, KindProvider, KindNative:
	default:
		return fmt.Errorf("manifest: unknown kind %q", d.Kind)
	}
	if !knownImplements[d.Implements] {
		return fmt.Errorf("manifest: unknown implements %q", d.Implements)
	}
	for name, v := range d.Auth {
		if err := validateAuthEntry(v); err != nil {
			return fmt.Errorf("manifest: auth[%s]: %w", name, err)
		}
	}
	if d.Kind == KindProvider && d.MinHostVersion == "" {
		return fmt.Errorf("manifest: min_host_version is required for kind: provider")
	}
	return nil
}

// validateAuthEntry enforces the refs-only invariant: an Auth value must be
// either a well-formed op:// secret reference or a bare environment-variable
// NAME — the name of a variable to read at runtime, never the secret itself.
func validateAuthEntry(v string) error {
	if strings.HasPrefix(v, "op://") {
		if _, err := ref.Parse(v); err != nil {
			return fmt.Errorf("invalid ref: %w", err)
		}
		return nil
	}
	if envNameRE.MatchString(v) {
		return nil
	}
	return fmt.Errorf("must be an op:// ref or an ENV_NAME, not literal material: %q", v)
}
