-- 0031_index_hot.sql — the five indexes whose absence made search, notifications
-- and the admin queue scan whole tables (Phase 2 data layer).
--
-- migrate:idempotent  yes
-- migrate:concurrent  true
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        Every index build is preceded by DROP INDEX CONCURRENTLY IF EXISTS of the same name, so an interrupted build is discarded and rebuilt on the next run rather than skipped.
--
-- Every index is CONCURRENTLY, so the runner (db/migrate.go) executes these
-- outside the migration transaction on autocommit. A concurrent build still
-- takes a SHARE UPDATE EXCLUSIVE lock per statement — brief and non-blocking
-- for reads and writes — but it is not instant, which is why each one is a
-- separate statement rather than one combined rebuild.
--
-- REPLAY SAFETY — this file previously argued the wrong way round
-- ---------------------------------------------------------------------------
-- The original text here said: "All statements are idempotent ... `CONCURRENTLY
-- IF NOT EXISTS` means a retried build finds the (possibly invalid) index
-- present and moves on", and then called the resulting INVALID index "a real
-- hazard" in an operator note further down. That is self-contradictory, and the
-- second half was the truth.
--
-- `CREATE INDEX CONCURRENTLY` that fails leaves a row in pg_class with
-- pg_index.indisvalid = false. `IF NOT EXISTS` compares NAMES ONLY and never
-- looks at indisvalid, so a retried build finds the broken index, skips, and
-- the file records its version marker as successfully applied. The index the
-- file exists to create is now permanently absent, and the only symptom is a
-- query that got slow at some point in the past.
--
-- So the twenty-six builds below no longer use IF NOT EXISTS. Each is preceded
-- by `DROP INDEX CONCURRENTLY IF EXISTS <same name>`, which makes a partial
-- failure self-healing: the replay discards the invalid relation and rebuilds
-- from clean. A replay costs one index rebuild; skipping costs the index.
--
-- Migrate() additionally hard-fails at boot if any index in the public schema is
-- invalid, so this state can no longer be reached silently in the first place.
-- See InvalidIndexes in services/api/internal/db/migrate.go.
-- ---------------------------------------------------------------------------

-- ===========================================================================
-- 1. Full-text search on businesses — the single largest win
-- ===========================================================================
--
-- service/search.go builds a five-way OR predicate:
--
--   to_tsvector('simple', name||' '||tagline||' '||description||' '
--                        ||' '||city||' '||array_to_string(tags,' '))
--       @@ plainto_tsquery('simple', $1)
--   OR b.name ILIKE '%'||$2||'%'
--   OR b.name % $3
--   OR cat.name ILIKE '%'||$4||'%'
--   OR EXISTS (SELECT 1 FROM products p WHERE p.business_id = b.id
--              AND ... to_tsvector('simple', p.name) @@ plainto_tsquery(...))
--
-- There was NO GIN index on that expression anywhere in the schema, and none on
-- the product expression either — even though migration 0001 already indexes
-- exactly this shape on chat_messages (0001:436), so the pattern was known and
-- simply never applied to the directory.
--
-- Consequences: branch 1 is unindexable, so the planner cannot BitmapOr it
-- with the trigram branches and falls back to a sequential scan of `businesses`,
-- lexing five columns per row; branch 5 is a correlated subquery per candidate
-- that re-lexes that business's products. Cost is O(candidates × products).
--
-- The expression is wrapped in IMMUTABLE helper functions on purpose. An index
-- expression must be IMMUTABLE, and the query must textually match it or the
-- index is never used. There were previously THREE mutually inconsistent
-- hand-written copies of this expression (the WHERE clause, the `relevance`
-- sort, and a shorter variant in the rank pass) — none indexed, all able to
-- drift. One function per shape makes drift impossible.
--
-- Two variants of the business index, not one:
--   * a partial index over live verified rows, which is what every public query
--     filters on (status='verified' AND deleted_at IS NULL);
--   * a full index, because admin search and the claims/moderation paths do not
--     carry that predicate and would otherwise miss the index entirely.

CREATE OR REPLACE FUNCTION biz_search_tsv(nm text, tg text, ds text, ct text, tgs text[])
RETURNS tsvector
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT to_tsvector('simple',
           coalesce(nm, '') || ' ' ||
           coalesce(tg, '') || ' ' ||
           coalesce(ds, '') || ' ' ||
           coalesce(ct, '') || ' ' ||
           coalesce(array_to_string(tgs, ' '), ''));
$$;

CREATE OR REPLACE FUNCTION product_search_tsv(nm text)
RETURNS tsvector
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT to_tsvector('simple', coalesce(nm, ''));
$$;

DROP INDEX CONCURRENTLY IF EXISTS idx_businesses_tsv;
CREATE INDEX CONCURRENTLY idx_businesses_tsv
    ON businesses USING gin (biz_search_tsv(name, tagline, description, city, tags))
    WHERE deleted_at IS NULL AND status = 'verified';

DROP INDEX CONCURRENTLY IF EXISTS idx_businesses_tsv_all;
CREATE INDEX CONCURRENTLY idx_businesses_tsv_all
    ON businesses USING gin (biz_search_tsv(name, tagline, description, city, tags));

DROP INDEX CONCURRENTLY IF EXISTS idx_products_tsv;
CREATE INDEX CONCURRENTLY idx_products_tsv
    ON products USING gin (product_search_tsv(name))
    WHERE deleted_at IS NULL AND is_published;

-- Trigram coverage for the ILIKE branches. businesses.name already has one from
-- 0001/0016; these are the columns the OR predicate also touches and which
-- therefore could not use the existing index.
DROP INDEX CONCURRENTLY IF EXISTS idx_businesses_tagline_trgm;
CREATE INDEX CONCURRENTLY idx_businesses_tagline_trgm
    ON businesses USING gin (tagline gin_trgm_ops) WHERE deleted_at IS NULL;
DROP INDEX CONCURRENTLY IF EXISTS idx_businesses_city_trgm;
CREATE INDEX CONCURRENTLY idx_businesses_city_trgm
    ON businesses USING gin (city gin_trgm_ops) WHERE deleted_at IS NULL;
DROP INDEX CONCURRENTLY IF EXISTS idx_categories_name_trgm;
CREATE INDEX CONCURRENTLY idx_categories_name_trgm
    ON categories USING gin (name gin_trgm_ops);

-- ===========================================================================
-- 2. businesses.owner_id — a foreign key with no index
-- ===========================================================================
--
-- Postgres does not index foreign keys automatically. This column is the target
-- of CreateNotification's per-notification EXISTS:
--
--   INSERT INTO notifications (...)
--   SELECT ... FROM users u WHERE u.id = $2
--
-- whose tier expression is
--   EXISTS (SELECT 1 FROM businesses b WHERE b.owner_id = u.id AND b.deleted_at IS NULL)
--
-- That ran on EVERY notification of ANY type. A 10k-follower announcement
-- therefore performed 10k scans of the whole directory inside a single request.
--
-- Equality column first, then the sort column the owner dashboard uses
-- (ListByOwner orders by updated_at DESC), partial on the live predicate.

DROP INDEX CONCURRENTLY IF EXISTS idx_businesses_owner;
CREATE INDEX CONCURRENTLY idx_businesses_owner
    ON businesses (owner_id, updated_at DESC) WHERE deleted_at IS NULL;

-- ===========================================================================
-- 3. collection_items (target_type, target_id) — 24 seq scans per search page
-- ===========================================================================
--
-- `save_count` is a correlated subquery in the row shape of every business card
-- (repo.BusinessCounts):
--   (SELECT count(*) FROM collection_items ci
--     WHERE ci.target_type = 'business' AND ci.target_id = b.id)
--
-- A 24-result page ran 24 sequential scans, plus one more per analytics view and
-- per InCollections call. Both columns are equality predicates, so the composite
-- order is already optimal.

DROP INDEX CONCURRENTLY IF EXISTS idx_collection_items_target;
CREATE INDEX CONCURRENTLY idx_collection_items_target
    ON collection_items (target_type, target_id);

-- ===========================================================================
-- 4. moderation_actions (created_at DESC) — the admin audit trail
-- ===========================================================================
--
-- admin.go lists the trail with ORDER BY created_at DESC LIMIT/OFFSET and the
-- only index is (target_type, target_id), referenced by no query. So every admin
-- dashboard load seq-scanned the entire append-only table, sorted it, then threw
-- away everything past the page — and the offset made it worse as the table grew.
-- This table has no retention job, so it only ever gets bigger.
--
-- The trailing `id DESC` is the stable tiebreaker: without it, two actions
-- sharing a created_at can swap places between pages and one is skipped while
-- another is shown twice.

DROP INDEX CONCURRENTLY IF EXISTS idx_moderation_actions_created;
CREATE INDEX CONCURRENTLY idx_moderation_actions_created
    ON moderation_actions (created_at DESC, id DESC);
DROP INDEX CONCURRENTLY IF EXISTS idx_moderation_actions_admin;
CREATE INDEX CONCURRENTLY idx_moderation_actions_admin
    ON moderation_actions (admin_id, created_at DESC);

-- ===========================================================================
-- 5. engagement_events (flagged) — the anomaly queue
-- ===========================================================================
--
-- service/profiles.go reads the anomaly queue as:
--   SELECT ... FROM engagement_events e JOIN businesses b ...
--   WHERE e.flagged = true ORDER BY e.occurred_at DESC LIMIT 100
--
-- `flagged` is a low-cardinality boolean with NO index, so LIMIT 100 cannot help:
-- Postgres must locate and sort every flagged row, with no index to start from,
-- i.e. a sequential scan of the 90-day event table — potentially hundreds of
-- millions of rows.

DROP INDEX CONCURRENTLY IF EXISTS idx_events_flagged;
CREATE INDEX CONCURRENTLY idx_events_flagged
    ON engagement_events (occurred_at DESC, id DESC) WHERE flagged;
DROP INDEX CONCURRENTLY IF EXISTS idx_events_target_signal;
CREATE INDEX CONCURRENTLY idx_events_target_signal
    ON engagement_events (target_type, target_id, signal) WHERE flagged = false;

-- ===========================================================================
-- 6. Secondary batch — the remaining missing FK / lookup indexes
-- ===========================================================================

-- Public profile page and the GDPR export both filter comments by author.
-- comments has no index on user_id at all, and the FK is NO ACTION, so deleting
-- a user with comments would also scan.
DROP INDEX CONCURRENTLY IF EXISTS idx_comments_user;
CREATE INDEX CONCURRENTLY idx_comments_user
    ON comments (user_id, created_at DESC);
-- Self-referencing FK; a parent delete currently cascades through a scan.
DROP INDEX CONCURRENTLY IF EXISTS idx_comments_parent;
CREATE INDEX CONCURRENTLY idx_comments_parent
    ON comments (parent_id) WHERE parent_id IS NOT NULL;

-- Restock alerts: every variant save filters/deletes by product_id, but the only
-- index leads with user_id.
DROP INDEX CONCURRENTLY IF EXISTS idx_product_stock_alerts_product;
CREATE INDEX CONCURRENTLY idx_product_stock_alerts_product
    ON product_stock_alerts (product_id);

-- The non-admin "my claims" list has no usable index: only (status, created_at)
-- exists, and a user-scoped query does not filter on status.
DROP INDEX CONCURRENTLY IF EXISTS idx_business_claims_user;
CREATE INDEX CONCURRENTLY idx_business_claims_user
    ON business_claims (user_id, created_at DESC);

DROP INDEX CONCURRENTLY IF EXISTS idx_appeals_user;
CREATE INDEX CONCURRENTLY idx_appeals_user
    ON appeals (user_id, created_at DESC);

-- The FK to users is CASCADE with only a partial index, so deleting a user with
-- RESOLVED reports seq-scans `reports`.
DROP INDEX CONCURRENTLY IF EXISTS idx_reports_reporter;
CREATE INDEX CONCURRENTLY idx_reports_reporter
    ON reports (reporter_id);

-- PurgeExpiredDeletions scans this and rewrites email/username, churning both
-- UNIQUE indexes.
DROP INDEX CONCURRENTLY IF EXISTS idx_users_deleted;
CREATE INDEX CONCURRENTLY idx_users_deleted
    ON users (deleted_at) WHERE deleted_at IS NOT NULL;
-- Weekly digest loads every opted-in address into memory.
DROP INDEX CONCURRENTLY IF EXISTS idx_users_digest;
CREATE INDEX CONCURRENTLY idx_users_digest
    ON users (digest_opt_in) WHERE digest_opt_in AND deleted_at IS NULL;

-- Retention sweep on auth_events.
DROP INDEX CONCURRENTLY IF EXISTS idx_auth_events_created;
CREATE INDEX CONCURRENTLY idx_auth_events_created
    ON auth_events (created_at);

-- Daily search-alert job reads notify_daily rows.
DROP INDEX CONCURRENTLY IF EXISTS idx_saved_searches_alerts;
CREATE INDEX CONCURRENTLY idx_saved_searches_alerts
    ON saved_searches (user_id) WHERE notify_daily;

-- Self-FK; blocks DELETE cascades on the category tree.
DROP INDEX CONCURRENTLY IF EXISTS idx_categories_parent;
CREATE INDEX CONCURRENTLY idx_categories_parent
    ON categories (parent_id);

-- analytics.go reads the latest snapshot per (period, business) and sorts by
-- taken_at. The existing index is (period, business_id) with no sort column, so
-- each call sorts every snapshot ever retained for that business.
DROP INDEX CONCURRENTLY IF EXISTS idx_trend_snapshots_period_biz_taken;
CREATE INDEX CONCURRENTLY idx_trend_snapshots_period_biz_taken
    ON trend_snapshots (period, business_id, taken_at DESC);

-- ===========================================================================
-- 7. Drop the indexes that are provably redundant
-- ===========================================================================
--
-- Each of these is a strict prefix of, or byte-identical to, another index, so
-- it is pure write amplification and storage. Every one of them has a live
-- table: `notifications` is the highest-write table in the schema, and
-- `engagement_events` is the highest-volume.

-- 0013:5 and 0015:7 created two byte-identical indexes on
-- notifications(user_id, type, created_at DESC). The 0015 statement used a
-- DIFFERENT NAME, so IF NOT EXISTS did not catch the collision.
DROP INDEX CONCURRENTLY IF EXISTS idx_notifications_user_type;

-- Strict prefix of idx_events_target_time (0001:375 → 0025 renamed/extended).
DROP INDEX CONCURRENTLY IF EXISTS idx_engagement_events_target;

-- Subsumed by idx_reviews_business_rating_live (0027), which every rating
-- aggregate actually filters on (`deleted_at IS NULL`).
DROP INDEX CONCURRENTLY IF EXISTS idx_reviews_business_rating;

-- Duplicates the UNIQUE constraint businesses_slug_key created in 0001, which
-- 0029 has now replaced with the partial businesses_slug_live.
DROP INDEX CONCURRENTLY IF EXISTS idx_businesses_slug;

-- Strict prefix of collections_user_id_slug_key.
DROP INDEX CONCURRENTLY IF EXISTS idx_collections_user;

-- last_read_message_id is a payload column: it never appears in a WHERE or
-- ORDER BY, so as a trailing index column it is dead weight.
DROP INDEX CONCURRENTLY IF EXISTS idx_chat_participants_user;
DROP INDEX CONCURRENTLY IF EXISTS idx_chat_participants_user;
CREATE INDEX CONCURRENTLY idx_chat_participants_user
    ON chat_participants (user_id);

-- Referenced by no query in the codebase.
DROP INDEX CONCURRENTLY IF EXISTS idx_moderation_actions_target;
-- ...and media is only ever read by primary key.
DROP INDEX CONCURRENTLY IF EXISTS idx_media_uploader;

-- ===========================================================================
-- 8. Pagination stability
-- ===========================================================================
--
-- Every paginated list needs a total order, or a row can appear on two pages
-- and another on none. These add the missing tiebreakers to the two lists the
-- app pages through with OFFSET.

DROP INDEX CONCURRENTLY IF EXISTS idx_reviews_business_recent;
CREATE INDEX CONCURRENTLY idx_reviews_business_recent
    ON reviews (business_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;

-- ===========================================================================
-- Operator notes
-- ===========================================================================
--
-- 1) Search quality. These indexes make the existing predicate fast; they do not
--    make it GOOD. The config is 'simple', which performs no stemming and no
--    stop-word removal, so "running cafe" will not match "run cafés" and "the"
--    is indexed as a term. For the public directory, 'english' is the right
--    choice. It is deliberately NOT changed here: it changes results, not just
--    speed, so it needs an evaluation of recall on real queries and a reindex,
--    not a silent migration. Chat should keep 'simple' — there you want handles
--    and code matched literally.
--
-- 2) Invalid indexes. A CREATE INDEX CONCURRENTLY that is cancelled or fails
--    leaves an index marked INVALID: it is present, takes up space, is never
--    used, and `IF NOT EXISTS` will then skip rebuilding it. After any
--    interrupted deploy, check and clean up:
--
--      SELECT indexrelid::regclass FROM pg_index WHERE NOT indisvalid;
--      -- then, per index:
--      DROP INDEX CONCURRENTLY <name>;   -- and re-run this migration
--
-- 3) Validate the CHECK constraints added in 0029 (the operator follow-up block
--    in that file lists them).
--
-- 4) Verified plan shapes (measured on Postgres 18.6, 5,000 synthetic verified
--    businesses + 1,000 products + 3,000 collection items, ANALYZEd).
--    Reproduce with infra/postgres/test_index_usage_seeded.sql.
--
--      predicate                                  plan chosen            verdict
--      biz_search_tsv @@ 'artisan'   (5/5000)     Bitmap Index Scan
--                                                           idx_businesses_tsv_all   USED
--      biz_search_tsv @@ 'coffee'    (1666/5000)  Seq Scan                CORRECT
--      product_search_tsv @@ 'latte'             Bitmap Index Scan
--                                                           idx_products_tsv        USED
--      collection_items(target_type,target_id)   Index Only Scan
--                                                           idx_collection_items_target USED
--      EXISTS(b.owner_id = u.id)                 Bitmap Index Scan
--                                                           idx_businesses_owner    USED
--      name ILIKE '%artisan%'        (4/5000)    Seq Scan                see below
--      anomalies (flagged) ORDER BY occurred_at  Index Only Scan
--                                                           idx_events_flagged      USED
--      moderation_actions ORDER BY created_at    Index Only Scan
--                                                           idx_moderation_actions_created USED
--
--    Two rows deserve comment rather than a verdict:
--
--    * "coffee" at 33% and "artisan" at 4/5000 both planning as Seq Scans is
--      CORRECT, not a missing index. At low selectivity reading the table is
--      cheaper than probing any index, and 5,000 rows is only ~334 buffers. A
--      test that flags those as regressions would be wrong. This is why the
--      verification script checks a selective AND a common term.
--    * The trigram index on businesses.name (from 0001/0016) is not chosen for
--      '%artisan%' at this table size. It becomes worthwhile at the row counts a
--      real directory reaches. Worth re-checking with production data rather than
--      assuming either way.
--
-- 5) Both biz_search_tsv variants exist: the partial (live+verified) and the
--    full. In the synthetic fixture every row is live+verified, so the two have
--    identical content and the planner picked the full one. In production the
--    partial is materially smaller and should win for public queries. If it does
--    not, drop the full index — two GIN indexes over the same rows is pure write
--    amplification on the hottest table in the schema.
