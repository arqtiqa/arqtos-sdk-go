package codeci_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codeci"
)

func TestKnownCapabilities_CarriesCheckPublish(t *testing.T) {
	got := codeci.KnownCapabilities()
	if !slices.Contains(got, codeci.CapCheckPublish) {
		t.Fatalf("KnownCapabilities() = %v; want it to contain %q, or a manifest declaring it is "+
			"rejected as an unknown capability", got, codeci.CapCheckPublish)
	}
	if want := 2; len(got) != want {
		t.Errorf("KnownCapabilities() has %d entries, want %d — the count is asserted so a capability "+
			"cannot be silently dropped while this test still passes on the one it looks for", len(got), want)
	}
}

// ⚠️ CapCheckPublish and CapCIControl must stay DISTINCT capabilities. On the
// host this contract is written against, an app installation can publish its own
// check without being able to re-run a workflow it did not create, and a token
// can do the reverse. A host that read one and assumed the other would act on a
// permission it does not hold.
func TestCapabilities_CheckPublishIsNotCIControl(t *testing.T) {
	if codeci.CapCheckPublish == codeci.CapCIControl {
		t.Fatal("CapCheckPublish and CapCIControl are the same value; a host checking one would " +
			"believe it holds the other")
	}
}

func TestCheckPublication_Validate(t *testing.T) {
	valid := codeci.CheckPublication{
		Name:       "arqtos/gate",
		HeadSHA:    "0f7c3a91d2b4e5f60718293a4b5c6d7e8f901234",
		Status:     codeci.RunStatusSuccess,
		ExternalID: "act:01a01578-ec47-7209-9200-cac2a1f75c7f",
	}

	tests := []struct {
		name     string
		mutate   func(codeci.CheckPublication) codeci.CheckPublication
		wantErr  bool
		wantSaid []string
	}{
		{"complete publication", func(p codeci.CheckPublication) codeci.CheckPublication { return p }, false, nil},
		{
			"no name", func(p codeci.CheckPublication) codeci.CheckPublication { p.Name = ""; return p },
			true, []string{"name"},
		},
		{
			"no head sha", func(p codeci.CheckPublication) codeci.CheckPublication { p.HeadSHA = ""; return p },
			true, []string{"head_sha"},
		},
		{
			// ⚠️ Not a missing address — a missing IDEMPOTENCY KEY. PublishCheck
			// MUST be idempotent on this id, so an empty one does not make the
			// call less precise, it makes that MUST unsatisfiable: a retry
			// after an ambiguous first call has nothing to match on and creates
			// a second check. Refused at the boundary rather than discovered on
			// the first retry.
			"no external id", func(p codeci.CheckPublication) codeci.CheckPublication {
				p.ExternalID = ""
				return p
			},
			true, []string{"external_id"},
		},
		{
			// The zero value. A publisher that forgot to set a status would
			// otherwise publish "unclassified" against a commit, and a required
			// check in a state no rule recognises blocks the change request
			// forever with nothing in the log to say why.
			"unspecified status", func(p codeci.CheckPublication) codeci.CheckPublication {
				p.Status = codeci.RunStatusUnspecified
				return p
			},
			true, []string{"unspecified"},
		},
		{
			"status outside the vocabulary", func(p codeci.CheckPublication) codeci.CheckPublication {
				p.Status = codeci.RunStatus(99)
				return p
			},
			true, []string{"closed vocabulary"},
		},
		{
			// Every problem at once, so a caller fixing a publication does not
			// learn the same thing one round trip per field.
			"nothing set at all", func(codeci.CheckPublication) codeci.CheckPublication {
				return codeci.CheckPublication{}
			},
			true, []string{"name", "head_sha", "external_id", "unspecified"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mutate(valid).Validate()
			if tt.wantErr && err == nil {
				t.Fatal("Validate accepted a publication that is not publishable")
			}
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Validate rejected a complete publication: %v", err)
				}
				return
			}
			if got := cerr.KindOf(err); got != cerr.KindInvalid {
				t.Errorf("kind = %v, want %v — a caller branches on the kind, never on the message", got, cerr.KindInvalid)
			}
			for _, want := range tt.wantSaid {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q, so the operator cannot see what to fix", err, want)
				}
			}
		})
	}
}

// A pending publication is legitimate — the gate publishes "running" before it
// has decided — so validation must not require a terminal status.
func TestCheckPublication_AcceptsANonTerminalStatus(t *testing.T) {
	for _, s := range []codeci.RunStatus{codeci.RunStatusPending, codeci.RunStatusRunning} {
		p := codeci.CheckPublication{Name: "arqtos/gate", HeadSHA: "0f7c3a9", Status: s, ExternalID: "act:1"}
		if err := p.Validate(); err != nil {
			t.Errorf("Validate rejected status %s: %v — a gate publishes in-progress before it decides", s, err)
		}
	}
}

// ⚠️ The rule this tier exists to obey: a NEW interface, never a method added to
// an existing one — including to the other optional tier. Adding PublishCheck to
// CIController would break every existing implementer at compile time in THEIR
// repository, after release, where this module never sees it.
func TestCheckPublisher_IsNotAMethodOnAnyExistingInterface(t *testing.T) {
	for _, iface := range []struct {
		name string
		typ  reflect.Type
	}{
		{"CodeCI", reflect.TypeOf((*codeci.CodeCI)(nil)).Elem()},
		{"CIController", reflect.TypeOf((*codeci.CIController)(nil)).Elem()},
	} {
		if _, found := iface.typ.MethodByName("PublishCheck"); found {
			t.Errorf("%s declares PublishCheck: the operation was added to an existing interface "+
				"instead of landing behind its own optional tier", iface.name)
		}
	}

	tier := reflect.TypeOf((*codeci.CheckPublisher)(nil)).Elem()
	if _, found := tier.MethodByName("PublishCheck"); !found {
		t.Fatal("CheckPublisher does not declare PublishCheck")
	}
	if got := tier.NumMethod(); got != 1 {
		t.Errorf("CheckPublisher declares %d methods, want 1", got)
	}
}

// The validation failure must be classifiable through errors.Is/As the same way
// every other refusal in this class is, or a caller has to string-match it.
func TestCheckPublication_ValidateIsAClassifiedFailure(t *testing.T) {
	err := codeci.CheckPublication{}.Validate()
	if err == nil {
		t.Fatal("an empty publication validated")
	}
	var ce *cerr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %T is not a *cerr.Error; a caller would have to read the message to classify it", err)
	}
}
