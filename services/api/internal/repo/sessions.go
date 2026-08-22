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

func (r *SessionRepo) RevokeAllExcept(ctx context.Context, userID, keepID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = COALESCE(revoked_at, now())
		 WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL`, userID, keepID)
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
