package reduce

import (
	"context"
	"errors"
	"fmt"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/tapeformat"
)

// ErrUndigestible is returned when an input cannot be encoded at all.
//
// ⚠️ IT IS NOT A REFUSAL. A refusal is a DECISION and comes back as an
// [Outcome] with a reason; an error means the reducer could not decide, which
// is a different thing with a different response. Collapsing them would let a
// caller read "we could not read this" as "this is inadmissible", and the two
// lead to opposite recoveries.
var ErrUndigestible = errors.New("reduce: an input cannot be canonically encoded")

// An Input is what a reduction is computed over.
//
// ⚠️ THE SHAPE OF THIS TYPE IS PROVISIONAL and the fixtures do not depend on it
// beyond naming these five things. What is SETTLED is that reduction returns an
// [Outcome] carrying a REASON — that is what the kill-gate fixtures assert, and
// it is what makes a refusal reconcilable against the tape by someone auditing
// it.
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

	// RootKeys and RootGrant are the authority the genesis act established.
	//
	// ⚠️ Carried out so an accepted genesis can be shown to have ESTABLISHED
	// something rather than merely to have been waved through — an acceptance
	// that names no authority is indistinguishable from a reducer that returns
	// true.
	RootKeys  []contracts.RootKey
	RootGrant contracts.Grant
}

// refused builds a refusal. It exists so no code path can produce one without
// saying why.
func refused(format string, args ...any) Outcome {
	return Outcome{Reason: fmt.Sprintf(format, args...)}
}

// Reduce judges a candidate against the accepted tape.
//
// ⚠️ IT NEVER RETURNS AN ERROR FOR A DECIDABLE INPUT. A refusal is a decision:
// Outcome{Accepted:false, Reason:…} with a nil error. An error means the reducer
// could not decide at all, which today is only an input that cannot be encoded.
//
// ⚠️ AND IT REFUSES BY DEFAULT. An act no rule admits is refused, never accepted
// — so a rule that has not been written yet cannot let something through, and
// each rule added replaces a general refusal with a specific one.
//
// # Purity
//
// No filesystem, no environment, no clock, no network, and it returns rather
// than invokes at any depth. A reducer that read the wall clock would produce a
// different answer on replay than it did at acceptance, which is the one thing
// replay exists to detect. A test in this package walks its own source and fails
// on any such access.
func Reduce(ctx context.Context, in Input) (Outcome, error) {
	if err := in.Genesis.Validate(); err != nil {
		return refused("the genesis act is unusable: %v", err), nil
	}
	genesisID, err := in.Genesis.ID()
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: the genesis act: %w", ErrUndigestible, err)
	}

	if len(in.Accepted) == 0 {
		return refused("the tape is empty; genesis is an entry on it, not an absence"), nil
	}

	// ⚠️ The tape must BEGIN at the genesis act, and the comparison is against
	// the act's own digest rather than a recorded id. The two live under
	// different domain tags, so an act body id is not interchangeable with a
	// genesis id — a tape that started at one would be a chain rooted in
	// something nothing vouches for.
	if got := in.Accepted[0].ActBodyID; got != genesisID {
		return refused("the tape begins at %s and the genesis act digests to %s; a chain must be "+
			"rooted in the genesis it claims", got, genesisID), nil
	}

	// Replaying the bootstrap alone: the act is accepted, and the authority it
	// establishes is carried out.
	if len(in.Accepted) == 1 && in.Candidate == nil {
		return Outcome{
			Accepted: true,
			// ⚠️ COPIED, not aliased. An outcome sharing the caller's backing
			// array lets a mutation of one silently change the other, which is
			// exactly the defect that made a golden fixture drift earlier in
			// this project.
			RootKeys:  append([]contracts.RootKey(nil), in.Genesis.RootKeys...),
			RootGrant: in.Genesis.RootGrant,
		}, nil
	}

	return refused("no rule in this build admits this input; %d accepted entr(y/ies), candidate=%v",
		len(in.Accepted), in.Candidate != nil), nil
}
