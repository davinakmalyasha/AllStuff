-- 0034_subscriptions_dedupe.sql — collapse any pre-existing duplicate
-- subscription rows, so 0035 can add the UNIQUE that `UpsertSubscription` needs.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        none
-- migrate:risk        DML
-- migrate:note        Keeps the newest row per business by (updated_at, created_at, id) and then asserts zero duplicates remain. The DELETE is idempotent: a second run finds one row per business and removes nothing.
--
-- WHY THIS EXISTS
-- --------------
-- 0028 created this index:
--
--   CREATE INDEX IF NOT EXISTS idx_subscriptions_business
--       ON subscriptions (business_id);
--
-- and its own comment says "one active-or-past row per business ... so this is
-- effectively a business_id lookup". The intent was uniqueness; the statement
-- delivered a plain non-unique btree.
--
-- repo/billing.go then does:
--
--   INSERT INTO subscriptions (...) VALUES (...)
--   ON CONFLICT (business_id) DO UPDATE SET ...
--
-- Postgres matches an ON CONFLICT inference clause against UNIQUE indexes and
-- unique constraints ONLY. There is none on (business_id), so every write raises
--
--   ERROR: there is no unique or exclusion constraint matching the
--          ON CONFLICT specification
--   SQLSTATE 42P10
--
-- Three call sites, all of them the paid path:
--
--   service/billing.go  customer.subscription.created  -> billing.go:494
--   service/billing.go  customer.subscription.updated  -> billing.go:494
--   service/billing.go  customer.subscription.deleted  -> billing.go:534
--   service/billing.go  Reconcile (6-hourly job)        -> billing.go:640
--
-- WHY IT NEVER SURFACED
-- --------------------
-- HandleWebhook (billing.go:398-414) claims the event in
-- billing_webhook_events BEFORE dispatching. So on every delivery:
--
--   1. ledger row inserted
--   2. handler raises 42P10
--   3. MarkWebhookFailed, return an error
--   4. Stripe retries
--   5. ClaimWebhookEvent returns fresh=false (the PK conflicts), so the handler
--      is NOT re-run, and the endpoint returns 200
--
-- The event is permanently dead. processed_at stays NULL forever, and
-- idx_billing_events_unprocessed lists it as operator-actionable work that can
-- never drain.
--
-- The blast radius: effectivePlan (billing.go:70-101) falls through to the free
-- plan for every business, so product_limit, gallery_limit and team_seats are
-- permanently the free-plan values and every paid entitlement is dead code.
-- Billing breaks the moment an operator fills in plans.stripe_price_id and a
-- real customer checks out.
--
-- WHY TWO FILES
-- -------------
-- Duplicates may already exist (any manual insert, a pg_restore, a
-- Reconcile that ran against a schema where the upsert failed differently).
-- Adding UNIQUE on a table with duplicates fails, and fails at CREATE INDEX
-- time — i.e. on autocommit, outside the migration transaction, leaving an
-- INVALID index behind. So the data is fixed first, here, and the constraint
-- is added second, in 0035.
--
-- Idempotent: DELETE removes at most the duplicates; on replay it matches
-- nothing. The assertion either passes or stops the boot before 0035 runs.
--
-- NON-GOALS
-- ---------
-- This does not reconcile subscription state against Stripe. That is what the
-- Reconcile job (billing.go:640) is for, and it is what will repopulate the
-- table once 0035 makes its upsert work.

-- ---------------------------------------------------------------------------
-- 1. Keep the newest row per business.
--
-- The ORDER BY is TOTAL: updated_at, then created_at, then id. Without the id
-- tiebreak two rows written in the same statement would be ordered
-- arbitrarily and the DELETE would pick a different survivor on each replay.
-- ---------------------------------------------------------------------------
WITH ranked AS (
    SELECT s.id,
           row_number() OVER (
               PARTITION BY s.business_id
               ORDER BY s.updated_at DESC, s.created_at DESC, s.id
           ) AS rn
      FROM subscriptions s
)
DELETE FROM subscriptions s
 USING ranked r
 WHERE s.id = r.id
   AND r.rn > 1;

-- ---------------------------------------------------------------------------
-- 2. Assert the invariant that 0035 depends on.
--
-- A DO block is a documented escape hatch (migrate_meta.go counts them rather
-- than pretending to inspect the plpgsql body). This one is short enough to be
-- obviously correct: it counts the businesses that still have more than one row
-- and raises if the number is non-zero.
--
-- If this fires, the operator has a row the ORDER BY could not rank — a NULL
-- updated_at, most likely, since updated_at is NOT NULL DEFAULT now() but a
-- hand-written INSERT can leave it out and NULL sorts last in DESC order.
-- Repair by hand and re-run; do NOT weaken the assertion.
-- ---------------------------------------------------------------------------
DO $chk$
DECLARE
    dupes int;
BEGIN
    SELECT count(*) INTO dupes
      FROM (SELECT business_id
              FROM subscriptions
             GROUP BY business_id
            HAVING count(*) > 1) d;

    IF dupes > 0 THEN
        RAISE EXCEPTION
            '0034: % business(es) still have more than one subscription row after de-duplication. Resolve by hand and re-run; 0035 cannot build the UNIQUE until this is zero.',
            dupes;
    END IF;
END
$chk$;