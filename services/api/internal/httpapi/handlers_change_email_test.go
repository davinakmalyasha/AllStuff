//go:build !race

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// The change-email handler had no HTTP coverage. The service tests cover the
// credential checks, so this is about the wire contract: status codes, the
// verify_required flag, normalization, and the fact that a failed attempt
// leaves the account's address alone.

func TestChangeEmail(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	user := passwordUser(t, h, securityTestPassword)
	taken := testutil.User(t, h)
	path := "/api/v1/me/email"

	do := func(t *testing.T, body any, u *domain.User) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			raw = jsonBody(t, body)
		}
		var req *http.Request
		if u == nil {
			req = authReq(t, http.MethodPost, path, raw)
		} else {
			req = td.authed(t, http.MethodPost, path, raw, u)
		}
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, req)
		return w
	}
	emailNow := func(t *testing.T) (string, *domain.User) {
		t.Helper()
		u, err := h.Repos.Users.GetByID(t.Context(), user.ID)
		if err != nil || u == nil {
			t.Fatalf("read user: %v", err)
		}
		return u.Email, u
	}

	if w := do(t, map[string]any{"password": securityTestPassword, "email": "x@example.test"}, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", w.Code)
	}
	if w := do(t, map[string]any{"password": "wrong-password-1", "email": "x@example.test"}, user); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: status = %d, want 401: %s", w.Code, w.Body.String())
	}
	if w := do(t, map[string]any{"password": securityTestPassword, "email": "not-an-email"}, user); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"email"`) {
		t.Errorf("invalid email: status = %d, want 400 naming the field: %s", w.Code, w.Body.String())
	}
	// Another account's address is a conflict, not a silent takeover.
	if w := do(t, map[string]any{"password": securityTestPassword, "email": taken.Email}, user); w.Code != http.StatusConflict {
		t.Errorf("taken email: status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if got, _ := emailNow(t); got != user.Email {
		t.Fatalf("a refused change altered the address to %q", got)
	}

	// The new address is normalized (trimmed and lowercased) before storage,
	// and changing it invalidates verification.
	raw := "  " + strings.ToUpper("Changed-"+util.NewUUID()[:8]+"@Example.Test") + "  "
	w := do(t, map[string]any{"password": securityTestPassword, "email": raw}, user)
	if w.Code != http.StatusOK {
		t.Fatalf("change email: status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"changed":true`) || !strings.Contains(w.Body.String(), `"verify_required":true`) {
		t.Errorf("change email body = %s", w.Body.String())
	}
	got, updated := emailNow(t)
	if want := strings.ToLower(strings.TrimSpace(raw)); got != want {
		t.Errorf("stored email = %q, want %q", got, want)
	}
	if updated.EmailVerifiedAt != nil {
		t.Error("the new address kept the old verification timestamp")
	}
	// The address another user owns is untouched.
	if other, _ := h.Repos.Users.GetByID(t.Context(), taken.ID); other == nil || other.Email != taken.Email {
		t.Errorf("another user's email changed: %+v", other)
	}
}
