// Package recorduid supplies offline UID syntax, never issuance authority.
package recorduid

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Schema identifies this declaration contract, not a legacy sequence input.
const Schema = "arqtos-uid-declaration/v1"

// Profile chooses a rendering grammar, not an authority or location.
type Profile string

const (
	Simple Profile = "simple"
	Full   Profile = "full"
)

// Mode separates new rendering from explicitly admitted read-only history.
type Mode string

const (
	Issuing    Mode = "issuing"
	Historical Mode = "historical"
)

// Namespace holds frozen origin coordinates, not a current location.
type Namespace struct {
	Profile      Profile `json:"profile" yaml:"profile"`
	KindPrefix   string  `json:"kind_prefix" yaml:"kind_prefix"`
	ProjectRef   string  `json:"project_ref,omitempty" yaml:"project_ref,omitempty"`
	ProjectSlug  string  `json:"project_slug,omitempty" yaml:"project_slug,omitempty"`
	OrgSlug      string  `json:"org_slug,omitempty" yaml:"org_slug,omitempty"`
	HostKey      string  `json:"host_key,omitempty" yaml:"host_key,omitempty"`
	RepositoryID string  `json:"repository_id,omitempty" yaml:"repository_id,omitempty"`
}

// Declaration is caller-supplied syntax metadata; its validity proves no acceptance.
type Declaration struct {
	Schema           string    `json:"schema" yaml:"schema"`
	OrgID            string    `json:"org_id" yaml:"org_id"`
	SequenceID       string    `json:"sequence_id" yaml:"sequence_id"`
	Revision         string    `json:"revision" yaml:"revision"`
	Kind             string    `json:"kind" yaml:"kind"`
	Mode             Mode      `json:"mode" yaml:"mode"`
	Namespace        Namespace `json:"namespace,omitzero" yaml:"namespace,omitempty"`
	HistoricalPrefix string    `json:"historical_prefix,omitempty" yaml:"historical_prefix,omitempty"`
}

var slug = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// Validate checks syntax only; callers must authenticate the declaration separately.
func (d Declaration) Validate() error {
	if d.Schema != Schema {
		return fmt.Errorf("recorduid: unsupported schema %q", d.Schema)
	}
	for _, field := range []struct{ name, value string }{
		{"org_id", d.OrgID}, {"sequence_id", d.SequenceID}, {"revision", d.Revision},
	} {
		if !opaque(field.value) {
			return fmt.Errorf("recorduid: invalid %s", field.name)
		}
	}
	if !slug.MatchString(d.Kind) {
		return fmt.Errorf("recorduid: invalid kind")
	}
	switch d.Mode {
	case Historical:
		if d.Namespace != (Namespace{}) {
			return fmt.Errorf("recorduid: historical declaration forbids issuing namespace")
		}
		if !opaque(d.HistoricalPrefix) {
			return fmt.Errorf("recorduid: invalid historical_prefix")
		}
		return nil
	case Issuing:
		if d.HistoricalPrefix != "" {
			return fmt.Errorf("recorduid: issuing declaration forbids historical_prefix")
		}
		return d.Namespace.validate()
	default:
		return fmt.Errorf("recorduid: unsupported mode %q", d.Mode)
	}
}

// Render proposes a canonical label; it reserves nothing and refuses history.
func (d Declaration) Render(ordinal uint64) (string, error) {
	if err := d.Validate(); err != nil {
		return "", err
	}
	if d.Mode == Historical {
		return "", fmt.Errorf("recorduid: historical declaration cannot render new labels")
	}
	if ordinal == 0 {
		return "", fmt.Errorf("recorduid: ordinal must be positive")
	}
	return d.prefix() + "-" + fmt.Sprintf("%05d", ordinal), nil
}

// Parse checks a label against the caller-selected declaration, not a global registry.
func (d Declaration) Parse(label string) (uint64, error) {
	if err := d.Validate(); err != nil {
		return 0, err
	}
	suffix, found := strings.CutPrefix(label, d.prefix()+"-")
	if !found {
		return 0, fmt.Errorf("recorduid: namespace mismatch")
	}
	for _, b := range []byte(suffix) {
		if b < '0' || b > '9' {
			return 0, fmt.Errorf("recorduid: invalid noncanonical ordinal")
		}
	}
	n, err := strconv.ParseUint(suffix, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("recorduid: ordinal overflow or invalid decimal: %w", err)
	}
	if n == 0 {
		return 0, fmt.Errorf("recorduid: ordinal must be positive")
	}
	if len(suffix) < 5 || (d.Mode == Issuing && suffix != fmt.Sprintf("%05d", n)) {
		return 0, fmt.Errorf("recorduid: noncanonical ordinal")
	}
	return n, nil
}

// ValidateSet refuses conflicting declarations within each supplied stable org scope.
// It proves neither inventory completeness nor registration or issuance authority.
func ValidateSet(declarations []Declaration) error {
	for i, d := range declarations {
		if err := d.Validate(); err != nil {
			return fmt.Errorf("recorduid: declaration %d: %w", i, err)
		}
		for _, other := range declarations[:i] {
			if d.OrgID != other.OrgID || d == other {
				continue
			}
			if d.SequenceID == other.SequenceID {
				if d.Kind != other.Kind || d.Revision == other.Revision {
					return fmt.Errorf("recorduid: conflicting declaration revision")
				}
				if d.Mode == Issuing && other.Mode == Issuing {
					return fmt.Errorf("recorduid: conflicting issuing revision")
				}
			}
			if d.prefix() == other.prefix() && d.SequenceID != other.SequenceID && (d.Mode == Issuing || other.Mode == Issuing) {
				return fmt.Errorf("recorduid: namespace collision")
			}
		}
	}
	return nil
}

func (n Namespace) validate() error {
	if !slug.MatchString(n.KindPrefix) {
		return fmt.Errorf("recorduid: invalid kind_prefix")
	}
	switch n.Profile {
	case Simple:
		if n.OrgSlug != "" || n.HostKey != "" || n.RepositoryID != "" {
			return fmt.Errorf("recorduid: simple profile forbids full coordinates")
		}
		if !opaque(n.ProjectRef) {
			return fmt.Errorf("recorduid: invalid project_ref")
		}
		if !slug.MatchString(n.ProjectSlug) {
			return fmt.Errorf("recorduid: invalid project_slug")
		}
	case Full:
		if n.ProjectRef != "" || n.ProjectSlug != "" {
			return fmt.Errorf("recorduid: full profile forbids simple coordinates")
		}
		if !slug.MatchString(n.OrgSlug) {
			return fmt.Errorf("recorduid: invalid org_slug")
		}
		if !slug.MatchString(n.HostKey) {
			return fmt.Errorf("recorduid: invalid host_key")
		}
		if !opaque(n.RepositoryID) {
			return fmt.Errorf("recorduid: invalid repository_id")
		}
	default:
		return fmt.Errorf("recorduid: unsupported profile %q", n.Profile)
	}
	return nil
}

func (d Declaration) prefix() string {
	if d.Mode == Historical {
		return d.HistoricalPrefix
	}
	n := d.Namespace
	parts := []string{n.KindPrefix, n.ProjectSlug}
	if n.Profile == Full {
		parts = []string{n.KindPrefix, n.OrgSlug, n.HostKey, n.RepositoryID}
	}
	for i, part := range parts {
		parts[i] = segment(part)
	}
	return strings.Join(parts, "-")
}

func segment(s string) string {
	const hex = "0123456789abcdef"
	var out strings.Builder
	for _, b := range []byte(s) {
		if b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '.' || b == '~' {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String()
}

func opaque(s string) bool {
	return s != "" && utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}
