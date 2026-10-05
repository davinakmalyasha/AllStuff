-- 0035_subscriptions_business_uniq.sql — give `subscriptions` the UNIQUE on
-- (business_id) that `UpsertSubscription`'s ON CONFLICT clause requires.
--
-- migrate:idempotent  yes
-- migrate:concurrent  true
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        Drop-paired: an interrupted concurrent build is discarded and rebuilt on the next run rather than left INVALID, because IF NOT EXISTS compares names only and never looks at indisvalid. 0034 guarantees the data already satisfies the constraint.
--
-- WHY THIS EXISTS
-- --------------
-- See 0034 for the full account. The short version: 0028 built a PLAIN index on
-- (business_id), repo/billing.go upserts with `ON CONFLICT (business_id)`, and
-- Postgres resolves that inference against unique indexes only. Every paid
-- subscription write raised SQLSTATE 42P10 and was then permanently swallowed by
-- the idempotency ledger.
--
-- This file builds the index that was always meant to exist. It replaces
-- idx_subscriptions_business rather than adding alongside it, because the new
-- index has the same leading column and therefore the same access path: the old
-- one becomes dead weight, and every extra index on the table is write
-- amplification on the exact row that a Stripe webhook updates.
--
-- WHY THIS IS A SEPARATE FILE
-- ---------------------------
-- 0034 fixes the data; this adds the constraint. CREATE INDEX CONCURRENTLY
-- cannot run inside a transaction, so this file's statement is routed to
-- autocommit (db/migrate.go) and its version marker is written after the
-- statement, not after a COMMIT. Keeping the data fix in a transactional file
-- means a failure there rolls back cleanly and 0035 never runs; keeping them
-- together would mean a duplicate-row failure happened on autocommit and left an
-- INVALID index behind.
--
-- Idempotent: DROP ... IF EXISTS then CREATE. A replay costs one concurrent
-- index build; skipping it costs the index, and with it all of billing.

-- ---------------------------------------------------------------------------
-- Both names are dropped, for two different reasons.
--
-- idx_subscriptions_business is the plain btree 0028 created. It is dropped
-- because the new index has the same leading column and therefore the same
-- access path: "subscription for this business" (repo/billing.go
-- GetSubscriptionForBusiness, and the owner dashboard) is served by either, so
-- keeping both would be write amplification on the exact row a Stripe webhook
-- updates.
--
-- subscriptions_business_uniq is dropped because THIS FILE CREATES IT, and the
-- drop has to precede the build under the same name. That is the invariant
-- TestMigrationsDeclareTheirIdempotencyContract enforces, and it enforces it
-- correctly: IF NOT EXISTS compares names only and never looks at
-- pg_index.indisvalid, so a concurrent build interrupted mid-flight leaves an
-- INVALID index that every later replay silently accepts. Dropping first makes
-- the replay self-healing - it costs one index rebuild, where skipping costs
-- the index and with it all of billing.
-- ---------------------------------------------------------------------------
DROP INDEX CONCURRENTLY IF EXISTS idx_subscriptions_business;
DROP INDEX CONCURRENTLY IF EXISTS subscriptions_business_uniq;

CREATE UNIQUE INDEX CONCURRENTLY subscriptions_business_uniq
    ON subscriptions (business_id);

-- GetSubscriptionForBusiness has no LIMIT, and it only ever became correct by
-- the uniqueness this file adds. A note here so the next reader does not
-- "optimise" it by adding a LIMIT: there is provably at most one row.
COMMENT ON INDEX subscriptions_business_uniq IS
    'One subscription row per business. Required by repo/billing.go UpsertSubscription, which uses ON CONFLICT (business_id); without UNIQUE that upsert raises 42P10. See 0034 for why this was missed.';