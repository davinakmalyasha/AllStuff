-- 0052_waitlist.sql — a queue for days that are already full.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:risk        DDL
-- migrate:note        No CONCURRENTLY: one new small table plus a partial index on it, and a notification CHECK rebuild (NOT VALID, as 0029 documents).

-- 0052: booking waitlist (PRD §5.6.4)
--
-- A full day is lost demand, and a cancellation is the only moment that demand
-- can be recovered. The waitlist is deliberately NOT a slot table: it records
-- "this customer wants this service on this day", and a cancellation offers the
-- freed time to the earliest waiter. Booking stays first-come-first-served;
-- the queue only decides who hears about it first.
--
-- notified_at is not a claim on the slot. Holding a slot for a waiter would
-- require a timed reservation row and a release job; notifying one person and
-- letting the normal booking race decide is simpler and cannot deadlock.
CREATE TABLE IF NOT EXISTS booking_waitlist (
    id           uuid PRIMARY KEY,
    business_id  uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    product_id   uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    customer_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- A calendar date in the BUSINESS's timezone. A waitlist entry is a day,
    -- not an instant: "Saturday" is the same request whether or not a slot
    -- exists yet.
    day          date NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    notified_at  timestamptz,
    UNIQUE (business_id, product_id, customer_id, day)
);

-- Match on (business, service, day) and skip people already told. Partial so
-- the index stays small when most entries have been notified.
CREATE INDEX IF NOT EXISTS idx_waitlist_open
    ON booking_waitlist (business_id, product_id, day, created_at)
    WHERE notified_at IS NULL;

-- Notification vocabulary. One more value, so every file that recreates the
-- constraint carries it (0029, 0044, 0051), or a replay drops it.
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
            'appointment_rescheduled','appointment_reminder','review_nudge',
            'slot_open'
        )
    ) NOT VALID;
