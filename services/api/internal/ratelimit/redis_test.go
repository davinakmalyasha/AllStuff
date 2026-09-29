package ratelimit

import (
	"strings"
	"testing"
	"time"
)

func TestParseCount(t *testing.T) {
	const window = 15 * time.Minute
	tests := []struct {
		name        string
		reply       any
		wantRemain  int
		wantRetry   time.Duration
		wantAllowed bool
	}{
		{
			name:        "first request in window",
			reply:       []any{1, int(window / time.Millisecond)},
			wantRemain:  9,
			wantAllowed: true,
		},
		{
			name:        "last allowed request",
			reply:       []any{10, int(window / time.Millisecond)},
			wantRemain:  0,
			wantAllowed: true,
		},
		{
			name:        "over limit reports remaining ttl",
			reply:       []any{11, 300_000},
			wantRemain:  0,
			wantRetry:   300 * time.Second,
			wantAllowed: false,
		},
		{
			// PTTL returns -2 when the key expired between INCR and PTTL. The
			// caller is not over budget, so this must allow rather than deny with
			// a 0ms retry that clients read as "retry immediately".
			name:        "negative ttl allows",
			reply:       []any{1, -2},
			wantRemain:  9,
			wantAllowed: true,
		},
		{
			// PTTL -1 means no expiry was ever set: a script bug. Allow with a
			// full-window retry rather than looping the caller.
			name:        "missing ttl allows with full window",
			reply:       []any{99, -1},
			wantRemain:  10,
			wantRetry:   window,
			wantAllowed: true,
		},
		{
			name:        "malformed reply fails open",
			reply:       "unexpected",
			wantRemain:  0,
			wantAllowed: true,
		},
		{
			name:        "short array fails open",
			reply:       []any{5},
			wantRemain:  0,
			wantAllowed: true,
		},
		{
			name:        "nil reply fails open",
			reply:       nil,
			wantRemain:  0,
			wantAllowed: true,
		},
		{
			name:        "wrong element types fail open",
			reply:       []any{"5", "1000"},
			wantRemain:  0,
			wantAllowed: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotRemain, gotRetry, gotOK := parseCount(tc.reply, 10, window)
			if gotOK != tc.wantAllowed {
				t.Errorf("allowed = %v, want %v", gotOK, tc.wantAllowed)
			}
			if gotRemain != tc.wantRemain {
				t.Errorf("remaining = %d, want %d", gotRemain, tc.wantRemain)
			}
			if gotRetry != tc.wantRetry {
				t.Errorf("retryAfter = %v, want %v", gotRetry, tc.wantRetry)
			}
		})
	}
}

func TestIsNoScript(t *testing.T) {
	if !isNoScript(errString("redis: NOSCRIPT No matching script. Please use EVAL.")) {
		t.Error("isNoScript should match the server's NOSCRIPT reply")
	}
	if isNoScript(errString("redis: LOADING Redis is loading the dataset in memory")) {
		t.Error("isNoScript must not match unrelated Redis errors")
	}
	if isNoScript(nil) {
		t.Error("isNoScript(nil) should be false")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// Keys must not leak the user-controlled parts (emails, IPs) into Redis
// keyspace, which is visible to anyone with redis-cli access and in slow logs.
func TestRedisKeyHashesAndNamespaces(t *testing.T) {
	k1 := redisKey("acct:login:user@example.com:10.0.0.1", 15*time.Minute)
	k2 := redisKey("acct:login:user@example.com:10.0.0.2", 15*time.Minute)
	if k1 == k2 {
		t.Error("distinct clients must not share a key")
	}
	if !strings.HasPrefix(k1, "bv:rl:") {
		t.Errorf("key %q missing bv:rl: namespace", k1)
	}
	if strings.Contains(k1, "user@example.com") {
		t.Errorf("key %q leaks the raw identity", k1)
	}
	if !strings.HasSuffix(k1, ":900") {
		t.Errorf("key %q should end with the window in seconds", k1)
	}
	// Same client, different window: distinct counters, so a 5/min tier and a
	// 10/15min tier over one key cannot overwrite each other.
	if k1 == redisKey("acct:login:user@example.com:10.0.0.1", time.Minute) {
		t.Error("different windows must use different keys")
	}
	// Stability matters: a non-deterministic key would reset every user's
	// budget on each restart.
	if k1 != redisKey("acct:login:user@example.com:10.0.0.1", 15*time.Minute) {
		t.Error("key must be deterministic")
	}
}

// A bad configuration must not become a wall of 429s. The Redis limiter has the
// same guard, and both must agree — a divergence would make a rate limit apply
// or not apply depending on which replica served the request.
func TestZeroLimitFailsOpen(t *testing.T) {
	l := NewInMemory()
	// Repeat each call: a guard that only covers the first call would still
	// deny on the second, which is the case that actually breaks traffic.
	for i := 0; i < 3; i++ {
		if _, _, ok := l.Allow("k", 0, time.Minute); !ok {
			t.Fatalf("limit=0 call %d denied; want allow", i+1)
		}
		if _, _, ok := l.Allow("k2", 5, 0); !ok {
			t.Fatalf("window=0 call %d denied; want allow", i+1)
		}
		if _, _, ok := l.Allow("k3", -1, time.Minute); !ok {
			t.Fatalf("limit=-1 call %d denied; want allow", i+1)
		}
	}
}
