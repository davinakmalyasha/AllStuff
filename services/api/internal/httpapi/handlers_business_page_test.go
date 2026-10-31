//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// The public business page had no HTTP test. It is the page every listing is
// judged by, and it decides three things per caller: whether to expose the draft
// preview, whether the caller counts as a manager for the owner affordances, and
// whether the view is recorded as engagement. The first two are authorization by
// another name, so the co-owner case is not academic.

func inviteAccepted(t *testing.T, h *testutil.H, businessID, inviterID, inviteeID, role string) {
	t.Helper()
	h.Exec(t, `INSERT INTO business_invites (id, business_id, email, role, token, invited_by, accepted_at, accepted_user_id)
		VALUES ($1,$2,$3,$4,$5,$6,now(),$7)`,
		util.NewUUID(), businessID, inviteeID+"@example.test", role, util.NewUUID(), inviterID, inviteeID)
}

type businessPagePayload struct {
	Business struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	} `json:"business"`
	Similar []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"similar"`
	Products []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"products"`
	IsOwner     bool     `json:"is_owner"`
	Highlighted []string `json:"highlighted_product_ids"`
	Trend       struct {
		IsBooming bool `json:"is_booming"`
		IsRising  bool `json:"is_rising"`
	} `json:"trend"`
	Preview map[string]any `json:"preview"`
}

func TestBusinessPage(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	coOwner := testutil.User(t, h)
	stranger := testutil.User(t, h)
	cat := testutil.Category(t, h, "Storefront Category")
	otherCat := testutil.Category(t, h, "Other Storefront Category")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Storefront Coffee", Slug: "storefront-coffee", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	inviteAccepted(t, h, b.ID, owner.ID, coOwner.ID, "co_owner")
	// A same-category listing is "similar"; a different-category one is not.
	testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Similar Cafe", Slug: "similar-cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Other Shop", Slug: "other-shop", Category: otherCat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	draft := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Draft Store", Slug: "draft-store", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessDraft,
	})
	suspended := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Suspended Store", Slug: "suspended-store", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessSuspended,
	})
	paused := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Paused Store", Slug: "paused-store", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessPaused,
	})

	visible := testutil.Product(t, h, b.ID, "Visible Espresso")
	hidden := testutil.Product(t, h, b.ID, "Hidden Cake")
	h.Exec(t, `UPDATE products SET is_published=false WHERE id=$1`, hidden.ID)
	h.Exec(t, `UPDATE businesses
		SET theme='{"colors":{"accent":"#123456"}}'::jsonb,
		    layout='{"sections":[{"key":"hero","enabled":true}]}'::jsonb,
		    highlighted_product_ids=ARRAY[$2]::uuid[]
		WHERE id=$1`, b.ID, visible.ID)
	h.Exec(t, `INSERT INTO trend_snapshots (id, period, business_id, score, velocity, is_booming, is_rising)
		VALUES ($1,'24h',$2,10,1,true,true)`, util.NewUUID(), b.ID)

	fetch := func(path string, u *domain.User) (*httptest.ResponseRecorder, businessPagePayload) {
		t.Helper()
		var req *http.Request
		if u == nil {
			req = authReq(t, http.MethodGet, path, nil)
		} else {
			req = td.authed(t, http.MethodGet, path, nil, u)
		}
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, req)
		var page businessPagePayload
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatalf("%s is not JSON: %v", path, err)
			}
		}
		return w, page
	}

	// ---- anonymous ----
	w, page := fetch("/api/v1/b/storefront-coffee", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("anonymous: status = %d: %s", w.Code, w.Body.String())
	}
	if page.IsOwner || page.Preview != nil {
		t.Errorf("anonymous caller is an owner: is_owner=%v preview=%v", page.IsOwner, page.Preview)
	}
	if len(page.Products) != 1 || page.Products[0].Name != "Visible Espresso" {
		t.Errorf("products = %+v, want only the published one", page.Products)
	}
	names := map[string]bool{}
	for _, sb := range page.Similar {
		names[sb.Name] = true
		if sb.ID == b.ID {
			t.Error("the business is similar to itself")
		}
	}
	if !names["Similar Cafe"] {
		t.Errorf("similar is missing the same-category listing: %+v", page.Similar)
	}
	if names["Other Shop"] {
		t.Errorf("similar contains a listing from another category: %+v", page.Similar)
	}
	if len(page.Highlighted) != 1 || page.Highlighted[0] != visible.ID {
		t.Errorf("highlighted_product_ids = %v, want [%s]", page.Highlighted, visible.ID)
	}
	if !page.Trend.IsBooming || !page.Trend.IsRising {
		t.Errorf("trend flags were not read from the snapshot: %+v", page.Trend)
	}

	// ---- views ----
	// A signed-in non-manager's view is engagement, deduped per user per day.
	if _, page = fetch("/api/v1/b/storefront-coffee", stranger); page.IsOwner {
		t.Error("a stranger is reported as the owner")
	}
	countViews := func(u *domain.User) int {
		t.Helper()
		var n int
		if err := h.QueryRow(t, `SELECT count(*) FROM engagement_events
			WHERE user_id=$1 AND target_id=$2 AND target_type='business' AND signal='view'`,
			u.ID, b.ID).Scan(&n); err != nil {
			t.Fatalf("count views: %v", err)
		}
		return n
	}
	if n := countViews(stranger); n != 1 {
		t.Errorf("stranger views = %d, want 1", n)
	}
	fetch("/api/v1/b/storefront-coffee", stranger)
	if n := countViews(stranger); n != 1 {
		t.Errorf("a second view the same day was counted: %d", n)
	}

	// ---- co-owner ----
	// ACCEPTED CO-OWNERS ARE MANAGERS (PRD 5.9.3). They edit the storefront, so
	// they must be able to preview their draft, and their own visits are not
	// customer engagement.
	_, page = fetch("/api/v1/b/storefront-coffee", coOwner)
	if !page.IsOwner {
		t.Error("an accepted co-owner is not reported as an owner")
	}
	_, page = fetch("/api/v1/b/storefront-coffee?draft=1", coOwner)
	if page.Preview == nil {
		t.Fatal("a co-owner cannot preview the draft storefront")
	}
	if page.Preview["theme"] == nil {
		t.Errorf("preview is missing the draft theme: %+v", page.Preview)
	}
	if n := countViews(coOwner); n != 0 {
		t.Errorf("a co-owner's own view was recorded as customer engagement: %d", n)
	}

	// ---- owner ----
	_, page = fetch("/api/v1/b/storefront-coffee?draft=1", owner)
	if !page.IsOwner || page.Preview == nil {
		t.Error("the owner cannot preview their own draft storefront")
	}
	if n := countViews(owner); n != 0 {
		t.Errorf("the owner's own view was recorded as customer engagement: %d", n)
	}

	// ?draft=1 is not a way around the owner check.
	if _, page := fetch("/api/v1/b/storefront-coffee?draft=1", stranger); page.Preview != nil || page.IsOwner {
		t.Error("a stranger got the draft preview")
	}

	// ---- status whitelist ----
	for name, slug := range map[string]string{
		"draft": draft.Slug, "suspended": suspended.Slug, "unknown": "no-such-shop",
	} {
		if w, _ := fetch("/api/v1/b/"+slug, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s business page: status = %d, want 404", name, w.Code)
		}
	}
	if w, _ := fetch("/api/v1/b/"+paused.Slug, nil); w.Code != http.StatusOK {
		t.Errorf("paused business page: status = %d, want 200", w.Code)
	}
}
