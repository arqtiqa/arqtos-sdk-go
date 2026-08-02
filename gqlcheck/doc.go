// Package gqlcheck validates GraphQL a client SENDS against the schema the
// backend actually serves.
//
// It takes a schema and some text and returns a verdict. There is no vendor
// schema in it, no connector contract and no host model: a caller supplies the
// SDL it pins — see [LoadSchema] and [LoadSchemaFile] — and this answers about
// the documents that caller hands it. That is the whole surface.
//
// # The question this package asks, and the one it does not
//
// A document-versus-DECODE check asks: does this selection request every leaf
// the Go struct on the other side of the decode carries? A document can satisfy
// that perfectly and still be REJECTED by the server, because the two are
// different questions and only one of them is usually asked.
//
// The measurement that produced this package: `id` was added to an inline
// fragment on an interface that has no `id`; the backend rejects the WHOLE
// document at validation — and the suite stayed green, because the fixture
// server was an httptest.NewServer that answers whatever it is asked. Nothing in
// that repository asked the schema.
//
// This package asks document-versus-SCHEMA. It is the check a fixture server
// cannot stand in for.
//
// # It composes a parser rather than scanning text
//
// Every check here goes through github.com/vektah/gqlparser/v2 — the parser and
// validator gqlgen is built on — and that is this module's only dependency for
// it. Composing the parser is also a repeat avoidance: the estate this was
// extracted from hand-rolled three GraphQL text scanners and every one of them
// produced a defect. A fourth would be the same defect class again.
//
// # The verdicts, and why only one of them is a pass
//
// A piece of GraphQL is [Valid], [Invalid], [Unknown] or [NotChecked], and
// [Report.OK] is true for exactly the first. Unknown is not a convenience: a
// document this package cannot PARSE is one it has said nothing about, and
// reporting it clean is how a real defect ships behind a green gate. Every
// failure mode that is not "the schema rejected this" — an unparseable document,
// a variable whose type cannot be inferred, an operation count that makes the
// answer a guess — lands on Unknown, and a caller must treat Unknown as a
// failure.
//
// [NotChecked] is the fourth, and nothing in THIS package produces it: it is the
// verdict for a caller that decides what to look at and needs to record that it
// did not look at something. It is part of the vocabulary rather than each
// caller's own bucket precisely so that [Report.OK] is what decides whether an
// unexamined value passes, in one place.
//
// # What a finding names
//
// A bare "invalid" over ten constants is not actionable, so every [Finding]
// carries the schema rule that fired, the validator's own message, the position,
// and the SELECTION PATH that reaches the offending node — "organization >
// issueFields > nodes > ... on IssueFieldCommon > id". The path is computed from
// the parser's AST, never from the document text.
//
// # BLIND SPOTS
//
//   - It answers about the schema it was GIVEN. A schema pinned at a date and
//     never refreshed cannot see a field the backend has since removed, and that
//     direction is silent. Refreshing the pin — and noticing when it is stale —
//     belongs to whoever vendors the SDL; this package cannot detect it.
//   - It answers about the text it was HANDED. A document assembled at run time
//     from pieces that are individually fine is only covered if something hands
//     the assembled text here.
//   - Schema validity is not semantic correctness. A document that asks for the
//     wrong field of the right type validates.
//
// # The boundary: this package does not FIND documents
//
// Something has to decide what text to hand it. In the estate this was extracted
// from that job is done by a Go-source sweep — it walks a loaded package, folds
// every string constant, classifies each as a document, a root selection or a
// fragment, and drives this — and that sweep did NOT move here. It is bound to a
// package loader and to Go AST machinery, none of which belongs in a contract
// SDK, and no second module has needed it.
//
// Three corrections recorded against that sweep are repeated here so they do not
// vanish in the extraction. Each is about the sweep and about the wire gate that
// accompanies it, NOT about this package — but a reader who takes this package
// and builds the finding half themselves will hit all three:
//
//   - A file behind an unselected BUILD TAG is NOT a blind spot for such a
//     sweep, though an earlier draft said three times that it was. The loader
//     reads every non-test `.go` file in the directory with no build context
//     applied at all, so a document behind `//go:build ignore_me` is swept like
//     any other. Stating a limit that does not exist is the same failure as
//     hiding one that does — a reader trusts the list either way.
//   - A companion "wire gate" — one that walks back from the transport call and
//     requires every operation reaching it to be driven by a test — must derive
//     its scope from what actually REACHES the transport, not from a receiver
//     name. Filtering on one type's methods leaves an exported free function, or
//     an option closure that reaches the transport, never required to have a
//     driver at all.
//   - A fragment cleared as "validated as part of its container" may be reported
//     [Valid] only when it has exactly ONE container. A reference implies
//     textual inclusion for a concatenated document; it does not for a fragment
//     REUSED in a second, run-time-spliced context. With two containers the
//     honest verdict is [Unknown], naming both — clearing it is an affirmative
//     pass over a document the backend rejects.
package gqlcheck
