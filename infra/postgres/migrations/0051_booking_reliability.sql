-- 0051_booking_reliability.sql — reminders and review nudges, both idempotent
-- per appointment, plus the notification vocabulary they need.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:risk        DDL
-- migrate:note        No CONCURRENTLY: one new small table and a notification CHECK rebuild. The CHECK is NOT VALID on purpose (0029 documents why); new rows are validated, existing rows are not re-scanned.

-- 0051: Booking Reliability Pack (PRD §5.6.4)
--
-- The recurring failure in small-service businesses is not a lack of bookings,
-- it is empty chairs: a customer forgets, a slot frees up and nobody takes it,
-- and a happy customer never leaves a review. All three are timing problems,
-- and the API already owns the calendar - so they need no external scheduler.
--
-- Every action is claimed exactly once per appointment by its row in this
-- table. The job inserts the claim FIRST and only notifies after the insert
-- won; a crash between the two loses one reminder instead of sending it twice,
-- which is the right trade for a nudge (a duplicate reminder reads as a bug to
-- the customer, a missed one reads as a busy shop).
CREATE TABLE IF NOT EXISTS appointment_events (
    id             uuid PRIMARY KEY,
    appointment_id uuid NOT NULL REFERENCES appointments(id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('reminder_24h', 'reminder_2h', 'review_nudge')),
    sent_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (appointment_id, kind)
);

-- The job scans by time window, not by appointment; the unique key covers the
-- lookup-claim path but not "which appointments start in the next hour".
CREATE INDEX IF NOT EXISTS idx_appointment_events_kind ON appointment_events (kind, sent_at);

-- Notification vocabulary. As with 0044, the FULL list must be repeated in
-- 0029 (which recreates this constraint on replay) or a replay silently drops
-- the new values and TestMigrateReplayIsSafe fails.
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_type_check;
ALTER TABLE notifications
    ADD CONSTRAINT notifications_type_check CHECK (
        type IN (
            'message_received','reaction_added','helpful_vote','product_review',
            'review_posted','review_replied','comment_on_business','comment_mention',
            'question_asked','question_answered','business_update',
            'verification_result','doc_re_request','business_suspended',
            'business_restored','moderation_warning','appeal_result',
            'category_new_business','back_in_stock','claim_result','trend_anomaly',
            'system_announcement','appointment_booked','appointment_cancelled',
            'appointment_rescheduled','appointment_reminder','review_nudge'
        )
    ) NOT VALID;
