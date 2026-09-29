-- Seed: initial category tree (admin-editable; PRD §5.8.3)
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        categories
-- migrate:risk        DML
-- migrate:note        Reference tree owned by the operator via /admin/categories. DO NOTHING with no target so a replay can never revert an admin rename.
--
-- WHY `ON CONFLICT DO NOTHING` AND NOT `ON CONFLICT (id) DO NOTHING`
-- ---------------------------------------------------------------------------
-- This file used to be a bare INSERT. The runner writes the version marker
-- AFTER the transaction commits (db/migrate.go), so a crash in that window
-- replays the file and 0002 fails forever with 23505 on categories_slug_key —
-- a fresh clone wedges on startup and never recovers on its own. The repair
-- procedure for a database already in that state is in docs/RUNBOOK.md under
-- "Migration wedged: rows present, marker absent".
--
-- The conflict target must be left off entirely. `categories` carries three
-- unique constraints (categories_pkey on id, categories_slug_key on slug, and
-- a (parent_id, name) key). Targeting only one leaves the other two able to
-- raise. Worse, targeting (id) specifically is WRONG here: if an admin renamed a
-- category's slug, the replay no longer conflicts on id, so it would insert a
-- SECOND row carrying the original slug — the exact duplicate the fix exists to
-- prevent.
--
-- DO NOTHING, never DO UPDATE: these rows are admin-editable reference data
-- (AdminCategoriesPage). A replay must never revert an operator's rename. This
-- is the distinction from 0028's `plans` seed, which legitimately uses a
-- targeted DO UPDATE because plan rows are migration-owned catalogue.
-- See REGISTRY.md rule 7.
-- ---------------------------------------------------------------------------
INSERT INTO categories (id, parent_id, name, slug, icon, description, sort_order) VALUES
  ('a1000000-0000-4000-8000-000000000001', NULL, 'Food & Dining', 'food-dining', 'utensils', 'Restaurants, street food, cafés, and everything edible.', 10),
  ('a1000000-0000-4000-8000-000000000002', NULL, 'Beauty & Care', 'beauty-care', 'sparkles', 'Salons, barbers, spas, and personal care.', 20),
  ('a1000000-0000-4000-8000-000000000003', NULL, 'Events & Entertainment', 'events-entertainment', 'camera', 'Photobooths, studios, entertainment venues.', 30),
  ('a1000000-0000-4000-8000-000000000004', NULL, 'Shopping', 'shopping', 'shopping-bag', 'Fashion, crafts, and retail.', 40),
  ('a1000000-0000-4000-8000-000000000005', NULL, 'Services', 'services', 'wrench', 'Professional and household services.', 50),
  ('a1000000-0000-4000-8000-000000000006', NULL, 'Health & Wellness', 'health-wellness', 'heart-pulse', 'Fitness, clinics, and wellness.', 60),

  ('a1000000-0000-4000-8000-000000000011', 'a1000000-0000-4000-8000-000000000001', 'Café', 'cafe', 'coffee', 'Coffee, tea, and pastry.', 1),
  ('a1000000-0000-4000-8000-000000000012', 'a1000000-0000-4000-8000-000000000001', 'Restaurant', 'restaurant', 'chef-hat', 'Sit-down dining.', 2),
  ('a1000000-0000-4000-8000-000000000013', 'a1000000-0000-4000-8000-000000000001', 'Street Food', 'street-food', 'flame', 'Street vendors and food stalls.', 3),
  ('a1000000-0000-4000-8000-000000000014', 'a1000000-0000-4000-8000-000000000001', 'Bakery', 'bakery', 'croissant', 'Bakeries and patisseries.', 4),
  ('a1000000-0000-4000-8000-000000000015', 'a1000000-0000-4000-8000-000000000001', 'Bar & Nightlife', 'bar-nightlife', 'martini', 'Bars, pubs, and nightlife.', 5),

  ('a1000000-0000-4000-8000-000000000021', 'a1000000-0000-4000-8000-000000000002', 'Salon', 'salon', 'scissors', 'Hair and beauty salons.', 1),
  ('a1000000-0000-4000-8000-000000000022', 'a1000000-0000-4000-8000-000000000002', 'Barber', 'barber', 'razor', 'Barbershops.', 2),
  ('a1000000-0000-4000-8000-000000000023', 'a1000000-0000-4000-8000-000000000002', 'Nail Studio', 'nail-studio', 'hand', 'Nail care.', 3),
  ('a1000000-0000-4000-8000-000000000024', 'a1000000-0000-4000-8000-000000000002', 'Spa', 'spa', 'flower', 'Spas and massages.', 4),

  ('a1000000-0000-4000-8000-000000000031', 'a1000000-0000-4000-8000-000000000003', 'Photobooth', 'photobooth', 'camera', 'Photobooth rentals and photo services.', 1),
  ('a1000000-0000-4000-8000-000000000032', 'a1000000-0000-4000-8000-000000000003', 'Photography Studio', 'photography-studio', 'aperture', 'Photography studios.', 2),
  ('a1000000-0000-4000-8000-000000000033', 'a1000000-0000-4000-8000-000000000003', 'Live Music', 'live-music', 'music', 'Live music venues.', 3),
  ('a1000000-0000-4000-8000-000000000034', 'a1000000-0000-4000-8000-000000000003', 'Event Venue', 'event-venue', 'calendar', 'Venues for events and parties.', 4),

  ('a1000000-0000-4000-8000-000000000041', 'a1000000-0000-4000-8000-000000000004', 'Fashion', 'fashion', 'shirt', 'Clothing and fashion.', 1),
  ('a1000000-0000-4000-8000-000000000042', 'a1000000-0000-4000-8000-000000000004', 'Crafts & Hobby', 'crafts-hobby', 'palette', 'Handmade and hobby shops.', 2),
  ('a1000000-0000-4000-8000-000000000043', 'a1000000-0000-4000-8000-000000000004', 'Electronics', 'electronics', 'cpu', 'Electronics and gadgets.', 3),

  ('a1000000-0000-4000-8000-000000000051', 'a1000000-0000-4000-8000-000000000005', 'Auto & Repair', 'auto-repair', 'car', 'Vehicle repair and care.', 1),
  ('a1000000-0000-4000-8000-000000000052', 'a1000000-0000-4000-8000-000000000005', 'Home Services', 'home-services', 'home', 'Cleaning, plumbing, and home care.', 2),
  ('a1000000-0000-4000-8000-000000000053', 'a1000000-0000-4000-8000-000000000005', 'Printing & Design', 'printing-design', 'printer', 'Printing and design services.', 3),

  ('a1000000-0000-4000-8000-000000000061', 'a1000000-0000-4000-8000-000000000006', 'Gym & Fitness', 'gym-fitness', 'dumbbell', 'Gyms and fitness studios.', 1),
  ('a1000000-0000-4000-8000-000000000062', 'a1000000-0000-4000-8000-000000000006', 'Clinic', 'clinic', 'stethoscope', 'Clinics and medical services.', 2),
  ('a1000000-0000-4000-8000-000000000063', 'a1000000-0000-4000-8000-000000000006', 'Wellness', 'wellness', 'leaf', 'Yoga, meditation, and wellness.', 3)
ON CONFLICT DO NOTHING;

-- Note: no schema_migrations row is inserted here; the runner handles that automatically.
