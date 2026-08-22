package ratelimit

import (
	"testing"
	"time"
)

func TestAllowWithinWindow(t *testing.T) {
	l := NewInMemory()
	for i := 0; i < 5; i++ {
		if _, _, ok := l.Allow("k", 5, time.Minute); !ok {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}
	// Sixth call within the same window is rejected with a retry hint.
	_, retry, ok := l.Allow("k", 5, time.Minute)
	if ok {
		t.Fatal("6th call must be rate limited")
	}
	if retry <= 0 || retry > time.Minute {
		t.Fatalf("retry hint %v out of range", retry)
	}
}

func TestWindowReset(t *testing.T) {
	l := NewInMemory()
	if _, _, ok := l.Allow("k", 1, 50*time.Millisecond); !ok {
		t.Fatal("first call allowed")
	}
	if _, _, ok := l.Allow("k", 1, 50*time.Millisecond); ok {
		t.Fatal("second call must be limited")
	}
	time.Sleep(60 * time.Millisecond)
	if _, _, ok := l.Allow("k", 1, 50*time.Millisecond); !ok {
		t.Fatal("window should have reset")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l := NewInMemory()
	for i := 0; i < 3; i++ {
		l.Allow("a", 3, time.Minute)
	}
	if _, _, ok := l.Allow("a", 3, time.Minute); ok {
		t.Fatal("a exhausted")
	}
	if _, _, ok := l.Allow("b", 3, time.Minute); !ok {
		t.Fatal("b untouched")
	}
}

func TestRemainingCount(t *testing.T) {
	l := NewInMemory()
	r, _, ok := l.Allow("k", 5, time.Minute)
	if !ok || r != 4 {
		t.Fatalf("remaining = %d, want 4", r)
	}
}
