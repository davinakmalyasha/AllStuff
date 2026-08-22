package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"bizverse/api/internal/domain"
)

type UserRepo struct{ pool pooler }

type pooler interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const UserColumns = `id, email, password_hash, name, username, avatar_url, bio, timezone,
	profile_links, email_verified_at, role, status, suspended_until, ban_reason, created_at, updated_at`

func ScanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.Username,
		&u.AvatarURL, &u.Bio, &u.Timezone, &u.ProfileLinks, &u.EmailVerifiedAt,
		&u.Role, &u.Status, &u.SuspendedUntil, &u.BanReason, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, username, timezone)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		u.ID, u.Email, u.PasswordHash, u.Name, u.Username, u.Timezone)
	return err
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+UserColumns+` FROM users WHERE email = $1 AND deleted_at IS NULL`, email)
	u, err := ScanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+UserColumns+` FROM users WHERE username = $1 AND deleted_at IS NULL`, username)
	u, err := ScanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+UserColumns+` FROM users WHERE id = $1 AND deleted_at IS NULL`, id)
	u, err := ScanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func (r *UserRepo) UsernameTaken(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE username = $1 AND deleted_at IS NULL)`, username).Scan(&exists)
	return exists, err
}

func (r *UserRepo) MarkEmailVerified(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now() WHERE id = $1`, id)
	return err
}

func (r *UserRepo) SetPassword(ctx context.Context, id, hash string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	return err
}

func (r *UserRepo) UpdateEmail(ctx context.Context, id, email string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users SET email = $2, email_verified_at = NULL, updated_at = now() WHERE id = $1`,
		id, email)
	return err
}

// allowedProfileFields: whitelist for PATCH /me (PRD §5.9.2).
var allowedProfileFields = map[string]bool{
	"name": true, "bio": true, "timezone": true, "avatar_url": true, "profile_links": true,
}

func (r *UserRepo) UpdateProfile(ctx context.Context, id string, fields map[string]any) error {
	cols := []string{"updated_at = now()"}
	args := []any{id}
	for k, v := range fields {
		if !allowedProfileFields[k] {
			return fmt.Errorf("field not allowed: %s", k)
		}
		args = append(args, v)
		cols = append(cols, k+" = $"+itoa(len(args)))
	}
	sql := "UPDATE users SET " + strings.Join(cols, ", ") + " WHERE id = $1"
	_, err := r.pool.Exec(ctx, sql, args...)
	return err
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
