//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Deleting a review is a soft delete owned by the review's author. The route
// had never been executed. Its authorization check is the whole point: a
// business owner can reply to a review but must not be able to erase it, and a
// deleted review has to disappear from the public list while the row remains
// for moderation history.

func TestReviewDelete(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	author := testutil.User(t, h)
	stranger := testutil.User(t, h)
	cat := testutil.Category(t, h, "Review Delete Category")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Review Delete Cafe", Slug: "review-delete-cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	review := testutil.Review(t, h, b.ID, author.ID, 5)
	path := "/api/v1/reviews/" + review.ID

	anon := func(t *testing.T, method, path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, authReq(t, method, path, nil))
		return w
	}

	if w := anon(t, http.MethodDelete, path); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous delete: status = %d, want 401: %s", w.Code, w.Body.String())
	}
	// The business owner's power is a reply, not deletion. If this ever passes,
	// an owner can silently remove criticism.
	if w := dash(t, td, hdl, http.MethodDelete, path, nil, owner); w.Code != http.StatusForbidden {
		t.Errorf("business owner delete: status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodDelete, path, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger delete: status = %d, want 403: %s", w.Code, w.Body.String())
	}
	var deleted bool
	if err := h.QueryRow(t, `SELECT deleted_at IS NOT NULL FROM reviews WHERE id = $1`, review.ID).Scan(&deleted); err != nil {
		t.Fatalf("read review: %v", err)
	}
	if deleted {
		t.Fatal("a refused delete soft-deleted the review")
	}

	if w := dash(t, td, hdl, http.MethodDelete, path, nil, author); w.Code != http.StatusNoContent {
		t.Fatalf("author delete: status = %d: %s", w.Code, w.Body.String())
	}
	if err := h.QueryRow(t, `SELECT deleted_at IS NOT NULL FROM reviews WHERE id = $1`, review.ID).Scan(&deleted); err != nil {
		t.Fatalf("read review after delete: %v", err)
	}
	if !deleted {
		t.Error("delete answered 204 without setting deleted_at")
	}

	// The public list is what the storefront renders, so the review must be
	// gone from it, not merely marked.
	w := anon(t, http.MethodGet, "/api/v1/businesses/"+b.ID+"/reviews")
	if w.Code != http.StatusOK {
		t.Fatalf("list reviews: status = %d: %s", w.Code, w.Body.String())
	}
	var list struct {
		Reviews []struct {
			ID string `json:"id"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("list JSON: %v", err)
	}
	if list.Reviews == nil {
		t.Fatal("review list is null, want an array")
	}
	for _, r := range list.Reviews {
		if r.ID == review.ID {
			t.Errorf("deleted review %s is still listed", review.ID)
		}
	}

	// A deleted review is gone for every caller, including its author.
	if w := dash(t, td, hdl, http.MethodDelete, path, nil, author); w.Code != http.StatusNotFound {
		t.Errorf("second delete: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/reviews/"+util.NewUUID(), nil, author); w.Code != http.StatusNotFound {
		t.Errorf("unknown review: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	// ...and a malformed one is the same 404, not a Postgres 22P02 behind a 500.
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/reviews/not-a-uuid", nil, author); w.Code != http.StatusNotFound {
		t.Errorf("malformed review id: status = %d, want 404: %s", w.Code, w.Body.String())
	}
}
