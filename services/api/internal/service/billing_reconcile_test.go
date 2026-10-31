//go:build !race

package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"allstuff/api/internal/config"
	"allstuff/api/internal/domain"
	"allstuff/api/internal/repo"
	"allstuff/api/internal/security"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// Reconcile is the fallback that rewrites paid-status rows from Stripe state
// when a webhook delivery is lost. It had no coverage: the only executed
// branch was the disabled-stripe early return by way of other tests. A mapping
// bug here silently downgrades paying businesses (support tickets) or leaves
// canceled ones granting (revenue leak), so every branch is covered: the
// guard, the per-row skip, failure isolation across the batch, the status
// normalization, and the exact columns written back.

func reconcileHarness(t *testing.T, h *testutil.H, stripe *security.StripeClient) *Billing {
	t.Helper()
	return NewBilling(h.Repos, config.Config{}, stripe, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func reconcileBusiness(t *testing.T, h *testutil.H, name string) *domain.Business {
	t.Helper()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	return testutil.Business(t, h, testutil.BusinessOpts{
		Name: name, Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
}

func seedSubscription(t *testing.T, h *testutil.H, bizID, stripeSubID, status string, periodEnd *time.Time) {
	t.Helper()
	var stripeID *string
	if stripeSubID != "" {
		v := stripeSubID
		stripeID = &v
	}
	cust := "cus_" + bizID[:8]
	err := h.Repos.Billing.UpsertSubscription(context.Background(), &repo.Subscription{
		ID:                 util.NewUUID(),
		BusinessID:         bizID,
		PlanID:             "growth",
		PriceCents:         1900,
		Currency:           "USD",
		StripeCustomerID:   &cust,
		StripeSubscription: stripeID,
		Status:             status,
		CurrentPeriodEnd:   periodEnd,
	})
	if err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
}

func readSubscription(t *testing.T, h *testutil.H, bizID string) *repo.Subscription {
	t.Helper()
	sub, err := h.Repos.Billing.GetSubscriptionForBusiness(context.Background(), bizID)
	if err != nil || sub == nil {
		t.Fatalf("read subscription for %s: %+v, %v", bizID, sub, err)
	}
	return sub
}

// stripeStub answers RetrieveSubscription from a map. Missing ids are a Stripe
// 404, which is how the batch-isolation branch is exercised.
type stripeStub struct {
	mu        sync.Mutex
	requested []string
	subs      map[string]map[string]any
}

func (s *stripeStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, _ := strings.CutPrefix(r.URL.Path, "/v1/subscriptions/")
	s.mu.Lock()
	s.requested = append(s.requested, id)
	s.mu.Unlock()
	obj, ok := s.subs[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"No such subscription"}}`))
		return
	}
	obj["id"] = id
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(obj)
}

func (s *stripeStub) saw(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, got := range s.requested {
		if got == id {
			return true
		}
	}
	return false
}

func stripeSubJSON(status string, cancelEnd bool, start, end, canceled int64) map[string]any {
	return map[string]any{
		"status": status, "cancel_at_period_end": cancelEnd,
		"current_period_start": start, "current_period_end": end,
		"canceled_at": canceled, "customer": "cus_1",
		"items": map[string]any{"data": []any{}},
	}
}

// stubStripeTransport routes every Stripe API call to the stub server. The
// client clones http.DefaultTransport at construction, so the swap must happen
// first; the clone carries the doctored dialers. Both dialers are needed:
// https URLs use DialTLSContext, and the handshake skips verification because
// the test server's certificate is self-signed.
func stubStripeTransport(t *testing.T, handler http.Handler) {
	t.Helper()
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport is not *http.Transport")
	}
	tr := base.Clone()
	addr := ts.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	tr.DialTLSContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		raw, err := new(net.Dialer).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test-only dialer
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		return tlsConn, nil
	}
	old := http.DefaultTransport
	http.DefaultTransport = tr
	t.Cleanup(func() { http.DefaultTransport = old })
}

func pastHour() *time.Time { t := time.Now().Add(-time.Hour).UTC().Truncate(time.Second); return &t }

func TestReconcileDisabledStripeTouchesNothing(t *testing.T) {
	h := testutil.New(t)
	biz := reconcileBusiness(t, h, "Reconcile Disabled Cafe")
	seedSubscription(t, h, biz.ID, "sub_disabled_1", "active", pastHour())

	var hit atomic.Bool
	stubStripeTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	b := reconcileHarness(t, h, security.NewStripeClient("", ""))
	if err := b.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if hit.Load() {
		t.Error("an unconfigured Stripe client made an outbound call")
	}
	if sub := readSubscription(t, h, biz.ID); sub.Status != "active" {
		t.Errorf("status = %q, want the row untouched", sub.Status)
	}
}

func TestReconcileNothingStale(t *testing.T) {
	h := testutil.New(t)
	b := reconcileHarness(t, h, security.NewStripeClient("sk_test_x", ""))
	// No subscriptions at all: the empty set is a success, not an error.
	if err := b.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func TestReconcileAppliesAuthoritativeState(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	bizA := reconcileBusiness(t, h, "Reconcile A")
	bizB := reconcileBusiness(t, h, "Reconcile B")
	bizC := reconcileBusiness(t, h, "Reconcile C")
	bizD := reconcileBusiness(t, h, "Reconcile D")
	bizE := reconcileBusiness(t, h, "Reconcile E")
	bizG := reconcileBusiness(t, h, "Reconcile G")

	past := pastHour()
	seedSubscription(t, h, bizA.ID, "sub_A", "active", past)
	seedSubscription(t, h, bizB.ID, "sub_B", "active", past)
	seedSubscription(t, h, bizC.ID, "sub_C", "active", past)
	seedSubscription(t, h, bizD.ID, "sub_D", "active", past)
	seedSubscription(t, h, bizE.ID, "sub_E", "active", past)
	// Not stale: canceled long ago. The job must leave it alone.
	seedSubscription(t, h, bizG.ID, "sub_G", "canceled", past)

	stub := &stripeStub{subs: map[string]map[string]any{
		// Canceled in Stripe: free plan, stamped, non-granting.
		"sub_A": stripeSubJSON("canceled", true, 1700000000, 1702592000, 1702000000),
		// Still active with a renewed period: the new end must land on the row.
		"sub_B": stripeSubJSON("active", false, 1702592000, 1705276800, 0),
		// A status the CHECK constraint has never heard of: collapse to the
		// non-granting default rather than storing it (the write would fail)
		// or, worse, granting on it.
		"sub_C": stripeSubJSON("paused_expired_future", false, 1702592000, 1705276800, 0),
		// sub_D is absent: Stripe 404s it (deleted dashboard-side).
		"sub_E": stripeSubJSON("past_due", false, 1702592000, 1705276800, 0),
	}}
	stubStripeTransport(t, stub)
	b := reconcileHarness(t, h, security.NewStripeClient("sk_test_x", ""))
	if err := b.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	for _, id := range []string{"sub_A", "sub_B", "sub_C", "sub_E"} {
		if !stub.saw(id) {
			t.Errorf("Stripe was never asked about %s", id)
		}
	}

	subA := readSubscription(t, h, bizA.ID)
	if subA.Status != "canceled" || subA.GrantsEntitlements() {
		t.Errorf("canceled: status = %q grants = %v", subA.Status, subA.GrantsEntitlements())
	}
	if subA.CanceledAt == nil || subA.CanceledAt.Unix() != 1702000000 {
		t.Errorf("canceled_at = %v, want the Stripe timestamp", subA.CanceledAt)
	}
	if !subA.CancelAtPeriodEnd {
		t.Error("cancel_at_period_end was not carried onto the row")
	}
	// The portal still needs the Stripe ids after cancellation.
	if subA.StripeSubscription == nil || *subA.StripeSubscription != "sub_A" {
		t.Errorf("stripe subscription = %v, want it retained", subA.StripeSubscription)
	}

	subB := readSubscription(t, h, bizB.ID)
	if subB.Status != "active" || !subB.GrantsEntitlements() {
		t.Errorf("active: status = %q grants = %v", subB.Status, subB.GrantsEntitlements())
	}
	if subB.CurrentPeriodEnd == nil || subB.CurrentPeriodEnd.Unix() != 1705276800 {
		t.Errorf("current_period_end = %v, want the renewed Stripe period", subB.CurrentPeriodEnd)
	}

	subC := readSubscription(t, h, bizC.ID)
	if subC.Status != "incomplete" || subC.GrantsEntitlements() {
		t.Errorf("unknown status: stored %q grants = %v; want incomplete and non-granting", subC.Status, subC.GrantsEntitlements())
	}

	// The 404 must not abort the batch: E (past_due) was still applied, and D
	// keeps its last known state rather than being zeroed.
	subD := readSubscription(t, h, bizD.ID)
	if subD.Status != "active" || subD.CurrentPeriodEnd == nil || subD.CurrentPeriodEnd.Unix() != past.Unix() {
		t.Errorf("failed subscription was modified: %+v", subD)
	}
	subE := readSubscription(t, h, bizE.ID)
	if subE.Status != "past_due" || !subE.GrantsEntitlements() {
		t.Errorf("past_due: status = %q grants = %v, want the dunning grace preserved", subE.Status, subE.GrantsEntitlements())
	}

	subG := readSubscription(t, h, bizG.ID)
	if subG.Status != "canceled" {
		t.Errorf("non-stale row: status = %q, want it untouched", subG.Status)
	}
	if stub.saw("sub_G") {
		t.Error("Stripe was asked about a subscription whose period had not ended stale")
	}
}

func TestReconcileSkipsRowsWithoutAStripeID(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	biz := reconcileBusiness(t, h, "Reconcile No Stripe ID")

	// The CHECK constraint forbids this shape (active without a Stripe id), so
	// the row is built with the constraint lifted in-transaction: this is the
	// defense-in-depth branch, exercised as the drift it defends against. The
	// rollback at test end restores the constraint.
	h.Exec(t, `ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_needs_stripe_sub`)
	seedSubscription(t, h, biz.ID, "", "active", pastHour())

	stub := &stripeStub{subs: map[string]map[string]any{}}
	stubStripeTransport(t, stub)
	b := reconcileHarness(t, h, security.NewStripeClient("sk_test_x", ""))
	if err := b.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if sub := readSubscription(t, h, biz.ID); sub.Status != "active" {
		t.Errorf("status = %q, want the row skipped untouched", sub.Status)
	}
	stub.mu.Lock()
	n := len(stub.requested)
	stub.mu.Unlock()
	if n != 0 {
		t.Errorf("Stripe was asked %d times for a row with no Stripe id", n)
	}
}

func TestStaleSubscriptionsShape(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	mk := func(name, status string, end *time.Time) string {
		biz := reconcileBusiness(t, h, name)
		seedSubscription(t, h, biz.ID, "sub_"+name, status, end)
		return biz.ID
	}
	future := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	past1 := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	past2 := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	past3 := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)

	// Inserted latest-first so the ORDER BY has something to prove.
	idLatest := mk("stale-latest", "active", &past3)
	idMiddle := mk("stale-middle", "trialing", &past2)
	idOldest := mk("stale-oldest", "past_due", &past1)
	mk("stale-canceled", "canceled", &past1)
	mk("stale-future", "active", &future)
	mk("stale-no-end", "active", nil)

	stale, err := h.Repos.Billing.StaleSubscriptions(ctx, 0)
	if err != nil {
		t.Fatalf("StaleSubscriptions: %v", err)
	}
	if len(stale) != 3 {
		t.Fatalf("stale = %d rows, want only the three ended paid rows", len(stale))
	}
	// Oldest period first.
	if stale[0].BusinessID != idOldest || stale[1].BusinessID != idMiddle || stale[2].BusinessID != idLatest {
		t.Errorf("order = %s, %s, %s; want oldest period first",
			stale[0].BusinessID, stale[1].BusinessID, stale[2].BusinessID)
	}

	limited, err := h.Repos.Billing.StaleSubscriptions(ctx, 2)
	if err != nil {
		t.Fatalf("StaleSubscriptions(2): %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("limit 2 returned %d rows", len(limited))
	}

	// created_at runs opposite to period order here: the latest period has
	// the oldest row. If the query ordered by creation instead of period end,
	// this comes back reversed.
	h.Exec(t, `UPDATE subscriptions SET created_at = now() - ($1 || ' hours')::interval WHERE business_id = $2`,
		"3", idLatest)
	h.Exec(t, `UPDATE subscriptions SET created_at = now() - ($1 || ' hours')::interval WHERE business_id = $2`,
		"2", idMiddle)
	h.Exec(t, `UPDATE subscriptions SET created_at = now() - ($1 || ' hours')::interval WHERE business_id = $2`,
		"1", idOldest)
	ordered, err := h.Repos.Billing.StaleSubscriptions(ctx, 10)
	if err != nil {
		t.Fatalf("StaleSubscriptions: %v", err)
	}
	if len(ordered) != 3 || ordered[0].BusinessID != idOldest ||
		ordered[1].BusinessID != idMiddle || ordered[2].BusinessID != idLatest {
		t.Errorf("order follows creation rather than period end: %v", ordered)
	}
}

func TestNormalizeStripeStatus(t *testing.T) {
	for _, s := range []string{"active", "trialing", "past_due", "canceled", "unpaid", "paused", "incomplete"} {
		if got := normalizeStripeStatus(s); got != s {
			t.Errorf("normalize(%q) = %q", s, got)
		}
	}
	// Unknown collapses to the non-granting default: a status Stripe adds
	// tomorrow must not grant access the day it appears.
	for _, s := range []string{"", "ACTIVE", "expired", "paused_expired_future"} {
		if got := normalizeStripeStatus(s); got != "incomplete" {
			t.Errorf("normalize(%q) = %q, want incomplete", s, got)
		}
	}
}

func TestPlanForStripePrice(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	b := reconcileHarness(t, h, security.NewStripeClient("", ""))
	h.Exec(t, `UPDATE plans SET stripe_price_id = 'price_growth_1' WHERE id = 'growth'`)

	if p, err := b.planForStripePrice(ctx, &security.StripeSubscription{PriceID: "price_growth_1"}); err != nil || p == nil || p.ID != "growth" {
		t.Errorf("known price: %+v, %v; want the growth plan", p, err)
	}
	// An unrecognised price is treated as free, never guessed: a wrong grant
	// is silent and irreversible, while free is visible and fixable.
	if p, err := b.planForStripePrice(ctx, &security.StripeSubscription{PriceID: "price_unknown_9"}); err != nil || p != nil {
		t.Errorf("unknown price: %+v, %v; want nil (free)", p, err)
	}
	// An empty price id matches nothing - including a plan whose operator-set
	// price id is itself empty.
	h.Exec(t, `UPDATE plans SET stripe_price_id = '' WHERE id = 'pro'`)
	if p, err := b.planForStripePrice(ctx, &security.StripeSubscription{}); err != nil || p != nil {
		t.Errorf("empty price: %+v, %v; want nil", p, err)
	}
}

func TestTruncateBounds(t *testing.T) {
	if got := truncate("short", 500); got != "short" {
		t.Errorf("short string was modified: %q", got)
	}
	long := strings.Repeat("x", 600)
	if got := truncate(long, 500); len(got) != 500 {
		t.Errorf("long string truncated to %d, want 500", len(got))
	}
}

func TestPublishableKeyPassthrough(t *testing.T) {
	h := testutil.New(t)
	// The service wrapper must pass the configured key through: the pricing
	// page renders Stripe.js from this value, and an empty one silently
	// disables checkout UI-wide.
	b := reconcileHarness(t, h, security.NewStripeClient("sk_test", "pk_test_123"))
	if got := b.PublishableKey(); got != "pk_test_123" {
		t.Errorf("PublishableKey = %q", got)
	}
	b = reconcileHarness(t, h, security.NewStripeClient("", ""))
	if got := b.PublishableKey(); got != "" {
		t.Errorf("unconfigured PublishableKey = %q, want empty", got)
	}
}
