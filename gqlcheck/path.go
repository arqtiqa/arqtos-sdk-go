package gqlcheck

import (
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// This file turns a validator error's POSITION into the selection PATH that
// reaches it.
//
// gqlparser reports where a rejection happened and not what it happened inside,
// and "Cannot query field id" over a document with ten inline fragments in it is
// not an answer an author can act on. The path is built by walking the parsed AST
// — the same AST the validator walked — and never by scanning the document text:
// a text scan is the failure mode this whole package exists to stop repeating.

// A pathIndex maps a position in the validated text to the path that reaches it.
type pathIndex struct {
	entries []pathEntry
}

type pathEntry struct {
	line, column int
	path         string
}

// newPathIndex records every field, inline fragment, fragment spread and
// ARGUMENT of the document, each with the path that reaches it.
//
// Arguments are recorded because a whole class of rejections — a required
// argument missing, an argument the field does not have, a value of the wrong
// type — is positioned at the argument rather than at the field.
func newPathIndex(doc *ast.QueryDocument) *pathIndex {
	idx := &pathIndex{}
	for _, op := range doc.Operations {
		idx.walk(op.SelectionSet, nil)
	}
	for _, frag := range doc.Fragments {
		idx.walk(frag.SelectionSet, []string{"fragment " + frag.Name})
	}
	sort.SliceStable(idx.entries, func(i, j int) bool {
		if idx.entries[i].line != idx.entries[j].line {
			return idx.entries[i].line < idx.entries[j].line
		}
		return idx.entries[i].column < idx.entries[j].column
	})
	return idx
}

func (i *pathIndex) walk(set ast.SelectionSet, prefix []string) {
	for _, sel := range set {
		switch s := sel.(type) {
		case *ast.Field:
			here := append(prefix[:len(prefix):len(prefix)], fieldSegment(s))
			i.record(s.Position, here)
			for _, a := range s.Arguments {
				i.recordValue(a.Position, append(here[:len(here):len(here)], "argument "+a.Name))
				i.walkValue(a.Value, append(here[:len(here):len(here)], "argument "+a.Name))
			}
			i.walk(s.SelectionSet, here)
		case *ast.InlineFragment:
			seg := "... on " + s.TypeCondition
			if s.TypeCondition == "" {
				seg = "..."
			}
			here := append(prefix[:len(prefix):len(prefix)], seg)
			i.record(s.Position, here)
			i.walk(s.SelectionSet, here)
		case *ast.FragmentSpread:
			i.record(s.Position, append(prefix[:len(prefix):len(prefix)], "..."+s.Name))
		}
	}
}

func (i *pathIndex) walkValue(v *ast.Value, prefix []string) {
	if v == nil {
		return
	}
	for _, c := range v.Children {
		here := prefix
		if c.Name != "" {
			here = append(prefix[:len(prefix):len(prefix)], c.Name)
		}
		i.recordValue(c.Value.Position, here)
		i.walkValue(c.Value, here)
	}
}

func (i *pathIndex) record(pos *ast.Position, path []string) {
	if pos == nil {
		return
	}
	i.entries = append(i.entries, pathEntry{line: pos.Line, column: pos.Column, path: strings.Join(path, " > ")})
}

func (i *pathIndex) recordValue(pos *ast.Position, path []string) { i.record(pos, path) }

// at returns the path for a position.
//
// An exact match wins. Failing that the LAST node at or before the position wins,
// because a rule positioned somewhere this index did not record — inside a value,
// on a directive — is still inside the last node that opened. A position before
// every recorded node has no path, and the empty string says so rather than
// naming the first one.
func (i *pathIndex) at(line, column int) string {
	best := ""
	for _, e := range i.entries {
		if e.line == line && e.column == column {
			return e.path
		}
		if e.line < line || (e.line == line && e.column < column) {
			best = e.path
			continue
		}
		break
	}
	return best
}

// fieldSegment renders one field of a path.
//
// An ALIASED field is rendered `desc: description`, showing both halves, because
// an alias is validated against the field it names and not against the response
// key — so an author reading a finding needs to see which of the two the schema
// rejected.
func fieldSegment(f *ast.Field) string {
	if f.Alias != "" && f.Alias != f.Name {
		return f.Alias + ": " + f.Name
	}
	return f.Name
}
