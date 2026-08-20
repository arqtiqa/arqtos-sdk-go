package reduce_test

import (
	"context"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/kernel/reduce"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/tapeformat"
)

// The head is DERIVED from a verified prefix and REPORTED on the outcome.
//
// ⚠️ A decision that does not say which head it was made against cannot be
// reconciled later against a tape that has since advanced — which is the whole
// difficulty acceptance being single-headed exists to remove.
func TestReduce_ReportsTheHeadItDecidedAgainst(t *testing.T) {
	in := input(t)
	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	want := in.Accepted[len(in.Accepted)-1].ActBodyID
	if out.Head != want {
		t.Errorf("the outcome reports head %q, the prefix ends at %q", out.Head, want)
	}

	// It is reported on an ACCEPTANCE too, not only on a refusal.
	genesisOnly := input(t)
	genesisOnly.Accepted = genesisOnly.Accepted[:1]
	genesisOnly.Acts = nil
	acc, err := reduce.Reduce(context.Background(), genesisOnly)
	if err != nil || !acc.Accepted {
		t.Fatalf("the genesis act was not accepted: %v %s", err, acc.Reason)
	}
	if acc.Head != genesisOnly.Accepted[0].ActBodyID {
		t.Errorf("an accepted genesis reports head %q, want %q", acc.Head, genesisOnly.Accepted[0].ActBodyID)
	}
}

// ⚠️ THE DELEGATION, PROVED. The reducer and the tape reader must never disagree
// about what a chain is: two implementations of one rule is exactly what the
// public-kernel boundary exists to prevent, and they would diverge on the cases
// neither test happens to cover.
//
// So every mutation below is applied to a prefix and BOTH are asked. Their
// verdicts must agree — not merely both be non-empty.
func TestReduce_AgreesWithTheTapeReaderAboutWhatAChainIs(t *testing.T) {
	base := input(t).Accepted

	mutations := map[string]func([]tapeformat.Entry) []tapeformat.Entry{
		"unbroken": func(e []tapeformat.Entry) []tapeformat.Entry { return e },
		"a gap in the sequence": func(e []tapeformat.Entry) []tapeformat.Entry {
			e[1].Sequence = "5"
			return e
		},
		"a broken parent link": func(e []tapeformat.Entry) []tapeformat.Entry {
			e[1].ParentActBodyID = "sha256:" + strings.Repeat("e", 64)
			return e
		},
		"the first entry names a parent": func(e []tapeformat.Entry) []tapeformat.Entry {
			e[0].ParentActBodyID = "sha256:" + strings.Repeat("d", 64)
			return e
		},
		"one act at two positions": func(e []tapeformat.Entry) []tapeformat.Entry {
			e[1].ActBodyID = e[0].ActBodyID
			e[1].ParentActBodyID = e[0].ActBodyID
			return e
		},
		"an unusable entry": func(e []tapeformat.Entry) []tapeformat.Entry {
			e[1].Sequence = ""
			return e
		},
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			entries := append([]tapeformat.Entry(nil), base...)
			entries = mutate(entries)

			readerErr := tapeformat.VerifyChain(entries)

			in := input(t)
			in.Accepted = entries
			out, err := reduce.Reduce(context.Background(), in)
			if err != nil {
				t.Fatalf("Reduce returned an error rather than a decision: %v", err)
			}

			readerRefuses := readerErr != nil
			reducerRefusesTheChain := strings.Contains(out.Reason, "not a chain")

			if readerRefuses != reducerRefusesTheChain {
				t.Fatalf("the reducer and the tape reader disagree about this prefix.\n"+
					"  reader:  %v\n  reducer: %q\n\n"+
					"They must apply ONE definition of a chain. The reducer delegates to "+
					"tapeformat.VerifyChain precisely so the public verifier and this runtime cannot "+
					"diverge on a case neither test covers.", readerErr, out.Reason)
			}
			if readerRefuses && !strings.Contains(out.Reason, readerErr.Error()) {
				t.Errorf("the refusal %q does not carry the reader's reason (%v), so the break is not named",
					out.Reason, readerErr)
			}
		})
	}

	// ⚠️ Count-asserted, and the unbroken case is in the table on purpose: a
	// comparison that only ever saw broken prefixes would agree trivially.
	if len(mutations) != 6 {
		t.Fatalf("compared %d prefixes, want 6", len(mutations))
	}
}

// An empty prefix has no head. Genesis is an entry on the tape, not an absence
// of one, so "nothing accepted yet" is not a state a candidate can be judged in.
func TestReduce_RefusesAPrefixWithNoHead(t *testing.T) {
	in := input(t)
	in.Accepted = nil
	out, err := reduce.Reduce(context.Background(), in)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if out.Accepted {
		t.Fatal("a candidate was judged against an empty prefix")
	}
	if out.Head != "" {
		t.Errorf("the outcome reports head %q for an empty prefix", out.Head)
	}
}
