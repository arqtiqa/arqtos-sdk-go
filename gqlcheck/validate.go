package gqlcheck

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

// A Verdict is what this package can say about one piece of GraphQL.
//
// ⚠️ Only ONE of them is a pass, and the other three are the whole point. A
// document that could not be parsed has not been checked, and a checker that
// reports the unchecked as clean is how the defect this package exists for ships
// behind a green suite.
type Verdict string

const (
	// Valid means the schema accepted the document.
	Valid Verdict = "valid"
	// Invalid means the schema rejected it, and [Report.Findings] says where.
	Invalid Verdict = "invalid"
	// Unknown means nothing was determined ABOUT SOMETHING THAT LOOKS LIKE
	// GRAPHQL: the text would not parse, a format verb could not be rendered, a
	// variable's type could not be inferred from the schema, a value could not be
	// folded at all. It is NEVER a pass.
	Unknown Verdict = "unknown"
	// NotChecked means the caller DID NOT LOOK, and says so. Nothing in this
	// package produces it: it is the verdict for a caller that selects what to
	// validate and needs to record the values it decided were not GraphQL at all.
	//
	// ⚠️ It belongs to this vocabulary rather than to each caller's own bucket
	// because that bucket used to answer [Valid] — the unknown-is-never-clean rule
	// broken inside the gate that exists to enforce correctness. The cheap test for
	// "not GraphQL" is that the text carries no `{`, which is sound about DOCUMENTS
	// and silent about fragments: a braceless leaf list — `"id title
	// nosuchleafatall"` — IS GraphQL, lands here, and was reported clean. A caller
	// must decide what to do with NotChecked; it may not read it as a pass, and
	// [Report.OK] is false for it.
	NotChecked Verdict = "not checked"
)

// A Finding is one reason the schema rejected a document.
type Finding struct {
	// Rule is the validator rule that fired — "FieldsOnCorrectType",
	// "ProvidedRequiredArguments". It is the class of the defect.
	Rule string
	// Message is the validator's own wording.
	Message string
	// Path is the selection path that reaches the offending node, rendered
	// "organization > issueFields > nodes > ... on IssueFieldCommon > id". It is
	// computed from the parsed AST and never from the document text, and it is
	// what makes a finding over ten constants actionable.
	Path string
	// Line and Column are the position IN THE TEXT THAT WAS VALIDATED, which for
	// a root selection is the wrapper rather than the constant — see
	// [Report.Checked].
	Line, Column int
}

func (f Finding) String() string {
	at := f.Path
	if at == "" {
		at = "(no path)"
	}
	return fmt.Sprintf("[%s] at %s (line %d, col %d): %s", f.Rule, at, f.Line, f.Column, f.Message)
}

// A Report is the whole answer about one named piece of GraphQL.
type Report struct {
	// Name is what the caller called it — a Go constant name, a wire document's
	// label. Every failure names it, because "invalid" over ten constants tells
	// an author nothing.
	Name string
	// Verdict is the answer. Unknown is a failure, not a pass.
	Verdict Verdict
	// Why is set when the verdict is Unknown: what stopped this from being
	// determined.
	Why string
	// Findings are the rejections, when the verdict is Invalid.
	Findings []Finding
	// Checked is the exact text that went to the validator. It differs from the
	// constant's own value when a root selection was wrapped in an operation, and
	// a failure prints it so that a line number means something.
	Checked string
}

// OK reports whether the schema accepted this. Unknown and NotChecked are NOT
// ok: neither of them is an answer about the document.
func (r Report) OK() bool { return r.Verdict == Valid }

func (r Report) String() string {
	switch r.Verdict {
	case Valid:
		if r.Why != "" {
			return fmt.Sprintf("%s: valid (%s)", r.Name, r.Why)
		}
		return fmt.Sprintf("%s: valid", r.Name)
	case NotChecked:
		return fmt.Sprintf("%s: NOT CHECKED — %s", r.Name, r.Why)
	case Unknown:
		return fmt.Sprintf("%s: UNKNOWN — %s\n--- the text ---\n%s\n---", r.Name, r.Why, r.Checked)
	default:
		parts := make([]string, 0, len(r.Findings))
		for _, f := range r.Findings {
			parts = append(parts, "  "+f.String())
		}
		return fmt.Sprintf("%s: INVALID against the schema\n%s\n--- the document validated ---\n%s\n---",
			r.Name, strings.Join(parts, "\n"), r.Checked)
	}
}

// A Schema is a loaded GraphQL schema documents are checked against.
type Schema struct {
	name string
	s    *ast.Schema
}

// LoadSchema parses an SDL document — the schema a backend publishes — into the
// thing documents are validated against.
//
// ⚠️ The SDL is the CALLER's to supply and to pin, and this module vendors none.
// A vendored schema is a vendor artefact with a refresh policy attached, and ONE
// pinned copy shared by every module that needs it is the only arrangement in
// which two of them cannot drift apart — silently, in the direction that reports
// clean.
func LoadSchema(name, sdl string) (*Schema, error) {
	s, err := gqlparser.LoadSchema(&ast.Source{Name: name, Input: sdl})
	if err != nil {
		return nil, fmt.Errorf("gqlcheck: loading the schema %q: %w", name, err)
	}
	if s == nil || s.Query == nil {
		return nil, fmt.Errorf("gqlcheck: the schema %q has no query root type, so no document can be checked "+
			"against it", name)
	}
	return &Schema{name: name, s: s}, nil
}

// LoadSchemaFile is [LoadSchema] over a vendored SDL file.
func LoadSchemaFile(path string) (*Schema, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("gqlcheck: reading the schema at %s: %w", path, err)
	}
	return LoadSchema(path, string(b))
}

// Name is what the schema was loaded as, for a failure message.
func (s *Schema) Name() string { return s.name }

// EnumValues reports the members of one ENUM type, in the order the schema
// declares them, and whether the schema has such an enum at all.
//
// ⚠️ The second return is not a convenience. A caller pins a Go-side list against
// this, and a misspelled or since-renamed type name would otherwise answer "no
// members" — which compares equal to nothing and turns the pin into a gate that
// checks nothing. `false` says the type is not an enum in this schema; an enum with
// no members is impossible in SDL, so a `true` always carries at least one.
//
// It is a READ of the vendored schema and never a guess: a Go-side copy of an enum
// is a list that drifts the day the backend adds a member, and the only thing that
// can say it has is the schema itself.
func (s *Schema) EnumValues(name string) ([]string, bool) {
	def, ok := s.s.Types[name]
	if !ok || def == nil || def.Kind != ast.Enum {
		return nil, false
	}
	out := make([]string, 0, len(def.EnumValues))
	for _, v := range def.EnumValues {
		out = append(out, v.Name)
	}
	return out, true
}

// ValidateDocument checks one COMPLETE executable document — `query(...) {...}`,
// `mutation(...) {...}` — against the schema.
//
// A document that will not parse comes back [Unknown] and never [Valid].
func (s *Schema) ValidateDocument(name, document string) Report {
	doc, perr := parser.ParseQuery(&ast.Source{Name: name, Input: document})
	if perr != nil {
		return Report{Name: name, Verdict: Unknown, Checked: document, Why: fmt.Sprintf(
			"it does not parse as a GraphQL document, so NOTHING has been checked about it and it is reported "+
				"unknown rather than clean: %v", perr)}
	}
	if len(doc.Operations) == 0 && len(doc.Fragments) == 0 {
		return Report{Name: name, Verdict: Unknown, Checked: document, Why: "it parses to no operation and no " +
			"fragment, so there is nothing in it to validate"}
	}
	return s.report(name, document, doc)
}

// ValidateVariables checks the VARIABLE VALUES a document is actually sent with,
// against the types the document's own operation declares for them.
//
// ⚠️ This is a different question from [Schema.ValidateDocument] and neither implies
// the other. A document declaring `$options: [SingleSelectFieldOptionInput!]!` is
// valid whatever is later put in that variable — the values are not in the
// document — so the whole shape of what goes on the wire inside a variable is
// invisible to every other check in this package. That is the input-object half of
// the blindness that let a leaf an interface does not have reach a live call: a
// fixture server answers whatever it is handed, and the document says nothing
// about the payload.
//
// It validates the DOCUMENT first and returns that report unchanged when the
// document is not valid: variable types read off a document the schema rejects are
// types nothing has established. That pass is also what RESOLVES each variable
// declaration to its schema type — gqlparser's coercion reads
// `VariableDefinition.Definition` and dereferences it without a nil check, so the
// order here is load-bearing rather than tidy.
//
// The coercion is gqlparser's own [validator.VariableValues], the same one a
// server runs before it executes, so what it refuses is what the backend refuses
// — an input-object member the type does not declare, a non-null member absent, a
// string where an Int belongs, and a value that is not a member of an ENUM. A
// non-null variable the caller did not supply at all is refused too.
//
// A document carrying no variable declarations and a caller supplying no variables
// is [Valid] and says so: there is nothing to coerce and nothing was hidden.
func (s *Schema) ValidateVariables(name, document string, variables map[string]any) Report {
	doc, perr := parser.ParseQuery(&ast.Source{Name: name, Input: document})
	if perr != nil {
		return Report{Name: name, Verdict: Unknown, Checked: document, Why: fmt.Sprintf(
			"it does not parse as a GraphQL document, so NOTHING has been checked about it and it is reported "+
				"unknown rather than clean: %v", perr)}
	}
	// The document pass walks doc, which is what fills in each variable
	// declaration's schema type.
	if r := s.report(name, document, doc); !r.OK() {
		return r
	}
	if len(doc.Operations) != 1 {
		return Report{Name: name, Verdict: Unknown, Checked: document, Why: fmt.Sprintf(
			"it carries %d operations, and which one's variable declarations these values are for is not "+
				"something this check may guess", len(doc.Operations))}
	}
	// ⚠️ A declaration the walk did not resolve is reported UNKNOWN rather than
	// handed on: the coercion dereferences it, so passing one through is a panic
	// inside a gate rather than a finding from it.
	for _, v := range doc.Operations[0].VariableDefinitions {
		if v.Definition == nil {
			return Report{Name: name, Verdict: Unknown, Checked: document, Why: fmt.Sprintf(
				"the variable $%s is declared as %s, which the schema walk did not resolve to a type — so the "+
					"values cannot be coerced and NOTHING has been checked about them", v.Variable, v.Type)}
		}
	}
	if _, err := validator.VariableValues(s.s, doc.Operations[0], variables); err != nil {
		index := newPathIndex(doc)
		var list gqlerror.List
		if errors.As(err, &list) {
			findings := make([]Finding, 0, len(list))
			for _, e := range list {
				findings = append(findings, variableFinding(e, index))
			}
			return invalidVariables(name, document, findings...)
		}
		var one *gqlerror.Error
		if errors.As(err, &one) {
			return invalidVariables(name, document, variableFinding(one, index))
		}
		// An error that is neither shape has no path and no rule: it is reported as
		// a finding anyway rather than dropped, because a coercion that failed and
		// said nothing is still a coercion that failed.
		return invalidVariables(name, document, Finding{Rule: variableRule, Message: err.Error()})
	}
	return Report{Name: name, Verdict: Valid, Checked: document}
}

// invalidVariables is the one shape a coercion rejection comes back in.
func invalidVariables(name, document string, findings ...Finding) Report {
	return Report{Name: name, Verdict: Invalid, Checked: document, Findings: findings}
}

// variableRule is the rule name every variable-coercion finding carries. The spec
// has no named rule for this — coercion happens before execution rather than in
// the validator's rule set — so naming it here keeps a finding attributable to the
// check that produced it.
const variableRule = "VariableValues"

// variableFinding renders one coercion error. Its PATH is the variable path
// gqlparser reports — `options[0].color` — and not a selection path: the defect is
// in the value, and the selection set has nothing to do with it.
func variableFinding(e *gqlerror.Error, index *pathIndex) Finding {
	f := finding(e, index)
	if f.Rule == "" || f.Rule == "validation" {
		f.Rule = variableRule
	}
	if p := e.Path.String(); p != "" {
		f.Path = p
	}
	return f
}

// ValidateRootSelection checks a SELECTION that is sent as a root field of an
// operation this package did not see — the aliased catalogue document's board and
// scope selections, which the alias engine assembles at run time.
//
// It wraps the selection in the operation its root fields belong to, DERIVES the
// variable declarations from the schema's own argument types (a selection carrying
// `first: $first` is wrapped in an operation declaring `$first: Int`), and
// validates the result. Inferring rather than guessing is what keeps the wrapper
// from being a second opinion about the document: the types come from the same
// schema the fields are checked against.
//
// A variable whose type the schema cannot supply is [Unknown]. So is a selection
// whose root fields are on neither root type.
func (s *Schema) ValidateRootSelection(name, selection string) Report {
	keyword, why := s.rootKeywordOf(selection)
	if keyword == "" {
		return Report{Name: name, Verdict: Unknown, Checked: selection, Why: why}
	}

	// Pass one annotates the argument values with the types the SCHEMA expects,
	// which is where the variable declarations come from.
	probe, perr := parser.ParseQuery(&ast.Source{Name: name, Input: wrap(keyword, "", selection)})
	if perr != nil {
		return Report{Name: name, Verdict: Unknown, Checked: wrap(keyword, "", selection), Why: fmt.Sprintf(
			"it does not parse as a %s selection: %v", keyword, perr)}
	}
	validator.Walk(s.s, probe, &validator.Events{})
	decls, derr := variableDeclarations(probe)
	if derr != nil {
		return Report{Name: name, Verdict: Unknown, Checked: wrap(keyword, "", selection), Why: derr.Error()}
	}

	document := wrap(keyword, decls, selection)
	doc, perr := parser.ParseQuery(&ast.Source{Name: name, Input: document})
	if perr != nil {
		return Report{Name: name, Verdict: Unknown, Checked: document, Why: fmt.Sprintf(
			"it does not parse once wrapped in its operation: %v", perr)}
	}
	return s.report(name, document, doc)
}

// defaultRules is gqlparser's own specified rule set — the GraphQL spec's
// validation rules, built once.
//
// It is the LIBRARY's list and this package adds nothing to it and removes
// nothing from it. A rule dropped here would be a class of rejection GitHub makes
// and this gate does not, which is the blindness the gate exists to end.
var defaultRules = rules.NewDefaultRules()

// report runs the validator and attributes every error to a selection path.
func (s *Schema) report(name, text string, doc *ast.QueryDocument) Report {
	errs := validator.ValidateWithRules(s.s, doc, defaultRules)
	if len(errs) == 0 {
		return Report{Name: name, Verdict: Valid, Checked: text}
	}
	// The path index is built from the doc the validator just walked, so a path
	// and the error it explains come from the same AST.
	index := newPathIndex(doc)
	out := Report{Name: name, Verdict: Invalid, Checked: text, Findings: make([]Finding, 0, len(errs))}
	for _, e := range errs {
		out.Findings = append(out.Findings, finding(e, index))
	}
	sort.SliceStable(out.Findings, func(i, j int) bool {
		if out.Findings[i].Line != out.Findings[j].Line {
			return out.Findings[i].Line < out.Findings[j].Line
		}
		return out.Findings[i].Column < out.Findings[j].Column
	})
	return out
}

func finding(e *gqlerror.Error, index *pathIndex) Finding {
	f := Finding{Rule: e.Rule, Message: e.Message}
	if f.Rule == "" {
		f.Rule = "validation"
	}
	if len(e.Locations) > 0 {
		f.Line, f.Column = e.Locations[0].Line, e.Locations[0].Column
	}
	f.Path = index.at(f.Line, f.Column)
	return f
}

// wrap renders a root selection as the operation that carries it.
func wrap(keyword, decls, selection string) string {
	head := keyword
	if decls != "" {
		head += "(" + decls + ")"
	}
	return head + " {\n" + strings.TrimSpace(selection) + "\n}\n"
}

// rootKeywordOf says which operation a selection's root fields belong to, or why
// it belongs to neither.
//
// Every root field must be on the SAME root type: a selection mixing a Query
// field and a Mutation field is not a selection any operation can carry, and
// picking one of the two would validate half of it and say nothing about the rest.
// suppliesRequiredArgs reports whether sel could really BE root field def — that
// is, the schema has such a field AND the selection supplies every argument the
// field requires.
//
// ⚠️ The argument half is what stops a systematic false positive, and the case is
// not exotic. GitHub's Query type, for one, has a field called `nodes` — so EVERY
// `nodes { … }` fragment written against that schema, the commonest shape there
// is in a paged GraphQL read, parses as a root selection and is then reported
// INVALID for the `ids: [ID!]!` argument no fragment of that shape ever carries.
//
// Reasoning it the other way round is what makes this sound rather than an
// excuse: the root field REQUIRES `ids`, this text does not supply it, therefore
// this text is not that root field. It is a fragment that happens to share a name,
// and a fragment is the wire gate's question.
//
// Measured, not supposed: three of the paged-read fragments in the connector this
// package was extracted from all landed here, and the fixture schema in this
// package's own tests carries the same trap so it cannot come back.
func suppliesRequiredArgs(def *ast.FieldDefinition, sel *ast.Field) bool {
	if def == nil {
		return false
	}
	for _, arg := range def.Arguments {
		if arg.Type == nil || !arg.Type.NonNull || arg.DefaultValue != nil {
			continue
		}
		if sel.Arguments.ForName(arg.Name) == nil {
			return false
		}
	}
	return true
}

func (s *Schema) rootKeywordOf(selection string) (keyword, why string) {
	doc, err := parser.ParseQuery(&ast.Source{Input: wrap("query", "", selection)})
	if err != nil {
		return "", fmt.Sprintf("it does not parse as a selection set: %v", err)
	}
	if len(doc.Operations) != 1 || len(doc.Operations[0].SelectionSet) == 0 {
		return "", "it parses to no root selection at all"
	}
	onQuery, onMutation := true, s.s.Mutation != nil
	var names []string
	for _, sel := range doc.Operations[0].SelectionSet {
		field, ok := sel.(*ast.Field)
		if !ok {
			return "", "its root selection is not a field, and an operation's root selections are fields"
		}
		names = append(names, field.Name)
		if !suppliesRequiredArgs(s.s.Query.Fields.ForName(field.Name), field) {
			onQuery = false
		}
		if s.s.Mutation == nil || !suppliesRequiredArgs(s.s.Mutation.Fields.ForName(field.Name), field) {
			onMutation = false
		}
	}
	switch {
	case onQuery:
		return "query", ""
	case onMutation:
		return "mutation", ""
	default:
		return "", fmt.Sprintf("its root field(s) %s are not all on the schema's query root type nor all on its "+
			"mutation root type, so no operation can carry this selection as written",
			strings.Join(quoteAll(names), ", "))
	}
}

// variableDeclarations renders the declaration list for every variable the
// selection references, with each type taken from the SCHEMA's own argument
// definition.
//
// A variable whose type cannot be inferred is an error rather than a guess: a
// wrapper that declared `$x: String` for an `ID!` argument would report a fault
// this package invented.
func variableDeclarations(doc *ast.QueryDocument) (string, error) {
	types := map[string]string{}
	var order []string
	var bad []string
	for _, op := range doc.Operations {
		for _, sel := range op.SelectionSet {
			collectVariables(sel, types, &order, &bad)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return "", fmt.Errorf("the type of %s cannot be inferred from the schema — the argument carrying it is "+
			"not one the schema defines — so the wrapper this selection would be validated in cannot be built, "+
			"and a guessed declaration would report a fault this check invented",
			strings.Join(uniqueSorted(bad), ", "))
	}
	sort.Strings(order)
	parts := make([]string, 0, len(order))
	for _, name := range uniqueSorted(order) {
		parts = append(parts, "$"+name+": "+types[name])
	}
	return strings.Join(parts, ", "), nil
}

func collectVariables(sel ast.Selection, types map[string]string, order, bad *[]string) {
	var args ast.ArgumentList
	var kids ast.SelectionSet
	switch s := sel.(type) {
	case *ast.Field:
		args, kids = s.Arguments, s.SelectionSet
	case *ast.InlineFragment:
		kids = s.SelectionSet
	case *ast.FragmentSpread:
		return
	}
	for _, a := range args {
		collectValueVariables(a.Value, types, order, bad)
	}
	for _, k := range kids {
		collectVariables(k, types, order, bad)
	}
}

func collectValueVariables(v *ast.Value, types map[string]string, order, bad *[]string) {
	if v == nil {
		return
	}
	if v.Kind == ast.Variable {
		switch {
		case v.ExpectedType == nil:
			*bad = append(*bad, "$"+v.Raw)
		case types[v.Raw] != "" && types[v.Raw] != v.ExpectedType.String():
			// The same name in two positions wanting two types. Nothing here can
			// pick one, and picking either validates half the selection against a
			// declaration the other half contradicts.
			*bad = append(*bad, "$"+v.Raw)
		default:
			if types[v.Raw] == "" {
				*order = append(*order, v.Raw)
			}
			types[v.Raw] = v.ExpectedType.String()
		}
	}
	for _, c := range v.Children {
		collectValueVariables(c.Value, types, order, bad)
	}
}

func uniqueSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return slicesCompact(out)
}

func slicesCompact(in []string) []string {
	out := in[:0]
	var last string
	for i, s := range in {
		if i > 0 && s == last {
			continue
		}
		out = append(out, s)
		last = s
	}
	return out
}

func quoteAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, fmt.Sprintf("%q", s))
	}
	return out
}
