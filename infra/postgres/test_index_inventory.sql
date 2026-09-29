-- test_index_inventory.sql — asserts the indexes 0031_index_hot.sql promises.
--
-- WHY THIS IS A SEPARATE FILE
-- ---------------------------
-- test_index_usage.sql held two unrelated things: a set of deterministic
-- assertions in a DO block, and a series of bare `EXPLAIN` statements meant to
-- be read by a human. They were run together or not at all, and since the
-- EXPLAINs assert nothing, a run of that file could not fail on a missing or
-- INVALID index — it just printed plans nobody diffed. The assertions were
-- therefore effectively not enforced anywhere.
--
-- The split:
--   * here — deterministic, runs on an EMPTY database, raises on failure.
--     Wired into the CI migration-contract step.
--   * test_index_usage.sql — the EXPLAIN report, kept as a manual tool. It
--     needs data to mean anything; see test_index_usage_seeded.sql for the
--     version CI actually runs.
--
-- Run with:
--   psql -d bizverse -v ON_ERROR_STOP=1 -f infra/postgres/test_index_inventory.sql

\pset footer off
\echo === index inventory (deterministic; no data required)

DO $do$
DECLARE n int;
BEGIN
  -- The critical indexes from 0031 exist.
  SELECT count(*) INTO n FROM pg_indexes WHERE indexname IN (
    'idx_businesses_tsv','idx_businesses_tsv_all','idx_products_tsv',
    'idx_businesses_owner','idx_collection_items_target',
    'idx_moderation_actions_created','idx_events_flagged'
  );
  IF n <> 7 THEN
    RAISE EXCEPTION 'expected the 7 critical indexes from 0031, found %', n;
  END IF;

  -- The redundant set must be gone, or 0031 did not run its DROPs.
  SELECT count(*) INTO n FROM pg_indexes WHERE indexname IN (
    'idx_notifications_user_type','idx_engagement_events_target',
    'idx_reviews_business_rating','idx_businesses_slug',
    'idx_collections_user','idx_moderation_actions_target','idx_media_uploader'
  );
  IF n <> 0 THEN
    RAISE EXCEPTION '0031 should have dropped 7 redundant indexes, % remain', n;
  END IF;

  -- Nothing left INVALID. A cancelled CONCURRENTLY build is present but
  -- unusable, and IF NOT EXISTS would then skip rebuilding it — which is the
  -- single most damaging way for this file to fail, because the index appears
  -- to exist and the planner silently refuses it.
  SELECT count(*) INTO n FROM pg_index WHERE NOT indisvalid;
  IF n <> 0 THEN
    RAISE EXCEPTION '% index(es) are marked INVALID — an interrupted CONCURRENTLY build; see the operator notes', n;
  END IF;

  RAISE NOTICE 'index inventory OK';
END $do$;

-- ---------------------------------------------------------------------------
-- 0032: the slug index is a real partial index, not an always-true predicate.
--
-- This one assertion would have caught 0029's inert index. A unique index whose
-- predicate is always true is indistinguishable from a plain unique constraint
-- by name alone, so nothing in the schema said so; only counting the rows it
-- actually constrains does.
--
-- The check works by inserting two rows with the same slug where the FIRST is
-- retired, then a second that is live, and confirming both are accepted. If the
-- predicate were always true the second insert would raise unique_violation.
-- Everything is inside the caller's transaction and discarded by the ROLLBACK
-- at the end of test_integrity.sql's own pattern; this file is run standalone,
-- so it manages its own transaction below.
-- ---------------------------------------------------------------------------
\echo === 0032: the slug index predicate is genuinely selective

BEGIN;

DO $do$
DECLARE
  b_retired uuid;
  b_live    uuid;
  u         uuid;
BEGIN
  SELECT id INTO u FROM users LIMIT 1;
  IF u IS NULL THEN
    RAISE EXCEPTION 'no users to own the fixture; run scripts/seed.sql first, or point this at a seeded database';
  END IF;

  INSERT INTO businesses (id, owner_id, name, slug, description, currency,
                          address, city, country, lat, lng, status, deleted_at)
  VALUES ('00320000-0000-4000-8000-00000000000a', u, 'Retired', 'invariant-slug',
          'a description long enough to clear any minimum length a listing must satisfy',
          'USD', '1 St', 'Jakarta', 'Indonesia', -6.2, 106.8, 'closed', now())
  RETURNING id INTO b_retired;

  BEGIN
    INSERT INTO businesses (id, owner_id, name, slug, description, currency,
                            address, city, country, lat, lng, status)
    VALUES ('00320000-0000-4000-8000-00000000000b', u, 'Live', 'invariant-slug',
            'a description long enough to clear any minimum length a listing must satisfy',
            'USD', '2 St', 'Jakarta', 'Indonesia', -6.3, 106.9, 'verified')
    RETURNING id INTO b_live;
  EXCEPTION WHEN unique_violation THEN
    RAISE EXCEPTION 'businesses_slug_live still constrains retired rows; its predicate is not selective';
  END;

  IF b_retired IS NULL OR b_live IS NULL THEN
    RAISE EXCEPTION 'fixture insert did not return both ids';
  END IF;

  RAISE NOTICE 'slug index predicate is selective';
END $do$;

ROLLBACK;

\echo === index inventory OK
