package tracker

import (
	"strings"
	"testing"
	"time"
)

// These cover the three dimensions arqtiqa/arqtos-sdk-go#65 adds beside the field
// predicates #61 shipped: [Filter.State], [Filter.Types] and
// [Filter.ChangedAtOrAfter].
//
// ⚠️ The dimension most worth testing is [Filter.Empty], and the reason is not
// obvious. Every one of these members can be added to a Filter WITHOUT touching
// Predicates, so a connector that decided "nothing to do" from `len(Predicates) == 0`
// would answer the whole board for a caller who asked for open Stories — a SUPERSET,
// which is the one failure a caller cannot detect. `githubprojects` really does gate
// on Empty(): its ScanWhere skips the catalogue read entirely when the filter is
// empty. So Empty() is load-bearing, not a convenience.

// dimensionCatalogue publishes a type vocabulary as well as fields, because
// [Filter.CheckAgainst] validates [Filter.Types] against [Catalogue.Types] the same
// way it validates a value against a single-select's Options.
func dimensionCatalogue() Catalogue {
	return Catalogue{
		Fields: []Field{
			{Name: "Status", Class: FieldClassBoard, Accepts: ValueOption, Options: []string{"Backlog", "Shipped"}},
		},
		Types: []string{"Story", "Bug", "Epic"},
	}
}

// TestFilter_EmptyAccountsForEveryDimension is the guard against a dimension being
// added to Filter and silently ignored.
//
// ⚠️ Each row carries EXACTLY ONE dimension, so a wrong Empty() cannot be masked by
// another member also being set. A row combining two would pass even if Empty()
// consulted only one of them.
func TestFilter_EmptyAccountsForEveryDimension(t *testing.T) {
	if !(Filter{}).Empty() {
		t.Error("the zero Filter is not Empty, so a caller asking for nothing would be told it asked for something")
	}
	cases := []struct {
		dimension string
		filter    Filter
		harm      string
	}{{
		dimension: "Predicates",
		filter:    Filter{Predicates: []Predicate{{Field: "Status", Match: MatchIs, Values: []string{"Shipped"}}}},
		harm:      "a field predicate",
	}, {
		dimension: "State",
		filter:    Filter{State: ItemStateOpen},
		harm:      "a caller asking for OPEN items would be answered with the whole board, closed ones included",
	}, {
		dimension: "Types",
		filter:    Filter{Types: []string{"Story"}},
		harm:      "a caller asking for Stories would be answered with Epics and Bugs too",
	}, {
		dimension: "ChangedAtOrAfter",
		filter:    Filter{ChangedAtOrAfter: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		harm:      "a caller reconciling against a watermark would re-read the entire board every time",
	}}
	for _, c := range cases {
		t.Run(c.dimension, func(t *testing.T) {
			if c.filter.Empty() {
				t.Errorf("a Filter carrying only %s reports Empty, so a connector gating on Empty() would send "+
					"no filter at all: %s", c.dimension, c.harm)
			}
		})
	}
}

func TestFilter_StringRendersEveryDimension(t *testing.T) {
	if got, want := (Filter{}).String(), "no filter"; got != want {
		t.Errorf("the zero Filter renders %q, want %q", got, want)
	}
	full := Filter{
		Predicates:       []Predicate{{Field: "Status", Match: MatchIs, Values: []string{"Shipped"}}},
		State:            ItemStateOpen,
		Types:            []string{"Story", "Bug"},
		ChangedAtOrAfter: time.Date(2026, 8, 1, 12, 30, 0, 0, time.UTC),
	}
	got := full.String()
	// ⚠️ Every dimension must appear. A refusal that named only some of what was
	// asked would send an operator to fix the wrong half of their filter.
	for _, want := range []string{"Status is Shipped", "is open", "type is Story|Bug", "2026-08-01T12:30:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("Filter.String() = %q, missing %q", got, want)
		}
	}
	if n := strings.Count(got, " AND "); n != 3 {
		t.Errorf("Filter.String() joined %d times with AND, want 3 — the filter is a CONJUNCTION of four things "+
			"and rendering it as fewer understates what it asks: %q", n, got)
	}
}

// TestItemState_EveryValueRendersAndTheZeroIsNamed mirrors
// [TestMatch_EveryValueRendersAndTheZeroIsNamed]: a value outside the closed set must
// render as what it IS rather than as any real state, so a refusal built from it names
// what was actually passed.
func TestItemState_EveryValueRendersAndTheZeroIsNamed(t *testing.T) {
	for state, want := range map[ItemState]string{
		ItemStateAny: "any state", ItemStateOpen: "open", ItemStateClosed: "closed",
	} {
		if got := state.String(); got != want {
			t.Errorf("ItemState(%d).String() = %q, want %q", int(state), got, want)
		}
	}
	outside := ItemState(99)
	if got := outside.String(); !strings.Contains(got, "99") {
		t.Errorf("a state outside the closed set renders %q and must name the value it carries, or a refusal "+
			"about it says nothing an operator can act on", got)
	}
}

func TestFilter_CheckAgainstRefusesAStateOutsideTheClosedSet(t *testing.T) {
	err := Filter{State: ItemState(99)}.CheckAgainst(dimensionCatalogue())
	if err == nil {
		t.Fatal("a state outside the closed set was accepted; a connector cannot render it, and defaulting it " +
			"to any/open/closed would answer a question nobody asked")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("the refusal does not name the value that was passed: %v", err)
	}
}

// TestFilter_CheckAgainstRefusesATypeTheBoardDoesNotPublish holds the type half of the
// obligation, for arqtiqa/arqtos-connectors#168's measured reason: a backend answers a
// type it does not recognise with an EMPTY SET and no error, which a caller cannot
// tell from "no items of that type".
func TestFilter_CheckAgainstRefusesATypeTheBoardDoesNotPublish(t *testing.T) {
	cat := dimensionCatalogue()
	cases := []struct {
		name  string
		types []string
		want  string
	}{
		{"a type the board does not serve", []string{"Fastrack"}, `"Fastrack"`},
		{"one bad type among good ones", []string{"Story", "Fastrack"}, `"Fastrack"`},
		{"an empty type", []string{""}, "empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Filter{Types: c.types}.CheckAgainst(cat)
			if err == nil {
				t.Fatalf("Types %v was accepted against a board serving %v", c.types, cat.Types)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal does not name %s: %v", c.want, err)
			}
		})
	}
	// ⚠️ And the control: a published type must NOT be refused, or the check above
	// would pass on a CheckAgainst that refused everything.
	if err := (Filter{Types: []string{"Story", "Bug"}}).CheckAgainst(cat); err != nil {
		t.Errorf("types this board publishes were refused, so the validation refuses everything rather than "+
			"the unpublished: %v", err)
	}
}

// TestFilter_CheckAgainstDoesNotRefuseATimestamp pins the stated LIMIT of this
// obligation — arqtiqa/arqtos-sdk-go#65's criterion that CheckAgainst says plainly
// what it cannot validate.
//
// ⚠️ A catalogue publishes fields, types and vocabularies. It says nothing about time,
// so there is no board fact a timestamp could be checked against and inventing one
// would refuse coherent questions. Even a FUTURE instant is coherent: it correctly
// admits nothing. What refuses a temporal bound is the CONNECTOR, with
// cerr.KindUnsupported, when its backend cannot express one.
func TestFilter_CheckAgainstDoesNotRefuseATimestamp(t *testing.T) {
	cat := dimensionCatalogue()
	for _, c := range []struct {
		name string
		at   time.Time
	}{
		{"a past instant", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"a future instant", time.Now().Add(48 * time.Hour)},
		{"a non-UTC instant", time.Date(2026, 8, 1, 9, 0, 0, 0, time.FixedZone("CEST", 2*60*60))},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := (Filter{ChangedAtOrAfter: c.at}).CheckAgainst(cat); err != nil {
				t.Errorf("CheckAgainst refused %s, and it has no board fact to refuse it against: %v", c.name, err)
			}
		})
	}
}
