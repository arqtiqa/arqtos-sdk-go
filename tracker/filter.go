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

// A Lifecycle narrows a read to items that are OPEN or CLOSED, and it is
// arqtiqa/arqtos-sdk-go#65's first dimension.
//
// ⚠️ It is NOT [Lifecycle], and the two are easy to confuse because the words
// overlap. [Lifecycle] is a WRITE — a close-or-reopen action on a [Change], carrying
// a [CloseReason] and an audit comment. This is a READ dimension: which items a scan
// returns. They share a domain and nothing else, which is why this one is named for
// the item's STATE rather than for its lifecycle.
//
// ⚠️ It is BINARY, matching [Item.Open], and that is a decision rather than a
// simplification. The four trackers publish three different vocabularies — GitHub
// `is:open`/`is:closed`, GitLab
// `opened`/`closed`/`locked`/`all`, and Linear and Plane BOTH a five-value grouping
// (Linear a state type; Plane a state_group of backlog/unstarted/started/completed/
// cancelled). Binary is what all four express EXACTLY — Plane folds via
// `state_group in [completed, cancelled]`, a lookup it genuinely has, and GitLab's
// `locked` folds to OPEN because a locked item is not a closed one. A
// filter that could ask about a richer lifecycle than [Item] can REPORT would be a
// predicate whose answer a caller cannot check against what came back, and
// trackerconform's own agreement check compares [Item.Open] precisely because that
// is the lifecycle the contract has. A five-state vocabulary is a later question and
// needs [Item] to carry it first.
type ItemState int

const (
	// ItemStateAny is the zero value and narrows NOTHING, so a Filter that says
	// nothing about lifecycle admits open and closed items alike. Unlike
	// [MatchUnspecified] this is a legitimate value: "I did not ask" is a
	// coherent thing to mean about a whole dimension, where it is not a coherent
	// thing to mean about how one predicate compares.
	ItemStateAny ItemState = iota
	// ItemStateOpen admits only items whose [Item.Open] is true.
	ItemStateOpen
	// ItemStateClosed admits only items whose [Item.Open] is false.
	ItemStateClosed
)

var itemStateNames = map[ItemState]string{
	ItemStateAny:    "any state",
	ItemStateOpen:   "open",
	ItemStateClosed: "closed",
}

// String renders the lifecycle for a refusal. Value receiver, as [Match.String].
func (s ItemState) String() string {
	if name, known := itemStateNames[s]; known {
		return name
	}
	return fmt.Sprintf("item_state(%d)", int(s))
}

// A Filter is the CONJUNCTION of everything it says: an item is admitted when every
// predicate admits it AND its lifecycle matches AND its type is one of those named.
//
// The zero Filter admits everything, so a [FilteredScanner] handed one answers
// exactly what [Tracker.Scan] would.
//
// # ⚠️ The three dimensions are separately CAPABILITY-GATED, and that is why they
// are separate members
//
// Adding a dimension to this struct is the one change that can break an existing
// [FilteredScanner] SILENTLY: a scanner that ignores a new member answers with a
// SUPERSET, and a superset is indistinguishable from the right answer — both are
// well-formed lists of real items. arqtiqa/arqtos-sdk-go#57 is the same shape in
// another tier: [TrainAdmin] grew a method, nothing failed to BUILD, and six tests
// failed hours later at a pin bump.
//
// So each dimension has its own capability — [CapServerFilter],
// [CapServerFilterState], [CapServerFilterType] — and a host reads the manifest
// BEFORE it loads a connector. A dimension a connector never declared is never sent
// to it, and one sent anyway is refused with cerr.KindUnsupported rather than
// ignored.
//
// ⚠️ These two tiers add no new METHOD, so unlike every other optional tier they
// cannot be checked structurally by optional/declared-is-implemented — there is
// nothing to type-assert. They are checked BEHAVIOURALLY instead, in both arms:
// declared means the dimension must be honoured, and NOT declared means it must be
// refused. The undeclared arm is the one that catches a scanner ignoring a member,
// and it is stronger here than a structural check would be.
type Filter struct {
	// Predicates are conditions on the board's own fields, gated by
	// [CapServerFilter].
	Predicates []Predicate
	// State narrows by open/closed, gated by [CapServerFilterState]. The zero
	// value narrows nothing.
	State ItemState
	// Types are the item types to admit, gated by [CapServerFilterType]. Empty
	// narrows nothing, and several are a DISJUNCTION — an item is admitted when its
	// [Item.Type] is any of them, the same way [MatchIs] treats several Values.
	//
	// ⚠️ It is a member rather than a [Predicate] because type is not a published
	// FIELD. [Predicate.Field] resolves against [Catalogue.Fields] and type is not
	// there: measured 2026-08-08, a GitHub board answers `Kind:Story` with 0 while
	// `type:Story` answers 789. So a Predicate naming it would be refused by
	// [Filter.CheckAgainst] as an unresolvable field name, which is the right answer
	// to the wrong question.
	//
	// ⚠️ Both preconditions this contract insists on are met, which is what makes
	// this the best-grounded of the three dimensions. [Item.Type] REPORTS it, so a
	// caller can check a returned item against what it asked — the rule that keeps
	// [ItemState] binary. And [Catalogue.Types] PUBLISHES the vocabulary, so
	// CheckAgainst can validate a requested type against the board's own set exactly
	// as it validates a value against a single-select field's Options. No new
	// validation concept is needed.
	//
	// ⚠️ A backend WITHOUT native types can still serve this. [CapNativeTypes]'s own
	// doc says one tracker serves a type the API can set and filter on while another
	// "has only labels and leaves Story a string in a label list" — and that
	// [Item.Type] is populated either way. So a label-based connector renders this as
	// a label query. CapNativeTypes governs the WRITE and says nothing about this
	// read.
	Types []string
	// ---------------------------------------------------------------------------
	// ⚠️ THERE IS NO TEMPORAL DIMENSION — retired, arqtiqa/arqtos-sdk-go#71
	// ---------------------------------------------------------------------------
	//
	// A "changed at or after" bound (`ChangedAtOrAfter time.Time`, gated by
	// `CapServerFilterTime`) shipped in v0.3.2 and is GONE. Its absence is
	// deliberate rather than pending, and the condition for its return is stated
	// below so the next person to want it finds the argument instead of
	// re-deriving it.
	//
	// # Why it went
	//
	// ⚠️ [Item] carries NO TIMESTAMP, so a temporal filtered read cannot be
	// checked against what came back. A scanner that ignored the bound would
	// answer with a SUPERSET — and per the reasoning that gives every dimension
	// its own tier, a superset is INDISTINGUISHABLE from the right answer. State
	// and Types are checkable because [Item.Open] and [Item.Type] are reported;
	// this one was not, and trackerconform held it in neither arm. Measured on
	// main 2026-08-10: trackerconform carried 10 references to the State tier, 8
	// to Type, and ZERO to time, while `CapServerFilterTime`'s own doc claimed it
	// was "checked the same way". A published contract asserting a check that does
	// not exist is worse than an absent capability.
	//
	// This is the same rule that keeps [ItemState] binary: A FILTER MUST NOT ASK
	// WHAT AN ITEM CANNOT REPORT. It applies to time exactly as it applies to
	// lifecycle.
	//
	// # The condition for its return
	//
	// [Item] gains a reported last-changed timestamp. Then the bound is checkable
	// against the response, trackerconform can hold both arms, and the tier means
	// something. Until then, re-adding it re-publishes an unverifiable capability
	// and TestKnownCapabilities_HasNoTemporalTier will refuse it.
	//
	// # The measurements, kept because they cost real work and outlive the field
	//
	// ⚠️ Whoever revives this inherits these rather than repeating them — and the
	// first is why the bound must be INCLUSIVE, not strict:
	//
	//   - GitHub, live board 2026-08-08: `updated:>2026-08-01` answered 284 items,
	//     `updated:>=2026-08-01` answered 308. A 24-item difference, so the two
	//     forms are not interchangeable.
	//   - GitLab's issues API offers only `updated_after`, documented as "on or
	//     after the given time" (verified in app/finders/issuable_finder.rb, not
	//     only the REST reference). It CANNOT express the strict form, so a
	//     contract offering strict would force it to refuse or approximate — and an
	//     approximated boundary is a wrong answer wearing a correct one's clothes.
	//   - Plane's IssueFilterSet compares `updated_at` by CALENDAR DATE
	//     (yyyy-MM-dd, date component, lookups exact and range only), so "at or
	//     after 14:30" renders as everything from 00:00 that day — a superset this
	//     contract forbids. A date-granular bound, if ever wanted, is a NEW tier
	//     beside a reported-timestamp one, not a loosening of it.
	//   - Inclusive is also correct for the use it existed for: a caller
	//     reconciling against a watermark wants everything changed at or since its
	//     last read. A strict bound can MISS an item changed in the watermark's own
	//     instant; an inclusive one can only re-read a boundary item, which is
	//     idempotent.
	//
	// ⚠️ SUPERSEDED OPERATOR RULING, recorded rather than erased: on 2026-08-08 the
	// operator ruled the bound an INSTANT — that Plane declines the tier rather than
	// the other three backends losing precision they have. That ruling was about
	// GRANULARITY and stands on its own terms; it is superseded by the 2026-08-10
	// ruling to retire the dimension outright, which answers a different question
	// (whether the tier can be VERIFIED at all). A revival re-opens the 08-08
	// question and should start from its answer.
	// ---------------------------------------------------------------------------
}

// Empty reports whether this filter narrows anything.
//
// ⚠️ It accounts for every dimension. A Filter carrying only a Lifecycle is NOT
// empty, and a scanner treating it as empty would answer the whole board for a
// caller who asked for open items.
func (f Filter) Empty() bool {
	return len(f.Predicates) == 0 && f.State == ItemStateAny && len(f.Types) == 0
}

// String renders the whole filter for a refusal.
func (f Filter) String() string {
	if f.Empty() {
		return "no filter"
	}
	out := make([]string, 0, len(f.Predicates)+2)
	for _, p := range f.Predicates {
		out = append(out, p.String())
	}
	if f.State != ItemStateAny {
		out = append(out, "is "+f.State.String())
	}
	if len(f.Types) > 0 {
		out = append(out, "type is "+strings.Join(f.Types, "|"))
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
	if _, known := itemStateNames[f.State]; !known {
		return fmt.Errorf("the filter asks for %s, which is outside the closed set this contract publishes: a "+
			"state nobody named is not a state a connector can render, and defaulting it would answer a "+
			"different question from the one asked", f.State)
	}
	// The type vocabulary, validated the same way a single-select value is: against
	// what the BOARD published, not against a set this contract carries. ⚠️ An
	// unpublished type is refused for #168's measured reason — a backend answers a
	// type it does not recognise with an EMPTY SET rather than a complaint, which a
	// caller cannot tell from "no items of that type".
	for i, t := range f.Types {
		if t == "" {
			return fmt.Errorf("type %d in the filter is empty: an unnamed type is not a type, and a filter "+
				"that quietly dropped it would answer a wider question than the one asked", i)
		}
		if !slices.Contains(cat.Types, t) {
			return fmt.Errorf("the filter asks for the item type %q and this board serves %s: a type the "+
				"catalogue cannot resolve is REFUSED rather than sent, because a backend that does not "+
				"recognise it answers with an EMPTY SET and no error", t, quotedOrNone(cat.Types))
		}
	}
	// ⚠️ There is nothing here for a temporal bound, and that is not an omission:
	// the dimension was RETIRED (arqtiqa/arqtos-sdk-go#71). A catalogue publishes
	// fields, types and vocabularies and says nothing about time, so there was no
	// board fact to validate against — which is the shallower half of why the tier
	// went. The load-bearing half is that [Item] cannot REPORT a timestamp, so the
	// RESPONSE could not be checked either. See [Filter]'s retirement note.
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
