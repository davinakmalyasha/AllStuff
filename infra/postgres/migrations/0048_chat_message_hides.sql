-- 0048_chat_message_hides.sql — per-participant message hiding.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        No CONCURRENTLY: this is a CREATE TABLE plus an index on a new table, so there is no existing index to leave INVALID behind. FKs are validated inline on empty tables.

-- WHY THIS EXISTS
-- --------------
-- chat_messages.deleted_for is a single column on the SHARED row. It can hold
-- 'none', 'me' or 'everyone', and it cannot express the only scope that matters:
-- "hidden from me, still there for them".
--
-- 'me' therefore hid a message from every reader of the thread. An author who
-- deleted their own message made it vanish for the other party too, while
-- continuing to see it themselves - because every read path filters on
-- `deleted_for <> 'everyone'` and 'me' is not 'everyone'. The author kept a
-- message that no longer existed for anyone else, which is the opposite of what
-- "delete for me" means.
--
-- This table holds the per-participant scope. 'everyone' stays on the shared row,
-- since that one really is global and every reader must honour it.
--
-- thread_id is denormalised alongside message_id deliberately. Every read of a
-- message goes through a thread-scoped query, so filtering hides by thread avoids
-- a join to chat_messages on the hot read path, and the FK to chat_messages is
-- kept as well so a hide cannot outlive its message.
--
-- No change to chat_messages.deleted_for's own CHECK: 0001 already constrains it
-- to exactly these three values, and this file initially tried to add the same
-- constraint under a clearer name. That failed on every replay with
-- "constraint ... already exists", because a replay re-runs the whole chain against
-- a database that already has it.
-- IF NOT EXISTS, because the replay gate deletes this migration's version marker
-- and re-runs it against a database that already has the table. Without it the
-- replay raises 42P07 and, per migrate_replay_integration_test.go, a migration
-- that cannot re-run "would wedge startup permanently" after a crash between
-- COMMIT and the marker write.
--
-- IF NOT EXISTS compares names only. That is acceptable HERE and not on the
-- indexes above: this is a new table with no prior definition to reconcile, and
-- the correct end state is asserted below.
CREATE TABLE IF NOT EXISTS chat_message_hides (
  message_id  bigint NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
  thread_id   uuid   NOT NULL REFERENCES chat_threads(id)  ON DELETE CASCADE,
  user_id     uuid   NOT NULL REFERENCES users(id)        ON DELETE CASCADE,
  hidden_at   timestamptz NOT NULL DEFAULT now(),
  -- A user hides a given message once. Without this, re-deleting raises a unique
  -- violation on a request that should simply succeed.
  PRIMARY KEY (message_id, user_id)
);

-- The read path is "messages in this thread the caller has not hidden", so the
-- leading column has to be thread_id. Leading on message_id would be the obvious
-- choice for the INSERT and useless for every read.
--
-- DROP then CREATE rather than CREATE IF NOT EXISTS, for the reason the lint
-- enforces on every other index: IF NOT EXISTS matches on NAME and never checks
-- indisvalid, so an interrupted build leaves an INVALID index that every later
-- replay silently skips. Drop-then-build is the recoverable shape.
DROP INDEX IF EXISTS idx_chat_message_hides_thread_user;
CREATE INDEX idx_chat_message_hides_thread_user
  ON chat_message_hides (thread_id, user_id);

-- ---------------------------------------------------------------------------
-- DATA REPAIR
-- ---------------------------------------------------------------------------
-- Rows already marked 'me' under the old scheme are exactly the messages an
-- author hid from themselves alone. Expand each one into a hide row for its
-- author, and clear the shared column so the message becomes visible to the other
-- party again - which is what the author was asking for.
--
-- This is deliberately a repair rather than a silent behaviour change: any
-- 'me' row in a live database is a message currently invisible to someone who
-- should still see it.
INSERT INTO chat_message_hides (message_id, thread_id, user_id)
SELECT m.id, m.thread_id, m.sender_id
  FROM chat_messages m
 WHERE m.deleted_for = 'me'
   AND m.sender_id IS NOT NULL
ON CONFLICT (message_id, user_id) DO NOTHING;

UPDATE chat_messages SET deleted_for = 'none' WHERE deleted_for = 'me';

-- Assert the repair, because a mis-scoped version of this silently either hides
-- nothing or un-hides too much, and neither shows up until a user reports a
-- missing message.
DO $assertions$
DECLARE
    v_stray int;
    v_hides int;
BEGIN
    SELECT count(*) INTO v_stray FROM chat_messages WHERE deleted_for = 'me';
    IF v_stray > 0 THEN
        RAISE EXCEPTION
            '0048 FAIL: % rows still carry deleted_for=''me'', which the per-participant path now owns',
            v_stray;
    END IF;

    SELECT count(*) INTO v_hides FROM chat_message_hides;
    RAISE NOTICE '0048: % per-participant hide(s) migrated from the shared column', v_hides;
END $assertions$;