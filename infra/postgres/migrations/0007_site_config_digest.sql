-- Site configuration (announcement banner, feature flags) + user digests (PRD §5.7).
CREATE TABLE IF NOT EXISTS site_config (
    id         uuid PRIMARY KEY,
    key        text NOT NULL UNIQUE,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE users ADD COLUMN digest_opt_in boolean NOT NULL DEFAULT false;
