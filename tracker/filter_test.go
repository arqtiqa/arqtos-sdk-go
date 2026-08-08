package tracker

import (
	"strings"
	"testing"
)

// filterCatalogue is a board serving one single-select with a vocabulary and one
// text field with none — the two shapes CheckAgainst treats differently.
func filterCatalogue() Catalogue {
	return Catalogue{Fields: []Field{
		{Name: "Status", Class: FieldClassBoard, Accepts: ValueOption, Options: []string{"Backlog", "Shipped"}},
		{Name: "Notes", Class: FieldClassBoard, Accepts: ValueText},
	}}
}

// TestFilter_CheckAgainstRefusesWhatTheBoardCannotAnswer is the contract
// obligation arqtiqa/arqtos-sdk-go#61 publishes, and it is driven per refusal
// rather than as "an error came back" — the four refusals send an operator to
// four different fixes.
//
// ⚠️ The hazard it exists for was MEASURED, not imagined:
// arqtiqa/arqtos-connectors#168 found GitHub answers `nosuchfield:x` with
// totalCount=0 and NO errors. A mistyped predicate is indistinguishable from "no
// items match", so a connector that passed one through trades a slow correct read
// for a fast wrong one.
//
// FALSIFIER, verified red per row: delete the branch the row names and the filter
// is accepted.
func TestFilter_CheckAgainstRefusesWhatTheBoardCannotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter Filter
		names  []string
	}{
		{
			name:   "a field the catalogue does not carry",
			filter: Filter{Predicates: []Predicate{{Field: "Nonesuch", Match: MatchIs, Values: []string{"x"}}}},
			names:  []string{"Nonesuch", `"Status"`, "EMPTY SET"},
		},
		{
			name:   "a comparison nobody chose",
			filter: Filter{Predicates: []Predicate{{Field: "Status", Values: []string{"Shipped"}}}},
			names:  []string{"Status", "unspecified", "never defaulted"},
		},
		{
			name:   "is-unset carrying values it would ignore",
			filter: Filter{Predicates: []Predicate{{Field: "Status", Match: MatchIsUnset, Values: []string{"Shipped"}}}},
			names:  []string{"is-unset", "silently ignored"},
		},
		{
			name:   "is with no values at all",
			filter: Filter{Predicates: []Predicate{{Field: "Status", Match: MatchIs}}},
			names:  []string{"no values", "everything or"},
		},
		{
			name:   "a value outside the vocabulary the field published",
			filter: Filter{Predicates: []Predicate{{Field: "Status", Match: MatchIs, Values: []string{"In Review"}}}},
			names:  []string{`"In Review"`, `"Backlog"`, `"Shipped"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.filter.CheckAgainst(filterCatalogue())
			if err == nil {
				t.Fatalf("CheckAgainst accepted %s: the backend answers it with an empty set and no error, so a "+
					"caller cannot tell it from \"no items match\"", tc.filter)
			}
			for _, want := range tc.names {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %s, so it does not say which of four fixes to make: %v",
						want, err)
				}
			}
		})
	}
}

// TestFilter_CheckAgainstAcceptsWhatTheBoardCanAnswer is the counterweight, and
// without it every assertion above is satisfied by refusing everything.
//
// ⚠️ The text-field row is the one that matters. A text field publishes no
// vocabulary, so a vocabulary check applied to it unconditionally would make text
// fields unfilterable — refusing every value against an empty Options list.
//
// FALSIFIER, verified red: drop the `len(fld.Options) == 0` guard.
func TestFilter_CheckAgainstAcceptsWhatTheBoardCanAnswer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter Filter
	}{
		{"the zero filter admits everything", Filter{}},
		{"a value in the published vocabulary",
			Filter{Predicates: []Predicate{{Field: "Status", Match: MatchIs, Values: []string{"Shipped"}}}}},
		{"is-not with a published value",
			Filter{Predicates: []Predicate{{Field: "Status", Match: MatchIsNot, Values: []string{"Backlog"}}}}},
		{"is-unset with no values",
			Filter{Predicates: []Predicate{{Field: "Status", Match: MatchIsUnset}}}},
		{"any value on a field that published NO vocabulary",
			Filter{Predicates: []Predicate{{Field: "Notes", Match: MatchIs, Values: []string{"anything at all"}}}}},
		{"a conjunction of two admissible predicates", Filter{Predicates: []Predicate{
			{Field: "Status", Match: MatchIs, Values: []string{"Shipped"}},
			{Field: "Notes", Match: MatchIsUnset},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.filter.CheckAgainst(filterCatalogue()); err != nil {
				t.Errorf("CheckAgainst refused a filter this board CAN answer (%s): a check that refuses "+
					"everything satisfies every negative row and is worth nothing: %v", tc.filter, err)
			}
		})
	}
}

// TestFilter_ARefusalNamesTheVocabularyEvenWhenItIsEMPTY guards the shape
// arqtiqa/arqtos-connectors#230 was filed against one repo over: a refusal that
// lists the available set and renders NOTHING when the set is empty, leaving the
// operator to read a truncated sentence as a logging bug — and blaming the value
// when the real cause is that the board published no vocabulary.
//
// FALSIFIER, verified red: make quotedOrNone return "" for an empty slice.
func TestFilter_ARefusalNamesTheVocabularyEvenWhenItIsEMPTY(t *testing.T) {
	empty := Catalogue{}
	f := Filter{Predicates: []Predicate{{Field: "Status", Match: MatchIs, Values: []string{"Shipped"}}}}

	err := f.CheckAgainst(empty)
	if err == nil {
		t.Fatal("a filter naming a field was accepted against a catalogue serving NO fields")
	}
	if !strings.Contains(err.Error(), "none at all") {
		t.Errorf("the refusal renders the empty field set as nothing, so the message reads truncated and the "+
			"operator is sent after the field NAME when the cause is a board that published none: %v", err)
	}
}

// TestMatch_EveryValueRendersAndTheZeroIsNamed keeps the refusals readable.
//
// ⚠️ A Match with no String() row renders as an integer, and a refusal saying
// "Status match(7)" tells an operator nothing. The zero value is included
// deliberately: it is the one a caller hits by forgetting the field, so it is the
// one most likely to appear in a message.
//
// FALSIFIER, verified red: delete a row from matchNames.
func TestMatch_EveryValueRendersAndTheZeroIsNamed(t *testing.T) {
	for _, m := range []Match{MatchUnspecified, MatchIs, MatchIsNot, MatchIsUnset} {
		got := m.String()
		if got == "" || strings.HasPrefix(got, "match(") {
			t.Errorf("Match(%d) renders as %q, so a refusal carrying it says nothing an operator can act on",
				int(m), got)
		}
	}
	// And an out-of-set value is not silently blank either.
	if got := Match(99).String(); !strings.Contains(got, "99") {
		t.Errorf("an unknown Match renders as %q, which hides which value it was", got)
	}
}
