//go:build !race

package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/email"
	"bizverse/api/internal/security"
	"bizverse/api/internal/service"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
	"bizverse/api/internal/ws"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// jobs.Run itself is not covered here, and the reasons are worth stating rather
// than leaving as an unexplained gap:
//
//   - its branching lives behind tickers of 10 minutes to 24 hours, which no test
//     can reach without an interval seam that does not exist
//   - its boot path calls service.Currency.Sync, which makes a real HTTPS request
//     to a rates feed, so exercising it would put an external service in the test
//
// What IS covered is the logic those tickers delegate to: leader acquisition and
// the retention sweep, both of which hold the behaviour that can actually be
// wrong. The one Run path that is cheap and safe is asserted below.

// The scheduler lock is what stops two replicas running every job twice. A
// second instance must wait for shutdown rather than start a competing schedule,
// and must not report an error while it waits.
func TestAcquireLeaderExcludesASecondInstance(t *testing.T) {
	h := testutil.New(t)
	// acquireLeader takes a connection from repos.Pool(); the default harness
	// binds Repos to a transaction and Pool() returns nil.
	h.UsePool()
	logger := testLogger()

	ctx := context.Background()
	first, err := acquireLeader(ctx, logger, h.Repos)
	if err != nil || first == nil {
		t.Fatalf("first acquire: conn=%v err=%v", first, err)
	}
	// Mirror Run's own cleanup exactly. The advisory lock is session-scoped, so a
	// connection returned to the pool still holds it; leaking it here would make
	// every later test in this database look like a second replica.
	defer func() {
		_, _ = first.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('bizverse:jobs'))`)
		first.Release()
	}()

	blockedCtx, cancelBlocked := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		conn, err := acquireLeader(blockedCtx, logger, h.Repos)
		if conn != nil {
			conn.Release()
		}
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("the second instance returned while the first held the lock: %v", err)
	case <-time.After(400 * time.Millisecond):
		// Still waiting, which is the designed behaviour.
	}

	cancelBlocked()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("after shutdown: err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting instance did not return after its context was cancelled")
	}
}

// Once the holder releases, the lock is available again: a restart or a failover
// must be able to take over.
func TestAcquireLeaderReacquiresAfterRelease(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	logger := testLogger()

	first, err := acquireLeader(context.Background(), logger, h.Repos)
	if err != nil || first == nil {
		t.Fatalf("first acquire: conn=%v err=%v", first, err)
	}
	if _, err := first.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('bizverse:jobs'))`); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	first.Release()

	second, err := acquireLeader(context.Background(), logger, h.Repos)
	if err != nil || second == nil {
		t.Fatalf("reacquire after release: conn=%v err=%v", second, err)
	}
	_, _ = second.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('bizverse:jobs'))`)
	second.Release()
}

// Run must return promptly when the process is already shutting down, and must
// not reach the jobs it would otherwise start.
func TestRunReturnsImmediatelyWhenAlreadyShuttingDown(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test"})
	logger := testLogger()
	notifier := service.NewNotifier(h.Repos, cfg, sender, ws.NewHub(logger, nil), logger)
	billing := service.NewBilling(h.Repos, cfg, security.NewStripeClient("", ""), logger)

	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, logger, h.Repos, cfg, sender, notifier, billing)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return when its context was already cancelled")
	}
}

// retention is the only place that deletes from four tables on a schedule. The
// predicates are the whole of it, so each table gets an expired row and a row
// that must survive.
func TestRetentionRemovesOnlyExpiredRows(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	ctx := context.Background()
	u := testutil.User(t, h)

	// consumed_tokens: 48-hour cutoff.
	h.ExecPool(t, `INSERT INTO consumed_tokens (jti, consumed_at) VALUES ($1, now() - interval '49 hours')`, "expired-jti")
	h.ExecPool(t, `INSERT INTO consumed_tokens (jti, consumed_at) VALUES ($1, now() - interval '47 hours')`, "fresh-jti")

	// sessions: revoked more than 90 days ago. A live session is never touched,
	// however old it is - the predicate is on revoked_at, not created_at.
	h.ExecPool(t, `INSERT INTO sessions (id, user_id, token_hash, revoked_at) VALUES ($1,$2,$3, now() - interval '91 days')`,
		util.NewUUID(), u.ID, "revoked-old")
	h.ExecPool(t, `INSERT INTO sessions (id, user_id, token_hash, revoked_at) VALUES ($1,$2,$3, now() - interval '89 days')`,
		util.NewUUID(), u.ID, "revoked-new")
	h.ExecPool(t, `INSERT INTO sessions (id, user_id, token_hash, created_at) VALUES ($1,$2,$3, now() - interval '400 days')`,
		util.NewUUID(), u.ID, "never-revoked")

	// engagement_events: 90-day cutoff.
	target := util.NewUUID()
	h.ExecPool(t, `INSERT INTO engagement_events (user_id, target_type, target_id, signal, weight, occurred_at, dedupe_key)
		VALUES ($1,'business',$2,'view',1, now() - interval '91 days', 'old-event')`, u.ID, target)
	h.ExecPool(t, `INSERT INTO engagement_events (user_id, target_type, target_id, signal, weight, occurred_at, dedupe_key)
		VALUES ($1,'business',$2,'view',1, now() - interval '89 days', 'new-event')`, u.ID, target)

	// auth_events: 180-day cutoff.
	h.ExecPool(t, `INSERT INTO auth_events (id, user_id, event, created_at) VALUES ($1,$2,'login', now() - interval '181 days')`,
		util.NewUUID(), u.ID)
	h.ExecPool(t, `INSERT INTO auth_events (id, user_id, event, created_at) VALUES ($1,$2,'login', now() - interval '179 days')`,
		util.NewUUID(), u.ID)

	retention(ctx, h.Repos, testLogger())

	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := h.Pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("counting: %v", err)
		}
		return n
	}

	if n := count(`SELECT count(*) FROM consumed_tokens WHERE jti='expired-jti'`); n != 0 {
		t.Error("an expired consumed token survived")
	}
	if n := count(`SELECT count(*) FROM consumed_tokens WHERE jti='fresh-jti'`); n != 1 {
		t.Error("a fresh consumed token was deleted")
	}

	if n := count(`SELECT count(*) FROM sessions WHERE token_hash='revoked-old'`); n != 0 {
		t.Error("a session revoked 91 days ago survived")
	}
	if n := count(`SELECT count(*) FROM sessions WHERE token_hash='revoked-new'`); n != 1 {
		t.Error("a session revoked 89 days ago was deleted")
	}
	// The one that matters most: a session that has never been revoked is not
	// retention's business, however old it is. Deleting it would log a user out
	// for the crime of having an old account.
	//
	// What actually protects it is SQL's three-valued logic, not the predicate's
	// explicit `revoked_at IS NOT NULL`: `NULL < now() - interval` is NULL, which
	// is not true, so an unrevoked row cannot match. Removing that clause is an
	// equivalent mutation and this test cannot distinguish it - the assertion is
	// on the outcome, which is the thing that must hold either way.
	if n := count(`SELECT count(*) FROM sessions WHERE token_hash='never-revoked'`); n != 1 {
		t.Error("a live session was deleted by retention")
	}

	if n := count(`SELECT count(*) FROM engagement_events WHERE dedupe_key='old-event'`); n != 0 {
		t.Error("an engagement event older than 90 days survived")
	}
	if n := count(`SELECT count(*) FROM engagement_events WHERE dedupe_key='new-event'`); n != 1 {
		t.Error("a recent engagement event was deleted")
	}

	if n := count(`SELECT count(*) FROM auth_events WHERE created_at < now() - interval '180 days'`); n != 0 {
		t.Error("an auth event older than 180 days survived")
	}
	if n := count(`SELECT count(*) FROM auth_events`); n != 1 {
		t.Error("retention deleted a recent auth event")
	}

	// A second sweep over an already-clean database is a no-op rather than an
	// error: the ticker calls this every 24 hours regardless.
	retention(ctx, h.Repos, testLogger())
	if n := count(`SELECT count(*) FROM auth_events`); n != 1 {
		t.Error("the second sweep changed the row set")
	}
}
