-- client_msg_id is an app-generated dedupe key (any string, PRD §5.5.3),
-- not a UUID.
ALTER TABLE chat_messages ALTER COLUMN client_msg_id TYPE text;
