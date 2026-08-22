-- Batch 3: daily search alerts (PRD §5.1.2) — saved searches can email new
-- matches; last_result_ids lets the job diff "new since last run".
ALTER TABLE saved_searches
  ADD COLUMN notify_daily  boolean     NOT NULL DEFAULT false,
  ADD COLUMN last_sent_at  timestamptz,
  ADD COLUMN last_result_ids jsonb     NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN updated_at    timestamptz NOT NULL DEFAULT now();
