package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// TFA — 2FA state + recovery codes (PRD §5.9.1, §7.4 user_2fa).
type TFARepo struct{ pool pooler }

func (r *TFARepo) Get(ctx context.Context, userID string) (*TFAState, error) {
	var s TFAState
	err := r.pool.QueryRow(ctx, `
		SELECT user_id, totp_secret_encrypted, enabled_at, recovery_codes_hash
		FROM user_2fa WHERE user_id = $1`, userID).
		Scan(&s.UserID, &s.SecretEncrypted, &s.EnabledAt, &s.RecoveryCodesHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &s, err
}

func (r *TFARepo) UpsertSecret(ctx context.Context, userID, secretEncrypted string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_2fa (id, user_id, totp_secret_encrypted, recovery_codes_hash)
		VALUES ($1, $2, $3, '[]'::jsonb)
		ON CONFLICT (user_id) DO UPDATE SET totp_secret_encrypted = EXCLUDED.totp_secret_encrypted, enabled_at = NULL`,
		newUUID(), userID, secretEncrypted)
	return err
}

func (r *TFARepo) Enable(ctx context.Context, userID string, recoveryHashes []string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE user_2fa SET enabled_at = now(), recovery_codes_hash = $2
		WHERE user_id = $1`, userID, recoveryHashes)
	return err
}

func (r *TFARepo) Disable(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM user_2fa WHERE user_id = $1`, userID)
	return err
}

func (r *TFARepo) UseRecoveryCode(ctx context.Context, userID, hash string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE user_2fa
		SET recovery_codes_hash = (
			SELECT jsonb_agg(c) FROM (
				SELECT value AS c FROM jsonb_array_elements_text(recovery_codes_hash) WHERE value <> $2
			) t
		)
		WHERE user_id = $1`, userID, hash)
	return err
}

type TFAState struct {
	UserID            string
	SecretEncrypted   string
	EnabledAt         *time.Time
	RecoveryCodesHash []byte
}
