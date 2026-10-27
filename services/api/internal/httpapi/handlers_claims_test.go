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

// Claim-a-business: submission is open to any signed-in user, but a claim
// carries an address and an evidence note, so the LIST is the sensitive part.
// A non-admin may see their own claims and nobody else's; the admin queue is
// behind the admin check, not merely behind a different path.

func TestClaimRoutes(t *testing.T) {
	// Pool-bound: an approved claim creates a business through the service, and
	// claims.Decide is one of the paths that opens its own transaction.
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	alice := testutil.User(t, h)
	bob := testutil.User(t, h)
	owner := testutil.User(t, h)
	admin := testutil.Admin(t, h)
	b := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner, Status: domain.BusinessVerified})

	// No session, no claim.
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, "/api/v1/me/claims", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous claim list: status = %d, want 401", w.Code)
	}
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, "/api/v1/claims", jsonBody(t, map[string]any{})))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous claim submit: status = %d, want 401", w.Code)
	}

	// Name, address and city are all required; the service names the field.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/claims", map[string]any{"address": "1 St", "city": "Jakarta"}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("claim without a name: status = %d, want 400", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/claims", map[string]any{"name": "New Place", "city": "Jakarta"}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("claim without an address: status = %d, want 400", w.Code)
	}

	claim := map[string]any{
		"name":     "Alice's New Place",
		"address":  "12 Claim Street",
		"city":     "Jakarta",
		"country":  "ID",
		"evidence": "I run this shop.",
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/claims", claim, alice); w.Code != http.StatusCreated {
		t.Fatalf("submit claim: status = %d: %s", w.Code, w.Body.String())
	}

	// The list is scoped to the caller. Alice sees hers; Bob, who is also a
	// signed-in user, must not see her address or evidence.
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/me/claims", nil, alice)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Alice's New Place") {
		t.Errorf("own claims: status = %d, body = %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me/claims", nil, bob); strings.Contains(w.Body.String(), "Alice's New Place") {
		t.Errorf("another user's claim is visible: %s", w.Body.String())
	}

	// Claiming a listing that already has an owner is refused, and the refusal
	// names the field rather than creating a claim nobody can approve.
	owned := map[string]any{
		"name":        "Hijack Attempt",
		"address":     "1 Test Street",
		"city":        "Jakarta",
		"business_id": b.ID,
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/claims", owned, bob); w.Code != http.StatusBadRequest {
		t.Errorf("claim an owned listing: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	// An id for a category that does not exist is a validation error, not a 500.
	unknownCat := map[string]any{
		"name": "Ghost Category Place", "address": "1 St", "city": "Jakarta",
		"category_id": util.NewUUID(),
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/claims", unknownCat, bob); w.Code != http.StatusBadRequest {
		t.Errorf("claim with an unknown category: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// Admin features are gated on ENROLLED 2FA as well as the role, so the
	// fixture is enrolled exactly the way handlers_admin_routes_test.go does it.
	h.ExecPool(t, `INSERT INTO user_2fa (id, user_id, totp_secret_encrypted, recovery_codes_hash, enabled_at)
		VALUES ($1,$2,'x','[]'::jsonb, now())`, util.NewUUID(), admin.ID)

	// The admin queue lists everybody's claims - for an admin only. A normal
	// signed-in user must not reach it by knowing the path, and an admin who has
	// not enrolled a second factor is refused with a code the UI can explain.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/admin/claims", nil, alice); w.Code != http.StatusForbidden {
		t.Errorf("admin queue as a normal user: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/admin/claims", nil, admin); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Alice's New Place") {
		t.Errorf("admin queue: status = %d, body = %s", w.Code, w.Body.String())
	}
}
