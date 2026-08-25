-- Performance indexes for observed hot predicates (2026-08 perf audit).
-- Each CREATE INDEX CONCURRENTLY runs OUTSIDE the migration transaction
-- (the migrator detects "INDEX CONCURRENTLY" and executes it on autocommit),
-- so large production tables never lock writes during the build.

-- Notification retention purge previously seq-scanned: nothing indexed
-- expires_at, and the DELETE ran unbatched.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_notifications_expires
  ON notifications (expires_at);

-- Category follower fan-out (announcements for category follows) filters by
-- category_id; UNIQUE(user_id, category_id) cannot serve it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_user_category_follows_category
  ON user_category_follows (category_id);

-- Map viewport searches use plain b.lat BETWEEN / b.lng BETWEEN; the GiST
-- index on ll_to_earth cannot serve raw coordinate range predicates.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_businesses_latlng
  ON businesses (lat, lng)
  WHERE deleted_at IS NULL AND status IN ('verified', 'paused');

-- Search + "similar" widgets always filter status='verified' AND
-- deleted_at IS NULL and usually a single category.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_businesses_cat_verified
  ON businesses (category_id, created_at DESC)
  WHERE deleted_at IS NULL AND status = 'verified';

-- Rating sort / min_rating filter aligned with soft-delete predicate so the
-- planner can avoid heap-fetching deleted reviews.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_reviews_business_rating_live
  ON reviews (business_id, rating)
  WHERE deleted_at IS NULL;
