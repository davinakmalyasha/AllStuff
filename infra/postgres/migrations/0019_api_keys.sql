-- Batch 6: public API keys (read-only, rate-limited). The raw key is shown
-- once at creation; only its SHA-256 is stored.
CREATE TABLE IF NOT EXISTS api_keys (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        text NOT NULL,
    key_hash    text NOT NULL UNIQUE,
    prefix      text NOT NULL,
    scopes      jsonb NOT NULL DEFAULT '["read"]'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz,
    last_used_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys (user_id, created_at DESC);
