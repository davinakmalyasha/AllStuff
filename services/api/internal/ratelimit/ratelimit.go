package ratelimit

import (
	"sync"
	"time"
)

// RateLimiter is a fixed-window limiter. In-memory per instance for now;
// swap in a Redis implementation for multi-instance deploys (PRD §10.1).
type RateLimiter interface {
	Allow(key string, limit int, window time.Duration) (remaining int, retryAfter time.Duration, ok bool)
}

type bucket struct {
	count int
	start time.Time
}

// maxBuckets bounds memory: once exceeded, the oldest expired buckets are
// evicted; if none are expired (extreme burst), new keys are still served by
// evicting the soonest-to-expire entry.
const maxBuckets = 100_000

type inMemory struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func NewInMemory() RateLimiter {
	return &inMemory{buckets: map[string]*bucket{}}
}

func (m *inMemory) Allow(key string, limit int, window time.Duration) (int, time.Duration, bool) {
	// A zero/negative limit or window is a misconfiguration, not a request to
	// enforce. Denying here would turn one bad config value into a 429 wall, and
	// it would also make the in-memory and Redis limiters disagree on the same
	// key — see the matching guard in redis.go.
	if limit <= 0 || window <= 0 {
		return 0, 0, true
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.buckets) >= maxBuckets {
		m.evictLocked(now, window)
	}

	b, ok := m.buckets[key]
	if !ok || now.Sub(b.start) >= window {
		if b == nil && len(m.buckets) >= maxBuckets {
			// No expired bucket could be reclaimed: drop the one closest to
			// expiry so unbounded key minting cannot grow memory forever.
			m.dropOldestLocked()
		}
		b = &bucket{count: 1, start: now}
		m.buckets[key] = b
		return limit - 1, 0, true
	}
	b.count++
	if b.count > limit {
		retry := window - now.Sub(b.start)
		return 0, retry, false
	}
	return limit - b.count, 0, true
}

// evictLocked removes buckets whose window has fully elapsed.
func (m *inMemory) evictLocked(now time.Time, window time.Duration) {
	for k, b := range m.buckets {
		if now.Sub(b.start) >= window {
			delete(m.buckets, k)
		}
		if len(m.buckets) < maxBuckets/2 {
			return
		}
	}
}

func (m *inMemory) dropOldestLocked() {
	var oldestKey string
	var oldest time.Time
	found := false
	for k, b := range m.buckets {
		if !found || b.start.Before(oldest) {
			oldestKey, oldest, found = k, b.start, true
		}
	}
	if found {
		delete(m.buckets, oldestKey)
	}
}
