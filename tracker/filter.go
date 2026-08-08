package tracker

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// A filtered read — arqtiqa/arqtos-sdk-go#61.
//
// # The gap this closes
//
// [Tracker.Scan] returns EVERY item on the board, and [Selection] narrows the
// PAYLOAD and never the reach. So a caller that wants "the items whose Status is
// Shipped" has no way to say so: it reads the whole board and filters what came
// back. On a board of 1,190 items that is twelve round trips to answer a question
// about six.
//
// arqtiqa/arqtos-connectors#168 measured that GitHub Projects v2 answers this
// server-side — `ProjectV2.items(query:)` is a real field-predicate grammar over
// the board's own columns, and `items.totalCount` FOLLOWS the filter, so a
// filtered walk can still compare the backend's own demand against what arrived.
//
// # ⚠️ Why it is an OPTIONAL TIER and not a parameter on Scan
//
// Operator ruling, 2026-08-08. Both shapes were measured:
//
//   - a fourth parameter on Scan gives every implementer a COMPILE ERROR, which
//     is louder than the silent break an added interface method gives (see
//     arqtiqa/arqtos-cli#1169 for what silence costs). But it edits 77 files
//     across three repos, sequenced through the release diamond, for a feature
//     most callers do not use;
//   - an optional tier behind a capability leaves [Tracker.Scan] untouched and is
//     the shape [TrainAdmin] and [SchemaAdmin] already establish.
//
// ⚠️ It does NOT breach arqtiqa/arqtos-sdk-go#49's five-operations rule, and the
// distinction is worth stating because the two precedents look contradictory.
// #49 refused a sixth operation NAMED AFTER A VENDOR MUTATION —
// `addProjectV2ItemById` becoming `PlaceItem` — because that is how
// internal/tracker grew one method per vendor API to fifteen. A filtered read is
// a general capability of a tracker, like administering its delivery buckets, and
// the optional tiers are where those live.
//
// # ⚠️ The predicate is in the CONTRACT's terms, never a vendor grammar
//
// `query:` is GitHub's UI filter syntax. A contract carrying its text would make
// every other tracker translate FROM GitHub, which is the defect
// `OP_SERVICE_ACCOUNT_TOKEN` is named for in another seam: a generic surface
// bound to one vendor. So a [Predicate] names a field by the name [Catalogue]
// publishes for it and a value from the vocabulary that field published, and each
// connector renders that in its own grammar.
//
// That is also what makes catalogue validation a CONTRACT obligation rather than
// one connector's diligence — see [Filter.CheckAgainst].

// A Match is how one [Predicate] compares. The set is CLOSED and deliberately
// tiny.
//
// ⚠️ There is no OR and no nesting, and that is a decision rather than an
// omission. A conjunction of simple predicates is expressible by every backend
// worth supporting — and by a client-side loop when the backend offers nothing —
// whereas disjunction and grouping are where filter grammars diverge and where a
// connector would start approximating. A contract a connector can only
// approximate is one whose answers a caller cannot trust.
//
// A caller needing OR issues two reads and unions them, which is honest about
// costing two reads.
type Match int

const (
	// MatchUnspecified is the zero value and is never valid: a predicate that
	// did not say how to compare is refused rather than defaulted to equality,
	// because a defaulted comparison is one nobody chose.
	MatchUnspecified Match = iota
	// MatchIs admits an item whose field holds ANY of Values.
	MatchIs
	// MatchIsNot admits an item whose field holds NONE of Values.
	MatchIsNot
	// MatchIsUnset admits an item whose field has no value at all. It takes no
	// Values, and one supplied is a refusal rather than an ignored argument.
	MatchIsUnset
)

var matchNames = map[Match]string{
	MatchUnspecified: "unspecified",
	MatchIs:          "is",
	MatchIsNot:       "is-not",
	MatchIsUnset:     "is-unset",
}

// String renders the match for a refusal an operator reads. It is on a VALUE
// receiver, so the value form a %v of a [Predicate] produces renders too.
func (m Match) String() string {
	if name, known := matchNames[m]; known {
		return name
	}
	return fmt.Sprintf("match(%d)", int(m))
}

// A Predicate is one condition on one field, in the board's own vocabulary.
type Predicate struct {
	// Field is the field's name as [Catalogue] publishes it — never a backend
	// identity and never a vendor spelling.
	Field string
	// Match is how to compare. The zero value is refused.
	Match Match
	// Values are the values to compare against, from the vocabulary the field
	// published. Empty for [MatchIsUnset] and required for the other two.
	Values []string
}

// String renders the predicate for a refusal. Value receiver, as [Match.String].
func (p Predicate) String() string {
	if p.Match == MatchIsUnset {
		return fmt.Sprintf("%s %s", p.Field, p.Match)
	}
	return fmt.Sprintf("%s %s %s", p.Field, p.Match, strings.Join(p.Values, "|"))
}

// A Filter is the CONJUNCTION of its predicates: an item is admitted when every
// one of them admits it.
//
// The zero Filter admits everything, so a [FilteredScanner] handed one answers
// exactly what [Tracker.Scan] would.
type Filter struct {
	Predicates []Predicate
}

// Empty reports whether this filter narrows anything.
func (f Filter) Empty() bool { return len(f.Predicates) == 0 }

// String renders the whole filter for a refusal.
func (f Filter) String() string {
	if f.Empty() {
		return "no filter"
	}
	out := make([]string, 0, len(f.Predicates))
	for _, p := range f.Predicates {
		out = append(out, p.String())
	}
	return strings.Join(out, " AND ")
}

// CheckAgainst refuses a filter this catalogue cannot answer, and it is the
// obligation every [FilteredScanner] is held to BEFORE any request.
//
// ⚠️ It exists because of a measured hazard rather than as hygiene.
// arqtiqa/arqtos-connectors#168 found that GitHub answers `nosuchfield:x` with
// `totalCount=0` AND NO ERRORS — an unknown field name is not rejected, it
// silently returns an empty set. So a mistyped predicate reads exactly like "no
// items match": a wrong answer wearing a correct answer's clothes, and the
// caller cannot tell which it got. A connector that passed a predicate through
// unvalidated would trade a slow correct read for a fast wrong one.
//
// It refuses, in this order:
//
//  1. a predicate naming a field the catalogue does not carry — the hazard above;
//  2. [MatchUnspecified], which is a comparison nobody chose;
//  3. Values supplied with [MatchIsUnset], or absent from the other two;
//  4. a value outside the vocabulary a single-select field published. ⚠️ Only
//     for a field whose Options are non-empty: a text field publishes no
//     vocabulary, and refusing every value against an empty list would make text
//     fields unfilterable.
//
// It does NOT check whether the backend can express the predicate. That is the
// connector's own refusal, and it is a different fault: this one says the BOARD
// has no such field, the other says this BACKEND cannot ask.
func (f Filter) CheckAgainst(cat Catalogue) error {
	byName := make(map[string]Field, len(cat.Fields))
	for _, fld := range cat.Fields {
		byName[fld.Name] = fld
	}
	for i, p := range f.Predicates {
		fld, served := byName[p.Field]
		if !served {
			return fmt.Errorf("predicate %d asks about the field %q and this board serves %s: a field name the "+
				"catalogue cannot resolve is REFUSED rather than sent, because a backend that does not "+
				"recognise it answers with an EMPTY SET and no error — which a caller cannot tell from "+
				"\"no items match\"", i, p.Field, quotedOrNone(catalogueFieldNames(cat)))
		}
		switch p.Match {
		case MatchUnspecified:
			return fmt.Errorf("predicate %d on %q says how to compare with %s: a comparison is never defaulted "+
				"to equality, because a default is one nobody chose", i, p.Field, p.Match)
		case MatchIsUnset:
			if len(p.Values) > 0 {
				return fmt.Errorf("predicate %d on %q is %s and carries %d value(s): the values would be "+
					"silently ignored, and a filter that quietly drops half of what it was given answers a "+
					"different question from the one asked", i, p.Field, p.Match, len(p.Values))
			}
			continue
		case MatchIs, MatchIsNot:
			if len(p.Values) == 0 {
				return fmt.Errorf("predicate %d on %q is %s with no values: it would admit everything or "+
					"nothing depending on which way a connector read it, and neither is what the caller asked",
					i, p.Field, p.Match)
			}
		default:
			return fmt.Errorf("predicate %d on %q carries %s, which is outside the closed set this contract "+
				"publishes", i, p.Field, p.Match)
		}
		// The vocabulary, and only where the field published one.
		if len(fld.Options) == 0 {
			continue
		}
		for _, v := range p.Values {
			if !slices.Contains(fld.Options, v) {
				return fmt.Errorf("predicate %d asks for %q on the field %q and that field serves %s: a value "+
					"outside the vocabulary the field published is REFUSED, for the same reason an unknown "+
					"field name is — the backend answers it with an empty set rather than a complaint",
					i, v, p.Field, quotedOrNone(fld.Options))
			}
		}
	}
	return nil
}

// catalogueFieldNames is the catalogue's own field names, in the order it
// published them. ⚠️ Not `fieldNames`: tracker_test.go already declares one, and
// a package-level collision between production and test code is a compile error
// the next author has to untangle rather than a name worth having.
func catalogueFieldNames(cat Catalogue) []string {
	out := make([]string, 0, len(cat.Fields))
	for _, f := range cat.Fields {
		out = append(out, f.Name)
	}
	return out
}

// quotedOrNone renders a vocabulary for a refusal, and says so when it is EMPTY
// rather than rendering nothing.
//
// ⚠️ An empty tail reads as a truncated message. arqtiqa/arqtos-connectors#230
// was filed against exactly that shape one repo over: a refusal that listed the
// available set and said nothing when the set was empty, leaving the operator to
// read "and that scope carries " as a bug in the logger.
func quotedOrNone(in []string) string {
	if len(in) == 0 {
		return "none at all"
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, fmt.Sprintf("%q", s))
	}
	return strings.Join(out, ", ")
}

// A FilteredScanner reads the items of a board that MATCH a filter, and it is
// the optional tier behind [CapServerFilter].
//
// # ⚠️ Honour it or refuse it — never widen it
//
// A connector that ignored the filter would answer with a SUPERSET, and a caller
// cannot detect that any more than it can detect a subset: both are well-formed
// lists of real items. So the contract is binary. Either every returned item
// satisfies every predicate, or the call is a typed refusal saying which
// predicate could not be expressed.
//
// ⚠️ That refusal is legitimate and expected. A backend whose filter grammar
// cannot ask a question this contract can express must SAY so — the host's answer
// is then to Scan and filter client-side, which it can only reach from a refusal.
//
// # Completeness survives the filter
//
// A filtered read is still a paged read, so [Resolution] holds exactly as it does
// for [Tracker.Scan]: a walk that stopped partway is a typed failure and never a
// shorter list.
//
// ⚠️ And the demand it compares against must be the FILTERED demand.
// arqtiqa/arqtos-connectors#168 measured that GitHub's `totalCount` follows the
// filter, which is what makes this checkable there. A backend whose count
// ignores the filter has no filtered demand to compare against, and such a
// connector must report the walk's completeness from something else or refuse —
// never report a short filtered read as complete.
type FilteredScanner interface {
	// ScanWhere returns every item on the board that satisfies filter, paging
	// to exhaustion.
	//
	// sel narrows what each item carries, exactly as in [Tracker.Scan]; filter
	// narrows WHICH items, which is the whole difference between the two.
	//
	// A filter the catalogue cannot answer is refused before any request — see
	// [Filter.CheckAgainst]. A filter this BACKEND cannot express is refused
	// too, and the two refusals are different faults.
	//
	// The zero Filter admits everything, so ScanWhere with one answers what
	// [Tracker.Scan] answers.
	ScanWhere(ctx context.Context, board BoardRef, sel Selection, filter Filter) (Resolution[Item], error)
}
