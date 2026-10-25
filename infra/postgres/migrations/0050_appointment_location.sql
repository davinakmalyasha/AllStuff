-- 0050_appointment_location.sql — an appointment records WHERE it happens.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:risk        DDL + data
-- migrate:note        No CONCURRENTLY: one nullable column on appointments and an index on it, plus a backfill that touches each row once. Separate from 0043 because migrations run in filename order and 0043 sorts BEFORE 0044, which creates appointments - a migration may not depend on a table a later number creates.

ALTER TABLE appointments ADD COLUMN IF NOT EXISTS location_id uuid REFERENCES business_locations(id) ON DELETE SET NULL;

-- Every appointment that predates locations belongs to the business's primary
-- location, which 0043 created from the address the business already had.
UPDATE appointments a
   SET location_id = b.primary_location_id
  FROM businesses b
 WHERE b.id = a.business_id AND a.location_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_appointments_location ON appointments (location_id, starts_at);
