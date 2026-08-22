-- Seed: demo content required by the E2E smoke suite (PRD §12.7).
-- Idempotent: safe to run after every migrate. Run with:
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/seed.sql

-- Demo owner (verified so digest/login flows work).
INSERT INTO users (id, email, name, username, password_hash, email_verified_at, role, status)
VALUES (
  '11111111-1111-4111-8111-111111111111',
  'demo@bizverse.test',
  'Demo Owner',
  'demoowner',
  '$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRzYWx0c2FsdA', -- 'seed-only-no-login'
  now(), 'user', 'active'
) ON CONFLICT (id) DO NOTHING;

-- Café expected by smoke.spec.ts (slug + hardcoded compare UUID).
INSERT INTO businesses (id, owner_id, name, slug, tagline, description, category_id, status,
	verification_level, verified_at, currency, address, lat, lng, city, country, hours, contact,
	tags, price_level, founded_year, last_published_at)
VALUES (
  '5845a5db-02b0-4ce8-a6d1-95f2b4b16396',
  '11111111-1111-4111-8111-111111111111',
  'Rumah Kopi Senja',
  'rumah-kopi-senja',
  'Slow coffee in a fast city.',
  'A cozy neighborhood coffee shop serving single-origin brews, hand-pulled espresso drinks, and homemade pastries from dusk until late. Free Wi-Fi, quiet corners, and the best kopi susu in Jakarta.',
  'a1000000-0000-4000-8000-000000000011', -- Café
  'verified', 'fully_verified', now(),
  'USD', 'Jl. Senopati No. 12, Kebayoran Baru', -6.2446, 106.8006,
  'Jakarta', 'Indonesia',
  '{"mon":{"open":"08:00","close":"22:00"},"tue":{"open":"08:00","close":"22:00"},"wed":{"open":"08:00","close":"22:00"},"thu":{"open":"08:00","close":"22:00"},"fri":{"open":"08:00","close":"23:00"},"sat":{"open":"09:00","close":"23:00"},"sun":{"open":"09:00","close":"21:00"}}'::jsonb,
  '{"phone":"+62-21-5550101","email":"hello@rumahkopisenja.id","instagram":"rumahkopisenja"}'::jsonb,
  ARRAY['coffee','espresso','wifi','pastries'], 2, 2017, now()
) ON CONFLICT (id) DO NOTHING;

-- Barbershop paired with the café on the compare page.
INSERT INTO businesses (id, owner_id, name, slug, tagline, description, category_id, status,
	verification_level, verified_at, currency, address, lat, lng, city, country, hours, contact,
	tags, price_level, founded_year, last_published_at)
VALUES (
  'dcb251c1-01c7-4969-967f-54f47967d3e9',
  '11111111-1111-4111-8111-111111111111',
  'Razor & Thread Barbershop',
  'razor-thread-barbershop',
  'Classic cuts, modern care.',
  'Traditional barbershop offering precision haircuts, hot-towel shaves, and beard sculpting. Walk-ins welcome seven days a week with a waiting lounge, cold drinks, and vintage chairs.',
  'a1000000-0000-4000-8000-000000000022', -- Barber
  'verified', 'verified', now(),
  'USD', 'Jl. Kemang Raya No. 88', -6.2607, 106.8128,
  'Jakarta', 'Indonesia',
  '{"mon":{"open":"10:00","close":"20:00"},"tue":{"open":"10:00","close":"20:00"},"wed":{"closed":true},"thu":{"open":"10:00","close":"20:00"},"fri":{"open":"10:00","close":"21:00"},"sat":{"open":"09:00","close":"21:00"},"sun":{"open":"10:00","close":"18:00"}}'::jsonb,
  '{"phone":"+62-21-5550202","email":"book@razorthread.id","instagram":"razorthread"}'::jsonb,
  ARRAY['haircut','shave','beard','walkins'], 2, 2015, now()
) ON CONFLICT (id) DO NOTHING;
