package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

// A code inside its validity window must be claimable exactly once. This is the
// property that stops a phished 6-digit code from minting a second session.
func TestLocalTOTPGuardRejectsReplay(t *testing.T) {
	g := newLocalTOTPGuard()
	ctx := context.Background()

	ok, err := g.claim(ctx, "user-1", 12345)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !ok {
		t.Fatal("first use of a timestep must be accepted")
	}

	ok, err = g.claim(ctx, "user-1", 12345)
	if err != nil {
		t.Fatalf("replay claim: %v", err)
	}
	if ok {
		t.Fatal("replay of the same timestep must be rejected")
	}
}

// The guard is keyed per user AND per step: one user's code must never consume
// another's window, and a new step must be claimable immediately.
func TestLocalTOTPGuardIsolatesUsersAndSteps(t *testing.T) {
	g := newLocalTOTPGuard()
	ctx := context.Background()

	if ok, _ := g.claim(ctx, "alice", 100); !ok {
		t.Fatal("alice step 100 should be fresh")
	}
	if ok, _ := g.claim(ctx, "bob", 100); !ok {
		t.Fatal("bob's identical step number must be independent of alice's")
	}
	if ok, _ := g.claim(ctx, "alice", 101); !ok {
		t.Fatal("a new timestep must be claimable")
	}
	if ok, _ := g.claim(ctx, "alice", 100); ok {
		t.Fatal("alice's step 100 must still be consumed")
	}
}

// Once a step is older than the retention window it is unreachable in practice
// (the code expired long before), so the entry must be reclaimed. Without this
// the map grows for the process lifetime.
func TestLocalTOTPGuardPrunesExpiredSteps(t *testing.T) {
	g := newLocalTOTPGuard()
	ctx := context.Background()

	if ok, _ := g.claim(ctx, "user-1", 1); !ok {
		t.Fatal("initial claim should succeed")
	}
	g.mu.Lock()
	g.used["user-1:1"] = time.Now().Add(-2 * usedTOTPStepTTL)
	sizeBefore := len(g.used)
	g.mu.Unlock()

	// Any later claim runs the prune pass.
	if ok, _ := g.claim(ctx, "user-2", 2); !ok {
		t.Fatal("unrelated claim should succeed")
	}

	g.mu.Lock()
	_, stillThere := g.used["user-1:1"]
	sizeAfter := len(g.used)
	g.mu.Unlock()

	if stillThere {
		t.Error("entry older than the TTL was not pruned")
	}
	if sizeAfter >= sizeBefore+2 {
		t.Errorf("map did not shrink: %d entries before, %d after", sizeBefore, sizeAfter)
	}
}

// The guard runs on the auth path under concurrent 2FA verification; a data
// race here would let two sessions through for one code.
func TestLocalTOTPGuardConcurrentClaimsAllowExactlyOne(t *testing.T) {
	g := newLocalTOTPGuard()
	ctx := context.Background()

	const attempts = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0

	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ok, _ := g.claim(ctx, "user-1", 777); ok {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if accepted != 1 {
		t.Fatalf("%d of %d concurrent claims were accepted, want exactly 1", accepted, attempts)
	}
}
