// Package skillspec is the skill.yml schema shipped alongside the connector SDK
// (one repo holds both the connector contract and the skill format). The client-native
// SKILL.md is synthesised elsewhere (arqtos-cli skillsresolve) — this is just the schema.
package skillspec

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

type Skill struct {
	Name           string   `yaml:"name"`
	Description    string   `yaml:"description"`
	Triggers       []string `yaml:"triggers,omitempty"`
	AdjacentSkills []string `yaml:"adjacent_skills,omitempty"`
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

func (s Skill) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("skillspec: name is required")
	}
	if s.Description == "" {
		return fmt.Errorf("skillspec: description is required")
	}
	return nil
}
