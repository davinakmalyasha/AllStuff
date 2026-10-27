//go:build !race

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/security"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// The security handlers had no HTTP test: 2FA, sessions, password and email
// changes, notification preferences, export, saved searches, compare sync,
// public collections, client-error intake, and account deletion. Their services
// are tested, so what is left to break here is the wiring - and the wiring is
// where a credential check or an ownership check can be omitted.
//
// These tests run in sequence within each function because a real account is a
// state machine: enrolling, confirming, verifying, and disabling 2FA are not
// independent operations.

const securityTestPassword = "correct-horse-battery9"

// passwordUser seeds a user whose password actually verifies. testutil.User
// inserts a placeholder hash on purpose, because most suites never authenticate
// with a password.
func passwordUser(t *testing.T, h *testutil.H, password string) *domain.User {
	t.Helper()
	u := testutil.User(t, h)
	hash, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := h.Repos.Users.SetPassword(context.Background(), u.ID, hash); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	return u
}

func TestSecurityTwoFactor(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := passwordUser(t, h, securityTestPassword)
	other := testutil.User(t, h)

	do := func(t *testing.T, method, path string, body any, u *domain.User) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			raw = jsonBody(t, body)
		}
		var req *http.Request
		if u == nil {
			req = authReq(t, method, path, raw)
		} else {
			req = td.authed(t, method, path, raw, u)
		}
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, req)
		return w
	}
	decode := func(t *testing.T, w *httptest.ResponseRecorder, v any) {
		t.Helper()
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
			t.Fatalf("not JSON: %v: %s", err, w.Body.String())
		}
	}

	type status struct {
		Enabled bool `json:"enabled"`
	}
	var st status
	decode(t, do(t, http.MethodGet, "/api/v1/me/security", nil, user), &st)
	if st.Enabled {
		t.Fatal("a fresh account reports 2FA enabled")
	}
	if w := do(t, http.MethodGet, "/api/v1/me/security", nil, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous 2FA status: status = %d, want 401", w.Code)
	}

	// Enrollment is a step-up operation: a valid access token is not enough.
	if w := do(t, http.MethodPost, "/api/v1/me/security/2fa", map[string]any{}, user); w.Code != http.StatusBadRequest {
		t.Errorf("enroll without a password: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodPost, "/api/v1/me/security/2fa",
		map[string]any{"current_password": "wrong-password-1"}, user); w.Code != http.StatusUnauthorized {
		t.Errorf("enroll with a wrong password: status = %d, want 401: %s", w.Code, w.Body.String())
	}
	var enroll struct {
		Secret     string `json:"secret"`
		OtpauthURL string `json:"otpauth_url"`
	}
	w := do(t, http.MethodPost, "/api/v1/me/security/2fa",
		map[string]any{"current_password": securityTestPassword}, user)
	decode(t, w, &enroll)
	if enroll.Secret == "" || !strings.HasPrefix(enroll.OtpauthURL, "otpauth://totp/") {
		t.Fatalf("enrollment = secret %q, url %q", enroll.Secret, enroll.OtpauthURL)
	}

	// Confirming with a malformed code must not enable the factor.
	if w := do(t, http.MethodPost, "/api/v1/me/security/2fa/confirm",
		map[string]any{"code": "12345"}, user); w.Code != http.StatusBadRequest {
		t.Errorf("confirm with a malformed code: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	code, err := security.TOTPCode(enroll.Secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	var confirm struct {
		Enabled       bool     `json:"enabled"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	decode(t, do(t, http.MethodPost, "/api/v1/me/security/2fa/confirm",
		map[string]any{"code": code}, user), &confirm)
	if !confirm.Enabled || len(confirm.RecoveryCodes) != 10 {
		t.Fatalf("confirm = enabled %v, %d recovery codes", confirm.Enabled, len(confirm.RecoveryCodes))
	}
	for _, c := range confirm.RecoveryCodes {
		if c == "" {
			t.Fatal("confirm issued an empty recovery code")
		}
	}
	// Re-enrolling over an enabled factor would let a stolen token replace the
	// authenticator; it must be refused even with the password.
	if w := do(t, http.MethodPost, "/api/v1/me/security/2fa",
		map[string]any{"current_password": securityTestPassword}, user); w.Code != http.StatusBadRequest {
		t.Errorf("re-enroll while enabled: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/security", nil, user), &st)
	if !st.Enabled {
		t.Error("status did not report the enabled factor")
	}

	// The login second-factor endpoint. The challenge is a signed token the
	// login handler mints after the password step; issue one directly so this
	// test does not re-test login.
	challenge, _, err := security.IssueToken(td.Config.JWTSecret, security.Token2FAChallenge, user, 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if w := do(t, http.MethodPost, "/api/v1/auth/2fa/verify",
		map[string]any{"challenge": challenge, "code": "000000"}, nil); w.Code != http.StatusBadRequest {
		t.Errorf("verify with a wrong code: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	// A recovery code is a second factor too, and consuming it must sign the
	// user in.
	w = do(t, http.MethodPost, "/api/v1/auth/2fa/verify",
		map[string]any{"challenge": challenge, "code": confirm.RecoveryCodes[0]}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("verify with a recovery code: status = %d: %s", w.Code, w.Body.String())
	}
	if cookieValue(w, cookieAccess) == "" || cookieValue(w, cookieRefresh) == "" {
		t.Error("2FA verification did not set session cookies")
	}
	// That recovery code was single-use.
	if w := do(t, http.MethodPost, "/api/v1/auth/2fa/verify",
		map[string]any{"challenge": challenge, "code": confirm.RecoveryCodes[0]}, nil); w.Code == http.StatusOK {
		t.Error("a consumed recovery code signed in a second time")
	}

	// Sessions: the verification minted one. The list must include it, and a
	// stranger must not be able to revoke it.
	var sessions struct {
		Sessions []struct {
			ID        string `json:"id"`
			RevokedAt any    `json:"revoked_at"`
		} `json:"sessions"`
		History []map[string]any `json:"history"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/security/sessions", nil, user), &sessions)
	if len(sessions.Sessions) == 0 {
		t.Fatal("the session created by 2FA verification is not listed")
	}
	if sessions.History == nil {
		t.Error("login history is null instead of an array")
	}
	sessionID := sessions.Sessions[0].ID
	if w := do(t, http.MethodDelete, "/api/v1/me/security/sessions/"+sessionID, nil, other); w.Code != http.StatusNotFound {
		t.Errorf("a stranger revoked a session: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodDelete, "/api/v1/me/security/sessions/"+sessionID, nil, user); w.Code != http.StatusNoContent {
		t.Errorf("owner revoke: status = %d: %s", w.Code, w.Body.String())
	}

	// Disabling requires the password AND a fresh code. The TOTP step was not
	// consumed by confirm (which only validates) or by the recovery-code path.
	if w := do(t, http.MethodDelete, "/api/v1/me/security/2fa",
		map[string]any{"code": code, "current_password": "wrong-password-1"}, user); w.Code != http.StatusUnauthorized {
		t.Errorf("disable with a wrong password: status = %d, want 401: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodDelete, "/api/v1/me/security/2fa",
		map[string]any{"code": "12345", "current_password": securityTestPassword}, user); w.Code != http.StatusBadRequest {
		t.Errorf("disable with a bad code: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodDelete, "/api/v1/me/security/2fa",
		map[string]any{"code": code, "current_password": securityTestPassword}, user); w.Code != http.StatusNoContent {
		t.Errorf("disable: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/security", nil, user), &st)
	if st.Enabled {
		t.Error("2FA is still enabled after a successful disable")
	}
}

func TestSecurityRecoveryCodeRotation(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := passwordUser(t, h, securityTestPassword)

	do := func(t *testing.T, method, path string, body any, u *domain.User) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			raw = jsonBody(t, body)
		}
		var req *http.Request
		if u == nil {
			req = authReq(t, method, path, raw)
		} else {
			req = td.authed(t, method, path, raw, u)
		}
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, req)
		return w
	}
	decode := func(t *testing.T, w *httptest.ResponseRecorder, v any) {
		t.Helper()
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
			t.Fatalf("not JSON: %v: %s", err, w.Body.String())
		}
	}

	var enroll struct {
		Secret string `json:"secret"`
	}
	decode(t, do(t, http.MethodPost, "/api/v1/me/security/2fa",
		map[string]any{"current_password": securityTestPassword}, user), &enroll)
	code, err := security.TOTPCode(enroll.Secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	var confirm struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	decode(t, do(t, http.MethodPost, "/api/v1/me/security/2fa/confirm",
		map[string]any{"code": code}, user), &confirm)
	if len(confirm.RecoveryCodes) != 10 {
		t.Fatalf("confirm issued %d recovery codes", len(confirm.RecoveryCodes))
	}

	// Regeneration is a step-up with the current password and a fresh code.
	if w := do(t, http.MethodPost, "/api/v1/me/security/2fa/recovery-codes",
		map[string]any{"code": code, "current_password": "wrong-password-1"}, user); w.Code != http.StatusUnauthorized {
		t.Errorf("regenerate with a wrong password: status = %d, want 401: %s", w.Code, w.Body.String())
	}
	var regen struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	decode(t, do(t, http.MethodPost, "/api/v1/me/security/2fa/recovery-codes",
		map[string]any{"code": code, "current_password": securityTestPassword}, user), &regen)
	if len(regen.RecoveryCodes) != 10 {
		t.Fatalf("regenerate issued %d recovery codes", len(regen.RecoveryCodes))
	}
	if regen.RecoveryCodes[0] == confirm.RecoveryCodes[0] {
		t.Error("regeneration returned the same first code")
	}

	challenge, _, err := security.IssueToken(td.Config.JWTSecret, security.Token2FAChallenge, user, 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	// The old set is dead the moment a new one is issued.
	if w := do(t, http.MethodPost, "/api/v1/auth/2fa/verify",
		map[string]any{"challenge": challenge, "code": confirm.RecoveryCodes[0]}, nil); w.Code == http.StatusOK {
		t.Error("a pre-regeneration recovery code still signed in")
	}
	if w := do(t, http.MethodPost, "/api/v1/auth/2fa/verify",
		map[string]any{"challenge": challenge, "code": regen.RecoveryCodes[1]}, nil); w.Code != http.StatusOK {
		t.Errorf("a regenerated recovery code failed: status = %d: %s", w.Code, w.Body.String())
	}
}

func TestSecurityAccountSurface(t *testing.T) {
	// ChangePassword opens its own pool transaction (it must keep the caller's
	// session and revoke the rest atomically), so Pool() has to be real. The
	// transaction harness would turn that into a nil dereference and a 500.
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	user := passwordUser(t, h, securityTestPassword)
	other := testutil.User(t, h)

	do := func(t *testing.T, method, path string, body any, u *domain.User, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			raw = jsonBody(t, body)
		}
		var req *http.Request
		if u == nil {
			req = authReq(t, method, path, raw)
		} else {
			req = td.authed(t, method, path, raw, u)
		}
		for _, c := range cookies {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, req)
		return w
	}
	decode := func(t *testing.T, w *httptest.ResponseRecorder, v any) {
		t.Helper()
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
			t.Fatalf("not JSON: %v: %s", err, w.Body.String())
		}
	}

	// ---- sessions ----
	makeSession := func(token string) *domain.Session {
		t.Helper()
		sess := &domain.Session{ID: util.NewUUID(), UserID: user.ID, TokenHash: hashToken(token)}
		if err := h.Repos.Sessions.Create(context.Background(), sess); err != nil {
			t.Fatalf("create session: %v", err)
		}
		return sess
	}
	keep := makeSession("refresh-keep")
	extra := makeSession("refresh-extra")

	var sessionList struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/security/sessions", nil, user), &sessionList)
	if len(sessionList.Sessions) != 2 {
		t.Errorf("sessions = %d, want 2", len(sessionList.Sessions))
	}
	revokedNow := func(id string) bool {
		var revoked bool
		if err := h.QueryRow(t, `SELECT revoked_at IS NOT NULL FROM sessions WHERE id=$1`, id).Scan(&revoked); err != nil {
			t.Fatalf("session lookup: %v", err)
		}
		return revoked
	}
	// Revoking another user's session must not touch it.
	if w := do(t, http.MethodDelete, "/api/v1/me/security/sessions/"+extra.ID, nil, other); w.Code != http.StatusNotFound {
		t.Errorf("stranger session revoke: status = %d, want 404", w.Code)
	}
	if revokedNow(extra.ID) {
		t.Error("a stranger revoked a session")
	}
	if w := do(t, http.MethodDelete, "/api/v1/me/security/sessions/"+extra.ID, nil, user); w.Code != http.StatusNoContent {
		t.Errorf("owner session revoke: status = %d: %s", w.Code, w.Body.String())
	}
	if !revokedNow(extra.ID) {
		t.Error("revoke did not set revoked_at")
	}
	// revoke-others keeps the session named by the refresh cookie and kills the
	// rest. Without that cookie there is no way to name the survivor, so the
	// request is refused rather than revoking everything.
	third := makeSession("refresh-third")
	if w := do(t, http.MethodPost, "/api/v1/me/security/sessions/revoke-others", nil, user); w.Code != http.StatusUnauthorized {
		t.Errorf("revoke-others without a refresh cookie: status = %d, want 401: %s", w.Code, w.Body.String())
	}
	w := do(t, http.MethodPost, "/api/v1/me/security/sessions/revoke-others", nil, user,
		&http.Cookie{Name: cookieRefresh, Value: "refresh-keep"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke-others: status = %d: %s", w.Code, w.Body.String())
	}
	if revokedNow(keep.ID) {
		t.Error("revoke-others revoked the current session")
	}
	if !revokedNow(third.ID) {
		t.Error("revoke-others left another session live")
	}

	// ---- password ----
	if w := do(t, http.MethodPost, "/api/v1/me/password",
		map[string]any{"current_password": "wrong-password-1", "new_password": "NewSecret123"}, user); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong current password: status = %d, want 401: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodPost, "/api/v1/me/password",
		map[string]any{"current_password": securityTestPassword, "new_password": "short"}, user); w.Code != http.StatusBadRequest {
		t.Errorf("weak new password: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	oldSession := makeSession("refresh-old")
	if w := do(t, http.MethodPost, "/api/v1/me/password",
		map[string]any{"current_password": securityTestPassword, "new_password": "NewSecret123"}, user,
		&http.Cookie{Name: cookieRefresh, Value: "refresh-keep"}); w.Code != http.StatusNoContent {
		t.Errorf("change password: status = %d: %s", w.Code, w.Body.String())
	}
	fresh, err := h.Repos.Users.GetByID(context.Background(), user.ID)
	if err != nil || fresh == nil {
		t.Fatalf("reload user: %v", err)
	}
	if ok, _ := security.VerifyPassword("NewSecret123", fresh.PasswordHash); !ok {
		t.Error("the new password does not verify")
	}
	if ok, _ := security.VerifyPassword(securityTestPassword, fresh.PasswordHash); ok {
		t.Error("the old password still verifies")
	}
	if revokedNow(keep.ID) {
		t.Error("change-password revoked the session it was told to keep")
	}
	if !revokedNow(oldSession.ID) {
		t.Error("change-password left another session live")
	}

	// ---- notification preferences ----
	var prefs map[string]any
	decode(t, do(t, http.MethodGet, "/api/v1/me/notifications-settings", nil, user), &prefs)
	if _, ok := prefs["channels"]; !ok {
		t.Errorf("default prefs have no channels: %v", prefs)
	}
	if prefs["digest_opt_in"] != false {
		t.Errorf("digest_opt_in = %v, want false", prefs["digest_opt_in"])
	}
	if w := do(t, http.MethodPatch, "/api/v1/me/notifications-settings",
		map[string]any{"digest_opt_in": true, "quiet_hours": map[string]any{"enabled": true}}, user); w.Code != http.StatusOK {
		t.Fatalf("save prefs: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/notifications-settings", nil, user), &prefs)
	if prefs["digest_opt_in"] != true {
		t.Errorf("digest_opt_in = %v after saving true", prefs["digest_opt_in"])
	}
	var digest bool
	if err := h.QueryRow(t, `SELECT digest_opt_in FROM users WHERE id=$1`, user.ID).Scan(&digest); err != nil {
		t.Fatalf("digest column: %v", err)
	}
	if !digest {
		t.Error("digest_opt_in was not mirrored onto the user row")
	}

	// ---- export ----
	w = do(t, http.MethodGet, "/api/v1/me/export", nil, user)
	if w.Code != http.StatusOK {
		t.Fatalf("export: status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("export content-type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, user.Username) {
		t.Errorf("export content-disposition = %q, want the username", cd)
	}
	var export map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &export); err != nil {
		t.Fatalf("export is not JSON: %v", err)
	}
	profile, _ := export["profile"].(map[string]any)
	if profile == nil || profile["email"] != fresh.Email {
		t.Errorf("export profile = %v", export["profile"])
	}
	for _, key := range []string{"businesses", "products", "collections", "reviews", "comments", "threads", "messages"} {
		if export[key] == nil {
			t.Errorf("export.%s is null instead of an array", key)
		}
	}

	// ---- saved searches ----
	if w := do(t, http.MethodPost, "/api/v1/me/saved-searches",
		map[string]any{"name": "   "}, user); w.Code != http.StatusBadRequest {
		t.Errorf("blank saved-search name: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	var saved struct {
		Search struct {
			ID          string         `json:"id"`
			Name        string         `json:"name"`
			Query       map[string]any `json:"query"`
			NotifyDaily bool           `json:"notify_daily"`
		} `json:"search"`
	}
	decode(t, do(t, http.MethodPost, "/api/v1/me/saved-searches", map[string]any{
		"name": "Weekend cafes", "query": map[string]any{"q": "cafe"}, "notify_daily": false,
	}, user), &saved)
	if saved.Search.ID == "" || saved.Search.Query["q"] != "cafe" {
		t.Fatalf("saved search = %+v", saved.Search)
	}
	searchID := saved.Search.ID
	var searches struct {
		Searches []struct {
			ID          string `json:"id"`
			NotifyDaily bool   `json:"notify_daily"`
		} `json:"searches"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/saved-searches", nil, user), &searches)
	if len(searches.Searches) != 1 {
		t.Fatalf("saved searches = %d, want 1", len(searches.Searches))
	}
	// A saved-search id is not a capability: another user must not be able to
	// toggle or delete it, and must be told the row is not theirs rather than
	// being given a no-op success.
	if w := do(t, http.MethodPatch, "/api/v1/me/saved-searches/"+searchID,
		map[string]any{"notify_daily": true}, other); w.Code != http.StatusNotFound {
		t.Errorf("a stranger toggled a saved search: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodDelete, "/api/v1/me/saved-searches/"+searchID, nil, other); w.Code != http.StatusNotFound {
		t.Errorf("a stranger deleted a saved search: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/saved-searches", nil, user), &searches)
	if len(searches.Searches) != 1 || searches.Searches[0].NotifyDaily {
		t.Fatalf("a stranger changed the saved search: %+v", searches.Searches)
	}
	if w := do(t, http.MethodPatch, "/api/v1/me/saved-searches/"+searchID,
		map[string]any{}, user); w.Code != http.StatusBadRequest {
		t.Errorf("patch without notify_daily: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodPatch, "/api/v1/me/saved-searches/"+searchID,
		map[string]any{"notify_daily": true}, user); w.Code != http.StatusNoContent {
		t.Errorf("owner toggle: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/saved-searches", nil, user), &searches)
	if len(searches.Searches) != 1 || !searches.Searches[0].NotifyDaily {
		t.Errorf("saved search after toggle = %+v", searches.Searches)
	}
	if w := do(t, http.MethodDelete, "/api/v1/me/saved-searches/"+searchID, nil, user); w.Code != http.StatusNoContent {
		t.Errorf("owner delete: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/saved-searches", nil, user), &searches)
	if len(searches.Searches) != 0 || searches.Searches == nil {
		t.Errorf("saved searches after delete = %v (want an empty array)", searches.Searches)
	}
	if w := do(t, http.MethodDelete, "/api/v1/me/saved-searches/"+searchID, nil, user); w.Code != http.StatusNotFound {
		t.Errorf("deleting a missing saved search: status = %d, want 404: %s", w.Code, w.Body.String())
	}

	// ---- compare sync ----
	var compared struct {
		IDs []string `json:"ids"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/compare", nil, user), &compared)
	if compared.IDs == nil || len(compared.IDs) != 0 {
		t.Errorf("empty compare tray = %v, want an empty array", compared.IDs)
	}
	// compare_ids is uuid[]. A non-UUID literal used to reach Postgres and
	// surface as a 500.
	if w := do(t, http.MethodPut, "/api/v1/me/compare",
		map[string]any{"ids": []string{"not-a-uuid"}}, user); w.Code != http.StatusBadRequest {
		t.Errorf("compare with a non-UUID: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	ids := []string{util.NewUUID(), util.NewUUID(), util.NewUUID(), util.NewUUID(), util.NewUUID()}
	if w := do(t, http.MethodPut, "/api/v1/me/compare",
		map[string]any{"ids": ids}, user); w.Code != http.StatusNoContent {
		t.Fatalf("compare sync: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/compare", nil, user), &compared)
	if len(compared.IDs) != 4 || compared.IDs[0] != ids[0] {
		t.Errorf("compare tray = %v, want the first four ids in order", compared.IDs)
	}

	// ---- public collections ----
	cat := testutil.Category(t, h, "Security Category")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Security Coffee", Slug: "security-coffee", Category: cat, Owner: user,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	col, err := td.Deps.Engagement.CreateCollection(context.Background(), user.ID, "Public list")
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	public := true
	if _, err := td.Deps.Engagement.UpdateCollection(context.Background(), user.ID, col.ID, nil, &public, nil); err != nil {
		t.Fatalf("publish collection: %v", err)
	}
	if err := td.Deps.Engagement.SaveTo(context.Background(), user.ID, col.ID, "business", b.ID, "great coffee"); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	var publicRes struct {
		Collection struct {
			ID string `json:"id"`
		} `json:"collection"`
		Items []struct {
			TargetID string `json:"target_id"`
			Note     string `json:"note"`
		} `json:"items"`
		Owner struct {
			Username string `json:"username"`
		} `json:"owner"`
		HasMore bool `json:"has_more"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/collections/"+col.ID+"?limit=1000", nil, nil), &publicRes)
	if publicRes.Collection.ID != col.ID || len(publicRes.Items) != 1 || publicRes.Items[0].TargetID != b.ID {
		t.Fatalf("public collection = %+v", publicRes)
	}
	if publicRes.Owner.Username != user.Username {
		t.Errorf("public collection owner = %q", publicRes.Owner.Username)
	}
	if publicRes.HasMore {
		t.Error("has_more is true for one item")
	}
	private, err := td.Deps.Engagement.CreateCollection(context.Background(), user.ID, "Private list")
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if w := do(t, http.MethodGet, "/api/v1/collections/"+private.ID, nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("a private collection is public: status = %d", w.Code)
	}
	if w := do(t, http.MethodGet, "/api/v1/collections/not-a-uuid", nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("malformed collection id: status = %d, want 404: %s", w.Code, w.Body.String())
	}

	// ---- client error intake (rate-limited per IP, anonymous) ----
	errorReq := func(body any) *httptest.ResponseRecorder {
		raw := jsonBody(t, body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/errors", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "198.51.100.7:40000"
		csrf := util.NewUUID()
		req.AddCookie(&http.Cookie{Name: cookieCSRF, Value: csrf, Path: "/"})
		req.Header.Set(security.CSRFHeader, csrf)
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, req)
		return w
	}
	w = errorReq(map[string]any{"message": strings.Repeat("x", 3000), "stack": "at app.js:1", "url": "/somewhere"})
	if w.Code != http.StatusCreated {
		t.Fatalf("client error: status = %d: %s", w.Code, w.Body.String())
	}
	var reason string
	if err := h.QueryRow(t, `SELECT reason FROM reports WHERE target_type='error' AND reporter_id IS NULL ORDER BY created_at DESC LIMIT 1`).Scan(&reason); err != nil {
		t.Fatalf("client-error report: %v", err)
	}
	if len([]rune(reason)) != 2000 {
		t.Errorf("stored client-error message is %d runes, want it truncated to 2000", len([]rune(reason)))
	}
	// A whitespace-only message is not a report. The limiter is checked before
	// decoding, so this request still consumes one of the ten slots.
	blank := errorReq(map[string]any{"message": "   "})
	if blank.Code != http.StatusBadRequest {
		t.Errorf("client error without a message: status = %d, want 400", blank.Code)
	}
	// The truncated report was slot 1, the rejected blank one slot 2, and these
	// eight fill the bucket. The next request is refused rather than writing
	// another row.
	for i := 0; i < 8; i++ {
		if w := errorReq(map[string]any{"message": "boom"}); w.Code != http.StatusCreated {
			t.Fatalf("client error %d: status = %d: %s", i, w.Code, w.Body.String())
		}
	}
	if w := errorReq(map[string]any{"message": "boom"}); w.Code != http.StatusTooManyRequests {
		t.Errorf("client error past the limit: status = %d, want 429: %s", w.Code, w.Body.String())
	} else if w.Header().Get("Retry-After") == "" {
		t.Error("429 has no Retry-After")
	}

	// ---- account deletion and restore ----
	if w := do(t, http.MethodPost, "/api/v1/me/delete", map[string]any{"password": "wrong-password-1"}, user); w.Code != http.StatusUnauthorized {
		t.Errorf("delete with a wrong password: status = %d, want 401: %s", w.Code, w.Body.String())
	}
	w = do(t, http.MethodPost, "/api/v1/me/delete", map[string]any{"password": "NewSecret123"}, user)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete account: status = %d: %s", w.Code, w.Body.String())
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieAccess && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("delete account did not clear the session cookies")
	}
	deleted, err := h.Repos.Users.GetAnyByID(context.Background(), user.ID)
	if err != nil || deleted == nil || deleted.DeletedAt == nil {
		t.Fatalf("account was not soft-deleted: %+v (%v)", deleted, err)
	}
	var live int
	if err := h.QueryRow(t, `SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL`, user.ID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Errorf("%d sessions are still live after account deletion", live)
	}
	// The deleted account keeps its data but cannot authenticate. Login names
	// the restore path instead of lying about the password.
	w = do(t, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"email": fresh.Email, "password": "NewSecret123"}, nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "account_pending_deletion") {
		t.Errorf("login while pending deletion: status = %d: %s", w.Code, w.Body.String())
	}
	// Restore is the public grace-period path.
	w = do(t, http.MethodPost, "/api/v1/auth/restore",
		map[string]any{"email": fresh.Email, "password": "NewSecret123"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("restore: status = %d: %s", w.Code, w.Body.String())
	}
	restoredAccess := cookieValue(w, cookieAccess)
	if restoredAccess == "" {
		t.Fatal("restore did not sign the user back in")
	}
	restored, _ := h.Repos.Users.GetByID(context.Background(), user.ID)
	if restored == nil || restored.DeletedAt != nil {
		t.Fatalf("account was not restored: %+v", restored)
	}
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq.AddCookie(&http.Cookie{Name: cookieAccess, Value: restoredAccess})
	meW := httptest.NewRecorder()
	hdl.ServeHTTP(meW, meReq)
	if meW.Code != http.StatusOK || !strings.Contains(meW.Body.String(), fresh.Email) {
		t.Errorf("restored session cannot read /me: status = %d: %s", meW.Code, meW.Body.String())
	}
	// Cancel-deletion is also a password-bearing endpoint; on a live account it
	// is a deliberate no-op rather than an error.
	if w := do(t, http.MethodPost, "/api/v1/me/delete/cancel",
		map[string]any{"password": "NewSecret123"}, restored); w.Code != http.StatusNoContent {
		t.Errorf("cancel deletion on a live account: status = %d: %s", w.Code, w.Body.String())
	}
}
