package repo

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repos groups all data access (pgx, parameterized SQL only — PRD §9.3).
type Repos struct {
	pool       *pgxpool.Pool
	Users      *UserRepo
	Sessions   *SessionRepo
	Categories *CategoryRepo
	Businesses *BusinessRepo
	Media      *MediaRepo
	Products   *ProductRepo
	Engagement *EngagementRepo
	Chat       *ChatRepo
	Push       *PushRepo
	Community  *CommunityRepo
	TFA        *TFARepo
}

func New(pool *pgxpool.Pool) *Repos {
	return &Repos{
		pool:       pool,
		Users:      &UserRepo{pool: pool},
		Sessions:   &SessionRepo{pool: pool},
		Categories: &CategoryRepo{pool: pool},
		Businesses: &BusinessRepo{pool: pool},
		Media:      &MediaRepo{pool: pool},
		Products:   &ProductRepo{pool: pool},
		Engagement: &EngagementRepo{pool: pool},
		Chat:       &ChatRepo{pool: pool},
		Push:       &PushRepo{pool: pool},
		Community:  &CommunityRepo{pool: pool},
		TFA:        &TFARepo{pool: pool},
	}
}

func (r *Repos) Pool() *pgxpool.Pool { return r.pool }

func (r *Repos) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return r.pool.Exec(ctx, sql, args...)
}

func (r *Repos) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return r.pool.QueryRow(ctx, sql, args...)
}

func (r *Repos) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return r.pool.Query(ctx, sql, args...)
}
