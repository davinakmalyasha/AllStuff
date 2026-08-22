-- pinned_message_ids reference chat_messages.id (bigint), not uuid.
-- Two-step cast + default handling (the default is '{}').
ALTER TABLE chat_participants ALTER COLUMN pinned_message_ids DROP DEFAULT;

ALTER TABLE chat_participants ALTER COLUMN pinned_message_ids TYPE text[]
  USING pinned_message_ids::text[];

ALTER TABLE chat_participants ALTER COLUMN pinned_message_ids TYPE bigint[]
  USING pinned_message_ids::bigint[];

ALTER TABLE chat_participants ALTER COLUMN pinned_message_ids SET DEFAULT '{}'::bigint[];
