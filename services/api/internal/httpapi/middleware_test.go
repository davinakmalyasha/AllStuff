package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/ratelimit"
	"bizverse/api/internal/security"
	"bizverse/api/internal/ws"

	"github.com/gorilla/websocket"
)

// newTestServer builds the real middleware chain with no database and adds
// lightweight probe routes (handlers never touch repos). The assertions
// target the chain itself (ordering, ETag, CSRF, auth).
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	deps := Deps{
		Config:      config.Config{JWTSecret: "test-secret-not-for-prod", AppEnv: "dev"},
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		RateLimiter: ratelimit.NewInMemory(),
	}
	s := NewServer(deps)
	s.mux.HandleFunc("GET /api/v1/test/etag", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	s.mux.HandleFunc("GET /api/v1/test/whoami", func(w http.ResponseWriter, r *http.Request) {
		user, found := currentUser(r)
		if !found {
			fail(w, domain.ErrNotAuthenticated)
			return
		}
		ok(w, map[string]any{"id": user.ID})
	})
	return s.Handler()
}

// Regression: the old ETag recorder swallowed every non-200 GET into a
// 200-with-empty-body.
func TestETagForwardsErrorsVerbatim(t *testing.T) {
	h := newTestServer(t)

	// Unauthenticated GET /api/v1/me → 401 must reach the client untouched.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 passthrough, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("error content type lost: %q", rec.Header().Get("Content-Type"))
	}
}

// ETags attach to successful JSON GETs and If-None-Match short-circuits.
func TestETagHappyPath(t *testing.T) {
	h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test/etag", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("probe should be 200, got %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header on JSON GET")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/test/etag", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match should yield 304, got %d", rec2.Code)
	}
}

// Mutations without a CSRF token are rejected before reaching handlers.
func TestCSRFRequiredForMutations(t *testing.T) {
	h := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/errors",
		strings.NewReader(`{"message":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected csrf rejection (403), got %d", rec.Code)
	}
}

// A tampered access token flows through the whole chain without breaking:
// auth passes through (no user context), rateLimit falls back to per-IP,
// and the route answers normally. (Full per-user-key ordering needs a DB;
// the chain wiring itself asserts auth-before-ratelimit by construction.)
func TestChainToleratesMissingUser(t *testing.T) {
	h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test/whoami", nil)
	req.AddCookie(&http.Cookie{Name: cookieAccess, Value: "tampered.token.value"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected clean 401 from route, got %d %s", rec.Code, rec.Body.String())
	}
}

// WS upgrades pass through the chain (path exempt from ETag buffering).
func TestWSUpgradeThroughChain(t *testing.T) {
	h := newTestServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	// No hub registered on this bare server: the upgrade reaches the mux and
	// fails as a normal route miss — but it must NOT fail with the recorder's
	// "not a Hijacker" protocol error (status 500 from the chain).
	if err == nil {
		resp.Body.Close()
		return // unexpected success is fine too (no hub wired here)
	}
	if resp != nil && resp.StatusCode >= 500 {
		t.Fatalf("upgrade broke in middleware: %v (%d)", err, resp.StatusCode)
	}
}

// REGRESSION (C1): the access-log recorder implemented neither Hijacker nor
// Flusher, so every authenticated WebSocket upgrade through api.Handler()
// failed with "websocket: response does not implement http.Hijacker" — all
// realtime features were silently dead in production wiring. The old test
// never authenticated, so it only exercised the 401 path and masked the bug.
func TestWSUpgradeAuthenticatedThroughChain(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	deps := Deps{
		Config:      config.Config{JWTSecret: "test-secret-not-for-prod", AppEnv: "dev"},
		Logger:      logger,
		RateLimiter: ratelimit.NewInMemory(),
		Hub:         ws.NewHub(logger, nil),
	}
	s := NewServer(deps)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	user := &domain.User{ID: "11111111-1111-4111-8111-111111111111", Role: domain.RoleUser}
	token, _, err := security.IssueToken(deps.Config.JWTSecret, security.TokenAccess, user, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	header := http.Header{}
	header.Add("Cookie", cookieAccess+"="+token)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		t.Fatalf("authenticated WS upgrade must succeed through the chain: %v (status=%d)", err, status)
	}
	defer conn.Close()

	var f Frame
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.ReadJSON(&f); err != nil {
		t.Fatalf("no welcome frame after upgrade: %v", err)
	}
	if f.Type != "welcome" {
		t.Fatalf("expected welcome frame, got %q", f.Type)
	}
}

// parsePositiveInt ceiling keeps absurd limits out of SQL.
func TestParsePositiveIntCapped(t *testing.T) {
	if got := parsePositiveInt("999999999", 20); got != 100 {
		t.Fatalf("expected cap at 100, got %d", got)
	}
	if got := parsePositiveInt("", 20); got != 20 {
		t.Fatalf("default broken: %d", got)
	}
}
