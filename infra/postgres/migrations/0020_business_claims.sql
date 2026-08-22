-- Batch 7: claim-a-business — public form → admin queue → draft business.
CREATE TABLE IF NOT EXISTS business_claims (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    business_id uuid REFERENCES businesses(id) ON DELETE SET NULL, -- claimed existing listing
    name        text NOT NULL,
    category_id uuid REFERENCES categories(id),
    address     text NOT NULL,
    city        text NOT NULL,
    country     text NOT NULL DEFAULT '',
    website     text NOT NULL DEFAULT '',
    evidence    text NOT NULL DEFAULT '', -- e.g. "I manage the shop since 2019"
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'approved', 'rejected')),
    note        text NOT NULL DEFAULT '',
    decided_by  uuid REFERENCES users(id),
    decided_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_business_claims_status ON business_claims (status, created_at DESC);
