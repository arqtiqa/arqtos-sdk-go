package contracts_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
)

const testCharterDigest = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func genesisTime() contracts.AcceptedTime {
	return contracts.AcceptedTime{
		At: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		Authority: contracts.TimeAuthority{
			Name:       "authority:genesis",
			Provenance: contracts.ClockAttested,
		},
	}
}

func rootGrant() contracts.Grant {
	return contracts.Grant{
		Scope: contracts.Scope{
			Subjects: []string{contracts.SubjectAll},
			Actions:  []string{contracts.ActionAll},
		},
		Unbounded: true,
	}
}

func goodGenesis() contracts.RepositoryGenesis {
	return contracts.RepositoryGenesis{
		SchemaVersion: contracts.GenesisSchemaVersion,
		Repository:    contracts.RepositoryIdentity{Authority: "github.com", Path: "arqtiqa/arqtos-core"},
		RootKeys: []contracts.RootKey{
			{KeyID: "root-1", Algorithm: "ed25519", PublicKey: "TFVSZWFsUHVibGljS2V5Qnl0ZXM="},
		},
		CharterDigest: testCharterDigest,
		RootGrant:     rootGrant(),
		AcceptedAt:    genesisTime(),
	}
}

// ---------------------------------------------------------------------------
// The act's shape
// ---------------------------------------------------------------------------

func TestRepositoryGenesis_AcceptsAWellFormedAct(t *testing.T) {
	if err := goodGenesis().Validate(); err != nil {
		t.Fatalf("Validate rejected a well-formed genesis: %v", err)
	}
}

// Every field the ledger bootstrap carries is REQUIRED. A genesis missing any
// one of them is not a smaller claim about the repository — it is a chain whose
// first link does not say what it authorises.
func TestRepositoryGenesis_RefusesAnyMissingField(t *testing.T) {
	cases := map[string]func(*contracts.RepositoryGenesis){
		"no repository identity": func(g *contracts.RepositoryGenesis) { g.Repository = contracts.RepositoryIdentity{} },
		"no root keys":           func(g *contracts.RepositoryGenesis) { g.RootKeys = nil },
		"no charter digest":      func(g *contracts.RepositoryGenesis) { g.CharterDigest = "" },
		"no root grant":          func(g *contracts.RepositoryGenesis) { g.RootGrant = contracts.Grant{} },
		"no accepted time":       func(g *contracts.RepositoryGenesis) { g.AcceptedAt = contracts.AcceptedTime{} },
		"no schema version":      func(g *contracts.RepositoryGenesis) { g.SchemaVersion = "" },
	}
	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			g := goodGenesis()
			break_(&g)
			err := g.Validate()
			if err == nil {
				t.Fatal("Validate accepted a genesis with a missing field")
			}
			if !errors.Is(err, contracts.ErrInvalidGenesis) {
				t.Errorf("error %v does not wrap ErrInvalidGenesis", err)
			}
		})
	}
	if len(cases) != 6 {
		t.Fatalf("expected 6 required-field cases, ran %d — a dropped case reads as coverage", len(cases))
	}
}

// ⚠️ The charter is bound BY DIGEST and never inlined. The charter content is a
// versioned resource in git; carrying a copy here would be a second source of
// truth, and the two would disagree exactly when it mattered.
func TestRepositoryGenesis_RefusesAMalformedCharterDigest(t *testing.T) {
	for name, digest := range map[string]string{
		"no algorithm prefix": strings.TrimPrefix(testCharterDigest, "sha256:"),
		"wrong algorithm":     "md5:0123456789abcdef0123456789abcdef",
		"short":               "sha256:0123",
		"not hex":             "sha256:zzzz456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		t.Run(name, func(t *testing.T) {
			g := goodGenesis()
			g.CharterDigest = digest
			if err := g.Validate(); !errors.Is(err, contracts.ErrInvalidGenesis) {
				t.Fatalf("Validate accepted charter digest %q: %v", digest, err)
			}
		})
	}
}

// ⚠️ The genesis act's identity is a DOMAIN-SEPARATED digest of its canonical
// bytes, so a genesis can never be reinterpreted as some other kind of record
// that happens to encode identically.
func TestRepositoryGenesis_IDIsStableAndFieldSensitive(t *testing.T) {
	g := goodGenesis()
	id, err := g.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if !strings.HasPrefix(id, "sha256:") {
		t.Errorf("ID %q does not name its hash algorithm", id)
	}
	again, err := g.ID()
	if err != nil || again != id {
		t.Fatalf("ID is not stable across calls: %q then %q (%v)", id, again, err)
	}

	// Every field must reach the digest. A field the identity ignores is a
	// field an attacker may change under a signature.
	changes := map[string]func(*contracts.RepositoryGenesis){
		"repository": func(x *contracts.RepositoryGenesis) { x.Repository.Path = "arqtiqa/other" },
		"root key":   func(x *contracts.RepositoryGenesis) { x.RootKeys[0].KeyID = "root-2" },
		"charter": func(x *contracts.RepositoryGenesis) {
			x.CharterDigest = strings.Replace(testCharterDigest, "0123", "3210", 1)
		},
		"root grant": func(x *contracts.RepositoryGenesis) { x.RootGrant.Scope.Subjects = []string{"refs/heads/main"} },
		"time":       func(x *contracts.RepositoryGenesis) { x.AcceptedAt.At = x.AcceptedAt.At.Add(time.Second) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			other := goodGenesis()
			change(&other)
			otherID, err := other.ID()
			if err != nil {
				t.Fatalf("ID: %v", err)
			}
			if otherID == id {
				t.Errorf("changing the %s did not change the genesis id — the field is outside the identity", name)
			}
		})
	}
}

// ⚠️ THE DELIBERATE ABSENCES. Each of these fields is one somebody will
// reasonably want to add, and each would break something structural. Checked by
// reflection so the refusal survives a refactor rather than living in a comment.
func TestGenesisTypes_DoNotCarryTheFieldsThatWouldBreakThem(t *testing.T) {
	forbidden := []struct {
		typ    any
		field  string
		reason string
	}{
		{contracts.RepositoryIdentity{}, "VendorID",
			"a vendor's opaque number is not an identity an outsider can read without the vendor's API"},
		{contracts.RepositoryIdentity{}, "NodeID", "same: a vendor-internal handle, not a provider-neutral name"},
		{contracts.RootKey{}, "ValidFrom",
			"a root key's validity starts at genesis, from RepositoryGenesis.AcceptedAt — a per-key start could claim validity BEFORE the ledger existed"},
		{contracts.RootKey{}, "ValidUntil",
			"revocation and rotation are ordinary acts on the tape, not a field on the bootstrap"},
		{contracts.RepositoryGenesis{}, "Charter",
			"the charter is bound by digest; an inline copy would be a second source of truth"},
	}
	for _, f := range forbidden {
		typ := reflect.TypeOf(f.typ)
		if _, ok := typ.FieldByName(f.field); ok {
			t.Errorf("%s declares %s — %s", typ.Name(), f.field, f.reason)
		}
	}
	if len(forbidden) != 5 {
		t.Fatalf("expected 5 forbidden fields, checked %d", len(forbidden))
	}
}

// The root keys' validity begins at genesis, and it is READ from the act rather
// than restated per key — so there is one answer, not N that can disagree.
func TestRepositoryGenesis_RootKeysAreValidFromGenesis(t *testing.T) {
	g := goodGenesis()
	if got := g.KeyValidFrom(); got != g.AcceptedAt {
		t.Errorf("KeyValidFrom() = %v, want the genesis accepted time %v", got, g.AcceptedAt)
	}
}

func TestRepositoryIdentity_RefusesAnIncompleteName(t *testing.T) {
	for name, id := range map[string]contracts.RepositoryIdentity{
		"no authority":       {Authority: "", Path: "arqtiqa/arqtos-core"},
		"no path":            {Authority: "github.com", Path: ""},
		"whitespace path":    {Authority: "github.com", Path: "   "},
		"trailing separator": {Authority: "github.com", Path: "arqtiqa/arqtos-core/"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := id.Validate(); err == nil {
				t.Fatalf("Validate accepted %#v", id)
			}
		})
	}
}

func TestRootKey_RefusesAnIncompleteKey(t *testing.T) {
	good := goodGenesis().RootKeys[0]
	for name, mutate := range map[string]func(*contracts.RootKey){
		"no key id":    func(k *contracts.RootKey) { k.KeyID = "" },
		"no algorithm": func(k *contracts.RootKey) { k.Algorithm = "" },
		"no material":  func(k *contracts.RootKey) { k.PublicKey = "" },
	} {
		t.Run(name, func(t *testing.T) {
			k := good
			mutate(&k)
			if err := k.Validate(); err == nil {
				t.Fatalf("Validate accepted %#v", k)
			}
		})
	}
}

// ⚠️ Two root keys sharing a key id would make "which key signed this"
// unanswerable at exactly the moment it is asked.
func TestRepositoryGenesis_RefusesDuplicateRootKeyIDs(t *testing.T) {
	g := goodGenesis()
	g.RootKeys = append(g.RootKeys, g.RootKeys[0])
	if err := g.Validate(); !errors.Is(err, contracts.ErrInvalidGenesis) {
		t.Fatalf("Validate accepted two root keys with the same id: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Non-amplification — the root grant is the ceiling
// ---------------------------------------------------------------------------

func boundedGrant(subjects, actions []string, at time.Time) contracts.Grant {
	return contracts.Grant{
		Scope:    contracts.Scope{Subjects: subjects, Actions: actions},
		NotAfter: contracts.AcceptedTime{At: at, Authority: genesisTime().Authority},
	}
}

func TestGrant_AttenuationNarrowsAndNeverWidens(t *testing.T) {
	parent := boundedGrant([]string{"refs/heads"}, []string{"merge", "publish"},
		time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))

	allowed := map[string]contracts.Grant{
		"identical": parent,
		"narrower subject": boundedGrant([]string{"refs/heads/main"}, []string{"merge"},
			time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		"fewer actions": boundedGrant([]string{"refs/heads"}, []string{"merge"},
			time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)),
		"earlier expiry": boundedGrant([]string{"refs/heads"}, []string{"merge", "publish"},
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
	}
	for name, child := range allowed {
		t.Run("allowed/"+name, func(t *testing.T) {
			if err := child.Attenuates(parent); err != nil {
				t.Errorf("a narrower grant was refused: %v", err)
			}
		})
	}

	refused := map[string]contracts.Grant{
		"wider subject": boundedGrant([]string{"refs"}, []string{"merge"},
			time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		"sibling subject": boundedGrant([]string{"refs/tags/v1"}, []string{"merge"},
			time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		"subject wildcard": boundedGrant([]string{contracts.SubjectAll}, []string{"merge"},
			time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		"extra action": boundedGrant([]string{"refs/heads/main"}, []string{"merge", "revoke"},
			time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		"action wildcard": boundedGrant([]string{"refs/heads/main"}, []string{contracts.ActionAll},
			time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		"later expiry": boundedGrant([]string{"refs/heads/main"}, []string{"merge"},
			time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)),
	}
	for name, child := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			err := child.Attenuates(parent)
			if err == nil {
				t.Fatalf("a grant that EXCEEDS its parent was allowed: %#v", child.Scope)
			}
			if !errors.Is(err, contracts.ErrAmplification) {
				t.Errorf("error %v does not wrap ErrAmplification", err)
			}
		})
	}
}

// ⚠️ The universe is not coverable by a narrower parent, and the parent chosen
// here is a STRING prefix of it ("*" against "**"). That is the shape a naive
// prefix rule waves through, and it is why the covering rule has no explicit
// wildcard branch — the property is pinned here instead.
func TestGrant_TheUniverseIsNeverCoveredByANarrowerParent(t *testing.T) {
	parent := boundedGrant([]string{"*"}, []string{"merge"}, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	child := boundedGrant([]string{contracts.SubjectAll}, []string{"merge"}, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := child.Attenuates(parent); !errors.Is(err, contracts.ErrAmplification) {
		t.Fatalf("a parent covering %q was read as covering every subject: %v", "*", err)
	}
}

// ⚠️ A prefix is a PATH prefix, not a string prefix. "refs/heads" must not cover
// "refs/heads-of-state": that is the classic way a scope check reads as narrower
// than it is.
func TestGrant_SubjectPrefixIsSegmentAware(t *testing.T) {
	parent := boundedGrant([]string{"refs/heads"}, []string{"merge"}, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	child := boundedGrant([]string{"refs/heads-of-state"}, []string{"merge"}, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := child.Attenuates(parent); err == nil {
		t.Fatal("a string prefix was treated as a path prefix — refs/heads must not cover refs/heads-of-state")
	}
}

// ⚠️ An unbounded child of a bounded parent is amplification in the one
// dimension that is easiest to miss, because the scope looks identical.
func TestGrant_UnboundedExpiryOnlyDescendsFromUnbounded(t *testing.T) {
	bounded := boundedGrant([]string{"refs/heads"}, []string{"merge"}, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	unbounded := contracts.Grant{Scope: bounded.Scope, Unbounded: true}

	err := unbounded.Attenuates(bounded)
	if !errors.Is(err, contracts.ErrAmplification) {
		t.Errorf("an unbounded child of a bounded parent was allowed: %v", err)
	}
	// ⚠️ The REASON is asserted, not just the sentinel. An unbounded child
	// carries a zero expiry, whose authority differs from any real parent's — so
	// the incomparable-authorities refusal catches it too, wrapping the same
	// error. Checking only the sentinel let a mutation that deleted this rule
	// pass on the neighbouring one.
	if !strings.Contains(err.Error(), "never expires") {
		t.Errorf("error %q does not name the unbounded child; it may be the incomparable-authorities "+
			"refusal firing instead, which would leave this rule untested", err)
	}
	if err := bounded.Attenuates(unbounded); err != nil {
		t.Errorf("a bounded child of an unbounded parent was refused: %v", err)
	}
	if err := unbounded.Attenuates(unbounded); err != nil {
		t.Errorf("an unbounded child of an unbounded parent was refused: %v", err)
	}
}

// ⚠️ Two expiries from DIFFERENT time authorities are not comparable, and
// comparing them anyway is how a rolled-back clock buys a later expiry. The
// refusal is fail-closed: incomparable reads as "does not attenuate".
func TestGrant_RefusesToCompareExpiriesFromDifferentAuthorities(t *testing.T) {
	parent := boundedGrant([]string{"refs/heads"}, []string{"merge"}, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	child := parent
	child.NotAfter.At = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	child.NotAfter.Authority = contracts.TimeAuthority{Name: "authority:other", Provenance: contracts.ClockLocal}

	err := child.Attenuates(parent)
	if err == nil {
		t.Fatal("compared expiries across two different time authorities")
	}
	if !strings.Contains(err.Error(), "authorit") {
		t.Errorf("error %q does not name the incomparable authorities", err)
	}
}

// The root grant is the ORIGIN of the chain: nothing issued anywhere below it
// may exceed it. This is the same relation, entered from the genesis act.
func TestRepositoryGenesis_PermitsOnlyWhatTheRootGrantCovers(t *testing.T) {
	g := goodGenesis()

	within := boundedGrant([]string{"refs/heads/main"}, []string{"merge"}, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := g.Permits(within); err != nil {
		t.Errorf("the root grant refused an authority inside it: %v", err)
	}

	// The root grant here is the universe, so the only way to exceed it is to
	// be malformed — which must still be refused rather than waved through.
	if err := g.Permits(contracts.Grant{}); err == nil {
		t.Error("the root grant permitted an unvalidatable authority")
	}

	// A NARROWER root grant must actually bind.
	narrow := goodGenesis()
	narrow.RootGrant = boundedGrant([]string{"refs/heads/main"}, []string{"merge"}, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	if err := narrow.Permits(within); !errors.Is(err, contracts.ErrAmplification) {
		t.Errorf("a narrower root grant permitted an authority beyond it: %v", err)
	}
}

func TestScope_RefusesAnEmptyOrBlankVocabulary(t *testing.T) {
	for name, s := range map[string]contracts.Scope{
		"no subjects":     {Subjects: nil, Actions: []string{"merge"}},
		"no actions":      {Subjects: []string{"refs/heads"}, Actions: nil},
		"blank subject":   {Subjects: []string{""}, Actions: []string{"merge"}},
		"blank action":    {Subjects: []string{"refs/heads"}, Actions: []string{" "}},
		"absolute prefix": {Subjects: []string{"/refs/heads"}, Actions: []string{"merge"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.Validate(); err == nil {
				t.Fatalf("Validate accepted %#v", s)
			}
		})
	}
}

// ⚠️ The empty string must NEVER mean "everything". A missing value that grants
// the universe is the wrong failure direction, so the universe has an explicit
// marker and the empty subject is refused outright (the test above).
func TestScope_TheUniverseIsExplicit(t *testing.T) {
	if contracts.SubjectAll == "" {
		t.Fatal("SubjectAll is the empty string — an omitted subject would grant everything")
	}
	if contracts.ActionAll == "" {
		t.Fatal("ActionAll is the empty string — an omitted action would grant everything")
	}
}

func TestRepositoryIdentity_StringIsTheComparedForm(t *testing.T) {
	id := contracts.RepositoryIdentity{Authority: "github.com", Path: "arqtiqa/arqtos-core"}
	if got, want := id.String(), "github.com/arqtiqa/arqtos-core"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// ⚠️ "Unbounded" and an expiry are contradictory, and the contradiction must be
// refused rather than resolved. Either resolution is a guess about which half
// the author meant, in a field that decides how long authority lasts.
func TestGrant_RefusesAContradictoryOrUnusableExpiry(t *testing.T) {
	scope := contracts.Scope{Subjects: []string{"refs/heads"}, Actions: []string{"merge"}}
	at := contracts.AcceptedTime{At: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Authority: genesisTime().Authority}

	for name, g := range map[string]contracts.Grant{
		"unbounded and expiring": {Scope: scope, Unbounded: true, NotAfter: at},
		"bounded with no expiry": {Scope: scope},
		"bounded with an unattributable expiry": {
			Scope:    scope,
			NotAfter: contracts.AcceptedTime{At: at.At},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := g.Validate(); !errors.Is(err, contracts.ErrInvalidGenesis) {
				t.Fatalf("Validate accepted %#v: %v", g, err)
			}
		})
	}

	good := contracts.Grant{Scope: scope, NotAfter: at}
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate rejected a well-formed bounded grant: %v", err)
	}
}

// A malformed root key inside an otherwise-valid genesis must be reported, not
// skipped over on the way to the duplicate-id check.
func TestRepositoryGenesis_ReportsAMalformedRootKey(t *testing.T) {
	g := goodGenesis()
	g.RootKeys = append(g.RootKeys, contracts.RootKey{KeyID: "root-2"})
	err := g.Validate()
	if !errors.Is(err, contracts.ErrInvalidGenesis) {
		t.Fatalf("Validate accepted a genesis carrying an unusable root key: %v", err)
	}
	if !strings.Contains(err.Error(), "root-2") {
		t.Errorf("error %q does not name the offending key", err)
	}
}
