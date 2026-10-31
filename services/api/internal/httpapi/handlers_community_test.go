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

// Community: Q&A, follows, category follows and owner announcements. The Q&A
// service has its own tests; these are the HTTP edges - who may read, who may
// write, and what an unknown id does - plus the follow/feed rules, which had no
// test at any level.

func TestCommunityQARoutes(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	asker := testutil.User(t, h)
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Q&A Category")
	b := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner, Category: cat, Status: domain.BusinessVerified})
	questions := "/api/v1/businesses/" + b.ID + "/questions"

	// Reading is public; a request with no session is a normal request.
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, questions, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"questions"`) {
		t.Errorf("public questions: status = %d, body = %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodPost, questions, jsonBody(t, map[string]any{"text": "Do you take reservations?"})))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous ask: status = %d, want 401", w.Code)
	}

	if w := dash(t, td, hdl, http.MethodPost, questions, map[string]any{"text": "Do you take reservations?"}, asker); w.Code != http.StatusCreated {
		t.Fatalf("ask: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, questions, map[string]any{"text": "   "}, asker); w.Code != http.StatusBadRequest {
		t.Errorf("empty question: status = %d, want 400", w.Code)
	}

	var questionID string
	if err := h.Pool.QueryRow(t.Context(), `SELECT id FROM questions WHERE business_id=$1 LIMIT 1`, b.ID).Scan(&questionID); err != nil {
		t.Fatalf("read question id: %v", err)
	}
	answer := "/api/v1/questions/" + questionID + "/answers"
	if w := dash(t, td, hdl, http.MethodPost, answer, map[string]any{"text": "Yes, after 6pm."}, owner); w.Code != http.StatusCreated {
		t.Fatalf("answer: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, answer, map[string]any{"text": "   "}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("empty answer: status = %d, want 400", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/questions/"+util.NewUUID()+"/answers", map[string]any{"text": "Nobody asked this."}, owner); w.Code != http.StatusNotFound {
		t.Errorf("answer to an unknown question: status = %d, want 404", w.Code)
	}
	// The question is listed with its answer attached.
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, questions, nil))
	if !strings.Contains(w.Body.String(), "Do you take reservations?") || !strings.Contains(w.Body.String(), "Yes, after 6pm.") {
		t.Errorf("the asked question is not listed with its answer: %s", w.Body.String())
	}
}

func TestCommunityFollowAndFeed(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	follower := testutil.User(t, h)
	owner := testutil.User(t, h)
	stranger := testutil.User(t, h)
	cat := testutil.Category(t, h, "Feed Category")
	b := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner, Category: cat, Status: domain.BusinessVerified})
	follow := "/api/v1/businesses/" + b.ID + "/follow"

	if w := dash(t, td, hdl, http.MethodPut, follow, nil, follower); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"following":true`) {
		t.Fatalf("follow: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, follow, nil, follower); !strings.Contains(w.Body.String(), `"following":true`) {
		t.Errorf("follow state after following: %s", w.Body.String())
	}
	// The owner cannot follow their own listing; follower counts feed trending,
	// so self-follows would be a ranking vote.
	if w := dash(t, td, hdl, http.MethodPut, follow, nil, owner); w.Code != http.StatusBadRequest {
		t.Errorf("owner following own business: status = %d, want 400", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPut, "/api/v1/businesses/"+util.NewUUID()+"/follow", nil, follower); w.Code != http.StatusNotFound {
		t.Errorf("follow an unknown business: status = %d, want 404", w.Code)
	}

	updates := "/api/v1/businesses/" + b.ID + "/updates"
	post := map[string]any{"title": "Fresh beans in", "body": "We just roasted a new batch this morning."}
	if w := dash(t, td, hdl, http.MethodPost, updates, post, owner); w.Code != http.StatusCreated {
		t.Fatalf("post update: status = %d: %s", w.Code, w.Body.String())
	}
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, updates, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Fresh beans in") {
		t.Errorf("public updates: status = %d, body = %s", w.Code, w.Body.String())
	}
	// Only a manager may announce, and the validation is not advisory.
	if w := dash(t, td, hdl, http.MethodPost, updates, post, stranger); w.Code != http.StatusForbidden {
		t.Errorf("update by a stranger: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, updates, map[string]any{"title": "Hi", "body": "Not long enough."}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("short title: status = %d, want 400", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, updates, map[string]any{"title": "Fresh beans in", "body": "short"}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("short body: status = %d, want 400", w.Code)
	}

	// The feed is built from follows, per user: the follower sees the
	// announcement, the stranger does not. A feed shared between accounts is the
	// failure this asserts against, not merely an empty list.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me/following-feed", nil, follower); !strings.Contains(w.Body.String(), "Fresh beans in") {
		t.Errorf("follower's feed is missing the announcement: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me/following-feed", nil, stranger); strings.Contains(w.Body.String(), "Fresh beans in") {
		t.Errorf("a non-follower's feed contains the announcement: %s", w.Body.String())
	}

	if w := dash(t, td, hdl, http.MethodDelete, follow, nil, follower); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"following":false`) {
		t.Errorf("unfollow: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me/following-feed", nil, follower); strings.Contains(w.Body.String(), "Fresh beans in") {
		t.Errorf("the feed kept an announcement after unfollowing its business: %s", w.Body.String())
	}
}

func TestCategoryFollowRoutes(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	u := testutil.User(t, h)
	cat := testutil.Category(t, h, "Follow Category")
	path := "/api/v1/categories/" + cat.ID + "/follow"

	if w := dash(t, td, hdl, http.MethodPut, path, nil, u); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"following":true`) {
		t.Fatalf("follow category: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, path, nil, u); !strings.Contains(w.Body.String(), `"following":true`) {
		t.Errorf("category follow state: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodDelete, path, nil, u); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"following":false`) {
		t.Errorf("unfollow category: status = %d: %s", w.Code, w.Body.String())
	}

	// A follow row has a foreign key. Without a lookup first, an id for a
	// category that no longer exists reaches the INSERT and comes back as a
	// constraint violation - a 500 for a request the caller can fix.
	missing := "/api/v1/categories/" + util.NewUUID() + "/follow"
	if w := dash(t, td, hdl, http.MethodPut, missing, nil, u); w.Code != http.StatusNotFound {
		t.Errorf("follow an unknown category: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	var n int
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM user_category_follows WHERE user_id=$1`, u.ID).Scan(&n); err != nil {
		t.Fatalf("count follows: %v", err)
	}
	if n != 0 {
		t.Errorf("a refused follow still wrote %d rows", n)
	}
}
