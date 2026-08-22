-- Batch 2/4 backend: amenities, special hours, saved searches, password/email change support.
ALTER TABLE businesses ADD COLUMN amenities text[] NOT NULL DEFAULT '{}';
ALTER TABLE businesses ADD COLUMN special_hours jsonb NOT NULL DEFAULT '{}'::jsonb; -- { "YYYY-MM-DD": {open, close, closed} }

CREATE TABLE saved_searches (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    query      jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_saved_searches_user ON saved_searches (user_id, created_at DESC);
