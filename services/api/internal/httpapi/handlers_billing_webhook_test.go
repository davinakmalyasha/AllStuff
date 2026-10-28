//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
)

// The webhook authenticated and deduplicated events before this, but no test
// ever delivered a handled event type. The three handlers that change billing
// state - subscription upsert, cancellation, invoice recording - had never
// run. Two defects came out of writing them:
//
//  1. invoices never stored the Stripe invoice id, so every invoice event (and
//     every retry) inserted another row instead of updating the first;
//  2. a failed dispatch left an error on the ledger, but the next delivery was
//     acknowledged as a duplicate without re-running the handler, so Stripe's
//     retry - the only automatic recovery - never applied the event.

func billingEvent(t *testing.T, id, typ string, object map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"id":   id,
		"type": typ,
		"data": map[string]any{"object": object},
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return body
}

func deliverWebhook(t *testing.T, hdl http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	sig := stripeSignature(testWebhookSecret, time.Now(), body)
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, webhookReq(body, sig))
	return w
}

func subscriptionObject(businessID, subID, status string) map[string]any {
	obj := map[string]any{
		"id":                   subID,
		"status":               status,
		"customer":             "cus_123",
		"cancel_at_period_end": false,
		"current_period_start": 1700000000,
		"current_period_end":   1702592000,
		"canceled_at":          0,
		"items": map[string]any{"data": []any{
			map[string]any{"price": map[string]any{"id": "price_growth"}},
		}},
	}
	if businessID != "" {
		obj["metadata"] = map[string]string{"business_id": businessID}
	}
	return obj
}

func invoiceObject(stripeID, subID, status, currency string, amountPaid, amountDue, created int64, businessID string) map[string]any {
	obj := map[string]any{
		"id":                 stripeID,
		"number":             "INV-" + stripeID,
		"status":             status,
		"customer":           "cus_123",
		"subscription":       subID,
		"amount_paid":        amountPaid,
		"amount_due":         amountDue,
		"currency":           currency,
		"hosted_invoice_url": "https://billing.example.test/" + stripeID,
		"invoice_pdf":        "https://billing.example.test/" + stripeID + ".pdf",
		"period_start":       1700000000,
		"period_end":         1702592000,
		"created":            created,
	}
	if businessID != "" {
		obj["metadata"] = map[string]string{"business_id": businessID}
	}
	return obj
}

func billingFixture(t *testing.T, h *testutil.H) *domain.Business {
	t.Helper()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Billing Events Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	// The seed leaves Stripe price ids to the operator.
	h.ExecPool(t, `UPDATE plans SET stripe_price_id = 'price_growth' WHERE id = 'growth'`)
	return biz
}

func TestBillingWebhookAppliesSubscriptionState(t *testing.T) {
	s, h, _ := billingServer(t)
	hdl := s.Handler()
	biz := billingFixture(t, h)
	const subID = "sub_apply_1"

	body := billingEvent(t, "evt_sub_1", "customer.subscription.created",
		subscriptionObject(biz.ID, subID, "active"))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("subscription.created: status = %d: %s", w.Code, w.Body.String())
	}

	sub, err := s.deps.Repos.Billing.GetSubscriptionForBusiness(t.Context(), biz.ID)
	if err != nil || sub == nil {
		t.Fatalf("subscription was not applied: %+v, %v", sub, err)
	}
	if sub.PlanID != "growth" || sub.PriceCents != 1900 || sub.Currency != "USD" {
		t.Errorf("plan = %s/%d/%s, want growth/1900/USD", sub.PlanID, sub.PriceCents, sub.Currency)
	}
	if sub.Status != "active" || !sub.GrantsEntitlements() {
		t.Errorf("status = %q grants = %v, want active and granting", sub.Status, sub.GrantsEntitlements())
	}
	if sub.StripeSubscription == nil || *sub.StripeSubscription != subID {
		t.Errorf("stripe subscription = %v, want %q", sub.StripeSubscription, subID)
	}
	if sub.StripeCustomerID == nil || *sub.StripeCustomerID != "cus_123" {
		t.Errorf("stripe customer = %v, want cus_123", sub.StripeCustomerID)
	}
	if sub.CurrentPeriodEnd == nil || sub.CurrentPeriodEnd.Unix() != 1702592000 {
		t.Errorf("current_period_end = %v, want the payload's timestamp", sub.CurrentPeriodEnd)
	}
	if sub.CanceledAt != nil {
		t.Errorf("canceled_at = %v, want nil for a zero timestamp", sub.CanceledAt)
	}

	var processed *time.Time
	var failure *string
	if err := h.QueryRow(t,
		`SELECT processed_at, error FROM billing_webhook_events WHERE provider_event_id = 'evt_sub_1'`).
		Scan(&processed, &failure); err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if processed == nil || failure != nil {
		t.Errorf("ledger row processed_at = %v, error = %v; want processed with no error", processed, failure)
	}

	// A dashboard-created subscription has no business metadata. The handler
	// must resolve it through the stored Stripe id rather than ignore it.
	body = billingEvent(t, "evt_sub_2", "customer.subscription.updated",
		subscriptionObject("", subID, "past_due"))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("subscription.updated: status = %d: %s", w.Code, w.Body.String())
	}
	sub, err = s.deps.Repos.Billing.GetSubscriptionForBusiness(t.Context(), biz.ID)
	if err != nil || sub == nil {
		t.Fatalf("read the updated subscription: %+v, %v", sub, err)
	}
	if sub.Status != "past_due" {
		t.Errorf("status = %q, want past_due", sub.Status)
	}
	// past_due keeps access on purpose: Stripe is still retrying the card, and
	// the period_end index catches rows that never resolve.
	if !sub.GrantsEntitlements() {
		t.Error("past_due lost the documented dunning grace period")
	}
	if sub.BusinessID != biz.ID {
		t.Errorf("the no-metadata update moved the subscription to %s", sub.BusinessID)
	}
}

func TestBillingWebhookCancellationMovesToFree(t *testing.T) {
	s, h, _ := billingServer(t)
	hdl := s.Handler()
	biz := billingFixture(t, h)
	const subID = "sub_cancel_1"

	body := billingEvent(t, "evt_cancel_seed", "customer.subscription.created",
		subscriptionObject(biz.ID, subID, "active"))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("seed: status = %d: %s", w.Code, w.Body.String())
	}

	body = billingEvent(t, "evt_cancel_1", "customer.subscription.deleted", map[string]any{
		"id":                   subID,
		"status":               "canceled",
		"customer":             "cus_123",
		"current_period_start": 1700000000,
		"current_period_end":   1702592000,
		"canceled_at":          1702000000,
	})
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("subscription.deleted: status = %d: %s", w.Code, w.Body.String())
	}

	sub, err := s.deps.Repos.Billing.GetSubscriptionForBusiness(t.Context(), biz.ID)
	if err != nil || sub == nil {
		t.Fatalf("read the canceled subscription: %+v, %v", sub, err)
	}
	if sub.PlanID != "free" || sub.PriceCents != 0 || sub.Currency != "USD" {
		t.Errorf("plan = %s/%d/%s, want free/0/USD", sub.PlanID, sub.PriceCents, sub.Currency)
	}
	if sub.Status != "canceled" || sub.GrantsEntitlements() {
		t.Errorf("status = %q grants = %v, want canceled and non-granting", sub.Status, sub.GrantsEntitlements())
	}
	// The portal still needs the Stripe ids after cancellation.
	if sub.StripeSubscription == nil || *sub.StripeSubscription != subID {
		t.Errorf("stripe subscription = %v, want it retained", sub.StripeSubscription)
	}
	if sub.StripeCustomerID == nil || *sub.StripeCustomerID != "cus_123" {
		t.Errorf("stripe customer = %v, want it retained", sub.StripeCustomerID)
	}
	if sub.CanceledAt == nil || sub.CanceledAt.Unix() != 1702000000 {
		t.Errorf("canceled_at = %v, want the payload's timestamp", sub.CanceledAt)
	}

	// A deletion for a subscription with no business metadata and no row is a
	// no-op, not an error: Stripe may deliver events for subscriptions created
	// before this deployment.
	body = billingEvent(t, "evt_cancel_unknown", "customer.subscription.deleted",
		subscriptionObject("", "sub_never_seen", "canceled"))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Errorf("unknown subscription: status = %d: %s", w.Code, w.Body.String())
	}
	var rows int
	if err := h.QueryRow(t, `SELECT count(*) FROM subscriptions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("subscriptions = %d, want only the fixture's row", rows)
	}
}

func TestBillingWebhookInvoicesAreIdempotent(t *testing.T) {
	s, h, _ := billingServer(t)
	hdl := s.Handler()
	biz := billingFixture(t, h)
	const subID = "sub_invoice_1"

	body := billingEvent(t, "evt_inv_seed", "customer.subscription.created",
		subscriptionObject(biz.ID, subID, "active"))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("seed: status = %d: %s", w.Code, w.Body.String())
	}

	// invoice.created fires before payment: the amount comes from amount_due
	// and paid_at must stay NULL.
	body = billingEvent(t, "evt_inv_created", "invoice.created",
		invoiceObject("in_123", subID, "draft", "usd", 0, 1900, 1700000100, biz.ID))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("invoice.created: status = %d: %s", w.Code, w.Body.String())
	}

	// invoice.paid carries the same object id and must UPDATE that row, not add
	// a second one. The column it conflicts on is the Stripe id, not the local
	// uuid used as the primary key.
	body = billingEvent(t, "evt_inv_paid", "invoice.paid",
		invoiceObject("in_123", subID, "paid", "usd", 1900, 0, 1700000200, biz.ID))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("invoice.paid: status = %d: %s", w.Code, w.Body.String())
	}

	invoices, err := s.deps.Repos.Billing.ListInvoices(t.Context(), biz.ID, 20)
	if err != nil {
		t.Fatalf("ListInvoices: %v", err)
	}
	if len(invoices) != 1 {
		t.Fatalf("invoices = %d, want 1: %+v", len(invoices), invoices)
	}
	inv := invoices[0]
	if inv.Status == nil || *inv.Status != "paid" {
		t.Errorf("status = %v, want paid", inv.Status)
	}
	if inv.AmountCents != 1900 || inv.Currency != "USD" {
		t.Errorf("amount = %d %s, want 1900 USD", inv.AmountCents, inv.Currency)
	}
	if inv.PaidAt == nil || inv.PaidAt.Unix() != 1700000200 {
		t.Errorf("paid_at = %v, want the paid event's created timestamp", inv.PaidAt)
	}
	if inv.SubscriptionID == nil {
		t.Error("the invoice is not linked to the subscription")
	}
	var stripeID string
	if err := h.QueryRow(t, `SELECT stripe_invoice_id FROM invoices WHERE business_id = $1`, biz.ID).
		Scan(&stripeID); err != nil {
		t.Fatal(err)
	}
	if stripeID != "in_123" {
		t.Errorf("stripe_invoice_id = %q, want the provider id in_123", stripeID)
	}

	// A later failure for the same invoice keeps the paid timestamp: the event
	// updates the row rather than replacing its history.
	body = billingEvent(t, "evt_inv_failed", "invoice.payment_failed",
		invoiceObject("in_123", subID, "open", "usd", 0, 1900, 1700000300, ""))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("invoice.payment_failed: status = %d: %s", w.Code, w.Body.String())
	}
	invoices, err = s.deps.Repos.Billing.ListInvoices(t.Context(), biz.ID, 20)
	if err != nil {
		t.Fatalf("ListInvoices: %v", err)
	}
	if len(invoices) != 1 {
		t.Fatalf("invoices after payment_failed = %d, want 1", len(invoices))
	}
	if invoices[0].Status == nil || *invoices[0].Status != "open" {
		t.Errorf("status = %v, want open", invoices[0].Status)
	}
	if invoices[0].PaidAt == nil || invoices[0].PaidAt.Unix() != 1700000200 {
		t.Errorf("paid_at = %v, want the earlier paid timestamp retained", invoices[0].PaidAt)
	}

	// An invoice for an unknown subscription, with no business metadata, is
	// ignored rather than failing or creating an orphan row.
	body = billingEvent(t, "evt_inv_orphan", "invoice.created",
		invoiceObject("in_orphan", "sub_never_seen", "draft", "usd", 0, 500, 1700000400, ""))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Errorf("orphan invoice: status = %d: %s", w.Code, w.Body.String())
	}
	var rows int
	if err := h.QueryRow(t, `SELECT count(*) FROM invoices`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("invoices = %d after an orphan event, want 1", rows)
	}

	// A malformed currency is normalised to USD instead of violating the
	// three-uppercase-letter CHECK and wedging the ledger.
	body = billingEvent(t, "evt_inv_currency", "invoice.created",
		invoiceObject("in_currency", subID, "draft", "us", 0, 700, 1700000500, biz.ID))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("short currency: status = %d: %s", w.Code, w.Body.String())
	}
	var currency string
	if err := h.QueryRow(t, `SELECT currency FROM invoices WHERE stripe_invoice_id = 'in_currency'`).
		Scan(&currency); err != nil {
		t.Fatal(err)
	}
	if currency != "USD" {
		t.Errorf("currency = %q, want the USD fallback", currency)
	}
}

func TestBillingWebhookRetriesAFailedEvent(t *testing.T) {
	// Pool-bound: the first delivery must fail with a real database error, and
	// a statement error inside the shared test transaction would abort every
	// later statement in the test.
	cfg := testConfig(t)
	cfg.StripeWebhookSecret = testWebhookSecret
	cfg.RateLimitGlobal = 100000
	s, h, _ := newDBTestServerOpts(t, cfg, true)
	hdl := s.Handler()
	biz := billingFixture(t, h)

	if _, err := h.Pool.Exec(t.Context(), `
		CREATE FUNCTION billing_fail_once() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'simulated transient failure';
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := h.Pool.Exec(t.Context(), `
		CREATE TRIGGER billing_fail_once BEFORE INSERT ON subscriptions
		FOR EACH ROW EXECUTE FUNCTION billing_fail_once()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	body := billingEvent(t, "evt_retry_1", "customer.subscription.created",
		subscriptionObject(biz.ID, "sub_retry_1", "active"))
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusInternalServerError {
		t.Fatalf("failing delivery: status = %d, want 500: %s", w.Code, w.Body.String())
	}
	var processed *time.Time
	var failure *string
	if err := h.Repos.QueryRow(t.Context(),
		`SELECT processed_at, error FROM billing_webhook_events WHERE provider_event_id = 'evt_retry_1'`).
		Scan(&processed, &failure); err != nil {
		t.Fatalf("read the ledger after failure: %v", err)
	}
	if processed != nil || failure == nil {
		t.Fatalf("failed delivery: processed_at = %v, error = %v; want unprocessed with an error", processed, failure)
	}
	if _, err := h.Pool.Exec(t.Context(), `DROP TRIGGER billing_fail_once ON subscriptions`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}

	// Stripe retries the same event id. It must be re-processed now that the
	// cause is gone, not acknowledged as a duplicate.
	if w := deliverWebhook(t, hdl, body); w.Code != http.StatusOK {
		t.Fatalf("retry: status = %d, want 200: %s", w.Code, w.Body.String())
	}
	sub, err := s.deps.Repos.Billing.GetSubscriptionForBusiness(t.Context(), biz.ID)
	if err != nil || sub == nil {
		t.Fatalf("the retried event was not applied: %+v, %v", sub, err)
	}
	if sub.Status != "active" || sub.StripeSubscription == nil || *sub.StripeSubscription != "sub_retry_1" {
		t.Errorf("retried subscription = %+v", sub)
	}
	if err := h.Repos.QueryRow(t.Context(),
		`SELECT processed_at, error FROM billing_webhook_events WHERE provider_event_id = 'evt_retry_1'`).
		Scan(&processed, &failure); err != nil {
		t.Fatalf("read the ledger after retry: %v", err)
	}
	if processed == nil || failure != nil {
		t.Errorf("retry: processed_at = %v, error = %v; want processed with no error", processed, failure)
	}
}
