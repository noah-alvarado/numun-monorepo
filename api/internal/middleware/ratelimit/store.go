package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// store holds per-key token-bucket limiters with last-seen timestamps and
// sweeps idle entries on a background goroutine. The zero value is unusable
// — construct via newStore.
type store struct {
	limit rate.Limit
	burst int

	mu      sync.Mutex
	buckets map[string]*entry

	now func() time.Time
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newStorePerMin constructs a store sized for a per-minute cap. Burst equals
// the per-minute cap so the limiter tolerates a 1-minute spike at the
// documented cap then refills steadily. This is the standard "60/min means
// 60 tokens over a minute" interpretation.
func newStorePerMin(perMinute int) *store {
	return newStore(rate.Limit(float64(perMinute)/60.0), perMinute)
}

func newStore(limit rate.Limit, burst int) *store {
	s := &store{
		limit:   limit,
		burst:   burst,
		buckets: make(map[string]*entry),
		now:     time.Now,
	}
	go s.sweepLoop()
	return s
}

// allow records an attempt for the given key and returns whether it is
// allowed. Concurrency-safe.
func (s *store) allow(key string) bool {
	s.mu.Lock()
	e, ok := s.buckets[key]
	if !ok {
		e = &entry{limiter: rate.NewLimiter(s.limit, s.burst)}
		s.buckets[key] = e
	}
	e.lastSeen = s.now()
	s.mu.Unlock()
	return e.limiter.Allow()
}

// sweepLoop removes entries that haven't been seen in `idle` for `interval`.
// Idle threshold is set to 15 minutes — three times the sweep interval — so
// an entry that's quietly under the limit isn't evicted between bursts.
func (s *store) sweepLoop() {
	const (
		interval = 5 * time.Minute
		idle     = 15 * time.Minute
	)
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		cutoff := s.now().Add(-idle)
		s.mu.Lock()
		for k, e := range s.buckets {
			if e.lastSeen.Before(cutoff) {
				delete(s.buckets, k)
			}
		}
		s.mu.Unlock()
	}
}
