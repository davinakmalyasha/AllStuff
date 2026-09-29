package security

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stripe — minimal REST client for Billing (Phase 7.1).
//
// Hand-rolled rather than pulling stripe-go, matching the existing
// currency/oauth/push clients: four endpoints, form encoding, and a webhook
// signature check are the entire surface. stripe-go is ~10 MB of transitive
// deps for that.
//
// The API host is a package constant, never configurable. A configurable
// Stripe base URL in an env var is an SSRF primitive: whoever can set env can
// point secret-key requests at a host they control and capture the key.
const stripeAPIHost = "https://api.stripe.com"

const (
	// stripeMaxResponse bounds every response body so a misbehaving or
	// impersonated endpoint cannot exhaust memory.
	stripeMaxResponse = 1 << 20 // 1 MiB
	stripeTimeout     = 15 * time.Second
)

// ErrStripeDisabled is returned when a billing operation is attempted with no
// Stripe key configured. Callers surface it as a 501 rather than a 500.
var ErrStripeDisabled = errors.New("billing: stripe is not configured")

// StripeClient is safe for concurrent use.
type StripeClient struct {
	secretKey      string
	publishableKey string
	http           *http.Client
}

func NewStripeClient(secretKey, publishableKey string) *StripeClient {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 32
	tr.MaxIdleConnsPerHost = 8
	return &StripeClient{
		secretKey:      secretKey,
		publishableKey: publishableKey,
		http:           &http.Client{Timeout: stripeTimeout, Transport: tr},
	}
}

// Enabled reports whether a secret key is configured. When false the Billing
// service still resolves the free plan so local development needs no Stripe
// account.
func (c *StripeClient) Enabled() bool { return c != nil && c.secretKey != "" }

func (c *StripeClient) PublishableKey() string {
	if c == nil {
		return ""
	}
	return c.publishableKey
}

// ---- webhook signature verification ----

// VerifyStripeSignature checks the `Stripe-Signature` header using the
// standard scheme: `t=<unix>,v1=<hex>[,v1=<hex>…]`. The HMAC is over
// "<timestamp>.<raw body>", and the tolerance bounds replay of a captured
// (timestamp, body, signature) triple.
//
// The raw body must be passed unparsed — re-encoding a decoded JSON body
// changes byte-for-byte and invalidates the signature, which is the single
// most common integration bug.
func VerifyStripeSignature(payload []byte, header, secret string, tolerance time.Duration) error {
	if secret == "" {
		return errors.New("billing: stripe webhook secret is not configured")
	}
	if header == "" {
		return errors.New("billing: missing Stripe-Signature header")
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 {
		return errors.New("billing: malformed Stripe-Signature header")
	}
	secs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("billing: malformed timestamp in Stripe-Signature header")
	}
	// The default tolerance is 5 minutes. The window exists to stop a captured
	// request being replayed later; it is not a nonce, so a duplicate delivery
	// inside the window is still accepted — idempotency is the event ledger's
	// job, not this check's.
	if tolerance > 0 {
		age := time.Since(time.Unix(secs, 0))
		if age < 0 {
			age = -age
		}
		if age > tolerance {
			return fmt.Errorf("billing: stripe signature timestamp outside %s tolerance", tolerance)
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	// Constant-time compare against every supplied v1. Stripe sends multiple
	// during secret rotation; failing on the first mismatch would break
	// rotation.
	for _, sig := range sigs {
		if hmac.Equal([]byte(sig), []byte(expected)) {
			return nil
		}
	}
	return errors.New("billing: stripe signature verification failed")
}

// StripeEvent is the subset of the event envelope we consume.
type StripeEvent struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	APIVersion string          `json:"api_version"`
	Created    int64           `json:"created"`
	LiveMode   bool            `json:"livemode"`
	Data       json.RawMessage `json:"data"`
	Object     json.RawMessage `json:"object"`
}

// ---- REST calls ----

// form encodes params as application/x-www-form-urlencoded, which is what
// Stripe's form API expects. Nested values use Stripe's bracket notation.
func form(params map[string]string) io.Reader {
	v := url.Values{}
	for k, val := range params {
		if val != "" {
			v.Set(k, val)
		}
	}
	return strings.NewReader(v.Encode())
}

// do performs an authenticated POST and decodes the JSON response into out.
// It drains the body before Close so the connection returns to the idle pool
// rather than forcing a new TLS handshake on every call.
func (c *StripeClient) do(ctx context.Context, path string, params map[string]string, out any) error {
	if !c.Enabled() {
		return ErrStripeDisabled
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stripeAPIHost+path, form(params))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, stripeMaxResponse))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return stripeError(resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// stripeError surfaces Stripe's own message. The key is a secret and must
// never reach a log line, so only the parsed error fields are included.
func stripeError(status int, body []byte) error {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error.Message != "" {
		return fmt.Errorf("stripe %d (%s/%s): %s", status, e.Error.Type, e.Error.Code, e.Error.Message)
	}
	return fmt.Errorf("stripe %d: request failed", status)
}

// ---- session creation ----

// StripeSession is the returned Checkout/Portal session.
type StripeSession struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Status   string `json:"status"`
	Customer string `json:"customer"`
}

// CheckoutSessionParams describes a Checkout Session creation.
type CheckoutSessionParams struct {
	// PriceID is the Stripe price (plans.stripe_price_id).
	PriceID string
	// BusinessID and OwnerEmail are written to metadata so the webhook can
	// resolve the subscription without trusting anything client-supplied.
	BusinessID string
	OwnerEmail string
	// CustomerID reuses an existing Stripe customer; empty creates one.
	CustomerID string
	// SuccessURL/CancelURL are absolute; PublicURL is used to build them.
	SuccessURL string
	CancelURL  string
	ClientRef  string
}

// CreateCheckoutSession starts a Stripe Checkout in `payment` mode. The
// subscription itself is created by Stripe on completion; we only learn about
// it through the webhook.
func (c *StripeClient) CreateCheckoutSession(ctx context.Context, p CheckoutSessionParams) (*StripeSession, error) {
	params := map[string]string{
		"mode":                    "subscription",
		"line_items[0][price]":    p.PriceID,
		"line_items[0][quantity]": "1",
		"success_url":             p.SuccessURL,
		"cancel_url":              p.CancelURL,
		"metadata[business_id]":   p.BusinessID,
		"client_reference_id":     p.ClientRef,
		"subscription_data[metadata][business_id]": p.BusinessID,
		"subscription_data[metadata][owner_email]": p.OwnerEmail,
	}
	if p.CustomerID != "" {
		params["customer"] = p.CustomerID
	} else if p.OwnerEmail != "" {
		// Stripe matches an existing customer by email so a business cannot
		// accumulate duplicate customers across repeated checkouts.
		params["customer_email"] = p.OwnerEmail
	}
	var out StripeSession
	if err := c.do(ctx, "/v1/checkout/sessions", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePortalSession opens the Stripe customer portal for self-serve
// cancellation, card updates and invoice history.
func (c *StripeClient) CreatePortalSession(ctx context.Context, customerID, returnURL string) (*StripeSession, error) {
	var out StripeSession
	err := c.do(ctx, "/v1/billing_portal/sessions", map[string]string{
		"customer":   customerID,
		"return_url": returnURL,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// StripeSubscription is the subscription fields we persist.
type StripeSubscription struct {
	ID                 string
	Status             string
	CancelAtPeriodEnd  bool
	CurrentPeriodStart int64
	CurrentPeriodEnd   int64
	CanceledAt         int64
	CustomerID         string
	// PriceID is the Stripe price from items.data[0].price.id. This is the
	// authoritative link back to a plan; guessing would be wrong whenever a
	// business changes plan or a second paid plan exists.
	PriceID string
}

// RetrieveSubscription re-reads authoritative state from Stripe. The webhook
// is the primary path; this is the reconciliation fallback for when a delivery
// is lost.
func (c *StripeClient) RetrieveSubscription(ctx context.Context, id string) (*StripeSubscription, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stripeAPIHost+"/v1/subscriptions/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stripe: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, stripeMaxResponse))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, stripeError(resp.StatusCode, body)
	}
	var s struct {
		ID                 string `json:"id"`
		Status             string `json:"status"`
		CancelAtPeriodEnd  bool   `json:"cancel_at_period_end"`
		CurrentPeriodStart int64  `json:"current_period_start"`
		CurrentPeriodEnd   int64  `json:"current_period_end"`
		CanceledAt         int64  `json:"canceled_at"`
		Customer           string `json:"customer"`
		Items              struct {
			Data []struct {
				Price struct {
					ID string `json:"id"`
				} `json:"price"`
			} `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, err
	}
	priceID := ""
	if len(s.Items.Data) > 0 {
		priceID = s.Items.Data[0].Price.ID
	}
	return &StripeSubscription{
		ID:                 s.ID,
		Status:             s.Status,
		CancelAtPeriodEnd:  s.CancelAtPeriodEnd,
		CurrentPeriodStart: s.CurrentPeriodStart,
		CurrentPeriodEnd:   s.CurrentPeriodEnd,
		CanceledAt:         s.CanceledAt,
		CustomerID:         s.Customer,
		PriceID:            priceID,
	}, nil
}

// ParseEvent decodes and validates a webhook envelope. Live/test mismatch is
// rejected: a replayed test-mode event must never grant paid entitlements in a
// live deployment (and vice versa).
func ParseEvent(payload []byte, wantLive bool) (*StripeEvent, error) {
	var ev StripeEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("billing: cannot decode stripe event: %w", err)
	}
	if ev.ID == "" || ev.Type == "" {
		return nil, errors.New("billing: stripe event missing id or type")
	}
	if wantLive && !ev.LiveMode {
		return nil, errors.New("billing: received a test-mode event in a live deployment")
	}
	return &ev, nil
}

// dataObject returns the payload an event's decoders should read.
//
// Stripe wraps the subject in `{"id":..,"object":{..}}` under `data`, so the
// subscription/invoice fields live one level deeper than `ev.Data`. Falling
// back to ev.Data itself keeps the decoders usable against a bare object, which
// is what RetrieveSubscription already has.
func (e *StripeEvent) dataObject() json.RawMessage {
	if len(e.Data) == 0 {
		return nil
	}
	var envelope struct {
		Object json.RawMessage `json:"object"`
	}
	if err := json.Unmarshal(e.Data, &envelope); err == nil && len(envelope.Object) > 0 {
		return envelope.Object
	}
	return e.Data
}

// DecodeSubscriptionObject extracts subscription fields from an event's
// data.object, including the price id so the plan can be resolved exactly
// rather than by guessing.
func (e *StripeEvent) DecodeSubscriptionObject() (*StripeSubscription, string, error) {
	var s struct {
		ID                 string            `json:"id"`
		Status             string            `json:"status"`
		CancelAtPeriodEnd  bool              `json:"cancel_at_period_end"`
		CurrentPeriodStart int64             `json:"current_period_start"`
		CurrentPeriodEnd   int64             `json:"current_period_end"`
		CanceledAt         int64             `json:"canceled_at"`
		Customer           string            `json:"customer"`
		Metadata           map[string]string `json:"metadata"`
		Items              struct {
			Data []struct {
				Price struct {
					ID string `json:"id"`
				} `json:"price"`
			} `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(e.dataObject(), &s); err != nil {
		return nil, "", fmt.Errorf("billing: cannot decode subscription object: %w", err)
	}
	if s.ID == "" {
		return nil, "", errors.New("billing: subscription object has no id")
	}
	priceID := ""
	if len(s.Items.Data) > 0 {
		priceID = s.Items.Data[0].Price.ID
	}
	return &StripeSubscription{
		ID:                 s.ID,
		Status:             s.Status,
		CancelAtPeriodEnd:  s.CancelAtPeriodEnd,
		CurrentPeriodStart: s.CurrentPeriodStart,
		CurrentPeriodEnd:   s.CurrentPeriodEnd,
		CanceledAt:         s.CanceledAt,
		CustomerID:         s.Customer,
		PriceID:            priceID,
	}, s.Metadata["business_id"], nil
}

// DecodeInvoiceObject extracts invoice fields from an event's data.object.
func (e *StripeEvent) DecodeInvoiceObject() (*InvoiceRecord, string, error) {
	var inv struct {
		ID                  string            `json:"id"`
		Number              string            `json:"number"`
		Status              string            `json:"status"`
		Customer            string            `json:"customer"`
		Subscription        string            `json:"subscription"`
		AmountPaid          int64             `json:"amount_paid"`
		AmountDue           int64             `json:"amount_due"`
		Currency            string            `json:"currency"`
		HostedInvoiceURL    string            `json:"hosted_invoice_url"`
		InvoicePDF          string            `json:"invoice_pdf"`
		PeriodStart         int64             `json:"period_start"`
		PeriodEnd           int64             `json:"period_end"`
		Created             int64             `json:"created"`
		Metadata            map[string]string `json:"metadata"`
		SubscriptionDetails struct {
			Metadata map[string]string `json:"metadata"`
		} `json:"subscription_details"`
	}
	if err := json.Unmarshal(e.dataObject(), &inv); err != nil {
		return nil, "", fmt.Errorf("billing: cannot decode invoice object: %w", err)
	}
	if inv.ID == "" {
		return nil, "", errors.New("billing: invoice object has no id")
	}
	amount := inv.AmountPaid
	if amount == 0 {
		amount = inv.AmountDue
	}
	businessID := inv.Metadata["business_id"]
	if businessID == "" {
		businessID = inv.SubscriptionDetails.Metadata["business_id"]
	}
	return &InvoiceRecord{
		ID:               inv.ID,
		Number:           inv.Number,
		Status:           inv.Status,
		SubscriptionID:   inv.Subscription,
		AmountCents:      int(amount),
		Currency:         strings.ToUpper(inv.Currency),
		HostedInvoiceURL: inv.HostedInvoiceURL,
		InvoicePDF:       inv.InvoicePDF,
		PeriodStart:      inv.PeriodStart,
		PeriodEnd:        inv.PeriodEnd,
		Created:          inv.Created,
	}, businessID, nil
}

// InvoiceRecord is the normalized subset of a Stripe invoice we persist.
type InvoiceRecord struct {
	ID               string
	Number           string
	Status           string
	SubscriptionID   string
	AmountCents      int
	Currency         string
	HostedInvoiceURL string
	InvoicePDF       string
	PeriodStart      int64
	PeriodEnd        int64
	Created          int64
}
