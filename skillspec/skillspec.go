// Package skillspec is the published skill.yml contract. Parse strictly
// decodes catalogue metadata (unknown fields rejected) and Validate checks
// declared values. Consumers load files, select versions, and emit
// client-native surfaces; this package is bytes in, Skill out.
package skillspec

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Visibility is a skill.yml visibility value.
type Visibility string

const (
	VisibilityPublic         Visibility = "public"
	VisibilityPrivateOverlay Visibility = "private-overlay"
	VisibilityPrivate        Visibility = "private"
)

// Classification is sensitivity_required.max_classification.
type Classification string

const (
	ClassificationPublic       Classification = "public"
	ClassificationInternal     Classification = "internal"
	ClassificationConfidential Classification = "confidential"
	ClassificationPrivileged   Classification = "privileged"
)

// AuditTier is a skill.yml audit_tier value.
type AuditTier string

const (
	AuditTierStandard  AuditTier = "standard"
	AuditTierEnhanced  AuditTier = "enhanced"
	AuditTierImmutable AuditTier = "immutable"
)

// ChurnClass is a skill.yml churn_class value.
type ChurnClass string

const (
	ChurnClassLow    ChurnClass = "low"
	ChurnClassMedium ChurnClass = "medium"
	ChurnClassHigh   ChurnClass = "high"
)

// Structure is a skill.yml structure value.
type Structure string

const (
	StructureHouse  Structure = "house"
	StructureRubric Structure = "rubric"
)

// FineTune is the optional fine_tune mapping on a published skill.
type FineTune struct {
	Eligible              bool     `yaml:"eligible"`
	RequiredCorpusClasses []string `yaml:"required_corpus_classes,omitempty"`
	RetuneTriggers        []string `yaml:"retune_triggers,omitempty"`
}

// Sensitivity is the optional sensitivity_required mapping.
type Sensitivity struct {
	MaxClassification Classification `yaml:"max_classification"`
}

// Skill is a published skill.yml document.
type Skill struct {
	Name                string       `yaml:"name"`
	Version             string       `yaml:"version,omitempty"`
	Visibility          Visibility   `yaml:"visibility,omitempty"`
	Description         string       `yaml:"description"`
	CapabilityClass     string       `yaml:"capability_class,omitempty"`
	MountPriority       *int         `yaml:"mount_priority,omitempty"`
	FineTune            *FineTune    `yaml:"fine_tune,omitempty"`
	AdjacentSkills      []string     `yaml:"adjacent_skills,omitempty"`
	SensitivityRequired *Sensitivity `yaml:"sensitivity_required,omitempty"`
	AuditTier           AuditTier    `yaml:"audit_tier,omitempty"`
	CoolDownTurns       *int         `yaml:"cool_down_turns,omitempty"`
	ChurnClass          ChurnClass   `yaml:"churn_class,omitempty"`
	Paths               []string     `yaml:"paths,omitempty"`
	Pinned              bool         `yaml:"pinned,omitempty"`
	Structure           Structure    `yaml:"structure,omitempty"`
	Triggers            []string     `yaml:"triggers,omitempty"`
	FineTuneCompatible  bool         `yaml:"fine_tune_compatible,omitempty"`
}

// Parse strictly decodes a skill.yml, rejecting unknown fields.
func Parse(b []byte) (Skill, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var s Skill
	if err := dec.Decode(&s); err != nil {
		return Skill{}, fmt.Errorf("skillspec: %w", err)
	}
	return s, nil
}

// Validate requires name and description. Other catalogue fields are
// optional so existing minimal specimens stay valid; when present they
// must match the published vocabularies and shapes.
func (s Skill) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("skillspec: name: required")
	}
	if s.Description == "" {
		return fmt.Errorf("skillspec: description: required")
	}
	if s.Version != "" && !isSemverXYZ(s.Version) {
		return fmt.Errorf("skillspec: version: must be semver X.Y.Z, got %q", s.Version)
	}
	if err := closed("visibility", string(s.Visibility), "private", "private-overlay", "public"); err != nil {
		return err
	}
	if s.MountPriority != nil {
		n := *s.MountPriority
		if n < 1 || n > 10 {
			return fmt.Errorf("skillspec: mount_priority: must be an integer 1-10, got %d", n)
		}
	}
	if s.SensitivityRequired != nil {
		mc := string(s.SensitivityRequired.MaxClassification)
		if mc == "" {
			return fmt.Errorf("skillspec: max_classification: required")
		}
		if err := closed("max_classification", mc, "confidential", "internal", "privileged", "public"); err != nil {
			return err
		}
	}
	if err := closed("audit_tier", string(s.AuditTier), "enhanced", "immutable", "standard"); err != nil {
		return err
	}
	if s.CoolDownTurns != nil && *s.CoolDownTurns < 0 {
		return fmt.Errorf("skillspec: cool_down_turns: must be an integer >= 0, got %d", *s.CoolDownTurns)
	}
	if err := closed("churn_class", string(s.ChurnClass), "high", "low", "medium"); err != nil {
		return err
	}
	if err := closed("structure", string(s.Structure), "house", "rubric"); err != nil {
		return err
	}
	return nil
}

func closed(field, got string, allowed ...string) error {
	if got == "" {
		return nil
	}
	for _, a := range allowed {
		if got == a {
			return nil
		}
	}
	return fmt.Errorf("skillspec: %s: must be one of %s, got %q", field, strings.Join(allowed, ", "), got)
}

func isSemverXYZ(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}
