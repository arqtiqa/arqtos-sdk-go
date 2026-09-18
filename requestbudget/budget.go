// Package requestbudget is the provider-neutral admission seam for tracker
// connectors. A host injects an opaque limiter identity; connector instances
// that share a key share remaining allowance. It is not a Tracker method and
// imports no vendor types.
package requestbudget

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

const DefaultMaxInFlight = 4

// A Key is an opaque host-provided limiter identity. It is not a credential
// and must not be logged as one.
type Key string

type Unit int

const (
	UnitUnknown Unit = iota
	UnitRequests
	UnitProvider
)

type Allowance struct {
	Known     bool
	Remaining int
	Limit     int
	Reset     time.Time
}

type Observation struct {
	Allowance  Allowance
	RetryAfter time.Duration
	Status     int
}

type Gate struct {
	key         Key
	mu          sync.Mutex
	allowance   Allowance
	inflight    int
	maxInFlight int
	now         func() time.Time
}

var registry sync.Map

func For(key Key) *Gate {
	if v, ok := registry.Load(key); ok {
		return v.(*Gate)
	}
	g := &Gate{key: key, maxInFlight: DefaultMaxInFlight, now: time.Now}
	actual, _ := registry.LoadOrStore(key, g)
	return actual.(*Gate)
}

func (g *Gate) Admit(ctx context.Context, n int) error {
	if n <= 0 {
		n = 1
	}
	for {
		g.mu.Lock()
		if g.inflight+n > g.maxInFlight {
			g.mu.Unlock()
			select {
			case <-ctx.Done():
				return cerr.New(cerr.KindTimeout, "Admit", ctx.Err())
			case <-time.After(10 * time.Millisecond):
				continue
			}
		}
		if g.allowance.Known && g.allowance.Remaining < n {
			reset := g.allowance.Reset
			g.mu.Unlock()
			if reset.IsZero() || !reset.After(g.now()) {
				return cerr.RateLimited("Admit", cerr.Observation{
					Bucket:       cerr.BucketUnknown,
					UpstreamKind: cerr.CountObserved,
					Provenance:   cerr.ProvenanceProvider,
				}, errors.New("quota remaining is spent"))
			}
			timer := time.NewTimer(time.Until(reset))
			select {
			case <-ctx.Done():
				timer.Stop()
				return cerr.New(cerr.KindTimeout, "Admit", ctx.Err())
			case <-timer.C:
				continue
			}
		}
		g.inflight += n
		if g.allowance.Known {
			g.allowance.Remaining -= n
		}
		g.mu.Unlock()
		return nil
	}
}

func (g *Gate) Release(n int) {
	if n <= 0 {
		n = 1
	}
	g.mu.Lock()
	g.inflight -= n
	if g.inflight < 0 {
		g.inflight = 0
	}
	g.mu.Unlock()
}

func (g *Gate) Observe(obs Observation) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if obs.Allowance.Known {
		g.allowance = obs.Allowance
	}
	if obs.RetryAfter > 0 {
		g.allowance.Reset = g.now().Add(obs.RetryAfter)
		if !g.allowance.Known {
			g.allowance.Known = true
			g.allowance.Remaining = 0
		}
	}
	if obs.Status == http.StatusTooManyRequests && !g.allowance.Known {
		g.allowance.Known = true
		g.allowance.Remaining = 0
	}
}

func ParseHeaders(h http.Header) Observation {
	var o Observation
	if h == nil {
		return o
	}
	if ra := h.Get("Retry-After"); ra != "" {
		if sec, err := strconv.Atoi(ra); err == nil && sec >= 0 {
			o.RetryAfter = time.Duration(sec) * time.Second
			o.Allowance.Known = true
			o.Allowance.Remaining = 0
		}
	}
	if rem := h.Get("X-RateLimit-Remaining"); rem != "" {
		if n, err := strconv.Atoi(rem); err == nil {
			o.Allowance.Known = true
			o.Allowance.Remaining = n
		}
	}
	if lim := h.Get("X-RateLimit-Limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			o.Allowance.Limit = n
		}
	}
	if rst := h.Get("X-RateLimit-Reset"); rst != "" {
		if n, err := strconv.Atoi(rst); err == nil {
			o.Allowance.Reset = time.Unix(int64(n), 0)
			o.Allowance.Known = true
		}
	}
	return o
}
