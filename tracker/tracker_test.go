package tracker

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// TestClassIsThePublishedOne pins this package to the class the connector
// package publishes, and it is the forward form of a canary this contract
// carried while it lived outside the SDK ("the SDK does not publish Tracker
// yet — adopt it when it does"). This is that day, asserted rather than
// assumed.
//
// It is not a tautology. A class this package contracts for that
// connector.Classes() does not carry is the half-added state connector.classes
// exists to prevent: every type here would build, every manifest declaring the
// class would be refused by manifest.Doc.Validate, and the symptom would point
// at the manifest, which was correct.
func TestClassIsThePublishedOne(t *testing.T) {
	if !connector.ClassTracker.Valid() {
		t.Fatalf("connector.ClassTracker is not in connector.Classes() (%v): this package contracts for a class "+
			"the SDK does not know, so every manifest declaring it is refused while every type here still builds",
			connector.Classes())
	}
	if !slices.Contains(connector.Classes(), connector.ClassTracker) {
		t.Fatalf("connector.Classes() = %v, missing ClassTracker", connector.Classes())
	}
}

func TestKnownCapabilities_IsACopy(t *testing.T) {
	got := KnownCapabilities()
	if len(got) == 0 {
		t.Fatal("the class declares no capabilities, so nothing can be checked against its vocabulary")
	}
	got[0] = "mutated-by-caller"
	if KnownCapabilities().Has("mutated-by-caller") {
		t.Error("KnownCapabilities returned the package's own slice: a caller can extend the closed vocabulary by writing to it")
	}
}

// TestTracker_IsExactlyTheFiveOperations is the structural gate on the single
// most important decision in this contract. The abstract backend this replaces
// grew to 15 methods, one per vendor API, which is how a 1,850-call sweep
// became possible. Adding a sixth operation is a contract change, and this test is
// what makes it one: it fails on the addition rather than at review.
func TestTracker_IsExactlyTheFiveOperations(t *testing.T) {
	base := methodNames(reflect.TypeOf((*connector.Connector)(nil)).Elem())
	all := methodNames(reflect.TypeOf((*Tracker)(nil)).Elem())

	own := make([]string, 0, len(all))
	for _, m := range all {
		if !slices.Contains(base, m) {
			own = append(own, m)
		}
	}
	want := []string{"Apply", "Catalogue", "Create", "GetItems", "Scan"}
	if !slices.Equal(own, want) {
		t.Fatalf("Tracker's own operations are %v, want exactly %v — the contract is five batch-first operations, "+
			"and a sixth is a contract change rather than a convenience", own, want)
	}
	// The embedded base must still be embedded rather than restated.
	for _, m := range base {
		if !slices.Contains(all, m) {
			t.Errorf("Tracker does not carry connector.Connector's %s: the base contract is embedded, never restated", m)
		}
	}
}

func methodNames(typ reflect.Type) []string {
	out := make([]string, 0, typ.NumMethod())
	for i := range typ.NumMethod() {
		out = append(out, typ.Method(i).Name)
	}
	slices.Sort(out)
	return out
}

// TestOptionalOperations_AreNotInTheContract: an optional operation that sat
// in [Tracker] would be one every connector had to implement, so a backend
// without trains or schema administration would answer it with a stub — the
// declaration and the reality drifting apart at exactly the point a host acts
// on them.
func TestOptionalOperations_AreNotInTheContract(t *testing.T) {
	all := methodNames(reflect.TypeOf((*Tracker)(nil)).Elem())
	for _, m := range []string{"ListTrains", "CreateTrains", "CloseTrains", "EnsureFields"} {
		if slices.Contains(all, m) {
			t.Errorf("%s is in Tracker: optional operations live behind a capability and an interface of their own", m)
		}
	}
	// ⚠️ 2 → 3 on 2026-08-07, CloseTrains (arqtos-connectors#144). Changed
	// deliberately: this count exists so growing an optional tier is a
	// conscious act rather than a drift, and the base Tracker contract is
	// still FIVE operations — that is the number this file's neighbours guard
	// and CloseTrains does not touch it.
	if got := len(methodNames(reflect.TypeOf((*TrainAdmin)(nil)).Elem())); got != 3 {
		t.Errorf("TrainAdmin carries %d methods, want 3 (ListTrains, CreateTrains, CloseTrains)", got)
	}
	if got := len(methodNames(reflect.TypeOf((*SchemaAdmin)(nil)).Elem())); got != 1 {
		t.Errorf("SchemaAdmin carries %d methods, want 1 (EnsureFields)", got)
	}
}

func TestResolved_RefusesAnEmptyList(t *testing.T) {
	res, err := Resolved([]Item{}, Complete)
	if err == nil {
		t.Fatal("Resolved accepted an empty list as a complete success")
	}
	if _, ierr := res.Items(); ierr == nil {
		t.Error("the resolution returned alongside the error is readable: a connector that ignored the error would pass an unread board off as an empty one")
	}
}

// TestResolved_RefusesAPartialRead is the property a 950-item board scan
// depends on: a cursor that broke off partway cannot report a smaller
// success, because an audit over the shorter list reports every unexamined
// item as compliant.
func TestResolved_RefusesAPartialRead(t *testing.T) {
	res, err := Resolved([]Item{{Title: "one"}}, Partial)
	if err == nil {
		t.Fatal("Resolved accepted a PARTIAL read as a success; a truncated scan must surface as a typed failure")
	}
	if _, ierr := res.Items(); ierr == nil {
		t.Error("a partial resolution is readable")
	}
}

func TestEmptyList_IsReadableAndEmpty(t *testing.T) {
	items, err := EmptyList[Item]().Items()
	if err != nil {
		t.Fatalf("an asserted-empty list must be readable: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("EmptyList carries %d items", len(items))
	}
}

// TestZeroResolution_IsNotAnEmptyList is the conflation the whole type exists
// to remove, asserted here because it is what a failure path produces when
// someone writes `return Resolution[Item]{}, err` and then forgets the err.
func TestZeroResolution_IsNotAnEmptyList(t *testing.T) {
	var zero Resolution[Item]
	if _, err := zero.Items(); err == nil {
		t.Fatal("the zero Resolution read as a list; a failure path returning it would look like a board with no items")
	}
}

func TestFieldClass_Routable(t *testing.T) {
	for _, tc := range []struct {
		class FieldClass
		want  bool
	}{
		{FieldClassUnspecified, false},
		{FieldClassBoard, true},
		{FieldClassItem, true},
		{FieldClassAttribute, true},
		{FieldClass(99), false},
	} {
		if got := tc.class.Routable(); got != tc.want {
			t.Errorf("FieldClass(%d).Routable() = %v, want %v", int(tc.class), got, tc.want)
		}
	}
}

func TestFieldClass_String_NamesWhatWasPassed(t *testing.T) {
	if got := FieldClass(99).String(); !strings.Contains(got, "99") {
		t.Errorf("FieldClass(99).String() = %q: a class outside the vocabulary must name what was passed", got)
	}
	if got := FieldClassUnspecified.String(); got == FieldClassBoard.String() {
		t.Error("two classes render the same name")
	}
}

func TestValueKind_Writable(t *testing.T) {
	for _, tc := range []struct {
		kind ValueKind
		want bool
	}{
		// A caller that said nothing did not mean "clear": the zero value is
		// refused, and ValueUnset is how a clear is SAID.
		{ValueUnspecified, false},
		{ValueUnset, true},
		{ValueText, true},
		{ValueNumber, true},
		{ValueDate, true},
		{ValueOption, true},
		{ValueUsers, true},
		{ValueNames, true},
		{ValueKind(99), false},
	} {
		if got := tc.kind.Writable(); got != tc.want {
			t.Errorf("ValueKind(%d).Writable() = %v, want %v", int(tc.kind), got, tc.want)
		}
	}
}

func TestValueKind_String_NamesWhatWasPassed(t *testing.T) {
	if got := ValueKind(99).String(); !strings.Contains(got, "99") {
		t.Errorf("ValueKind(99).String() = %q: a kind outside the vocabulary must name what was passed", got)
	}
}

func TestCloseReason_UsableAsReason(t *testing.T) {
	for _, tc := range []struct {
		reason CloseReason
		want   bool
	}{
		{CloseReasonUnspecified, false},
		{CloseReasonCompleted, true},
		{CloseReasonCanceled, true},
		{CloseReason(99), false},
	} {
		if got := tc.reason.UsableAsReason(); got != tc.want {
			t.Errorf("CloseReason(%d).UsableAsReason() = %v, want %v", int(tc.reason), got, tc.want)
		}
	}
}

func TestCloseReasons_IsACopyOfTheClosedVocabulary(t *testing.T) {
	got := CloseReasons()
	if len(got) < 3 {
		t.Fatalf("CloseReasons() = %v, want the whole closed vocabulary", got)
	}
	got[0] = CloseReason(99)
	if slices.Contains(CloseReasons(), CloseReason(99)) {
		t.Error("CloseReasons returned the package's own slice")
	}
}

func TestCloseReason_String_NamesWhatWasPassed(t *testing.T) {
	if got := CloseReason(99).String(); !strings.Contains(got, "99") {
		t.Errorf("CloseReason(99).String() = %q", got)
	}
}

// TestDate_Valid covers the reason this type is not a time.Time: a target
// date is a calendar date, and the only failure worth refusing before a
// network call is a date that does not exist.
func TestDate_Valid(t *testing.T) {
	for _, tc := range []struct {
		date Date
		want bool
	}{
		{Date{2026, time.July, 31}, true},
		{Date{2024, time.February, 29}, true},
		{Date{2026, time.February, 29}, false},
		{Date{2026, time.June, 31}, false},
		{Date{2026, time.Month(13), 1}, false},
		{Date{}, false},
	} {
		if got := tc.date.Valid(); got != tc.want {
			t.Errorf("Date%+v.Valid() = %v, want %v", tc.date, got, tc.want)
		}
	}
}

func TestDate_String(t *testing.T) {
	if got := (Date{2026, time.July, 31}).String(); got != "2026-07-31" {
		t.Errorf("Date.String() = %q, want the ISO 8601 calendar date", got)
	}
}

func TestBoardRef_Valid_RequiresTheWholeAddress(t *testing.T) {
	full := BoardRef{Provider: "boardprovider", Instance: "example-org", Board: "3"}
	if !full.Valid() {
		t.Fatalf("%v is a fully-qualified address and was refused", full)
	}
	for _, partial := range []BoardRef{
		{Instance: "example-org", Board: "3"},
		{Provider: "boardprovider", Board: "3"},
		{Provider: "boardprovider", Instance: "example-org"},
		{},
	} {
		if partial.Valid() {
			t.Errorf("%#v was accepted: a board that is not (provider, instance, board) cannot be routed to, "+
				"and a real estate runs two boards on one provider plus a tracker on another", partial)
		}
	}
}

// TestItemRef_SameNumberOnAnotherTracker_IsADifferentItem is the falsifier
// for the whole reason an address is fully qualified: if two items on
// different trackers compared equal, the pre-network refusal of a foreign
// parent would pass a cross-tracker link straight through.
func TestItemRef_SameNumberOnAnotherTracker_IsADifferentItem(t *testing.T) {
	here := ItemRef{
		Board:  BoardRef{Provider: "boardprovider", Instance: "example-org", Board: "1"},
		Scope:  "example-org/web-app",
		Number: 284,
	}
	otherBoard := here
	otherBoard.Board.Board = "3"
	otherProvider := here
	otherProvider.Board.Provider = "otherprovider"

	if here == otherBoard {
		t.Error("two items with the same number on two boards of one provider compared equal")
	}
	if here == otherProvider {
		t.Error("two items on different trackers with the same number compared equal")
	}
	if here != (ItemRef{Board: here.Board, Scope: here.Scope, Number: here.Number}) {
		t.Error("an ItemRef is not comparable by value, so a foreign-board refusal cannot be a comparison")
	}
}

// TestBoardRef_String and TestItemRef_String pin the exact renderings their
// docstrings promise, because these two strings are not decoration: the address rule makes
// the pre-network cross-tracker refusal load-bearing with three live trackers,
// and the refusal's message is how an operator sees WHICH board a foreign
// parent named. A refusal that rendered two different boards the same way is
// unactionable.
func TestBoardRef_String(t *testing.T) {
	got := BoardRef{Provider: "boardprovider", Instance: "example-org", Board: "3"}.String()
	if want := "boardprovider/example-org/3"; got != want {
		t.Errorf("BoardRef.String() = %q, want %q", got, want)
	}
}

func TestItemRef_String(t *testing.T) {
	got := ItemRef{
		Board:  BoardRef{Provider: "boardprovider", Instance: "example-org", Board: "3"},
		Scope:  "example-org/service-api",
		Number: 54,
	}.String()
	if want := "boardprovider/example-org/3:example-org/service-api#54"; got != want {
		t.Errorf("ItemRef.String() = %q, want %q", got, want)
	}
}

// TestKnownCapabilities_IsTheClosedSet asserts the vocabulary's CONTENTS.
// TestKnownCapabilities_IsACopy above already covers the copy property, and
// this deliberately does not restate it: a duplicated assertion is one that
// cannot be observed to fail on its own.
//
// The contents matter because manifest.Doc.Validate closes a connector.yml
// against exactly this set, so a capability missing here cannot be declared at
// all — and one present here is declarable by every third party. Adding or
// removing one is a contract change, and this is what makes it one.
func TestKnownCapabilities_IsTheClosedSet(t *testing.T) {
	got := KnownCapabilities()
	want := connector.Capabilities{
		CapNativeTypes, CapNativeHierarchy, CapCrossScope, CapItemFields,
		CapTrains, CapScopedTrains, CapSchemaAdmin, CapBoardMembership,
		CapServerFilter,
		// ⚠️ arqtiqa/arqtos-sdk-go#65. Two SEPARATE tiers rather than widening
		// server_filter, because a dimension added to Filter breaks an existing
		// FilteredScanner silently — it answers a SUPERSET, which is
		// indistinguishable from the right answer.
		CapServerFilterState,
		CapServerFilterTime,
		CapServerFilterType,
	}
	if len(got) != len(want) {
		t.Fatalf("KnownCapabilities() = %v, want exactly %v — a capability added or removed here is a contract change", got, want)
	}
	for _, c := range want {
		if !got.Has(c) {
			t.Fatalf("KnownCapabilities() is missing %q", c)
		}
	}
}

// TestCapabilityWireNamesArePinned: these strings are what an external author
// writes in a connector.yml and what a host matches on. Renaming one silently
// unregisters every connector that declared it, and this repo is public for
// third parties to encode against exactly these spellings.
func TestCapabilityWireNamesArePinned(t *testing.T) {
	for declared, want := range map[connector.Capability]string{
		CapNativeTypes:     "native_types",
		CapNativeHierarchy: "native_hierarchy",
		CapCrossScope:      "cross_scope",
		CapItemFields:      "item_fields",
		CapTrains:          "trains",
		CapScopedTrains:    "scoped_trains",
		CapSchemaAdmin:     "schema_admin",
		CapBoardMembership: "board_membership",
	} {
		if string(declared) != want {
			t.Errorf("capability wire name is %q, want %q: renaming it unregisters every connector that declared it", declared, want)
		}
	}
}

// TestTypeIsNotASecondFieldWriteSurface is the structural gate on the one-write-surface rule.
//
// The contract defines Change as exactly {Target, Fields, Parent, Lifecycle}
// and collapses that backend's six field-write methods — SetIssueFieldValue, UpdateItemFieldValue,
// SetLabels, SetMilestone, SetAssignees, SetIssueType — into Change.Fields, so
// that the routing rule lives in the Catalogue rather than in prose a caller has
// to have read. A Change.Type or a Draft.Type is SetIssueType back as a struct
// field: a second place to say one fact, and the drift that follows is a write
// that lands in one surface while a reader checks the other.
//
// An item's type is written as a [FieldClassAttribute] entry in Fields, by name,
// like its labels and its assignees.
//
// The set is asserted EXACTLY rather than by absence, so that any new field on
// Change has to be argued for here. Place was added under that rule and is
// admitted because it writes no field: it changes whether the item is ON the
// board, which is not a value any Catalogue reports and has no [FieldClass]. A
// field that could be spelled as a Fields entry does not belong in this list.
func TestTypeIsNotASecondFieldWriteSurface(t *testing.T) {
	got := fieldNames(reflect.TypeOf(Change{}))
	if want := []string{"Target", "Fields", "Parent", "Lifecycle", "Place"}; !slices.Equal(got, want) {
		t.Errorf("Change carries %v, want exactly %v", got, want)
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(Change{}), reflect.TypeOf(Draft{})} {
		if _, ok := typ.FieldByName("Type"); ok {
			t.Errorf("%s.Type exists: an item's type is written through Fields as an entry of class %s, and one "+
				"fact gets exactly one write surface", typ.Name(), FieldClassAttribute)
		}
	}
	// The READ keeps its promoted Type, and that asymmetry is deliberate: the
	// methodology keys its status flow and its required-field matrix on the
	// type, and a connector without CapNativeTypes must answer it from whatever
	// convention its backend does carry.
	if _, ok := reflect.TypeOf(Item{}).FieldByName("Type"); !ok {
		t.Error("Item lost its Type: every reader keys on it and no Selection can switch it off")
	}
}

func fieldNames(typ reflect.Type) []string {
	out := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		out = append(out, typ.Field(i).Name)
	}
	return out
}

// TestItem_HasONEIdiomForAFactThatCouldNotBeRead gates the SHAPE of the
// could-not-read members rather than their presence.
//
// [Item.ChildrenErr] distinguishes "read and empty" from "could not read" for
// the child set, and the parent link needs the same distinction. A second
// mechanism for one idea — a ParentRead bool, a ParentUnread flag, a sentinel
// [ItemParent] — would leave one contract with two vocabularies for the same
// fact, which nobody keeps in step. So the rule is checked rather than
// described: every member that can fail to arrive as a WHOLE has a sibling
// named <Member>Err of type error.
//
// ⚠️ The scalar pair is deliberately NOT this shape, and the asymmetry is the
// decision rather than an exception. [Item.Open] and [ItemParent.Open] cannot
// express "unread" in the value, and an error beside a boolean would say WHY the
// state is unknown while nothing needs to know why — so those carry a read
// FLAG, and both of them carry the SAME one. Two shapes, each used for every
// member of its kind, is one idiom per idea; the failure this gate is against is
// two shapes for one kind.
//
// FALSIFIER: rename ParentErr to ParentUnread and make it a bool; delete it; or
// rename [ItemParent.OpenRead] to StateKnown, which leaves the two scalar pairs
// spelled differently.
func TestItem_HasONEIdiomForAFactThatCouldNotBeRead(t *testing.T) {
	item := reflect.TypeOf(Item{})
	errType := reflect.TypeOf((*error)(nil)).Elem()

	// The WHOLE-member pairs: a link or a set that either arrived or did not.
	for _, member := range []string{"Children", "Parent"} {
		if _, held := item.FieldByName(member); !held {
			t.Fatalf("Item has no %s, so this gate has nothing to check the read-failure idiom against", member)
		}
		sibling, held := item.FieldByName(member + "Err")
		if !held {
			t.Errorf("Item.%s can fail to arrive and Item has no %sErr: %q is how this contract already spells "+
				"\"could not read\" for a set, and a second spelling for one idea is how one contract grows two "+
				"vocabularies nobody keeps in step", member, member, "ChildrenErr")
			continue
		}
		if sibling.Type != errType {
			t.Errorf("Item.%sErr is a %s and not an error: the failure carries WHY, which is what an operator acts "+
				"on, and a boolean throws it away", member, sibling.Type)
		}
	}

	// The SCALAR pairs: a value that cannot express its own absence, so the read
	// flag is the second member — and it is the SAME member on both types.
	for _, pair := range []struct {
		typ  reflect.Type
		what string
	}{{item, "Item"}, {reflect.TypeOf(ItemParent{}), "ItemParent"}} {
		flag, held := pair.typ.FieldByName("OpenRead")
		if !held {
			t.Errorf("%s.Open is a two-valued bool with no OpenRead beside it, so a state nobody read is "+
				"indistinguishable from a closed one — and read as closed it reports live work as finished",
				pair.what)
			continue
		}
		if flag.Type.Kind() != reflect.Bool {
			t.Errorf("%s.OpenRead is a %s: it answers whether the state was read and nothing else", pair.what,
				flag.Type)
		}
	}
}

// TestItemParent_CarriesTheThreeFactsAHierarchyAuditNeeds pins WHAT the parent
// link carries, because a bare reference is exactly as type-correct and is what
// this member held before.
//
// Each fact answers a rule that cannot run without it: the KIND is what reports
// a wrong-kind parent, and the STATE plus its read flag are what report live
// work under a finished aggregator without inventing the parent's lifecycle.
//
// FALSIFIER: revert Item.Parent to *ItemRef.
func TestItemParent_CarriesTheThreeFactsAHierarchyAuditNeeds(t *testing.T) {
	parent, held := reflect.TypeOf(Item{}).FieldByName("Parent")
	if !held {
		t.Fatal("Item has no Parent")
	}
	if parent.Type != reflect.PointerTo(reflect.TypeOf(ItemParent{})) {
		t.Fatalf("Item.Parent is a %s: a bare reference cannot carry the parent's kind or its state, so an audit "+
			"over it can report neither a wrong-kind parent nor a closed one holding open work", parent.Type)
	}
	if got, want := fieldNames(reflect.TypeOf(ItemParent{})),
		[]string{"Ref", "Type", "Open", "OpenRead"}; !slices.Equal(got, want) {
		t.Errorf("ItemParent carries %v, want exactly %v", got, want)
	}
}

func TestItemRef_Valid(t *testing.T) {
	board := BoardRef{Provider: "boardprovider", Instance: "example-org", Board: "3"}
	if !(ItemRef{Board: board, Scope: "example-org/service-api", Number: 54}).Valid() {
		t.Fatal("a fully-addressed item was refused")
	}
	for name, ref := range map[string]ItemRef{
		"no board":  {Scope: "example-org/service-api", Number: 54},
		"no scope":  {Board: board, Number: 54},
		"no number": {Board: board, Scope: "example-org/service-api"},
		// A draft board item has neither scope nor number. It is not
		// addressable, and that is a fact about the item rather than a bug.
		"a draft item": {Board: board},
	} {
		if ref.Valid() {
			t.Errorf("%s was accepted as an item address: %#v", name, ref)
		}
	}
}
