package repo

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repos groups all data access (pgx, parameterized SQL only — PRD §9.3).
type Repos struct {
	// q is the executor every method on Repos itself uses, and the same `pooler`
	// the sub-repos hold. It was a bare *pgxpool.Pool, which NewForTx never set -
	// so in transaction mode it stayed nil and every direct Exec/Query/QueryRow
	// on Repos panicked with a nil dereference rather than returning an error.
	//
	// That is a landmine rather than a visible bug, because production only ever
	// uses New() and so never exercised it. It surfaced through Confirm2FA's
	// best-effort `auth_events` insert, the one write on this path that calls
	// Repos.Exec directly instead of going through a sub-repo.
	q          pooler
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
	Billing    *BillingRepo
}

func New(pool *pgxpool.Pool) *Repos {
	return &Repos{
		q:          pool,
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
		Billing:    &BillingRepo{pool: pool},
	}
}

// NewForTx mirrors every repo onto an open transaction so services can run
// multi-step writes atomically (e.g. chat message insert + thread touch).
// Commit/Rollback stay with the caller.
func NewForTx(tx pgx.Tx) *Repos {
	return &Repos{
		q:          tx,
		Users:      &UserRepo{pool: tx},
		Sessions:   &SessionRepo{pool: tx},
		Categories: &CategoryRepo{pool: tx},
		Businesses: &BusinessRepo{pool: tx},
		Media:      &MediaRepo{pool: tx},
		Products:   &ProductRepo{pool: tx},
		Engagement: &EngagementRepo{pool: tx},
		Chat:       &ChatRepo{pool: tx},
		Push:       &PushRepo{pool: tx},
		Community:  &CommunityRepo{pool: tx},
		TFA:        &TFARepo{pool: tx},
		Billing:    &BillingRepo{pool: tx},
	}
}

func (r *Repos) Pool() *pgxpool.Pool { p, _ := r.q.(*pgxpool.Pool); return p }

func (r *Repos) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return r.q.Exec(ctx, sql, args...)
}

func (r *Repos) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return r.q.QueryRow(ctx, sql, args...)
}

func (r *Repos) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return r.q.Query(ctx, sql, args...)
}
