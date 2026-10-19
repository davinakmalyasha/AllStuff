//go:build !race

// Database-backed tests for the handler layer.
//
// The tag matches the rest of the database-backed suites: testutil clones a
// database per case, which is too slow to also run under the race detector.
// Everything that uses newDBTestServer must carry the same tag, or the -race build
// loses the harness and fails to compile.
package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/ratelimit"
	"bizverse/api/internal/security"
	"bizverse/api/internal/service"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
	"bizverse/api/internal/ws"
)

// testDeps mirrors what cmd/api/main.go wires at boot, against a per-test
// database instead of a pool.
//
// This duplication is deliberate but it is also a real cost: a service added to
// main.go and not added here makes every test in this package exercise a Deps
// with a nil field, and the failure is a nil dereference inside a handler rather
// than a compile error. The handlers that need a field assert it is non-nil
// first, so the gap surfaces as "the harness does not wire X" instead of a panic.
type testDeps struct {
	Config config.Config
	Repos  *testutil.H
	Deps   Deps
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		AppEnv:      "test",
		PublicURL:   "https://bizverse.test",
		JWTSecret:   "test-signing-secret-for-the-handler-harness-0123456789",
		MediaDir:    t.TempDir(),
		MediaBase:   "/media",
		CORSOrigins: []string{"https://bizverse.test"},
		WSOrigins:   []string{"https://bizverse.test"},
	}
}

func newDBTestServer(t *testing.T) (*Server, *testutil.H) {
	t.Helper()
	s, h, _ := newDBTestServerWith(t, testConfig(t))
	return s, h
}

// newDBTestServerDeps is for tests that need the wired services as well as the
// server - uploading through deps.Media rather than a second Media pointed at a
// different directory, for instance.
func newDBTestServerDeps(t *testing.T) (*Server, *testutil.H, *testDeps) {
	t.Helper()
	return newDBTestServerWith(t, testConfig(t))
}

func newDBTestServerWith(t *testing.T, cfg config.Config) (*Server, *testutil.H, *testDeps) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// An empty ResendConfig selects the log sender, so no mail leaves the test.
	sender := email.NewSender(email.ResendConfig{AppEnv: "test", PublicURL: cfg.PublicURL})
	hub := ws.NewHub(logger, cfg.WSOrigins)
	// A client with no secret key is the "Stripe not configured" deployment,
	// which still resolves the free plan.
	stripeClient := security.NewStripeClient("", "")

	billingSvc := service.NewBilling(h.Repos, cfg, stripeClient, logger)
	notifier := service.NewNotifier(h.Repos, cfg, sender, hub, logger)
	authSvc := service.NewAuth(h.Repos, cfg, sender, logger)
	userSvc := service.NewUsers(h.Repos)
	categorySvc := service.NewCategories(h.Repos)
	businessSvc := service.NewBusinesses(h.Repos, billingSvc)
	productSvc := service.NewProducts(h.Repos, notifier, billingSvc)
	analyticsSvc := service.NewAnalytics(h.Repos)
	engagementSvc := service.NewEngagement(h.Repos, notifier)
	trendingSvc := service.NewTrending(h.Repos, notifier)
	chatSvc := service.NewChat(h.Repos, notifier)
	currencySvc := service.NewCurrency(h.Repos)
	oauthSvc := service.NewOAuth(h.Repos, cfg)
	communitySvc := service.NewCommunity(h.Repos, notifier)
	profilesSvc := service.NewProfiles(h.Repos, notifier)
	invitesSvc := service.NewInvites(h.Repos, cfg, sender, billingSvc)
	searchSvc := service.NewSearch(h.Repos)
	adminSvc := service.NewAdmin(h.Repos, notifier)
	mediaSvc := service.NewMedia(h.Repos, cfg.MediaDir, cfg.MediaBase, cfg.MediaEncryptionKey, cfg.ClamAVAddr)
	citiesSvc := service.NewCities(h.Repos)
	apiKeysSvc := service.NewAPIKeys(h.Repos)
	claimsSvc := service.NewClaims(h.Repos, notifier)

	deps := Deps{
		Config:      cfg,
		Logger:      logger,
		Repos:       h.Repos,
		Auth:        authSvc,
		Users:       userSvc,
		Categories:  categorySvc,
		Businesses:  businessSvc,
		Products:    productSvc,
		Analytics:   analyticsSvc,
		Engagement:  engagementSvc,
		Trending:    trendingSvc,
		Chat:        chatSvc,
		Currency:    currencySvc,
		OAuth:       oauthSvc,
		Community:   communitySvc,
		Profiles:    profilesSvc,
		Invites:     invitesSvc,
		Search:      searchSvc,
		Admin:       adminSvc,
		Media:       mediaSvc,
		Cities:      citiesSvc,
		APIKeys:     apiKeysSvc,
		Claims:      claimsSvc,
		Billing:     billingSvc,
		Notifier:    notifier,
		RateLimiter: ratelimit.NewInMemory(),
		Hub:         hub,
	}
	return NewServer(deps), h, &testDeps{Config: cfg, Repos: h, Deps: deps}
}

// authed builds a request that passes the same checks a browser would: a signed
// access token in the cookie the middleware reads, and - for anything that is
// not a GET - a matching CSRF cookie and header pair.
//
// The token is minted with security.IssueToken rather than by driving the login
// handler, so a test that needs an authenticated caller does not depend on the
// login flow being correct. Tests of login itself do it the other way.
func (td *testDeps) authed(t *testing.T, method, path string, body []byte, u *domain.User) *http.Request {
	t.Helper()
	token, _, err := security.IssueToken(td.Config.JWTSecret, security.TokenAccess, u, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.AddCookie(&http.Cookie{Name: "bv_access", Value: token})
	if method != http.MethodGet && method != http.MethodHead {
		csrf := util.NewUUID()
		req.AddCookie(&http.Cookie{Name: "bv_csrf", Value: csrf, Path: "/"})
		req.Header.Set(security.CSRFHeader, csrf)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// authedNoCSRF is authed with the CSRF pair omitted, for asserting that the
// middleware refuses a state-changing request without it.
func (td *testDeps) authedNoCSRF(t *testing.T, method, path string, body []byte, u *domain.User) *http.Request {
	t.Helper()
	req := td.authed(t, method, path, body, u)
	req.Header.Del(security.CSRFHeader)
	out := httptest.NewRequest(method, path, nil)
	out.Header = req.Header
	for _, ck := range req.Cookies() {
		if ck.Name != "bv_csrf" {
			out.AddCookie(ck)
		}
	}
	return out
}
