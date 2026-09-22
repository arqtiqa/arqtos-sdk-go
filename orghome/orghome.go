// Package orghome is the versioned organisation control-home and
// adoption-declaration contract. It extends the existing configuration
// families; it is not an eighth family. Core owns resolution and
// migration execution.
//
// doc-arq-00021 §§1b, 3.1, 5a; doc-arq-00014 §6c.
package orghome

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the only admitted schema_version.
const SchemaVersion = 1

// KindOrgAdoption is the one writable org adoption declaration.
const KindOrgAdoption = "org-adoption"

// KindOrgCommon is the pre-migration org-common document.
const KindOrgCommon = "org-common"

var mutableAliases = map[string]bool{
	"latest": true, "main": true, "head": true, "origin/main": true,
}

// A Document is one canonical org adoption declaration.
type Document struct {
	SchemaVersion int         `yaml:"schema_version" json:"schema_version"`
	Kind          string      `yaml:"kind" json:"kind"`
	OrgID         string      `yaml:"org_id" json:"org_id"`
	Home          Home        `yaml:"home" json:"home"`
	Declaration   Declaration `yaml:"declaration" json:"declaration"`
	Lock          Lock        `yaml:"lock" json:"lock"`
	Secrets       []SecretRef `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// Home is the trusted administrative locator. Names are addressing, not identity.
type Home struct {
	SourceID         string  `yaml:"source_id" json:"source_id"`
	Locator          Locator `yaml:"locator" json:"locator"`
	ExpectedRevision string  `yaml:"expected_revision" json:"expected_revision"`
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

type v0 struct {
	SchemaVersion    int        `yaml:"schema_version"`
	Kind             string     `yaml:"kind"`
	Name             string     `yaml:"name"`
	ExpectedRevision string     `yaml:"expected_revision"`
	Locator          Locator    `yaml:"locator"`
	Upstream         Selection  `yaml:"upstream"`
	OrgSeed          *Selection `yaml:"org_seed"`
	Lock             struct {
		Digest string `yaml:"digest"`
	} `yaml:"lock"`
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
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("orghome: schema_version %d is not admitted (want %d)", d.SchemaVersion, SchemaVersion)
	}
	if d.Kind != KindOrgAdoption {
		return fmt.Errorf("orghome: kind %q is not %s", d.Kind, KindOrgAdoption)
	}
	if d.OrgID == "" {
		return fmt.Errorf("orghome: org_id: required")
	}
	if d.Home.SourceID == "" {
		return fmt.Errorf("orghome: ambiguous root: source_id is required")
	}
	if d.Home.Locator.Host == "" || d.Home.Locator.Owner == "" || d.Home.Locator.Name == "" {
		return fmt.Errorf("orghome: home locator requires host, owner and name")
	}
	if d.OrgID == d.Home.Locator.Name || d.Home.SourceID == d.Home.Locator.Name {
		return fmt.Errorf("orghome: source names and locators are not identity (repository name %q)", d.Home.Locator.Name)
	}
	if err := exact("expected_revision", d.Home.ExpectedRevision); err != nil {
		return err
	}
	if err := exact("declaration.revision", d.Declaration.Revision); err != nil {
		return err
	}
	if err := selection("upstream", d.Declaration.Upstream); err != nil {
		return err
	}
	if d.Declaration.OrgSeed != nil {
		if err := selection("org_seed", *d.Declaration.OrgSeed); err != nil {
			return err
		}
	}
	if d.Lock.DeclarationRevision != d.Declaration.Revision {
		return fmt.Errorf("orghome: lock is independently writable: declaration_revision %q != %q", d.Lock.DeclarationRevision, d.Declaration.Revision)
	}
	if err := digest("lock.digest", d.Lock.Digest); err != nil {
		return err
	}
	for i, s := range d.Secrets {
		if s.Name == "" {
			return fmt.Errorf("orghome: secrets[%d]: name required", i)
		}
		if !strings.HasPrefix(s.Ref, "cred://") {
			return fmt.Errorf("orghome: secrets[%d]: reference-only secret configuration (want cred://)", i)
		}
	}
	return nil
}

// CanonicalBytes is the signing payload of a validated document.
func CanonicalBytes(d Document) ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("orghome: canonical: %w", err)
	}
	return append(b, '\n'), nil
}

// Migrate lifts a v0 org-common document using issued identities, never a repository name.
func Migrate(old []byte, bind Binding) (Document, error) {
	dec := yaml.NewDecoder(bytes.NewReader(old))
	dec.KnownFields(true)
	var src v0
	if err := dec.Decode(&src); err != nil {
		return Document{}, fmt.Errorf("orghome: migrate: %w", err)
	}
	if src.Kind != KindOrgCommon {
		return Document{}, fmt.Errorf("orghome: migrate: kind %q is not %s", src.Kind, KindOrgCommon)
	}
	if bind.OrgID == "" || bind.SourceID == "" || bind.OrgID == src.Name || bind.SourceID == src.Name || bind.SourceID == src.Locator.Name {
		return Document{}, fmt.Errorf("orghome: migrate: cannot infer identity from a repository name")
	}
	d := Document{
		SchemaVersion: SchemaVersion,
		Kind:          KindOrgAdoption,
		OrgID:         bind.OrgID,
		Home: Home{
			SourceID:         bind.SourceID,
			Locator:          src.Locator,
			ExpectedRevision: src.ExpectedRevision,
		},
		Declaration: Declaration{
			Revision: "1",
			Upstream: src.Upstream,
			OrgSeed:  src.OrgSeed,
		},
		Lock: Lock{
			DeclarationRevision: "1",
			Digest:              src.Lock.Digest,
		},
	}
	if err := d.Validate(); err != nil {
		return Document{}, fmt.Errorf("orghome: migrate: %w", err)
	}
	return d, nil
}

func selection(field string, s Selection) error {
	if s.Identity == "" || s.Revision == "" {
		return fmt.Errorf("orghome: %s: identity and revision required", field)
	}
	if err := exact(field+".revision", s.Revision); err != nil {
		return err
	}
	return digest(field+".digest", s.Digest)
}

func exact(field, v string) error {
	if v == "" {
		return fmt.Errorf("orghome: %s: required", field)
	}
	if mutableAliases[strings.ToLower(v)] {
		return fmt.Errorf("orghome: %s %q is a mutable latest alias", field, v)
	}
	return nil
}

func digest(field, v string) error {
	const prefix = "sha256:"
	if !strings.HasPrefix(v, prefix) {
		return fmt.Errorf("orghome: %s: must be sha256:<hex>", field)
	}
	h := strings.TrimPrefix(v, prefix)
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != sha256.Size {
		return fmt.Errorf("orghome: %s: must be sha256:<hex>", field)
	}
	return nil
}
