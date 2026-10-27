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

// Unpublishing is the owner's "take my storefront down" action. It had never
// been executed. The service method has no gate of its own beyond ownership,
// so what matters here is that the ownership check accepts both the owner and
// an accepted co-owner, refuses a read-only viewer, and that the snapshot and
// timestamp are actually cleared (a request that returns 200 while leaving the
// published storefront live is the dangerous failure mode).

func TestBusinessUnpublish(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	coOwner := testutil.User(t, h)
	viewer := testutil.User(t, h)
	stranger := testutil.User(t, h)
	cat := testutil.Category(t, h, "Unpublish Category")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Unpublish Coffee", Slug: "unpublish-coffee", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	inviteAccepted(t, h, b.ID, owner.ID, coOwner.ID, "co_owner")
	inviteAccepted(t, h, b.ID, owner.ID, viewer.ID, "viewer")
	path := "/api/v1/businesses/" + b.ID + "/unpublish"

	// A published storefront is a non-null snapshot plus the time it went live.
	// Seeding the state directly keeps this test about the transition, not
	// about Publish's own validation (covered elsewhere).
	publish := func(t *testing.T) {
		t.Helper()
		h.Exec(t, `UPDATE businesses SET published_snapshot = '{"theme":{"accent":"#123456"}}'::jsonb,
			last_published_at = now() WHERE id = $1`, b.ID)
	}
	state := func(t *testing.T) (snapshot, published bool) {
		t.Helper()
		if err := h.QueryRow(t, `SELECT published_snapshot IS NOT NULL,
			last_published_at IS NOT NULL FROM businesses WHERE id = $1`, b.ID).
			Scan(&snapshot, &published); err != nil {
			t.Fatalf("read published state: %v", err)
		}
		return snapshot, published
	}

	publish(t)
	anonymous := httptest.NewRecorder()
	hdl.ServeHTTP(anonymous, authReq(t, http.MethodPost, path, nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Errorf("anonymous unpublish: status = %d, want 401: %s", anonymous.Code, anonymous.Body.String())
	}
	if snapshot, published := state(t); !snapshot || !published {
		t.Fatal("an anonymous request changed the published state")
	}

	// A refused caller must not take the storefront down on the way out.
	for name, u := range map[string]*domain.User{"stranger": stranger, "viewer": viewer} {
		if w := dash(t, td, hdl, http.MethodPost, path, nil, u); w.Code != http.StatusForbidden {
			t.Errorf("%s unpublish: status = %d, want 403: %s", name, w.Code, w.Body.String())
		}
		if snapshot, published := state(t); !snapshot || !published {
			t.Fatalf("%s unpublish cleared the snapshot", name)
		}
	}

	w := dash(t, td, hdl, http.MethodPost, path, nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("owner unpublish: status = %d: %s", w.Code, w.Body.String())
	}
	var env businessEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unpublish JSON: %v", err)
	}
	if env.Business.ID != b.ID {
		t.Errorf("unpublish returned business %q, want %q", env.Business.ID, b.ID)
	}
	if snapshot, published := state(t); snapshot || published {
		t.Errorf("after unpublish: snapshot = %v, last_published_at = %v", snapshot, published)
	}

	// A co-owner has the same management right, and the action is repeatable:
	// a second unpublish of an already-unpublished listing stays 200 rather
	// than turning into a conflict.
	publish(t)
	if w := dash(t, td, hdl, http.MethodPost, path, nil, coOwner); w.Code != http.StatusOK {
		t.Fatalf("co-owner unpublish: status = %d: %s", w.Code, w.Body.String())
	}
	if snapshot, published := state(t); snapshot || published {
		t.Errorf("co-owner unpublish left snapshot = %v, published = %v", snapshot, published)
	}
	if w := dash(t, td, hdl, http.MethodPost, path, nil, owner); w.Code != http.StatusOK {
		t.Errorf("second unpublish: status = %d, want 200: %s", w.Code, w.Body.String())
	}

	// A malformed path id is a client mistake, not a 500 from the uuid column.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/not-a-uuid/unpublish", nil, owner); w.Code != http.StatusNotFound {
		t.Errorf("malformed id: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+util.NewUUID()+"/unpublish", nil, owner); w.Code != http.StatusNotFound {
		t.Errorf("unknown id: status = %d, want 404: %s", w.Code, w.Body.String())
	}
}
