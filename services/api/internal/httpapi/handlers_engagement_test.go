//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
)

// The engagement handlers were never executed by a test: likes, recommends,
// collections, comments, reviews, notifications, leaderboards and reports. The
// service layer has tests, so these assertions are mostly about the wiring -
// except that the wiring is where authorization can go missing. A handler that
// forgets to check a nil collection does not fail any service test.

func TestEngagementRoutes(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	coOwner := testutil.User(t, h)
	alice := testutil.User(t, h)
	bob := testutil.User(t, h)
	cat := testutil.Category(t, h, "Engagement Category")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Engagement Coffee", Slug: "engagement-coffee", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	product := testutil.Product(t, h, b.ID, "House Blend")
	inviteAccepted(t, h, b.ID, owner.ID, coOwner.ID, "co_owner")

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
	// decode unmarshals a 2xx body and turns anything else into a fatal that
	// prints the server's error, which is what makes a status regression
	// readable.
	decode := func(t *testing.T, w *httptest.ResponseRecorder, v any) {
		t.Helper()
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
			t.Fatalf("not JSON: %v: %s", err, w.Body.String())
		}
	}

	// ---- likes ----
	path := "/api/v1/likes/business/" + b.ID
	var liked struct {
		Liked bool `json:"liked"`
	}
	decode(t, do(t, http.MethodPut, path, nil, alice), &liked)
	if !liked.Liked {
		t.Error("PUT like did not report liked=true")
	}
	decode(t, do(t, http.MethodGet, path, nil, alice), &liked)
	if !liked.Liked {
		t.Error("GET like state lost the like")
	}
	decode(t, do(t, http.MethodDelete, path, nil, alice), &liked)
	if liked.Liked {
		t.Error("DELETE like did not report liked=false")
	}
	// A like is a row in a uuid column: an unknown type is a field error, not a
	// 500 from Postgres.
	if w := do(t, http.MethodPut, "/api/v1/likes/widget/"+b.ID, nil, alice); w.Code != http.StatusBadRequest {
		t.Errorf("unknown like target type: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodPut, "/api/v1/likes/business/"+b.ID, nil, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous like: status = %d, want 401", w.Code)
	}
	// A product is a likeable target too, and the handler must pass the path
	// value through unchanged.
	decode(t, do(t, http.MethodPut, "/api/v1/likes/product/"+product.ID, nil, alice), &liked)
	if !liked.Liked {
		t.Error("a product could not be liked")
	}

	// ---- recommends ----
	var rec struct {
		Recommended bool `json:"recommended"`
	}
	decode(t, do(t, http.MethodPut, "/api/v1/recommends/"+b.ID, nil, alice), &rec)
	if !rec.Recommended {
		t.Error("PUT recommend did not report recommended=true")
	}
	decode(t, do(t, http.MethodGet, "/api/v1/recommends/"+b.ID, nil, alice), &rec)
	if !rec.Recommended {
		t.Error("GET recommend state lost the recommendation")
	}
	decode(t, do(t, http.MethodDelete, "/api/v1/recommends/"+b.ID, nil, alice), &rec)
	if rec.Recommended {
		t.Error("DELETE recommend did not report recommended=false")
	}
	// Self-recommendation is suppressed for a manager: it must not report true,
	// and it must not write an event. A co-owner is a manager here exactly as
	// they are for likes, replies and analytics - a co-owner recommending the
	// business they run is the same self-inflation the owner rule exists to stop.
	for name, u := range map[string]*domain.User{"owner": owner, "co-owner": coOwner} {
		decode(t, do(t, http.MethodPut, "/api/v1/recommends/"+b.ID, nil, u), &rec)
		if rec.Recommended {
			t.Errorf("the %s's own recommendation was recorded", name)
		}
		decode(t, do(t, http.MethodGet, "/api/v1/recommends/"+b.ID, nil, u), &rec)
		if rec.Recommended {
			t.Errorf("the %s has a stored self-recommendation", name)
		}
	}

	// ---- collections ----
	var created struct {
		Collection struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			IsPublic bool   `json:"is_public"`
		} `json:"collection"`
	}
	w := do(t, http.MethodPost, "/api/v1/me/collections", map[string]any{"name": "Weekend List"}, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create collection: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &created)
	collectionID := created.Collection.ID
	if collectionID == "" || created.Collection.Name != "Weekend List" {
		t.Fatalf("created collection = %+v", created.Collection)
	}
	// Names are validated in the service, but the handler must turn that into a
	// 400 rather than a constraint violation.
	if w := do(t, http.MethodPost, "/api/v1/me/collections", map[string]any{"name": ""}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("empty collection name: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// Save a business into it, then read it back.
	if w := do(t, http.MethodPost, "/api/v1/me/collections/"+collectionID+"/items",
		map[string]any{"target_type": "business", "target_id": b.ID}, alice); w.Code != http.StatusCreated {
		t.Fatalf("save to collection: status = %d: %s", w.Code, w.Body.String())
	}
	var items struct {
		Items []struct {
			ID         string `json:"id"`
			TargetID   string `json:"target_id"`
			TargetName string `json:"target_name"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/collections/"+collectionID+"/items", nil, alice), &items)
	if len(items.Items) != 1 || items.Items[0].TargetID != b.ID || items.Items[0].TargetName != "Engagement Coffee" {
		t.Fatalf("collection items = %+v", items.Items)
	}
	if items.HasMore {
		t.Error("has_more is true for a single item")
	}
	itemID := items.Items[0].ID

	var saves struct {
		CollectionIDs []string `json:"collection_ids"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/saved/business/"+b.ID, nil, alice), &saves)
	if len(saves.CollectionIDs) != 1 || saves.CollectionIDs[0] != collectionID {
		t.Errorf("save state for the owner = %v, want [%s]", saves.CollectionIDs, collectionID)
	}
	// Another user's save state is their own: no collection ids.
	decode(t, do(t, http.MethodGet, "/api/v1/saved/business/"+b.ID, nil, bob), &saves)
	if len(saves.CollectionIDs) != 0 {
		t.Errorf("bob sees alice's saves: %v", saves.CollectionIDs)
	}

	// AUTHORIZATION: a collection id is not a capability. Bob knows the id (it is
	// in a URL alice may share) and must not be able to rename, read or empty
	// alice's collection.
	if w := do(t, http.MethodPatch, "/api/v1/me/collections/"+collectionID,
		map[string]any{"name": "Hijacked"}, bob); w.Code != http.StatusNotFound {
		t.Errorf("a stranger renamed a collection: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodGet, "/api/v1/me/collections/"+collectionID+"/items", nil, bob); w.Code != http.StatusNotFound {
		t.Errorf("a stranger read a collection's items: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodDelete, "/api/v1/me/collections/"+collectionID+"/items/"+itemID, nil, bob); w.Code != http.StatusNotFound {
		t.Errorf("a stranger removed a collection item: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	// And the item is still there after those attempts.
	decode(t, do(t, http.MethodGet, "/api/v1/me/collections/"+collectionID+"/items", nil, alice), &items)
	if len(items.Items) != 1 {
		t.Fatalf("the collection lost its item to a stranger: %+v", items.Items)
	}

	// The owner can rename it, and an explicit empty cover clears rather than
	// stores an empty uuid.
	var updated struct {
		Collection struct {
			Name string `json:"name"`
		} `json:"collection"`
	}
	decode(t, do(t, http.MethodPatch, "/api/v1/me/collections/"+collectionID,
		map[string]any{"name": "Weekend Shortlist"}, alice), &updated)
	if updated.Collection.Name != "Weekend Shortlist" {
		t.Errorf("renamed collection = %q", updated.Collection.Name)
	}
	// Removing the item is a 204, and a second removal is a no-op 204 (the row is
	// gone; the collection is still alice's).
	if w := do(t, http.MethodDelete, "/api/v1/me/collections/"+collectionID+"/items/"+itemID, nil, alice); w.Code != http.StatusNoContent {
		t.Errorf("owner remove item: status = %d: %s", w.Code, w.Body.String())
	}
	// Bob deleting alice's collection must 404, not delete it.
	if w := do(t, http.MethodDelete, "/api/v1/me/collections/"+collectionID, nil, bob); w.Code != http.StatusNotFound {
		t.Errorf("a stranger deleted a collection: status = %d, want 404: %s", w.Code, w.Body.String())
	}

	// ---- comments ----
	commentsPath := "/api/v1/businesses/" + b.ID + "/comments"
	if w := do(t, http.MethodPost, commentsPath, map[string]any{"text": "  "}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("empty comment: status = %d, want 400", w.Code)
	}
	var parentRes struct {
		Comment struct {
			ID string `json:"id"`
		} `json:"comment"`
	}
	w = do(t, http.MethodPost, commentsPath, map[string]any{"text": "Is the patio dog friendly?"}, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create comment: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &parentRes)
	parentID := parentRes.Comment.ID
	var childRes struct {
		Comment struct {
			ID string `json:"id"`
		} `json:"comment"`
	}
	w = do(t, http.MethodPost, commentsPath, map[string]any{"text": "Yes, dogs are welcome.", "parent_id": parentID}, bob)
	if w.Code != http.StatusCreated {
		t.Fatalf("reply: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &childRes)

	var tree struct {
		Comments []struct {
			ID       string `json:"id"`
			Text     string `json:"text"`
			Children []struct {
				ID string `json:"id"`
			} `json:"children"`
		} `json:"comments"`
	}
	decode(t, do(t, http.MethodGet, commentsPath, nil, nil), &tree)
	if len(tree.Comments) != 1 || tree.Comments[0].ID != parentID {
		t.Fatalf("comment tree roots = %+v, want only the parent", tree.Comments)
	}
	if len(tree.Comments[0].Children) != 1 || tree.Comments[0].Children[0].ID != childRes.Comment.ID {
		t.Errorf("comment tree children = %+v", tree.Comments[0].Children)
	}
	// A non-author cannot edit or delete someone else's comment.
	if w := do(t, http.MethodPatch, "/api/v1/comments/"+parentID, map[string]any{"text": "edited by a stranger"}, bob); w.Code != http.StatusForbidden {
		t.Errorf("stranger edited a comment: status = %d, want 403", w.Code)
	}
	if w := do(t, http.MethodDelete, "/api/v1/comments/"+parentID, nil, bob); w.Code != http.StatusForbidden {
		t.Errorf("stranger deleted a comment: status = %d, want 403", w.Code)
	}
	decode(t, do(t, http.MethodPatch, "/api/v1/comments/"+parentID, map[string]any{"text": "Is the patio dog friendly? (edited)"}, alice), &parentRes)
	// Comment likes are their own toggle.
	var cliked struct {
		Liked bool `json:"liked"`
	}
	decode(t, do(t, http.MethodPut, "/api/v1/comments/"+parentID+"/like", nil, bob), &cliked)
	if !cliked.Liked {
		t.Error("PUT comment like did not report liked=true")
	}
	decode(t, do(t, http.MethodDelete, "/api/v1/comments/"+parentID+"/like", nil, bob), &cliked)
	if cliked.Liked {
		t.Error("DELETE comment like did not report liked=false")
	}

	// ---- reviews ----
	reviewsPath := "/api/v1/businesses/" + b.ID + "/reviews"
	if w := do(t, http.MethodPost, reviewsPath, map[string]any{"rating": 0, "text": "too short"}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("rating 0: status = %d, want 400", w.Code)
	}
	var reviewRes struct {
		Review struct {
			ID string `json:"id"`
		} `json:"review"`
	}
	w = do(t, http.MethodPost, reviewsPath, map[string]any{
		"rating": 5, "text": "The house blend is excellent and the staff are kind.",
	}, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create review: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &reviewRes)
	if reviewRes.Review.ID == "" {
		t.Fatal("create review returned no id")
	}
	reviewID := reviewRes.Review.ID
	// One business review per user. The duplicate is rejected in the service,
	// and a 201 here would leave two rows and a double-weighted rating.
	if w := do(t, http.MethodPost, reviewsPath, map[string]any{
		"rating": 4, "text": "A second review would inflate the average.",
	}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("duplicate review: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	// A PRODUCT review is a different unique key by schema (product_id IS NOT
	// NULL), so alice may review the same business again against a product. That
	// distinction exists to keep the average meaningful per item.
	var productReview struct {
		Review struct {
			ID string `json:"id"`
		} `json:"review"`
	}
	w = do(t, http.MethodPost, reviewsPath, map[string]any{
		"rating": 4, "text": "The beans are good but the grind is a touch coarse.",
		"product_id": product.ID,
	}, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("product review: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &productReview)
	if productReview.Review.ID == "" || productReview.Review.ID == reviewID {
		t.Errorf("product review id = %q, want a distinct row", productReview.Review.ID)
	}
	var list struct {
		Reviews []struct {
			ID string `json:"id"`
		} `json:"reviews"`
	}
	// The unfiltered list is the BUSINESS review list, so it carries only
	// product_id IS NULL rows; the product review appears on the product filter.
	decode(t, do(t, http.MethodGet, reviewsPath+"?sort=oldest&limit=5", nil, nil), &list)
	if len(list.Reviews) != 1 || list.Reviews[0].ID != reviewID {
		t.Errorf("business reviews = %+v, want only %s", list.Reviews, reviewID)
	}
	decode(t, do(t, http.MethodGet, reviewsPath+"?product_id="+product.ID, nil, nil), &list)
	if len(list.Reviews) != 1 || list.Reviews[0].ID != productReview.Review.ID {
		t.Errorf("product reviews = %+v, want only %s", list.Reviews, productReview.Review.ID)
	}
	// A stranger cannot edit or delete it.
	if w := do(t, http.MethodPatch, "/api/v1/reviews/"+reviewID, map[string]any{
		"rating": 1, "text": "Rewritten by somebody else.",
	}, bob); w.Code != http.StatusForbidden {
		t.Errorf("stranger edited a review: status = %d, want 403", w.Code)
	}
	// Only a manager may reply as the business.
	if w := do(t, http.MethodPost, "/api/v1/reviews/"+reviewID+"/reply",
		map[string]any{"reply": "Thanks for the review!"}, bob); w.Code != http.StatusForbidden {
		t.Errorf("stranger replied as the business: status = %d, want 403", w.Code)
	}
	w = do(t, http.MethodPost, "/api/v1/reviews/"+reviewID+"/reply",
		map[string]any{"reply": "Thanks for the review!"}, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("owner reply: status = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodPatch, "/api/v1/reviews/"+reviewID+"/reply",
		map[string]any{"reply": "Thanks again!"}, owner); w.Code != http.StatusOK {
		t.Errorf("owner edit reply: status = %d: %s", w.Code, w.Body.String())
	}
	// Votes: -1/0/1 only, and not on your own review.
	if w := do(t, http.MethodPut, "/api/v1/reviews/"+reviewID+"/helpful",
		map[string]any{"vote": 2}, bob); w.Code != http.StatusBadRequest {
		t.Errorf("vote 2: status = %d, want 400", w.Code)
	}
	if w := do(t, http.MethodPut, "/api/v1/reviews/"+reviewID+"/helpful",
		map[string]any{"vote": 1}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("voting on your own review: status = %d, want 400", w.Code)
	}
	if w := do(t, http.MethodPut, "/api/v1/reviews/"+reviewID+"/helpful",
		map[string]any{"vote": 1}, bob); w.Code != http.StatusOK {
		t.Errorf("helpful vote: status = %d: %s", w.Code, w.Body.String())
	}
	var mine struct {
		Reviews []struct {
			ID string `json:"id"`
		} `json:"reviews"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/me/reviews", nil, alice), &mine)
	if len(mine.Reviews) != 2 {
		t.Errorf("my reviews = %+v, want both of alice's reviews", mine.Reviews)
	}

	// ---- notifications ----
	var notifs struct {
		Notifications []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"notifications"`
		Unread int `json:"unread"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/notifications?type=helpful_vote", nil, alice), &notifs)
	if len(notifs.Notifications) != 1 || notifs.Unread < 1 {
		t.Fatalf("alice's helpful_vote notifications = %+v, unread %d", notifs.Notifications, notifs.Unread)
	}
	if w := do(t, http.MethodPost, "/api/v1/notifications/read", map[string]any{"all": true}, alice); w.Code != http.StatusNoContent {
		t.Fatalf("mark all read: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, do(t, http.MethodGet, "/api/v1/notifications", nil, alice), &notifs)
	if notifs.Unread != 0 {
		t.Errorf("unread after mark-all = %d", notifs.Unread)
	}

	// ---- leaderboards ----
	// No snapshots yet: an empty array, not null and not a 500.
	for _, path := range []string{"/api/v1/leaderboards", "/api/v1/trending", "/api/v1/rising"} {
		w := do(t, http.MethodGet, path, nil, nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status = %d: %s", path, w.Code, w.Body.String())
			continue
		}
		var lb struct {
			Entries []json.RawMessage `json:"entries"`
		}
		decode(t, w, &lb)
		if lb.Entries == nil {
			t.Errorf("%s encodes an empty leaderboard as null", path)
		}
	}
	// An out-of-range window falls back to 24h rather than reaching SQL.
	var lb struct {
		Period string `json:"period"`
		Scope  string `json:"scope"`
	}
	decode(t, do(t, http.MethodGet, "/api/v1/leaderboards?window=99y&scope=", nil, nil), &lb)
	if lb.Period != "24h" || lb.Scope != "global" {
		t.Errorf("leaderboard defaults = %+v, want 24h/global", lb)
	}

	// ---- reports ----
	var reported struct {
		Reported bool `json:"reported"`
	}
	if w := do(t, http.MethodPost, "/api/v1/reports", map[string]any{
		"target_type": "review", "target_id": reviewID,
	}, bob); w.Code != http.StatusBadRequest {
		t.Errorf("report without a reason: status = %d, want 400", w.Code)
	}
	// target_type is a CHECK constraint. An unknown value must be a field error,
	// not a 500 from Postgres.
	if w := do(t, http.MethodPost, "/api/v1/reports", map[string]any{
		"target_type": "banana", "target_id": reviewID, "reason": "not a real type",
	}, bob); w.Code != http.StatusBadRequest {
		t.Errorf("unknown report target_type: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	w = do(t, http.MethodPost, "/api/v1/reports", map[string]any{
		"target_type": "review", "target_id": reviewID, "reason": "abusive language",
	}, bob)
	if w.Code != http.StatusCreated {
		t.Fatalf("report: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &reported)
	if !reported.Reported {
		t.Error("report did not report reported=true")
	}
	// The same user reporting the same target twice is one open report, and the
	// second request still succeeds: the ON CONFLICT clause makes it idempotent.
	if w := do(t, http.MethodPost, "/api/v1/reports", map[string]any{
		"target_type": "review", "target_id": reviewID, "reason": "abusive language, again",
	}, bob); w.Code != http.StatusCreated {
		t.Errorf("duplicate report: status = %d: %s", w.Code, w.Body.String())
	}
	var reportCount int
	if err := h.QueryRow(t, `SELECT count(*) FROM reports WHERE reporter_id=$1 AND target_type='review' AND target_id=$2`,
		bob.ID, reviewID).Scan(&reportCount); err != nil {
		t.Fatal(err)
	}
	if reportCount != 1 {
		t.Errorf("open reports = %d, want 1", reportCount)
	}
}
