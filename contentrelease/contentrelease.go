// Package contentrelease is the versioned GoldenBaseRelease contract.
// Parse strictly decodes YAML (unknown fields rejected). Validate closes
// identity, digests, compatibility and trust bindings. CanonicalBytes is
// the publisher/consumer signing payload. Core owns activation; this
// package does not execute packs or methods.
//
// doc-arq-00021 §§1b, 3.1, 5a.
package contentrelease

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the only admitted schema_version.
const SchemaVersion = 1

// KindContentRelease distinguishes this document from a connector manifest.
const KindContentRelease = "content-release"

// Profile selects the portable or runtime qualification surface.
type Profile string

const (
	ProfilePortable Profile = "portable"
	ProfileRuntime  Profile = "runtime"
)

// Qualification is the evidence floor the release claims, not a proof it ran.
type Qualification string

const (
	QualificationReviewedDigest Qualification = "reviewed-digest"
	QualificationDSSETUF        Qualification = "dsse-tuf"
)

// Lifecycle is how an artefact is updated after adoption.
type Lifecycle string

const (
	LifecycleManaged  Lifecycle = "managed"
	LifecycleScaffold Lifecycle = "scaffold"
	LifecycleAuthored Lifecycle = "authored"
)

// admittedTypes are the required-type vocabulary. Optional unknown types
// may be omitted when dependency-safe; required unknown types refuse.
var admittedTypes = map[string]bool{
	"golden-base-release": true,
	"skill":               true,
	"pack":                true,
	"method":              true,
	"primer":              true,
	"template":            true,
	"contract-projection": true,
	"loop-definition":     true,
	"radar":               true,
}

var mutableAliases = map[string]bool{
	"latest": true, "main": true, "head": true, "origin/main": true,
}

// Manifest is one content-release document.
type Manifest struct {
	SchemaVersion int            `yaml:"schema_version" json:"schema_version"`
	Kind          string         `yaml:"kind" json:"kind"`
	Profile       Profile        `yaml:"profile" json:"profile"`
	Qualification Qualification  `yaml:"qualification" json:"qualification"`
	Identity      Identity       `yaml:"identity" json:"identity"`
	Revision      string         `yaml:"revision" json:"revision"`
	Digest        string         `yaml:"digest" json:"digest"`
	Files         []File         `yaml:"files" json:"files"`
	Artefacts     []Artefact     `yaml:"artefacts" json:"artefacts"`
	Clients       []ClientTarget `yaml:"clients,omitempty" json:"clients,omitempty"`
	Runtime       *Runtime       `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Trust         Trust          `yaml:"trust" json:"trust"`
}

// Identity is the logical release, not a record UID and not a digest.
type Identity struct {
	ID        string `yaml:"id" json:"id"`
	Type      string `yaml:"type" json:"type"`
	Publisher string `yaml:"publisher" json:"publisher"`
	Source    string `yaml:"source" json:"source"`
}

// File is one exact-byte member of the release.
type File struct {
	Path   string `yaml:"path" json:"path"`
	Digest string `yaml:"digest" json:"digest"`
}

// Artefact is one qualified logical member. ID is type-prefixed, not a UID.
type Artefact struct {
	ID        string    `yaml:"id" json:"id"`
	Type      string    `yaml:"type" json:"type"`
	Digest    string    `yaml:"digest" json:"digest"`
	Source    string    `yaml:"source,omitempty" json:"source,omitempty"`
	Required  bool      `yaml:"required" json:"required"`
	Lifecycle Lifecycle `yaml:"lifecycle" json:"lifecycle"`
	DependsOn []string  `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Replaces  string    `yaml:"replaces,omitempty" json:"replaces,omitempty"`
}

// ClientTarget is a consuming client the release claims to support.
type ClientTarget struct {
	Name       string `yaml:"name" json:"name"`
	MinVersion string `yaml:"min_version,omitempty" json:"min_version,omitempty"`
}

// Runtime is binary compatibility for the runtime profile.
type Runtime struct {
	MinBinary string `yaml:"min_binary" json:"min_binary"`
}

// Trust binds signer and update-root refs to the publisher/source namespace.
type Trust struct {
	Publisher     string `yaml:"publisher" json:"publisher"`
	Namespace     string `yaml:"namespace" json:"namespace"`
	SignerRef     string `yaml:"signer_ref" json:"signer_ref"`
	UpdateRootRef string `yaml:"update_root_ref" json:"update_root_ref"`
}

// Parse strictly decodes a content-release YAML document.
func Parse(b []byte) (Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("contentrelease: %w", err)
	}
	return m, nil
}

// Validate refuses unknown versions, connector-shaped kinds, mutable aliases,
// private paths, secret refs, cycles, ambiguous identities, required unknown
// types, and trust bindings that do not match the publisher/source namespace.
func (m Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("contentrelease: schema_version %d is not admitted (want %d)", m.SchemaVersion, SchemaVersion)
	}
	if m.Kind != KindContentRelease {
		return fmt.Errorf("contentrelease: kind %q is not %s", m.Kind, KindContentRelease)
	}
	if err := closed("profile", string(m.Profile), string(ProfilePortable), string(ProfileRuntime)); err != nil {
		return err
	}
	if err := closed("qualification", string(m.Qualification), string(QualificationReviewedDigest), string(QualificationDSSETUF)); err != nil {
		return err
	}
	if m.Identity.ID == "" || m.Identity.Type == "" || m.Identity.Publisher == "" || m.Identity.Source == "" {
		return fmt.Errorf("contentrelease: identity requires id, type, publisher and source")
	}
	if !admittedTypes[m.Identity.Type] {
		return fmt.Errorf("contentrelease: identity type %q is not admitted", m.Identity.Type)
	}
	if m.Revision == "" {
		return fmt.Errorf("contentrelease: revision: required")
	}
	if mutableAliases[strings.ToLower(m.Revision)] {
		return fmt.Errorf("contentrelease: revision %q is a mutable latest alias", m.Revision)
	}
	if err := digest("digest", m.Digest); err != nil {
		return err
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("contentrelease: files: required")
	}
	for i, f := range m.Files {
		if err := filePath(f.Path); err != nil {
			return fmt.Errorf("contentrelease: files[%d]: %w", i, err)
		}
		if err := digest("digest", f.Digest); err != nil {
			return fmt.Errorf("contentrelease: files[%d]: %w", i, err)
		}
	}
	if len(m.Artefacts) == 0 {
		return fmt.Errorf("contentrelease: artefacts: required")
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for i, a := range m.Artefacts {
		if a.ID == "" {
			return fmt.Errorf("contentrelease: artefacts[%d]: id required", i)
		}
		if seen[a.ID] {
			return fmt.Errorf("contentrelease: duplicate artefact id %q", a.ID)
		}
		seen[a.ID] = true
		ids[a.ID] = true
		if a.Type == "" {
			return fmt.Errorf("contentrelease: artefacts[%d]: type required", i)
		}
		if a.Required && !admittedTypes[a.Type] {
			return fmt.Errorf("contentrelease: artefacts[%d]: required type %q is not admitted", i, a.Type)
		}
		if err := digest("digest", a.Digest); err != nil {
			return fmt.Errorf("contentrelease: artefacts[%d]: %w", i, err)
		}
		if err := closed("lifecycle", string(a.Lifecycle), string(LifecycleManaged), string(LifecycleScaffold), string(LifecycleAuthored)); err != nil {
			return fmt.Errorf("contentrelease: artefacts[%d]: %w", i, err)
		}
	}
	for i, a := range m.Artefacts {
		for _, dep := range a.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("contentrelease: artefacts[%d]: depends_on %q is not in this release", i, dep)
			}
		}
	}
	if cycle := artefactCycle(m.Artefacts); cycle != "" {
		return fmt.Errorf("contentrelease: artefact dependency cycle: %s", cycle)
	}
	if m.Profile == ProfileRuntime && (m.Runtime == nil || m.Runtime.MinBinary == "") {
		return fmt.Errorf("contentrelease: runtime.min_binary is required for profile %s", ProfileRuntime)
	}
	if err := m.Trust.validate(m.Identity); err != nil {
		return err
	}
	return nil
}

// OptionalOmissions names optional artefacts whose type is not admitted.
func (m Manifest) OptionalOmissions() []string {
	var out []string
	for _, a := range m.Artefacts {
		if a.Required || admittedTypes[a.Type] {
			continue
		}
		out = append(out, a.ID)
	}
	return out
}

// CanonicalBytes is the stable signing payload for a validated manifest.
func CanonicalBytes(m Manifest) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("contentrelease: canonical: %w", err)
	}
	return append(b, '\n'), nil
}

func (t Trust) validate(id Identity) error {
	if t.Publisher == "" || t.Namespace == "" || t.SignerRef == "" || t.UpdateRootRef == "" {
		return fmt.Errorf("contentrelease: trust requires publisher, namespace, signer_ref and update_root_ref")
	}
	if t.Publisher != id.Publisher {
		return fmt.Errorf("contentrelease: trust publisher %q does not match identity publisher %q", t.Publisher, id.Publisher)
	}
	if t.Namespace != id.Source {
		return fmt.Errorf("contentrelease: trust namespace %q does not match identity source %q", t.Namespace, id.Source)
	}
	if selfRef(t.SignerRef) || selfRef(t.UpdateRootRef) {
		return fmt.Errorf("contentrelease: trust refs must not establish the release's own trust")
	}
	if !namespaceRef(t.SignerRef, id.Publisher, id.Source) || !namespaceRef(t.UpdateRootRef, id.Publisher, id.Source) {
		return fmt.Errorf("contentrelease: trust refs must bind to publisher %q or source namespace %q", id.Publisher, id.Source)
	}
	return nil
}

func namespaceRef(ref, publisher, source string) bool {
	return strings.HasPrefix(ref, "ns:"+publisher+"/") || strings.HasPrefix(ref, "ns:"+source+"/")
}

func selfRef(s string) bool {
	n := strings.ToLower(strings.TrimSpace(s))
	return n == "self" || n == "this" || n == "inline"
}

func filePath(p string) error {
	if p == "" {
		return fmt.Errorf("path: required")
	}
	if strings.Contains(p, "op://") {
		return fmt.Errorf("path %q is a secret reference (op://)", p)
	}
	if path.IsAbs(p) || strings.HasPrefix(p, "~") || strings.Contains(p, "..") {
		return fmt.Errorf("path %q is not a release-relative path", p)
	}
	lower := strings.ToLower(p)
	if strings.Contains(lower, "/users/") || strings.HasPrefix(lower, "users/") || strings.Contains(lower, "/home/") {
		return fmt.Errorf("path %q encodes a private deployment path", p)
	}
	return nil
}

func digest(field, v string) error {
	const prefix = "sha256:"
	if !strings.HasPrefix(v, prefix) {
		return fmt.Errorf("%s: must be sha256:<hex>", field)
	}
	h := strings.TrimPrefix(v, prefix)
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != sha256.Size {
		return fmt.Errorf("%s: must be sha256:<hex>", field)
	}
	return nil
}

func closed(field, got string, want ...string) error {
	if got == "" {
		return fmt.Errorf("contentrelease: %s: required", field)
	}
	for _, w := range want {
		if got == w {
			return nil
		}
	}
	return fmt.Errorf("contentrelease: %s %q is not admitted", field, got)
}

func artefactCycle(arts []Artefact) string {
	deps := map[string][]string{}
	for _, a := range arts {
		deps[a.ID] = append([]string{}, a.DependsOn...)
	}
	const (
		unseen = iota
		open
		done
	)
	state := map[string]int{}
	var stack []string
	var found string
	var walk func(string) bool
	walk = func(id string) bool {
		switch state[id] {
		case done:
			return false
		case open:
			found = strings.Join(append(stack, id), " -> ")
			return true
		}
		state[id] = open
		stack = append(stack, id)
		for _, d := range deps[id] {
			if walk(d) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
		return false
	}
	for _, a := range arts {
		if walk(a.ID) {
			return found
		}
	}
	return ""
}
