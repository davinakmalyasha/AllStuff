-- 0033_total_order_on_paginated_lists.sql — give every OFFSET-paginated list a
-- total order, by extending the indexes that already carry the sort key.
--
-- migrate:idempotent  yes
-- migrate:concurrent  true
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        Every index is preceded by DROP INDEX CONCURRENTLY IF EXISTS of the same name, so an interrupted build is discarded and rebuilt on the next run rather than left INVALID.
--
-- WHY THIS EXISTS
-- --------------
-- 0031 already stated the rule, in its own words:
--
--   "Every paginated list needs a total order, or a row can appear on two pages
--    and another on none."
--
-- and acted on it for two lists, building
--
--   idx_reviews_business_recent (business_id, created_at DESC, id DESC)
--   idx_moderation_actions_created (created_at DESC, id DESC)
--
-- The index work was done; the QUERIES were not. ListReviews still orders by
-- `r.created_at DESC` alone, so a `id DESC` sitting at the end of the index is
-- never requested and never used. A total order that the query does not ask for
-- is not a total order.
--
-- The failure is user-visible. Postgres does not guarantee an order for rows
-- that tie on the sort key, and it picks a different one per execution because
-- the plan changes with LIMIT/OFFSET. Ties are not rare here: scripts/seed.sql
-- inserts rows with generate_series in a single statement, so `now()` is
-- constant across all of them, and any bulk import or backfill behaves the same
-- way. Paging 12 rows in pages of 4 can therefore show one business on page 1
-- and again on page 2 while a twelfth is never shown at all. Every individual
-- page was a perfectly valid result for its own query, so nothing reports an
-- error anywhere.
--
-- The remaining fix, `GetLatestUnread`, turned out to be DEAD CODE, and worth
-- recording because the way it was found is the point.
--
-- It was `ORDER BY created_at DESC LIMIT 1`, documented as "used after bulk
-- fan-out inserts to hydrate WS/email dispatches" — and that documentation was
-- simply wrong. The bulk fan-out had already been rewritten to
-- `INSERT … RETURNING` (see CreateNotificationsForFollowers, whose own comment
-- records that the "previous GetLatestUnread hydration could surface an older
-- unread notification"). So there was no live bug here, and an audit that trusted
-- the doc comment would have reported one.
--
-- A stale comment on unreachable code is still a defect, because it is what a
-- reader trusts. The comment is now accurate and the tiebreaker added, so the
-- "newest" contract holds if anything ever calls it. `idx_notifications_type` is
-- extended for it and for the type-filtered read path; the cost is 8 bytes a row
-- on a table that is already indexed four ways.
--
-- WHY INDEXES CHANGE AT ALL
-- -------------------------
-- The obvious patch — append `, id DESC` to the ORDER BY and touch nothing
-- else — would be a performance regression hiding behind a correctness fix.
-- Every one of these ORDER BY clauses exactly matches its index today, so the
-- read is index-ordered. Add a column the index does not have and Postgres can
-- no longer produce that order from the index; it falls back to sorting the
-- whole match set and then discarding all but LIMIT rows, on lists that are
-- currently the cheapest reads in the product.
--
-- So each index is extended by the same tiebreaker the query will request, which
-- keeps the index-ordered plan AND makes the order total. Note the direction
-- care: a DESC index scanned backwards yields ASC, and vice versa, so
-- idx_comments_recent (created_at DESC, id DESC) read backwards gives exactly
-- the ASC/ASC order ListComments asks for.
--
-- DELIBERATELY NOT CHANGED
-- ------------------------
-- * `idx_reviews_business_recent` already carries `id DESC`. Nothing to do; the
--   index was right and the query was wrong.
--
-- * No index for the admin verification queue. `BusinessRepo.ByStatus` filters
--   `status = ANY($1)`, which is a multi-value comparison, so an index on
--   (status, updated_at, id) cannot yield a single total order across the array
--   — Postgres would still have to merge and sort. The query already sorts
--   today; adding a tiebreaker to it costs nothing, and adding the index would
--   be storage that never gets used for ordering. It is an admin-volume query.
--
-- * `ListUserReviews` filters only `user_id`, for which the only index is
--   `idx_reviews_user (user_id)`. It already sorts, so it already pays for the
--   whole match set; a tiebreaker is free and an index is out of proportion.

-- ---------------------------------------------------------------------------
-- 1. questions: ListQuestions orders by created_at DESC with OFFSET.
-- ---------------------------------------------------------------------------
/* autocommit */ DROP INDEX CONCURRENTLY IF EXISTS idx_questions_business;
CREATE INDEX CONCURRENTLY idx_questions_business
    ON questions (business_id, created_at DESC, id DESC);

-- ---------------------------------------------------------------------------
-- 2. comments: ListComments orders by created_at ASC with OFFSET, served by a
--    backward scan of this index.
-- ---------------------------------------------------------------------------
/* autocommit */ DROP INDEX CONCURRENTLY IF EXISTS idx_comments_recent;
CREATE INDEX CONCURRENTLY idx_comments_recent
    ON comments (business_id, created_at DESC, id DESC);

-- ---------------------------------------------------------------------------
-- 3. notifications by type: GetLatestUnread (LIMIT 1) and the type-filtered
--    read path. 0013 owns this name; 0015 created a byte-identical duplicate
--    under a different name, which 0031 dropped.
-- ---------------------------------------------------------------------------
/* autocommit */ DROP INDEX CONCURRENTLY IF EXISTS idx_notifications_type;
CREATE INDEX CONCURRENTLY idx_notifications_type
    ON notifications (user_id, type, created_at DESC, id DESC);

-- ---------------------------------------------------------------------------
-- 4. notifications by read state: ListNotifications, which does NOT filter
--    is_read but is the unfiltered-by-type listing.
-- ---------------------------------------------------------------------------
/* autocommit */ DROP INDEX CONCURRENTLY IF EXISTS idx_notifications_user;
CREATE INDEX CONCURRENTLY idx_notifications_user
    ON notifications (user_id, is_read, created_at DESC, id DESC);
