package requestbudget

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

func TestFor_SameKeySharesAllowance(t *testing.T) {
	k := Key("placeholder-limiter-a")
	a := For(k)
	b := For(k)
	if a != b {
		t.Fatal("same key returned distinct gates")
	}
	a.Observe(Observation{Allowance: Allowance{Known: true, Remaining: 2, Limit: 10}})
	if err := b.Admit(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	err := a.Admit(context.Background(), 1)
	if cerr.KindOf(err) != cerr.KindRateLimited {
		t.Fatalf("shared remaining spent independently: %v", err)
	}
	if strings.Contains(err.Error(), string(k)) && len(k) > 8 {
		t.Fatal("error echoed the limiter identity")
	}
}

func TestFor_DistinctKeysAreIsolated(t *testing.T) {
	a := For(Key("placeholder-limiter-b"))
	b := For(Key("placeholder-limiter-c"))
	a.Observe(Observation{Allowance: Allowance{Known: true, Remaining: 0, Limit: 10}})
	if err := b.Admit(context.Background(), 1); err != nil {
		t.Fatalf("distinct key blocked by the other: %v", err)
	}
}

func TestAdmit_UnknownAllowanceIsNotUnlimited(t *testing.T) {
	g := For(Key("placeholder-limiter-unknown"))
	g.Observe(Observation{})
	var n atomic.Int32
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Admit(ctx, 1); err == nil {
				n.Add(1)
				time.Sleep(20 * time.Millisecond)
				g.Release(1)
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	if int(n.Load()) > DefaultMaxInFlight {
		t.Fatalf("unknown allowance admitted %d in flight, cap %d", n.Load(), DefaultMaxInFlight)
	}
	cancel()
	wg.Wait()
}

func TestAdmit_CancellationStopsWait(t *testing.T) {
	g := For(Key("placeholder-limiter-cancel"))
	g.Observe(Observation{
		Allowance:  Allowance{Known: true, Remaining: 0, Limit: 10, Reset: time.Now().Add(time.Hour)},
		RetryAfter: time.Hour,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := g.Admit(ctx, 1)
	if err == nil {
		t.Fatal("wait completed despite cancel")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("wait ignored cancellation: %v", time.Since(start))
	}
	if cerr.KindOf(err) != cerr.KindTimeout && cerr.KindOf(err) != cerr.KindRateLimited {
		t.Fatalf("kind=%s", cerr.KindOf(err))
	}
}

func TestParseHeaders_RetryAfterAndMissing(t *testing.T) {
	got := ParseHeaders(map[string][]string{"Retry-After": {"7"}})
	if !got.Allowance.Known || got.RetryAfter != 7*time.Second {
		t.Fatalf("%+v", got)
	}
	missing := ParseHeaders(nil)
	if missing.Allowance.Known {
		t.Fatal("absent headers reported a known allowance")
	}
	bad := ParseHeaders(map[string][]string{"Retry-After": {"not-a-number"}})
	if bad.RetryAfter != 0 && bad.Allowance.Known {
		t.Fatalf("malformed headers: %+v", bad)
	}
}
