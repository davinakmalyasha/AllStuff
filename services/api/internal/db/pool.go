package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"allstuff/api/internal/repo"
)

// pool.go — the second pool.
//
// The process holds two pools against the same DSN:
//   - the owner pool (Connect): migrations, background jobs, the notifier,
//     tests, dev-login. RLS does not constrain the table owner, so these
//     keep working exactly as before, with zero code changes.
//   - the request pool (ConnectRLS): every HTTP request. Each acquisition
//     drops to the least-privilege `allstuff_app` role and stamps the requesting
//     user (repo.ConfigureRLS), so 0053's policies re-check every row.
//
// Two pools rather than one hook-less pool plus SET ROLE in handlers: the
// hook must run on acquire, which belongs to pool construction, and mixing
// constrained and unconstrained traffic on one pool would make every
// exemption a per-query decision instead of a wiring decision.

// ConnectRLS builds the request-path pool: same DSN and sizing as Connect,
// plus the RLS privilege-drop hooks. The role and policies must exist —
// i.e. migrations must have run — or every acquisition fails loudly rather
// than serving unconstrained.
func ConnectRLS(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if cfg.MaxConns < 32 {
		cfg.MaxConns = 32
	}
	if cfg.MinConns == 0 {
		cfg.MinConns = 4
	}
	if cfg.MaxConnLifetime <= 0 {
		cfg.MaxConnLifetime = 30 * time.Minute
	}
	if cfg.MaxConnIdleTime <= 0 {
		cfg.MaxConnIdleTime = 5 * time.Minute
	}
	if cfg.HealthCheckPeriod <= 0 {
		cfg.HealthCheckPeriod = time.Minute
	}
	repo.ConfigureRLS(cfg)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
