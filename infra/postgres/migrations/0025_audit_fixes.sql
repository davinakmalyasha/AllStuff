-- Migration 0025: audit fixes — reports flexibility, perf indexes, job idempotency.

-- 1) Support-contact and client-error reports land in `reports` like other
--    inbound messages. The old CHECK rejected 'error'/'support' and
--    reporter_id was NOT NULL, so both endpoints always failed.
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
  CHECK (target_type IN ('review', 'comment', 'message', 'attachment', 'product',
                         'business', 'user', 'reaction', 'error', 'support'));
ALTER TABLE reports ALTER COLUMN reporter_id DROP NOT NULL;

-- 2) Perf indexes for real query shapes:
--    - trend snapshot lookups in markRising / analytics leaderboard / meta
CREATE INDEX IF NOT EXISTS idx_trend_snapshots_period_biz ON trend_snapshots (period, business_id);
--    - follower fan-out (weekly digest, announcements, restock alerts)
CREATE INDEX IF NOT EXISTS idx_follows_business ON follows (business_id);
--    - chat search uses deleted_for <> 'everyone' (includes soft-deleted-for-me
--      rows), which the original partial index (deleted_for = 'none') can't serve
DROP INDEX IF EXISTS idx_chat_messages_thread_search;
CREATE INDEX idx_chat_messages_search ON chat_messages
  USING gin (to_tsvector('simple', coalesce(body, ''))) WHERE deleted_for <> 'everyone';

-- 3) Job idempotency markers (weekly digest must not double-send on restart).
CREATE TABLE IF NOT EXISTS job_runs (
  job     text        NOT NULL,
  period  text        NOT NULL,
  ran_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (job, period)
);

-- 4) Single-use consumption markers for stateless email tokens (password
--    reset / verification). A captured link stops working after first use.
CREATE TABLE IF NOT EXISTS consumed_tokens (
  jti         text        PRIMARY KEY,
  consumed_at timestamptz NOT NULL DEFAULT now()
);

