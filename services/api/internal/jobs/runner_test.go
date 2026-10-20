package jobs

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// No build tag on purpose. Every other test in this package needs a database and
// is therefore excluded from the race detector, which meant
// `go test -race ./internal/jobs/` ran nothing at all - on the most concurrent
// code in the repository. jobRunner is where that concurrency lives, and it needs
// no database.

func runnerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitNotBusy(t *testing.T, r *jobRunner, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.busy(name) {
		if time.Now().After(deadline) {
			t.Fatalf("job %q never completed", name)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A job that takes longer than its tick must not overlap itself. Cross-replica
// duplication is the leader lock's job; this guard is for one instance.
func TestJobRunnerRefusesOverlappingRuns(t *testing.T) {
	r := newJobRunner(runnerLogger())
	started := make(chan struct{})
	release := make(chan struct{})

	if !r.spawn("trending", func() { close(started); <-release }) {
		t.Fatal("the first spawn did not start")
	}
	<-started

	if r.spawn("trending", func() { t.Error("an overlapping run of the same job started") }) {
		t.Error("a second spawn of a running job started")
	}

	// A different job is unaffected: one slow digest must not freeze the rest.
	otherDone := make(chan struct{})
	if !r.spawn("currency", func() { close(otherDone) }) {
		t.Error("a different job was refused because trending was running")
	}
	<-otherDone

	close(release)
	waitNotBusy(t, r, "trending")

	// The name frees when the run returns, so the next tick can run it.
	if !r.spawn("trending", func() {}) {
		t.Error("the job could not run again after completing")
	}
	waitNotBusy(t, r, "trending")
}

// A panic in a job goroutine would take the whole process down, and the name
// would never be freed - so a single bad tick would permanently stop that job.
func TestJobRunnerRecoversAPanicAndFreesTheName(t *testing.T) {
	r := newJobRunner(runnerLogger())
	ran := make(chan struct{})

	if !r.spawn("weekly_digest", func() { close(ran); panic("boom") }) {
		t.Fatal("the spawn did not start")
	}
	<-ran
	waitNotBusy(t, r, "weekly_digest")

	if !r.spawn("weekly_digest", func() {}) {
		t.Error("the job could not run again after panicking; the name was not freed")
	}
	waitNotBusy(t, r, "weekly_digest")
}

// The ticker goroutines call spawn concurrently, so the map and the guard have to
// be safe under contention. This is the assertion the race detector is here for.
func TestJobRunnerIsSafeUnderConcurrentSpawns(t *testing.T) {
	r := newJobRunner(runnerLogger())

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.spawn("same", func() {})
		}()
	}
	wg.Wait()
	waitNotBusy(t, r, "same")

	// And with distinct names, so nothing is serialised away by the guard.
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.spawn(string(rune('a'+i%26)), func() {})
		}(i)
	}
	wg.Wait()
}
