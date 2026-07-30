package githubratelimit

import (
	"context"
	"time"
)

// A Clock is the ONLY source of now and of waiting in this package.
//
// It exists so that the tests for a package whose entire job is waiting do not
// wait. A backoff test that measures elapsed wall-clock time asserts a property
// of the machine it runs on — and under a -race build with coverage
// instrumentation, that machine is several times slower than the one the
// threshold was chosen on. Those tests do not fail honestly; they flake. With
// the clock injected, the tests assert the COMPUTED delay and the NUMBER of
// attempts, both of which are exact.
//
// A Clock MUST be safe for concurrent use: a [Gate] is, and it calls straight
// through.
type Clock interface {
	// Now reports the current time.
	Now() time.Time
	// Sleep blocks for d, or until ctx is done, whichever happens first. It
	// returns ctx.Err() in the second case and nil in the first, so a caller
	// can tell a completed wait from an abandoned one — a distinction a bare
	// time.Sleep cannot make, and the reason this is not just a Now() seam.
	//
	// A non-positive d returns immediately, and returns nil even if ctx is
	// already done: there was nothing to interrupt.
	Sleep(ctx context.Context, d time.Duration) error
}

// SystemClock is the real clock: wall time and a real, cancellable sleep. It is
// what [New] uses when [Options.Clock] is nil.
//
// It carries no state, so the zero value is usable and copying it is free.
type SystemClock struct{}

// Now reports the wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }

// Sleep waits out d on a timer, abandoning the wait if ctx is done first. The
// timer is always stopped, so a cancelled wait does not leave one armed for the
// remainder of what could be a full hour.
func (SystemClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
