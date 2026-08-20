package contracts

import (
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
)

// The ledger bootstrap.
//
// # Every chain needs a first link, and the first link is the one nothing else
// vouches for
//
// [RepositoryGenesis] is that link. It says which repository the ledger governs,
// which keys are root, which charter is in force, and what authority everything
// else attenuates from. Nothing inside the tape can establish it, because it is
// what the tape begins from — which is why the question of WHICH genesis a
// verifier should trust is answered outside this package, by the trust-anchor
// rule in [github.com/arqtiqa/arqtos-sdk-go/verify].
//
// # ⚠️ Replaying a chain proves consistency, never canonicity
//
// A compromised administrator can mint an alternate genesis and build a chain on
// it that replays perfectly. Every claim this file supports is therefore
// RELATIVE to a genesis, and none of it says the genesis is the right one.

// A RepositoryIdentity names the repository a ledger governs.
//
// ⚠️ It is provider-neutral BY CONSTRUCTION: an authority plus a path, both
// readable by a person. A vendor's opaque numeric id is deliberately absent —
// see the deliberate-absence test — because an outsider replaying a bundle
// cannot resolve one without the vendor's API, which is precisely the dependency
// an outsider-verifiable bundle exists to remove.
//
// The trade is real and worth naming: a name survives a host migration and dies
// on a rename, where a vendor number does the opposite. The identity is compared
// as an opaque string and never resolved, so a rename is a fact the tape must
// record as an act rather than something an id absorbs silently.
type RepositoryIdentity struct {
	// Authority is the naming authority that assigns Path — a DNS-shaped name
	// such as "github.com". Compared, never dereferenced.
	Authority string `json:"authority"`
	// Path is the repository's path under that authority.
	Path string `json:"path"`
}

// Validate reports whether r is a usable repository name.
func (r RepositoryIdentity) Validate() error {
	var problems []string
	if strings.TrimSpace(r.Authority) == "" {
		problems = append(problems, "the naming authority is empty")
	}
	switch p := strings.TrimSpace(r.Path); {
	case p == "":
		problems = append(problems, "the repository path is empty")
	case strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/"):
		// ⚠️ A leading or trailing separator makes two spellings of one name,
		// and identity is compared as a string.
		problems = append(problems, "the repository path has a leading or trailing separator")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: repository identity: %s", ErrInvalidGenesis, strings.Join(problems, "; "))
}

// String renders r as authority/path.
func (r RepositoryIdentity) String() string { return r.Authority + "/" + r.Path }

// A RootKey is a key that is root from genesis.
//
// ⚠️ It carries NO validity interval. Its validity starts at
// [RepositoryGenesis.AcceptedAt] — see [RepositoryGenesis.KeyValidFrom] — and it
// ends when an act on the tape revokes it. Restating a start per key would let a
// key claim validity before the ledger it belongs to existed; carrying an end
// here would put the key registry's lifecycle inside the bootstrap, where no act
// can ever change it.
type RootKey struct {
	// KeyID names the key. It is unique within a genesis.
	KeyID string `json:"key_id"`
	// Algorithm names the signature algorithm, so a verifier never has to
	// infer one from a key's length.
	Algorithm string `json:"algorithm"`
	// PublicKey is the key material, base64, carried opaquely: this package
	// neither parses nor verifies it.
	PublicKey string `json:"public_key"`
}

// Validate reports whether k is a usable root key.
func (k RootKey) Validate() error {
	var problems []string
	if strings.TrimSpace(k.KeyID) == "" {
		problems = append(problems, "the key id is empty")
	}
	if strings.TrimSpace(k.Algorithm) == "" {
		problems = append(problems, "the algorithm is unnamed — a verifier would have to infer one")
	}
	if strings.TrimSpace(k.PublicKey) == "" {
		problems = append(problems, "the key material is empty")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: root key %q: %s", ErrInvalidGenesis, k.KeyID, strings.Join(problems, "; "))
}

// The two wildcards, and there are exactly two.
//
// ⚠️ Neither is the empty string, and that is the whole point. If the universe
// were spelled "", an omitted subject would grant everything — a missing value
// failing OPEN, in the one place in this design where that is least survivable.
// The universe must be typed out.
const (
	// SubjectAll is the only subject wildcard: every subject.
	SubjectAll = "**"
	// ActionAll is the only action wildcard: every action.
	//
	// It exists because the root grant must be able to say "everything" before
	// the action vocabulary is closed. A child may hold it only if its parent
	// does.
	ActionAll = "*"
)

// A Scope is what an authority covers: subjects, and the actions permitted on
// them.
//
// Subjects are PATH prefixes. Containment is segment-aware — "refs/heads"
// covers "refs/heads/main" and does NOT cover "refs/heads-of-state" — because a
// plain string prefix reads as narrower than it is, which is the direction that
// silently grants more than intended.
type Scope struct {
	Subjects []string `json:"subjects"`
	Actions  []string `json:"actions"`
}

// Validate reports whether s is a usable scope.
//
// ⚠️ An EMPTY list is refused rather than read as either "nothing" or
// "everything". Both readings are defensible, which is exactly why the value
// must be stated.
func (s Scope) Validate() error {
	var problems []string
	if len(s.Subjects) == 0 {
		problems = append(problems, "no subjects — an empty list is refused, not read as all or none")
	}
	if len(s.Actions) == 0 {
		problems = append(problems, "no actions — an empty list is refused, not read as all or none")
	}
	for _, subj := range s.Subjects {
		switch {
		case strings.TrimSpace(subj) == "":
			problems = append(problems, "a blank subject")
		case subj != SubjectAll && (strings.HasPrefix(subj, "/") || strings.HasSuffix(subj, "/")):
			problems = append(problems, fmt.Sprintf("subject %q has a leading or trailing separator", subj))
		}
	}
	for _, act := range s.Actions {
		if strings.TrimSpace(act) == "" {
			problems = append(problems, "a blank action")
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: scope: %s", ErrInvalidGenesis, strings.Join(problems, "; "))
}

// covers reports whether the subject prefix parent contains the subject child.
func coversSubject(parent, child string) bool {
	if parent == SubjectAll {
		return true
	}
	// ⚠️ There is deliberately NO explicit "only the universe covers the
	// universe" branch here — it would be unreachable, and an unreachable guard
	// reads as protection nobody is getting. The property it would assert is a
	// consequence of the rule below: SubjectAll contains no separator, so it is
	// not a path prefix of anything and no narrower parent can cover it. That
	// property is what is fragile, so it is pinned by a test rather than by a
	// branch that can never fire.
	return child == parent || strings.HasPrefix(child, parent+"/")
}

// A Grant is persistent, attenuating authority: a scope, and how long it lasts.
//
// ⚠️ This is the ORIGIN half of the authority model, not the whole of it. A
// single-use permit bound to one exact action is a separate type with its own
// mandatory expiry and its own single-spend accounting; this is the persistent
// authority such a permit is issued FROM, and [Grant.Attenuates] is the relation
// that issuance must satisfy. Building permits on any other relation would
// reintroduce the hole this one closes.
type Grant struct {
	// Scope is what the grant covers.
	Scope Scope `json:"scope"`
	// Unbounded declares that the grant has no expiry.
	//
	// ⚠️ It is a DECLARATION, not an inference from a zero NotAfter. An
	// unbounded authority is the strongest thing this type can express, and it
	// must never be what an unfilled struct means.
	Unbounded bool `json:"unbounded"`
	// NotAfter is the expiry, and must be zero when Unbounded is set.
	NotAfter AcceptedTime `json:"not_after"`
}

// Validate reports whether g is a usable grant.
func (g Grant) Validate() error {
	if err := g.Scope.Validate(); err != nil {
		return err
	}
	if g.Unbounded {
		if !g.NotAfter.At.IsZero() {
			return fmt.Errorf("%w: grant is declared unbounded and also carries an expiry", ErrInvalidGenesis)
		}
		return nil
	}
	if err := g.NotAfter.Validate(); err != nil {
		return fmt.Errorf("%w: grant expiry: %w", ErrInvalidGenesis, err)
	}
	return nil
}

// Attenuates reports whether g never exceeds parent — the non-amplification
// relation. A nil return means g is issuable from parent.
//
// ⚠️ It returns an ERROR rather than a bool because the REASON is the useful
// half, and because one of the answers is neither yes nor no: two expiries from
// different time authorities are not comparable at all. That case fails closed
// and says so, rather than picking a comparison and hiding the assumption.
func (g Grant) Attenuates(parent Grant) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if err := parent.Validate(); err != nil {
		return fmt.Errorf("parent grant: %w", err)
	}

	for _, child := range g.Scope.Subjects {
		covered := false
		for _, p := range parent.Scope.Subjects {
			if coversSubject(p, child) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("%w: subject %q is outside the parent's scope %v",
				ErrAmplification, child, parent.Scope.Subjects)
		}
	}

	parentAny := false
	for _, a := range parent.Scope.Actions {
		if a == ActionAll {
			parentAny = true
			break
		}
	}
	if !parentAny {
		for _, child := range g.Scope.Actions {
			found := false
			for _, p := range parent.Scope.Actions {
				if child == p {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: action %q is not held by the parent %v",
					ErrAmplification, child, parent.Scope.Actions)
			}
		}
	}

	switch {
	case parent.Unbounded:
		// Anything, bounded or not, is no later than never.
		return nil
	case g.Unbounded:
		return fmt.Errorf("%w: the child never expires while the parent expires at %s",
			ErrAmplification, parent.NotAfter.At)
	case g.NotAfter.Authority != parent.NotAfter.Authority:
		// ⚠️ Fail closed. Comparing two clocks is how a rolled-back one buys a
		// later expiry, and "I could not tell" must never read as "within".
		return fmt.Errorf("%w: expiries come from different time authorities (%q and %q) and are not comparable",
			ErrAmplification, g.NotAfter.Authority.Name, parent.NotAfter.Authority.Name)
	case !parent.NotAfter.NotBefore(g.NotAfter):
		return fmt.Errorf("%w: the child expires at %s, later than the parent's %s",
			ErrAmplification, g.NotAfter.At, parent.NotAfter.At)
	}
	return nil
}

// A RepositoryGenesis is the ledger bootstrap: the act every other act traces to.
type RepositoryGenesis struct {
	// SchemaVersion is [SchemaVersionNumber].
	//
	// ⚠️ A [Number] — an integer carried as its decimal string — for the reason
	// that type exists: this act's digest is its identity, and the canonical
	// form forbids JSON numbers.
	SchemaVersion Number `json:"schema_version"`
	// Repository is which repository this ledger governs.
	Repository RepositoryIdentity `json:"repository"`
	// RootKeys are the keys that are root from genesis.
	RootKeys []RootKey `json:"root_keys"`
	// CharterDigest binds the initial charter BY DIGEST.
	//
	// ⚠️ By digest, never inline. The charter's content is a versioned resource
	// in git and that copy is authoritative; a second copy here would disagree
	// with it exactly when someone was relying on one of them.
	CharterDigest string `json:"charter_digest"`
	// RootGrant is the authority everything else attenuates from, and the
	// ceiling nothing below may exceed.
	RootGrant Grant `json:"root_grant"`
	// AcceptedAt is when the genesis was accepted, from a named time authority.
	// It is also when every root key's validity begins.
	AcceptedAt AcceptedTime `json:"accepted_at"`
}

// Validate reports whether g is a well-formed genesis act.
func (g RepositoryGenesis) Validate() error {
	var problems []string
	if g.SchemaVersion == "" {
		problems = append(problems, "no schema version")
	}
	if err := g.Repository.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(g.RootKeys) == 0 {
		problems = append(problems, "no root keys — a ledger nothing can sign for cannot be extended")
	}
	seen := make(map[string]bool, len(g.RootKeys))
	for _, k := range g.RootKeys {
		if err := k.Validate(); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if seen[k.KeyID] {
			problems = append(problems, fmt.Sprintf("duplicate root key id %q — 'which key signed this' would have no answer", k.KeyID))
		}
		seen[k.KeyID] = true
	}
	if err := validDigest(g.CharterDigest); err != nil {
		problems = append(problems, "charter digest: "+err.Error())
	}
	if err := g.RootGrant.Validate(); err != nil {
		problems = append(problems, "root grant: "+err.Error())
	}
	if err := g.AcceptedAt.Validate(); err != nil {
		problems = append(problems, "accepted time: "+err.Error())
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidGenesis, strings.Join(problems, "; "))
}

// KeyValidFrom is when every root key's validity begins: the genesis time.
//
// It is a method rather than a field on [RootKey] so there is ONE answer that
// cannot drift from the act it belongs to.
func (g RepositoryGenesis) KeyValidFrom() AcceptedTime { return g.AcceptedAt }

// ID is the genesis act's identity: the domain-separated digest of its canonical
// bytes.
//
// ⚠️ This is the value a verifier's trust anchor names. Two genesis acts
// differing in any field have different ids, which is what makes an alternate
// genesis detectable at all — see the trust-anchor rule in the verify package.
func (g RepositoryGenesis) ID() (string, error) {
	return canonical.Digest(canonical.DomainGenesis, g)
}

// Permits reports whether child may be issued under this genesis — the entry
// point to the non-amplification chain, whose origin is the root grant.
func (g RepositoryGenesis) Permits(child Grant) error { return child.Attenuates(g.RootGrant) }

// validDigest checks the algorithm-tagged digest form this package binds by.
func validDigest(d string) error {
	prefix := canonical.HashName + ":"
	if !strings.HasPrefix(d, prefix) {
		return fmt.Errorf("%q does not begin with %q — a bare hash cannot say which algorithm produced it", d, prefix)
	}
	hex := strings.TrimPrefix(d, prefix)
	// sha-256 is 32 bytes, so 64 hex characters. A short digest is a truncated
	// one, and a truncated digest is a weaker claim wearing a full one's shape.
	if len(hex) != 64 {
		return fmt.Errorf("%q has %d hex characters, want 64", d, len(hex))
	}
	for _, r := range hex {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return fmt.Errorf("%q is not lowercase hexadecimal", d)
		}
	}
	return nil
}
