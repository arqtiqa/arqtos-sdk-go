package gqlcheck

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// These tests exercise this package against SMALL schemas of their own rather
// than against a real vendor schema — GitHub's is 1.5 MB — so that each of them
// names the property it pins and none of them depends on what a backend happens
// to serve this month.
//
// ⚠️ That is a deliberate split and not a gap: a caller pins the real schema it
// vendors and drives its own documents through this package in its own CI, which
// is where the real schema and the real documents meet. This module ships no
// vendor schema, so it cannot be that place.

// fixtureSDL carries, deliberately, the trap that broke the first version of the
// caller that drives this: a root field called `nodes`. GitHub's Query type really
// has one, so a connection's node selection — `nodes { name }` — parses as a ROOT
// selection and would then be reported for the `ids` argument no real document
// ever needed. See suppliesRequiredArgs for the reasoning that answer forced, and
// TestValidateRootSelection_AFragmentSharingARootFieldsNameIsNotThatRootField for
// the falsifier that keeps it from coming back.
const fixtureSDL = `
type Query {
  nodes(ids: [ID!]!): [Node]!
  organization(login: String!): Organization
}
type Mutation {
  touch(id: ID!): Boolean
}
interface Node { id: ID! }
type Organization implements Node {
  id: ID!
  issueFields(first: Int!, after: String): IssueFieldConnection!
}
type IssueFieldConnection {
  totalCount: Int!
  pageInfo: PageInfo!
  nodes: [IssueFieldCommon]
}
type PageInfo { hasNextPage: Boolean! endCursor: String }
interface IssueFieldCommon {
  name: String!
  dataType: String!
  description: String
}
type IssueFieldSingleSelect implements IssueFieldCommon {
  name: String!
  dataType: String!
  description: String
  options: [Option!]!
}
type Option { name: String! }
`

func fixtureSchema(t *testing.T) *Schema {
	t.Helper()
	s, err := LoadSchema("fixture", fixtureSDL)
	if err != nil {
		t.Fatalf("loading the fixture schema: %v", err)
	}
	return s
}

func document(leaves string) string {
	return `query($org: String!, $first: Int!) {
  organization(login: $org) {
    issueFields(first: $first) {
      nodes { ... on IssueFieldCommon { ` + leaves + ` } }
    }
  }
}`
}

// TestValidateDocument_AFieldTheTypeDoesNotHaveIsInvalidAndTheFindingCarriesThePath
// is the shape of the defect this package was built for: a leaf asked for on an
// interface that has no such field, which the backend rejects and a fixture server
// answers.
//
// The path assertion is half the point. "Cannot query field id" over a document
// with four inline fragments in it does not say which one, and a bare "invalid"
// over ten constants is what this whole gate exists to be better than.
func TestValidateDocument_AFieldTheTypeDoesNotHaveIsInvalidAndTheFindingCarriesThePath(t *testing.T) {
	r := fixtureSchema(t).ValidateDocument("d", document("name dataType id"))
	if r.Verdict != Invalid {
		t.Fatalf("a document asking for a field the interface does not have is %q, want %q:\n%s",
			r.Verdict, Invalid, r)
	}
	const want = "organization > issueFields > nodes > ... on IssueFieldCommon > id"
	var found bool
	for _, f := range r.Findings {
		if f.Path == want && f.Rule == "FieldsOnCorrectType" {
			found = true
		}
	}
	if !found {
		t.Errorf("no finding carries the rule FieldsOnCorrectType at path %q, so a reader is told a document is "+
			"broken and not where:\n%s", want, r)
	}
}

// TestValidateDocument_AnUnparseableDocumentIsUnknownAndNeverValid is the
// unknown-is-never-clean rule.
//
// ⚠️ It is the rule that decides whether a real defect ships. A document the
// checker could not read is one it has said nothing about, and reporting it clean
// converts "nobody looked" into "nothing wrong" — which is the exact state the
// defect this package exists for shipped behind a green suite.
func TestValidateDocument_AnUnparseableDocumentIsUnknownAndNeverValid(t *testing.T) {
	s := fixtureSchema(t)
	for _, row := range []struct {
		name string
		doc  string
	}{
		{"an unclosed selection set", `query { organization(login: "x") { issueFields(first: 1) {`},
		{"a stray brace", `query { organization(login: "x") {{ } }`},
		{"an unterminated string", `query { organization(login: "x) { id } }`},
		{"empty", ``},
		{"not GraphQL at all", `this is prose, not a document`},
	} {
		t.Run(row.name, func(t *testing.T) {
			r := s.ValidateDocument(row.name, row.doc)
			if r.Verdict != Unknown {
				t.Errorf("%s is reported %q, want %q — a checker that cannot parse a document has checked "+
					"nothing about it:\n%s", row.name, r.Verdict, Unknown, r)
			}
			if r.OK() {
				t.Errorf("%s reports OK on an unknown verdict, so every gate built on this passes on documents "+
					"nothing checked", row.name)
			}
			if r.Why == "" {
				t.Errorf("%s is unknown with no reason, which tells an author nothing to act on", row.name)
			}
		})
	}
}

// TestValidateDocument_AnAliasIsCheckedAgainstTheFieldItNamesNotTheResponseKey
// pins the half of GraphQL a text check gets backwards.
//
// The two documents differ ONLY in the field the alias names. A checker reading
// the response key passes both; a checker reading the field passes one.
func TestValidateDocument_AnAliasIsCheckedAgainstTheFieldItNamesNotTheResponseKey(t *testing.T) {
	s := fixtureSchema(t)

	ok := s.ValidateDocument("real-field", document("desc: description"))
	if !ok.OK() {
		t.Errorf("`desc: description` was rejected and IssueFieldCommon.description exists. The response key is "+
			"the caller's to choose and the schema has no opinion about it:\n%s", ok)
	}

	bad := s.ValidateDocument("missing-field", document("desc: nosuchleaf"))
	if bad.Verdict != Invalid {
		t.Fatalf("`desc: nosuchleaf` is %q, want %q. Same response key, different field — a checker that passes "+
			"both is reading the key:\n%s", bad.Verdict, Invalid, bad)
	}
	var found bool
	for _, f := range bad.Findings {
		if strings.Contains(f.Message, "nosuchleaf") && strings.HasSuffix(f.Path, "desc: nosuchleaf") {
			found = true
		}
	}
	if !found {
		t.Errorf("the finding does not carry `nosuchleaf` in both the message and the path, so a reader cannot "+
			"tell which half of the alias the schema objected to:\n%s", bad)
	}
}

// TestValidateRootSelection_DeclaresTheVariablesTheSchemaSaysTheArgumentsTake
// covers the wrapper the alias-assembled selections are validated in.
//
// A selection carrying `first: $first` is not a document, and wrapping it in
// `query { … }` makes every variable in it undefined. The declarations are
// INFERRED from the schema's own argument types rather than guessed, so the
// wrapper cannot be a second opinion about the document it is wrapping.
func TestValidateRootSelection_DeclaresTheVariablesTheSchemaSaysTheArgumentsTake(t *testing.T) {
	s := fixtureSchema(t)
	sel := `organization(login: $org) {
    issueFields(first: $first, after: $after) { totalCount nodes { ... on IssueFieldCommon { name } } }
  }`
	r := s.ValidateRootSelection("selection", sel)
	if !r.OK() {
		t.Fatalf("a legal root selection was rejected:\n%s", r)
	}
	// The wrapper is checked directly, because a wrapper that declared the wrong
	// types would still validate this particular selection and would report a
	// fault it invented on the next one.
	for _, want := range []string{"$after: String", "$first: Int!", "$org: String!"} {
		if !strings.Contains(r.Checked, want) {
			t.Errorf("the wrapper does not declare %s; it is:\n%s", want, r.Checked)
		}
	}
}

// TestValidateRootSelection_AVariableTheSchemaCannotTypeIsUnknownNotAGuess is the
// fail-closed leg of the wrapper.
//
// `$x` sits on an argument the schema does not define, so nothing can say what
// type to declare it as. Declaring `String` and hoping would report a fault this
// package invented on every selection using it — so the verdict is unknown.
func TestValidateRootSelection_AVariableTheSchemaCannotTypeIsUnknownNotAGuess(t *testing.T) {
	r := fixtureSchema(t).ValidateRootSelection("selection", `organization(login: $org, bogus: $x) { id }`)
	if r.Verdict != Unknown {
		t.Fatalf("a selection whose variable sits on an argument the schema does not define is %q, want %q:\n%s",
			r.Verdict, Unknown, r)
	}
	if !strings.Contains(r.Why, "$x") {
		t.Errorf("the reason does not name $x, so an author cannot tell which variable stopped the check: %s", r.Why)
	}
}

// TestValidateRootSelection_ASelectionOnNeitherRootTypeIsUnknown keeps the
// wrapper honest about what it does not know.
//
// A selection whose root fields are on neither the query nor the mutation root
// type is not a selection any operation can carry as written, and validating it
// as a query anyway would report a rejection about a document nobody sends.
func TestValidateRootSelection_ASelectionOnNeitherRootTypeIsUnknown(t *testing.T) {
	r := fixtureSchema(t).ValidateRootSelection("selection", `notARootField { id }`)
	if r.Verdict != Unknown {
		t.Fatalf("a selection on neither root type is %q, want %q:\n%s", r.Verdict, Unknown, r)
	}
}

// ---------------------------------------------------------------------------
// the VALUES a document is sent with
// ---------------------------------------------------------------------------

// varSDL is a schema whose one mutation takes the shape the value check exists
// for: a list of input objects with a non-null ENUM member, a non-null String and
// a NULLABLE String id. That is
// `ProjectV2SingleSelectFieldOptionInput` reduced to the members that decide
// whether a payload coerces.
const varSDL = `
type Query { ping: Boolean }
type Mutation { setOptions(options: [OptionInput!]!): Boolean }
enum Colour { BLUE GRAY GREEN }
input OptionInput {
  colour: Colour!
  description: String!
  id: String
  name: String!
}
`

func varSchema(t *testing.T) *Schema {
	t.Helper()
	s, err := LoadSchema("vars", varSDL)
	if err != nil {
		t.Fatalf("loading the variable fixture schema: %v", err)
	}
	return s
}

const setOptionsDocument = `mutation($options: [OptionInput!]!) { setOptions(options: $options) }`

func option(over map[string]any) map[string]any {
	o := map[string]any{"colour": "BLUE", "description": "in flight", "id": "OPT_1", "name": "In Build"}
	for k, v := range over {
		o[k] = v
	}
	return o
}

// TestValidateVariables_WhatTheDocumentCannotSayAnythingAbout is the property the
// whole method exists for.
//
// ⚠️ `mutation($options: [OptionInput!]!)` is a VALID document for every possible
// value of `$options`, because the values are not in it. So [Schema.ValidateDocument]
// — and every gate built on it — is blind to an input-object member that does not
// exist, a non-null member that is absent, a scalar of the wrong type and an enum
// value that is not a member. Each row here is one of those, and the FIRST row is
// the one that keeps the rest honest: a payload that is correct must pass, or a
// check that refuses everything would satisfy all of them.
func TestValidateVariables_WhatTheDocumentCannotSayAnythingAbout(t *testing.T) {
	s := varSchema(t)
	for _, row := range []struct {
		name    string
		options []map[string]any
		verdict Verdict
		path    string
	}{
		{
			name:    "the correct payload",
			options: []map[string]any{option(nil)},
			verdict: Valid,
		},
		{
			name:    "a member the input type does not declare",
			options: []map[string]any{option(map[string]any{"color": "BLUE"})},
			verdict: Invalid,
			path:    "variable.options[0].color",
		},
		{
			name:    "a non-null member that is absent",
			options: []map[string]any{{"colour": "BLUE", "name": "In Build"}},
			verdict: Invalid,
			path:    "variable.options[0].description",
		},
		{
			name:    "an enum value that is no member",
			options: []map[string]any{option(map[string]any{"colour": "CERULEAN"})},
			verdict: Invalid,
			path:    "variable.options[0].colour",
		},
		{
			name:    "a scalar of the wrong type",
			options: []map[string]any{option(map[string]any{"id": 7})},
			verdict: Invalid,
			path:    "variable.options[0].id",
		},
	} {
		t.Run(row.name+" is "+string(row.verdict), func(t *testing.T) {
			// The DOCUMENT is identical on every row and valid on every row, which
			// is the point: if it were the thing being judged, every row would agree.
			if d := s.ValidateDocument("doc", setOptionsDocument); !d.OK() {
				t.Fatalf("the document itself is %q, so this row would be judging the wrong thing:\n%s",
					d.Verdict, d)
			}
			r := s.ValidateVariables("doc", setOptionsDocument,
				map[string]any{"options": row.options})
			if r.Verdict != row.verdict {
				t.Fatalf("the coercion says %q, want %q:\n%s", r.Verdict, row.verdict, r)
			}
			if row.path == "" {
				return
			}
			var found bool
			for _, f := range r.Findings {
				if f.Path == row.path && f.Rule == variableRule {
					found = true
				}
			}
			if !found {
				t.Errorf("no finding carries the rule %s at the value path %q. A payload of many options and a "+
					"bare \"invalid\" tells an author nothing about WHICH option and which member:\n%s",
					variableRule, row.path, r)
			}
		})
	}
}

// TestValidateVariables_ADocumentTheSchemaRejectsIsReportedAsTheDocument keeps the
// two questions from being confused in the answer.
//
// A variable type read off a document the schema rejects is a type nothing has
// established, so the document's own findings are what comes back — not a
// coercion verdict computed against an unresolved declaration.
func TestValidateVariables_ADocumentTheSchemaRejectsIsReportedAsTheDocument(t *testing.T) {
	r := varSchema(t).ValidateVariables("doc",
		`mutation($options: [OptionInput!]!) { setOptions(options: $options) nosuchrootfield }`,
		map[string]any{"options": []map[string]any{option(nil)}})
	if r.Verdict != Invalid {
		t.Fatalf("a document the schema rejects is %q, want %q:\n%s", r.Verdict, Invalid, r)
	}
	for _, f := range r.Findings {
		if f.Rule == variableRule {
			t.Errorf("the answer carries a coercion finding for a document that is not valid, so a reader is "+
				"sent to the payload for a defect that is in the document:\n%s", r)
		}
	}
}

// TestValidateVariables_TheUncheckableIsUnknownAndNeverValid is the same
// unknown-is-never-clean rule, where the failure mode is a PANIC rather than a
// wrong answer: gqlparser's coercion dereferences a variable declaration's
// resolved type without a nil check, so a declaration the schema walk could not
// resolve must never reach it.
func TestValidateVariables_TheUncheckableIsUnknownAndNeverValid(t *testing.T) {
	s := varSchema(t)
	for _, row := range []struct {
		name     string
		document string
	}{
		{"a document that does not parse", `mutation($options: [OptionInput!]!) { setOptions(`},
		{
			name: "two operations, so which one's declarations these are is a guess",
			document: `mutation A($options: [OptionInput!]!) { setOptions(options: $options) }
mutation B($options: [OptionInput!]!) { setOptions(options: $options) }`,
		},
	} {
		t.Run(row.name+" is unknown", func(t *testing.T) {
			r := s.ValidateVariables("doc", row.document, map[string]any{"options": []map[string]any{option(nil)}})
			if r.Verdict == Valid {
				t.Fatalf("it is reported %q, and a check that did not run may never report a pass:\n%s",
					r.Verdict, r)
			}
			if r.OK() {
				t.Fatalf("OK() is true for verdict %q", r.Verdict)
			}
		})
	}
}

// TestValidateVariables_ANonNullVariableNobodySuppliedIsRefused is the row for the
// caller that forgot the payload entirely. An empty map and a correct one are the
// same document, so only the coercion can tell them apart.
func TestValidateVariables_ANonNullVariableNobodySuppliedIsRefused(t *testing.T) {
	r := varSchema(t).ValidateVariables("doc", setOptionsDocument, map[string]any{})
	if r.Verdict != Invalid {
		t.Fatalf("a non-null variable nobody supplied is %q, want %q:\n%s", r.Verdict, Invalid, r)
	}
}

// TestEnumValues_ReadsTheMembersAndSaysSoWhenTheTypeIsNotAnEnum is the gate for
// the accessor a caller pins a Go-side copy of an enum against.
//
// ⚠️ Both halves matter, and the second is the one that keeps a pin honest. A
// misspelled or since-renamed type name answering `nil, true` would make the pin
// compare an empty set against an empty set and pass forever, which is the exact
// shape of a gate that checks nothing. So the miss is reported as a miss, for a
// name the schema does not have AND for a name it has as something other than an
// enum.
func TestEnumValues_ReadsTheMembersAndSaysSoWhenTheTypeIsNotAnEnum(t *testing.T) {
	s := varSchema(t)

	got, ok := s.EnumValues("Colour")
	if !ok {
		t.Fatal("the schema declares `enum Colour { BLUE GRAY GREEN }` and this reports it is not an enum, so " +
			"every pin built on it would compare nothing against nothing")
	}
	if want := []string{"BLUE", "GRAY", "GREEN"}; !slices.Equal(got, want) {
		t.Errorf("the members of Colour read as %v, want %v — the schema's own declaration order", got, want)
	}

	for _, name := range []string{"NoSuchType", "OptionInput", "Mutation"} {
		if got, ok := s.EnumValues(name); ok {
			t.Errorf("%q reports as an enum with the members %v. It is %s in this schema, and a pin that took "+
				"this answer would be pinning a Go-side list against a set nothing produced", name, got,
				map[string]string{
					"NoSuchType":  "not declared at all",
					"OptionInput": "an input object",
					"Mutation":    "an object type",
				}[name])
		}
	}
}

// TestLoadSchema_RefusesSDLItCannotUse keeps a broken schema from producing a
// gate that reports "no problems found" about everything.
func TestLoadSchema_RefusesSDLItCannotUse(t *testing.T) {
	if _, err := LoadSchema("broken", `type Query { this is not SDL`); err == nil {
		t.Error("SDL that does not parse loaded without error, so every document checked against it would come " +
			"back clean")
	}
}

// TestLoadSchema_RefusesSDLWithNoQueryRootType is the second way a schema can be
// useless, and it is the one that PARSES.
//
// `type Foo { x: Int }` loads without complaint and yields a schema whose query
// root is nil. No executable document can be checked against it at all, so a gate
// built on one would report about nothing while looking exactly like a gate that
// works. Refusing at load is what keeps that from being every caller's own
// discovery.
func TestLoadSchema_RefusesSDLWithNoQueryRootType(t *testing.T) {
	for _, sdl := range []string{`type Foo { x: Int }`, `type Mutation { touch: Boolean }`} {
		_, err := LoadSchema("rootless", sdl)
		if err == nil {
			t.Errorf("SDL with no query root type (%s) loaded without error, and nothing can be validated "+
				"against it", sdl)
			continue
		}
		if !strings.Contains(err.Error(), "query root type") {
			t.Errorf("loading %s failed with %q, which does not say the query root type is what is missing", sdl, err)
		}
	}
}

// TestLoadSchemaFile_ReadsAPinnedSDLAndNamesTheFileItReadIt is the entry point a
// caller with a VENDORED schema uses.
//
// This module vendors no schema — the pin belongs to whoever depends on the
// backend — so reading one off disk is how the pinned bytes get here at all. The
// name assertion is the load-bearing half: every failure message carries
// [Schema.Name], and a gate over a pinned file that cannot say WHICH file it read
// sends an author to the wrong pin.
func TestLoadSchemaFile_ReadsAPinnedSDLAndNamesTheFileItReadIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.graphql")
	if err := os.WriteFile(path, []byte(fixtureSDL), 0o600); err != nil {
		t.Fatalf("writing the fixture schema: %v", err)
	}

	s, err := LoadSchemaFile(path)
	if err != nil {
		t.Fatalf("loading a schema that LoadSchema accepts as a string failed off disk: %v", err)
	}
	if s.Name() != path {
		t.Errorf("the schema names itself %q, want the path it was read from, %q", s.Name(), path)
	}
	if r := s.ValidateDocument("d", document("name dataType")); !r.OK() {
		t.Errorf("a document valid against the fixture schema is %q once the same SDL came off disk:\n%s",
			r.Verdict, r)
	}

	if _, err := LoadSchemaFile(filepath.Join(t.TempDir(), "nosuchfile.graphql")); err == nil {
		t.Error("a schema file that does not exist loaded without error, so a mistyped pin path would produce " +
			"a gate over an empty schema rather than a failure")
	}
}

// TestReport_OKIsTrueForValidAndForNothingElse pins the single line every gate
// built on this package hangs off.
//
// ⚠️ [NotChecked] is in this table because it is the row that has been wrong
// before: a value nobody examined answered [Valid], which is "nobody looked"
// rendered as "nothing wrong". OK() is the one place that decision is made, so it
// is asserted here rather than left to each caller to get right separately.
func TestReport_OKIsTrueForValidAndForNothingElse(t *testing.T) {
	for _, row := range []struct {
		verdict Verdict
		ok      bool
	}{
		{Valid, true},
		{Invalid, false},
		{Unknown, false},
		{NotChecked, false},
	} {
		if got := (Report{Name: "r", Verdict: row.verdict}).OK(); got != row.ok {
			t.Errorf("OK() is %v for the verdict %q, want %v", got, row.verdict, row.ok)
		}
	}
}

// TestReport_StringNamesTheValueAndPrintsTheTextThatWasValidated covers the other
// half of an actionable failure: the rendering a gate actually prints.
//
// A finding's line number is a position in the text that WAS validated, which for
// a root selection is the wrapper and not the caller's own constant — so a report
// that omits that text hands an author a line number pointing into a document
// that does not exist on their disk.
func TestReport_StringNamesTheValueAndPrintsTheTextThatWasValidated(t *testing.T) {
	s := fixtureSchema(t)

	bad := s.ValidateDocument("theConstant", document("name nosuchleaf"))
	if bad.Verdict != Invalid {
		t.Fatalf("the fixture document is %q, want %q — this test is judging the wrong thing:\n%s",
			bad.Verdict, Invalid, bad)
	}
	for _, want := range []string{
		"theConstant",                          // which value
		"FieldsOnCorrectType",                  // which rule
		"... on IssueFieldCommon > nosuchleaf", // the path index resolved the position
		`Cannot query field "nosuchleaf"`,      // the validator's own wording
		"the document validated",               // and the text the line numbers are in
	} {
		if !strings.Contains(bad.String(), want) {
			t.Errorf("the rendered report does not carry %q:\n%s", want, bad)
		}
	}

	unknown := s.ValidateDocument("brokenConstant", `query { organization(login: "x") {`)
	if !strings.Contains(unknown.String(), "UNKNOWN") || !strings.Contains(unknown.String(), unknown.Why) {
		t.Errorf("an unknown verdict renders without the word UNKNOWN or without its reason, so a reader can "+
			"mistake it for a pass:\n%s", unknown)
	}

	notChecked := Report{Name: "notGraphQL", Verdict: NotChecked, Why: "it carries no selection set"}
	if !strings.Contains(notChecked.String(), "NOT CHECKED") {
		t.Errorf("a not-checked verdict renders as %q, which does not say nobody looked", notChecked)
	}
}

// TestValidateRootSelection_AFragmentSharingARootFieldsNameIsNotThatRootField
// drives the trap fixtureSDL carries on purpose.
//
// The fixture's Query has a field called `nodes(ids: [ID!]!)`, exactly as GitHub's
// does, so a connection's node selection — `nodes { name }`, the commonest shape
// in a paged GraphQL read — parses as a root selection. Reasoning forwards
// ("there IS a root field called nodes") reports it INVALID for an argument no
// fragment of that shape ever carries: a systematic false positive over the whole
// class.
//
// ⚠️ This is the falsifier for suppliesRequiredArgs and not decoration. Remove its
// argument half and the verdict here becomes Invalid, naming `ids` — a defect this
// package invented about a document nobody sends.
func TestValidateRootSelection_AFragmentSharingARootFieldsNameIsNotThatRootField(t *testing.T) {
	r := fixtureSchema(t).ValidateRootSelection("nodeFragment", `nodes { name }`)
	if r.Verdict != Unknown {
		t.Fatalf("a fragment sharing a root field's name is %q, want %q — the root field requires `ids`, this "+
			"text does not supply them, so it is not that root field:\n%s", r.Verdict, Unknown, r)
	}
	if !strings.Contains(r.Why, "nodes") {
		t.Errorf("the reason does not name the field it could not place: %s", r.Why)
	}
	for _, f := range r.Findings {
		if strings.Contains(f.Message, "ids") {
			t.Errorf("the answer carries a finding about the `ids` argument, which is the invented defect this "+
				"reasoning exists to stop:\n%s", r)
		}
	}
}
