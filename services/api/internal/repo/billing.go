package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// BillingRepo — plans catalogue, subscription state, invoice history and the
// webhook idempotency ledger (Phase 7.1, migration 0028).
type BillingRepo struct{ pool pooler }

// BillingPlan mirrors a row of `plans`.
type BillingPlan struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	PriceCents    int      `json:"price_cents"`
	Currency      string   `json:"currency"`
	StripePriceID *string  `json:"-"`
	Entitlements  []string `json:"entitlements"`
	SortOrder     int      `json:"sort_order"`
	IsActive      bool     `json:"is_active"`
}

// Purchasable reports whether checkout can be started for this plan.
func (p *BillingPlan) Purchasable() bool { return p.StripePriceID != nil && *p.StripePriceID != "" }

// Subscription mirrors a row of `subscriptions`.
type Subscription struct {
	ID                 string     `json:"id"`
	BusinessID         string     `json:"business_id"`
	PlanID             string     `json:"plan_id"`
	PlanName           string     `json:"plan_name,omitempty"`
	PriceCents         int        `json:"price_cents"`
	Currency           string     `json:"currency"`
	StripeCustomerID   *string    `json:"-"`
	StripeSubscription *string    `json:"-"`
	Status             string     `json:"status"`
	CancelAtPeriodEnd  bool       `json:"cancel_at_period_end"`
	CurrentPeriodStart *time.Time `json:"current_period_start,omitempty"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end,omitempty"`
	CanceledAt         *time.Time `json:"canceled_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// GrantsEntitlements reports whether the subscription state is one that should
// confer paid capabilities.
//
// This is the single place that decides "is this business paying?". It is a
// strict allowlist rather than a denylist so a status Stripe adds later
// (e.g. "paused" semantics changing) cannot silently grant access.
func (s *Subscription) GrantsEntitlements() bool {
	if s == nil {
		return false
	}
	switch s.Status {
	case "active", "trialing":
		return true
	case "past_due":
		// Grace period: Stripe retries. Keeping access here avoids punishing
		// a customer for a card that is about to succeed, and the period_end
		// index in 0028 exists to catch rows that never resolve.
		return true
	default:
		return false
	}
}

// Invoice mirrors a row of `invoices`.
type Invoice struct {
	ID               string     `json:"id"`
	BusinessID       string     `json:"business_id"`
	SubscriptionID   *string    `json:"subscription_id,omitempty"`
	Number           *string    `json:"number,omitempty"`
	Status           *string    `json:"status,omitempty"`
	AmountCents      int        `json:"amount_cents"`
	Currency         string     `json:"currency"`
	HostedInvoiceURL *string    `json:"hosted_invoice_url,omitempty"`
	InvoicePDFURL    *string    `json:"invoice_pdf_url,omitempty"`
	PeriodStart      *time.Time `json:"period_start,omitempty"`
	PeriodEnd        *time.Time `json:"period_end,omitempty"`
	PaidAt           *time.Time `json:"paid_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

const planColumns = `id, name, description, price_cents, currency, stripe_price_id, entitlements, sort_order, is_active`

func scanPlan(row pgx.Row) (*BillingPlan, error) {
	var p BillingPlan
	if err := row.Scan(&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.Currency,
		&p.StripePriceID, &p.Entitlements, &p.SortOrder, &p.IsActive); err != nil {
		return nil, err
	}
	return &p, nil
}

// ListPlans returns the active catalogue in marketing order. The whole
// catalogue is 3 rows today and is read on every /pricing render, so it is not
// worth a cache tier yet.
func (r *BillingRepo) ListPlans(ctx context.Context) ([]*BillingPlan, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+planColumns+`
		FROM plans WHERE is_active ORDER BY sort_order, price_cents`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*BillingPlan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlan returns a single plan or nil when it does not exist / is inactive.
func (r *BillingRepo) GetPlan(ctx context.Context, id string) (*BillingPlan, error) {
	p, err := scanPlan(r.pool.QueryRow(ctx, `SELECT `+planColumns+`
		FROM plans WHERE id = $1 AND is_active`, id))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

const subscriptionColumns = `s.id, s.business_id, s.plan_id, COALESCE(p.name, ''),
	s.price_cents, s.currency, s.stripe_customer_id, s.stripe_subscription_id, s.status,
	s.cancel_at_period_end, s.current_period_start, s.current_period_end, s.canceled_at,
	s.created_at, s.updated_at`

func scanSubscription(row pgx.Row) (*Subscription, error) {
	var s Subscription
	if err := row.Scan(&s.ID, &s.BusinessID, &s.PlanID, &s.PlanName, &s.PriceCents, &s.Currency,
		&s.StripeCustomerID, &s.StripeSubscription, &s.Status, &s.CancelAtPeriodEnd,
		&s.CurrentPeriodStart, &s.CurrentPeriodEnd, &s.CanceledAt,
		&s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetSubscriptionForBusiness returns the business's subscription, or nil.
// Every owner-dashboard billing read funnels through here.
func (r *BillingRepo) GetSubscriptionForBusiness(ctx context.Context, businessID string) (*Subscription, error) {
	s, err := scanSubscription(r.pool.QueryRow(ctx, `SELECT `+subscriptionColumns+`
		FROM subscriptions s LEFT JOIN plans p ON p.id = s.plan_id
		WHERE s.business_id = $1`, businessID))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// GetSubscriptionByStripeID is the webhook's row lookup.
func (r *BillingRepo) GetSubscriptionByStripeID(ctx context.Context, stripeSubID string) (*Subscription, error) {
	if stripeSubID == "" {
		return nil, nil
	}
	s, err := scanSubscription(r.pool.QueryRow(ctx, `SELECT `+subscriptionColumns+`
		FROM subscriptions s LEFT JOIN plans p ON p.id = s.plan_id
		WHERE s.stripe_subscription_id = $1`, stripeSubID))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// UpsertSubscription writes subscription state keyed on business_id.
//
// A webhook for a brand-new business arrives with only the stripe
// subscription id (and metadata), so the row is created on demand. The ON
// CONFLICT makes a redelivery a no-op rather than a unique violation, and
// COALESCE keeps a nullable field that this particular event did not carry
// from being clobbered with NULL.
func (r *BillingRepo) UpsertSubscription(ctx context.Context, s *Subscription) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO subscriptions (
			id, business_id, plan_id, price_cents, currency,
			stripe_customer_id, stripe_subscription_id, status,
			cancel_at_period_end, current_period_start, current_period_end, canceled_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (business_id) DO UPDATE SET
			plan_id                = EXCLUDED.plan_id,
			price_cents            = EXCLUDED.price_cents,
			currency               = EXCLUDED.currency,
			stripe_customer_id     = COALESCE(EXCLUDED.stripe_customer_id, subscriptions.stripe_customer_id),
			stripe_subscription_id = COALESCE(EXCLUDED.stripe_subscription_id, subscriptions.stripe_subscription_id),
			status                 = EXCLUDED.status,
			cancel_at_period_end   = EXCLUDED.cancel_at_period_end,
			current_period_start   = COALESCE(EXCLUDED.current_period_start, subscriptions.current_period_start),
			current_period_end     = COALESCE(EXCLUDED.current_period_end, subscriptions.current_period_end),
			canceled_at            = EXCLUDED.canceled_at,
			updated_at             = now()`,
		s.ID, s.BusinessID, s.PlanID, s.PriceCents, s.Currency,
		s.StripeCustomerID, s.StripeSubscription, s.Status, s.CancelAtPeriodEnd,
		s.CurrentPeriodStart, s.CurrentPeriodEnd, s.CanceledAt)
	return err
}

// ListInvoices returns billing history for a business, newest first.
func (r *BillingRepo) ListInvoices(ctx context.Context, businessID string, limit int) ([]*Invoice, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, business_id, subscription_id, number, status, amount_cents, currency,
		       hosted_invoice_url, invoice_pdf_url, period_start, period_end, paid_at, created_at
		FROM invoices WHERE business_id = $1 ORDER BY created_at DESC LIMIT $2`, businessID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Invoice{}
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(&inv.ID, &inv.BusinessID, &inv.SubscriptionID, &inv.Number, &inv.Status,
			&inv.AmountCents, &inv.Currency, &inv.HostedInvoiceURL, &inv.InvoicePDFURL,
			&inv.PeriodStart, &inv.PeriodEnd, &inv.PaidAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &inv)
	}
	return out, rows.Err()
}

// UpsertInvoice is idempotent on stripe_invoice_id, which has a UNIQUE
// constraint — Stripe sends invoice.created then invoice.paid for the same
// invoice, and the second must update the first.
func (r *BillingRepo) UpsertInvoice(ctx context.Context, inv *Invoice, stripeSubID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO invoices (
			id, business_id, subscription_id, stripe_invoice_id, number, status,
			amount_cents, currency, hosted_invoice_url, invoice_pdf_url,
			period_start, period_end, paid_at)
		VALUES ($1,$2,
			(SELECT id FROM subscriptions WHERE stripe_subscription_id = NULLIF($3,'')),
			$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (stripe_invoice_id) DO UPDATE SET
			status            = EXCLUDED.status,
			amount_cents      = EXCLUDED.amount_cents,
			currency          = EXCLUDED.currency,
			hosted_invoice_url= COALESCE(EXCLUDED.hosted_invoice_url, invoices.hosted_invoice_url),
			invoice_pdf_url   = COALESCE(EXCLUDED.invoice_pdf_url, invoices.invoice_pdf_url),
			period_start      = COALESCE(EXCLUDED.period_start, invoices.period_start),
			period_end        = COALESCE(EXCLUDED.period_end, invoices.period_end),
			paid_at           = COALESCE(EXCLUDED.paid_at, invoices.paid_at)`,
		inv.ID, inv.BusinessID, stripeSubID, inv.ID, inv.Number, inv.Status,
		inv.AmountCents, inv.Currency, inv.HostedInvoiceURL, inv.InvoicePDFURL,
		inv.PeriodStart, inv.PeriodEnd, inv.PaidAt)
	return err
}

// ---- webhook idempotency ledger ----

// ClaimWebhookEvent inserts the event id, returning false when it was already
// present. This is the single point of truth for "have I handled this?".
//
// The caller MUST return 2xx for a duplicate: Stripe retries until it gets
// one, so a duplicate is a success, not an error.
func (r *BillingRepo) ClaimWebhookEvent(ctx context.Context, eventID, eventType, objectID string, payload []byte) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO billing_webhook_events (provider_event_id, type, object_id, payload)
		VALUES ($1,$2,$3,$4) ON CONFLICT (provider_event_id) DO NOTHING`,
		eventID, eventType, objectID, payload)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MarkWebhookProcessed stamps a successfully handled event.
func (r *BillingRepo) MarkWebhookProcessed(ctx context.Context, eventID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE billing_webhook_events
		SET processed_at = now(), error = NULL WHERE provider_event_id = $1`, eventID)
	return err
}

// MarkWebhookFailed records a handler error while leaving processed_at NULL,
// so the row stays visible in the "unprocessed" index for operator triage.
func (r *BillingRepo) MarkWebhookFailed(ctx context.Context, eventID string, cause string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE billing_webhook_events SET error = $2 WHERE provider_event_id = $1`,
		eventID, cause)
	return err
}

// StaleSubscriptions returns businesses whose paid period has ended but whose
// row still claims an active status. Driven by the idx_subscriptions_period_end
// partial index; used by the reconciliation job when a webhook is lost.
func (r *BillingRepo) StaleSubscriptions(ctx context.Context, limit int) ([]*Subscription, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT `+subscriptionColumns+`
		FROM subscriptions s LEFT JOIN plans p ON p.id = s.plan_id
		WHERE s.status IN ('active','trialing','past_due')
		  AND s.current_period_end IS NOT NULL
		  AND s.current_period_end < now()
		ORDER BY s.current_period_end
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Subscription{}
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
