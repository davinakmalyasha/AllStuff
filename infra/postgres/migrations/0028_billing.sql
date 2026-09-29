-- 0028_billing.sql — monetization (Phase 7.1).
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        plans
-- migrate:risk        MIXED
-- migrate:note        Targeted DO UPDATE on plans. The seed owns name, description, entitlements and sort_order; it deliberately does NOT own price_cents, currency or stripe_price_id, so a replay can never clear a live Stripe price id out from under active subscriptions.
--
-- Design notes:
--  * `plans` is a catalogue table, not config: entitlements are read on hot
--    paths (every product create, gallery add and co-owner invite resolves
--    them), so they are relational and joinable rather than a `site_config`
--    jsonb blob.
--  * Entitlements are stored as a text[] and resolved in Go, so adding a
--    capability never needs a migration. An entitlement that is neither a
--    known boolean nor a `name:N` limit is IGNORED by the resolver rather than
--    rejected, so a rolling deploy with an old binary cannot 500.
--  * `subscriptions` is unique per business so a webhook retry is idempotent at
--    the row level, independent of the event ledger.
--  * `billing_webhook_events` is the idempotency ledger. Stripe retries a
--    delivery until it gets a 2xx, and it can also deliver the same event id
--    twice. The PRIMARY KEY on provider_event_id makes the handler safe to
--    replay unconditionally.
--  * Every FK column is indexed (see 0030's notes; Postgres does not do this
--    for you and the 0029 audit turned up many that were missing).
--
-- Idempotent: safe to replay after a crash between COMMIT and the version
-- marker (db/migrate.go records the marker outside the transaction).

-- ---------------------------------------------------------------------------
-- plans
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS plans (
    id              text PRIMARY KEY,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    -- Monthly price in the smallest currency unit (cents). 0 = free.
    price_cents     int NOT NULL DEFAULT 0 CHECK (price_cents >= 0),
    currency        char(3) NOT NULL DEFAULT 'USD' CHECK (currency ~ '^[A-Z]{3}$'),
    -- Stripe price id. NULL for the free plan (no checkout needed).
    stripe_price_id text,
    -- Capability list resolved by service.Billing.Entitlements.
    entitlements    text[] NOT NULL DEFAULT '{}',
    -- Marketing display order on /pricing.
    sort_order      int NOT NULL DEFAULT 0,
    -- Free plans cannot be purchased; the guard is CHECK not code so a bad
    -- INSERT cannot create an unbuyable-but-priced row.
    is_active       boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    -- A FREE plan must not carry a Stripe price: it would be purchasable for
    -- nothing and the webhook would try to activate a $0 subscription.
    --
    -- The converse is deliberately ALLOWED: a paid plan with no
    -- stripe_price_id is a normal intermediate state (listed on /pricing, not
    -- yet for sale). The operator fills it in with
    --   UPDATE plans SET stripe_price_id = 'price_...' WHERE id = 'growth';
    -- and until then BillingPlan.Purchasable() is false and checkout refuses
    -- with "This plan is not available for purchase yet" rather than failing
    -- inside Stripe. A two-way CHECK here would have made the seeded catalogue
    -- unappliable.
    CONSTRAINT plans_free_has_no_price CHECK (
        price_cents <> 0 OR stripe_price_id IS NULL
    )
);

-- ---------------------------------------------------------------------------
-- subscriptions — one active-or-past row per business
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS subscriptions (
    id                     uuid PRIMARY KEY,
    business_id            uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    plan_id                text NOT NULL REFERENCES plans(id),
    -- Denormalized from plans for display without a join on the owner dashboard.
    price_cents            int NOT NULL DEFAULT 0 CHECK (price_cents >= 0),
    currency               char(3) NOT NULL DEFAULT 'USD' CHECK (currency ~ '^[A-Z]{3}$'),
    -- Stripe's identifiers. customer_id is set as soon as checkout completes;
    -- subscription_id is NULL while a checkout session is merely open.
    stripe_customer_id     text,
    stripe_subscription_id text,
    -- tri-state mirroring Stripe. 'incomplete' means the first payment has not
    -- settled; entitlements must NOT be granted for it.
    status                 text NOT NULL DEFAULT 'incomplete'
                           CHECK (status IN ('incomplete','active','past_due','canceled','unpaid','paused','trialing')),
    cancel_at_period_end   boolean NOT NULL DEFAULT false,
    current_period_start   timestamptz,
    current_period_end     timestamptz,
    canceled_at            timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    -- Stripe ids are unique when present so a webhook cannot attach two
    -- businesses to one Stripe subscription.
    CONSTRAINT subscriptions_stripe_sub_key UNIQUE (stripe_subscription_id),
    CONSTRAINT subscriptions_stripe_customer_key UNIQUE (stripe_customer_id),
    -- An active/past_due/trialing row must reference a Stripe subscription.
    CONSTRAINT subscriptions_needs_stripe_sub CHECK (
        status IN ('incomplete','canceled') OR stripe_subscription_id IS NOT NULL
    )
);

-- The hot lookup is "subscription for this business" — one row per business,
-- so this is effectively a business_id lookup. Keeping the explicit index
-- rather than relying on the UNIQUE constraints above (which lead with
-- stripe ids) avoids a sequential scan on the owner dashboard.
CREATE INDEX IF NOT EXISTS idx_subscriptions_business ON subscriptions (business_id);
-- Reconciliation job: find rows whose current_period_end has passed while
-- still marked active (i.e. the webhook never arrived).
CREATE INDEX IF NOT EXISTS idx_subscriptions_period_end ON subscriptions (current_period_end)
    WHERE status IN ('active', 'trialing', 'past_due');

-- ---------------------------------------------------------------------------
-- invoices — display-only history; Stripe is the system of record
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS invoices (
    id                     uuid PRIMARY KEY,
    business_id            uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    subscription_id        uuid REFERENCES subscriptions(id) ON DELETE SET NULL,
    stripe_invoice_id      text NOT NULL UNIQUE,
    number                 text,
    status                 text,
    amount_cents           int NOT NULL DEFAULT 0,
    currency               char(3) NOT NULL DEFAULT 'USD' CHECK (currency ~ '^[A-Z]{3}$'),
    hosted_invoice_url     text,
    invoice_pdf_url        text,
    -- Denormalized billing period so history survives plan changes.
    period_start           timestamptz,
    period_end             timestamptz,
    paid_at                timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_invoices_business ON invoices (business_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- billing_webhook_events — idempotency ledger
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS billing_webhook_events (
    provider_event_id text PRIMARY KEY,
    type              text NOT NULL,
    -- Resource the event concerns, for operator forensics.
    object_id         text,
    -- Raw body retained for replay/debugging. Stripe payloads are small and
    -- non-PII beyond ids; retention is bounded by the purge in jobs.
    payload           jsonb NOT NULL DEFAULT '{}'::jsonb,
    received_at       timestamptz NOT NULL DEFAULT now(),
    processed_at      timestamptz,
    -- Non-null once handled; an error leaves processed_at NULL so the event is
    -- visible as unprocessed rather than silently dropped.
    error             text
);

CREATE INDEX IF NOT EXISTS idx_billing_events_unprocessed
    ON billing_webhook_events (received_at) WHERE processed_at IS NULL;

-- ---------------------------------------------------------------------------
-- seed the catalogue
--
-- WHAT A PLAN MAY AND MAY NOT SELL
-- ---------------------------------------------------------------------------
-- This is the whole commercial model, so it is worth stating the rule rather
-- than leaving it to whoever edits the array next.
--
-- A plan may sell CAPACITY: how many products a business may publish, how many
-- gallery photos, how many co-owner seats, API access, analytics depth, and
-- support priority. Capacity is bounded by what the operator actually pays for
-- in infrastructure, and no user is worse off when one business has a larger
-- catalogue than another.
--
-- A plan may NOT sell TRUST, and it may NOT sell PLACEMENT.
--
--   * `verified_badge` is gone. Verification is an admin decision made after
--     reviewing registration documents, and the two levels exist to mean
--     something. Putting a paid badge on that ladder makes the badge
--     meaningless, and it is the single thing a directory's users trust most.
--     It also contradicts the product's own stated rule that owners own their
--     content and admins verify it.
--   * `featured_placement` is gone. Featured is an admin-curated editorial slot
--     at /admin/curation, and the product's stated non-goal is "ads or promoted
--     listings". A paid slot is a promoted listing with extra steps.
--
-- An earlier revision of this seed sold both. It was the correct call to remove
-- them rather than to remove monetisation, because the parts worth keeping are
-- the parts that do not touch trust: the Stripe client, the
-- billing_webhook_events idempotency ledger, and the six-hourly reconciliation
-- job that stops a dropped webhook from silently revoking a paid plan.
--
-- Entitlements (resolved in service.Billing.Entitlements):
--   analytics            — owner analytics dashboard
--   analytics_advanced   — funnels, response time, trend scores
--   api_access           — /api/v2 read API keys
--   product_limit:N      — published product cap (N per business)
--   gallery_limit:N      — storefront gallery photo cap
--   team_seats:N         — co-owner + viewer invitations
--   support_priority     — faster help-center response
--
-- Values are intentionally strings so the resolver never needs a migration
-- when a new capability is added. An unknown value is IGNORED by the resolver
-- rather than rejected, so a rolling deploy with an old binary cannot 500.
--
-- NOTE: `webhooks` and `embeddable_widget` were in the Pro plan while neither
-- feature existed. They come back when the migrations that build them land;
-- until then a plan must not advertise a capability the code cannot honour.
-- ---------------------------------------------------------------------------
INSERT INTO plans (id, name, description, price_cents, currency, stripe_price_id, entitlements, sort_order)
VALUES
    ('free', 'Free',
     'Everything a customer needs to find and contact a business.',
     0, 'USD', NULL,
     ARRAY['analytics','api_access','product_limit:5','gallery_limit:3','team_seats:1'],
     0),

    ('growth', 'Growth',
     'More catalogue, more gallery photos, and more people on your team.',
     1900, 'USD', NULL,
     ARRAY['analytics','analytics_advanced','api_access','product_limit:50',
           'gallery_limit:10','team_seats:5'],
     10),

    ('pro', 'Pro',
     'A large catalogue and a large team, with priority support.',
     4900, 'USD', NULL,
     ARRAY['analytics','analytics_advanced','api_access','product_limit:500',
           'gallery_limit:30','team_seats:25','support_priority'],
     20)
-- Only the marketing/description fields are refreshed on replay. price_cents,
-- currency and stripe_price_id are OPERATOR-owned: this file must never be able
-- to reset a price the operator has configured, or worse, clear the Stripe
-- price id out from under live subscriptions.
ON CONFLICT (id) DO UPDATE SET
    name            = EXCLUDED.name,
    description     = EXCLUDED.description,
    entitlements    = EXCLUDED.entitlements,
    sort_order      = EXCLUDED.sort_order,
    updated_at      = now();

-- NOTE: stripe_price_id is intentionally NULL in the seed. A plan row with a
-- price but no stripe_price_id is rejected by the plans_free_has_no_price CHECK,
-- so the operator must fill it in before selling:
--
--   UPDATE plans SET stripe_price_id = 'price_...' WHERE id = 'growth';
--
-- Checkout refuses to start for a plan whose stripe_price_id is NULL rather
-- than failing inside Stripe (see service/billing.go: StartCheckout).
