package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bizverse/api/internal/config"
	"bizverse/api/internal/email"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/service"
)

// Run starts background jobs (ARCHITECTURE §4). A Postgres advisory lock
// elects a single leader per cluster so scaled-out replicas don't double-run
// digests/alerts/currency writes; non-leaders idle until shutdown. A failed
// lock attempt at boot retries with backoff instead of silently running
// leaderless (two replicas booting during a DB hiccup would otherwise both
// fan out duplicate emails).
func Run(ctx context.Context, logger *slog.Logger, repos *repo.Repos, cfg config.Config, sender email.Sender, notifier *service.Notifier, billing *service.Billing) {
	conn, err := acquireLeader(ctx, logger, repos)
	if conn != nil {
		defer conn.Release()
		defer func() {
			_, _ = conn.Exec(context.WithoutCancel(ctx),
				`SELECT pg_advisory_unlock(hashtext('bizverse:jobs'))`)
		}()
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		logger.Error("jobs disabled: could not acquire scheduler lock", "err", err)
		return
	}

	logger.Info("jobs started")

	trending := service.NewTrending(repos, notifier)
	currency := service.NewCurrency(repos)
	digest := service.NewDigest(repos, sender, cfg.PublicURL)
	alerts := service.NewSearchAlerts(repos, sender, cfg)
	ops := service.NewOps(repos, cfg.MediaDir)
	authPurger := service.NewAuth(repos, cfg, sender, logger)

	// Recompute immediately at boot, then every 10 minutes.
	if err := trending.Compute(ctx); err != nil {
		logger.Error("trending initial compute", "err", err)
	}
	// Currency sync (PRD D5): hourly, best-effort.
	if err := currency.Sync(ctx); err != nil {
		logger.Warn("currency initial sync", "err", err)
	}
	// Notification retention: prune past expires_at (default 90 days, PRD 5.7).
	if _, err := repos.Engagement.PurgeExpired(ctx); err != nil {
		logger.Warn("notification purge initial", "err", err)
	}
	// Account deletion grace (14 days) expiry: hard-purge soft-deleted
	// users (PRD 5.9.2).
	if _, err := authPurger.PurgeExpiredDeletions(ctx); err != nil {
		logger.Warn("deletion purge initial", "err", err)
	}
	// Snapshot retention: keep 14 days of trend_snapshots; orphan media cleanup.
	if err := ops.PruneSnapshots(ctx); err != nil {
		logger.Warn("snapshot prune initial", "err", err)
	}
	if err := ops.CleanupOrphanMedia(ctx); err != nil {
		logger.Warn("media cleanup initial", "err", err)
	}

	trendTicker := time.NewTicker(10 * time.Minute)
	currencyTicker := time.NewTicker(time.Hour)
	// Digest scheduling: check hourly whether the current ISO week's period
	// has been claimed yet (job_runs marker). A 24h ticker phased at boot
	// time skipped or double-sent around DST/week boundaries.
	digestCheckTicker := time.NewTicker(time.Hour)
	alertTicker := time.NewTicker(24 * time.Hour)
	purgeTicker := time.NewTicker(24 * time.Hour)
	opsTicker := time.NewTicker(24 * time.Hour)
	billingTicker := time.NewTicker(6 * time.Hour)
	defer trendTicker.Stop()
	defer currencyTicker.Stop()
	defer digestCheckTicker.Stop()
	defer alertTicker.Stop()
	defer purgeTicker.Stop()
	defer opsTicker.Stop()
	defer billingTicker.Stop()

	// Each ticker runs in its own goroutine so one slow job (a Monday digest
	// over thousands of recipients) no longer freezes trending/currency/purges
	// behind a single select loop. `running` guards each job against
	// overlapping with itself when a run exceeds its interval; cross-replica
	// duplication is already excluded by the leader lock.
	var runningMu sync.Mutex
	running := map[string]bool{}
	spawn := func(name string, fn func(ctx context.Context)) {
		runningMu.Lock()
		if running[name] {
			runningMu.Unlock()
			return
		}
		running[name] = true
		runningMu.Unlock()
		go func() {
			defer func() {
				runningMu.Lock()
				delete(running, name)
				runningMu.Unlock()
				// A panic in a job goroutine is unrecoverable from outside and
				// takes the whole process down. recover() was previously
				// present only in the HTTP middleware, so a nil deref in any
				// detached job (trending recompute, digest fan-out, retention
				// sweep) was a full outage rather than one failed tick.
				if r := recover(); r != nil {
					logger.Error("job panicked", "job", name, "panic", r, "stack", string(debug.Stack()))
				}
			}()
			fn(ctx)
		}()
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("jobs stopped")
			return
		case <-trendTicker.C:
			start := time.Now()
			spawn("trending", func(ctx context.Context) {
				if err := trending.Compute(ctx); err != nil {
					logger.Error("trending recompute", "err", err)
					return
				}
				logger.Info("trending recompute done", "dur_ms", time.Since(start).Milliseconds())
			})
		case <-currencyTicker.C:
			spawn("currency", func(ctx context.Context) {
				if err := currency.Sync(ctx); err != nil {
					logger.Warn("currency sync", "err", err)
					return
				}
				logger.Info("currency sync done")
			})
		case <-digestCheckTicker.C:
			// Weekly digest (PRD 5.7): every hourly tick tries to claim the
			// current ISO-week period; claimPeriod dedupes, so the first
			// tick after the week rolls over wins and later ticks no-op.
			// Go layouts have NO week verb ("W" is literal), so the old
			// Format("2006-W02") keyed on day-of-month; use ISOWeek().
			now := time.Now().UTC()
			y, w := now.ISOWeek()
			period := fmt.Sprintf("%04d-W%02d", y, w)
			spawn("weekly_digest", func(ctx context.Context) {
				if !claimPeriod(ctx, repos, "weekly_digest", period) {
					return
				}
				if err := digest.SendWeekly(ctx); err != nil {
					// Release the slot: a failed run must not burn the
					// whole week's digest; next hourly check retries.
					releasePeriod(context.WithoutCancel(ctx), repos, "weekly_digest", period)
					logger.Warn("digest", "err", err)
				}
			})
		case <-alertTicker.C:
			// Daily search alerts (PRD 5.1.2), idempotent per day.
			day := time.Now().UTC().Format("2006-01-02")
			spawn("search_alerts", func(ctx context.Context) {
				if !claimPeriod(ctx, repos, "search_alerts", day) {
					return
				}
				if err := alerts.SendDaily(ctx); err != nil {
					releasePeriod(context.WithoutCancel(ctx), repos, "search_alerts", day)
					logger.Warn("search alerts", "err", err)
				}
			})
		case <-purgeTicker.C:
			spawn("purges", func(ctx context.Context) {
				n, err := repos.Engagement.PurgeExpired(ctx)
				if err != nil {
					logger.Warn("notification purge", "err", err)
				} else if n > 0 {
					logger.Info("notification purge", "removed", n)
				}
				if dn, err := authPurger.PurgeExpiredDeletions(ctx); err != nil {
					logger.Warn("deletion purge", "err", err)
				} else if dn > 0 {
					logger.Info("deletion purge", "removed", dn)
				}
				retention(ctx, repos, logger)
			})
		case <-opsTicker.C:
			spawn("ops", func(ctx context.Context) {
				if err := ops.PruneSnapshots(ctx); err != nil {
					logger.Warn("snapshot prune", "err", err)
				}
				if err := ops.CleanupOrphanMedia(ctx); err != nil {
					logger.Warn("media cleanup", "err", err)
				}
			})
		case <-billingTicker.C:
			// Billing reconciliation (Phase 7.1). Stripe delivers webhooks
			// at-least-once, so a delivery can be lost; without this sweep a
			// canceled subscription would keep granting entitlements forever
			// and an active one would never be noticed if `created` was missed.
			// Cheap: the query is index-backed and usually returns zero rows.
			spawn("billing_reconcile", func(ctx context.Context) {
				if err := billing.Reconcile(ctx); err != nil {
					logger.Warn("billing reconcile", "err", err)
				}
			})
		}
	}
}

// acquireLeader loops until the advisory lock is held, the context is done,
// or a hard error persists past the retry budget.
func acquireLeader(ctx context.Context, logger *slog.Logger, repos *repo.Repos) (*pgxpool.Conn, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		conn, err := repos.Pool().Acquire(ctx)
		if err != nil {
			return nil, err
		}
		var leader bool
		qerr := conn.QueryRow(ctx,
			`SELECT pg_try_advisory_lock(hashtext('bizverse:jobs'))`).Scan(&leader)
		if qerr == nil && leader {
			return conn, nil
		}
		if qerr != nil {
			// DB hiccup: release and retry with backoff.
			conn.Release()
			if time.Now().After(deadline) {
				return nil, qerr
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		// Lock held by another replica: idle until shutdown (leader semantics).
		logger.Info("jobs disabled: another instance holds the scheduler lock")
		<-ctx.Done()
		conn.Release()
		return nil, ctx.Err()
	}
}

// claimPeriod atomically claims a job/period slot (INSERT .. ON CONFLICT DO
// NOTHING). Failed runs call releasePeriod so the next tick retries instead
// of silently skipping the rest of the period.
func claimPeriod(ctx context.Context, repos *repo.Repos, job, period string) bool {
	tag, err := repos.Exec(ctx,
		`INSERT INTO job_runs (job, period) VALUES ($1, $2) ON CONFLICT (job, period) DO NOTHING`,
		job, period)
	return err == nil && tag.RowsAffected() > 0
}

func releasePeriod(ctx context.Context, repos *repo.Repos, job, period string) {
	_, _ = repos.Exec(ctx, `DELETE FROM job_runs WHERE job = $1 AND period = $2`, job, period)
}

// retention trims append-only tables that previously grew forever
// (privacy/GDPR + storage hygiene). Deletes are chunked by ctid so no single
// transaction holds millions of dead tuples (WAL spike, autovacuum lag).
func retention(ctx context.Context, repos *repo.Repos, logger *slog.Logger) {
	stmts := []struct{ name, table, pred string }{
		{"consumed_tokens", "consumed_tokens", `consumed_at < now() - interval '48 hours'`},
		{"sessions_revoked", "sessions", `revoked_at IS NOT NULL AND revoked_at < now() - interval '90 days'`},
		{"engagement_events", "engagement_events", `occurred_at < now() - interval '90 days'`},
		{"auth_events", "auth_events", `created_at < now() - interval '180 days'`},
	}
	for _, st := range stmts {
		var total int64
		for {
			tag, err := repos.Exec(ctx,
				`DELETE FROM `+st.table+` WHERE ctid IN (
					SELECT ctid FROM `+st.table+` WHERE `+st.pred+` LIMIT 50000)`)
			if err != nil {
				logger.Warn("retention "+st.name, "err", err)
				break
			}
			n := tag.RowsAffected()
			total += n
			if n < 50000 {
				break
			}
		}
		if total > 0 {
			logger.Info("retention "+st.name, "removed", total)
		}
	}
}
