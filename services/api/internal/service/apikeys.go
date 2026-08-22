package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
)

// APIKeys — public read API credentials (PRD §9.6). Keys are shown once at
// creation; only SHA-256 hashes are stored. /api/v2/* is read-only.
type APIKeys struct {
	repos *repo.Repos
}

func NewAPIKeys(repos *repo.Repos) *APIKeys { return &APIKeys{repos: repos} }

type APIKey struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// Create issues a key; the raw value is returned exactly once.
func (k *APIKeys) Create(ctx context.Context, userID, name string) (*APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 60 {
		return nil, "", domain.ErrValidation.WithField("name", "Name must be 1–60 characters.")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", err
	}
	rawKey := "bv_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawKey))
	key := &APIKey{ID: newUUID(), UserID: userID, Name: name, Prefix: rawKey[:10], Scopes: []string{"read"}}
	_, err := k.repos.Exec(ctx, `
		INSERT INTO api_keys (id, user_id, name, key_hash, prefix) VALUES ($1, $2, $3, $4, $5)`,
		key.ID, key.UserID, key.Name, hex.EncodeToString(sum[:]), key.Prefix)
	if err != nil {
		return nil, "", err
	}
	return key, rawKey, nil
}

func (k *APIKeys) List(ctx context.Context, userID string) ([]*APIKey, error) {
	rows, err := k.repos.Query(ctx, `
		SELECT id, user_id, name, prefix, scopes, created_at, revoked_at, last_used_at
		FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIKey{}
	for rows.Next() {
		var key APIKey
		if err := rows.Scan(&key.ID, &key.UserID, &key.Name, &key.Prefix, &key.Scopes,
			&key.CreatedAt, &key.RevokedAt, &key.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, &key)
	}
	return out, rows.Err()
}

func (k *APIKeys) Revoke(ctx context.Context, userID, id string) error {
	_, err := k.repos.Exec(ctx, `
		UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

// Valid returns the key row if rawKey matches and is not revoked.
func (k *APIKeys) Valid(ctx context.Context, rawKey string) (*APIKey, error) {
	sum := sha256.Sum256([]byte(rawKey))
	var key APIKey
	err := k.repos.QueryRow(ctx, `
		SELECT id, user_id, name, prefix, scopes, created_at, revoked_at, last_used_at
		FROM api_keys WHERE key_hash = $1`, hex.EncodeToString(sum[:])).
		Scan(&key.ID, &key.UserID, &key.Name, &key.Prefix, &key.Scopes,
			&key.CreatedAt, &key.RevokedAt, &key.LastUsedAt)
	if err != nil {
		return nil, nil
	}
	if key.RevokedAt != nil {
		return nil, nil
	}
	_, _ = k.repos.Exec(ctx,
		`UPDATE api_keys SET last_used_at = now() WHERE id = $1`, key.ID)
	return &key, nil
}
