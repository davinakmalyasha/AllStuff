-- Per-user notification preferences (PRD §5.7): channel matrix + quiet hours.
ALTER TABLE users ADD COLUMN notification_prefs jsonb NOT NULL DEFAULT '{}'::jsonb;
