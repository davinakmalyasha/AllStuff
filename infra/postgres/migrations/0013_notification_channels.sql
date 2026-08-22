-- Batch 1 (PRD §5.7): notification type index + email-channel routing support.
-- The notifications table already carries channel + expires_at; this adds the
-- indexes the new filter and purge queries need.

CREATE INDEX idx_notifications_type ON notifications (user_id, type, created_at DESC);
