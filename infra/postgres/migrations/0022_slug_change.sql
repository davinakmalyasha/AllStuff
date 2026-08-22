-- 0022: slug immutability support (PRD §8.2) — records the one permitted
-- slug change; NULL means the owner has never renamed the URL.

ALTER TABLE businesses ADD COLUMN IF NOT EXISTS slug_changed_at timestamptz;
