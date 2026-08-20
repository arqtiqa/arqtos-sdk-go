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
	"sync"
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

// ⚠️ THE MUTEX IS LOAD-BEARING, and its absence was a DORMANT race.
//
// A fixture body may be concurrent — the git compare-and-swap fixture races
// four goroutines and calls Errorf from each. Without this lock those append
// to one slice unsynchronised, and the race would first fire on the day that
// fixture is supposed to START JUDGING git: the worst possible moment for the
// harness to be the thing that breaks.
//
// It stayed hidden because the fixture is red — GitRefStore returns
// immediately, so the goroutines never reach Errorf. A harness that exists to
// make fixtures trustworthy cannot carry the class of defect it was built to
// catch.
type recorder struct {
	mu       sync.Mutex
	failures []string
}

func (r *recorder) Helper()             {}
func (r *recorder) Logf(string, ...any) {}

func (r *recorder) Error(a ...any) { r.record(fmt.Sprint(a...)) }

func (r *recorder) Errorf(f string, a ...any) { r.record(fmt.Sprintf(f, a...)) }

// ⚠️ Fatal and Fatalf abort the CALLING goroutine, which is all a panic can do.
// In a concurrent body that stops one goroutine and leaves the others running —
// the same semantics testing.T has, and the same reason a fixture body should
// not rely on Fatal to stop its peers.
func (r *recorder) Fatal(a ...any) { r.Error(a...); panic(abort{}) }

func (r *recorder) Fatalf(f string, a ...any) { r.Errorf(f, a...); panic(abort{}) }

func (r *recorder) record(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, s)
}

// snapshot returns the recorded failures under the lock, so Assess reads a
// consistent view even if a stray goroutine is still running.
func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.failures...)
}

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

	failures := r.snapshot()
	if len(failures) == 0 {
		return Verdict{Message: fmt.Sprintf("RED FIXTURE IS NO LONGER RED.\n\n"+
			"It is waiting for %s, and its assertions now all pass — either that capability has "+
			"arrived, or the fixture never asserted anything.\n\n"+
			"Both cases need the same change, in this commit: make this an ORDINARY test by removing "+
			"the redfixture wrapper and taking *testing.T directly. Do not delete the assertions to "+
			"make this message go away.", waitingFor)}
	}

	return Verdict{StillRed: true, Message: fmt.Sprintf(
		"still red, waiting for %s — %d assertion(s) failing:\n  - %s",
		waitingFor, len(failures), strings.Join(failures, "\n  - "))}
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
