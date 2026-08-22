-- 0024: pinned conversations (PRD §5.5.1) — per participant, max 5,
-- order preserved by array position.

ALTER TABLE chat_participants ADD COLUMN IF NOT EXISTS pinned_thread_ids uuid[] NOT NULL DEFAULT '{}';
