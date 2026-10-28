//go:build !race

package httpapi

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// The OAuth handlers had never been executed. The service layer was covered
// through an injected transport, so what was left untested is the browser
// contract: the state nonce, where Google sends the browser back to, what the
// callback does with a bad or replayed state, and which cookies survive each
// exit. Those are the parts an attacker interacts with.

const (
	oauthStartPath    = "/api/v1/auth/oauth/google"
	oauthCallbackPath = "/api/v1/auth/oauth/google/callback"
)

func oauthTestServer(t *testing.T, clientID, secret string) (*Server, *testutil.H, *testDeps) {
	t.Helper()
	cfg := testConfig(t)
	cfg.GoogleOAuthClientID = clientID
	cfg.GoogleOAuthSecret = secret
	cfg.CookieSecure = true
	cfg.RateLimitGlobal = 100000
	return newDBTestServerWith(t, cfg)
}

type googleTransport func(*http.Request) (*http.Response, error)

func (f googleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func googleJSON(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// stubGoogle replaces the process-wide transport for one test. Only Google
// hosts are answered; anything else falls through to the real transport so an
// unrelated outbound call in the same test is not silently broken.
func stubGoogle(t *testing.T, fn googleTransport) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = googleTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "googleapis.com") {
			return fn(r)
		}
		return old.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = old })
}

func googleUser(email string, verified bool) googleTransport {
	return func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "oauth2.googleapis.com") {
			return googleJSON(200, `{"access_token":"at-123"}`), nil
		}
		return googleJSON(200, fmt.Sprintf(
			`{"email":%q,"email_verified":%v,"name":"Google Person"}`, email, verified)), nil
	}
}

func oauthStart(t *testing.T, hdl http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, httptest.NewRequest(http.MethodGet, oauthStartPath, nil))
	return w
}

// oauthCallback builds the request Google would send: a state parameter and the
// matching host-only cookie set by the start handler.
func oauthCallback(t *testing.T, hdl http.Handler, cookieState, queryState, code string) *httptest.ResponseRecorder {
	t.Helper()
	target := oauthCallbackPath + "?state=" + url.QueryEscape(queryState)
	if code != "" {
		target += "&code=" + url.QueryEscape(code)
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if cookieState != "" {
		req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: cookieState})
	}
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, req)
	return w
}

func oauthCookie(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func requireOAuthRedirect(t *testing.T, w *httptest.ResponseRecorder, want string) {
	t.Helper()
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

// requireStateCleared asserts the state cookie is expired in this same
// response. It must be set before http.Redirect writes the header, or it is
// dropped - which is exactly the bug this guards.
func requireStateCleared(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	c := oauthCookie(t, w, oauthStateCookie)
	if c == nil {
		t.Fatal("the response does not clear the OAuth state cookie")
	}
	if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("state cookie = value %q, max-age %d; want an expired cookie", c.Value, c.MaxAge)
	}
}

func requireNoSession(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for _, name := range []string{cookieAccess, cookieRefresh, cookieCSRF} {
		if c := oauthCookie(t, w, name); c != nil {
			t.Errorf("a failed callback issued %s=%q", name, c.Value)
		}
	}
}

func TestOAuthHandlersDisabled(t *testing.T) {
	// Enabled requires both halves. A half-configured provider must fail closed
	// instead of building a consent URL with an empty credential.
	cases := []struct{ name, clientID, secret string }{
		{"neither credential", "", ""},
		{"client id only", "client-id", ""},
		{"secret only", "", "client-secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := oauthTestServer(t, tc.clientID, tc.secret)
			hdl := s.Handler()
			outbound := false
			stubGoogle(t, func(*http.Request) (*http.Response, error) {
				outbound = true
				return googleJSON(200, `{}`), nil
			})

			w := oauthStart(t, hdl)
			// The button is a plain link, so an unconfigured provider redirects
			// back to login with the reason rather than rendering a JSON 400.
			if w.Code != http.StatusFound {
				t.Fatalf("start: status = %d, want 302: %s", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Location"); got != "/login?error=oauth_disabled" {
				t.Errorf("Location = %q, want the disabled-provider login redirect", got)
			}
			if c := oauthCookie(t, w, oauthStateCookie); c != nil {
				t.Errorf("a disabled provider set a state cookie: %+v", c)
			}

			w = oauthCallback(t, hdl, "state", "state", "code")
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not configured") {
				t.Errorf("callback: status = %d, body = %s; want the not-configured 400", w.Code, w.Body.String())
			}
			if outbound {
				t.Error("a disabled provider made an outbound token request")
			}
		})
	}
}

func TestOAuthStartRedirectsToGoogle(t *testing.T) {
	s, _, _ := oauthTestServer(t, "client-id", "client-secret")
	w := oauthStart(t, s.Handler())
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302: %s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Scheme != "https" || loc.Host != "accounts.google.com" || loc.Path != "/o/oauth2/v2/auth" {
		t.Fatalf("consent URL = %s", loc)
	}
	q := loc.Query()
	for k, want := range map[string]string{
		"client_id":     "client-id",
		"redirect_uri":  "https://bizverse.test/api/v1/auth/oauth/google/callback",
		"response_type": "code",
		"scope":         "openid email profile",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	state := q.Get("state")
	if state == "" {
		t.Fatal("consent URL has no state")
	}
	raw, err := base64.RawURLEncoding.DecodeString(state)
	if err != nil || len(raw) != 24 {
		t.Errorf("state = %q decodes to %d bytes (err %v), want 24 random bytes", state, len(raw), err)
	}

	c := oauthCookie(t, w, oauthStateCookie)
	if c == nil {
		t.Fatal("start did not set the state cookie")
	}
	if c.Value != state {
		t.Errorf("state cookie = %q, URL state = %q", c.Value, state)
	}
	if c.Path != "/" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 600 {
		t.Errorf("state cookie attributes = %+v", c)
	}
}

func TestOAuthCallbackRejectsBadState(t *testing.T) {
	s, _, _ := oauthTestServer(t, "client-id", "client-secret")
	hdl := s.Handler()

	cases := []struct{ name, cookie, state string }{
		{"no cookie", "", "state"},
		{"no query state", "state", ""},
		{"mismatched state", "state-a", "state-b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outbound := false
			stubGoogle(t, func(*http.Request) (*http.Response, error) {
				outbound = true
				return googleJSON(200, `{}`), nil
			})
			w := oauthCallback(t, hdl, tc.cookie, tc.state, "code")
			requireOAuthRedirect(t, w, "/login?error=oauth_state")
			requireNoSession(t, w)
			// The state is a one-shot nonce. Leaving it behind lets a later
			// callback reuse it, and the old comment claimed this was cleared
			// while the deferred write happened after the redirect had flushed.
			requireStateCleared(t, w)
			if outbound {
				t.Error("a rejected state reached the token endpoint")
			}
		})
	}

	// An empty cookie must not match an empty query state. Without the explicit
	// emptiness check the comparison is "" == "", and the callback would
	// exchange the code as if a state had been issued.
	outbound := false
	stubGoogle(t, func(*http.Request) (*http.Response, error) {
		outbound = true
		return googleJSON(200, `{}`), nil
	})
	req := httptest.NewRequest(http.MethodGet, oauthCallbackPath+"?state=&code=the-code", nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: ""})
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, req)
	requireOAuthRedirect(t, w, "/login?error=oauth_state")
	requireNoSession(t, w)
	requireStateCleared(t, w)
	if outbound {
		t.Error("an empty state cookie reached the token endpoint")
	}
}

// A ResponseRecorder accepts Set-Cookie even after WriteHeader, but a real
// server does not: http.Redirect flushes the header, so anything added later is
// silently dropped. This test therefore goes over a socket, which is the only
// way to prove the clear happens BEFORE the redirect rather than merely
// appearing in the handler's header map.
func TestOAuthStateCookieClearedOnTheWire(t *testing.T) {
	s, _, _ := oauthTestServer(t, "client-id", "client-secret")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequest(http.MethodGet, srv.URL+oauthCallbackPath+"?state=wrong&code=the-code", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: "right"})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/login?error=oauth_state" {
		t.Fatalf("Location = %q", got)
	}
	var cleared bool
	for _, c := range resp.Cookies() {
		if c.Name == oauthStateCookie {
			cleared = c.Value == "" && c.MaxAge < 0
		}
	}
	if !cleared {
		t.Error("the state cookie was not expired on the wire (a Set-Cookie after http.Redirect is dropped)")
	}
}

func TestOAuthCallbackRequiresCode(t *testing.T) {
	s, _, _ := oauthTestServer(t, "client-id", "client-secret")
	hdl := s.Handler()
	outbound := false
	stubGoogle(t, func(*http.Request) (*http.Response, error) {
		outbound = true
		return googleJSON(200, `{}`), nil
	})

	req := httptest.NewRequest(http.MethodGet,
		oauthCallbackPath+"?state=state&error=access_denied", nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: "state"})
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, req)
	requireOAuthRedirect(t, w, "/login?error=oauth_denied")
	requireNoSession(t, w)
	requireStateCleared(t, w)
	if outbound {
		t.Error("a denied consent reached the token endpoint")
	}
}

func TestOAuthCallbackExchangeFailures(t *testing.T) {
	s, _, _ := oauthTestServer(t, "client-id", "client-secret")
	hdl := s.Handler()

	cases := []struct {
		name string
		stub googleTransport
	}{
		{"transport failure", func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection reset")
		}},
		{"token endpoint refuses the code", func(*http.Request) (*http.Response, error) {
			return googleJSON(400, `{"error":"invalid_grant"}`), nil
		}},
		{"token response has no access token", func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Host, "oauth2.googleapis.com") {
				return googleJSON(200, `{"token_type":"Bearer"}`), nil
			}
			return googleJSON(200, `{"email":"x@example.test","email_verified":true}`), nil
		}},
		{"userinfo has no email", func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Host, "oauth2.googleapis.com") {
				return googleJSON(200, `{"access_token":"at"}`), nil
			}
			return googleJSON(200, `{"email_verified":true}`), nil
		}},
		{"Google reports the email unverified", googleUser("unverified@example.test", false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubGoogle(t, tc.stub)
			w := oauthCallback(t, hdl, "state", "state", "the-code")
			requireOAuthRedirect(t, w, "/login?error=oauth_failed")
			requireNoSession(t, w)
			requireStateCleared(t, w)
		})
	}
}

func TestOAuthCallbackCreatesAccount(t *testing.T) {
	s, h, _ := oauthTestServer(t, "client-id", "client-secret")
	hdl := s.Handler()
	const email = "new.google@example.test"

	var tokenForm url.Values
	stubGoogle(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "oauth2.googleapis.com") {
			b, _ := io.ReadAll(r.Body)
			tokenForm, _ = url.ParseQuery(string(b))
			return googleJSON(200, `{"access_token":"at-123"}`), nil
		}
		return googleJSON(200, `{"email":"new.google@example.test","email_verified":true,"name":"New Google"}`), nil
	})

	w := oauthCallback(t, hdl, "state-1", "state-1", "the-code")
	requireOAuthRedirect(t, w, "/me")
	requireStateCleared(t, w)

	// The code goes to Google unchanged, with the same redirect URI used to
	// start the flow. A mismatch here is an invalid_grant from Google.
	if got := tokenForm.Get("code"); got != "the-code" {
		t.Errorf("token code = %q, want the callback's code", got)
	}
	if got := tokenForm.Get("redirect_uri"); got != "https://bizverse.test/api/v1/auth/oauth/google/callback" {
		t.Errorf("token redirect_uri = %q", got)
	}

	for _, name := range []string{cookieAccess, cookieRefresh, cookieCSRF} {
		if c := oauthCookie(t, w, name); c == nil || c.Value == "" {
			t.Errorf("successful callback did not set %s", name)
		}
	}
	if c := oauthCookie(t, w, cookieAccess); c != nil && (!c.HttpOnly || !c.Secure) {
		t.Errorf("access cookie attributes = %+v", c)
	}
	if c := oauthCookie(t, w, cookieRefresh); c != nil && c.SameSite != http.SameSiteStrictMode {
		t.Errorf("refresh cookie SameSite = %v, want Strict", c.SameSite)
	}
	if c := oauthCookie(t, w, cookieCSRF); c != nil && c.HttpOnly {
		t.Error("the CSRF cookie is HttpOnly; the SPA must be able to echo it")
	}

	u, err := h.Repos.Users.GetByEmail(t.Context(), email)
	if err != nil || u == nil {
		t.Fatalf("the Google account was not created: %+v, %v", u, err)
	}
	if u.EmailVerifiedAt == nil {
		t.Error("the new account is not email-verified")
	}
	if u.Name != "New Google" || u.Username == "" || u.Role != domain.RoleUser {
		t.Errorf("created user = %+v", u)
	}
	var sessions int
	if err := h.QueryRow(t, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.ID).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 1 {
		t.Errorf("sessions = %d, want 1", sessions)
	}

	// A second sign-in with the same verified Google address links to the same
	// account rather than creating a duplicate.
	w = oauthCallback(t, hdl, "state-2", "state-2", "the-code")
	requireOAuthRedirect(t, w, "/me")
	requireStateCleared(t, w)
	var users int
	if err := h.QueryRow(t, `SELECT count(*) FROM users WHERE email = $1`, email).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 1 {
		t.Errorf("users with %s = %d, want 1", email, users)
	}
}

func TestOAuthCallback2FA(t *testing.T) {
	s, h, _ := oauthTestServer(t, "client-id", "client-secret")
	hdl := s.Handler()
	user := testutil.User(t, h)
	h.Exec(t, `INSERT INTO user_2fa (id, user_id, totp_secret_encrypted, recovery_codes_hash, enabled_at)
		VALUES ($1,$2,'encrypted-secret','[]'::jsonb,now())`, util.NewUUID(), user.ID)
	stubGoogle(t, googleUser(user.Email, true))

	w := oauthCallback(t, hdl, "state", "state", "the-code")
	if w.Code != http.StatusFound {
		t.Fatalf("2FA callback status = %d, want 302: %s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Path != "/2fa" {
		t.Fatalf("2FA redirect path = %q, want /2fa", loc.Path)
	}
	if loc.RawQuery != "" {
		t.Errorf("2FA redirect has a query string %q; the challenge must be in the fragment", loc.RawQuery)
	}
	if !strings.HasPrefix(loc.Fragment, "challenge=") || len(strings.TrimPrefix(loc.Fragment, "challenge=")) < 20 {
		t.Errorf("2FA redirect fragment = %q, want a challenge token", loc.Fragment)
	}
	requireNoSession(t, w)
	requireStateCleared(t, w)

	var sessions int
	if err := h.QueryRow(t, `SELECT count(*) FROM sessions WHERE user_id = $1`, user.ID).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Errorf("a 2FA-gated login created %d sessions before the second factor", sessions)
	}
}

func TestOAuthCallbackRefusesAccountStates(t *testing.T) {
	s, h, _ := oauthTestServer(t, "client-id", "client-secret")
	hdl := s.Handler()

	banned := testutil.UserOpts_(t, h, testutil.UserOpts{Banned: true})
	h.Exec(t, `UPDATE users SET email='banned-oauth@example.test' WHERE id=$1`, banned.ID)
	unverified := testutil.UserOpts_(t, h, testutil.UserOpts{Unverified: true})
	h.Exec(t, `UPDATE users SET email='unverified-oauth@example.test' WHERE id=$1`, unverified.ID)
	suspended := testutil.UserOpts_(t, h, testutil.UserOpts{Suspended: true})
	h.Exec(t, `UPDATE users SET email='suspended-oauth@example.test' WHERE id=$1`, suspended.ID)

	cases := []struct{ name, email string }{
		{"banned account", "banned-oauth@example.test"},
		{"unverified local account", "unverified-oauth@example.test"},
		{"suspended account", "suspended-oauth@example.test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubGoogle(t, googleUser(tc.email, true))
			w := oauthCallback(t, hdl, "state", "state", "the-code")
			requireOAuthRedirect(t, w, "/login?error=oauth_failed")
			requireNoSession(t, w)
			requireStateCleared(t, w)
		})
	}
}
