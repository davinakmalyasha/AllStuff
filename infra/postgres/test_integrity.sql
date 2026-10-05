-- Functional test for the schema changes in migration 0029_integrity.sql and the
-- moderation hide/restore statements in internal/service/admin.go.
--
-- Those are hand-written UPDATEs against an ENUM column and against columns
-- added in 0029, which is exactly the SQL that compiles in Go and fails at
-- runtime. Everything here runs inside a transaction that is rolled back, and
-- assertions RAISE so a regression is loud rather than a printed boolean nobody
-- reads.
--
-- Run with:
--   psql -d bizverse -v ON_ERROR_STOP=1 -f infra/postgres/test_integrity.sql

\pset footer off
\echo === 0029_integrity.sql + admin.go moderation statements

-- Explicit transaction so the ROLLBACK at the end actually discards the
-- fixtures. psql runs each statement in its own transaction by default, which
-- would commit the test rows.
BEGIN;

DO $do$
DECLARE
  u1  uuid := '11111111-1111-1111-1111-111111111111';
  u2  uuid := '55555555-5555-5555-5555-555555555555';
  cat uuid := '22222222-2222-2222-2222-222222222222';
  b1  uuid := '33333333-3333-3333-3333-333333333333';
  b2  uuid := '66666666-6666-6666-6666-666666666666';
  b3  uuid := '77777777-7777-7777-7777-777777777777';
  st  text;
  pre text;
  n   int;
BEGIN
  ---------------------------------------------------------------------
  -- fixtures
  ---------------------------------------------------------------------
  INSERT INTO users (id, email, username, name, password_hash)
  VALUES (u1, 'mod-test-1@example.com', 'mod_test_1', 'Mod One', 'x'),
         (u2, 'mod-test-2@example.com', 'mod_test_2', 'Mod Two', 'x');

  INSERT INTO categories (id, name, slug, icon)
  VALUES (cat, 'Mod Test Cat', 'mod-test-cat', 'store')
  ON CONFLICT (id) DO NOTHING;

  -- NOTE lat/lng are NOT NULL on businesses. Drafts created by the claims flow
  -- write 0,0 placeholders (service/claims.go), so the fixtures mirror that
  -- rather than omitting the columns.
  INSERT INTO businesses
    (id, owner_id, name, slug, description, category_id, address, city, country, lat, lng, status)
  VALUES (b1, u1, 'Mod Test Biz', 'mod-test-biz',
          'A description long enough to satisfy any length check.',
          cat, '1 Test St', 'Testville', 'Testland', 0, 0, 'verified');

  ---------------------------------------------------------------------
  -- 1. owner_id is nullable (migration 0029; unblocks claim-an-existing-listing)
  ---------------------------------------------------------------------
  BEGIN
    INSERT INTO businesses
      (id, owner_id, name, slug, description, category_id, address, city, country, lat, lng, status)
    VALUES ('88888888-8888-8888-8888-888888888888', NULL, 'Unclaimed Biz', 'unclaimed-biz',
            'A listing nobody has claimed yet.', cat, '2 Test St', 'Testville', 'Testland', 0, 0, 'verified');
  EXCEPTION WHEN not_null_violation THEN
    RAISE EXCEPTION 'owner_id is still NOT NULL: claim-an-existing-listing remains unreachable';
  END;

  ---------------------------------------------------------------------
  -- 2. description has a default (migration 0029; fixes the claim-approval 500)
  ---------------------------------------------------------------------
  BEGIN
    INSERT INTO businesses (id, owner_id, name, slug, category_id, address, city, country, lat, lng, status)
    VALUES ('99999999-9999-9999-9999-999999999999', u1, 'No Desc', 'no-desc-biz',
            cat, '3 Test St', 'Testville', 'Testland', 0, 0, 'draft');
  EXCEPTION WHEN not_null_violation THEN
    RAISE EXCEPTION 'businesses.description still has no DEFAULT: approving a new claim will 500';
  END;

  ---------------------------------------------------------------------
  -- 3. moderation hide/restore on a business (admin.go)
  ---------------------------------------------------------------------
  UPDATE businesses
  SET status = 'suspended',
      pre_suspend_status = COALESCE(pre_suspend_status, status::text),
      updated_at = now()
  WHERE id = b1 AND deleted_at IS NULL;
  SELECT status::text, pre_suspend_status INTO st, pre FROM businesses WHERE id = b1;
  IF st <> 'suspended' OR pre <> 'verified' THEN
    RAISE EXCEPTION 'hide did not suspend + preserve prior status (got %, %)', st, pre;
  END IF;

  UPDATE businesses
  SET status = CASE
        WHEN pre_suspend_status IS NOT NULL THEN pre_suspend_status::business_status
        ELSE 'pending_review'::business_status
      END,
      pre_suspend_status = NULL
  WHERE id = b1 AND deleted_at IS NULL;
  SELECT status::text, pre_suspend_status INTO st, pre FROM businesses WHERE id = b1;
  IF st <> 'verified' OR pre IS NOT NULL THEN
    RAISE EXCEPTION 'restore did not return the prior status (got %, %)', st, pre;
  END IF;

  -- With no captured status, restore must fall back to pending_review rather
  -- than failing the enum cast.
  UPDATE businesses SET pre_suspend_status = NULL, status = 'suspended' WHERE id = b1;
  UPDATE businesses
  SET status = CASE
        WHEN pre_suspend_status IS NOT NULL THEN pre_suspend_status::business_status
        ELSE 'pending_review'::business_status
      END
  WHERE id = b1;
  SELECT status::text INTO st FROM businesses WHERE id = b1;
  IF st <> 'pending_review' THEN
    RAISE EXCEPTION 'restore fallback should be pending_review, got %', st;
  END IF;

  ---------------------------------------------------------------------
  -- 4. soft-deleted slugs are reusable (businesses_slug_live is partial)
  ---------------------------------------------------------------------
  INSERT INTO businesses
    (id, owner_id, name, slug, description, category_id, address, city, country, lat, lng, status, deleted_at)
  VALUES (b2, u2, 'Old Biz', 'reusable-slug', 'An old description of the business.',
          cat, '4 Test St', 'Testville', 'Testland', 0, 0, 'verified', now());

  -- Two LIVE rows may not share a slug.
  BEGIN
    INSERT INTO businesses
      (id, owner_id, name, slug, description, category_id, address, city, country, lat, lng, status)
    VALUES (b3, u2, 'Dup A', 'reusable-slug', 'First live row.', cat, '5 Test St', 'Testville', 'Testland', 0, 0, 'draft');
    INSERT INTO businesses
      (id, owner_id, name, slug, description, category_id, address, city, country, lat, lng, status)
    VALUES ('12121212-1212-1212-1212-121212121212', u2, 'Dup B', 'reusable-slug', 'Second live row.',
              cat, '6 Test St', 'Testville', 'Testland', 0, 0, 'draft');
    RAISE EXCEPTION 'two live businesses were allowed to share a slug';
  EXCEPTION WHEN unique_violation THEN
    NULL; -- expected
  END;

  -- A new live row may take a soft-deleted row's slug.
  BEGIN
    INSERT INTO businesses
      (id, owner_id, name, slug, description, category_id, address, city, country, lat, lng, status)
    VALUES ('13131313-1313-1313-1313-131313131313', u2, 'New Biz', 'reusable-slug',
            'Taking the freed slug.', cat, '7 Test St', 'Testville', 'Testland', 0, 0, 'draft');
  EXCEPTION WHEN unique_violation THEN
    RAISE EXCEPTION 'a soft-deleted slug is still reserved (SlugTaken says it is free, so the INSERT 500s)';
  END;

  ---------------------------------------------------------------------
  -- 5. media soft delete + the attachment-hide statement (admin.go)
  ---------------------------------------------------------------------
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'media' AND column_name = 'deleted_at'
  ) THEN
    RAISE EXCEPTION 'media.deleted_at is missing: an attachment report cannot be actioned';
  END IF;

  INSERT INTO media (id, uploader_id, kind, original_name, mime, size, path)
  VALUES ('14141414-1414-1414-1414-141414141414', u1, 'chat_image', 'x.png', 'image/png', 10, 'test/x.png');

  -- The kind allowlist must EXCLUDE document_verification, or a moderator
  -- "hide" would destroy a compliance document.
  UPDATE media SET deleted_at = now()
  WHERE id = '14141414-1414-1414-1414-141414141414'
    AND kind = ANY(ARRAY['chat_image','chat_file','chat_audio','chat_video',
                         'avatar','product','gallery']);
  IF NOT EXISTS (SELECT 1 FROM media
                 WHERE id = '14141414-1414-1414-1414-141414141414'
                   AND deleted_at IS NOT NULL) THEN
    RAISE EXCEPTION 'a chat_image was not hidden by the attachment-hide statement';
  END IF;

  INSERT INTO media (id, uploader_id, kind, original_name, mime, size, path)
  VALUES ('15151515-1515-1515-1515-151515151515', u1, 'document_verification',
          'id.pdf', 'application/pdf', 10, 'test/id.pdf');
  UPDATE media SET deleted_at = now()
  WHERE id = '15151515-1515-1515-1515-151515151515'
    AND kind = ANY(ARRAY['chat_image','chat_file','chat_audio','chat_video',
                         'avatar','product','gallery']);
  IF EXISTS (SELECT 1 FROM media
             WHERE id = '15151515-1515-1515-1515-151515151515'
               AND deleted_at IS NOT NULL) THEN
    RAISE EXCEPTION 'a verification document was hidden — moderation must not destroy compliance evidence';
  END IF;

  ---------------------------------------------------------------------
  -- 6. the constraint/index inventory 0029 promised
  ---------------------------------------------------------------------
  SELECT count(*) INTO n FROM pg_indexes
  WHERE indexname IN ('businesses_slug_live', 'idx_businesses_unclaimed',
                      'collections_one_default', 'media_path_key',
                      'trend_snapshots_uniq', 'business_claims_open_business',
                      'idx_media_deleted_at');
  IF n <> 7 THEN
    RAISE EXCEPTION 'expected 7 new indexes from 0029, found %', n;
  END IF;

  IF EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'businesses_slug_key') THEN
    RAISE EXCEPTION 'businesses_slug_key still exists; the soft-delete-aware index cannot take effect';
  END IF;

  -- One default collection per user.
  BEGIN
    INSERT INTO collections (id, user_id, name, slug, is_default)
    VALUES ('16161661-1616-1616-1616-161616161616', u1, 'A', 'a', true);
    INSERT INTO collections (id, user_id, name, slug, is_default)
    VALUES ('17171717-1717-1717-1717-171717171717', u1, 'B', 'b', true);
    RAISE EXCEPTION 'two default collections were allowed for one user';
  EXCEPTION WHEN unique_violation THEN
    NULL; -- expected
  END;

  RAISE NOTICE 'all 0029 integrity + moderation assertions passed';
  -- Everything above is inside the caller's transaction; the ROLLBACK that
  -- follows this DO block discards it.
END $do$;

-- ---------------------------------------------------------------------------
-- 0032: retirement releases the slug.
--
-- This is the assertion that would have caught 0029's inert partial index. The
-- Go test in internal/repo proves the same thing through the service, but only
-- when a database is configured; this block runs in the migration-contract step
-- against the CI database, so the invariant is checked on every build.
--
-- Two claims are asserted, and the second is the one that would be missed by a
-- naive fix:
--
--   1. A closed listing's slug is genuinely free, so another business can take
--      it. This is the user-visible half.
--   2. A closed listing is still invisible to the public. A fix that only did
--      (1) - and forgot the partial index or the visibility filter - would let
--      two businesses serve the same URL.
--
-- No BEGIN/ROLLBACK of its own: the whole file already runs inside one
-- transaction that the single ROLLBACK at the end discards, so a nested BEGIN
-- here only earns a "there is already a transaction in progress" warning and a
-- ROLLBACK that would commit the earlier assertions' work out from under them.
-- ---------------------------------------------------------------------------

DO $do$
DECLARE
  b_closed  uuid;
  b_new     uuid;
  cat       uuid;
  resolved  uuid;
  -- u1 is declared per-DO-block in this file; the seed's fixture user. Declared
  -- again here rather than reused, because each DO block is its own scope.
  u1        uuid := '11111111-1111-1111-1111-111111111111';
BEGIN
  SELECT id INTO cat FROM categories LIMIT 1;

  -- lat/lng are NOT NULL today. That is the data-model impossibility behind the
  -- reserved service-area migration: a mobile or home-services business has no
  -- coordinates and cannot be represented at all.
  INSERT INTO businesses (id, owner_id, name, slug, description, currency, address, city, country, lat, lng, status, deleted_at)
  VALUES ('00320000-0000-4000-8000-000000000001', u1, 'Retiring Shop', 'retiring-shop',
          'a description long enough to clear any minimum length a listing must satisfy',
          'USD', '1 Test Street', 'Jakarta', 'Indonesia', -6.2, 106.8, 'closed', now())
  RETURNING id INTO b_closed;

  IF b_closed IS NULL THEN
    RAISE EXCEPTION 'could not create the retiring listing';
  END IF;

  -- (1) The slug is free. If 0032's backfill or a later Close stopped writing
  -- deleted_at, this duplicate insert raises unique_violation and fails here.
  BEGIN
    INSERT INTO businesses (id, owner_id, name, slug, description, currency, address, city, country, lat, lng, status)
    VALUES ('00320000-0000-4000-8000-000000000002', u1, 'New Owner', 'retiring-shop',
            'a description long enough to clear any minimum length a listing must satisfy',
            'USD', '2 Test Street', 'Jakarta', 'Indonesia', -6.3, 106.9, 'verified')
    RETURNING id INTO b_new;
  EXCEPTION WHEN unique_violation THEN
    RAISE EXCEPTION 'a closed listing still reserves its slug; 0032''s backfill or Close is not writing deleted_at';
  END;

  -- (2) Two rows now carry the same slug string, and only the live one may
  -- resolve. If GetBySlug or the index stopped excluding retired rows, the
  -- closed shop would serve the new shop's URL.
  SELECT id INTO resolved FROM businesses
   WHERE slug = 'retiring-shop' AND deleted_at IS NULL;

  IF resolved IS DISTINCT FROM b_new THEN
    RAISE EXCEPTION 'slug ''retiring-shop'' resolved to %, expected the live listing % - a retired row is shadowing it',
      coalesce(resolved::text, 'nothing'), b_new;
  END IF;

  -- And the retired row is out of the public predicate entirely.
  IF EXISTS (SELECT 1 FROM businesses
              WHERE id = b_closed AND status = 'verified' AND deleted_at IS NULL) THEN
    RAISE EXCEPTION 'the retired listing is still selectable as a live verified business';
  END IF;
  RAISE NOTICE '0032 slug-release assertions passed';
END $do$;

-- ---------------------------------------------------------------------------
-- 0035: subscriptions is UNIQUE on business_id.
--
-- This is the assertion for the defect described in 0034. It is a pure schema
-- check with no fixtures, so it could be asserted the moment 0035 was written --
-- but nothing in the Go test suite covers it either, because the failure mode is
-- SQLSTATE 42P10 at RUNTIME: repo/billing.go compiles fine, the upsert compiles
-- fine, and the statement only fails when Stripe delivers an event. A
-- green build with a broken billing path was the whole problem.
--
-- The check is written against pg_indexes rather than by attempting the upsert,
-- for one reason: attempting it would need a business, a plan and a subscription
-- row, and a 42P10 raised inside a BEGIN block poisons the transaction for
-- everything after it. Reading the catalog cannot fail.
--
-- Two things are asserted, because either one alone is insufficient:
--   1. an index named subscriptions_business_unq exists AND is UNIQUE, and
--   2. it is unique on business_id alone.
-- A non-unique index with the right name would pass a name-only check, which is
-- the same class of mistake as 0028's IF NOT EXISTS matching a name with
-- different columns.
-- ---------------------------------------------------------------------------

DO $do$
DECLARE
  def text;
  n    int;
BEGIN
  SELECT pg_get_indexdef(i.indexrelid) INTO def
    FROM pg_index i
    JOIN pg_class c ON c.oid = i.indexrelid
   WHERE c.relname = 'subscriptions_business_uniq'
     AND i.indisunique;

  IF def IS NULL THEN
    RAISE EXCEPTION
      'subscriptions has no UNIQUE index named subscriptions_business_uniq. repo/billing.go UpsertSubscription uses ON CONFLICT (business_id); Postgres resolves that against unique indexes only, so every customer.subscription.* webhook and the 6-hourly reconcile raised 42P10 and were then permanently swallowed by the billing_webhook_events ledger. See migration 0034.';
  END IF;

  IF def NOT LIKE '%(business_id)%' THEN
    RAISE EXCEPTION
      'subscriptions_business_uniq is unique on the wrong column set: %', def;
  END IF;

  -- And the live data must already satisfy it, or the concurrent build in 0035
  -- failed and left an INVALID index behind. Migrate() hard-fails at boot on an
  -- invalid index, but this file is also run by hand, so check it here too.
  SELECT count(*) INTO n
    FROM pg_index i
    JOIN pg_class c ON c.oid = i.indexrelid
   WHERE c.relname = 'subscriptions_business_uniq'
     AND NOT i.indisvalid;

  IF n > 0 THEN
    RAISE EXCEPTION
      'subscriptions_business_uniq exists but is INVALID - the concurrent build was interrupted. Re-run 0035.';
  END IF;

  RAISE NOTICE '0035 subscriptions business_id uniqueness assertion passed';
END $do$;

ROLLBACK;

\echo === done (all work rolled back)
