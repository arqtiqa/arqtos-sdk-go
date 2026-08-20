package reduce

import (
	"context"
	"errors"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/tapeformat"
)

// ErrNotImplemented is returned by [Reduce] for every input in this build.
//
// ⚠️ It exists so a caller can distinguish "this build cannot decide" from "this
// act is inadmissible" — two conclusions with opposite responses, which a
// generic failure would collapse. The fixtures depend on the distinction: a
// fixture asserting "the candidate is refused" would PASS against a function
// that refuses everything, which is why they assert the REASON.
var ErrNotImplemented = errors.New("reduce: the reducer is not implemented in this build")

// An Input is what a reduction is computed over.
//
// ⚠️ THE SHAPE OF THIS TYPE IS PROVISIONAL and the fixtures do not depend on it
// beyond naming these five things. What is SETTLED is that reduction returns an
// [Outcome] carrying a REASON — that is what the kill-gate fixtures assert, and
// it is what makes a refusal reconcilable against the tape by someone auditing
// it. How the inputs are carried is decided when the reducer is built.
type Input struct {
	// Genesis is the ledger bootstrap the chain traces to.
	Genesis contracts.RepositoryGenesis
	// Accepted is the tape so far, root-first.
	Accepted []tapeformat.Entry
	// Acts are the acts the accepted entries name, by act body id.
	Acts map[string]contracts.ActSpec
	// Candidate is the act being judged. It is nil when the reduction is of
	// the genesis act alone.
	Candidate *contracts.ActSpec
	// Observations are what the observer independently saw. A tree swap is
	// only visible by comparing one of these against the act's bound tree.
	Observations []contracts.EvidenceEvent
}

// An Outcome is a reduction's verdict.
type Outcome struct {
	// Accepted says whether the candidate may join the tape.
	Accepted bool

	// Reason says why not, when it may not.
	//
	// ⚠️ A refusal with no reason is not a usable refusal. Someone auditing the
	// tape has to be able to reconcile a rejection against what is on it, and
	// "no" does not let them. Every kill-gate fixture asserts on this field
	// rather than on the presence of an error, because a reducer that refused
	// everything would satisfy the weaker assertion.
	Reason string

	// RootKeys and RootGrant are the authority the genesis act established,
	// carried out so an accepted genesis can be checked to have established
	// something rather than merely to have been waved through.
	RootKeys  []contracts.RootKey
	RootGrant contracts.Grant
}

// Reduce judges a candidate against the accepted tape.
//
// ⚠️ In this build it decides nothing and returns [ErrNotImplemented] for every
// input. That inability is deliberate and load-bearing, exactly as it is in the
// public verifier: before the reducer exists, the one behaviour this function
// must not have is the ability to report a verdict — a caller that got one would
// be told an act was admissible by code that never looked at it.
//
// The reducer that implements this is separate, deliberate work, and the
// fixtures beside it were written first so it has to satisfy a check it did not
// author.
func Reduce(ctx context.Context, in Input) (Outcome, error) {
	return Outcome{}, ErrNotImplemented
}
