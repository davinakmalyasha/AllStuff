package jobs

import (
	"context"
	"log/slog"
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
func Run(ctx context.Context, logger *slog.Logger, repos *repo.Repos, cfg config.Config, sender email.Sender, notifier *service.Notifier) {
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

	// Recompute immediately at boot, then every 10 minutes.
	if err := trending.Compute(ctx); err != nil {
		logger.Error("trending initial compute", "err", err)
	}
	// Currency sync (PRD D5): hourly, best-effort.
	if err := currency.Sync(ctx); err != nil {
		logger.Warn("currency initial sync", "err", err)
	}
	// Notification retention: prune past expires_at (default 90 days, PRD §5.7).
	if _, err := repos.Engagement.PurgeExpired(ctx); err != nil {
		logger.Warn("notification purge initial", "err", err)
	}
	// Account deletion grace (14 days) expiry: hard-purge soft-deleted
	// users (PRD §5.9.2).
	if _, err := service.NewAuth(repos, cfg, sender, logger).PurgeExpiredDeletions(ctx); err != nil {
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
	// Digest scheduling: check hourly whether it's Monday-UTC and not yet
	// sent this ISO week (job_runs marker). A 24h ticker phased at boot time
	// skipped or double-sent around DST/week boundaries.
	digestCheckTicker := time.NewTicker(time.Hour)
	alertTicker := time.NewTicker(24 * time.Hour)
	purgeTicker := time.NewTicker(24 * time.Hour)
	opsTicker := time.NewTicker(24 * time.Hour)
	defer trendTicker.Stop()
	defer currencyTicker.Stop()
	defer digestCheckTicker.Stop()
	defer alertTicker.Stop()
	defer purgeTicker.Stop()
	defer opsTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("jobs stopped")
			return
		case <-trendTicker.C:
			start := time.Now()
			if err := trending.Compute(ctx); err != nil {
				logger.Error("trending recompute", "err", err)
				continue
			}
			logger.Info("trending recompute done", "dur_ms", time.Since(start).Milliseconds())
		case <-currencyTicker.C:
			if err := currency.Sync(ctx); err != nil {
				logger.Warn("currency sync", "err", err)
				continue
			}
			logger.Info("currency sync done")
		case <-digestCheckTicker.C:
			// Weekly digest on Mondays UTC (PRD §5.7), idempotent per week.
			now := time.Now().UTC()
			if now.Weekday() == time.Monday {
				if oncePerPeriod(ctx, repos, "weekly_digest", now.Format("2006-W02")) {
					if err := digest.SendWeekly(ctx); err != nil {
						logger.Warn("digest", "err", err)
					}
				}
			}
		case <-alertTicker.C:
			// Daily search alerts (PRD §5.1.2), idempotent per day.
			day := time.Now().UTC().Format("2006-01-02")
			if oncePerPeriod(ctx, repos, "search_alerts", day) {
				if err := alerts.SendDaily(ctx); err != nil {
					logger.Warn("search alerts", "err", err)
				}
			}
		case <-purgeTicker.C:
			n, err := repos.Engagement.PurgeExpired(ctx)
			if err != nil {
				logger.Warn("notification purge", "err", err)
				continue
			}
			if n > 0 {
				logger.Info("notification purge", "removed", n)
			}
			if dn, err := service.NewAuth(repos, cfg, sender, logger).PurgeExpiredDeletions(ctx); err != nil {
				logger.Warn("deletion purge", "err", err)
			} else if dn > 0 {
				logger.Info("deletion purge", "removed", dn)
			}
			retention(ctx, repos, logger)
		case <-opsTicker.C:
			if err := ops.PruneSnapshots(ctx); err != nil {
				logger.Warn("snapshot prune", "err", err)
			}
			if err := ops.CleanupOrphanMedia(ctx); err != nil {
				logger.Warn("media cleanup", "err", err)
			}
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

// oncePerPeriod records and reports whether this job/period combination is
// running for the first time — restarts mid-week no longer double-send.
func oncePerPeriod(ctx context.Context, repos *repo.Repos, job, period string) bool {
	tag, err := repos.Exec(ctx,
		`INSERT INTO job_runs (job, period) VALUES ($1, $2) ON CONFLICT (job, period) DO NOTHING`,
		job, period)
	return err == nil && tag.RowsAffected() > 0
}

// retention trims append-only tables that previously grew forever
// (privacy/GDPR + storage hygiene).
func retention(ctx context.Context, repos *repo.Repos, logger *slog.Logger) {
	stmts := []struct{ name, sql string }{
		{"consumed_tokens", `DELETE FROM consumed_tokens WHERE consumed_at < now() - interval '48 hours'`},
		{"sessions_revoked", `DELETE FROM sessions WHERE revoked_at IS NOT NULL AND revoked_at < now() - interval '90 days'`},
		{"engagement_events", `DELETE FROM engagement_events WHERE occurred_at < now() - interval '90 days'`},
		{"auth_events", `DELETE FROM auth_events WHERE created_at < now() - interval '180 days'`},
	}
	for _, st := range stmts {
		if tag, err := repos.Exec(ctx, st.sql); err != nil {
			logger.Warn("retention "+st.name, "err", err)
		} else if tag.RowsAffected() > 0 {
			logger.Info("retention "+st.name, "removed", tag.RowsAffected())
		}
	}
}
