//go:build !race

package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Billing had no coverage. Checkout and the portal need a Stripe key, so they
// are tested for their refusal shape; the webhook does not - authenticity there
// comes from an HMAC the test can compute - and it is the only path that changes
// subscription state in production.

const testWebhookSecret = "whsec_test_secret"

func billingServer(t *testing.T) (*Server, *testutil.H, *testDeps) {
	t.Helper()
	cfg := testConfig(t)
	cfg.StripeWebhookSecret = testWebhookSecret
	cfg.RateLimitGlobal = 100000
	return newDBTestServerWith(t, cfg)
}

// stripeSignature builds the header Stripe sends: t=<unix>,v1=<hex hmac of
// "<t>.<payload>">.
func stripeSignature(secret string, ts time.Time, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", ts.Unix())
	mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

func webhookReq(body []byte, sig string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhook", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("Stripe-Signature", sig)
	}
	req.RemoteAddr = "203.0.113.77:40000"
	return req
}

func TestBillingPlansIsPublic(t *testing.T) {
	s, _, _ := billingServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authReq(t, http.MethodGet, "/api/v1/plans", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("plans: status = %d: %s", w.Code, w.Body.String())
	}
	// Checkout is not configured in tests, and the payload must say so rather
	// than leaving the client to discover it at the checkout call.
	if !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("plans did not report enabled:false: %s", w.Body.String())
	}
}

func TestBillingWebhookRefusesBadSignatures(t *testing.T) {
	s, _, _ := billingServer(t)
	h := s.Handler()
	body := []byte(`{"id":"evt_sig_1","type":"customer.created","data":{"object":{}}}`)

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"no header", webhookReq(body, "")},
		{"a wrong secret", webhookReq(body, stripeSignature("whsec_wrong", time.Now(), body))},
		{"a tampered body", webhookReq([]byte(`{"id":"evt_sig_2","type":"customer.created"}`),
			stripeSignature(testWebhookSecret, time.Now(), body))},
		{"a stale timestamp", webhookReq(body, stripeSignature(testWebhookSecret, time.Now().Add(-time.Hour), body))},
		{"a malformed header", webhookReq(body, "garbage")},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, tc.req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, w.Code)
		}
	}
	// A refused payload is not evidence of anything, so nothing is recorded.
	var n int
	if err := s.deps.Repos.QueryRow(t.Context(), `SELECT count(*) FROM billing_webhook_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d events were recorded from unverified payloads", n)
	}
}

func TestBillingWebhookIsIdempotent(t *testing.T) {
	s, _, _ := billingServer(t)
	h := s.Handler()
	body := []byte(`{"id":"evt_dup_1","type":"customer.created","data":{"object":{"id":"cus_1"}}}`)
	sig := stripeSignature(testWebhookSecret, time.Now(), body)

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, webhookReq(body, sig))
		// A duplicate must still be a 2xx: Stripe retries until it sees one.
		if w.Code != http.StatusOK {
			t.Fatalf("delivery %d: status = %d: %s", i+1, w.Code, w.Body.String())
		}
	}
	// ...but it is claimed exactly once.
	var n int
	if err := s.deps.Repos.QueryRow(t.Context(),
		`SELECT count(*) FROM billing_webhook_events WHERE provider_event_id='evt_dup_1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("the event was claimed %d times, want 1", n)
	}
}

func TestBillingRequiresManagedBusiness(t *testing.T) {
	s, h, td := billingServer(t)
	handler := s.Handler()
	owner := testutil.User(t, h)
	coOwner := testutil.User(t, h)
	stranger := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Billing Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	// An accepted co-owner may manage the listing, so they may pay for it.
	h.Exec(t, `INSERT INTO business_invites (id, business_id, email, role, token, invited_by, accepted_at, accepted_user_id)
		VALUES ($1,$2,$3,'co_owner',$4,$5,now(),$6)`,
		util.NewUUID(), biz.ID, coOwner.Email, util.NewUUID(), owner.ID, coOwner.ID)

	paths := []string{
		"/api/v1/businesses/" + biz.ID + "/billing",
		"/api/v1/businesses/" + biz.ID + "/billing/invoices",
	}
	for _, path := range paths {
		// Anonymous is unauthenticated, not forbidden.
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, authReq(t, http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: status = %d, want 401", path, w.Code)
		}
		// A perfectly valid account that does not manage the listing is refused.
		if w := dash(t, td, handler, http.MethodGet, path, nil, stranger); w.Code != http.StatusForbidden {
			t.Errorf("%s stranger: status = %d, want 403", path, w.Code)
		}
		// The owner and the co-owner both get through the gate.
		for name, u := range map[string]*domain.User{"owner": owner, "co-owner": coOwner} {
			w := dash(t, td, handler, http.MethodGet, path, nil, u)
			if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
				t.Errorf("%s %s: status = %d, want past the gate", path, name, w.Code)
			}
			if w.Code >= 500 {
				t.Errorf("%s %s: status = %d: %s", path, name, w.Code, w.Body.String())
			}
		}
	}

	// The portal is a POST on the same gate, and it had never been executed.
	// Without a Stripe key the service answers 404 "not configured"; what must
	// not happen is a 500 or a portal URL for a stranger.
	portal := "/api/v1/businesses/" + biz.ID + "/billing/portal"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authReq(t, http.MethodPost, portal, nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("portal anonymous: status = %d, want 401", w.Code)
	}
	if w := dash(t, td, handler, http.MethodPost, portal, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("portal stranger: status = %d, want 403", w.Code)
	}
	for name, u := range map[string]*domain.User{"owner": owner, "co-owner": coOwner} {
		w := dash(t, td, handler, http.MethodPost, portal, nil, u)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not configured") {
			t.Errorf("portal %s: status = %d, want the unconfigured-deployment 404: %s", name, w.Code, w.Body.String())
		}
	}
	// A malformed id cannot name a business. It used to reach a uuid column and
	// surface as a 500 (SQLSTATE 22P02).
	if w := dash(t, td, handler, http.MethodPost,
		"/api/v1/businesses/not-a-uuid/billing/portal", nil, owner); w.Code != http.StatusNotFound {
		t.Errorf("portal malformed id: status = %d, want 404: %s", w.Code, w.Body.String())
	}
}

func TestBillingInvoicesForANewBusiness(t *testing.T) {
	s, h, td := billingServer(t)
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Invoice Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	w := dash(t, td, s.Handler(), http.MethodGet, "/api/v1/businesses/"+biz.ID+"/billing/invoices", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("invoices: status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"invoices"`) {
		t.Errorf("no invoices key: %s", w.Body.String())
	}
}

// Checkout without a Stripe key must refuse cleanly rather than 500 or panic.
func TestBillingCheckoutWithoutStripe(t *testing.T) {
	s, h, td := billingServer(t)
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Checkout Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	w := dash(t, td, s.Handler(), http.MethodPost,
		"/api/v1/businesses/"+biz.ID+"/billing/checkout", map[string]any{"plan_id": "pro"}, owner)
	if w.Code >= 500 {
		t.Errorf("checkout: status = %d: %s", w.Code, w.Body.String())
	}
	if w.Code < 400 {
		t.Errorf("checkout succeeded with no Stripe key: %d", w.Code)
	}
}
