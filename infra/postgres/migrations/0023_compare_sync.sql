-- 0023: cross-device compare tray sync (PRD §5.1.5): each account keeps its
-- last compare selection; guests keep it in localStorage only.

ALTER TABLE users ADD COLUMN IF NOT EXISTS compare_ids uuid[] NOT NULL DEFAULT '{}';
