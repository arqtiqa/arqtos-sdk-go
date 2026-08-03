// Package tracker is the Tracker connector-class contract: one adapter over
// one work tracker — one board on one instance of one provider — and its
// items, their fields, their hierarchy and their lifecycle.
//
// # How it composes with the rest of the SDK
//
// Nothing here is a second invention of something the SDK already publishes:
//
//   - the class is [github.com/arqtiqa/arqtos-sdk-go/connector.ClassTracker],
//     spelled in the connector package's closed set and nowhere else. This
//     package publishes no constant of its own for it, the same way roster and
//     codeci do not: a second name for one class is the half-added state
//     connector.classes exists to prevent, one level out.
//     TestClassIsThePublishedOne is what pins the connection;
//   - the base contract is connector.Connector, embedded, not restated;
//   - every failure is a *cerr.Error from the SDK's closed vocabulary, so a
//     host routes on the classification and never on message text. There is no
//     sentinel error, and the only error type of this package's own is
//     [FaultError] — the shape the codeci class publishes for a contract
//     violation;
//   - every list operation returns [Resolution], which IS the roster package's
//     fail-closed list resolution — aliased, not re-implemented. That is the
//     one place this class differs from codeci, which carries its own copy: the
//     invariant is class-independent, and a second copy of it is a second thing
//     to get wrong;
//   - optional operations sit behind capabilities and separate interfaces, the
//     way roster.Watcher sits behind roster.CapWatch.
//
// The conformance harness for this class is the trackerconform package, the
// way rosterconform is roster's.
//
// # Five operations, and why a sixth is a contract change
//
// The contract is batch-first and name-keyed, and it is five operations:
// [Tracker.Catalogue], [Tracker.Scan], [Tracker.GetItems], [Tracker.Create]
// and [Tracker.Apply]. The abstract backend this replaces grew to fifteen
// methods — one per vendor API — while its own documentation still said nine,
// and that growth is how a fifty-operation board update became a
// 1,850-request sweep: each method fetched what it needed for itself, so the
// same field catalogue was read once per operation and the same board was
// re-scanned once per rollup hop.
//
// Five operations that each take a SET remove that by construction. There is
// one field-write surface ([Change.Fields]), keyed by field NAME, and the
// adapter routes each name to whichever of the backend's APIs serves it — so
// a caller cannot pick the wrong API, because a caller does not pick an API.
// TestTracker_IsExactlyTheFiveOperations is the gate: adding an operation
// fails it, which is the point. An operation that seems to be missing is
// either a [Selection] on a read or a [Change] on a write.
//
// # No identity crosses this boundary
//
// Nothing in this contract carries a backend identifier: not a field id, not
// an option id, not an item id. Everything is addressed by NAME, and the
// [Catalogue] that resolves names is valid for one call chain and must not be
// stored. This is not tidiness. On the tracker this class was designed
// against, editing one option of a single-select field regenerates the
// identity of EVERY option in it, so a cached option id addresses a dead
// option and writes silently land nowhere. A contract that cannot express a
// cached identity cannot hold a stale one.
//
// # One connector serves one tracker
//
// An estate runs several trackers at once — two boards on one provider plus a
// tracker on another is the live arrangement this class was designed for — so
// a board number alone cannot route. Every address here is fully qualified:
// see [BoardRef] and [ItemRef]. Composing several trackers, and deciding
// which of them is primary, is the HOST's job; no connector may see a second
// tracker, which is what keeps this contract portable and puts the
// multi-tracker complexity in exactly one place.
//
// # Transport is a caller-supplied token
//
// A connector in this class receives its token from its caller — at
// construction, in memory — and never reads one from the environment. A
// connector that fell back to an ambient variable when its token was empty
// would resolve SOME credential in most environments, and act as whichever
// identity that variable happened to hold: not a failure, a silent
// substitution of identity. Refusing the empty token is the only behaviour
// that cannot do that.
package tracker

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// The capability vocabulary of this class.
//
// Each one is a measured difference between real trackers rather than a
// speculative flag, and each is declared in the connector's manifest AND
// reported by Capabilities(). A host reads the manifest before it loads
// anything, so an undeclared capability is one the host will never call.
//
// None of them is declaration-only: each has either an interface or a
// behavioural difference a conformance harness can observe. A capability
// whose only effect is to be declared cannot be checked against a running
// connector at all.
const (
	// CapNativeTypes declares that an item's TYPE is first-class on the
	// backend — a real attribute with its own vocabulary — rather than a
	// convention over labels.
	//
	// It is optional because the difference is real and load-bearing: one
	// tracker serves an item type the API can set and filter on, another has
	// only labels and leaves "Story" a string in a label list. [Item.Type] is
	// populated either way, because the methodology keys its status flow and
	// its required-field matrix on the type and cannot work without one — but
	// a host planning a type CHANGE needs to know whether it is writing an
	// attribute or rewriting a label set, and a connector without this
	// capability refuses a type write with cerr.KindUnsupported.
	//
	// That write is an ordinary [FieldClassAttribute] entry in [Change.Fields]
	// and there is no second surface for it: one field-write surface is the
	// whole of the rule, and a Change.Type would be that backend's
	// SetIssueType back as a struct field.
	CapNativeTypes connector.Capability = "native_types"

	// CapNativeHierarchy declares that parent/child between items is a real
	// link the backend maintains, via [Item.Parent], [Item.Children] and
	// [Change.Parent].
	//
	// It is optional because a tracker without it can only fake hierarchy —
	// a field naming a parent by text, a naming convention — and a faked
	// parent cannot be traversed, so rollup over children is not merely
	// slower there, it is unavailable. A connector that does not declare it
	// refuses [Change.Parent] with cerr.KindUnsupported rather than writing
	// something a reader cannot follow back.
	CapNativeHierarchy connector.Capability = "native_hierarchy"

	// CapCrossScope declares that one board spans several [Scope]s, and that
	// hierarchy may cross them: a child in one scope under a parent in
	// another.
	//
	// It is optional because it is the difference between a board that IS a
	// project and a board that is a view over many. Where it holds, an item's
	// scope is part of its identity and a parent's scope is its own — see
	// [ItemRef]. Where it does not, every item on the board shares one scope,
	// and an [ItemRef] naming another is cerr.KindInvalid.
	CapCrossScope connector.Capability = "cross_scope"

	// CapItemFields declares that the backend serves fields on the ITEM,
	// distinct from the fields the board carries about the item — two field
	// surfaces with two APIs and, usually, two administrative scopes.
	//
	// It is optional because most trackers have only one. Where both exist a
	// [Catalogue] reports [FieldClassItem] fields alongside
	// [FieldClassBoard] ones and the adapter routes writes by class; where
	// only one exists, no [FieldClassItem] field is ever reported, and a
	// declared-but-absent one is drift the schema check names.
	CapItemFields connector.Capability = "item_fields"

	// CapTrains declares that the backend's delivery buckets — milestones,
	// cycles, releases, whatever it calls them — are administrable through
	// this connector, via [TrainAdmin].
	//
	// It is optional because reading a train off an item ([Selection.Train])
	// is an ordinary read that every tracker serves, while CREATING one is
	// separately privileged on most and absent from some. A connector that
	// can read trains but not create them declares nothing here and still
	// populates the train on every item.
	CapTrains connector.Capability = "trains"

	// CapScopedTrains declares that trains are per-[Scope] rather than
	// per-tracker: the same train name is a DIFFERENT bucket in each scope,
	// and a board spanning eight scopes needs the name created eight times.
	//
	// This is the one place where a backend quirk had to become a capability
	// rather than a contract property. Milestones on one provider are
	// per-repository; cycles and projects on another are per-workspace. A
	// contract that assumed either would be unimplementable on the other, and
	// a host planning a train move must know whether a missing bucket in one
	// scope is a gap to close or a question that does not apply.
	//
	// It is not declaration-only: it changes what [TrainAdmin] accepts and
	// what its answers mean. With it, [TrainSpec.Scope] is required and
	// [TrainAdmin.ListTrains] answers per scope; without it, a non-empty
	// TrainSpec.Scope is cerr.KindInvalid and trains are reported under the
	// zero Scope, because they are not partitioned.
	CapScopedTrains connector.Capability = "scoped_trains"

	// CapSchemaAdmin declares that the board's own field schema can be
	// created and extended through this connector, via [SchemaAdmin].
	//
	// It is optional because it is the most privileged thing this class can
	// do and the least often wanted: a host that reads and writes items needs
	// nothing here, and a token scoped for item work will not carry it.
	CapSchemaAdmin connector.Capability = "schema_admin"
)

// knownCapabilities is the closed capability vocabulary of this class. A
// manifest declaring anything outside it fails conformance: a capability the
// host does not recognise is a capability the host will not use, and a typo is
// indistinguishable from a capability that has yet to ship.
var knownCapabilities = connector.Capabilities{
	CapNativeTypes, CapNativeHierarchy, CapCrossScope, CapItemFields,
	CapTrains, CapScopedTrains, CapSchemaAdmin,
}

// KnownCapabilities returns the closed capability vocabulary for this class,
// as a copy. Adding one is a deliberate contract change.
func KnownCapabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), knownCapabilities...)
}

// A Resolution is what a list operation in this contract returns: either a
// list the connector actually read from the tracker, or nothing at all.
//
// It is an ALIAS for the roster package's fail-closed list resolution, not a type of this
// package's own. The generic is published in the roster package because that
// class landed first, but the invariant it enforces is class-independent, and
// a second copy of it here would be a second thing to get wrong. Everything
// its documentation says holds verbatim for a list of tracker items:
//
//   - the zero Resolution — what `return Resolution[Item]{}, nil` produces —
//     is UNRESOLVED, and [Resolution.Items] refuses to read it. A failure path
//     therefore cannot hand back an empty list by accident;
//   - a board that genuinely holds nothing is expressible only by SAYING so,
//     with [EmptyList]. Emptiness is asserted, never inferred from a length;
//   - [Resolved] takes a [Completeness] on every call with no default, so a
//     pagination loop that broke off partway cannot be reported as a smaller
//     success. It must return a typed failure instead.
//
// That last one is the property a board makes concrete, and it is the reason
// this contract exists in the shape it does. A board scan is a paginated read
// of hundreds of items; an audit run against the first page and no error
// reports every item it never examined as compliant, and a rollup computed
// from a truncated child set reports a parent complete because it could not
// see the child that is not.
type Resolution[T any] = roster.Resolution[T]

// A Completeness is the connector's assertion about whether the list it hands
// to [Resolved] is everything the operation is meant to report. It is the
// roster package's type; see [Complete] and [Partial].
type Completeness = roster.Completeness

const (
	// Complete asserts the list is everything the operation is meant to
	// report — a read whose pagination ran to its own natural end.
	Complete = roster.Complete
	// Partial asserts the list is only what a read covered before it stopped.
	// [Resolved] refuses to build a readable Resolution from one: a truncated
	// read is a typed failure, never a smaller success.
	Partial = roster.Partial
)

// Resolved wraps a list the connector actually read, asserting its
// [Completeness]. It forwards to roster; the Completeness argument is
// mandatory there and is deliberately not defaulted here, because the whole
// value of the type is that no call site can assert a complete read by
// omission.
//
// It returns an unreadable Resolution and an error when items is empty (say so
// with [EmptyList] instead) or when c is not [Complete]. That error is the
// *roster.FaultError, because this is a forward and not a
// re-implementation; [CheckResolution] is where it becomes this contract's
// [FaultError], and a classification-only host sees
// cerr.KindContractViolation from either shape.
func Resolved[T any](items []T, c Completeness) (Resolution[T], error) {
	return roster.Resolved(items, c)
}

// EmptyList reports a tracker that genuinely, verifiably holds none of the
// requested thing — a board with no items, a scope with no trains. It forwards
// to roster.
//
// This is an ASSERTION, not a fallback. Call it only where the backend
// distinguishes "read successfully, found none" from "could not read": an HTTP
// 200 carrying an empty array is the first, and anything else is not.
func EmptyList[T any]() Resolution[T] { return roster.EmptyRoster[T]() }

// A Scope is one partition of a tracker's item space, as the provider serving
// it declares that partition: a repository on one, a team on another, a
// project on a third.
//
// It is deliberately an opaque name rather than a structured type. The
// contract does not know what a scope IS — that is the provider's fact, and
// the connector states it in [Catalogue.ScopeKind] so a host rendering a gap
// can name it. What the contract does know is that a scope plus a number is
// an item's identity and a number alone is not, which is why this appears in
// [ItemRef] and why no operation here takes a "repos" argument: repositories
// are one provider's spelling of this, not the concept.
type Scope string

// A BoardRef is the fully-qualified address of one board.
//
// All three components are load-bearing, and a partial address is refused
// ([BoardRef.Valid]). The estate this class was designed for runs two boards
// on one provider plus a tracker on another, so neither a board identifier nor
// an (owner, number) pair can route a decision: the first collides across
// providers, the second across instances of one provider. An address that
// cannot be compared is an address a cross-tracker refusal cannot enforce.
type BoardRef struct {
	// Provider names the backend family — the connector's own name for what
	// it talks to, matching its manifest.
	Provider string
	// Instance names WHICH deployment or account of that provider: an
	// organisation, a workspace, a self-hosted host name. Two instances of
	// one provider is the ordinary case, not an exotic one.
	Instance string
	// Board names the board within the instance, as a string because
	// providers spell it differently — a number on one, a key on another —
	// and a caller only ever passes it back.
	Board string
}

// Valid reports whether b is a fully-qualified address. A BoardRef missing any
// component names no board, and every operation refuses one with
// cerr.KindInvalid rather than guessing the rest from context.
func (b BoardRef) Valid() bool {
	return b.Provider != "" && b.Instance != "" && b.Board != ""
}

// String renders the address as provider/instance/board, for a message an
// operator reads.
func (b BoardRef) String() string { return b.Provider + "/" + b.Instance + "/" + b.Board }

// An ItemRef is the fully-qualified address of one item.
//
// (scope, number) is the item's identity within a board, and the [BoardRef] is
// carried with it because two boards can hold the same number: a parent
// reference that compared equal across trackers would let a cross-tracker link
// through the pre-network refusal that exists to stop exactly that. Every
// field is comparable, so that refusal is a comparison rather than a
// convention.
//
// A board item that is only a draft — no scope and no number — has no ItemRef
// at all. That is a fact about the item rather than a gap: it cannot be
// addressed, read individually or parented, and [ItemRef.Valid] is how a
// connector says so.
type ItemRef struct {
	// Board is the board the item lives on, fully qualified.
	Board BoardRef
	// Scope is the partition the item belongs to, in the provider's own
	// terms. A parent's scope is its OWN, always: a child in one scope under
	// a parent in another is ordinary on a board that spans scopes.
	Scope Scope
	// Number is the item's number within its scope. It is an int because
	// every tracker this contract targets numbers items within a partition;
	// it is never an identity on its own.
	Number int
}

// Valid reports whether r addresses an item: a fully-qualified board, a scope,
// and a positive number.
func (r ItemRef) Valid() bool { return r.Board.Valid() && r.Scope != "" && r.Number > 0 }

// String renders the address as provider/instance/board:scope#number.
func (r ItemRef) String() string {
	return r.Board.String() + ":" + string(r.Scope) + "#" + strconv.Itoa(r.Number)
}

// A FieldClass says WHERE a field lives, which is the only thing that decides
// which of a backend's APIs a write to it takes.
//
// It is an integer type whose zero value means "nothing was said" rather than
// naming one of the classes, because a field whose class was never established
// cannot be routed, and routing it to a default is how a write lands in the
// wrong place and reports success. A caller never reads this: the connector
// reports it in a [Catalogue] and the adapter dispatches on it.
type FieldClass int

const (
	// FieldClassUnspecified is the zero value: no class was established. It
	// is refused with cerr.KindInvalid, never defaulted — see [FieldClass].
	FieldClassUnspecified FieldClass = iota
	// FieldClassBoard is a field the BOARD carries about an item: it exists
	// because the item is on this board, and it disappears with the board.
	FieldClassBoard
	// FieldClassItem is a field on the ITEM itself, which the item keeps
	// whichever boards it is on. Only a connector declaring
	// [CapItemFields] ever reports one.
	FieldClassItem
	// FieldClassAttribute is a first-class attribute of the item that the
	// backend serves by name: on the tracker this class was designed against,
	// its TYPE, its labels, its assignees, its train and its parent.
	//
	// The item's type is in this class and is written like any other field, by
	// NAME, through [Change.Fields]. That is the one-write-surface rule, and it
	// is the reason there is one field-write surface rather than six methods:
	// the abstract backend this replaces published SetIssueType beside
	// SetLabels, SetAssignees and SetMilestone and had to encode the routing
	// rule in prose, and prose is what gets lost in an extraction. A
	// Change.Type would be that method back as a struct field.
	//
	// PARENT and open/closed LIFECYCLE are named as backend attributes here and
	// are still not addressable as fields, for a reason that is about the
	// carrier rather than about routing: a [Value] cannot hold an item
	// reference or a close reason. [Change.Parent] and [Change.Lifecycle] are
	// their surfaces.
	FieldClassAttribute
)

var fieldClassNames = map[FieldClass]string{
	FieldClassUnspecified: "unspecified",
	FieldClassBoard:       "board",
	FieldClassItem:        "item",
	FieldClassAttribute:   "attribute",
}

// Valid reports whether c names a class in the closed vocabulary.
// FieldClassUnspecified is valid as a value and still refused as a routing
// decision — see [FieldClass.Routable].
func (c FieldClass) Valid() bool {
	_, ok := fieldClassNames[c]
	return ok
}

// Routable reports whether c can decide which API a write takes: a class in
// the vocabulary that is not [FieldClassUnspecified].
func (c FieldClass) Routable() bool { return c.Valid() && c != FieldClassUnspecified }

// String renders c's stable name. A value outside the vocabulary renders as
// invalid_field_class(N) rather than as any real class, so a message built
// from it names what was actually passed.
func (c FieldClass) String() string {
	if name, ok := fieldClassNames[c]; ok {
		return name
	}
	return "invalid_field_class(" + strconv.Itoa(int(c)) + ")"
}

// A ValueKind says which of a [Value]'s carriers holds the value, and it is
// the whole of a value's type information.
//
// Its zero value is refused for the reason [ValueUnset] exists: a caller that
// said nothing did not mean "clear this field", and a contract in which those
// two are the same value clears fields nobody asked to clear.
type ValueKind int

const (
	// ValueUnspecified is the zero value: nothing was said. It is refused
	// with cerr.KindInvalid — see [ValueKind].
	ValueUnspecified ValueKind = iota
	// ValueUnset CLEARS the field. It is deliberately distinct from empty
	// text: on at least one backend the two are different API calls, and on
	// every backend they are different facts — an empty string is a value, an
	// unset field has none.
	ValueUnset
	// ValueText carries [Value.Text].
	ValueText
	// ValueNumber carries [Value.Number].
	ValueNumber
	// ValueDate carries [Value.Date].
	ValueDate
	// ValueOption carries [Value.Option]: one option of a single-select
	// field, by its NAME. Never by an identity — see the package doc.
	ValueOption
	// ValueUsers carries [Value.Names] as principals: assignees, reviewers.
	// It is distinct from [ValueNames] because a principal is resolved
	// against the tracker's directory and a label is validated against the
	// board's own set — same carrier, two resolutions, and a connector that
	// confused them would silently create labels named after people.
	ValueUsers
	// ValueNames carries [Value.Names] as names of things on the board:
	// labels, most often. A name the board does not already carry is
	// cerr.KindInvalid, never an auto-creation.
	ValueNames
)

var valueKindNames = map[ValueKind]string{
	ValueUnspecified: "unspecified",
	ValueUnset:       "unset",
	ValueText:        "text",
	ValueNumber:      "number",
	ValueDate:        "date",
	ValueOption:      "option",
	ValueUsers:       "users",
	ValueNames:       "names",
}

// Valid reports whether k names a kind in the closed vocabulary.
func (k ValueKind) Valid() bool {
	_, ok := valueKindNames[k]
	return ok
}

// Writable reports whether k can be written: a kind in the vocabulary that is
// not [ValueUnspecified]. A connector refuses anything else with
// cerr.KindInvalid before any network call.
func (k ValueKind) Writable() bool { return k.Valid() && k != ValueUnspecified }

// String renders k's stable name. A value outside the vocabulary renders as
// invalid_value_kind(N) rather than as any real kind.
func (k ValueKind) String() string {
	if name, ok := valueKindNames[k]; ok {
		return name
	}
	return "invalid_value_kind(" + strconv.Itoa(int(k)) + ")"
}

// A Value is one field value, in whichever carrier its [ValueKind] names.
//
// A connector reads ONLY the carrier Kind names and ignores the rest, so a
// stray Text on a [ValueOption] is not a second fact about the value — it is
// nothing at all. The union is flat rather than an interface because these
// values are built and compared in bulk, and because a nil interface would
// reintroduce the "nothing was said" ambiguity [ValueUnspecified] removes.
type Value struct {
	// Kind names the carrier. [ValueUnspecified] is refused.
	Kind ValueKind
	// Text carries [ValueText].
	Text string
	// Number carries [ValueNumber].
	Number float64
	// Date carries [ValueDate].
	Date Date
	// Option carries [ValueOption]: the option's NAME.
	Option string
	// Names carries [ValueUsers] and [ValueNames]. It REPLACES the field's
	// current contents rather than adding to them: a set-valued field written
	// with a shorter list is a removal, which is why a caller assembles the
	// whole set and why [ValueUnset] is how an empty one is said.
	Names []string
}

// A Date is a calendar date: a target date, a start date, a train's due date.
//
// It is not a time.Time, and it is not a dependency. A time.Time carries a
// clock reading and a zone, and both are wrong here rather than merely
// unnecessary: "target 2026-07-31" is the same date in every zone, while a
// midnight-in-UTC timestamp rendered in a zone behind UTC is the day before.
// The shape is deliberately the same as the widely-used civil date type — year,
// month, day — so adopting one later is a type swap rather than a rewrite, but
// three ints do not justify a module.
//
// The zero Date is not a date. A field with no date is expressed with
// [ValueUnset], never with a zero one.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// Valid reports whether d is a date that exists. It is the one check worth
// making before a network call: a backend asked to set the 31st of June
// answers with a validation failure that names its own API rather than the
// caller's mistake.
func (d Date) Valid() bool {
	t := time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
	return t.Year() == d.Year && t.Month() == d.Month && t.Day() == d.Day
}

// String renders d as an ISO 8601 calendar date (YYYY-MM-DD).
func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day) }

// A Selection says how much of each item a read carries.
//
// It narrows the PAYLOAD and never how far the read goes: a scan with
// everything switched off still pages the board to exhaustion. Its purpose is
// cost — a rollup pass reads fields and hierarchy and has no use for labels —
// and every field is an opt-in, so the zero Selection is a legitimate, cheap
// read of each item's own record.
//
// A field excluded by a Selection is absent from [Item.Fields] and is NOT in
// [Item.Unread]: it was not asked for, which is a third state and not the same
// as unread or unset. [Item.Selected] is what carries that distinction with
// the item, so an item that travels away from the call that read it does not
// lose it.
type Selection struct {
	// BoardFields includes the [FieldClassBoard] field values.
	BoardFields bool
	// ItemFields includes the [FieldClassItem] field values. A connector
	// without [CapItemFields] has none to include.
	ItemFields bool
	// Train includes the item's delivery bucket.
	Train bool
	// Labels includes the item's labels.
	Labels bool
	// Assignees includes the item's assignees.
	Assignees bool
	// Children includes [Item.Children], paged to exhaustion. A short child
	// set is not a smaller answer: a rollup computed from one reports a
	// parent complete because it could not see the child that is not, so a
	// child-set read that fails sets [Item.ChildrenErr] and leaves Children
	// nil.
	Children bool
}

// An Item is one item on the board, as the tracker holds it.
type Item struct {
	// Ref is the item's fully-qualified address.
	Ref ItemRef
	// Title is the item's title.
	Title string
	// Type is the backend's own NAME for the item's type. It is promoted out
	// of [Item.Fields] because the methodology keys its status flow and its
	// required-field matrix on it, and because a connector without
	// [CapNativeTypes] must still answer it — there, from whatever
	// convention the backend does carry.
	Type string
	// Open is whether the tracker considers the item open.
	Open bool
	// Parent is the item's parent, addressed in the parent's OWN scope.
	// Cross-scope hierarchy is ordinary on a board that spans scopes.
	Parent *ItemRef
	// Fields carries every field value the [Selection] asked for, from ALL
	// THREE field classes, merged and keyed by field NAME. A name absent from
	// this map is unset — unless it is in [Item.Unread], or was not selected.
	Fields map[string]Value
	// Children are the item's children, present only when
	// [Selection.Children] asked for them and the read succeeded.
	Children []ItemRef

	// Selected is the [Selection] this item was read under. It travels with
	// the item so that a later reader can tell "not asked for" from "unset"
	// without knowing which call produced it.
	//
	// Its zero value is a LEGITIMATE read — the cheap Selection with every
	// opt-in off — and is therefore indistinguishable, in the type alone, from
	// an item whose Selected was never populated. That is the conflation this
	// field was added to remove, reappearing one level down, and the type
	// cannot close it: a presence marker would be either a second field nobody
	// reads or a pointer every reader must nil-check, and this contract admits
	// no pointer a caller must nil-check (see [Tracker]).
	//
	// It is closed by CONFORMANCE instead, and that is the deliberate choice
	// rather than an omission. The conformance harness must assert that every Item a read
	// returns carries a Selected EQUAL to the Selection that read was given —
	// for the zero Selection as much as for a populated one, because the zero
	// one is exactly the case a connector that never sets the field would pass
	// by accident. A connector that leaves it zero under a non-zero Selection
	// fails that check, so on a conformant connector a zero Selected is the
	// read's own answer and not the absence of one.
	Selected Selection

	// Unread names the fields whose read FAILED. A name here means the value
	// is unknown; its absence from [Item.Fields] means it is unset. Those are
	// different facts and they do not share a representation, because an
	// audit that read unknown as unset reports an item compliant on evidence
	// it never had.
	Unread []string

	// ChildrenErr is non-nil when the child-set read failed. Then
	// [Item.Children] is nil and MEANINGLESS — not empty. A genuine childless
	// item has a nil ChildrenErr and an empty Children, and the two states
	// must be observably different for any rollup over them to mean anything.
	ChildrenErr error
}

// A Draft is one item to file.
type Draft struct {
	// Scope is where the item is filed, in the provider's own terms.
	Scope Scope
	// Title is required.
	Title string
	// Body is the item's description.
	Body string
	// Fields are the field values to set at filing, keyed by field NAME,
	// across all three classes. A name the [Catalogue] does not carry is
	// cerr.KindInvalid before any network call.
	//
	// The item's TYPE is one of them — a [FieldClassAttribute] field like any
	// other — and it is REQUIRED at filing: an item filed without one cannot be
	// checked against the methodology's per-type rules, and a type applied
	// afterwards is a second write that can fail on its own. One write surface,
	// so exactly one place it can be said.
	Fields map[string]Value
	// Parent is the parent to file the item under, or nil for none. A parent
	// on another board is cerr.KindInvalid before any network call.
	Parent *ItemRef
}

// A Change is one item's worth of change.
//
// Every part of it is optional except the target, and each has exactly one
// spelling for "leave this alone", so a change assembled field by field cannot
// silently assert something nobody set.
type Change struct {
	// Target is the item to change.
	Target ItemRef
	// Fields are the values to write, keyed by field NAME, across all three
	// classes: the connector routes each name by the class the [Catalogue]
	// reports for it. A [ValueUnset] clears the field; a
	// [ValueUnspecified] is cerr.KindInvalid rather than a clear.
	//
	// This is the ONLY field-write surface in the contract, and the item's TYPE
	// goes through it like anything else — see [FieldClassAttribute]. A caller
	// cannot pick the wrong API because a caller does not pick an API.
	Fields map[string]Value
	// Parent re-parents the item: nil leaves it alone, and a pointer to the
	// zero ItemRef DETACHES it. A parent on another board is
	// cerr.KindInvalid before any network call — with several trackers live
	// at once that is an ordinary mistake rather than an exotic one, which is
	// why the refusal is unconditional and pre-network.
	Parent *ItemRef
	// Lifecycle closes or reopens the item. nil leaves it alone.
	Lifecycle *Lifecycle
}

// A Lifecycle is a close or a reopen, with the reason and the audit trail that
// belong to it.
type Lifecycle struct {
	// Close closes the item; false on a non-nil Lifecycle reopens it.
	Close bool
	// Reason is why the item is being closed, and it is REQUIRED when Close
	// is true: a tracker that distinguishes completed from abandoned work
	// reports the wrong figure for everything downstream if the distinction
	// is left to a default. It must be [CloseReasonUnspecified] on a reopen.
	Reason CloseReason
	// AuditComment is posted BEFORE the close, in that order, so that an item
	// is never closed with the explanation still in flight. An empty comment
	// posts nothing.
	AuditComment string
}

// A CloseReason is why an item was closed. It is a closed vocabulary because
// the backends' own sets are closed, and a reason outside it is rejected by
// the backend with a message naming its API rather than the caller's mistake.
type CloseReason int

const (
	// CloseReasonUnspecified is the zero value: no reason was given. It is
	// refused on a close and required on a reopen — see [Lifecycle.Reason].
	CloseReasonUnspecified CloseReason = iota
	// CloseReasonCompleted is work that was done.
	CloseReasonCompleted
	// CloseReasonCanceled is work that was closed without being done —
	// spelled not_planned by one provider and canceled by another, one fact
	// either way.
	CloseReasonCanceled
)

// closeReasonNames is the single source of truth for the closed CloseReason
// vocabulary: [CloseReasons], [CloseReason.Valid] and [CloseReason.String] all
// derive from it, so a reason cannot be half-added.
var closeReasonNames = map[CloseReason]string{
	CloseReasonUnspecified: "unspecified",
	CloseReasonCompleted:   "completed",
	CloseReasonCanceled:    "canceled",
}

var closeReasons = func() []CloseReason {
	out := make([]CloseReason, 0, len(closeReasonNames))
	for r := range closeReasonNames {
		out = append(out, r)
	}
	slices.Sort(out)
	return out
}()

// CloseReasons returns the closed CloseReason vocabulary, in ascending order,
// as a copy.
func CloseReasons() []CloseReason { return slices.Clone(closeReasons) }

// Valid reports whether r names a reason in the closed vocabulary.
// CloseReasonUnspecified is valid as a value and still refused as a close
// reason — see [CloseReason.UsableAsReason].
func (r CloseReason) Valid() bool {
	_, ok := closeReasonNames[r]
	return ok
}

// UsableAsReason reports whether r can close an item: a reason in the
// vocabulary that is not [CloseReasonUnspecified].
func (r CloseReason) UsableAsReason() bool { return r.Valid() && r != CloseReasonUnspecified }

// String renders r's stable name. A value outside the vocabulary renders as
// invalid_close_reason(N) rather than as any real reason.
func (r CloseReason) String() string {
	if name, ok := closeReasonNames[r]; ok {
		return name
	}
	return "invalid_close_reason(" + strconv.Itoa(int(r)) + ")"
}

// An ApplyReport is what [Tracker.Apply] reports about a change set.
//
// Apply is NOT transactional, and this type says so rather than leaving a
// caller to hope. Its arithmetic is the contract: every change is either
// applied or attributed in [ApplyReport.Failed], and a change listed nowhere
// was APPLIED. That default is what makes [CheckApplyReport] worth calling —
// a report that does not add up converts a failure into a success.
type ApplyReport struct {
	// Requested is DEMAND: how many changes were asked for, counted before
	// anything was attempted and across every batch the connector split them
	// into. It is always ≥ Applied, and the gap is the point — a report where
	// the two are equal by construction cannot show a loss.
	//
	// It is the connector's CLAIM about the demand, not the demand itself, so
	// [CheckApplyReport] takes the real figure separately and checks this
	// against it. A report checked only against itself makes the zero report
	// clean, and a connector that dropped every change would pass.
	Requested int
	// Applied is how many changes the backend accepted.
	Applied int
	// Failed maps the INDEX of a change in the requested set to its
	// classified failure. Every entry carries a non-nil error: an index
	// listed with no reason is a change whose outcome nobody knows, which is
	// the one thing this report may not say.
	Failed map[int]error
}

// A Catalogue resolves everything a board addresses by NAME to what the
// backend needs to address it, and reports what the backend actually serves.
//
// It carries no backend identity — see the package doc — so what crosses this
// boundary is names, classes and vocabularies. It is valid for ONE call chain
// and MUST NOT be stored: a single-select edit regenerates every option
// identity behind it, and a schema change run in the same process invalidates
// it mid-flight.
//
// It is returned as a value with an error rather than as a [Resolution]
// because there is no such thing as a partial catalogue: a scope the backend
// would not answer for is a typed failure of the whole call, never a smaller
// catalogue that a later check reads as "that scope has no labels".
type Catalogue struct {
	// Board is the board this catalogue was read for. It is carried so that a
	// caller holding catalogues for two boards of one provider cannot check
	// one against the other's schema.
	Board BoardRef
	// ScopeKind is the provider's own word for what a [Scope] is —
	// "repository", "team", "project". This is where a scope's meaning is
	// DECLARED: the contract deliberately does not know what a scope is, and
	// a host naming a missing one to an operator has to.
	ScopeKind string
	// Fields is every addressable field, as the backend actually serves it.
	// A declared class that disagrees with the class reported here is
	// cerr.KindInvalid naming both, which turns a silent no-op into a loud
	// startup failure.
	Fields []Field
	// Types are the item type names the backend serves.
	Types []string
	// Labels are the label names available in each scope. A label a caller
	// writes must already be here: this contract has no auto-creation, and a
	// typo that created a label is a typo nobody notices.
	Labels map[Scope][]string
	// Trains are the delivery buckets available in each scope. A connector
	// without [CapScopedTrains] reports them under the zero Scope, because
	// they are not partitioned.
	Trains map[Scope][]Train
}

// A Field is one addressable field, as the backend serves it.
type Field struct {
	// Name is the backend's own name for the field, and the only handle a
	// caller ever uses.
	Name string
	// Class is where the field lives, which is how a write to it is routed.
	Class FieldClass
	// Accepts is the [ValueKind] the field takes. It is reported so that
	// declared-versus-live checking can see a field whose TYPE changed —
	// the destructive drift that cannot be applied and must be reported with
	// its recovery recipe instead.
	Accepts ValueKind
	// Options are the option NAMES of a single-select field, in the
	// backend's own order, and empty for every other kind.
	Options []string
}

// A Train is one delivery bucket — a milestone, a cycle, a release.
//
// The name is the only handle: a bucket's number or identity would be a
// cached identity in the one place this contract most needs not to have one,
// since a train is addressed on every write that moves work between releases.
type Train struct {
	// Name is the bucket's name, as the backend holds it.
	Name string
	// Open is whether the bucket still accepts work. A closed train is still
	// a train: listing only the open ones reports a scope as missing a train
	// it has, and an applier then fails on the create that follows.
	Open bool
}

// Tracker is the connector-class contract.
//
// Every failure it returns is typed: a *cerr.Error whose Kind comes from
// cerr's closed vocabulary, so a host acts on the classification and never on
// the message. A failure the connector cannot classify is cerr.KindUnknown,
// which fails the call and escalates nothing.
//
// No list operation can report a success carrying no list — see [Resolution].
// No operation returns a pointer a caller must nil-check for a single item: a
// (*Item, nil) return is the same conflation in another shape, which is why
// the item reads return a [Resolution] of items even for one.
//
// Every operation takes a [BoardRef] and refuses one that is not fully
// qualified. One connector serves one tracker, so the board argument is not
// routing between backends — it is the connector checking that the caller
// means the board it actually holds, which is what stops a change assembled
// for one board from being applied to another.
//
// Optional operations live behind capabilities rather than in this interface:
// [TrainAdmin] behind [CapTrains], [SchemaAdmin] behind [CapSchemaAdmin]. A
// host type-asserts for them, and a conformance run fails a connector that
// declares one without implementing it — in both directions.
type Tracker interface {
	connector.Connector

	// Catalogue resolves everything the board addresses by NAME to whatever
	// the backend addresses it by, in as few round trips as the backend
	// allows, for the given scopes.
	//
	// The result is valid for THIS call chain only and MUST NOT be stored: a
	// single-select edit regenerates every option identity behind it. An
	// empty scopes list asks about the board itself and whatever the backend
	// serves without a scope.
	Catalogue(ctx context.Context, board BoardRef, scopes []Scope) (Catalogue, error)

	// Scan returns every item on the board, paging to exhaustion.
	//
	// A read that stopped partway is a typed failure, never a shorter list.
	// sel narrows what each item carries; it never narrows how far the read
	// goes.
	Scan(ctx context.Context, board BoardRef, sel Selection) (Resolution[Item], error)

	// GetItems reads the named items in full.
	//
	// An item that does not exist, or exists but is not on this board, is
	// cerr.KindNotFound — never a silently omitted entry, because a caller
	// that asked about five items and got four has no way to learn which
	// question went unanswered.
	GetItems(ctx context.Context, board BoardRef, refs []ItemRef, sel Selection) (Resolution[Item], error)

	// Create files items and returns them AS RE-READ after filing, never as
	// constructed from the request.
	//
	// Filing is several backend operations and the last of them is the
	// verification: an item echoed back from the request would report a type,
	// a parent and a field set that nothing confirmed had landed.
	Create(ctx context.Context, board BoardRef, drafts []Draft) (Resolution[Item], error)

	// Apply executes a change set and reports what happened to each change.
	//
	// It is NOT transactional and says so: the report attributes every change
	// individually, and a change not listed as failed was applied. An error
	// the connector cannot attribute to a change is a hard failure of the
	// whole call, never a partial success — folding an unattributable failure
	// into the report would say "everything else was applied" about changes
	// nobody can account for.
	Apply(ctx context.Context, board BoardRef, changes []Change) (ApplyReport, error)
}

// TrainAdmin is the optional operation group behind [CapTrains]:
// read and create the backend's delivery buckets.
//
// A connector that can do this does two things, and must do both: implement
// this interface, and declare [CapTrains] in its manifest and from
// Capabilities(). Declaring without implementing is worse than declaring
// nothing — the host plans a train it can never create.
type TrainAdmin interface {
	// ListTrains returns the delivery buckets of each scope, open and closed
	// both.
	//
	// A scope that could not be read comes back with [ScopeTrains.Err] set,
	// never as a scope with no trains: the set is a UNION over scopes, so the
	// less of it a caller can see the greener a replan looks.
	ListTrains(ctx context.Context, scopes []Scope) (Resolution[ScopeTrains], error)

	// CreateTrains creates the named buckets and reports what happened to
	// each spec, with the same attribution rules as [Tracker.Apply].
	//
	// A create must be verified by RE-READING: a create loop that iterated
	// once still returns successfully for every name it was given, and the
	// count is the only thing that shows it.
	CreateTrains(ctx context.Context, specs []TrainSpec) (ApplyReport, error)
}

// ScopeTrains is one scope's delivery buckets, or the reason they are unknown.
type ScopeTrains struct {
	// Scope is the scope this entry is about. A connector without
	// [CapScopedTrains] reports the zero Scope.
	Scope Scope
	// Trains are the buckets in that scope. It is nil and MEANINGLESS when
	// Err is non-nil.
	Trains []Train
	// Err is non-nil when this scope could not be read. Unknown dominates:
	// a caller must not read this entry as a scope with no trains, because
	// the answer it would compute is "nothing to create here".
	Err error
}

// A TrainSpec is one delivery bucket to create.
type TrainSpec struct {
	// Scope is where to create it. It is required on a connector declaring
	// [CapScopedTrains] and must be empty on one that does not.
	Scope Scope
	// Name is the bucket's name.
	Name string
	// Description is optional prose.
	Description string
	// Due is the bucket's due date, or the zero [Date] for none.
	Due Date
}

// SchemaAdmin is the optional operation behind [CapSchemaAdmin]: create and
// extend the board's own field schema.
type SchemaAdmin interface {
	// EnsureFields creates missing fields and EXTENDS option sets.
	//
	// Any change that would regenerate option identities — narrowing an
	// option set, renaming an option, changing a field's kind — is REFUSED
	// and returned in the plan with the snapshot→mutate→requery→restore→verify
	// recipe. It is never applied, not even behind a flag: the identities it
	// regenerates are held by everything that read the board before it, and
	// there is no way to make that safe from inside one call.
	EnsureFields(ctx context.Context, board BoardRef, want []FieldSpec) (SchemaPlan, error)
}

// A FieldSpec is one field the board should carry.
type FieldSpec struct {
	// Name is the field's name.
	Name string
	// Class is where the field should live.
	Class FieldClass
	// Accepts is the [ValueKind] the field should take.
	Accepts ValueKind
	// Options are the option names a single-select field should carry. They
	// may be EXTENDED and never narrowed — see [SchemaAdmin.EnsureFields].
	Options []string
}

// A SchemaPlan is what [SchemaAdmin.EnsureFields] did, and what it refused.
type SchemaPlan struct {
	// Created names the fields that were created.
	Created []string
	// Extended names the options that were added, per field.
	Extended []FieldChange
	// Refused are the changes that were not made, each with its recovery
	// recipe. A plan with refusals is a successful call: the refusal IS the
	// answer, and a caller that treated it as a failure would retry it.
	Refused []Refusal
}

// A FieldChange is one field's added options.
type FieldChange struct {
	// Field is the field's name.
	Field string
	// Options are the option names that were added, in the order they were
	// added, so a message built from it reads the same way twice.
	Options []string
}

// A Refusal is a schema change that was not applied, and how to apply it
// safely by hand.
type Refusal struct {
	// Field is the field the change was for.
	Field string
	// Reason says what was asked and why it cannot be done in one call.
	Reason string
	// Recipe is the operator-run sequence that achieves it safely:
	// snapshot the affected values, mutate the schema, requery the new
	// identities, restore the values, verify by re-reading.
	Recipe string
}
