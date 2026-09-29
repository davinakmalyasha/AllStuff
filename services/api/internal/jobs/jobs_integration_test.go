//go:build !race

package jobs

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"bizverse/api/internal/testutil"
)

// The scheduler had no tests, and it contains the mechanism that stands between
// a bad deploy and a duplicate blast to every opted-in user.
//
// claimPeriod is an INSERT ... ON CONFLICT DO NOTHING into a (job, period) ledger.
// It is what makes the weekly digest and the daily search alerts at-most-once per
// period, and releasePeriod is what makes a failure retry on the next tick
// instead of silently skipping the rest of the period. Between them they are the
// difference between "one digest went out" and "every opted-in user gets the
// same email again, and again, on every replica that boots".
//
// Neither is a hard-to-test function, which is precisely why it had no test: the
// logic is two lines, and the failure it prevents is a production incident.
//
// WHY THESE TESTS USE THE POOL, NOT THE TEST TRANSACTION
// ------------------------------------------------------
// claimPeriod calls repos.Exec, and Repos.Exec is backed by the pool field, which
// NewForTx leaves nil. A tx-bound Repos panics here rather than failing a
// useful assertion — repo.go:69 dereferences it unconditionally.
//
// Using the pool means these tests do not get the transaction-rollback cleanup
// the other integration tests rely on. That is acceptable and still fully
// isolated, because the isolation here comes one level up: every test gets its
// own database CLONED from the template, and the harness DROPs that database
// afterwards. Nothing survives the test either way — the ledger rows die with
// the database, not with a ROLLBACK.
//
// A useful side effect: each pool Exec is its own implicit transaction, so the
// expected-error case below needs no SAVEPOINT. A failed statement cannot poison
// anything.

// TestClaimPeriodIsAtMostOnce is the core guarantee. Two claims for the same
// (job, period) must produce exactly one winner, and the loser must be told it
// lost rather than being handed a success it did not earn.
func TestClaimPeriodIsAtMostOnce(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	repos := h.PoolRepos()

	if !claimPeriod(ctx, repos, "weekly_digest", "2026-W38") {
		t.Fatal("the first claim for a period must succeed")
	}
	if claimPeriod(ctx, repos, "weekly_digest", "2026-W38") {
		t.Error("a second claim for the same (job, period) succeeded; the digest would be sent twice")
	}

	// A different period is a different claim.
	if !claimPeriod(ctx, repos, "weekly_digest", "2026-W39") {
		t.Error("a new period must be claimable")
	}
	// A different job in the same period is a different claim.
	if !claimPeriod(ctx, repos, "search_alerts", "2026-W38") {
		t.Error("a different job must be claimable in a period already claimed by another job")
	}

	// Exactly three rows, so the assertions above are not passing because the
	// INSERT is silently a no-op.
	if n := countClaims(t, h); n != 3 {
		t.Errorf("job_runs has %d rows, want 3", n)
	}
}

// TestReleasePeriodAllowsRetry is the other half. A job that fails must give
// its slot back, or the rest of the period is lost — the digest silently never
// goes out, and nothing reports it.
func TestReleasePeriodAllowsRetry(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	repos := h.PoolRepos()

	if !claimPeriod(ctx, repos, "weekly_digest", "2026-W40") {
		t.Fatal("first claim must succeed")
	}
	releasePeriod(ctx, repos, "weekly_digest", "2026-W40")

	// The slot is free again.
	if !claimPeriod(ctx, repos, "weekly_digest", "2026-W40") {
		t.Error("after release the period must be claimable again, otherwise a single failed run loses the whole period")
	}
	// And only one claim is live.
	if claimPeriod(ctx, repos, "weekly_digest", "2026-W40") {
		t.Error("the retry must claim the slot, not duplicate it")
	}
}

// TestReleaseOfAnUnclaimedPeriodIsHarmless covers the path releasePeriod is
// actually called on: it discards its error, so it runs on paths where the claim
// may not have been taken. It must also not delete a claim for a DIFFERENT
// period of the same job — that is the bug that would silently skip a week.
func TestReleaseOfAnUnclaimedPeriodIsHarmless(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	repos := h.PoolRepos()

	releasePeriod(ctx, repos, "weekly_digest", "2099-W01") // never claimed

	if !claimPeriod(ctx, repos, "weekly_digest", "2026-W41") {
		t.Fatal("claim after a stray release must succeed")
	}
	releasePeriod(ctx, repos, "weekly_digest", "2026-W40") // wrong period

	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM job_runs WHERE job = 'weekly_digest'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("weekly_digest has %d claims, want 1; a release must only remove its own (job, period)", n)
	}
}

// TestClaimPeriodIsBackedByAUniquePair confirms the guarantee is the schema's
// and not just the query's shape. It inserts the duplicate directly, bypassing
// claimPeriod entirely, so the test still holds if someone rewrites the INSERT
// to a SELECT-then-INSERT — which is the classic way this guarantee regresses.
func TestClaimPeriodIsBackedByAUniquePair(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	repos := h.PoolRepos()

	if !claimPeriod(ctx, repos, "weekly_digest", "2026-W42") {
		t.Fatal("claim failed")
	}

	tag, err := repos.Exec(ctx,
		`INSERT INTO job_runs (job, period) VALUES ('weekly_digest', '2026-W42')`)
	if err == nil {
		t.Fatal("the (job, period) pair must be unique in the schema; without it two replicas could both believe they own the period")
	}
	var pgErr *pgconn.PgError
	if !asPgError(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("expected unique-violation 23505, got %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Errorf("the duplicate insert reported %d rows affected; it must affect none", tag.RowsAffected())
	}

	// The losing insert must not have added a row.
	if n := countClaims(t, h); n != 1 {
		t.Errorf("job_runs has %d rows, want 1", n)
	}
}

func countClaims(t *testing.T, h *testutil.H) int {
	t.Helper()
	var n int
	if err := h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM job_runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func asPgError(err error, target **pgconn.PgError) bool {
	pe, ok := err.(*pgconn.PgError)
	if ok {
		*target = pe
	}
	return ok
}
