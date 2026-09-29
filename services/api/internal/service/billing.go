package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/security"
	"bizverse/api/internal/util"
)

// Billing — subscriptions and entitlements (Phase 7.1).
//
// Stripe is the system of record for money; this service is the system of
// record for *access*. It never trusts client-supplied state: every
// entitlement decision reads the subscriptions table, which is only ever
// written by a signature-verified webhook or the reconciliation job.
type Billing struct {
	repos  *repo.Repos
	cfg    config.Config
	stripe *security.StripeClient
	logger *slog.Logger
}

func NewBilling(repos *repo.Repos, cfg config.Config, stripe *security.StripeClient, logger *slog.Logger) *Billing {
	return &Billing{repos: repos, cfg: cfg, stripe: stripe, logger: logger}
}

// FreePlanID is the implicit default every business starts on.
const FreePlanID = "free"

// billingSigToler is the Stripe signature replay window.
const billingSigToler = 5 * time.Minute

// ---- plans & entitlements ----

// Plans returns the active catalogue. Public: the pricing page renders it
// without authentication.
func (b *Billing) Plans(ctx context.Context) ([]*repo.BillingPlan, error) {
	return b.repos.Billing.ListPlans(ctx)
}

// PublishableKey is exposed to the browser for Stripe.js.
func (b *Billing) PublishableKey() string { return b.stripe.PublishableKey() }

// Enabled reports whether paid checkout is configured.
func (b *Billing) Enabled() bool { return b.stripe.Enabled() }

// Entitlements is the resolved capability set for a business. An unconfigured
// or unknown plan degrades to the free set rather than to nothing, so a typo
// in the catalogue cannot lock an owner out of their own storefront.
func (b *Billing) Entitlements(ctx context.Context, businessID string) (*Entitlements, error) {
	plan, err := b.effectivePlan(ctx, businessID)
	if err != nil {
		return nil, err
	}
	return ResolveEntitlements(plan.Entitlements), nil
}

// effectivePlan resolves which plan a business is on, taking the free plan
// when there is no row or the row does not currently grant access.
func (b *Billing) effectivePlan(ctx context.Context, businessID string) (*repo.BillingPlan, error) {
	sub, err := b.repos.Billing.GetSubscriptionForBusiness(ctx, businessID)
	if err != nil {
		return nil, err
	}
	if sub != nil && sub.GrantsEntitlements() {
		p, err := b.repos.Billing.GetPlan(ctx, sub.PlanID)
		if err != nil {
			return nil, err
		}
		if p != nil {
			return p, nil
		}
		// Subscription points at a plan that is no longer active (an operator
		// retired it). Fall through to free rather than dropping access.
		b.logger.Warn("subscription references an inactive plan; falling back to free",
			"business_id", businessID, "plan_id", sub.PlanID)
	}
	free, err := b.repos.Billing.GetPlan(ctx, FreePlanID)
	if err != nil {
		return nil, err
	}
	if free == nil {
		// The catalogue was never seeded (migration 0028 not applied). Return
		// an empty set rather than a 500 so the rest of the product keeps
		// working; the free plan's entitlements are the least surprising
		// fallback.
		b.logger.Error("free plan missing from catalogue; check migration 0028")
		return &repo.BillingPlan{ID: FreePlanID}, nil
	}
	return free, nil
}

// Entitlements is the resolved, queryable capability set.
//
// Every field here is something the operator pays INFRASTRUCTURE for: a bigger
// catalogue, a bigger gallery, more seats, more API calls. Nothing in this
// struct buys a trust signal or a better position in search.
//
// The distinction is the product's whole argument. Verification is an admin
// decision made after reviewing registration documents, and a directory that
// sells the verified badge has made that badge meaningless. Featured placement
// is an editorial slot chosen at /admin/curation, and a paid slot is a promoted
// listing. An earlier revision of this struct carried `verified_badge` and
// `featured_placement`; both were removed from the catalogue and from here, and
// they are not coming back. See the seed comment in migration 0028.
type Entitlements struct {
	Analytics         bool `json:"analytics"`
	AnalyticsAdvanced bool `json:"analytics_advanced"`
	APIAccess         bool `json:"api_access"`
	SupportPriority   bool `json:"support_priority"`
	ProductLimit      int  `json:"product_limit"`
	GalleryLimit      int  `json:"gallery_limit"`
	TeamSeats         int  `json:"team_seats"`
}

// EntitlementSource resolves a business's current capability set.
//
// Declared here as an interface so Products, Businesses and Invites can gate on
// capacity without importing the billing package's dependencies, and so a test
// can supply a fixed set instead of a Stripe-backed lookup. *Billing is the one
// production implementation.
type EntitlementSource interface {
	Entitlements(ctx context.Context, businessID string) (*Entitlements, error)
}

// Unlimited is the sentinel for "this capability is not capped". Chosen as -1
// because zero is a meaningful entitlement value elsewhere in this package
// (`support_priority` style booleans resolve to false, not to unlimited).
const Unlimited = -1

// Has reports a boolean capability. Unknown capability strings return false, so
// a typo in a gate is a deny rather than an accidental allow.
func (e *Entitlements) Has(name string) bool {
	switch name {
	case "analytics":
		return e.Analytics
	case "analytics_advanced":
		return e.AnalyticsAdvanced
	case "api_access":
		return e.APIAccess
	case "support_priority":
		return e.SupportPriority
	default:
		return false
	}
}

// Exceeds reports whether count is beyond the named capacity cap, treating a
// non-positive cap as unlimited.
//
// The asymmetry with Limit is deliberate: Limit falls back to a caller-supplied
// default when a cap is absent, which is right for a *display* value and wrong
// for a *gate*. A business whose plan omits `product_limit` has no published
// cap, and inventing one out of a default constant would be a limit nobody
// agreed to.
func (e *Entitlements) Exceeds(name string, count int) bool {
	switch name {
	case "product_limit":
		return e.ProductLimit > 0 && count > e.ProductLimit
	case "gallery_limit":
		return e.GalleryLimit > 0 && count > e.GalleryLimit
	case "team_seats":
		return e.TeamSeats > 0 && count > e.TeamSeats
	}
	return false
}

// Limit returns a `name:N` entitlement's numeric cap, or def when absent.
func (e *Entitlements) Limit(name string, def int) int {
	switch name {
	case "product_limit":
		if e.ProductLimit > 0 {
			return e.ProductLimit
		}
	case "gallery_limit":
		if e.GalleryLimit > 0 {
			return e.GalleryLimit
		}
	case "team_seats":
		if e.TeamSeats > 0 {
			return e.TeamSeats
		}
	}
	return def
}

// ResolveEntitlements parses the `plans.entitlements` text[] into a struct.
// Pure and table-testable: no I/O, no config.
func ResolveEntitlements(raw []string) *Entitlements {
	e := &Entitlements{
		// Conservative defaults for a business with no entitlements at all.
		ProductLimit: 5,
		GalleryLimit: 3,
		TeamSeats:    1,
	}
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, val, hasVal := strings.Cut(item, ":")
		if hasVal {
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				// A malformed cap is ignored, not clamped: better to fall back
				// to the struct default than to grant 0 or a huge number.
				continue
			}
			switch name {
			case "product_limit":
				e.ProductLimit = n
			case "gallery_limit":
				e.GalleryLimit = n
			case "team_seats":
				e.TeamSeats = n
			}
			continue
		}
		switch name {
		case "analytics":
			e.Analytics = true
		case "analytics_advanced":
			e.AnalyticsAdvanced = true
		case "api_access":
			e.APIAccess = true
		case "support_priority":
			e.SupportPriority = true
			// featured_placement, verified_badge, webhooks and embeddable_widget
			// are deliberately absent. The first two sold trust and placement and
			// were removed from the catalogue; the last two were never built. An
			// unknown name falling through to nothing is the same code path a
			// typo takes, so a plan cannot accidentally re-grant them by
			// reintroducing the string.
		}
	}
	return e
}

// ---- checkout ----

// StartCheckout creates a Stripe Checkout session for a plan.
//
// The caller has already proven it may manage the business; this re-checks via
// the subscription read so a business can never open checkout for a plan that
// a hidden rule disallows, and so an already-paid business is sent to the
// portal instead of double-subscribing.
func (b *Billing) StartCheckout(ctx context.Context, businessID, ownerEmail, planID string) (string, error) {
	if !b.stripe.Enabled() {
		return "", domain.ErrNotFound.WithField("_", "Billing is not configured on this deployment.")
	}
	plan, err := b.repos.Billing.GetPlan(ctx, planID)
	if err != nil {
		return "", err
	}
	if plan == nil {
		return "", domain.ErrValidation.WithField("plan_id", "Unknown plan.")
	}
	if !plan.Purchasable() {
		// Not a client error: the catalogue is real but not yet sold. Surface a
		// generic message rather than confirming the plan exists but is
		// unconfigured.
		return "", domain.ErrConflict.WithField("plan_id", "This plan is not available for purchase yet.")
	}
	if plan.PriceCents == 0 {
		// Downgrade to free is a no-op, not a checkout.
		return "", domain.ErrValidation.WithField("plan_id", "The free plan does not require checkout.")
	}

	existing, err := b.repos.Billing.GetSubscriptionForBusiness(ctx, businessID)
	if err != nil {
		return "", err
	}
	if existing != nil && existing.GrantsEntitlements() && existing.StripeCustomerID != nil {
		return "", domain.ErrConflict.WithField("_", "This business already has an active subscription. Manage it from the billing portal.")
	}

	params := security.CheckoutSessionParams{
		PriceID:    *plan.StripePriceID,
		BusinessID: businessID,
		OwnerEmail: ownerEmail,
		SuccessURL: b.cfg.PublicURL + "/dashboard/billing?checkout=success",
		CancelURL:  b.cfg.PublicURL + "/dashboard/billing?checkout=cancelled",
		ClientRef:  businessID,
	}
	if existing != nil && existing.StripeCustomerID != nil {
		params.CustomerID = *existing.StripeCustomerID
	}
	sess, err := b.stripe.CreateCheckoutSession(ctx, params)
	if err != nil {
		if errors.Is(err, security.ErrStripeDisabled) {
			return "", domain.ErrNotFound.WithField("_", "Billing is not configured on this deployment.")
		}
		return "", err
	}
	return sess.URL, nil
}

// OpenPortal creates a Stripe customer-portal session for self-serve
// cancellation and card updates.
func (b *Billing) OpenPortal(ctx context.Context, businessID string) (string, error) {
	if !b.stripe.Enabled() {
		return "", domain.ErrNotFound.WithField("_", "Billing is not configured on this deployment.")
	}
	sub, err := b.repos.Billing.GetSubscriptionForBusiness(ctx, businessID)
	if err != nil {
		return "", err
	}
	if sub == nil || sub.StripeCustomerID == nil {
		return "", domain.ErrConflict.WithField("_", "This business has no billing account yet.")
	}
	sess, err := b.stripe.CreatePortalSession(ctx, *sub.StripeCustomerID, b.cfg.PublicURL+"/dashboard/billing")
	if err != nil {
		return "", err
	}
	return sess.URL, nil
}

// SubscriptionFor returns the billing state a dashboard renders.
func (b *Billing) SubscriptionFor(ctx context.Context, businessID string) (map[string]any, error) {
	sub, err := b.repos.Billing.GetSubscriptionForBusiness(ctx, businessID)
	if err != nil {
		return nil, err
	}
	ent, err := b.Entitlements(ctx, businessID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"entitlements": ent,
		"enabled":      b.Enabled(),
	}
	if sub == nil {
		// No row: the business is on the implicit free plan. Report the plan so
		// the UI can render "Free" rather than a blank state.
		free, ferr := b.repos.Billing.GetPlan(ctx, FreePlanID)
		if ferr != nil {
			return nil, ferr
		}
		if free != nil {
			out["plan"] = publicPlan(free)
		}
		out["subscription"] = nil
		return out, nil
	}
	// The stripe ids are marked `json:"-"` in the repo type; keep it that way
	// — a customer id in an owner-facing payload is needless exposure.
	out["subscription"] = sub
	if sub.PlanID != "" {
		if p, perr := b.repos.Billing.GetPlan(ctx, sub.PlanID); perr == nil && p != nil {
			out["plan"] = publicPlan(p)
		}
	}
	return out, nil
}

// publicPlan strips operator-only fields from a plan before it reaches a client.
func publicPlan(p *repo.BillingPlan) map[string]any {
	return map[string]any{
		"id":           p.ID,
		"name":         p.Name,
		"description":  p.Description,
		"price_cents":  p.PriceCents,
		"currency":     p.Currency,
		"entitlements": p.Entitlements,
		"sort_order":   p.SortOrder,
		"purchasable":  p.Purchasable(),
	}
}

// ---- webhook ----

// HandleWebhook is the single entry point for Stripe deliveries.
//
// Order matters: signature first (never parse an unverified body), then the
// idempotency claim, then the handler. A duplicate is reported as success so
// Stripe stops retrying, and the ledger row is what makes that safe.
func (b *Billing) HandleWebhook(ctx context.Context, rawBody []byte, signature string) error {
	if err := security.VerifyStripeSignature(rawBody, signature, b.cfg.StripeWebhookSecret, billingSigToler); err != nil {
		// Returned as a 400 by the handler. Deliberately does not record the
		// event: an unverified payload is not evidence of anything.
		return domain.ErrValidation.WithField("_", "Invalid webhook signature.")
	}
	ev, err := security.ParseEvent(rawBody, b.cfg.AppEnv == "prod")
	if err != nil {
		return domain.ErrValidation.WithField("_", "Unsupported webhook payload.")
	}

	fresh, err := b.repos.Billing.ClaimWebhookEvent(ctx, ev.ID, ev.Type, eventObjectID(ev), rawBody)
	if err != nil {
		return err
	}
	if !fresh {
		b.logger.Info("billing webhook duplicate; acknowledging", "event_id", ev.ID, "type", ev.Type)
		return nil
	}

	if err := b.dispatch(ctx, ev); err != nil {
		_ = b.repos.Billing.MarkWebhookFailed(ctx, ev.ID, truncate(err.Error(), 500))
		// Returning an error makes Stripe retry, which is what we want for a
		// transient failure. The ledger's PK means the retry re-claims nothing
		// and re-runs the handler — so the handler must be idempotent, which
		// the upserts below are.
		return err
	}
	if err := b.repos.Billing.MarkWebhookProcessed(ctx, ev.ID); err != nil {
		return err
	}
	return nil
}

// dispatch routes an event to its handler. Unhandled types are a success: a
// new Stripe event type must not put the endpoint into a retry loop.
func (b *Billing) dispatch(ctx context.Context, ev *security.StripeEvent) error {
	switch ev.Type {
	case "checkout.session.completed":
		return nil // the subscription.created event carries the state we need
	case "customer.subscription.created", "customer.subscription.updated":
		return b.onSubscriptionChanged(ctx, ev)
	case "customer.subscription.deleted":
		return b.onSubscriptionCanceled(ctx, ev)
	case "invoice.created", "invoice.paid", "invoice.payment_failed", "invoice.finalized":
		return b.onInvoice(ctx, ev)
	default:
		b.logger.Debug("billing webhook: unhandled type", "type", ev.Type)
		return nil
	}
}

// onSubscriptionChanged applies authoritative subscription state.
func (b *Billing) onSubscriptionChanged(ctx context.Context, ev *security.StripeEvent) error {
	stripeSub, businessID, err := ev.DecodeSubscriptionObject()
	if err != nil {
		return err
	}
	if businessID == "" {
		// Fall back to the row we already have. Metadata is set at checkout
		// time, but a subscription created outside Checkout (in the Stripe
		// dashboard) will not carry it.
		existing, ferr := b.repos.Billing.GetSubscriptionByStripeID(ctx, stripeSub.ID)
		if ferr != nil {
			return ferr
		}
		if existing == nil {
			b.logger.Warn("billing webhook for a subscription with no business; ignoring",
				"subscription_id", stripeSub.ID, "event", ev.ID)
			return nil
		}
		businessID = existing.BusinessID
	}
	plan, err := b.planForStripePrice(ctx, stripeSub)
	if err != nil {
		return err
	}
	planID := FreePlanID
	priceCents, currency := 0, "USD"
	if plan != nil {
		planID = plan.ID
		priceCents = plan.PriceCents
		currency = plan.Currency
	}

	// 0028 requires a Stripe subscription id whenever status is not
	// incomplete/canceled; guard anyway so a malformed payload produces a
	// skipped event rather than a CHECK violation that wedges the ledger.
	status := normalizeStripeStatus(stripeSub.Status)
	if status != "incomplete" && status != "canceled" && stripeSub.ID == "" {
		return fmt.Errorf("billing: refusing to apply status %q without a subscription id", status)
	}

	sub := &repo.Subscription{
		ID:                 util.NewUUID(),
		BusinessID:         businessID,
		PlanID:             planID,
		PriceCents:         priceCents,
		Currency:           currency,
		StripeCustomerID:   optional(stripeSub.CustomerID),
		StripeSubscription: optional(stripeSub.ID),
		Status:             status,
		CancelAtPeriodEnd:  stripeSub.CancelAtPeriodEnd,
		CurrentPeriodStart: unixPtr(stripeSub.CurrentPeriodStart),
		CurrentPeriodEnd:   unixPtr(stripeSub.CurrentPeriodEnd),
		CanceledAt:         unixPtr(stripeSub.CanceledAt),
	}
	if err := b.repos.Billing.UpsertSubscription(ctx, sub); err != nil {
		return err
	}
	b.logger.Info("billing subscription applied",
		"business_id", businessID, "plan_id", planID, "status", status)
	return nil
}

// onSubscriptionCanceled moves the business back to the free plan.
func (b *Billing) onSubscriptionCanceled(ctx context.Context, ev *security.StripeEvent) error {
	stripeSub, businessID, err := ev.DecodeSubscriptionObject()
	if err != nil {
		return err
	}
	if businessID == "" {
		existing, ferr := b.repos.Billing.GetSubscriptionByStripeID(ctx, stripeSub.ID)
		if ferr != nil {
			return ferr
		}
		if existing == nil {
			return nil
		}
		businessID = existing.BusinessID
	}
	// Keep the Stripe ids: the owner must still be able to open the portal and
	// see their (empty) invoice history. Only the plan and status change.
	sub := &repo.Subscription{
		ID:                 util.NewUUID(),
		BusinessID:         businessID,
		PlanID:             FreePlanID,
		PriceCents:         0,
		Currency:           "USD",
		StripeCustomerID:   optional(stripeSub.CustomerID),
		StripeSubscription: optional(stripeSub.ID),
		Status:             "canceled",
		CancelAtPeriodEnd:  false,
		CurrentPeriodStart: unixPtr(stripeSub.CurrentPeriodStart),
		CurrentPeriodEnd:   unixPtr(stripeSub.CurrentPeriodEnd),
		CanceledAt:         unixPtr(stripeSub.CanceledAt),
	}
	if err := b.repos.Billing.UpsertSubscription(ctx, sub); err != nil {
		return err
	}
	b.logger.Info("billing subscription canceled; business moved to free",
		"business_id", businessID)
	return nil
}

// onInvoice records billing history.
func (b *Billing) onInvoice(ctx context.Context, ev *security.StripeEvent) error {
	rec, businessID, err := ev.DecodeInvoiceObject()
	if err != nil {
		return err
	}
	if businessID == "" {
		existing, ferr := b.repos.Billing.GetSubscriptionByStripeID(ctx, rec.SubscriptionID)
		if ferr != nil {
			return ferr
		}
		if existing == nil {
			b.logger.Warn("billing invoice for an unknown subscription; ignoring",
				"invoice_id", rec.ID, "event", ev.ID)
			return nil
		}
		businessID = existing.BusinessID
	}
	currency := rec.Currency
	if len(currency) != 3 {
		currency = "USD"
	}
	inv := &repo.Invoice{
		ID:               util.NewUUID(),
		BusinessID:       businessID,
		Number:           optional(rec.Number),
		Status:           optional(rec.Status),
		AmountCents:      rec.AmountCents,
		Currency:         currency,
		HostedInvoiceURL: optional(rec.HostedInvoiceURL),
		InvoicePDFURL:    optional(rec.InvoicePDF),
		PeriodStart:      unixPtr(rec.PeriodStart),
		PeriodEnd:        unixPtr(rec.PeriodEnd),
	}
	// Only a settled invoice has a paid_at. invoice.created fires before
	// payment, so deriving paid_at from event type avoids stamping a paid
	// invoice that later fails.
	if rec.Status == "paid" {
		inv.PaidAt = unixPtr(rec.Created)
	}
	return b.repos.Billing.UpsertInvoice(ctx, inv, rec.SubscriptionID)
}

// planForStripePrice maps the Stripe price on a subscription back to a plan.
//
// The match is exact on stripe_price_id. When it cannot be resolved the
// subscription is treated as free rather than being assigned an arbitrary paid
// plan: an operator can see the unrecognised price in the logs and fix the
// catalogue, whereas a wrong grant is silent and irreversible.
func (b *Billing) planForStripePrice(ctx context.Context, s *security.StripeSubscription) (*repo.BillingPlan, error) {
	if s.PriceID == "" {
		return nil, nil
	}
	plans, err := b.repos.Billing.ListPlans(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range plans {
		if p.StripePriceID != nil && *p.StripePriceID == s.PriceID {
			return p, nil
		}
	}
	b.logger.Warn("billing: stripe price does not match any plan; treating as free",
		"price_id", s.PriceID, "subscription_id", s.ID)
	return nil, nil
}

// Reconcile repairs subscriptions whose period ended without a webhook.
// Called from the jobs runner; safe to run on every replica because it only
// writes rows it has re-read from Stripe.
func (b *Billing) Reconcile(ctx context.Context) error {
	if !b.stripe.Enabled() {
		return nil
	}
	stale, err := b.repos.Billing.StaleSubscriptions(ctx, 50)
	if err != nil {
		return err
	}
	if len(stale) == 0 {
		return nil
	}
	for _, s := range stale {
		if s.StripeSubscription == nil {
			continue
		}
		auth, err := b.stripe.RetrieveSubscription(ctx, *s.StripeSubscription)
		if err != nil {
			// One failure must not abort the batch; the next tick retries.
			b.logger.Warn("billing reconcile: stripe read failed",
				"business_id", s.BusinessID, "err", err)
			continue
		}
		upd := *s
		upd.Status = normalizeStripeStatus(auth.Status)
		upd.CancelAtPeriodEnd = auth.CancelAtPeriodEnd
		upd.CurrentPeriodStart = unixPtr(auth.CurrentPeriodStart)
		upd.CurrentPeriodEnd = unixPtr(auth.CurrentPeriodEnd)
		upd.CanceledAt = unixPtr(auth.CanceledAt)
		if err := b.repos.Billing.UpsertSubscription(ctx, &upd); err != nil {
			b.logger.Warn("billing reconcile: write failed",
				"business_id", s.BusinessID, "err", err)
		}
	}
	if len(stale) > 0 {
		b.logger.Info("billing reconcile completed", "examined", len(stale))
	}
	return nil
}

// ---- helpers ----

// normalizeStripeStatus maps Stripe's status vocabulary onto the CHECK
// constraint in 0028. Unknown values collapse to "incomplete", the
// non-granting default, so a new Stripe status cannot accidentally grant
// entitlements.
func normalizeStripeStatus(s string) string {
	switch s {
	case "active", "trialing", "past_due", "canceled", "unpaid", "paused", "incomplete":
		return s
	default:
		return "incomplete"
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

func unixPtr(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0).UTC()
	return &t
}

func eventObjectID(ev *security.StripeEvent) string {
	if len(ev.Data) == 0 {
		return ""
	}
	var probe struct {
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(ev.Data, &probe); err != nil {
		return ""
	}
	if probe.Object.ID != "" {
		return probe.Object.ID
	}
	return probe.ID
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
