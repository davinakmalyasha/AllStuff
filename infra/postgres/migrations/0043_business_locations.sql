-- 0043_business_locations.sql — a business may operate from more than one place;
-- each place has its own address and its own hours.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:risk        DDL + data
-- migrate:note        No CONCURRENTLY: new tables, two catalogue-only columns on businesses, one nullable column on appointments, and a backfill that touches every business ONCE - at this size that is a single short transaction. The backfill is written as INSERT ... WHERE NOT EXISTS / UPDATE so replaying the file changes nothing.

-- The address, coordinates and hours already live on `businesses` and stay there
-- as a cached copy for the primary location, so every existing reader (search,
-- the directory, the storefront, the SSR pages) keeps working untouched. This
-- table is additive: the second location onwards has a home.
CREATE TABLE IF NOT EXISTS business_locations (
    id            uuid PRIMARY KEY,
    business_id   uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    address       text NOT NULL,
    city          text NOT NULL,
    country       text NOT NULL,
    lat           double precision NOT NULL,
    lng           double precision NOT NULL,
    timezone      text NOT NULL DEFAULT 'UTC',
    -- The same shape as businesses.hours / special_hours, so the slot
    -- computation reads one format whichever record it got them from.
    hours         jsonb NOT NULL DEFAULT '{}'::jsonb,
    special_hours jsonb NOT NULL DEFAULT '{}'::jsonb,
    phone         text CHECK (phone IS NULL OR char_length(phone) <= 40),
    is_primary    boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Exactly one primary location per business: it is the one the cached columns
-- mirror, and the one a booking uses when no location is named.
CREATE UNIQUE INDEX IF NOT EXISTS idx_business_locations_primary
    ON business_locations (business_id) WHERE is_primary;
CREATE INDEX IF NOT EXISTS idx_business_locations_business ON business_locations (business_id);

ALTER TABLE businesses ADD COLUMN IF NOT EXISTS primary_location_id uuid REFERENCES business_locations(id) ON DELETE SET NULL;
ALTER TABLE businesses ADD COLUMN IF NOT EXISTS location_count int NOT NULL DEFAULT 0;

-- Every existing business becomes a single-location business.
INSERT INTO business_locations (id, business_id, name, address, city, country, lat, lng, timezone, hours, special_hours, is_primary)
SELECT gen_random_uuid(), b.id, b.name, b.address, b.city, b.country, b.lat, b.lng,
       coalesce(nullif(b.timezone, ''), 'UTC'), b.hours, b.special_hours, true
  FROM businesses b
 WHERE NOT EXISTS (SELECT 1 FROM business_locations l WHERE l.business_id = b.id)
ON CONFLICT DO NOTHING;

UPDATE businesses b
   SET primary_location_id = l.id,
       location_count = (SELECT count(*) FROM business_locations l2 WHERE l2.business_id = b.id)
  FROM business_locations l
 WHERE l.business_id = b.id AND l.is_primary;
