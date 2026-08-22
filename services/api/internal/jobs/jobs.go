package jobs

import (
	"context"
	"log/slog"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/email"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/service"
)

// Run starts background jobs (ARCHITECTURE §4).
func Run(ctx context.Context, logger *slog.Logger, repos *repo.Repos, cfg config.Config, sender email.Sender) {
	logger.Info("jobs started")

	trending := service.NewTrending(repos)
	currency := service.NewCurrency(repos)
	digest := service.NewDigest(repos, sender)
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
	// Snapshot retention: keep 14 days of trend_snapshots; orphan media cleanup.
	if err := ops.PruneSnapshots(ctx); err != nil {
		logger.Warn("snapshot prune initial", "err", err)
	}
	if err := ops.CleanupOrphanMedia(ctx); err != nil {
		logger.Warn("media cleanup initial", "err", err)
	}

	trendTicker := time.NewTicker(10 * time.Minute)
	currencyTicker := time.NewTicker(time.Hour)
	digestTicker := time.NewTicker(24 * time.Hour)
	alertTicker := time.NewTicker(24 * time.Hour)
	purgeTicker := time.NewTicker(24 * time.Hour)
	opsTicker := time.NewTicker(24 * time.Hour)
	defer trendTicker.Stop()
	defer currencyTicker.Stop()
	defer digestTicker.Stop()
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
		case <-digestTicker.C:
			// Weekly digest on Mondays (PRD §5.7).
			if time.Now().Weekday() == time.Monday {
				if err := digest.SendWeekly(ctx); err != nil {
					logger.Warn("digest", "err", err)
				}
			}
			// Daily search alerts (PRD §5.1.2).
			if err := alerts.SendDaily(ctx); err != nil {
				logger.Warn("search alerts", "err", err)
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
