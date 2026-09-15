// Package recorduid supplies offline UID syntax, never issuance authority.
package recorduid

import "errors"

// Schema identifies this declaration contract, not a legacy sequence input.
const Schema = "arqtos-uid-declaration/v1"

type Profile string

const (
	Simple Profile = "simple"
	Full   Profile = "full"
)

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

func (d Declaration) Validate() error { return errors.New("recorduid: codec not implemented") }

func (d Declaration) Render(ordinal uint64) (string, error) {
	return "", errors.New("recorduid: codec not implemented")
}

func (d Declaration) Parse(label string) (uint64, error) {
	return 0, errors.New("recorduid: codec not implemented")
}

func ValidateSet(declarations []Declaration) error {
	return errors.New("recorduid: codec not implemented")
}
