-- 0022: slug immutability support (PRD §8.2) — records the one permitted
-- slug change; NULL means the owner has never renamed the URL.

ALTER TABLE businesses ADD COLUMN IF NOT EXISTS pre_suspend_status text;
ALTER TABLE businesses ADD COLUMN IF NOT EXISTS slug_changed_at timestamptz;

-- Drop dead schema identified in the hardening audit:
-- chat_attachments was superseded by messages.media_id;
-- notifications.channel is write-never (routing lives in the prefs matrix).
DROP TABLE IF EXISTS chat_attachments;
ALTER TABLE notifications DROP COLUMN IF EXISTS channel;
