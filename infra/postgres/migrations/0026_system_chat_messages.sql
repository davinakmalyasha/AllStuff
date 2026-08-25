-- System chat messages were never insertable: sender_id is NOT NULL uuid
-- (empty string → "invalid input syntax for type uuid") and 'system' is not
-- an allowed sender_role. Allow anonymous system senders so thread
-- reopen/close notices (PRD §5.5.2) can actually be persisted.

ALTER TABLE chat_messages ALTER COLUMN sender_id DROP NOT NULL;

ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_sender_role_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_sender_role_check
  CHECK (sender_role IN ('user', 'owner', 'admin', 'system'));

ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_system_sender_check
  CHECK (sender_role <> 'system' OR sender_id IS NULL);
