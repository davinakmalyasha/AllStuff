-- 0044_booking.sql — appointments and blackouts, with double-booking prevented
-- by the database rather than by a read-then-write check.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:risk        DDL
-- migrate:note        No CONCURRENTLY: the only index on an EXISTING table is on products (a column ADD, catalogue-only), and every other index and constraint is on a table this file creates, so there is no live table to block. The EXCLUDE constraints are built as part of CREATE/ALTER on a small table and are not a concern at this size.

-- 0044: bookings (PRD §5.6.4)
--
-- Capacity-based appointments. Deliberately NO booking_slots table and NO
-- booking_services table: slots are derived from (business hours x service
-- duration x existing appointments x blackouts), and a second catalogue would
-- mean two admin UIs and a migration story for every existing service. See
-- REGISTRY.md.
--
-- Double-booking is prevented by the DATABASE, not by a read-then-write check:
-- two concurrent requests both see the slot free and both insert. The EXCLUDE
-- constraint makes the second insert fail with SQLSTATE 23P01, which the service
-- maps to a friendly "slot taken".
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- How long one appointment of this service takes. NULL means "not bookable":
-- a product without a duration cannot produce slots.
ALTER TABLE products ADD COLUMN IF NOT EXISTS duration_minutes int;
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_duration_minutes_check;
ALTER TABLE products ADD CONSTRAINT products_duration_minutes_check
    CHECK (duration_minutes IS NULL OR duration_minutes BETWEEN 5 AND 480);

CREATE TABLE IF NOT EXISTS appointments (
    id           uuid PRIMARY KEY,
    business_id  uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    product_id   uuid NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    customer_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    starts_at    timestamptz NOT NULL,
    ends_at      timestamptz NOT NULL,
    status       text NOT NULL DEFAULT 'confirmed'
                 CHECK (status IN ('pending', 'confirmed', 'cancelled', 'completed', 'no_show')),
    notes        text CHECK (notes IS NULL OR char_length(notes) <= 500),
    cancelled_by uuid REFERENCES users(id) ON DELETE SET NULL,
    cancelled_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at)
);

-- Half-open [start, end): a 10:00-10:30 appointment and a 10:30-11:00 one do
-- not overlap, so back-to-back bookings are allowed.
-- Guarded, not plain DDL: 0045 adds a staff dimension and REPLACES this rule
-- with two partitioned ones. Replaying 0044 after 0045 must not put the
-- business-wide constraint back, or TestMigrateReplayIsSafe would see a schema
-- change - the same replay trap 0029 documents for its constraints.
ALTER TABLE appointments DROP CONSTRAINT IF EXISTS appointments_no_overlap;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = 'public' AND table_name = 'appointments' AND column_name = 'staff_id'
    ) THEN
        ALTER TABLE appointments ADD CONSTRAINT appointments_no_overlap
            EXCLUDE USING gist (
                business_id WITH =,
                tstzrange(starts_at, ends_at, '[)') WITH &&
            ) WHERE (status IN ('pending', 'confirmed'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_appointments_customer ON appointments (customer_id, starts_at DESC);
CREATE INDEX IF NOT EXISTS idx_appointments_business ON appointments (business_id, starts_at);

-- Time the business is unavailable regardless of hours: holidays, maintenance,
-- a staff shortage.
CREATE TABLE IF NOT EXISTS booking_blackouts (
    id          uuid PRIMARY KEY,
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    starts_at   timestamptz NOT NULL,
    ends_at     timestamptz NOT NULL,
    reason      text CHECK (reason IS NULL OR char_length(reason) <= 200),
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at)
);

ALTER TABLE booking_blackouts DROP CONSTRAINT IF EXISTS booking_blackouts_no_overlap;
ALTER TABLE booking_blackouts ADD CONSTRAINT booking_blackouts_no_overlap
    EXCLUDE USING gist (
        business_id WITH =,
        tstzrange(starts_at, ends_at, '[)') WITH &&
    );

CREATE INDEX IF NOT EXISTS idx_booking_blackouts_business ON booking_blackouts (business_id, starts_at);

-- The booking notification types. 0029 owns this CHECK and recreates it on
-- replay, so the same two values are added there as well - see the note in that
-- file. Without both, a replay would quietly drop them and every booking would
-- still notify successfully here while failing on a replayed database.
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
            -- 0051 (reliability). Repeated here for the same replay reason: 0044
            -- also recreates the constraint.
            'appointment_rescheduled','appointment_reminder','review_nudge'
        )
    ) NOT VALID;
