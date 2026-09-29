package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
)

// SessionRepo — refresh-token registry (PRD §5.9.1, §7.4).
type SessionRepo struct{ pool pooler }

const sessionColumns = `id, user_id, token_hash, ip, user_agent, created_at, last_seen_at, revoked_at`

func scanSession(row pgx.Row) (*domain.Session, error) {
	var s domain.Session
	if err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.IP, &s.UserAgent,
		&s.CreatedAt, &s.LastSeenAt, &s.RevokedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SessionRepo) Create(ctx context.Context, s *domain.Session) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, token_hash, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5)`,
		s.ID, s.UserID, s.TokenHash, s.IP, s.UserAgent)
	return err
}

func (r *SessionRepo) GetByTokenHash(ctx context.Context, hash string) (*domain.Session, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE token_hash = $1`, hash)
	s, err := scanSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

func (r *SessionRepo) Touch(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET last_seen_at = now() WHERE id = $1`, id)
	return err
}

func (r *SessionRepo) Revoke(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = COALESCE(revoked_at, now()) WHERE id = $1`, id)
	return err
}

// RevokeAllExcept revokes every live session for a user, optionally keeping
// one.
//
// The empty-keepID case is explicit rather than passed through as a sentinel.
// `sessions.id` is a uuid, so `id <> $2` with $2 = ” makes Postgres raise
// `invalid input syntax for type uuid: ""` (and pgx's UUID codec rejects it
// client-side first). Because the statement errored, three incident-response
// controls silently did nothing:
//
//	auth.go ResetPassword  — the password changed, the revocation failed, the
//	                         handler returned 500, and every other session
//	                         (including a stolen one) stayed alive.
//	auth.go Refresh        — replaying a rotated token is the anti-theft signal,
//	                         and its "revoke the family" sweep was a no-op.
//	auth.go ChangePassword — keepSessionID is "" whenever the refresh cookie is
//	                         absent, so the same failure.
//
// Passing a real UUID (the RevokeOthers and handleSessionRevoke paths) worked,
// which is why this went unnoticed. A separate branch keeps the common case a
// single indexed UPDATE with no comparison at all.
func (r *SessionRepo) RevokeAllExcept(ctx context.Context, userID, keepID string) error {
	if keepID == "" {
		_, err := r.pool.Exec(ctx,
			`UPDATE sessions SET revoked_at = COALESCE(revoked_at, now())
			 WHERE user_id = $1 AND revoked_at IS NULL`, userID)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = COALESCE(revoked_at, now())
		 WHERE user_id = $1 AND id <> $2::uuid AND revoked_at IS NULL`, userID, keepID)
	return err
}

func (r *SessionRepo) ListByUser(ctx context.Context, userID string) ([]*domain.Session, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE user_id = $1 ORDER BY created_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
