package contracts

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The time contract.
//
// # ⚠️ Replay never consults the current clock
//
// A reducer that read the wall clock would produce a different answer on replay
// than it did at acceptance — which is the one thing replay exists to detect. So
// predicates never evaluate "now": they evaluate a RECORDED acceptance time, and
// that time carries the authority that produced it.
//
// A timestamp with no named authority is not a weaker claim about time. It is no
// claim: nothing can reproduce it, nothing can reconcile it, and nothing can say
// whether the clock behind it was trustworthy.

// A ClockProvenance says how much is known about the clock a time came from.
//
// ⚠️ The zero value is Unspecified and is REFUSED, not defaulted to the weakest
// real grade. A default would make "nobody recorded the provenance" and "we know
// it was a bare local clock" indistinguishable, and only one of them is a fact.
type ClockProvenance int

const (
	// ClockProvenanceUnspecified is the zero value: nothing was said.
	ClockProvenanceUnspecified ClockProvenance = iota
	// ClockLocal is a host's own clock, unsynchronised as far as anyone knows.
	// ⚠️ It is a legitimate value and the weakest one — it must be expressible,
	// or a deployment without better would be forced to overstate.
	ClockLocal
	// ClockSynchronised is a host clock disciplined by a named time service.
	ClockSynchronised
	// ClockAttested is a time asserted by an authority that signs its answer.
	ClockAttested
)

var clockProvenanceNames = map[ClockProvenance]string{
	ClockProvenanceUnspecified: "unspecified",
	ClockLocal:                 "local",
	ClockSynchronised:          "synchronised",
	ClockAttested:              "attested",
}

var clockProvenances = []ClockProvenance{
	ClockProvenanceUnspecified, ClockLocal, ClockSynchronised, ClockAttested,
}

// ClockProvenances returns the closed vocabulary, as a copy.
func ClockProvenances() []ClockProvenance { return slices.Clone(clockProvenances) }

// Valid reports whether p is in the closed vocabulary.
func (p ClockProvenance) Valid() bool { return slices.Contains(clockProvenances, p) }

// Stated reports whether p actually says something about the clock.
func (p ClockProvenance) Stated() bool { return p.Valid() && p != ClockProvenanceUnspecified }

// String names p, or marks it invalid. It never returns the empty string.
func (p ClockProvenance) String() string {
	if n, ok := clockProvenanceNames[p]; ok {
		return n
	}
	return fmt.Sprintf("invalid_clock_provenance(%d)", int(p))
}

// MarshalJSON renders p as its NAME.
//
// ⚠️ Two reasons, and the second is structural. A record whose digest is its
// identity is encoded canonically, and the canonical form forbids JSON numbers —
// so an int-valued enum could not appear in one at all. And a name is what an
// outsider reads: "3" in an evidence bundle means nothing without this package,
// which is most of what an outsider-verifiable bundle exists to avoid.
//
// An out-of-vocabulary value is an ERROR rather than a number, so a corrupt enum
// cannot round-trip through a record and come back looking valid.
func (p ClockProvenance) MarshalJSON() ([]byte, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("%w: cannot encode %s", ErrInvalidTime, p)
	}
	return json.Marshal(p.String())
}

// UnmarshalJSON reads the name form written by [ClockProvenance.MarshalJSON].
//
// ⚠️ A JSON number is REFUSED, not accepted as the underlying integer. Accepting
// both would mean two encodings of one value, and the one that is not canonical
// would still verify — which is the whole failure the canonical form prevents.
func (p *ClockProvenance) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("%w: clock provenance must be a name, not %s", ErrInvalidTime, data)
	}
	for candidate, n := range clockProvenanceNames {
		if n == name {
			*p = candidate
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not a clock provenance", ErrInvalidTime, name)
}

// A TimeAuthority is who asserted a time, and what is known about their clock.
//
// ⚠️ It replaces a bare string. A name alone cannot be evaluated: two
// deployments can both say "host-clock" and mean a disciplined NTP client in one
// case and a virtual machine that has been suspended in the other, and a report
// that could not tell them apart would present both as the same claim.
type TimeAuthority struct {
	// Name identifies the authority. It is compared, not parsed.
	Name string `json:"name"`
	// Provenance says how much is known about its clock.
	Provenance ClockProvenance `json:"provenance"`
}

// Validate reports whether a is usable as the source of a recorded time.
func (a TimeAuthority) Validate() error {
	var problems []string
	if strings.TrimSpace(a.Name) == "" {
		problems = append(problems, "the authority is unnamed — a timestamp nothing can attribute cannot be replayed or reconciled")
	}
	if !a.Provenance.Valid() {
		problems = append(problems, fmt.Sprintf("clock provenance %s is outside the closed vocabulary", a.Provenance))
	} else if !a.Provenance.Stated() {
		problems = append(problems, "clock provenance is unspecified — it must be stated, not defaulted, "+
			"because an unrecorded provenance and a known-weak one are different facts")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidTime, strings.Join(problems, "; "))
}

// An AcceptedTime is a time recorded at acceptance, with its authority.
//
// ⚠️ This is what predicates evaluate. They never evaluate the current clock,
// and a value of this type is the only shape in which a time enters the replay
// path.
type AcceptedTime struct {
	At        time.Time     `json:"at"`
	Authority TimeAuthority `json:"authority"`
}

// Validate reports whether t is usable.
func (t AcceptedTime) Validate() error {
	if t.At.IsZero() {
		return fmt.Errorf("%w: the time is zero", ErrInvalidTime)
	}
	return t.Authority.Validate()
}

// NotBefore reports whether t is at or after other.
//
// ⚠️ It is a comparison between two RECORDED times and takes no argument that
// could be the current clock. That is deliberate: a helper that accepted a
// "now" would be the door through which the wall clock re-enters replay, and it
// would look entirely reasonable at the call site.
func (t AcceptedTime) NotBefore(other AcceptedTime) bool { return !t.At.Before(other.At) }
