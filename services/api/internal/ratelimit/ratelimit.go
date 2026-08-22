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

type inMemory struct {
	mu     sync.Mutex
	buckets map[string]*bucket
}

func NewInMemory() RateLimiter {
	return &inMemory{buckets: map[string]*bucket{}}
}

func (m *inMemory) Allow(key string, limit int, window time.Duration) (int, time.Duration, bool) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.buckets[key]
	if !ok || now.Sub(b.start) >= window {
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
