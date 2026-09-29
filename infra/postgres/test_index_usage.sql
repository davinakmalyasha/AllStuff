-- Verifies that the indexes added in 0031_index_hot.sql are actually USED.
--
-- An index that exists but is never chosen is worse than no index: it costs
-- write amplification and storage for nothing. `pg_indexes` only proves the
-- DDL ran; only a plan proves the query shape matches the index expression.
--
-- The search predicate in service/search.go must call the IMMUTABLE helpers
-- (biz_search_tsv / product_search_tsv). If the query still hand-writes
-- to_tsvector(...) inline, the expression differs textually from the index
-- expression and the planner cannot use it — which is the failure mode the
-- helpers exist to prevent.
--
-- THIS FILE IS A MANUAL TOOL AND ASSERTS NOTHING.
--
-- Every statement below is a bare EXPLAIN, so this file cannot fail: it prints
-- plans for a human to read. It is not run in CI, and the deterministic checks
-- that used to sit at the top of it (the indexes exist, the redundant ones were
-- dropped, nothing is INVALID) now live in test_index_inventory.sql, which does
-- raise on failure and IS run in CI.
--
-- It is also unreliable on an empty database. With no statistics the planner
-- has no cost model, so it will happily pick a small index and evaluate the
-- expensive predicate as a Filter, which makes an empty-table EXPLAIN say
-- nothing about whether the index is usable. For a decision, use the seeded
-- variant, which loads synthetic rows, ANALYZEs, and asserts the plan shape:
--
--   psql -d bizverse -v ON_ERROR_STOP=1 -f infra/postgres/test_index_usage_seeded.sql
--
-- Run this one only to read plans interactively:
--   psql -d bizverse -f infra/postgres/test_index_usage.sql

\pset footer off
\echo === plan shapes, for reading by eye only (no assertions)
\echo === for an assertion, use test_index_usage_seeded.sql

\echo
\echo === the search predicate can use the index (expression must match)


\echo
\echo === the search predicate can use the index (expression must match)
-- This is the exact predicate shape from service/search.go, written with the
-- helpers. If the planner cannot use idx_businesses_tsv here, it cannot use it
-- for the real query either.
EXPLAIN (COSTS OFF)
SELECT b.id FROM businesses b
 WHERE b.status = 'verified' AND b.deleted_at IS NULL
   AND biz_search_tsv(b.name, b.tagline, b.description, b.city, b.tags)
       @@ plainto_tsquery('simple', 'coffee');

\echo
\echo === product name search
EXPLAIN (COSTS OFF)
SELECT p.id FROM products p
 WHERE p.is_published AND p.deleted_at IS NULL
   AND product_search_tsv(p.name) @@ plainto_tsquery('simple', 'latte');

\echo
\echo === the notification tier lookup (per-notification EXISTS on owner_id)
EXPLAIN (COSTS OFF)
SELECT u.id FROM users u
 WHERE u.id = '00000000-0000-0000-0000-000000000000'
   AND EXISTS (SELECT 1 FROM businesses b
                WHERE b.owner_id = u.id AND b.deleted_at IS NULL);

\echo
\echo === save_count on a business card
EXPLAIN (COSTS OFF)
SELECT (SELECT count(*) FROM collection_items ci
         WHERE ci.target_type = 'business'
           AND ci.target_id = '00000000-0000-0000-0000-000000000000');

\echo
\echo === the anomaly queue
EXPLAIN (COSTS OFF)
SELECT e.id FROM engagement_events e
 WHERE e.flagged = true
 ORDER BY e.occurred_at DESC, e.id DESC
 LIMIT 100;

\echo
\echo === the admin audit trail
EXPLAIN (COSTS OFF)
SELECT ma.id FROM moderation_actions ma
 ORDER BY ma.created_at DESC, ma.id DESC
 LIMIT 50 OFFSET 0;

\echo
\echo === DRIFT CHECK: the OLD inline expression must NOT be used
-- If this shows an index scan, something is still matching the inline
-- to_tsvector(...) form and the two definitions are at risk of diverging.
\echo (an inline Seq Scan here is EXPECTED and is the point of the helpers)
EXPLAIN (COSTS OFF)
SELECT b.id FROM businesses b
 WHERE b.status = 'verified' AND b.deleted_at IS NULL
   AND to_tsvector('simple', coalesce(b.name,'') || ' ' || coalesce(b.tagline,'') || ' ' ||
                        coalesce(b.description,'') || ' ' || coalesce(b.city,'') || ' ' ||
                        coalesce(array_to_string(b.tags,' '),''))
       @@ plainto_tsquery('simple', 'coffee');

\echo
\echo === done — compare the plan shapes above by hand
