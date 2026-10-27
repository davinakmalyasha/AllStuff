//go:build !race

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/security"
	"bizverse/api/internal/testutil"
)

// Every admin route sits behind withAdmin2FA. That middleware had no test: the
// admin suites either seed an enrolled admin or use a non-admin, so neither the
// block nor the exempt-path list had ever executed. The exemption list is the
// dangerous half - a missing entry locks an unenrolled admin out of the page
// that fixes the problem, and an over-broad entry hands the dashboard to an
// admin who never enrolled.

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not a JSON error: %v: %s", err, w.Body.String())
	}
	return env.Error.Code
}

func requireAdmin2FABlocked(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusForbidden || errorCode(t, w) != "admin_2fa_required" {
		t.Fatalf("status = %d, code = %q; want the admin_2fa_required 403: %s",
			w.Code, errorCode(t, w), w.Body.String())
	}
}

func TestAdmin2FAGate(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	admin := testutil.UserOpts_(t, h, testutil.UserOpts{Role: domain.RoleAdmin})
	member := testutil.User(t, h)
	adminTFAEnrollment.Delete(admin.ID)
	t.Cleanup(func() { adminTFAEnrollment.Delete(admin.ID) })

	const settings = "/api/v1/admin/settings"
	// The FIRST request is uncached, so it is the only one that reaches the
	// database lookup and the exemption check below it. Starting with an exempt
	// path proves that branch allows recovery, not just the cached branch.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me/security", nil, admin); w.Code != http.StatusOK {
		t.Fatalf("security page (uncached): status = %d, want 200: %s", w.Code, w.Body.String())
	}
	requireAdmin2FABlocked(t, dash(t, td, hdl, http.MethodGet, settings, nil, admin))
	// The third request is served from the memoized decision rather than the
	// database, and must reach the same answer.
	requireAdmin2FABlocked(t, dash(t, td, hdl, http.MethodGet, settings, nil, admin))

	// Exempt paths let the admin recover: the security page to enroll, the
	// refresh/logout pair to stay signed in, the login 2FA verifier, and the WS
	// upgrade (a long-lived connection cannot re-run this middleware later).
	// These now arrive with a cached negative entry, covering that branch's copy
	// of the same list.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me/security", nil, admin); w.Code != http.StatusOK {
		t.Errorf("security page (cached): status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/auth/logout", nil, admin); w.Code != http.StatusNoContent {
		t.Errorf("logout: status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/auth/refresh", nil, admin); w.Code != http.StatusUnauthorized {
		t.Errorf("refresh without a cookie: status = %d, want 401, not the 2FA gate: %s", w.Code, w.Body.String())
	} else if code := errorCode(t, w); code == "admin_2fa_required" {
		t.Error("refresh was blocked by the admin 2FA gate")
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/auth/2fa/verify",
		map[string]any{"challenge": "not-a-token", "code": "000000"}, admin); w.Code == http.StatusForbidden {
		t.Errorf("2fa verify: status = %d, not the 2FA gate: %s", w.Code, w.Body.String())
	} else if code := errorCode(t, w); code != "token_invalid" {
		t.Errorf("2fa verify error = %q, want token_invalid from the verifier: %s", code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/ws", nil, admin); w.Code == http.StatusForbidden {
		t.Errorf("WS upgrade was blocked by the admin 2FA gate: %s", w.Body.String())
	}

	// A non-admin is refused by adminOnly, not by the 2FA mandate.
	w := dash(t, td, hdl, http.MethodGet, settings, nil, member)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin settings: status = %d, want 403", w.Code)
	}
	if code := errorCode(t, w); code == "admin_2fa_required" {
		t.Error("a non-admin was told to enroll in 2FA instead of being refused as non-admin")
	}
}

func TestAdmin2FAEnrollmentInvalidatesCache(t *testing.T) {
	// Enroll/confirm/disable run through their own security pages, but the
	// middleware's memoized decision is per process. A stale negative blocks an
	// admin who just enrolled; a stale positive admits one who just disabled.
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	const password = "correct-horse-battery9"
	admin := testutil.UserOpts_(t, h, testutil.UserOpts{Role: domain.RoleAdmin})
	hash, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := h.Repos.Users.SetPassword(context.Background(), admin.ID, hash); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	adminTFAEnrollment.Delete(admin.ID)
	t.Cleanup(func() { adminTFAEnrollment.Delete(admin.ID) })

	const settings = "/api/v1/admin/settings"
	// Prime the negative cache before enrollment.
	requireAdmin2FABlocked(t, dash(t, td, hdl, http.MethodGet, settings, nil, admin))

	w := dash(t, td, hdl, http.MethodPost, "/api/v1/me/security/2fa",
		map[string]any{"current_password": password}, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("enroll: status = %d: %s", w.Code, w.Body.String())
	}
	var enroll struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &enroll); err != nil || enroll.Secret == "" {
		t.Fatalf("enroll response: %v: %s", err, w.Body.String())
	}
	code, err := security.TOTPCode(enroll.Secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/me/security/2fa/confirm",
		map[string]any{"code": code}, admin); w.Code != http.StatusOK {
		t.Fatalf("confirm: status = %d: %s", w.Code, w.Body.String())
	}

	// No sleep: confirmation must clear the cached "not enrolled" answer.
	if w := dash(t, td, hdl, http.MethodGet, settings, nil, admin); w.Code != http.StatusOK {
		t.Fatalf("admin settings after enrollment: status = %d: %s", w.Code, w.Body.String())
	}

	// Disabling fires the opposite direction. The successful admin request
	// above cached enabled=true; without invalidation this request would still
	// be served for the rest of the TTL.
	code, err = security.TOTPCode(enroll.Secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode for disable: %v", err)
	}
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/me/security/2fa",
		map[string]any{"code": code, "current_password": password}, admin); w.Code != http.StatusNoContent {
		t.Fatalf("disable: status = %d: %s", w.Code, w.Body.String())
	}
	requireAdmin2FABlocked(t, dash(t, td, hdl, http.MethodGet, settings, nil, admin))

	// The security page is still reachable so the admin can enroll again.
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/me/security", nil, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("security page after disable: status = %d: %s", w.Code, w.Body.String())
	}
}
