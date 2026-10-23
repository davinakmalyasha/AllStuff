//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
)

// The owner wizard had no coverage: create, list, get, patch, submit, resubmit
// and the one-time slug change. It is the flow every business goes through, and
// the authorisation on it is per-listing rather than per-role.

func dash(t *testing.T, td *testDeps, h http.Handler, method, path string, body any, u *domain.User) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw = jsonBody(t, body)
	}
	req := td.authed(t, method, path, raw, u)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

type businessEnvelope struct {
	Business struct {
		ID       string `json:"id"`
		Slug     string `json:"slug"`
		Status   string `json:"status"`
		Name     string `json:"name"`
		OwnerID  string `json:"owner_id"`
		Category string `json:"category_id"`
		Desc     string `json:"description"`
	} `json:"business"`
}

func TestBusinessLifecycle(t *testing.T) {
	s, tu, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, tu)
	stranger := testutil.User(t, tu)
	// Categories are two levels deep in production and submission requires a
	// leaf, so the fixture builds the parent too.
	parent := testutil.Category(t, tu, "Cafe")
	cat := testutil.ChildCategory(t, tu, parent, "Coffee")

	// An unverified account cannot start a listing at all.
	unverified := testutil.UserOpts_(t, tu, testutil.UserOpts{Unverified: true})
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses", nil, unverified); w.Code != http.StatusForbidden {
		t.Errorf("unverified create: status = %d, want 403", w.Code)
	}

	w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses", nil, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d: %s", w.Code, w.Body.String())
	}
	var created businessEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := created.Business.ID
	if id == "" || created.Business.Slug == "" {
		t.Fatalf("create returned no id/slug: %s", w.Body.String())
	}
	if created.Business.Status != string(domain.BusinessDraft) {
		t.Errorf("new listing status = %q, want draft", created.Business.Status)
	}

	// It appears in the owner's list and is readable by the owner.
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/businesses", nil, owner)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), id) {
		t.Errorf("list: status=%d, body does not contain the new listing", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/businesses/"+id, nil, owner); w.Code != http.StatusOK {
		t.Errorf("owner get: status = %d", w.Code)
	}

	// Another account cannot read or edit it. The response must not be a 5xx and
	// must not be a success - not-found is the honest answer for a listing that
	// is not yours.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/businesses/"+id, nil, stranger); w.Code == http.StatusOK || w.Code >= 500 {
		t.Errorf("stranger get: status = %d", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPatch, "/api/v1/businesses/"+id, map[string]any{"name": "Hijacked"}, stranger); w.Code == http.StatusOK || w.Code >= 500 {
		t.Errorf("stranger patch: status = %d", w.Code)
	}

	// A draft cannot be submitted empty.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+id+"/submit", nil, owner); w.Code != http.StatusBadRequest {
		t.Errorf("submit missing fields: status = %d, want 400", w.Code)
	}

	// Fill in what submission requires.
	patch := map[string]any{
		"name":        "Kopi Senja",
		"description": "A small coffee shop serving single-origin beans from the highlands, open every morning.",
		"category_id": cat.ID,
		"address":     "Jl. Merdeka 10",
		"lat":         -6.2,
		"lng":         106.8,
		"city":        "Jakarta",
		"country":     "ID",
		"contact":     map[string]any{"phone": "+62 812 3456 7890"},
		"logo_url":    "https://cdn.example.test/logo.png",
	}
	w = dash(t, td, hdl, http.MethodPatch, "/api/v1/businesses/"+id, patch, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: status = %d: %s", w.Code, w.Body.String())
	}
	var updated businessEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Business.Name != "Kopi Senja" {
		t.Errorf("name = %q after patch", updated.Business.Name)
	}

	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+id+"/submit", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("submit: status = %d: %s", w.Code, w.Body.String())
	}
	var submitted businessEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &submitted)
	if submitted.Business.Status != string(domain.BusinessPending) {
		t.Errorf("status = %q after submit, want pending_review", submitted.Business.Status)
	}
	// Submitting twice is refused: it is already in review.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+id+"/submit", nil, owner); w.Code != http.StatusBadRequest {
		t.Errorf("second submit: status = %d, want 400", w.Code)
	}

	// The slug can be changed once, and only once (PRD §8.2).
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+id+"/slug-change", nil, owner); w.Code != http.StatusOK {
		t.Fatalf("first slug change: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+id+"/slug-change", nil, owner); w.Code != http.StatusBadRequest {
		t.Errorf("second slug change: status = %d, want 400", w.Code)
	}
}

// A rejected listing can be edited and resubmitted; anything else cannot.
func TestBusinessResubmitOnlyFromRejected(t *testing.T) {
	s, tu, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, tu)
	parent := testutil.Category(t, tu, "Cafe")
	cat := testutil.ChildCategory(t, tu, parent, "Coffee")
	b := testutil.Business(t, tu, testutil.BusinessOpts{
		Name: "Rejected Cafe", Category: cat, Owner: owner, Status: domain.BusinessRejected,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	// Give it what submission requires: the rejection had a reason, and the owner
	// has since supplied the missing pieces.
	tu.Exec(t, `UPDATE businesses SET logo_url='https://cdn.example.test/logo.png',
		contact='{"phone":"+62 812 3456 7890"}'::jsonb,
		description='A rejected listing whose owner has now fixed everything that was wrong with it.'
		WHERE id=$1`, b.ID)

	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/resubmit", nil, owner); w.Code != http.StatusOK {
		t.Errorf("resubmit a rejected listing: status = %d: %s", w.Code, w.Body.String())
	}

	// The cap is three. A rejection is the only state that may resubmit, so the
	// status is reset between attempts - which is also what happens in practice
	// when an admin rejects each resubmission.
	// One resubmit has already succeeded above, so two more reach the cap.
	for i := 0; i < 2; i++ {
		tu.Exec(t, `UPDATE businesses SET status='rejected' WHERE id=$1`, b.ID)
		if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/resubmit", nil, owner); w.Code != http.StatusOK {
			t.Fatalf("resubmit %d: status = %d: %s", i+2, w.Code, w.Body.String())
		}
	}
	tu.Exec(t, `UPDATE businesses SET status='rejected' WHERE id=$1`, b.ID)
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/resubmit", nil, owner); w.Code != http.StatusBadRequest {
		t.Errorf("fourth resubmit: status = %d, want 400 (cap is 3)", w.Code)
	}

	// A draft is submitted, not resubmitted.
	draft := testutil.Business(t, tu, testutil.BusinessOpts{
		Name: "Draft Cafe", Category: cat, Owner: owner, Status: domain.BusinessDraft,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	// Every submission requirement except the status, so the gate is the only
	// thing that can refuse the resubmit.
	tu.Exec(t, `UPDATE businesses SET logo_url='https://cdn.example.test/logo.png' WHERE id=$1`, draft.ID)
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+draft.ID+"/resubmit", nil, owner); w.Code == http.StatusOK {
		t.Error("resubmit accepted a draft")
	}
}

// The fixture supplies every submission requirement except the logo, which makes
// the logo rule the only one that can refuse. Asserting on the FIELD rather than
// the status keeps it honest: an empty draft also answers 400, but for the
// description, and that would prove nothing about this rule.
func TestBusinessSubmitRequiresALogo(t *testing.T) {
	s, tu, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, tu)
	parent := testutil.Category(t, tu, "Cafe")
	leaf := testutil.ChildCategory(t, tu, parent, "Coffee")
	b := testutil.Business(t, tu, testutil.BusinessOpts{
		Name: "No Logo Cafe", Category: leaf, Owner: owner, Status: domain.BusinessDraft,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})

	w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/submit", nil, owner)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("submit without a logo: status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "logo_url") {
		t.Errorf("refused for the wrong reason: %s", w.Body.String())
	}

	// With a logo it goes through, so the refusal above really was the logo.
	tu.Exec(t, `UPDATE businesses SET logo_url='https://cdn.example.test/logo.png' WHERE id=$1`, b.ID)
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/submit", nil, owner); w.Code != http.StatusOK {
		t.Errorf("submit with a logo: status = %d: %s", w.Code, w.Body.String())
	}
}
