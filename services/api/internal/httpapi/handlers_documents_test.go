//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Verification documents, the profile update, and API-key revocation. Small
// handlers, but the first owns the file a moderator reviews before a business is
// trusted, and the last is how a leaked key gets closed.

func draftBusiness(t *testing.T, h *testutil.H, owner *domain.User, name string) *domain.Business {
	t.Helper()
	cat := testutil.Category(t, h, "Cafe")
	return testutil.Business(t, h, testutil.BusinessOpts{
		Name: name, Category: cat, Owner: owner, Status: domain.BusinessDraft,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
}

func TestBusinessDocuments(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	stranger := testutil.User(t, h)
	biz := draftBusiness(t, h, owner, "Doc Cafe")
	base := "/api/v1/businesses/" + biz.ID + "/documents"

	// Empty to start.
	w := dash(t, td, hdl, http.MethodGet, base, nil, owner)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"documents"`) {
		t.Fatalf("list: status = %d: %s", w.Code, w.Body.String())
	}

	// A stranger can neither list nor add.
	if w := dash(t, td, hdl, http.MethodGet, base, nil, stranger); w.Code == http.StatusOK {
		t.Error("a stranger listed another owner's documents")
	}
	// Attached media must be the caller's own verification upload, so the add
	// below uses a real one rather than a bare UUID.
	code, mediaID := uploadMedia(t, td, hdl, owner, string(domain.MediaDocVerif), "license.pdf", pdfBytes)
	if code != http.StatusCreated || mediaID == "" {
		t.Fatalf("upload: status = %d, id = %q", code, mediaID)
	}
	body := map[string]any{"kind": "license", "media_id": mediaID}
	if w := dash(t, td, hdl, http.MethodPost, base, body, stranger); w.Code == http.StatusCreated {
		t.Error("a stranger added a document to another owner's business")
	}

	// The kind is an allowlist.
	if w := dash(t, td, hdl, http.MethodPost, base, map[string]any{"kind": "selfie", "media_id": util.NewUUID()}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("invalid kind: status = %d, want 400", w.Code)
	}

	w = dash(t, td, hdl, http.MethodPost, base, body, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("add: status = %d: %s", w.Code, w.Body.String())
	}
	var added struct {
		Document struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Status string `json:"status"`
		} `json:"document"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	if added.Document.ID == "" || added.Document.Kind != "license" || added.Document.Status != "pending" {
		t.Errorf("document = %+v, want a pending licence", added.Document)
	}

	w = dash(t, td, hdl, http.MethodGet, base, nil, owner)
	if strings.Count(w.Body.String(), `"kind"`) != 1 {
		t.Errorf("list after add: %s", w.Body.String())
	}

	if w := dash(t, td, hdl, http.MethodDelete, base+"/"+added.Document.ID, nil, owner); w.Code != http.StatusNoContent {
		t.Errorf("remove: status = %d, want 204", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, base, nil, owner); strings.Contains(w.Body.String(), `"license"`) {
		t.Error("the document survived removal")
	}
}

// Documents are only editable while the listing is a draft or rejected; after
// submission the evidence a moderator is reviewing must not change under them.
func TestBusinessDocumentsFrozenAfterSubmission(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	owner := testutil.User(t, h)
	biz := draftBusiness(t, h, owner, "Frozen Cafe")
	h.Exec(t, `UPDATE businesses SET status='pending_review' WHERE id=$1`, biz.ID)

	w := dash(t, td, s.Handler(), http.MethodPost, "/api/v1/businesses/"+biz.ID+"/documents",
		map[string]any{"kind": "license", "media_id": util.NewUUID()}, owner)
	if w.Code != http.StatusBadRequest {
		t.Errorf("add to a submitted listing: status = %d, want 400", w.Code)
	}
}

func TestUpdateMe(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := testutil.User(t, h)

	w := dash(t, td, hdl, http.MethodPatch, "/api/v1/me", map[string]any{"name": "Ada Lovelace", "bio": "  Coffee and code.  "}, user)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", w.Code, w.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated["name"] != "Ada Lovelace" {
		t.Errorf("name = %v", updated["name"])
	}
	// The bio is trimmed, and the projection still carries no password material.
	if bio, _ := updated["bio"].(string); bio != "Coffee and code." {
		t.Errorf("bio = %q, want it trimmed", bio)
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Errorf("the profile response mentions a password: %s", w.Body.String())
	}

	// A bio over the limit, an empty patch, and a script-bearing avatar URL.
	if w := dash(t, td, hdl, http.MethodPatch, "/api/v1/me", map[string]any{"bio": strings.Repeat("b", 301)}, user); w.Code != http.StatusBadRequest {
		t.Errorf("301-rune bio: status = %d, want 400", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPatch, "/api/v1/me", map[string]any{}, user); w.Code != http.StatusBadRequest {
		t.Errorf("empty patch: status = %d, want 400", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPatch, "/api/v1/me", map[string]any{"avatar_url": "javascript:alert(1)"}, user); w.Code != http.StatusBadRequest {
		t.Errorf("javascript avatar: status = %d, want 400", w.Code)
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := testutil.User(t, h)

	w := dash(t, td, hdl, http.MethodPost, "/api/v1/me/api-keys", map[string]any{"name": "CI"}, user)
	if w.Code != http.StatusCreated {
		t.Fatalf("create key: status = %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
		RawKey string `json:"raw_key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Key.ID == "" || created.RawKey == "" {
		t.Fatalf("create returned no id/raw key: %s", w.Body.String())
	}

	w = dash(t, td, hdl, http.MethodGet, "/api/v1/me/api-keys", nil, user)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), created.Key.ID) {
		t.Fatalf("list: status = %d: %s", w.Code, w.Body.String())
	}
	// The raw value is shown once, at creation; the list must not repeat it.
	if strings.Contains(w.Body.String(), created.RawKey) {
		t.Error("the list exposes the raw key again")
	}

	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/me/api-keys/"+created.Key.ID, nil, user); w.Code != http.StatusNoContent {
		t.Errorf("revoke: status = %d, want 204", w.Code)
	}
	// Revoking again finds nothing to revoke, and says so.
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/me/api-keys/"+created.Key.ID, nil, user); w.Code != http.StatusNotFound {
		t.Errorf("a second revoke: status = %d, want 404", w.Code)
	}
	// Another user cannot revoke it either.
	other := testutil.User(t, h)
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/me/api-keys", map[string]any{"name": "Mine"}, other)
	var second struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &second)
	// Another user's attempt is refused AND has no effect: the row must still be
	// live for its owner, which is the property that matters more than the code.
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/me/api-keys/"+second.Key.ID, nil, user); w.Code != http.StatusNotFound {
		t.Errorf("a stranger's revoke: status = %d, want 404", w.Code)
	}
	var revoked *string
	if err := h.QueryRow(t, `SELECT revoked_at::text FROM api_keys WHERE id=$1`, second.Key.ID).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked != nil {
		t.Error("one user's request revoked another user's key")
	}
}
