package cerr_test

import (
	"errors"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

func TestKindOfAndRetryable(t *testing.T) {
	base := errors.New("boom")
	e := cerr.New(cerr.KindUnavailable, "Resolve", base)

	if cerr.KindOf(e) != cerr.KindUnavailable {
		t.Fatalf("KindOf = %v", cerr.KindOf(e))
	}
	if !errors.Is(e, base) {
		t.Fatalf("Unwrap chain broken")
	}
	if !cerr.Retryable(e) {
		t.Fatalf("Unavailable must be retryable")
	}
	if cerr.Retryable(cerr.New(cerr.KindNotFound, "Resolve", nil)) {
		t.Fatalf("NotFound must not be retryable")
	}
	if cerr.KindOf(base) != cerr.KindUnknown {
		t.Fatalf("plain error must classify as Unknown")
	}
}

// TestVocabularyIsClosed pins the failure vocabulary by name. It is the
// mechanism that makes adding a class a DELIBERATE contract change: a new
// Kind that is not added here fails this test, and one that is added here is
// visible in the diff of a published contract.
func TestVocabularyIsClosed(t *testing.T) {
	want := map[cerr.Kind]string{
		cerr.KindUnknown:           "unknown",
		cerr.KindNotFound:          "not_found",
		cerr.KindUnauthorized:      "unauthorized",
		cerr.KindUnavailable:       "unavailable",
		cerr.KindRateLimited:       "rate_limited",
		cerr.KindUnsupported:       "unsupported",
		cerr.KindInvalid:           "invalid",
		cerr.KindTimeout:           "timeout",
		cerr.KindContractViolation: "contract_violation",
	}

	got := cerr.Kinds()
	if len(got) != len(want) {
		t.Fatalf("Kinds() has %d entries, the pinned vocabulary has %d: %v", len(got), len(want), got)
	}

	seen := map[string]cerr.Kind{}
	for _, k := range got {
		name, ok := want[k]
		if !ok {
			t.Fatalf("Kinds() contains %v (%q), which is not in the pinned vocabulary", k, k.String())
		}
		if k.String() != name {
			t.Fatalf("Kind %v renders %q, pinned as %q", k, k.String(), name)
		}
		if prev, dup := seen[name]; dup {
			t.Fatalf("Kinds %v and %v both render %q; a Kind with no String case is indistinguishable from another", k, prev, name)
		}
		seen[name] = k
		if !k.Valid() {
			t.Fatalf("Kind %v is in Kinds() but reports Valid() == false", k)
		}
	}
}

func TestKindOutsideTheVocabularyIsNotValid(t *testing.T) {
	rogue := cerr.Kind(9999)
	if rogue.Valid() {
		t.Fatalf("Kind(9999) must not be Valid")
	}
	if rogue.String() == cerr.KindUnknown.String() {
		t.Fatalf("an out-of-vocabulary Kind must not render as %q — that hides it behind the safe default", cerr.KindUnknown)
	}
}

// TestUnknownDoesNotTripTheBreaker is REQ-ARQ-P-19's load-bearing default,
// driven by a FABRICATED error rather than a live backend: escalating on a
// guess turns one unrecognised error into a total resolution outage for that
// backend, which is worse than the rate limit the breaker guards.
func TestUnknownDoesNotTripTheBreaker(t *testing.T) {
	fabricated := cerr.New(cerr.KindUnknown, "Resolve", errors.New("fabricated: something the connector could not classify"))
	if cerr.TripsBreaker(fabricated) {
		t.Fatalf("KindUnknown must NOT trip the breaker")
	}

	// The same holds for a failure that never passed through cerr.New at all:
	// it classifies as Unknown, and Unknown does not escalate.
	if cerr.TripsBreaker(errors.New("fabricated: 429 too many requests, slow down")) {
		t.Fatalf("an unclassified error must NOT trip the breaker, whatever its text says")
	}
}

func TestRateLimitedTripsTheBreaker(t *testing.T) {
	fabricated := cerr.New(cerr.KindRateLimited, "Resolve", errors.New("fabricated: quota exhausted"))
	if !cerr.TripsBreaker(fabricated) {
		t.Fatalf("KindRateLimited must trip the breaker")
	}
	// A rate limit is withheld by the breaker, never retried into the wall.
	if cerr.Retryable(fabricated) {
		t.Fatalf("KindRateLimited must not be Retryable")
	}
	for _, k := range cerr.Kinds() {
		if k == cerr.KindRateLimited {
			continue
		}
		if cerr.TripsBreaker(cerr.New(k, "Resolve", nil)) {
			t.Fatalf("only KindRateLimited trips the breaker; %v did", k)
		}
	}
}

func TestContractViolationIsNeitherRetryableNorBreakerTripping(t *testing.T) {
	e := cerr.New(cerr.KindContractViolation, "Resolve", nil)
	if cerr.Retryable(e) {
		t.Fatalf("a broken connector does not get better on retry")
	}
	if cerr.TripsBreaker(e) {
		t.Fatalf("a contract violation is the connector's fault, not backend load")
	}
}

// TestClassified separates "typed as Unknown" from "never typed at all" — the
// distinction a host needs to tell a connector that classified its failure
// from one whose error text the host would otherwise have to parse.
func TestClassified(t *testing.T) {
	if !cerr.Classified(cerr.New(cerr.KindUnknown, "Resolve", nil)) {
		t.Fatalf("an explicit KindUnknown is classified")
	}
	if cerr.Classified(errors.New("rate-limited: too many requests")) {
		t.Fatalf("a plain error is NOT classified, however classifiable its text looks")
	}
	if cerr.Classified(nil) {
		t.Fatalf("nil is not a classified failure")
	}
	if cerr.Classified(cerr.New(cerr.Kind(9999), "Resolve", nil)) {
		t.Fatalf("a Kind outside the closed vocabulary is not a classification")
	}
}
