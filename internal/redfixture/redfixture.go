// Package redfixture makes a failing test's REDNESS an assertion.
//
// # ⚠️ The problem it exists to fix
//
// A fixture written before the thing it tests is usually spelled:
//
//	func TestSomethingIsRefused(t *testing.T) {
//	    t.Skip("RED FIXTURE — waiting on the reducer …")
//	}
//
// That is not a red test. It is a comment with a function signature. Delete the
// skip and it passes, because there is nothing in it to fail — so it asserts
// nothing, guards nothing, and reports as coverage. Nine of these shipped in
// this project's first week and were caught in review, not by the suite.
//
// Pairing such a skip with a "still needed" canary does not fix it either. A
// canary that watches for a symbol appearing forces the SKIP's removal; it
// cannot force an assertion to be written, and it usually watches the wrong
// symbol.
//
// # What this does instead
//
// [Expect] runs the fixture body against a recording [T] and REQUIRES it to
// fail. Three properties follow, and each closes one of the holes above:
//
//   - An EMPTY body produces no failures, so [Expect] fails. A fixture with no
//     assertions cannot be shipped.
//   - A body that PASSES means the capability has arrived, so [Expect] fails and
//     names what must now be promoted. The fixture cannot outlive its reason.
//   - Otherwise the test reports as skipped, with the failures it recorded — so
//     CI stays green while the gap stays visible and named.
//
// The redness is measured rather than asserted in prose, which is the whole
// point: "this test is waiting for X" becomes a checkable claim.
package redfixture

import (
	"fmt"
	"strings"
	"testing"
)

// A T is the subset of [testing.TB] a fixture body may use.
//
// ⚠️ It is deliberately NOT *testing.T. A body holding the real one could call
// Skip, Parallel or Cleanup, and could report a pass — none of which mean
// anything for a body whose failure is the expected outcome.
type T interface {
	Helper()
	Logf(format string, args ...any)
	Error(args ...any)
	Errorf(format string, args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

// abort unwinds a body that called Fatalf.
type abort struct{}

type recorder struct{ failures []string }

func (r *recorder) Helper()                   {}
func (r *recorder) Logf(string, ...any)       {}
func (r *recorder) Error(a ...any)            { r.failures = append(r.failures, fmt.Sprint(a...)) }
func (r *recorder) Errorf(f string, a ...any) { r.failures = append(r.failures, fmt.Sprintf(f, a...)) }
func (r *recorder) Fatal(a ...any)            { r.Error(a...); panic(abort{}) }
func (r *recorder) Fatalf(f string, a ...any) { r.Errorf(f, a...); panic(abort{}) }

// A Verdict is what [Assess] concluded about a fixture body.
type Verdict struct {
	// StillRed is true when the body failed, which is the expected state for a
	// fixture waiting on unbuilt behaviour.
	StillRed bool
	// Message explains the verdict. It carries the body's failures when the
	// fixture is still red, and what to do about it when it is not.
	Message string
}

// Assess runs body and reports whether it is still red.
//
// ⚠️ It is separate from [Expect], and takes no *testing.T, precisely so this
// package's own claims can be tested. A harness whose behaviour can only be
// observed by failing a real test is a harness nobody verifies.
//
// A panic from the body that is NOT its own Fatalf PROPAGATES. Swallowing it
// would turn a nil dereference inside a fixture into "still red", which is the
// exact confusion this package exists to remove.
func Assess(waitingFor string, body func(T)) Verdict {
	if strings.TrimSpace(waitingFor) == "" {
		return Verdict{Message: "redfixture: no capability named. A fixture that cannot say what it " +
			"waits for cannot be judged stale, and will outlive its reason."}
	}

	r := &recorder{}
	func() {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			if _, ok := p.(abort); ok {
				return
			}
			panic(p)
		}()
		body(r)
	}()

	if len(r.failures) == 0 {
		return Verdict{Message: fmt.Sprintf("RED FIXTURE IS NO LONGER RED.\n\n"+
			"It is waiting for %s, and its assertions now all pass — either that capability has "+
			"arrived, or the fixture never asserted anything.\n\n"+
			"Both cases need the same change, in this commit: make this an ORDINARY test by removing "+
			"the redfixture wrapper and taking *testing.T directly. Do not delete the assertions to "+
			"make this message go away.", waitingFor)}
	}

	return Verdict{StillRed: true, Message: fmt.Sprintf(
		"still red, waiting for %s — %d assertion(s) failing:\n  - %s",
		waitingFor, len(r.failures), strings.Join(r.failures, "\n  - "))}
}

// Expect runs body and requires it to FAIL, because body describes behaviour
// that does not exist yet.
//
// waitingFor names the capability the fixture is waiting on and appears in every
// message this produces.
//
// A still-red fixture is reported as SKIPPED — but only after its redness has
// been proved, and with its failures printed, so the gap is visible rather than
// merely labelled.
func Expect(t *testing.T, waitingFor string, body func(T)) {
	t.Helper()
	v := Assess(waitingFor, body)
	if !v.StillRed {
		t.Fatal(v.Message)
	}
	t.Skip(v.Message)
}
