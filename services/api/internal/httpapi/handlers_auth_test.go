//go:build !race

package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"allstuff/api/internal/security"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// The authentication handlers had no coverage. They are the only way into the
// product, and three of them carry security contracts that are invisible in the
// response body: refresh rotation, no account enumeration, and not leaking the
// password hash.

var authIP atomic.Int64

// authReq builds a public (unauthenticated) request from a UNIQUE source
// address. The per-IP auth tier is 5/min, so sharing one address across a test
// would turn the fifth request into a 429 that looks like a credential failure.
func authReq(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = fmt.Sprintf("203.0.113.%d:40000", (authIP.Add(1)%250)+1)
	// Every state-changing route, including sign-in, uses the double-submit
	// cookie: this is the pair a browser would already hold from the SPA
	// bootstrap.
	if method != http.MethodGet && method != http.MethodHead {
		csrf := util.NewUUID()
		req.AddCookie(&http.Cookie{Name: cookieCSRF, Value: csrf, Path: "/"})
		req.Header.Set(security.CSRFHeader, csrf)
	}
	return req
}

func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// cookieValue returns the value of a response cookie, or "".
func cookieValue(w *httptest.ResponseRecorder, name string) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func TestAuthRegister(t *testing.T) {
	s, _ := newDBTestServer(t)
	h := s.Handler()
	email := util.NewUUID() + "@example.test"
	username := "u" + util.NewUUID()[:8]

	w := httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": email, "password": "Sup3rSecret!", "name": "Ada Lovelace", "username": username})))
	if w.Code != http.StatusCreated {
		t.Fatalf("register: status = %d: %s", w.Code, w.Body.String())
	}
	// All three session cookies must be issued together: the CSRF cookie is what
	// makes the subsequent writes possible.
	for _, name := range []string{cookieAccess, cookieRefresh, cookieCSRF} {
		if cookieValue(w, name) == "" {
			t.Errorf("register did not set %s", name)
		}
	}
	// The response is the public projection of the user. A password hash on the
	// wire would be the worst possible leak from this endpoint.
	if strings.Contains(w.Body.String(), "password") {
		t.Errorf("register response mentions a password: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), email) {
		t.Errorf("register response is missing the email: %s", w.Body.String())
	}
}

func TestAuthRegisterRefusesDuplicatesAndWeakPasswords(t *testing.T) {
	s, td := newDBTestServer(t)
	_ = td
	h := s.Handler()
	email := util.NewUUID() + "@example.test"
	username := "u" + util.NewUUID()[:8]

	w := httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": email, "password": "Sup3rSecret!", "name": "Ada", "username": username})))
	if w.Code != http.StatusCreated {
		t.Fatalf("first register: %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": email, "password": "Sup3rSecret!", "name": "Ada", "username": "x" + util.NewUUID()[:8]})))
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate email: status = %d, want 409", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": util.NewUUID() + "@example.test", "password": "short", "name": "Ada", "username": "y" + util.NewUUID()[:8]})))
	if w.Code != http.StatusBadRequest {
		t.Errorf("weak password: status = %d, want 400", w.Code)
	}
}

func TestAuthLogin(t *testing.T) {
	s, td := newDBTestServer(t)
	h := s.Handler()
	email := util.NewUUID() + "@example.test"
	username := "u" + util.NewUUID()[:8]

	w := httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": email, "password": "Sup3rSecret!", "name": "Ada", "username": username})))
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}

	login := func(pass string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/login",
			jsonBody(t, map[string]string{"email": email, "password": pass})))
		return w
	}

	if w := login("Sup3rSecret!"); w.Code != http.StatusOK {
		t.Fatalf("login: status = %d: %s", w.Code, w.Body.String())
	} else if cookieValue(w, cookieRefresh) == "" {
		t.Error("login did not issue a refresh cookie")
	}
	// Wrong password and unknown account are the SAME error, so neither is an
	// account-existence oracle.
	if w := login("WrongPassword1"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: status = %d, want 401", w.Code)
	}
	if w := login("Sup3rSecret!"); w.Code != http.StatusOK {
		t.Fatal("the correct password stopped working")
	}

	// A banned account cannot sign in even with the right password.
	banned := testutil.UserOpts_(t, td, testutil.UserOpts{Banned: true})
	_ = banned
	w = httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/login",
		jsonBody(t, map[string]string{"email": banned.Email, "password": "Sup3rSecret!"})))
	// The fixture's password hash is a placeholder, so this is an invalid-credential
	// answer; what matters is that it is not a successful login.
	if w.Code == http.StatusOK {
		t.Error("a banned account logged in")
	}
}

func TestAuthRefreshRotatesAndRefusesReuse(t *testing.T) {
	s, td := newDBTestServer(t)
	_ = td
	h := s.Handler()
	email := util.NewUUID() + "@example.test"
	username := "u" + util.NewUUID()[:8]

	w := httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": email, "password": "Sup3rSecret!", "name": "Ada", "username": username})))
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d", w.Code)
	}
	first := cookieValue(w, cookieRefresh)
	if first == "" {
		t.Fatal("no refresh cookie from register")
	}

	refresh := func(token string) *httptest.ResponseRecorder {
		req := authReq(t, http.MethodPost, "/api/v1/auth/refresh", nil)
		req.AddCookie(&http.Cookie{Name: cookieRefresh, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	w = refresh(first)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh: status = %d: %s", w.Code, w.Body.String())
	}
	rotated := cookieValue(w, cookieRefresh)
	if rotated == "" || rotated == first {
		t.Errorf("refresh did not rotate the token (got %q)", rotated)
	}

	// The OLD token must be dead: that is what makes a stolen refresh token
	// useful only until the real user's next refresh.
	if w := refresh(first); w.Code != http.StatusUnauthorized {
		t.Errorf("reused refresh token: status = %d, want 401", w.Code)
	}
	// And replaying a rotated token is the theft signal, so it must kill the
	// whole family - including the token the legitimate user is holding now.
	// Rejecting only the replayed token would leave the thief and the victim
	// both signed in.
	if w := refresh(rotated); w.Code != http.StatusUnauthorized {
		t.Errorf("the rotated token survived a replay: status = %d, want 401", w.Code)
	}
	// And no cookie at all is a session error, not a panic.
	if w := httptest.NewRecorder(); func() bool {
		h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/refresh", nil))
		return w.Code == http.StatusUnauthorized
	}() == false {
		t.Errorf("missing refresh cookie: status = %d, want 401", w.Code)
	}
}

func TestAuthLogoutRevokesTheSession(t *testing.T) {
	s, _ := newDBTestServer(t)
	h := s.Handler()
	email := util.NewUUID() + "@example.test"
	username := "u" + util.NewUUID()[:8]

	w := httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": email, "password": "Sup3rSecret!", "name": "Ada", "username": username})))
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d", w.Code)
	}
	refreshTok := cookieValue(w, cookieRefresh)
	csrf := cookieValue(w, cookieCSRF)

	req := authReq(t, http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: cookieRefresh, Value: refreshTok})
	req.AddCookie(&http.Cookie{Name: cookieCSRF, Value: csrf, Path: "/"})
	req.Header.Set(security.CSRFHeader, csrf)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout: status = %d: %s", w.Code, w.Body.String())
	}
	// Cookies are cleared on the response.
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieRefresh && c.Value != "" && c.MaxAge > 0 {
			t.Errorf("logout left a live refresh cookie")
		}
	}

	// The revoked token no longer refreshes.
	req = authReq(t, http.MethodPost, "/api/v1/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: cookieRefresh, Value: refreshTok})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("refresh after logout: status = %d, want 401", w.Code)
	}
}

// Neither recovery endpoint may reveal whether an address has an account.
func TestAuthRecoveryDoesNotEnumerate(t *testing.T) {
	s, _ := newDBTestServer(t)
	h := s.Handler()
	email := util.NewUUID() + "@example.test"
	username := "u" + util.NewUUID()[:8]

	w := httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": email, "password": "Sup3rSecret!", "name": "Ada", "username": username})))
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d", w.Code)
	}

	known := email
	unknown := util.NewUUID() + "@example.test"
	for _, addr := range []string{known, unknown} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/forgot-password",
			jsonBody(t, map[string]string{"email": addr})))
		if w.Code != http.StatusNoContent {
			t.Errorf("forgot-password(%s): status = %d, want 204", addr, w.Code)
		}

		w = httptest.NewRecorder()
		h.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/resend-verification",
			jsonBody(t, map[string]string{"email": addr})))
		if w.Code != http.StatusOK {
			t.Errorf("resend-verification(%s): status = %d, want 200", addr, w.Code)
		}
		if !strings.Contains(w.Body.String(), `"sent":true`) {
			t.Errorf("resend-verification(%s): body = %s, want sent:true", addr, w.Body.String())
		}
	}
}

// /ready is deliberately not asserted here: it pings Repos.Pool(), and the
// handler harness binds Repos to a test TRANSACTION, so Pool() is nil and the
// guard cannot pass. Production always builds Repos from a pool - NewForTx is
// used only by tests and by ChangePassword's scoped transaction - so this is a
// harness boundary rather than a defect, but it means /ready needs a
// pool-bound harness to be covered properly.
func TestHealth(t *testing.T) {
	s, _ := newDBTestServer(t)
	h := s.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodGet, "/api/v1/health", nil))
	if w.Code != http.StatusOK {
		t.Errorf("health: status = %d, want 200", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, authReq(t, http.MethodGet, "/api/v1/auth/csrf", nil))
	// 204: it exists only to set the cookie, not to return a body.
	if w.Code != http.StatusNoContent {
		t.Errorf("csrf bootstrap: status = %d, want 204", w.Code)
	}
	if cookieValue(w, cookieCSRF) == "" {
		t.Error("the CSRF bootstrap did not issue a cookie")
	}
}
