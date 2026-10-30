//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bizverse/api/internal/testutil"
)

// The waitlist routes: joining is authenticated and idempotent, leaving is
// scoped to the entry's owner, and the list is the caller's own.

func TestWaitlistRoutes(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	b, p, _ := bookingFixture(t, h)
	customer := testutil.User(t, h)
	stranger := testutil.User(t, h)
	day := bookingDay().Format("2006-01-02")

	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, "/api/v1/me/waitlist", nil))
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Errorf("anonymous waitlist: status = %d, want 401/403", w.Code)
	}

	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/waitlist",
		map[string]any{"product_id": p.ID, "day": day}, customer)
	if w.Code != http.StatusCreated {
		t.Fatalf("join: status = %d: %s", w.Code, w.Body.String())
	}
	var joined struct {
		Waitlist struct {
			ID  string `json:"id"`
			Day string `json:"day"`
		} `json:"waitlist"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &joined); err != nil {
		t.Fatal(err)
	}
	if joined.Waitlist.ID == "" || joined.Waitlist.Day != day {
		t.Fatalf("entry = %+v, want id and day %s", joined.Waitlist, day)
	}

	// Asking twice keeps one row and returns the same entry.
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/waitlist",
		map[string]any{"product_id": p.ID, "day": day}, customer)
	if w.Code != http.StatusCreated {
		t.Errorf("second join: status = %d", w.Code)
	}
	var again struct {
		Waitlist struct {
			ID string `json:"id"`
		} `json:"waitlist"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &again)
	if again.Waitlist.ID != joined.Waitlist.ID {
		t.Errorf("second join returned a different entry: %s vs %s", again.Waitlist.ID, joined.Waitlist.ID)
	}

	w = dash(t, td, hdl, http.MethodGet, "/api/v1/me/waitlist", nil, customer)
	if w.Code != http.StatusOK || !containsAll(w.Body.String(), joined.Waitlist.ID, p.Name) {
		t.Errorf("my waitlist: status = %d: %s", w.Code, w.Body.String())
	}

	// A malformed day and a missing product are validation/not-found, not a row.
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/waitlist",
		map[string]any{"product_id": p.ID, "day": "tomorrow"}, customer)
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed day: status = %d, want 400", w.Code)
	}
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/waitlist",
		map[string]any{"product_id": "", "day": day}, customer)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing product: status = %d, want 404", w.Code)
	}

	// Another user cannot remove the entry.
	w = dash(t, td, hdl, http.MethodDelete, "/api/v1/waitlist/"+joined.Waitlist.ID, nil, stranger)
	if w.Code != http.StatusNotFound {
		t.Errorf("stranger leave: status = %d, want 404", w.Code)
	}
	w = dash(t, td, hdl, http.MethodDelete, "/api/v1/waitlist/"+joined.Waitlist.ID, nil, customer)
	if w.Code != http.StatusNoContent {
		t.Errorf("leave: status = %d, want 204", w.Code)
	}
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/me/waitlist", nil, customer)
	if w.Code != http.StatusOK || containsAll(w.Body.String(), joined.Waitlist.ID) {
		t.Errorf("waitlist after leave: %s", w.Body.String())
	}
}

func containsAll(body string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(body, p) {
			return false
		}
	}
	return true
}
