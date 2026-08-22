-- Batch 1: typo tolerance — trigram similarity for the search name fallback
-- (PRD §5.1.2), plus the index that makes the % operator fast.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_businesses_name_trgm ON businesses USING gin (name gin_trgm_ops);
