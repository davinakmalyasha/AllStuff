-- Batch 7: performance indexes (gap analysis): correlated-subquery hot spots.

CREATE INDEX IF NOT EXISTS idx_reviews_business_rating ON reviews (business_id, rating);
CREATE INDEX IF NOT EXISTS idx_likes_target ON likes (target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_recommends_business ON recommends (business_id);
CREATE INDEX IF NOT EXISTS idx_comments_business ON comments (business_id, created_at);
CREATE INDEX IF NOT EXISTS idx_notifications_user_type ON notifications (user_id, type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_target_time ON engagement_events (target_type, target_id, occurred_at DESC);
