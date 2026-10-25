-- 0046_location_service_hours.sql — hours that apply to ONE service at ONE place.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:risk        DDL
-- migrate:note        No CONCURRENTLY: one new table with a composite primary key, catalogue-only.

-- A clinic open 09:00-17:00 may run a specialist clinic 13:00-17:00; a kitchen
-- may take orders for one menu until it closes the grill early. The location's
-- own hours remain the outer bound, and a row here NARROWS the window for that
-- service.
--
-- A MISSING weekday row means "this service follows the location's hours that
-- day", which is the common case and why this is an override table rather than a
-- full week per service. is_working=false is a day the service is unavailable
-- even though the place is open.
CREATE TABLE IF NOT EXISTS location_service_hours (
    location_id uuid NOT NULL REFERENCES business_locations(id) ON DELETE CASCADE,
    product_id  uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    weekday     text NOT NULL CHECK (weekday IN ('mon','tue','wed','thu','fri','sat','sun')),
    is_working  boolean NOT NULL DEFAULT true,
    opens_at    time,
    closes_at   time,
    PRIMARY KEY (location_id, product_id, weekday),
    CHECK (NOT is_working OR (opens_at IS NOT NULL AND closes_at IS NOT NULL AND closes_at > opens_at))
);

CREATE INDEX IF NOT EXISTS idx_location_service_hours_product ON location_service_hours (product_id);
