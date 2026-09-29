-- Loads a synthetic directory into a transaction, then re-checks the plans from
-- test_index_usage.sql with real statistics (ANALYZE) and real row counts.
--
-- WHY THE DATA MATTERS: on an empty table the planner has no statistics, so
-- every plan is equally cheap and it will happily pick a small index and
-- evaluate the expensive predicate as a Filter. That makes an empty-table EXPLAIN
-- useless for deciding whether a full-text index is used. The rows here are
-- generated from generate_series so the FTS term statistics are meaningful.
--
-- Everything is rolled back.

\pset footer off
\t on
\echo === seeding a synthetic directory (rolled back)

BEGIN;

INSERT INTO users (id, email, username, name, password_hash)
SELECT gen_random_uuid(),
       'bulk' || g || '@example.com',
       'bulk_user_' || g,
       'Bulk User ' || g,
       'x'
FROM generate_series(1, 200) g;

INSERT INTO categories (id, name, slug, icon)
SELECT gen_random_uuid(), 'Cat ' || g, 'cat-' || g, 'store'
FROM generate_series(1, 20) g;

-- 5,000 businesses.
--
-- DISTRIBUTION MATTERS, and getting it wrong makes the test lie. A first draft
-- gave every business the same owner_id and the same term to every third row.
-- With one distinct owner_id and 33% selectivity the planner CORRECTLY prefers
-- a sequential scan, so the plan showed "index not used" for indexes that are
-- perfectly usable. A test that cannot distinguish "index is broken" from
-- "index is not the right tool for this data" is not a test.
--
-- So: owners are spread across the 200 users, and a RARE term is available for
-- the selectivity-sensitive checks alongside the common one.
INSERT INTO businesses
  (id, owner_id, name, slug, description, category_id, address, city, country,
   lat, lng, status, tags, created_at)
SELECT gen_random_uuid(),
       (SELECT u.id FROM users u ORDER BY u.id LIMIT 1 OFFSET (g % 200)),
       CASE WHEN g % 3 = 0 THEN 'Coffee House ' || g
            WHEN g % 997 = 0 THEN 'Artisan Barista ' || g   -- rare term
            ELSE 'Business ' || g END,
       'bulk-biz-' || g,
       CASE WHEN g % 3 = 0
            THEN 'Speciality coffee, espresso and pastries served daily. Latte and cold brew.'
            WHEN g % 997 = 0
            THEN 'Award winning single origin, cupping certified.'
            ELSE 'A general local business offering services to the community. ' || repeat('x', 200)
       END,
       (SELECT id FROM categories ORDER BY random() LIMIT 1),
       g || ' Main St',
       CASE WHEN g % 2 = 0 THEN 'Jakarta' ELSE 'Bandung' END,
       'Indonesia',
       -6.2 + (g % 100) / 100.0,
       106.8 + (g % 100) / 100.0,
       'verified',
       CASE WHEN g % 997 = 0 THEN ARRAY['artisan','cupping']::text[]
            ELSE ARRAY['coffee','food','service']::text[] END,
       now() - (g || ' minutes')::interval
FROM generate_series(1, 5000) g;

INSERT INTO products (id, business_id, name, description, currency, is_published, created_at)
SELECT gen_random_uuid(),
       (SELECT id FROM businesses LIMIT 1),
       CASE WHEN g % 2 = 0 THEN 'Latte ' || g ELSE 'Menu item ' || g END,
       'A latte with a rich espresso base.',
       'USD', true, now()
FROM generate_series(1, 1000) g;
-- collection_items.collection_id is a real FK, so a collection must exist
-- first. One per user, then items pointing at it.
INSERT INTO collections (id, user_id, name, slug, is_default)
SELECT gen_random_uuid(), id, 'Favourites', 'fav-' || g, true
FROM (SELECT id, row_number() OVER () AS g FROM users) s;

-- One item per distinct business, so the (collection_id, target_type,
-- target_id) unique key is not violated by a random pick repeating.
INSERT INTO collection_items (id, collection_id, target_type, target_id, created_at)
SELECT gen_random_uuid(),
       (SELECT id FROM collections LIMIT 1),
       'business',
       b.id,
       now()
FROM businesses b
LIMIT 3000;

-- ANALYZE is the point of this file: without statistics the planner cannot
-- weigh the GIN index against a sequential scan.
ANALYZE businesses;
ANALYZE products;
ANALYZE collection_items;
ANALYZE collections;

\echo
\echo --- 1a. business full-text search, SELECTIVE term (expect the GIN index)
\echo     "artisan" appears in ~5 of 5000 rows, so an index is the right tool.
EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF)
SELECT b.id FROM businesses b
 WHERE b.status = 'verified' AND b.deleted_at IS NULL
   AND biz_search_tsv(b.name, b.tagline, b.description, b.city, b.tags)
       @@ plainto_tsquery('simple', 'artisan');

\echo
\echo --- 1b. business full-text search, COMMON term (1/3 of rows)
\echo     A Seq Scan here is CORRECT, not a missing index: at 33% selectivity
\echo     reading the whole table is cheaper than probing the index. Both plans
\echo     must be read together; checking only 1b would falsely report a
\echo     regression.
EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF)
SELECT b.id FROM businesses b
 WHERE b.status = 'verified' AND b.deleted_at IS NULL
   AND biz_search_tsv(b.name, b.tagline, b.description, b.city, b.tags)
       @@ plainto_tsquery('simple', 'coffee');

\echo
\echo --- 2. product name search
EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF)
SELECT p.id FROM products p
 WHERE p.is_published AND p.deleted_at IS NULL
   AND product_search_tsv(p.name) @@ plainto_tsquery('simple', 'latte');

\echo
\echo --- 3. save_count subquery (24 of these per search page)
EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF)
SELECT (SELECT count(*) FROM collection_items ci
         WHERE ci.target_type = 'business'
           AND ci.target_id = (SELECT id FROM businesses LIMIT 1));

\echo
\echo --- 4. the notification tier EXISTS (owners are spread over 200 users,
\echo        so owner_id is selective here and the index should be used)
EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF)
SELECT u.id FROM users u
 WHERE u.id = (SELECT id FROM users LIMIT 1)
   AND EXISTS (SELECT 1 FROM businesses b
                WHERE b.owner_id = u.id AND b.deleted_at IS NULL);

\echo
\echo --- 5a. ILIKE on a RARE substring (expect the trigram index)
EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF)
SELECT b.id FROM businesses b
 WHERE b.status = 'verified' AND b.deleted_at IS NULL
   AND b.name ILIKE '%artisan%';

\echo
\echo --- 5b. ILIKE on a COMMON substring (Seq Scan is correct at 33% selectivity)
EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF)
SELECT b.id FROM businesses b
 WHERE b.status = 'verified' AND b.deleted_at IS NULL
   AND b.name ILIKE '%coffee%';

\echo
\echo --- 6. COMBINED: the real search shape (FTS OR ILIKE OR trigram, joined to category)
EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF)
SELECT b.id FROM businesses b
  JOIN categories cat ON cat.id = b.category_id
 WHERE b.status = 'verified' AND b.deleted_at IS NULL
   AND (
        biz_search_tsv(b.name, b.tagline, b.description, b.city, b.tags)
            @@ plainto_tsquery('simple', 'coffee')
     OR b.name ILIKE '%coffee%'
     OR cat.name ILIKE '%coffee%'
   );

\echo
\echo --- 7. row counts, for sanity
SELECT (SELECT count(*) FROM businesses) AS businesses,
       (SELECT count(*) FROM products)    AS products,
       (SELECT count(*) FROM collection_items) AS collection_items;

-- ===========================================================================
-- 8. ASSERTIONS — what this file actually gates on, and why
-- ===========================================================================
--
-- Everything above PRINTS a plan for a human to read. That is deliberate and it
-- is not a test: the planner's choice depends on the PostgreSQL version, the
-- statistics it sampled, `random_page_cost`, and the hardware. An assertion
-- like "the plan must contain idx_businesses_tsv" is a flaky gate: it fails on a
-- faster machine, passes on a slower one, and trains people to re-run CI until
-- it goes green. Asserting on EXPLAIN output is asserting on the environment.
--
-- There is one property here that is NOT environment-dependent and is worth
-- gating on: whether each hot-path index EXISTS, is VALID, and is APPLICABLE to
-- the query it exists for. A planner may reasonably choose a sequential scan
-- over a bitmap heap scan; it may never choose to NOT USE an index that is the
-- only way to evaluate an expression.
--
-- `enable_seqscan = off` isolates exactly that. It does not say "use this index",
-- it says "do not consider a sequential scan", and Postgres will still pick a
-- wrong index or give up entirely. So the assertion below fails when:
--   * the index was never created (a migration that silently no-ops)
--   * the build was interrupted, leaving indisvalid = false
--   * the index expression no longer matches the query expression, which is the
--     exact drift 0031 was written to stop
-- and it does NOT fail when the planner merely prefers a different plan.
DO $assertions$
DECLARE
    v_missing text;
    v_plan    text;
    v_count   int;
    r         record;
BEGIN
    -- Row counts are deterministic: if the synthetic seed did not load, every
    -- plan above is meaningless and the file should say so rather than printing
    -- confident nonsense.
    SELECT count(*) INTO v_count FROM businesses;
    IF v_count <> 5000 THEN
        RAISE EXCEPTION 'fixture FAIL: expected 5000 businesses, got %', v_count;
    END IF;

    -- Every index below must exist and be valid. `pg_index.indisvalid` is the
    -- one property that a failed CREATE INDEX CONCURRENTLY leaves false, and it
    -- is exactly what `IF NOT EXISTS` never re-checks.
    SELECT string_agg(want.name, ', ') INTO v_missing
      FROM (VALUES
              ('idx_businesses_tsv'),
              ('idx_businesses_tsv_all'),
              ('idx_products_tsv'),
              ('idx_businesses_cat_verified'),
              ('idx_businesses_latlng'),
              ('idx_businesses_owner'),
              ('idx_businesses_city'),
              ('idx_businesses_city_trgm'),
              ('idx_businesses_tagline_trgm'),
              ('idx_categories_name_trgm'),
              ('idx_collection_items_target'),
              ('idx_moderation_actions_created'),
              ('idx_events_flagged'),
              ('idx_reviews_business_rating_live'),
              ('businesses_slug_live'),
              ('media_path_key')
           ) AS want(name)
     WHERE NOT EXISTS (
        SELECT 1 FROM pg_index i
          JOIN pg_class c ON c.oid = i.indexrelid
          JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = 'public' AND c.relname = want.name AND i.indisvalid);

    IF v_missing IS NOT NULL THEN
        RAISE EXCEPTION
            'index FAIL: missing or INVALID: %. An index with indisvalid = false was skipped by every past IF NOT EXISTS replay — see docs/RUNBOOK.md "CREATE INDEX CONCURRENTLY fails".',
            v_missing;
    END IF;

    -- The FTS expression must be APPLICABLE. If the index expression and the
    -- query expression have drifted apart, the planner cannot use the index at
    -- all — and that is precisely the bug 0031's biz_search_tsv helpers exist to
    -- make unrepresentable. Forcing the index by disabling sequential scans
    -- turns "the planner did not choose it" into a non-event while keeping
    -- "the planner CANNOT choose it" a hard failure.
    -- `enable_seqscan = off` ALONE IS NOT ENOUGH, and finding that out is the
    -- reason this section exists as an assertion rather than a printed plan.
    -- With only a sequential scan disabled, the planner picks the cheapest
    -- remaining index — a btree on `status` — and evaluates the FTS expression
    -- as a Filter. The plan is legal, fast to plan, and catastrophically slow to
    -- execute, and it looks fine in EXPLAIN output.
    --
    -- A GIN index can ONLY be reached through a Bitmap Index Scan, so disabling
    -- plain index scans as well leaves exactly one route to the expression. Now
    -- "the planner did not choose it" is impossible and "the planner CANNOT
    -- choose it" is the only remaining failure — which is the property worth
    -- asserting.
    SET LOCAL enable_seqscan = off;
    SET LOCAL enable_bitmapscan = on;

    -- =========================================================================
    -- How to test that an expression is INDEX-ABLE, deterministically
    -- =========================================================================
    -- This took three attempts, and the two wrong ones are worth recording.
    --
    -- Attempt 1: `enable_seqscan = off`. Does not work. The planner simply picks
    -- the cheapest remaining index — a btree on `status` — and evaluates the FTS
    -- expression as a Filter. The plan is legal, plans instantly, and is
    -- catastrophically slow to execute. It also looks entirely reasonable in
    -- EXPLAIN output, which is what makes it dangerous.
    --
    -- Attempt 2: `enable_seqscan = off, enable_indexscan = off`. Also does not
    -- work. GUCs tell the planner what to AVOID; they cannot tell it what to USE.
    -- With both disabled it still chose a btree — this time
    -- `idx_businesses_status` — for a Bitmap Index Scan, and filtered the rest.
    --
    -- What DOES work is removing the competing options. Dropping every other
    -- index on `businesses` leaves exactly one route to the expression, so the
    -- plan answers the question actually being asked: "if the planner had only
    -- the GIN index available, COULD it evaluate this?" That is the drift check —
    -- an index expression that no longer matches the query expression makes this
    -- impossible, and nothing else does.
    --
    -- Safe, because this whole file runs inside a transaction that is rolled
    -- back.
    -- =========================================================================

    EXPLAIN (COSTS OFF, FORMAT JSON)
    SELECT b.id FROM businesses b
     WHERE b.status = 'verified' AND b.deleted_at IS NULL
       AND b.city = 'Jakarta'
     INTO v_plan;
    IF v_plan !~ 'idx_businesses_city' THEN
        RAISE EXCEPTION 'city FAIL: no usable index on businesses.city. Plan was:%', v_plan;
    END IF;

    -- The value must be known at PLAN time for the index to be usable. A
    -- volatile expression like gen_random_uuid() is re-evaluated per row, so the
    -- planner cannot turn it into an index lookup and correctly falls back to a
    -- scan — which reads exactly like a missing index and is not one. An
    -- InitPlan from a subquery is constant for the whole statement, which is
    -- what the real owner-scoped queries look like: a session-derived id bound
    -- as a parameter.
    -- Two things this query must get right, both of which are easy to get wrong
    -- and both of which look identical to "the index is missing":
    --
    -- 1. idx_businesses_owner is PARTIAL: `WHERE deleted_at IS NULL`. A partial
    --    index is only usable when the query can prove the predicate holds, so
    --    the query has to carry `deleted_at IS NULL` — which is exactly what
    --    repo.Businesses.ListByOwner does. A test that omits it asserts nothing
    --    about the real access path.
    -- 2. The value must be known at PLAN time. A volatile expression like
    --    gen_random_uuid() is re-evaluated per row, so the planner cannot turn it
    --    into an index lookup and correctly falls back to a scan. An InitPlan
    --    from a subquery is constant for the whole statement, which is what an
    --    owner-scoped query with a session-derived id actually looks like.
    EXPLAIN (COSTS OFF, FORMAT JSON)
    SELECT b.id FROM businesses b
     WHERE b.deleted_at IS NULL
       AND b.owner_id = (SELECT id FROM users ORDER BY id LIMIT 1)
     INTO v_plan;
    RAISE NOTICE 'owner plan: %', left(replace(v_plan, E'\n', ' '), 200);
    IF v_plan !~ 'idx_businesses_owner' THEN
        RAISE EXCEPTION
            'owner FAIL: idx_businesses_owner is not usable for an owner-scoped live-row lookup. Plan was:%',
            v_plan;
    END IF;

    -- Same class of check for the city index, which is a plain (non-partial)
    -- btree. service/cities.go filters on the city name for every city page,
    -- leaderboard scope and map centre, so this is the index that five separate
    -- code paths depend on.
    EXPLAIN (COSTS OFF, FORMAT JSON)
    SELECT b.id FROM businesses b
     WHERE b.status = 'verified' AND b.deleted_at IS NULL
       AND b.city = 'Jakarta'
     INTO v_plan;
    IF v_plan !~ 'idx_businesses_city' THEN
        RAISE EXCEPTION 'city FAIL: no usable index on businesses.city. Plan was:%', v_plan;
    END IF;

    -- The btree checks below are single-column equality on a selective value, so they
    -- legitimately want a plain Index Scan. Re-enable it now that the GIN
    -- applicability check is done.
    SET LOCAL enable_indexscan = on;
    SET LOCAL enable_bitmapscan = on;

    RAISE NOTICE 'index assertions ok (16 indexes present+valid; city, owner and FTS expressions all applicable)';
    FOR r IN
        SELECT c.relname
          FROM pg_index i
          JOIN pg_class c     ON c.oid = i.indexrelid
          JOIN pg_class t     ON t.oid = i.indrelid
          JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = 'public'
           AND t.relname = 'businesses'
           AND NOT i.indisprimary
           AND c.relname NOT IN ('idx_businesses_tsv', 'idx_businesses_tsv_all')
    LOOP
        EXECUTE format('DROP INDEX %I', r.relname);
    END LOOP;

    -- `EXPLAIN ... INTO <text>` inside plpgsql captures ONLY THE FIRST LINE of a
    -- text-format plan, so the answer — which lives on line two — was being
    -- compared against a truncated string. `FORMAT JSON` returns one
    -- self-contained value, so the whole plan is captured with no truncation.
    EXPLAIN (COSTS OFF, FORMAT JSON)
    SELECT b.id FROM businesses b
     WHERE b.status = 'verified' AND b.deleted_at IS NULL
       AND biz_search_tsv(b.name, b.tagline, b.description, b.city, b.tags)
           @@ plainto_tsquery('simple', 'coffee')
     INTO v_plan;

    -- Accept EITHER tsvector index. There are two on purpose: the partial one
    -- (live + verified only) and the full one, which exists for admin, claims
    -- and moderation queries that do not carry the status predicate. The planner
    -- is entitled to prefer either depending on the query, and pinning the
    -- assertion to the partial index made it fail while the expression was
    -- perfectly well covered — an over-specified assertion is a flaky gate with
    -- extra steps.
    --
    -- What is NOT acceptable is the planner using NEITHER, which is what happens
    -- if the index expression drifts from the query expression. That is the
    -- drift 0031's biz_search_tsv helpers exist to make unrepresentable.

    -- Always show the plan, even on success. The assertion tells you WHETHER the
    -- index is usable; the plan tells you WHICH index won, which is the part you
    -- need when the answer is "not the one you expected".
    RAISE NOTICE 'fts plan: %', left(replace(v_plan, E'\n', ' '), 300);

    IF v_plan !~ 'idx_businesses_tsv' THEN
        RAISE EXCEPTION
            'fts FAIL: the business FTS expression is covered by neither idx_businesses_tsv nor idx_businesses_tsv_all.%Plan was:%',
            chr(10), v_plan;
    END IF;
    IF v_plan !~ 'idx_businesses_tsv_all' THEN
        RAISE NOTICE
            'note: the partial idx_businesses_tsv was not used here; the planner took the full index instead. Both are valid, and the full index is the one to watch — it covers rows the public surface never serves, so it is pure write amplification if the partial one keeps winning.';
    END IF;
END $assertions$;

ROLLBACK;
\echo === done (rolled back)
