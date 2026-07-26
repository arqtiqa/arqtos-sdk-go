package credential_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// mustRef parses a metavariable-form op:// reference for use as test input.
func mustRef(t *testing.T, s string) ref.Ref {
	t.Helper()
	r, err := ref.Parse(s)
	if err != nil {
		t.Fatalf("ref.Parse(%q): %v", s, err)
	}
	return r
}

// TestResolvedRefusesEmptyMaterial is REQ-ARQ-P-17 at the point of the
// mistake. The backend behaviour it models is real and recurring: a
// signed-out read returns EMPTY OUTPUT WITH EXIT CODE 0, so a connector that
// forwards what it got has an empty value and no error to report.
//
// It cannot report that as a success: the constructor hands back a fault
// instead, and the Resolution it hands back with it is unreadable.
func TestResolvedRefusesEmptyMaterial(t *testing.T) {
	for _, tc := range []struct {
		name string
		mat  *credential.Material
	}{
		{"nil material", nil},
		{"zero-length material", credential.NewMaterial(nil)},
		{"empty material", credential.NewMaterial([]byte{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := credential.Resolved(tc.mat)
			if err == nil {
				t.Fatalf("Resolved(%s) returned no error: (empty value, nil error) must not be constructible", tc.name)
			}
			var fe *credential.FaultError
			if !errors.As(err, &fe) {
				t.Fatalf("Resolved(%s) error is %T, want *credential.FaultError — a broken connector must be named, not coerced into a generic error", tc.name, err)
			}
			if fe.Fault != credential.FaultUnresolved {
				t.Fatalf("Fault = %q, want %q", fe.Fault, credential.FaultUnresolved)
			}
			if cerr.KindOf(err) != cerr.KindContractViolation {
				t.Fatalf("KindOf = %v, want KindContractViolation", cerr.KindOf(err))
			}
			if _, verr := res.Value(); verr == nil {
				t.Fatalf("the Resolution returned alongside the fault must not be readable")
			}
		})
	}
}

// TestZeroResolutionIsUnreadable closes the other half of REQ-ARQ-P-17: the
// zero Resolution — what a connector returning `Resolution{}, nil` produces —
// cannot be read as an empty credential. There is no accessor that yields
// "" from it.
func TestZeroResolutionIsUnreadable(t *testing.T) {
	var zero credential.Resolution
	mat, err := zero.Value()
	if err == nil {
		t.Fatalf("the zero Resolution must not read as a value")
	}
	if mat != nil {
		t.Fatalf("a failed Value() must not hand back material (%v)", mat)
	}
	var fe *credential.FaultError
	if !errors.As(err, &fe) || fe.Fault != credential.FaultUnresolved {
		t.Fatalf("Value() error = %v, want a FaultError of %q", err, credential.FaultUnresolved)
	}
}

// TestResolvedEmptyIsPresentAndDistinctFromUnresolved is the other side of
// the same requirement: an operator's genuinely-empty secret is expressible,
// but only by SAYING SO. Emptiness is never inferred from the bytes.
func TestResolvedEmptyIsPresentAndDistinctFromUnresolved(t *testing.T) {
	res := credential.ResolvedEmpty()
	mat, err := res.Value()
	if err != nil {
		t.Fatalf("ResolvedEmpty().Value() = %v, want a present, empty value", err)
	}
	if mat == nil {
		t.Fatalf("ResolvedEmpty() must yield material, empty but present")
	}
	if len(mat.Reveal()) != 0 {
		t.Fatalf("ResolvedEmpty() material = %d bytes, want 0", len(mat.Reveal()))
	}

	// The distinction that matters: a deliberate empty reads; an unresolved
	// one does not.
	if _, err := (credential.Resolution{}).Value(); err == nil {
		t.Fatalf("unresolved must not read like ResolvedEmpty")
	}
}

// TestResolutionStringDistinguishesUnresolvedFromRedacted: the rendering is
// for a log, and a log that renders both states identically cannot tell an
// operator which one they are looking at.
func TestResolutionStringDistinguishesUnresolvedFromRedacted(t *testing.T) {
	resolved, err := credential.Resolved(credential.NewMaterial([]byte("placeholder-value")))
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	unresolved := credential.Resolution{}
	if unresolved.String() == resolved.String() {
		t.Fatalf("an unresolved Resolution must not render like a resolved one (%q)", unresolved.String())
	}
	if !strings.Contains(unresolved.String(), "UNRESOLVED") {
		t.Fatalf("unresolved renders as %q", unresolved.String())
	}
	if unresolved.GoString() != unresolved.String() {
		t.Fatalf("GoString must redact the same way: %q", unresolved.GoString())
	}
}

func TestResolvedRoundTripsMaterial(t *testing.T) {
	res, err := credential.Resolved(credential.NewMaterial([]byte("placeholder-value")))
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	mat, err := res.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if string(mat.Reveal()) != "placeholder-value" {
		t.Fatalf("Reveal = %q", mat.Reveal())
	}
}

// TestResolutionRedactsOnFormat: a Resolution is carried through host code and
// may be logged there. It must redact exactly as Material does.
func TestResolutionRedactsOnFormat(t *testing.T) {
	secret := []byte("s3cret")
	res, err := credential.Resolved(credential.NewMaterial(secret))
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		got := fmt.Sprintf(verb, res)
		if leaksSecretBytes(got, secret) {
			t.Fatalf("%s of a Resolution leaks material: %q", verb, got)
		}
	}
	if got := fmt.Sprintf("%+v", struct{ R credential.Resolution }{R: res}); leaksSecretBytes(got, secret) {
		t.Fatalf("%%+v of a struct embedding a Resolution leaks material: %q", got)
	}
}

// TestCheckResolutionNamesTheConnector is the host-side half of
// REQ-ARQ-P-17: the violation is rejected AND attributed, so a broken
// connector cannot hide behind a generic error.
func TestCheckResolutionNamesTheConnector(t *testing.T) {
	_, err := credential.CheckResolution("placeholder-loader", "Resolve", credential.Resolution{}, nil)
	if err == nil {
		t.Fatalf("(unresolved, nil error) must be rejected by the host")
	}
	var fe *credential.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *credential.FaultError", err)
	}
	if fe.Connector != "placeholder-loader" {
		t.Fatalf("Connector = %q, want the connector to be named", fe.Connector)
	}
	if fe.Op != "Resolve" {
		t.Fatalf("Op = %q", fe.Op)
	}
	if cerr.Retryable(err) {
		t.Fatalf("a broken connector does not get better on retry")
	}
	if cerr.TripsBreaker(err) {
		t.Fatalf("a contract violation is not backend load and must not open the breaker")
	}
}

// TestCheckResolutionAttributesAConnectorlessFault: a fault raised inside the
// connector (by Resolved) carries no name, because a connector does not know
// what the host registered it as. The host supplies the name.
func TestCheckResolutionAttributesAConnectorlessFault(t *testing.T) {
	res, faulted := credential.Resolved(credential.NewMaterial(nil))
	_, err := credential.CheckResolution("placeholder-loader", "Resolve", res, faulted)
	var fe *credential.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *credential.FaultError", err)
	}
	if fe.Connector != "placeholder-loader" {
		t.Fatalf("Connector = %q, want the host's name for the connector", fe.Connector)
	}
	if fe.Fault != credential.FaultUnresolved {
		t.Fatalf("Fault = %q, want the original fault preserved", fe.Fault)
	}
}

func TestCheckResolutionPassesTypedFailuresThrough(t *testing.T) {
	want := cerr.New(cerr.KindNotFound, "Resolve", nil)
	_, err := credential.CheckResolution("placeholder-loader", "Resolve", credential.Resolution{}, want)
	if !errors.Is(err, want) {
		t.Fatalf("a typed failure must reach the caller unchanged, got %v", err)
	}
	var fe *credential.FaultError
	if errors.As(err, &fe) {
		t.Fatalf("a connector that failed correctly must not be reported as at fault")
	}
}

func TestCheckResolutionAcceptsAValidResolution(t *testing.T) {
	in, err := credential.Resolved(credential.NewMaterial([]byte("placeholder-value")))
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	out, err := credential.CheckResolution("placeholder-loader", "Resolve", in, nil)
	if err != nil {
		t.Fatalf("a conformant resolution must pass the guard: %v", err)
	}
	mat, err := out.Value()
	if err != nil || string(mat.Reveal()) != "placeholder-value" {
		t.Fatalf("guard must not alter the value: %v %v", mat, err)
	}
	// A deliberately-empty secret is conformant too.
	if _, err := credential.CheckResolution("placeholder-loader", "Resolve", credential.ResolvedEmpty(), nil); err != nil {
		t.Fatalf("an explicitly-empty resolution must pass the guard: %v", err)
	}
}

func TestCheckBatch(t *testing.T) {
	a := mustRef(t, "op://<vault>/<item>/<field>")
	b := mustRef(t, "op://<vault>/<other-item>/<field>")
	ok, err := credential.Resolved(credential.NewMaterial([]byte("placeholder-value")))
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}

	resolvedA := mustBatchResolved(t, a, ok)
	resolvedB := mustBatchResolved(t, b, ok)
	failedB := mustBatchFailed(t, b, cerr.New(cerr.KindNotFound, "ResolveBatch", nil))

	t.Run("accepts results that correspond to the request", func(t *testing.T) {
		in := []credential.BatchResult{resolvedA, failedB}
		if _, err := credential.CheckBatch("placeholder-loader", []ref.Ref{a, b}, in, nil); err != nil {
			t.Fatalf("conformant batch rejected: %v", err)
		}
	})

	t.Run("rejects a short result set", func(t *testing.T) {
		in := []credential.BatchResult{resolvedA}
		_, err := credential.CheckBatch("placeholder-loader", []ref.Ref{a, b}, in, nil)
		assertFault(t, err, credential.FaultBatchMismatch)
	})

	t.Run("rejects results out of order", func(t *testing.T) {
		in := []credential.BatchResult{resolvedB, resolvedA}
		_, err := credential.CheckBatch("placeholder-loader", []ref.Ref{a, b}, in, nil)
		assertFault(t, err, credential.FaultBatchMismatch)
	})

	t.Run("rejects an element with neither value nor error", func(t *testing.T) {
		// The blank a refusing constructor hands back: it keeps the ref, so
		// the batch lines up positionally and the fault is the empty outcome
		// rather than a mismatch.
		blank, err := credential.BatchResolved(b, credential.Resolution{})
		if err == nil {
			t.Fatalf("BatchResolved must refuse an unresolved Resolution")
		}
		in := []credential.BatchResult{resolvedA, blank}
		_, err = credential.CheckBatch("placeholder-loader", []ref.Ref{a, b}, in, nil)
		assertFault(t, err, credential.FaultUnresolved)
	})

	t.Run("passes a whole-call failure through", func(t *testing.T) {
		want := cerr.New(cerr.KindUnavailable, "ResolveBatch", nil)
		if _, err := credential.CheckBatch("placeholder-loader", []ref.Ref{a}, nil, want); !errors.Is(err, want) {
			t.Fatalf("whole-call failure must reach the caller unchanged, got %v", err)
		}
	})
}

func assertFault(t *testing.T, err error, want credential.Fault) {
	t.Helper()
	var fe *credential.FaultError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %v (%T), want a *credential.FaultError of %q", err, err, want)
	}
	if fe.Fault != want {
		t.Fatalf("Fault = %q, want %q", fe.Fault, want)
	}
	if fe.Connector != "placeholder-loader" {
		t.Fatalf("Connector = %q, want the connector named", fe.Connector)
	}
}

func TestFaultErrorMessageNamesConnectorOpAndFault(t *testing.T) {
	e := &credential.FaultError{Connector: "placeholder-loader", Op: "Resolve", Fault: credential.FaultUnresolved, Detail: "detail-here"}
	msg := e.Error()
	for _, want := range []string{"placeholder-loader", "Resolve", string(credential.FaultUnresolved), "detail-here"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("FaultError message %q omits %q", msg, want)
		}
	}
	// An unnamed fault (raised inside a connector) still renders.
	if (&credential.FaultError{Fault: credential.FaultUnresolved}).Error() == "" {
		t.Fatalf("an unattributed fault must still render")
	}
}

// mustBatchResolved builds a resolved BatchResult, failing the test if the
// constructor refuses it.
func mustBatchResolved(t *testing.T, r ref.Ref, res credential.Resolution) credential.BatchResult {
	t.Helper()
	b, err := credential.BatchResolved(r, res)
	if err != nil {
		t.Fatalf("BatchResolved(%s): %v", r, err)
	}
	return b
}

// mustBatchFailed builds a failed BatchResult, failing the test if the
// constructor refuses it.
func mustBatchFailed(t *testing.T, r ref.Ref, err error) credential.BatchResult {
	t.Helper()
	b, cerr := credential.BatchFailed(r, err)
	if cerr != nil {
		t.Fatalf("BatchFailed(%s): %v", r, cerr)
	}
	return b
}

// TestBatchResultCannotCarryBothOutcomes: "never both and never neither" was
// a doc comment before it was a property. With three exported fields a
// connector could fill in a resolution AND an error, and two hosts reading
// the same result would disagree about what happened. There is now no way to
// express it: each constructor sets exactly one outcome, and nothing else
// can set either.
func TestBatchResultCannotCarryBothOutcomes(t *testing.T) {
	r := mustRef(t, "op://<vault>/<item>/<field>")
	ok, err := credential.Resolved(credential.NewMaterial([]byte("placeholder-value")))
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}

	resolved := mustBatchResolved(t, r, ok)
	if resolved.Err() != nil {
		t.Fatalf("a resolved BatchResult carries an error: %v", resolved.Err())
	}
	mat, verr := resolved.Resolution().Value()
	if verr != nil || string(mat.Reveal()) != "placeholder-value" {
		t.Fatalf("resolved BatchResult did not keep its value: %v %v", mat, verr)
	}
	if resolved.Ref() != r {
		t.Fatalf("Ref() = %v, want %v", resolved.Ref(), r)
	}

	failure := cerr.New(cerr.KindNotFound, "ResolveBatch", nil)
	failed := mustBatchFailed(t, r, failure)
	if !errors.Is(failed.Err(), failure) {
		t.Fatalf("Err() = %v, want the failure it was built with", failed.Err())
	}
	if _, verr := failed.Resolution().Value(); verr == nil {
		t.Fatalf("a failed BatchResult must not carry a readable resolution")
	}
	if failed.Ref() != r {
		t.Fatalf("Ref() = %v, want %v", failed.Ref(), r)
	}
}

// TestBatchResultCannotCarryNeitherOutcome: the other half. A constructor
// asked for an outcome that is not one refuses, and — mirroring
// credential.Resolved — the value it hands back alongside the error carries
// no outcome, so ignoring the error cannot launder a blank into a result.
func TestBatchResultCannotCarryNeitherOutcome(t *testing.T) {
	r := mustRef(t, "op://<vault>/<item>/<field>")

	t.Run("resolved with nothing resolved", func(t *testing.T) {
		got, err := credential.BatchResolved(r, credential.Resolution{})
		if err == nil {
			t.Fatalf("BatchResolved accepted an unresolved Resolution")
		}
		var fe *credential.FaultError
		if !errors.As(err, &fe) || fe.Fault != credential.FaultUnresolved {
			t.Fatalf("error is %v (%T), want a FaultUnresolved *FaultError", err, err)
		}
		if got.Err() != nil {
			t.Fatalf("the refused result carries an error outcome: %v", got.Err())
		}
		if _, verr := got.Resolution().Value(); verr == nil {
			t.Fatalf("the refused result carries a readable resolution")
		}
		if got.Ref() != r {
			t.Fatalf("the refused result must keep its ref for attribution, got %v", got.Ref())
		}
	})

	t.Run("failed with a nil error", func(t *testing.T) {
		got, err := credential.BatchFailed(r, nil)
		if err == nil {
			t.Fatalf("BatchFailed accepted a nil error")
		}
		var fe *credential.FaultError
		if !errors.As(err, &fe) || fe.Fault != credential.FaultUnresolved {
			t.Fatalf("error is %v (%T), want a FaultUnresolved *FaultError", err, err)
		}
		if got.Err() != nil {
			t.Fatalf("the refused result carries an error outcome: %v", got.Err())
		}
		if _, verr := got.Resolution().Value(); verr == nil {
			t.Fatalf("the refused result carries a readable resolution")
		}
	})

	t.Run("the zero BatchResult is caught by CheckBatch", func(t *testing.T) {
		_, err := credential.CheckBatch("placeholder-loader", []ref.Ref{{}}, []credential.BatchResult{{}}, nil)
		assertFault(t, err, credential.FaultUnresolved)
	})
}

// TestBatchResolvedAcceptsADeliberatelyEmptySecret: a secret an operator
// really did store empty is a VALUE, and a batch must be able to carry it.
// Refusing it here would push authors back towards reporting a failure for a
// secret that resolved perfectly well.
func TestBatchResolvedAcceptsADeliberatelyEmptySecret(t *testing.T) {
	r := mustRef(t, "op://<vault>/<item>/<field>")
	got := mustBatchResolved(t, r, credential.ResolvedEmpty())
	mat, err := got.Resolution().Value()
	if err != nil {
		t.Fatalf("a deliberately-empty batch result must be readable: %v", err)
	}
	if len(mat.Reveal()) != 0 {
		t.Fatalf("material = %q, want empty", mat.Reveal())
	}
	if _, err := credential.CheckBatch("placeholder-loader", []ref.Ref{r}, []credential.BatchResult{got}, nil); err != nil {
		t.Fatalf("CheckBatch rejected a deliberately-empty result: %v", err)
	}
}

// TestBatchResultRedacts: a BatchResult travels through host code that logs,
// and it holds material. It must be no more revealing than the Resolution
// inside it, while still saying which reference it is for.
func TestBatchResultRedacts(t *testing.T) {
	const secret = "placeholder-do-not-log"
	r := mustRef(t, "op://<vault>/<item>/<field>")
	ok, err := credential.Resolved(credential.NewMaterial([]byte(secret)))
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}

	for _, tc := range []struct {
		name string
		br   credential.BatchResult
	}{
		{"resolved", mustBatchResolved(t, r, ok)},
		{"failed", mustBatchFailed(t, r, cerr.New(cerr.KindNotFound, "ResolveBatch", nil))},
		{"no outcome", credential.BatchResult{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, s := range []string{
				fmt.Sprintf("%v", tc.br),
				tc.br.String(),
				fmt.Sprintf("%#v", tc.br),
				fmt.Sprint([]credential.BatchResult{tc.br}),
				// Inside a struct, where fmt reaches the value through an
				// unexported field and cannot call String() on it.
				fmt.Sprintf("%+v", struct{ B credential.BatchResult }{tc.br}),
			} {
				if strings.Contains(s, secret) {
					t.Fatalf("BatchResult leaked material: %s", s)
				}
			}
		})
	}

	// It still has to be useful in a log: the reference is diagnosis, not
	// material.
	if !strings.Contains(fmt.Sprintf("%v", mustBatchResolved(t, r, ok)), r.String()) {
		t.Fatalf("a redacted BatchResult must still name its reference")
	}
}
