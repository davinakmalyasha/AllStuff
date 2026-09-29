package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

// signStripe builds a valid `Stripe-Signature` header for payload/secret.
func signStripe(payload, secret string, at time.Time) string {
	ts := fmt.Sprintf("%d", at.Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + payload))
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyStripeSignature_Valid(t *testing.T) {
	payload := []byte(`{"id":"evt_1","type":"customer.subscription.updated"}`)
	secret := "whsec_test_secret"
	header := signStripe(string(payload), secret, time.Now())

	if err := VerifyStripeSignature(payload, header, secret, 5*time.Minute); err != nil {
		t.Fatalf("expected valid signature to verify, got %v", err)
	}
}

// The whole point of signing the raw body is that re-encoding changes the
// bytes. A handler that unmarshals then re-marshals before verifying will fail
// here — which is the correct outcome, and the reason the handler passes the
// raw body through.
func TestVerifyStripeSignature_RejectsReorderedBody(t *testing.T) {
	secret := "whsec_test_secret"
	original := []byte(`{"id":"evt_1","type":"a"}`)
	tampered := []byte(`{"type":"a","id":"evt_1"}`)

	header := signStripe(string(original), secret, time.Now())
	if err := VerifyStripeSignature(original, header, secret, 5*time.Minute); err != nil {
		t.Fatalf("original must verify: %v", err)
	}
	if err := VerifyStripeSignature(tampered, header, secret, 5*time.Minute); err == nil {
		t.Fatal("expected a body whose bytes changed to fail verification")
	}
}

func TestVerifyStripeSignature_RejectsWrongSecret(t *testing.T) {
	payload := []byte(`{"id":"evt_1"}`)
	header := signStripe(string(payload), "whsec_real", time.Now())
	if err := VerifyStripeSignature(payload, header, "whsec_attacker", 5*time.Minute); err == nil {
		t.Fatal("expected verification to fail under a different secret")
	}
}

func TestVerifyStripeSignature_RejectsTamperedPayload(t *testing.T) {
	secret := "whsec_test_secret"
	header := signStripe(`{"amount":1}`, secret, time.Now())
	if err := VerifyStripeSignature([]byte(`{"amount":9999}`), header, secret, 5*time.Minute); err == nil {
		t.Fatal("expected a tampered body to fail verification")
	}
}

// A captured (timestamp, body, signature) triple is replayable until the
// tolerance window closes. This asserts the window is actually enforced.
func TestVerifyStripeSignature_RejectsStaleTimestamp(t *testing.T) {
	payload := []byte(`{"id":"evt_1"}`)
	secret := "whsec_test_secret"
	old := time.Now().Add(-10 * time.Minute)
	header := signStripe(string(payload), secret, old)

	if err := VerifyStripeSignature(payload, header, secret, 5*time.Minute); err == nil {
		t.Fatal("expected a 10-minute-old timestamp to be rejected by a 5-minute tolerance")
	}
	if err := VerifyStripeSignature(payload, header, secret, 0); err != nil {
		t.Fatalf("tolerance 0 must disable the window check, got %v", err)
	}
}

// Stripe rotates the endpoint secret and sends several v1 values during the
// overlap. Failing on the first non-matching one would break rotation.
func TestVerifyStripeSignature_AcceptsAnyOfSeveralV1(t *testing.T) {
	payload := []byte(`{"id":"evt_1"}`)
	secret := "whsec_new"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(payload)))
	good := hex.EncodeToString(mac.Sum(nil))

	header := "t=" + ts + ",v1=" + strings.Repeat("0", 64) + ",v1=" + good
	if err := VerifyStripeSignature(payload, header, secret, 5*time.Minute); err != nil {
		t.Fatalf("expected a match on the second v1 to verify, got %v", err)
	}
}

func TestVerifyStripeSignature_MalformedHeaders(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{}`)
	cases := map[string]string{
		"empty":         "",
		"no timestamp":  "v1=abc",
		"no signature":  "t=12345",
		"garbage":       "not-a-header",
		"non-numeric t": "t=abc,v1=abc",
	}
	for name, header := range cases {
		if err := VerifyStripeSignature(payload, header, secret, 5*time.Minute); err == nil {
			t.Errorf("%s: expected malformed header to be rejected", name)
		}
	}
}

func TestVerifyStripeSignature_RejectsWhenSecretUnset(t *testing.T) {
	payload := []byte(`{}`)
	header := signStripe(string(payload), "whsec_x", time.Now())
	// An unconfigured webhook secret must fail closed. Accepting would mean
	// anyone could post subscription state.
	if err := VerifyStripeSignature(payload, header, "", 5*time.Minute); err == nil {
		t.Fatal("expected verification to fail when the webhook secret is unset")
	}
}

func TestParseEvent_RejectsTestModeInProd(t *testing.T) {
	live := []byte(`{"id":"evt_1","type":"x","livemode":true}`)
	if _, err := ParseEvent(live, true); err != nil {
		t.Fatalf("a live-mode event must parse in a live deployment: %v", err)
	}
	// The important half: a replayed test-mode event must not grant paid
	// entitlements in production.
	test := []byte(`{"id":"evt_2","type":"x","livemode":false}`)
	if _, err := ParseEvent(test, true); err == nil {
		t.Fatal("expected a test-mode event to be rejected when live mode is required")
	}
}

func TestParseEvent_RejectsMalformed(t *testing.T) {
	for name, body := range map[string]string{
		"not json": `nope`,
		"no id":    `{"type":"x"}`,
		"no type":  `{"id":"evt_1"}`,
		"empty":    ``,
	} {
		if _, err := ParseEvent([]byte(body), false); err == nil {
			t.Errorf("%s: expected malformed event to be rejected", name)
		}
	}
}

// wrapEvent builds a Stripe event envelope around an object payload, which is
// the shape decoders receive in ev.Data.
func wrapEvent(id, typ, object string) []byte {
	return []byte(fmt.Sprintf(`{"id":%q,"type":%q,"livemode":false,"data":{"object":%s}}`, id, typ, object))
}

// The price id is the only reliable link from a Stripe subscription back to a
// plan; guessing would mis-assign entitlements whenever a business changes plan.
func TestDecodeSubscriptionObject_ExtractsPriceAndBusiness(t *testing.T) {
	body := wrapEvent("evt_1", "customer.subscription.updated", `{
		"id":"sub_1","status":"active","customer":"cus_1",
		"cancel_at_period_end":true,
		"current_period_start":1700000000,"current_period_end":1702592000,
		"metadata":{"business_id":"biz-1"},
		"items":{"data":[{"price":{"id":"price_growth"}}]}}`)

	ev, err := ParseEvent(body, false)
	if err != nil {
		t.Fatal(err)
	}
	sub, businessID, err := ev.DecodeSubscriptionObject()
	if err != nil {
		t.Fatal(err)
	}
	if sub.ID != "sub_1" || sub.Status != "active" || !sub.CancelAtPeriodEnd {
		t.Errorf("unexpected subscription decode: %+v", sub)
	}
	if sub.PriceID != "price_growth" {
		t.Errorf("PriceID = %q, want price_growth", sub.PriceID)
	}
	if businessID != "biz-1" {
		t.Errorf("businessID = %q, want biz-1", businessID)
	}
	if sub.CustomerID != "cus_1" {
		t.Errorf("CustomerID = %q, want cus_1", sub.CustomerID)
	}
}

func TestDecodeInvoiceObject_FallsBackToSubscriptionMetadata(t *testing.T) {
	// Stripe puts business_id on the subscription, not always on the invoice.
	body := wrapEvent("evt_2", "invoice.paid", `{
		"id":"in_1","number":"A-1","status":"paid","amount_paid":1900,
		"currency":"usd","subscription":"sub_1","hosted_invoice_url":"https://x",
		"period_start":1700000000,"period_end":1702592000,
		"subscription_details":{"metadata":{"business_id":"biz-2"}}}`)

	ev, err := ParseEvent(body, false)
	if err != nil {
		t.Fatal(err)
	}
	inv, businessID, err := ev.DecodeInvoiceObject()
	if err != nil {
		t.Fatal(err)
	}
	if businessID != "biz-2" {
		t.Errorf("businessID = %q, want biz-2 (subscription metadata fallback)", businessID)
	}
	if inv.AmountCents != 1900 || inv.Currency != "USD" {
		t.Errorf("unexpected invoice decode: %+v", inv)
	}
	if inv.HostedInvoiceURL != "https://x" {
		t.Errorf("HostedInvoiceURL = %q", inv.HostedInvoiceURL)
	}
}

// An invoice with amount_paid == 0 must fall back to amount_due, otherwise a
// legitimately-unpaid invoice records as a zero-value charge.
func TestDecodeInvoiceObject_FallsBackToAmountDue(t *testing.T) {
	body := wrapEvent("evt_3", "invoice.created", `{
		"id":"in_2","status":"open","amount_paid":0,"amount_due":4900,"currency":"usd"}`)
	ev, err := ParseEvent(body, false)
	if err != nil {
		t.Fatal(err)
	}
	inv, _, err := ev.DecodeInvoiceObject()
	if err != nil {
		t.Fatal(err)
	}
	if inv.AmountCents != 4900 {
		t.Errorf("AmountCents = %d, want 4900", inv.AmountCents)
	}
}
