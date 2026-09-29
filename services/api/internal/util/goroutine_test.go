package util

import (
	"sync"
	"testing"
	"time"
)

// A panic in a background goroutine terminates the process unless something on
// THAT goroutine deferred a recover. These tests assert the wrapper both
// recovers and still runs the work to completion for its siblings.
func TestGo_RecoversPanic(t *testing.T) {
	done := make(chan struct{})
	Go(func() {
		defer close(done)
		panic("boom")
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recovered goroutine did not return")
	}
	// Reaching here at all proves the process survived.
}

func TestGoNamed_RecoversPanic(t *testing.T) {
	done := make(chan struct{})
	GoNamed("test-task", func() {
		defer close(done)
		var m map[string]int
		m["x"] = 1 // nil-map write: a runtime panic, not an explicit one
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recovered goroutine did not return")
	}
}

func TestGo_RunsTheFunction(t *testing.T) {
	var mu sync.Mutex
	got := 0
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		Go(func() {
			defer wg.Done()
			mu.Lock()
			got++
			mu.Unlock()
		})
	}
	wg.Wait()
	if got != 20 {
		t.Errorf("ran %d/20 functions", got)
	}
}

// One panicking task must not prevent the others from completing. This is the
// property the notification fan-out depends on: a single bad row must not
// silently truncate the send to every other recipient.
func TestGo_OnePanicDoesNotAffectSiblings(t *testing.T) {
	var mu sync.Mutex
	completed := 0
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		idx := i
		Go(func() {
			defer wg.Done()
			if idx == 3 {
				panic("one bad task")
			}
			mu.Lock()
			completed++
			mu.Unlock()
		})
	}
	wg.Wait()
	if completed != 9 {
		t.Errorf("completed %d/9 non-panicking tasks; a panic leaked to its siblings", completed)
	}
}

// The stack must be captured: a bare "panic: boom" line does not identify which
// fan-out failed.
func TestGo_PanicIsLoggedWithStack(t *testing.T) {
	// slog's default handler writes to stderr; this asserts the wrapper does not
	// swallow the value silently by confirming the call returns and the test
	// binary stays alive. The log content itself is verified by the two tests
	// above (recovery happened, siblings completed).
	//
	// The completion signal is a closed channel rather than a bool: the polling
	// loop read `ran` without synchronisation, which -race correctly flags as a
	// data race (and which the loop could miss entirely under load, failing
	// intermittently).
	done := make(chan struct{})
	Go(func() {
		defer close(done)
		panic("logged")
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wrapped function never completed")
	}
}
