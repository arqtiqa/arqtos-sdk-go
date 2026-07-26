// Package cerr is the connector error taxonomy. Contract methods return *Error;
// callers classify with KindOf/Retryable/TripsBreaker and never string-match.
//
// # The vocabulary is closed
//
// [Kinds] is the whole set. A connector classifies every failure into one of
// them, and a host acts on the Kind alone — it never reads the message. That
// is the point: a backend that rewords an error must not be able to change
// host behaviour, and three backends must not each grow their own
// string-matching dialect of the same classification.
//
// Adding a Kind is therefore a deliberate change to a published contract, not
// a local edit. A Kind outside the set is not a classification: [Kind.Valid]
// reports false for it and [Classified] refuses it.
//
// # The safe default runs the other way from version checking
//
// A failure the connector cannot classify is [KindUnknown], and Unknown does
// NOT trip a breaker ([TripsBreaker]). Escalating on a guess converts one
// unrecognised error into a total resolution outage for that backend —
// strictly worse than the rate limit the breaker guards against.
//
// This is the opposite default from version negotiation, where an
// unverifiable connector is refused. Both are correct: an unverifiable
// connector must not run, while an unclassified transient failure must not
// escalate. Do not unify them.
package cerr

import (
	"errors"
	"fmt"
	"slices"
)

type Kind int

const (
	// KindUnknown is the failure a connector could not classify. It is the
	// safe default: it fails the call and escalates nothing.
	KindUnknown Kind = iota
	// KindNotFound is a referenced secret, scope, or lease that does not exist.
	KindNotFound
	// KindUnauthorized is the connector or caller identity lacking access —
	// including a backend session that is not signed in.
	KindUnauthorized
	// KindUnavailable is a transiently unreachable backing store.
	KindUnavailable
	// KindUnsupported is an operation this connector does not implement.
	KindUnsupported
	// KindInvalid is bad input: a malformed ref, an unknown lease id.
	KindInvalid
	// KindTimeout is a deadline passing before the operation completed.
	KindTimeout
	// KindRateLimited is the backend refusing load: a quota or rate limit the
	// backend itself reported. It is the one Kind that trips a host's
	// circuit breaker, so a connector returns it only on positive evidence
	// from the backend — never as a guess about an error it did not
	// recognise, which is what KindUnknown is for.
	KindRateLimited
	// KindContractViolation is the connector returning something this
	// contract does not admit — for example reporting a successful
	// resolution that carries no value. It is detected by the host or by the
	// SDK's own guards, and a connector never returns it deliberately. It is
	// neither retryable nor breaker-tripping: the fault is in the connector,
	// not in backend load.
	KindContractViolation
)

// kindNames is the single source of truth for the closed vocabulary: Kinds,
// Valid and String all derive from it, so a Kind cannot be half-added (in the
// enum but nameless, or named but not enumerable).
var kindNames = map[Kind]string{
	KindUnknown:           "unknown",
	KindNotFound:          "not_found",
	KindUnauthorized:      "unauthorized",
	KindUnavailable:       "unavailable",
	KindUnsupported:       "unsupported",
	KindInvalid:           "invalid",
	KindTimeout:           "timeout",
	KindRateLimited:       "rate_limited",
	KindContractViolation: "contract_violation",
}

// kinds is kindNames' key set in a stable order, built once.
var kinds = func() []Kind {
	out := make([]Kind, 0, len(kindNames))
	for k := range kindNames {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}()

// Kinds returns the closed failure vocabulary, in ascending order. The
// returned slice is a copy: a caller cannot narrow or extend the contract by
// mutating it.
func Kinds() []Kind { return slices.Clone(kinds) }

// Valid reports whether k is in the closed vocabulary. A Kind that is not is
// not a classification — it is an integer someone converted.
func (k Kind) Valid() bool {
	_, ok := kindNames[k]
	return ok
}

// String renders k's stable wire/log name. A Kind outside the vocabulary
// renders as invalid_kind(N) rather than as "unknown", so it cannot hide
// behind the safe default.
func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("invalid_kind(%d)", int(k))
}

type Error struct {
	Kind Kind
	Op   string // the contract op, e.g. "Resolve"
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Kind, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Kind)
}

func (e *Error) Unwrap() error { return e.Err }

func New(kind Kind, op string, err error) *Error { return &Error{Kind: kind, Op: op, Err: err} }

// KindOf returns the Kind of the first *Error in the chain, else KindUnknown.
// An error that carries no Kind is Unknown, which is the safe default — see
// [Classified] to tell that case apart from a connector that classified its
// failure as Unknown on purpose.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindUnknown
}

// Classified reports whether err carries a Kind from the closed vocabulary —
// that is, whether the connector classified the failure at all.
//
// It exists because KindOf cannot answer that question: it returns
// KindUnknown both for a connector that deliberately said "I could not
// classify this" and for one that returned a bare error the host would have
// to parse. The first is conformant; the second is what the typed vocabulary
// exists to eliminate.
func Classified(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind.Valid()
}

// Retryable reports whether err is transient (Unavailable or Timeout) — the
// kinds where repeating the same call, after a backoff, may succeed with no
// change in caller behaviour.
//
// KindRateLimited is deliberately absent: a rate limit is not waited out by
// retrying into it. It is withheld by the breaker — see [TripsBreaker].
func Retryable(err error) bool {
	k := KindOf(err)
	return k == KindUnavailable || k == KindTimeout
}

// TripsBreaker reports whether err is positive evidence that the backend is
// refusing load, and therefore that a host should open its circuit breaker
// for that backend.
//
// Exactly one Kind qualifies: KindRateLimited. In particular KindUnknown does
// not, and that is the requirement rather than an omission — a breaker opened
// on a guess turns one unrecognised error into a total resolution outage for
// the backend it was meant to protect.
//
// The breaker itself is the host's, not the connector's. This predicate is
// published so that every host and every conformance run reads the same rule,
// instead of each rebuilding it from the Kind list.
func TripsBreaker(err error) bool { return KindOf(err) == KindRateLimited }
