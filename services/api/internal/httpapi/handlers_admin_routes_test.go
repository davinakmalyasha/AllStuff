//go:build !race

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Every admin route is wrapped in s.adminOnly, and the handlers behind it are
// the moderation surface: suspend a user, hide content, delete a category. A
// route that forgot the wrapper, or a handler that panics, is a privilege bug
// rather than a broken page, so the whole table is asserted rather than a
// sample.

type adminRoute struct {
	method string
	path   string
}

// adminRouteTable mirrors the mux registrations in server.go. Params are filled
// with values that are well-formed but refer to nothing, so a handler reaches
// its own not-found path instead of dying in parameter parsing.
func adminRouteTable() []adminRoute {
	routes := []adminRoute{
		{"GET", "/api/v1/admin/claims"},
		{"POST", "/api/v1/admin/claims/{id}/decide"},
		{"POST", "/api/v1/admin/categories"},
		{"PATCH", "/api/v1/admin/categories/{id}"},
		{"DELETE", "/api/v1/admin/categories/{id}"},
		{"POST", "/api/v1/admin/categories/{id}/merge/{id}"},
		{"GET", "/api/v1/admin/verify"},
		{"GET", "/api/v1/admin/verify/{id}"},
		{"POST", "/api/v1/admin/verify/{id}/decide"},
		{"POST", "/api/v1/admin/verify/{id}/re-request"},
		{"GET", "/api/v1/admin/verify/{id}/documents/{id}/file"},
		{"GET", "/api/v1/admin/reports"},
		{"POST", "/api/v1/admin/reports/{id}/decide"},
		{"POST", "/api/v1/admin/content/review/{id}/hide"},
		{"POST", "/api/v1/admin/content/review/{id}/restore"},
		{"GET", "/api/v1/admin/users"},
		{"POST", "/api/v1/admin/users/{id}/ban"},
		{"GET", "/api/v1/admin/curation"},
		{"PUT", "/api/v1/admin/curation"},
		{"GET", "/api/v1/admin/moderation-actions"},
		{"GET", "/api/v1/admin/banned-words"},
		{"POST", "/api/v1/admin/banned-words"},
		{"DELETE", "/api/v1/admin/banned-words/spam"},
		{"GET", "/api/v1/admin/banned-words/allowlist"},
		{"PUT", "/api/v1/admin/banned-words/allowlist"},
		{"POST", "/api/v1/admin/businesses/{id}/suspend"},
		{"POST", "/api/v1/admin/businesses/{id}/restore"},
		{"GET", "/api/v1/admin/settings"},
		{"PUT", "/api/v1/admin/settings"},
		{"GET", "/api/v1/admin/appeals"},
		{"POST", "/api/v1/admin/appeals/{id}/decide"},
		{"GET", "/api/v1/admin/anomalies"},
		{"POST", "/api/v1/admin/anomalies/1/resolve"},
		{"GET", "/api/v1/admin/kpis"},
	}
	for i := range routes {
		routes[i].path = strings.ReplaceAll(routes[i].path, "{id}", util.NewUUID())
	}
	return routes
}

func adminTestServer(t *testing.T) (*Server, *testDeps) {
	t.Helper()
	cfg := testConfig(t)
	// The table makes ~100 requests; the default global bucket is 120/min per
	// IP and a 429 here would look like an authorization failure.
	cfg.RateLimitGlobal = 100000
	s, _, td := newDBTestServerWith(t, cfg)
	return s, td
}

func TestAdminRoutesRejectNonAdmins(t *testing.T) {
	s, td := adminTestServer(t)
	user := testutil.User(t, td.Repos)
	h := s.Handler()

	for _, rt := range adminRouteTable() {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			// Unauthenticated. A GET is refused as unauthenticated (401); a write
			// is refused by the CSRF layer first (403), because CSRF is checked
			// before auth. Both are refusals, and neither is a 5xx.
			anon := httptest.NewRequest(rt.method, rt.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, anon)
			want := http.StatusUnauthorized
			if rt.method != http.MethodGet && rt.method != http.MethodHead {
				want = http.StatusForbidden
			}
			if w.Code != want {
				t.Errorf("anonymous: status = %d, want %d", w.Code, want)
			}

			// A perfectly valid logged-in user is still refused.
			req := td.authed(t, rt.method, rt.path, []byte("{}"), user)
			w = httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("normal user: status = %d, want 403", w.Code)
			}
		})
	}
}

func TestAdminRoutesReachHandlers(t *testing.T) {
	s, td := adminTestServer(t)
	admin := testutil.Admin(t, td.Repos)
	// Admin features are gated on ENROLLED 2FA, not just on the role, so the
	// fixture is enrolled. The secret is never verified here - the middleware
	// only asks whether enrollment happened.
	// enabled_at has no default since 0047 - that migration removed the
	// DEFAULT now() which marked every factor enabled the instant it was
	// written - so enrollment has to set it explicitly.
	td.Repos.Exec(t, `INSERT INTO user_2fa (id, user_id, totp_secret_encrypted, recovery_codes_hash, enabled_at)
		VALUES ($1,$2,'x','[]'::jsonb, now())`,
		util.NewUUID(), admin.ID)
	var enrolled bool
	if err := td.Repos.QueryRow(t, `SELECT enabled_at IS NOT NULL FROM user_2fa WHERE user_id=$1`, admin.ID).Scan(&enrolled); err != nil {
		t.Fatalf("read enrollment: %v", err)
	}
	if !enrolled {
		t.Fatal("the admin fixture is not enrolled in 2FA")
	}
	h := s.Handler()

	for _, rt := range adminRouteTable() {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := td.authed(t, rt.method, rt.path, []byte("{}"), admin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			// The point is that the request got PAST the gate and the handler
			// answered: a 403 means the role check is broken, and a 5xx means
			// the handler panicked or failed on input it should have rejected.
			if w.Code == http.StatusForbidden {
				t.Fatalf("an admin was refused: %d %s", w.Code, w.Body.String())
			}
			if w.Code >= 500 {
				t.Fatalf("handler failed with %d: %s", w.Code, w.Body.String())
			}
			// And the response must never leak a stack trace or internal error
			// detail, whatever the status.
			if strings.Contains(w.Body.String(), "goroutine") {
				t.Error("the response contains a goroutine dump")
			}
		})
	}
}

func TestAdminRoutesRequireCSRFForWrites(t *testing.T) {
	s, td := adminTestServer(t)
	admin := testutil.Admin(t, td.Repos)
	h := s.Handler()

	// An admin session cookie alone must not authorise a state-changing request:
	// that is the whole reason the CSRF pair is separate.
	for _, rt := range adminRouteTable() {
		if rt.method == http.MethodGet || rt.method == http.MethodHead {
			continue
		}
		req := td.authedNoCSRF(t, rt.method, rt.path, []byte("{}"), admin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s without CSRF: status = %d, want 403", rt.method, rt.path, w.Code)
		}
	}
}

// The two defects the route table found: an unknown id surfaced as a 500, and
// the UPDATE itself failed at plan time because the CASE mixed the
// business_status enum with text, so EVERY suspend and restore was broken.
func TestAdminBusinessSuspendRestore(t *testing.T) {
	s, td := adminTestServer(t)
	admin := testutil.Admin(t, td.Repos)
	td.Repos.Exec(t, `INSERT INTO user_2fa (id, user_id, totp_secret_encrypted, recovery_codes_hash, enabled_at)
		VALUES ($1,$2,'x','[]'::jsonb, now())`, util.NewUUID(), admin.ID)
	h := s.Handler()

	cat := testutil.Category(t, td.Repos, "Cafe")
	mk := func(name string, status domain.BusinessStatus) *domain.Business {
		return testutil.Business(t, td.Repos, testutil.BusinessOpts{
			Name: name, Category: cat, Owner: testutil.User(t, td.Repos), Status: status,
			City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		})
	}
	live := mk("Suspend Me", domain.BusinessVerified)
	draft := mk("Draft Cafe", domain.BusinessDraft)

	do := func(action, id, reason string) *httptest.ResponseRecorder {
		method := http.MethodPost
		body := []byte(`{"reason":"` + reason + `"}`)
		req := td.authed(t, method, "/api/v1/admin/businesses/"+id+"/"+action, body, admin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	state := func(id string) (status, prev string) {
		if err := td.Repos.QueryRow(t, `SELECT status::text, coalesce(pre_suspend_status,'') FROM businesses WHERE id=$1`, id).
			Scan(&status, &prev); err != nil {
			t.Fatal(err)
		}
		return
	}

	// Unknown business: 404, not 500. Both verbs are checked, because only
	// restore runs the pre_suspend_status lookup and only suspend reaches the
	// UPDATE by itself.
	if w := do("suspend", util.NewUUID(), "rules"); w.Code != http.StatusNotFound {
		t.Errorf("suspend unknown: status = %d, want 404", w.Code)
	}
	if w := do("restore", util.NewUUID(), ""); w.Code != http.StatusNotFound {
		t.Errorf("restore unknown: status = %d, want 404", w.Code)
	}
	// A suspension needs a reason.
	if w := do("suspend", live.ID, "  "); w.Code != http.StatusBadRequest {
		t.Errorf("no reason: status = %d, want 400", w.Code)
	}

	if w := do("suspend", live.ID, "abusive listing"); w.Code != http.StatusOK {
		t.Fatalf("suspend: status = %d: %s", w.Code, w.Body.String())
	}
	if status, prev := state(live.ID); status != "suspended" || prev != "verified" {
		t.Errorf("after suspend: status=%q pre=%q, want suspended/verified", status, prev)
	}

	// Restore returns the listing to the status it had, not to 'verified'.
	if w := do("restore", live.ID, ""); w.Code != http.StatusOK {
		t.Fatalf("restore: status = %d: %s", w.Code, w.Body.String())
	}
	if status, _ := state(live.ID); status != "verified" {
		t.Errorf("after restore: status = %q, want verified", status)
	}

	// A suspended DRAFT must come back as a draft. Forcing 'verified' would
	// publish a listing its owner never finished.
	if w := do("suspend", draft.ID, "checking"); w.Code != http.StatusOK {
		t.Fatalf("suspend draft: %d", w.Code)
	}
	if w := do("restore", draft.ID, ""); w.Code != http.StatusOK {
		t.Fatalf("restore draft: %d", w.Code)
	}
	if status, _ := state(draft.ID); status != "draft" {
		t.Errorf("a restored draft came back as %q, want draft", status)
	}
}
