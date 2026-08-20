package redfixture_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/internal/redfixture"
)

// ⚠️ THE HARNESS'S OWN CLAIMS. A harness that decides whether other tests are
// honest has to be tested itself, or it is one more thing asserting without
// being checked — which is the defect it exists to fix.

// The defect this package makes impossible: a fixture body with no assertions.
// It must NOT be reported as still red, because nothing about it is red.
func TestAssess_RejectsAnEmptyBody(t *testing.T) {
	v := redfixture.Assess("the reducer", func(redfixture.T) {})
	if v.StillRed {
		t.Fatal("an empty fixture body was reported as still red — a body with no assertions asserts nothing")
	}
	if !strings.Contains(v.Message, "NO LONGER RED") {
		t.Errorf("message %q does not say the fixture is not red", v.Message)
	}
	if !strings.Contains(v.Message, "never asserted anything") {
		t.Errorf("message %q does not offer the empty-body reading, which is the likelier one", v.Message)
	}
}

// A body whose assertions all pass means the capability arrived, and the fixture
// must be promoted in the same change.
func TestAssess_FailsWhenTheFixtureWouldPass(t *testing.T) {
	v := redfixture.Assess("a git-backed RefStore", func(ft redfixture.T) {
		ft.Logf("checked something that holds")
	})
	if v.StillRed {
		t.Fatal("a fixture whose assertions pass was reported as still red")
	}
	if !strings.Contains(v.Message, "a git-backed RefStore") {
		t.Errorf("message %q does not name what the fixture was waiting for", v.Message)
	}
	// ⚠️ It must tell the reader to PROMOTE, not to delete. The cheapest way to
	// make this message stop is to delete the assertions, and that is the one
	// response that must be argued against in the message itself.
	if !strings.Contains(v.Message, "Do not delete the assertions") {
		t.Errorf("message %q does not warn against the cheapest wrong fix", v.Message)
	}
}

func TestAssess_ReportsAFailingBodyAsStillRed(t *testing.T) {
	v := redfixture.Assess("the reducer", func(ft redfixture.T) {
		ft.Errorf("the refusal did not name the earlier spend")
	})
	if !v.StillRed {
		t.Fatalf("a genuinely red fixture was not reported as red: %s", v.Message)
	}
	if !strings.Contains(v.Message, "the refusal did not name the earlier spend") {
		t.Errorf("message %q does not carry the body's failures, so the gap is invisible", v.Message)
	}
	if !strings.Contains(v.Message, "1 assertion") {
		t.Errorf("message %q does not report how many assertions are failing", v.Message)
	}
}

// ⚠️ Fatalf must ABORT. A body that kept going after its own Fatalf would run
// assertions against state its guard just rejected, and report consequences as
// if they were causes.
func TestAssess_FatalfStopsTheBody(t *testing.T) {
	reached := false
	v := redfixture.Assess("a git-backed RefStore", func(ft redfixture.T) {
		ft.Fatalf("the store does not exist")
		reached = true
	})
	if reached {
		t.Fatal("Fatalf did not stop the body")
	}
	if !v.StillRed || !strings.Contains(v.Message, "1 assertion") {
		t.Errorf("a body that called Fatalf recorded %q", v.Message)
	}
}

func TestAssess_RecordsEveryFailure(t *testing.T) {
	v := redfixture.Assess("the reducer", func(ft redfixture.T) {
		ft.Errorf("first")
		ft.Errorf("second")
		ft.Errorf("third")
	})
	if !strings.Contains(v.Message, "3 assertion") {
		t.Errorf("message %q does not report all three failures", v.Message)
	}
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message %q is missing %q", v.Message, want)
		}
	}
}

// ⚠️ A panic that is not the body's own Fatalf must PROPAGATE. Swallowing it
// would turn a nil dereference inside a fixture into "still red" — a broken
// fixture reading as a pending one.
func TestAssess_DoesNotSwallowARealPanic(t *testing.T) {
	defer func() {
		if p := recover(); p == nil {
			t.Fatal("a real panic inside a fixture body was swallowed and would have read as still-red")
		}
	}()
	redfixture.Assess("the reducer", func(redfixture.T) {
		// Any panic that is not the body's own Fatalf — a nil dereference, an
		// index out of range, this.
		panic("a defect inside the fixture body")
	})
}

func TestAssess_RequiresACapabilityName(t *testing.T) {
	for _, name := range []string{"", "   "} {
		v := redfixture.Assess(name, func(ft redfixture.T) { ft.Errorf("red") })
		if v.StillRed {
			t.Errorf("a fixture waiting for %q was accepted; it could never be judged stale", name)
		}
	}
}

// Expect is a thin wrapper, and its one behaviour worth pinning is that a red
// fixture SKIPS rather than fails — otherwise CI could not stay green.
func TestExpect_SkipsAStillRedFixture(t *testing.T) {
	redfixture.Expect(t, "this test's own demonstration", func(ft redfixture.T) {
		ft.Errorf("deliberately red, to show Expect skips rather than fails")
	})
	t.Fatal("Expect did not skip a still-red fixture")
}

// ⚠️ THE RACE THAT WAS DORMANT. A fixture body may be concurrent — the git
// compare-and-swap fixture races four goroutines and calls Errorf from each —
// and the recorder appended to one slice with no lock. It stayed hidden only
// because that fixture is red: the store returns immediately, so the goroutines
// never reach Errorf.
//
// The harness exists to make fixtures trustworthy. It cannot carry the class of
// defect it was built to catch.
func TestAssess_IsSafeUnderAConcurrentBody(t *testing.T) {
	const writers = 8
	const each = 25

	var started sync.WaitGroup
	started.Add(writers)

	v := redfixture.Assess("a concurrent capability", func(ft redfixture.T) {
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				// ⚠️ A barrier, so the goroutines genuinely overlap inside the
				// recorder. Without it they can serialise and the test would
				// pass against the unlocked version — a concurrency test that
				// does not race proves nothing.
				started.Done()
				started.Wait()
				for j := range each {
					ft.Errorf("writer %d failure %d", i, j)
				}
			}(i)
		}
		wg.Wait()
	})

	if !v.StillRed {
		t.Fatal("a body that recorded failures was not reported as red")
	}
	// ⚠️ The COUNT is asserted. An unlocked append does not only race — it
	// LOSES entries, and a lost failure is a fixture quietly reporting less
	// than it found.
	want := writers * each
	if !strings.Contains(v.Message, fmt.Sprintf("%d assertion(s)", want)) {
		t.Errorf("recorded a count other than %d; entries were lost:\n%s", want, v.Message)
	}
}

// Fatal from one goroutine stops that goroutine and does not take the harness
// with it — the same semantics testing.T has.
func TestAssess_FatalInOneGoroutineDoesNotKillTheRest(t *testing.T) {
	v := redfixture.Assess("a concurrent capability", func(ft redfixture.T) {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			defer func() { _ = recover() }() // the body's own goroutine owns its abort
			ft.Fatalf("the store does not exist")
		}()
		go func() {
			defer wg.Done()
			ft.Errorf("and the other goroutine still recorded")
		}()
		wg.Wait()
	})

	if !v.StillRed {
		t.Fatal("a concurrent body with failures was not reported as red")
	}
	for _, want := range []string{"the store does not exist", "and the other goroutine still recorded"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message is missing %q:\n%s", want, v.Message)
		}
	}
}
