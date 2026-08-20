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

	// Head is the entry the decision was made relative to: the last entry of a
	// verified accepted prefix.
	//
	// ⚠️ REPORTED, not assumed. Acceptance is single-headed, and a decision
	// that does not say which head it was made against cannot be reconciled
	// later against a tape that has since advanced.
	Head string

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

	// ⚠️ THE PREFIX MUST BE A CHAIN BEFORE IT CAN HAVE A HEAD. A reducer that
	// judged a candidate against a broken prefix would be deciding against a
	// history that does not exist.
	//
	// ⚠️ AND THE RULE IS DELEGATED, not reimplemented. tapeformat.VerifyChain
	// is the one definition of what a chain is, and the public verifier applies
	// it too — a second implementation here is precisely what the public-kernel
	// boundary exists to prevent, and the two would disagree on the cases
	// neither test happens to cover.
	if err := tapeformat.VerifyChain(in.Accepted); err != nil {
		return refused("the accepted prefix is not a chain, so it has no head to accept against: %v", err), nil
	}
	head := in.Accepted[len(in.Accepted)-1].ActBodyID

	if out, decided := timeRule(in, head); decided {
		return out, nil
	}

	// Replaying the bootstrap alone: the act is accepted, and the authority it
	// establishes is carried out.
	if len(in.Accepted) == 1 && in.Candidate == nil {
		return Outcome{
			Accepted: true,
			Head:     head,
			// ⚠️ COPIED, not aliased. An outcome sharing the caller's backing
			// array lets a mutation of one silently change the other, which is
			// exactly the defect that made a golden fixture drift earlier in
			// this project.
			RootKeys:  append([]contracts.RootKey(nil), in.Genesis.RootKeys...),
			RootGrant: in.Genesis.RootGrant,
		}, nil
	}

	if in.Candidate == nil {
		out := refused("no rule in this build admits a replay of %d accepted entr(y/ies) with no candidate",
			len(in.Accepted))
		out.Head = head
		return out, nil
	}

	if out, decided := spendRule(in, head); decided {
		return out, nil
	}
	if out, decided := treeRule(in, head); decided {
		return out, nil
	}

	out := refused("no rule in this build admits this candidate")
	out.Head = head
	return out, nil
}

// timeRule refuses a tape whose acceptance times do not run forwards.
//
// ⚠️ THIS IS A PROPERTY OF THE SEQUENCE, not of a pair of values, which is why
// it lives in the reducer and not beside AcceptedTime. The pair comparison it
// rests on — AcceptedTime.NotBefore — is the contract's; the sequence is the
// tape's, and only something holding the whole tape can see a regression.
//
// ⚠️ AND TWO AUTHORITIES ARE NOT COMPARABLE. A regression is only a regression
// if both times came from the same clock. Comparing two clocks is how a
// rolled-back one buys a later position, and "I could not tell" must never read
// as "in order" — so a tape whose authority changes is refused with its OWN
// reason. Incomparable and out-of-order are different findings: one is a
// configuration fact about the acceptors, the other is a clock that moved
// backwards, and they send an operator to different places.
func timeRule(in Input, head string) (Outcome, bool) {
	for i := 1; i < len(in.Accepted); i++ {
		prev := in.Accepted[i-1].AcceptedAt
		cur := in.Accepted[i].AcceptedAt

		if cur.Authority != prev.Authority {
			out := refused("entries %d and %d record acceptance under different authorities (%q and %q), "+
				"whose clocks are not comparable; the tape's order cannot be checked across them",
				i-1, i, prev.Authority.Name, cur.Authority.Name)
			out.Head = head
			return out, true
		}

		// ⚠️ NOT-BEFORE, not strictly-after. Two acts accepted in the same
		// instant are ordered by their positions on the tape, and refusing
		// equal times would refuse a correct tape whose clock is simply
		// coarser than its acceptance rate.
		if !cur.NotBefore(prev) {
			out := refused("entry %d was accepted at %s and entry %d at %s under the one authority %q — "+
				"the tape's acceptance times regress, which is a rolled-back clock or a reordered chain",
				i-1, prev.At, i, cur.At, cur.Authority.Name)
			out.Head = head
			return out, true
		}
	}
	return Outcome{}, false
}

// ObservedTreeKey is the EvidenceEvent detail an observation reports the tree it
// actually saw under.
const ObservedTreeKey = "observed_tree_oid"

// treeRule refuses a candidate observed against a tree its signed body does not
// bind.
//
// ⭐ THIS IS THE W2 KILL GATE. If a tree swap is not caught here, the design's
// central claim does not hold: an attacker with a valid signature over one tree
// can have a DIFFERENT tree merged, and every downstream artefact — the receipt,
// the evidence bundle, the verifier's replay — agrees with itself while
// describing something that did not happen.
//
// ⚠️ Everything about such an act is valid. The signature verifies, the DSSE
// binding is intact, the act body id is correct. Nothing inside the act is
// wrong; only the comparison of BOUND against OBSERVED can find it, which is
// why no amount of checking the act alone would.
func treeRule(in Input, head string) (Outcome, bool) {
	candidate := *in.Candidate
	if candidate.CandidateTreeOID == "" {
		out := refused("the candidate binds no tree, so no effect can be checked against it")
		out.Head = head
		return out, true
	}

	seen := 0
	for _, ev := range in.Observations {
		// ⚠️ An observation about a DIFFERENT act says nothing about this one.
		// Applying it would refuse an innocent candidate on someone else's
		// evidence, which is a false positive that erodes trust in the gate.
		if ev.ActBodyID != candidate.ActBodyID {
			continue
		}
		observed, ok := ev.Detail[ObservedTreeKey]
		if !ok || observed == "" {
			continue
		}
		seen++
		if observed != candidate.CandidateTreeOID {
			out := refused("the candidate binds tree %s and was observed against %s; the effect is not "+
				"the effect that was authorised", candidate.CandidateTreeOID, observed)
			out.Head = head
			return out, true
		}
	}

	// ⚠️ NO OBSERVATION IS NOT A MATCH. Absence of evidence is not evidence of
	// absence, and an act nobody watched must not be accepted AS IF the effect
	// had been seen to match — that is precisely the state a tree swap arrives
	// in when the observer is degraded. The refusal says which it is, so the
	// two are never confused in a report.
	if seen == 0 {
		out := refused("no observation reports a tree for this act, so the effect cannot be compared "+
			"against the tree it binds (%s); an unobserved act is not a matching one",
			candidate.CandidateTreeOID)
		out.Head = head
		return out, true
	}
	return Outcome{}, false
}

// spendRule refuses a candidate whose permit the accepted prefix already spent.
//
// ⚠️ The conservation law is NON-AMPLIFICATION, NOT QUANTITY CONSERVATION.
// Nothing counts permits and a grant may issue many; what is forbidden is one
// permit reaching two accepted acts.
//
// ⚠️ And under E3-H this law is DETECTIVE. The host does not consume permits, so
// acceptance guarantees at most one arqtos acceptance — unmatched or duplicated
// HOST effects are caught by reconciliation, not prevented here. Reading this
// rule as a preventative one is how the residual gets lost.
func spendRule(in Input, head string) (Outcome, bool) {
	candidate := *in.Candidate
	if candidate.Permit.IssuerActBodyID == "" {
		out := refused("the candidate names no permit; an act with no authorisation is not admissible")
		out.Head = head
		return out, true
	}

	// ⚠️ Walk the ACCEPTED ENTRIES, not the Acts map. The tape is what was
	// accepted; the map is a lookup beside it. An act present in the map but
	// not on the tape was never accepted and spends nothing, and iterating the
	// map would silently include it.
	for i, entry := range in.Accepted {
		if i == 0 {
			// The genesis act establishes authority rather than consuming it.
			continue
		}
		act, ok := in.Acts[entry.ActBodyID]
		if !ok {
			// ⚠️ AN ABSENT RECORD IS NOT EVIDENCE OF ABSENCE. An accepted entry
			// whose act is missing might have spent this very permit, and
			// treating it as unspent would let a double-spend through by
			// withholding a file.
			out := refused("entry %d records act %s, whose ActSpec is missing, so it cannot be shown "+
				"not to have spent this permit", i, entry.ActBodyID)
			out.Head = head
			return out, true
		}
		if act.Permit == candidate.Permit {
			out := refused("permit %s#%s is already spent by %s at entry %d",
				candidate.Permit.IssuerActBodyID, candidate.Permit.OutputIndex, entry.ActBodyID, i)
			out.Head = head
			return out, true
		}
	}
	return Outcome{}, false
}
