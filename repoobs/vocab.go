package repoobs

import (
	"encoding/json"
	"fmt"
	"slices"
)

// A Presence says whether an axis was observed, and how.
type Presence int

const (
	PresenceUnspecified Presence = iota
	PresenceUnknown
	PresenceDenied
	PresenceUnsupported
	PresenceMeasured
)

var presenceNames = map[Presence]string{
	PresenceUnspecified: "unspecified",
	PresenceUnknown:     "unknown",
	PresenceDenied:      "denied",
	PresenceUnsupported: "unsupported",
	PresenceMeasured:    "measured",
}

var presences = func() []Presence {
	out := make([]Presence, 0, len(presenceNames))
	for p := range presenceNames {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}()

// Presences returns the closed presence vocabulary, as a copy.
func Presences() []Presence { return slices.Clone(presences) }

func (p Presence) Valid() bool {
	_, ok := presenceNames[p]
	return ok
}

func (p Presence) Stated() bool { return p.Valid() && p != PresenceUnspecified }

func (p Presence) String() string {
	if n, ok := presenceNames[p]; ok {
		return n
	}
	return fmt.Sprintf("invalid_presence(%d)", int(p))
}

func (p Presence) MarshalJSON() ([]byte, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("%w: cannot encode %s", ErrInvalid, p)
	}
	return json.Marshal(p.String())
}

func (p *Presence) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("%w: presence must be a name, not %s", ErrInvalid, data)
	}
	for candidate, n := range presenceNames {
		if n == name {
			*p = candidate
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not a presence", ErrInvalid, name)
}

// A Coverage is how completely the source observation was taken.
type Coverage int

const (
	CoverageUnspecified Coverage = iota
	CoverageUnknown
	CoverageDenied
	CoverageUnsupported
	CoverageMeasured
)

var coverageNames = map[Coverage]string{
	CoverageUnspecified: "unspecified",
	CoverageUnknown:     "unknown",
	CoverageDenied:      "denied",
	CoverageUnsupported: "unsupported",
	CoverageMeasured:    "measured",
}

func (c Coverage) Valid() bool {
	_, ok := coverageNames[c]
	return ok
}

func (c Coverage) Stated() bool { return c.Valid() && c != CoverageUnspecified }

func (c Coverage) String() string {
	if n, ok := coverageNames[c]; ok {
		return n
	}
	return fmt.Sprintf("invalid_coverage(%d)", int(c))
}

func (c Coverage) MarshalJSON() ([]byte, error) {
	if !c.Valid() {
		return nil, fmt.Errorf("%w: cannot encode %s", ErrInvalid, c)
	}
	return json.Marshal(c.String())
}

func (c *Coverage) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("%w: coverage must be a name, not %s", ErrInvalid, data)
	}
	for candidate, n := range coverageNames {
		if n == name {
			*c = candidate
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not a coverage", ErrInvalid, name)
}

// A Fetch is whether the latest source fetch succeeded.
type Fetch int

const (
	FetchUnspecified Fetch = iota
	FetchFailed
	FetchSucceeded
)

var fetchNames = map[Fetch]string{
	FetchUnspecified: "unspecified",
	FetchFailed:      "failed",
	FetchSucceeded:   "succeeded",
}

func (f Fetch) Valid() bool {
	_, ok := fetchNames[f]
	return ok
}

func (f Fetch) String() string {
	if n, ok := fetchNames[f]; ok {
		return n
	}
	return fmt.Sprintf("invalid_fetch(%d)", int(f))
}

func (f Fetch) MarshalJSON() ([]byte, error) {
	if !f.Valid() {
		return nil, fmt.Errorf("%w: cannot encode %s", ErrInvalid, f)
	}
	if f == FetchUnspecified {
		return []byte("null"), nil
	}
	return json.Marshal(f.String())
}

func (f *Fetch) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*f = FetchUnspecified
		return nil
	}
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("%w: fetch must be a name, not %s", ErrInvalid, data)
	}
	for candidate, n := range fetchNames {
		if n == name {
			*f = candidate
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not a fetch", ErrInvalid, name)
}

// A Relation is this tree's pinned base versus the source observation.
type Relation int

const (
	RelationUnspecified Relation = iota
	RelationEqual
	RelationBehind
	RelationAhead
	RelationDiverged
)

var relationNames = map[Relation]string{
	RelationUnspecified: "unspecified",
	RelationEqual:       "equal",
	RelationBehind:      "behind",
	RelationAhead:       "ahead",
	RelationDiverged:    "diverged",
}

func (r Relation) Valid() bool {
	_, ok := relationNames[r]
	return ok
}

func (r Relation) Stated() bool { return r.Valid() && r != RelationUnspecified }

func (r Relation) String() string {
	if n, ok := relationNames[r]; ok {
		return n
	}
	return fmt.Sprintf("invalid_relation(%d)", int(r))
}

func (r Relation) MarshalJSON() ([]byte, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("%w: cannot encode %s", ErrInvalid, r)
	}
	if r == RelationUnspecified {
		return []byte("null"), nil
	}
	return json.Marshal(r.String())
}

func (r *Relation) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*r = RelationUnspecified
		return nil
	}
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("%w: relation must be a name, not %s", ErrInvalid, data)
	}
	for candidate, n := range relationNames {
		if n == name {
			*r = candidate
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not a relation", ErrInvalid, name)
}

// A Durability is the implemented draft evidence level.
type Durability int

const (
	DurabilityUnspecified Durability = iota
	DurabilityWorkingFiles
	DurabilityLocalGit
	DurabilityCheckpointed
	DurabilityTransferred
	DurabilityPublished
)

var durabilityNames = map[Durability]string{
	DurabilityUnspecified:  "unspecified",
	DurabilityWorkingFiles: "working_files",
	DurabilityLocalGit:     "local_git",
	DurabilityCheckpointed: "checkpointed",
	DurabilityTransferred:  "transferred",
	DurabilityPublished:    "published",
}

func (d Durability) Valid() bool {
	_, ok := durabilityNames[d]
	return ok
}

func (d Durability) String() string {
	if n, ok := durabilityNames[d]; ok {
		return n
	}
	return fmt.Sprintf("invalid_durability(%d)", int(d))
}

func (d Durability) MarshalJSON() ([]byte, error) {
	if !d.Valid() {
		return nil, fmt.Errorf("%w: cannot encode %s", ErrInvalid, d)
	}
	if d == DurabilityUnspecified {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

func (d *Durability) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*d = DurabilityUnspecified
		return nil
	}
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("%w: durability must be a name, not %s", ErrInvalid, data)
	}
	for candidate, n := range durabilityNames {
		if n == name {
			*d = candidate
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not a durability", ErrInvalid, name)
}
