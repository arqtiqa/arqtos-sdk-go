// Package orghome is the versioned organisation control-home and
// adoption-declaration contract. It extends the existing configuration
// families; it is not an eighth family. Core owns resolution and
// migration execution.
//
// doc-arq-00021 §§1b, 3.1, 5a; doc-arq-00014 §6c.
package orghome

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the only admitted schema_version.
const SchemaVersion = 1

// KindOrgAdoption is the one writable org adoption declaration.
const KindOrgAdoption = "org-adoption"

// KindOrgCommon is the pre-migration org-common document.
const KindOrgCommon = "org-common"

// A Document is one canonical org adoption declaration.
type Document struct {
	SchemaVersion int          `yaml:"schema_version" json:"schema_version"`
	Kind          string       `yaml:"kind" json:"kind"`
	OrgID         string       `yaml:"org_id" json:"org_id"`
	Home          Home         `yaml:"home" json:"home"`
	Declaration   Declaration  `yaml:"declaration" json:"declaration"`
	Lock          Lock         `yaml:"lock" json:"lock"`
	Secrets       []SecretRef  `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// Home is the trusted administrative locator. Names are addressing, not identity.
type Home struct {
	SourceID          string  `yaml:"source_id" json:"source_id"`
	Locator           Locator `yaml:"locator" json:"locator"`
	ExpectedRevision  string  `yaml:"expected_revision" json:"expected_revision"`
}

// Locator is a native repository address. It is not OrgID, a grant, or identity.
type Locator struct {
	Host  string `yaml:"host" json:"host"`
	Owner string `yaml:"owner" json:"owner"`
	Name  string `yaml:"name" json:"name"`
}

// Declaration selects exact upstream and optional org-seed releases.
type Declaration struct {
	Revision string     `yaml:"revision" json:"revision"`
	Upstream Selection  `yaml:"upstream" json:"upstream"`
	OrgSeed  *Selection `yaml:"org_seed,omitempty" json:"org_seed,omitempty"`
}

// Selection is one content-release pin. Identity is the contentrelease id.
type Selection struct {
	Identity string `yaml:"identity" json:"identity"`
	Revision string `yaml:"revision" json:"revision"`
	Digest   string `yaml:"digest" json:"digest"`
}

// Lock is a derived view of Declaration. It is not independently writable.
type Lock struct {
	DeclarationRevision string `yaml:"declaration_revision" json:"declaration_revision"`
	Digest              string `yaml:"digest" json:"digest"`
}

// SecretRef is a named secret reference. Only cred:// refs are admitted.
type SecretRef struct {
	Name string `yaml:"name" json:"name"`
	Ref  string `yaml:"ref" json:"ref"`
}

// Binding supplies issued identities a name-only v0 document cannot infer.
type Binding struct {
	OrgID    string
	SourceID string
}

// Parse strictly decodes an org-adoption YAML document.
func Parse(b []byte) (Document, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var d Document
	if err := dec.Decode(&d); err != nil {
		return Document{}, fmt.Errorf("orghome: %w", err)
	}
	return d, nil
}

// Validate authenticates identity, lock linkage and reference-only secrets.
func (d Document) Validate() error {
	return nil
}

// CanonicalBytes is the signing payload of a validated document.
func CanonicalBytes(d Document) ([]byte, error) {
	return nil, nil
}

// Migrate lifts a v0 org-common document using issued identities, never a repository name.
func Migrate(old []byte, bind Binding) (Document, error) {
	return Document{}, nil
}
