-- 0045_staff_scheduling.sql — staff, their service qualifications and their
-- weekly hours; appointment conflicts become per-staff once a booking names one.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:risk        DDL
-- migrate:note        No CONCURRENTLY: everything here is either a new table or a small catalogue change on appointments (an added nullable column and two rebuilt constraints). The EXCLUDE constraints are validated against the existing rows, which are all unstaffed, so both predicates are trivially satisfied.

-- A staff member is a row on the business, not necessarily a platform account.
-- Many small shops will never give their staff a login, and requiring one would
-- block the feature for exactly the businesses that need a calendar most.
CREATE TABLE IF NOT EXISTS business_staff (
    id          uuid PRIMARY KEY,
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    name        text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    email       citext,
    phone       text CHECK (phone IS NULL OR char_length(phone) <= 40),
    active      boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- One staff row per account per business: linking the same person twice would
-- split their calendar and let them be double-booked.
CREATE UNIQUE INDEX IF NOT EXISTS idx_business_staff_user
    ON business_staff (business_id, user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_business_staff_business ON business_staff (business_id, active);

-- Which services a staff member performs. A staff member with NO rows here is
-- not qualified for anything; that is deliberate, because "qualified for
-- everything" as a default silently grants skills nobody assigned.
CREATE TABLE IF NOT EXISTS staff_services (
    staff_id   uuid NOT NULL REFERENCES business_staff(id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    PRIMARY KEY (staff_id, product_id)
);
CREATE INDEX IF NOT EXISTS idx_staff_services_product ON staff_services (product_id);

-- Per-weekday hours. A row may mark a day off ('is_working' false), and a
-- MISSING row means "inherit the business hours for that day" so a shop that
-- opens 09:00-17:00 does not have to repeat that per staff member.
CREATE TABLE IF NOT EXISTS staff_hours (
    staff_id   uuid NOT NULL REFERENCES business_staff(id) ON DELETE CASCADE,
    weekday    text NOT NULL CHECK (weekday IN ('mon','tue','wed','thu','fri','sat','sun')),
    is_working boolean NOT NULL DEFAULT true,
    opens_at   time,
    closes_at  time,
    PRIMARY KEY (staff_id, weekday),
    CHECK (NOT is_working OR (opens_at IS NOT NULL AND closes_at IS NOT NULL AND closes_at > opens_at))
);

ALTER TABLE appointments ADD COLUMN IF NOT EXISTS staff_id uuid REFERENCES business_staff(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_appointments_staff ON appointments (staff_id, starts_at);

-- Conflict rules, now two:
--
--   * UNSTAFFED bookings still conflict per BUSINESS, which is what 0044
--     enforced and what a shop without staff expects.
--   * STAFFED bookings conflict per STAFF MEMBER only, so two staff can see two
--     customers at the same time - the entire point of having staff.
--
-- Expressing this as one constraint per partition (staff_id IS NULL / IS NOT
-- NULL) rather than dropping the business-wide rule keeps unstaffed bookings
-- safe while allowing genuine concurrency.
ALTER TABLE appointments DROP CONSTRAINT IF EXISTS appointments_no_overlap;
ALTER TABLE appointments DROP CONSTRAINT IF EXISTS appointments_no_overlap_unstaffed;
ALTER TABLE appointments ADD CONSTRAINT appointments_no_overlap_unstaffed
    EXCLUDE USING gist (
        business_id WITH =,
        tstzrange(starts_at, ends_at, '[)') WITH &&
    ) WHERE (status IN ('pending', 'confirmed') AND staff_id IS NULL);
ALTER TABLE appointments DROP CONSTRAINT IF EXISTS appointments_no_overlap_staff;
ALTER TABLE appointments ADD CONSTRAINT appointments_no_overlap_staff
    EXCLUDE USING gist (
        staff_id WITH =,
        tstzrange(starts_at, ends_at, '[)') WITH &&
    ) WHERE (status IN ('pending', 'confirmed') AND staff_id IS NOT NULL);
