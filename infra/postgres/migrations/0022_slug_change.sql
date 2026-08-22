-- 0022: slug immutability support (PRD §8.2) — records the one permitted
-- slug change; NULL means the owner has never renamed the URL.

ALTER TABLE businesses ADD COLUMN IF NOT EXISTS slug_changed_at timestamptz;

-- 0023 prep: thread pinning (max 5 per participant, PRD §5.5.1) and
-- notification retention tiers (90d users / 180d owners / 1y admins, §5.7)
-- reuse existing tables; no schema needed there.
