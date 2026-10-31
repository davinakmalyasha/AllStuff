package security

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// stripeStub answers every request from a canned response and records what it
// was asked.
//
// The transport is the only seam available. stripeAPIHost is a package constant
// and deliberately not configurable - the comment on it explains why, and it is
// right: a base URL read from the environment is an SSRF primitive aimed
// squarely at the secret key. So a test cannot repoint the URL, and intercepting
// the transport is the alternative.
type stripeStub struct {
	status int
	body   string
	err    error

	mu      sync.Mutex
	req     *http.Request
	rawBody string
}

func (s *stripeStub) RoundTrip(r *http.Request) (*http.Response, error) {
	// A GET carries no body, so this must not assume one.
	var raw string
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
	}
	s.mu.Lock()
	// Clone with the request's own context. Cloning with context.Background()
	// silently drops cancellation, which makes any assertion about the context
	// a statement about this stub rather than about the code under test.
	s.req = r.Clone(r.Context())
	s.rawBody = raw
	s.mu.Unlock()

	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     make(http.Header),
		Request:    r,
	}, nil
}

func (s *stripeStub) request(t *testing.T) *http.Request {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.req == nil {
		t.Fatal("no request reached the transport")
	}
	return s.req
}

func stubClient(stub *stripeStub, secret string) *StripeClient {
	return &StripeClient{secretKey: secret, publishableKey: "pk_test_pub", http: &http.Client{Transport: stub}}
}

// NewStripeClient had never executed. The tuning it applies is the reason the
// fan-out does not open a connection per call, so it is pinned rather than
// assumed.
func TestNewStripeClient(t *testing.T) {
	c := NewStripeClient("sk_test_abc", "pk_test_def")
	if c == nil {
		t.Fatal("got nil")
	}
	if !c.Enabled() {
		t.Error("a client built with a secret key should be enabled")
	}
	if c.PublishableKey() != "pk_test_def" {
		t.Errorf("PublishableKey = %q", c.PublishableKey())
	}
	if c.http == nil || c.http.Timeout != stripeTimeout {
		t.Errorf("http timeout = %v, want %v", c.http.Timeout, stripeTimeout)
	}

	tr, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.http.Transport)
	}
	if tr == http.DefaultTransport {
		t.Error("the default transport was used directly; tuning it would mutate the process-wide one")
	}
	if tr.MaxIdleConns != 32 || tr.MaxIdleConnsPerHost != 8 {
		t.Errorf("idle conns = %d/%d, want 32/8", tr.MaxIdleConns, tr.MaxIdleConnsPerHost)
	}
}

// Stripe is optional: with no key the Billing service still resolves the free
// plan, so every method has to survive a nil client rather than only an
// unconfigured one.
func TestStripeClientDisabledAndNil(t *testing.T) {
	var nilClient *StripeClient
	if nilClient.Enabled() {
		t.Error("a nil client must not report itself enabled")
	}
	if got := nilClient.PublishableKey(); got != "" {
		t.Errorf("nil client PublishableKey = %q, want empty", got)
	}

	unconfigured := NewStripeClient("", "")
	if unconfigured.Enabled() {
		t.Error("a client with no secret key must not report itself enabled")
	}

	// The nil client must be usable without a panic, and must not attempt a
	// request. This is the path the app takes when STRIPE_SECRET_KEY is unset.
	stub := &stripeStub{status: 200, body: `{}`}
	var c *StripeClient
	if err := c.do(context.Background(), "/v1/checkout/sessions", nil, nil); !errors.Is(err, ErrStripeDisabled) {
		t.Errorf("nil client do: err = %v, want ErrStripeDisabled", err)
	}
	if _, err := unconfigured.CreateCheckoutSession(context.Background(), CheckoutSessionParams{PriceID: "p"}); !errors.Is(err, ErrStripeDisabled) {
		t.Errorf("unconfigured CreateCheckoutSession: err = %v, want ErrStripeDisabled", err)
	}
	_ = stub
}

func TestForm(t *testing.T) {
	read := func(params map[string]string) string {
		b, err := io.ReadAll(form(params))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	if got := read(nil); got != "" {
		t.Errorf("nil params = %q, want empty", got)
	}
	if got := read(map[string]string{}); got != "" {
		t.Errorf("empty params = %q, want empty", got)
	}

	// Empty values are dropped rather than sent as `k=`. An empty string is
	// meaningful to some Stripe fields and not to others, so the client's choice
	// is to omit - which means "unset" travels as absent, not as blank.
	if got := read(map[string]string{"a": "", "b": "  "}); got != "b=++" {
		t.Errorf("got %q; empty values should be dropped and spaces encoded", got)
	}

	// Order is deterministic because url.Values.Encode sorts, which is what makes
	// a recorded body usable in an assertion.
	if got := read(map[string]string{"b": "2", "a": "1"}); got != "a=1&b=2" {
		t.Errorf("got %q, want a=1&b=2", got)
	}

	// Stripe's bracket notation must survive encoding: the brackets and the
	// dots are what express nesting to their form parser.
	got := read(map[string]string{
		"line_items[0][price]": "price_123",
		"customer_email":       "a+b@example.com",
	})
	if !strings.Contains(got, "line_items%5B0%5D%5Bprice%5D=price_123") {
		t.Errorf("bracket notation was not encoded as expected: %q", got)
	}
	if !strings.Contains(got, "customer_email=a%2Bb%40example.com") {
		t.Errorf("the email was not percent-encoded: %q", got)
	}
}

// stripeError must not echo the response body. The client's key is a secret, and
// a body that failed to parse as JSON is exactly the body most likely to contain
// it - an HTML error page, a proxy's echo of the request, a truncated response.
func TestStripeErrorDoesNotEchoTheBody(t *testing.T) {
	t.Run("parses Stripe's own error shape", func(t *testing.T) {
		err := stripeError(402, []byte(`{"error":{"type":"card_error","code":"card_declined","message":"Your card was declined.","param":"card"}}`))
		if err == nil {
			t.Fatal("want an error")
		}
		msg := err.Error()
		for _, want := range []string{"402", "card_error", "card_declined", "Your card was declined."} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q should contain %q", msg, want)
			}
		}
	})

	t.Run("falls back without the body", func(t *testing.T) {
		for _, body := range []string{
			"",
			"not json",
			`{"error":{"type":"api_error"}}`, // no message
			`{"unexpected":"shape"}`,
			`{"error":null}`,
		} {
			err := stripeError(500, []byte(body))
			if err == nil {
				t.Fatalf("body %q: want an error", body)
			}
			if !strings.Contains(err.Error(), "500") {
				t.Errorf("body %q: error %q should name the status", body, err)
			}
			if strings.Contains(err.Error(), "not json") {
				t.Errorf("body %q: the raw body reached the error message", body)
			}
		}
	})

	t.Run("a secret-looking body is not echoed", func(t *testing.T) {
		// The failure this guards: an unparseable body that happens to contain
		// the key, turning a log line into a credential leak.
		for _, leak := range []string{
			"sk_live_51H8xQ2eZvKYlo2C", //gitleaks:allow -- a fake, and shaped like a real key because that is the point
			"whsec_abcdef0123456789",
			"<html><body>Authorization: Bearer sk_live_leaked</body></html>",
		} {
			msg := stripeError(401, []byte(leak)).Error()
			if strings.Contains(msg, "sk_live") || strings.Contains(msg, "whsec_") || strings.Contains(msg, "leaked") {
				t.Errorf("the error echoed the body: %q", msg)
			}
		}
	})
}

func TestStripeDo(t *testing.T) {
	t.Run("posts with auth and form encoding", func(t *testing.T) {
		stub := &stripeStub{status: 200, body: `{"id":"cs_1"}`}
		c := stubClient(stub, "sk_test_secret")

		var out StripeSession
		if err := c.do(context.Background(), "/v1/checkout/sessions", map[string]string{"a": "1"}, &out); err != nil {
			t.Fatalf("do: %v", err)
		}
		if out.ID != "cs_1" {
			t.Errorf("decoded id = %q, want cs_1", out.ID)
		}

		req := stub.request(t)
		if req.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", req.Method)
		}
		if got := req.URL.String(); got != stripeAPIHost+"/v1/checkout/sessions" {
			t.Errorf("url = %q", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer sk_test_secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", got)
		}
		if stub.rawBody != "a=1" {
			t.Errorf("body = %q, want a=1", stub.rawBody)
		}
	})

	t.Run("a nil out decodes nothing and does not fail", func(t *testing.T) {
		stub := &stripeStub{status: 200, body: `{"id":"cs_1"}`}
		c := stubClient(stub, "sk_test_secret")
		if err := c.do(context.Background(), "/v1/x", nil, nil); err != nil {
			t.Errorf("do with a nil out: %v", err)
		}
	})

	t.Run("a 4xx becomes a Stripe error, not a decode failure", func(t *testing.T) {
		stub := &stripeStub{status: 402, body: `{"error":{"type":"card_error","code":"card_declined","message":"declined"}}`}
		c := stubClient(stub, "sk_test_secret")

		var out StripeSession
		err := c.do(context.Background(), "/v1/x", nil, &out)
		if err == nil {
			t.Fatal("want an error")
		}
		if !strings.Contains(err.Error(), "card_declined") {
			t.Errorf("err = %v, want Stripe's own message", err)
		}
	})

	t.Run("a transport failure is wrapped, not swallowed", func(t *testing.T) {
		stub := &stripeStub{err: errors.New("dial tcp: connection refused")}
		c := stubClient(stub, "sk_test_secret")
		err := c.do(context.Background(), "/v1/x", nil, nil)
		if err == nil {
			t.Fatal("want an error")
		}
		if !strings.Contains(err.Error(), "stripe:") {
			t.Errorf("err = %v, want the stripe: prefix", err)
		}
	})

	t.Run("an oversized response is bounded", func(t *testing.T) {
		// The cap exists so an impersonated or misbehaving endpoint cannot
		// exhaust memory. A body past the limit is truncated, so the JSON fails
		// to parse - an error rather than a large allocation.
		stub := &stripeStub{status: 200, body: `{"id":"` + strings.Repeat("x", stripeMaxResponse+1024) + `"}`}
		c := stubClient(stub, "sk_test_secret")

		var out StripeSession
		if err := c.do(context.Background(), "/v1/x", nil, &out); err == nil {
			t.Error("a response past the cap should not decode cleanly")
		}
	})

	t.Run("an undeserialisable success body is an error", func(t *testing.T) {
		stub := &stripeStub{status: 200, body: "<html>not json</html>"}
		c := stubClient(stub, "sk_test_secret")
		var out StripeSession
		if err := c.do(context.Background(), "/v1/x", nil, &out); err == nil {
			t.Error("want a decode error")
		}
	})
}

func TestCreateCheckoutSession(t *testing.T) {
	base := CheckoutSessionParams{
		PriceID:    "price_123",
		BusinessID: "biz_9",
		OwnerEmail: "owner@example.com",
		SuccessURL: "https://allstuff.test/ok",
		CancelURL:  "https://allstuff.test/no",
		ClientRef:  "ref_1",
	}

	run := func(t *testing.T, p CheckoutSessionParams) (*StripeSession, map[string]string) {
		t.Helper()
		stub := &stripeStub{status: 200, body: `{"id":"cs_1","url":"https://checkout.stripe.com/x","status":"open","customer":"cus_1"}`}
		c := stubClient(stub, "sk_test_secret")
		got, err := c.CreateCheckoutSession(context.Background(), p)
		if err != nil {
			t.Fatalf("CreateCheckoutSession: %v", err)
		}
		req := stub.request(t)
		if req.URL.Path != "/v1/checkout/sessions" {
			t.Errorf("path = %s", req.URL.Path)
		}
		fields := map[string]string{}
		for _, kv := range strings.Split(stub.rawBody, "&") {
			k, v, _ := strings.Cut(kv, "=")
			fields[k] = v
		}
		return got, fields
	}

	t.Run("decodes the session", func(t *testing.T) {
		got, _ := run(t, base)
		if got.ID != "cs_1" || got.URL == "" || got.Status != "open" || got.Customer != "cus_1" {
			t.Errorf("session = %#v", got)
		}
	})

	t.Run("sends what the webhook will need", func(t *testing.T) {
		// BusinessID travels in metadata on both the session and the
		// subscription_data, because the subscription outlives the session.
		_, f := run(t, base)
		want := map[string]string{
			"mode":                            "subscription",
			"line_items%5B0%5D%5Bprice%5D":    "price_123",
			"line_items%5B0%5D%5Bquantity%5D": "1",
			"success_url":                     "https%3A%2F%2Fallstuff.test%2Fok",
			"cancel_url":                      "https%3A%2F%2Fallstuff.test%2Fno",
			"metadata%5Bbusiness_id%5D":       "biz_9",
			"client_reference_id":             "ref_1",
			"subscription_data%5Bmetadata%5D%5Bbusiness_id%5D": "biz_9",
			"subscription_data%5Bmetadata%5D%5Bowner_email%5D": "owner%40example.com",
		}
		for k, v := range want {
			if f[k] != v {
				t.Errorf("%s = %q, want %q", k, f[k], v)
			}
		}
	})

	t.Run("an existing customer is reused", func(t *testing.T) {
		p := base
		p.CustomerID = "cus_existing"
		_, f := run(t, p)
		if f["customer"] != "cus_existing" {
			t.Errorf("customer = %q, want cus_existing", f["customer"])
		}
		if _, ok := f["customer_email"]; ok {
			t.Error("an existing customer must not also send customer_email; Stripe rejects the pair")
		}
	})

	t.Run("an email is used when there is no customer", func(t *testing.T) {
		// Stripe matches an existing customer by email, so a business cannot
		// accumulate duplicate customers across repeated checkouts.
		_, f := run(t, base)
		if f["customer_email"] != "owner%40example.com" {
			t.Errorf("customer_email = %q", f["customer_email"])
		}
		if _, ok := f["customer"]; ok {
			t.Error("customer should not be sent when only an email is known")
		}
	})

	t.Run("neither is sent when neither is known", func(t *testing.T) {
		p := base
		p.OwnerEmail = ""
		_, f := run(t, p)
		if _, ok := f["customer"]; ok {
			t.Error("customer was sent with no customer id")
		}
		if _, ok := f["customer_email"]; ok {
			t.Error("customer_email was sent with no email")
		}
	})

	t.Run("a Stripe error propagates", func(t *testing.T) {
		stub := &stripeStub{status: 400, body: `{"error":{"type":"invalid_request_error","code":"resource_missing","message":"No such price"}}`}
		c := stubClient(stub, "sk_test_secret")
		got, err := c.CreateCheckoutSession(context.Background(), base)
		if err == nil {
			t.Fatal("want an error")
		}
		if got != nil {
			t.Errorf("session = %#v, want nil on error", got)
		}
		if !strings.Contains(err.Error(), "No such price") {
			t.Errorf("err = %v, want Stripe's message", err)
		}
	})

	t.Run("a disabled client refuses before any request", func(t *testing.T) {
		stub := &stripeStub{status: 200, body: `{}`}
		c := stubClient(stub, "")
		if _, err := c.CreateCheckoutSession(context.Background(), base); !errors.Is(err, ErrStripeDisabled) {
			t.Errorf("err = %v, want ErrStripeDisabled", err)
		}
		stub.mu.Lock()
		sent := stub.req
		stub.mu.Unlock()
		if sent != nil {
			t.Error("a request was made for a disabled client")
		}
	})
}

func TestCreatePortalSession(t *testing.T) {
	stub := &stripeStub{status: 200, body: `{"id":"bps_1","url":"https://billing.stripe.com/x"}`}
	c := stubClient(stub, "sk_test_secret")

	got, err := c.CreatePortalSession(context.Background(), "cus_1", "https://allstuff.test/return")
	if err != nil {
		t.Fatalf("CreatePortalSession: %v", err)
	}
	if got.ID != "bps_1" || got.URL == "" {
		t.Errorf("session = %#v", got)
	}
	req := stub.request(t)
	if req.URL.Path != "/v1/billing_portal/sessions" {
		t.Errorf("path = %s", req.URL.Path)
	}
	if !strings.Contains(stub.rawBody, "customer=cus_1") {
		t.Errorf("body = %q, want the customer id", stub.rawBody)
	}
	if !strings.Contains(stub.rawBody, "return_url=") {
		t.Errorf("body = %q, want the return url", stub.rawBody)
	}
}

// RetrieveSubscription is the reconciliation fallback when a webhook delivery is
// lost, so it reads state Stripe considers authoritative. It had never executed.
func TestRetrieveSubscription(t *testing.T) {
	const body = `{
	  "id":"sub_1","status":"active","cancel_at_period_end":true,
	  "current_period_start":1700000000,"current_period_end":1702600000,"canceled_at":0,
	  "customer":"cus_1",
	  "items":{"data":[{"price":{"id":"price_abc"}},{"price":{"id":"price_second"}}]}
	}`

	stub := &stripeStub{status: 200, body: body}
	c := stubClient(stub, "sk_test_secret")

	got, err := c.RetrieveSubscription(context.Background(), "sub_1")
	if err != nil {
		t.Fatalf("RetrieveSubscription: %v", err)
	}
	if got.ID != "sub_1" || got.Status != "active" || !got.CancelAtPeriodEnd {
		t.Errorf("subscription = %#v", got)
	}
	if got.CurrentPeriodEnd != 1702600000 || got.CustomerID != "cus_1" {
		t.Errorf("subscription = %#v", got)
	}
	// The first item's price is the authoritative link back to a plan. Taking
	// the second would be wrong whenever a business has more than one item.
	if got.PriceID != "price_abc" {
		t.Errorf("PriceID = %q, want price_abc (the first item)", got.PriceID)
	}

	req := stub.request(t)
	if req.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", req.Method)
	}
	if req.URL.Path != "/v1/subscriptions/sub_1" {
		t.Errorf("path = %s", req.URL.Path)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk_test_secret" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestRetrieveSubscriptionEdgeCases(t *testing.T) {
	t.Run("an id needing escaping is escaped", func(t *testing.T) {
		// The id reaches a URL path. Unescaped, a crafted id could add path
		// segments and address a different endpoint.
		//
		// Asserted on EscapedPath, not Path. Go parses %2F back into Path, so
		// Path legitimately shows the slashes - it is the decoded view. What
		// crosses the wire is EscapedPath, and that is what has to keep the id
		// as a single segment.
		stub := &stripeStub{status: 200, body: `{"id":"x"}`}
		c := stubClient(stub, "sk_test_secret")
		if _, err := c.RetrieveSubscription(context.Background(), "sub/../customers/evil"); err != nil {
			t.Fatalf("err = %v", err)
		}
		esc := stub.request(t).URL.EscapedPath()
		if strings.Contains(esc, "/customers/") {
			t.Errorf("the id added a path segment on the wire: %s", esc)
		}
		if !strings.HasSuffix(esc, "sub%2F..%2Fcustomers%2Fevil") {
			t.Errorf("the id was not sent as one escaped segment: %s", esc)
		}
	})

	t.Run("no items means no price", func(t *testing.T) {
		stub := &stripeStub{status: 200, body: `{"id":"sub_2","status":"canceled","items":{"data":[]}}`}
		c := stubClient(stub, "sk_test_secret")
		got, err := c.RetrieveSubscription(context.Background(), "sub_2")
		if err != nil {
			t.Fatal(err)
		}
		if got.PriceID != "" {
			t.Errorf("PriceID = %q, want empty", got.PriceID)
		}
	})

	t.Run("a 404 is a Stripe error", func(t *testing.T) {
		stub := &stripeStub{status: 404, body: `{"error":{"type":"invalid_request_error","code":"resource_missing","message":"No such subscription"}}`}
		c := stubClient(stub, "sk_test_secret")
		got, err := c.RetrieveSubscription(context.Background(), "sub_gone")
		if err == nil {
			t.Fatal("want an error")
		}
		if got != nil {
			t.Errorf("subscription = %#v, want nil", got)
		}
		if !strings.Contains(err.Error(), "No such subscription") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a transport failure is wrapped", func(t *testing.T) {
		stub := &stripeStub{err: errors.New("timeout")}
		c := stubClient(stub, "sk_test_secret")
		if _, err := c.RetrieveSubscription(context.Background(), "sub_1"); err == nil || !strings.Contains(err.Error(), "stripe:") {
			t.Errorf("err = %v, want the stripe: prefix", err)
		}
	})

	t.Run("an undeserialisable body is an error", func(t *testing.T) {
		stub := &stripeStub{status: 200, body: `{"id":`}
		c := stubClient(stub, "sk_test_secret")
		if _, err := c.RetrieveSubscription(context.Background(), "sub_1"); err == nil {
			t.Error("want a decode error")
		}
	})

	t.Run("the request honours context cancellation", func(t *testing.T) {
		stub := &stripeStub{status: 200, body: `{}`}
		c := stubClient(stub, "sk_test_secret")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.RetrieveSubscription(ctx, "sub_1"); err != nil {
			// The stub ignores the context; what matters is that the request
			// carries it. Asserted below instead of on the error.
			t.Logf("stub returned %v (the transport does not honour ctx)", err)
		}
		if got := stub.request(t).Context().Err(); !errors.Is(got, context.Canceled) {
			t.Errorf("request context = %v, want it to carry cancellation", got)
		}
	})
}
