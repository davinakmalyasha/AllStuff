//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bizverse/api/internal/security"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// The last small handlers: the public meta/metrics/ready trio and the three
// token-bearing account flows.

func TestMetaAndMetrics(t *testing.T) {
	s, _, _ := newDBTestServerDeps(t)
	hdl := s.Handler()

	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, "/api/v1/meta", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("meta: status = %d", w.Code)
	}
	var meta map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	// The SPA bootstraps from this: site config plus the FX timestamp.
	if _, ok := meta["site"]; !ok {
		t.Error("meta has no site key")
	}
	if _, ok := meta["rates"]; !ok {
		t.Error("meta has no rates key")
	}

	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("metrics: status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("metrics Content-Type = %q, want text/plain", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("metrics rendered nothing")
	}
}

// /ready pings the pool, so it needs the pool-bound harness: the default one
// binds Repos to a transaction and Pool() is nil.
func TestReadyWithAPool(t *testing.T) {
	s, _, _ := newDBTestServerPool(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authReq(t, http.MethodGet, "/api/v1/ready", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ready: status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"ready":true`) {
		t.Errorf("ready body = %s", w.Body.String())
	}
}

func TestVerifyEmailFlow(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	u := testutil.UserOpts_(t, h, testutil.UserOpts{Unverified: true})

	token, _, err := security.IssueToken(td.Config.JWTSecret, security.TokenVerifyEmail, u, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// A bad token is refused.
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/verify-email",
		jsonBody(t, map[string]string{"token": "not-a-token"})))
	if w.Code != http.StatusBadRequest && w.Code != http.StatusUnauthorized {
		t.Errorf("bad token: status = %d, want 400/401", w.Code)
	}

	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/verify-email",
		jsonBody(t, map[string]string{"token": token})))
	if w.Code != http.StatusNoContent {
		t.Fatalf("verify: status = %d: %s", w.Code, w.Body.String())
	}
	after, err := h.Repos.Users.GetByID(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.EmailVerifiedAt == nil {
		t.Error("the account is still unverified after a successful verification")
	}

	// The link is single-use, so a captured copy is worthless once used.
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/verify-email",
		jsonBody(t, map[string]string{"token": token})))
	if w.Code == http.StatusNoContent {
		t.Error("the verification token worked twice")
	}
}

func TestResetPasswordFlow(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()

	email := util.NewUUID() + "@example.test"
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": email, "password": "OldPassword1", "name": "Reset User", "username": "u" + util.NewUUID()[:8]})))
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	u, err := h.Repos.Users.GetByEmail(t.Context(), email)
	if err != nil || u == nil {
		t.Fatalf("look up the new user: %v", err)
	}

	token, _, err := security.IssueToken(td.Config.JWTSecret, security.TokenResetPassword, u, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// The password policy is checked before the token is consumed, so a weak
	// attempt does not burn the link.
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/reset-password",
		jsonBody(t, map[string]string{"token": token, "password": "short"})))
	if w.Code != http.StatusBadRequest {
		t.Errorf("weak password: status = %d, want 400", w.Code)
	}

	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/reset-password",
		jsonBody(t, map[string]string{"token": token, "password": "NewPassword2"})))
	if w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d: %s", w.Code, w.Body.String())
	}

	// The new password works and the old one does not.
	login := func(pass string) int {
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/login",
			jsonBody(t, map[string]string{"email": email, "password": pass})))
		return w.Code
	}
	if code := login("NewPassword2"); code != http.StatusOK {
		t.Errorf("login with the new password: %d", code)
	}
	if code := login("OldPassword1"); code != http.StatusUnauthorized {
		t.Errorf("login with the OLD password: %d, want 401", code)
	}

	// And the link is burnt.
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/reset-password",
		jsonBody(t, map[string]string{"token": token, "password": "AnotherPass3"})))
	if w.Code == http.StatusNoContent {
		t.Error("the reset token worked twice")
	}
}

// An account inside its deletion grace period can be signed back into, and only
// with the password - the grace window is not an open door.
func TestRestoreAccount(t *testing.T) {
	s, h, _ := newDBTestServerPool(t)
	hdl := s.Handler()
	email := util.NewUUID() + "@example.test"

	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/register",
		jsonBody(t, map[string]string{"email": email, "password": "RestoreMe1", "name": "Restore User", "username": "u" + util.NewUUID()[:8]})))
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d", w.Code)
	}
	u, err := h.Repos.Users.GetByEmail(t.Context(), email)
	if err != nil || u == nil {
		t.Fatalf("look up: %v", err)
	}
	h.ExecPool(t, `UPDATE users SET deleted_at=now() WHERE id=$1`, u.ID)

	// Wrong password: refused, and the account stays deleted.
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/restore",
		jsonBody(t, map[string]string{"email": email, "password": "WrongPassword1"})))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: status = %d, want 401", w.Code)
	}

	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/restore",
		jsonBody(t, map[string]string{"email": email, "password": "RestoreMe1"})))
	if w.Code != http.StatusOK {
		t.Fatalf("restore: status = %d: %s", w.Code, w.Body.String())
	}
	if cookieValue(w, cookieRefresh) == "" {
		t.Error("restore did not sign the user back in")
	}
	after, err := h.Repos.Users.GetByID(t.Context(), u.ID)
	if err != nil || after == nil {
		t.Fatalf("after restore: %v", err)
	}
	if after.DeletedAt != nil {
		t.Error("the account is still marked deleted")
	}

	// An account that is not deleted is simply not restorable this way.
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/auth/restore",
		jsonBody(t, map[string]string{"email": email, "password": "RestoreMe1"})))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("restore of a live account: status = %d, want 401", w.Code)
	}
}
