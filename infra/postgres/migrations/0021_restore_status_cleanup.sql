-- 0021: remember the pre-suspension business status so admin restore can
-- return a business to what it was (a suspended draft must not come back
-- as verified).

ALTER TABLE businesses ADD COLUMN IF NOT EXISTS pre_suspend_status text;

-- Drop dead schema identified in the hardening audit:
-- chat_attachments was superseded by messages.media_id;
-- notifications.channel is write-never (routing lives in the prefs matrix).
DROP TABLE IF EXISTS chat_attachments;
ALTER TABLE notifications DROP COLUMN IF EXISTS channel;
