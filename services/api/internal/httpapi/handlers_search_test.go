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
	"bizverse/api/internal/util"
)

type searchPage struct {
	Businesses []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"businesses"`
	Count   *int `json:"count"`
	HasMore bool `json:"has_more"`
}

func decodeSearch(t *testing.T, w *httptest.ResponseRecorder) searchPage {
	t.Helper()
	var page searchPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("search is not JSON: %v: %s", err, w.Body.String())
	}
	return page
}

func namesOf(page searchPage) map[string]bool {
	out := map[string]bool{}
	for _, b := range page.Businesses {
		out[b.Name] = true
	}
	return out
}

func TestSearchRoutes(t *testing.T) {
	s, h := newDBTestServer(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	catA := testutil.Category(t, h, "Search Roasteries")
	catB := testutil.Category(t, h, "Search Bakeries")
	mk := func(name, slug string, cat *domain.Category, city string, price int, status domain.BusinessStatus) *domain.Business {
		b := testutil.Business(t, h, testutil.BusinessOpts{
			Name: name, Slug: slug, Category: cat, Owner: owner,
			City: city, Country: "ID", Status: status,
		})
		h.Exec(t, `UPDATE businesses SET price_level=$1 WHERE id=$2`, price, b.ID)
		return b
	}
	mk("Alpha Roasters", "alpha-roasters", catA, "Jakarta", 2, domain.BusinessVerified)
	mk("Beta Roasters", "beta-roasters", catA, "Bandung", 3, domain.BusinessVerified)
	mk("Gamma Draft", "gamma-draft", catA, "Jakarta", 3, domain.BusinessDraft)
	mk("Paused Roasters", "paused-roasters", catA, "Bandung", 2, domain.BusinessPaused)
	d := mk("Delta Cafe", "delta-cafe", catB, "Jakarta", 1, domain.BusinessVerified)
	testutil.Product(t, h, d.ID, "Cold Brew")

	get := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, authReq(t, http.MethodGet, "/api/v1/search"+query, nil))
		return w
	}

	// ---- no filters ----
	w := get("")
	if w.Code != http.StatusOK {
		t.Fatalf("search: status = %d: %s", w.Code, w.Body.String())
	}
	page := decodeSearch(t, w)
	names := namesOf(page)
	for _, want := range []string{"Alpha Roasters", "Beta Roasters", "Delta Cafe"} {
		if !names[want] {
			t.Errorf("search is missing %q: %s", want, w.Body.String())
		}
	}
	if names["Gamma Draft"] || names["Paused Roasters"] {
		t.Errorf("search returned a non-verified listing: %s", w.Body.String())
	}
	// The first page asks for the exact count by default.
	if page.Count == nil || *page.Count != 3 {
		t.Errorf("count = %v, want 3", page.Count)
	}
	if page.HasMore {
		t.Error("has_more is true for the only page")
	}

	// ---- filters ----
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"query", "?q=Alpha", []string{"Alpha Roasters"}},
		// Products are searchable: the product match has to reach the page,
		// otherwise a listing is findable by name only.
		{"product name", "?q=Cold+Brew", []string{"Delta Cafe"}},
		{"city", "?city=Jakarta", []string{"Alpha Roasters", "Delta Cafe"}},
		{"one category", "?category=" + catA.ID, []string{"Alpha Roasters", "Beta Roasters"}},
		{"two categories", "?category=" + catA.ID + "&category=" + catB.ID,
			[]string{"Alpha Roasters", "Beta Roasters", "Delta Cafe"}},
		{"one price level", "?price_level=3", []string{"Beta Roasters"}},
		{"two price levels", "?price_level=1&price_level=3", []string{"Beta Roasters", "Delta Cafe"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := get(c.query)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			page := decodeSearch(t, w)
			got := namesOf(page)
			if len(got) != len(c.want) {
				t.Fatalf("results = %v, want %v", got, c.want)
			}
			for _, want := range c.want {
				if !got[want] {
					t.Errorf("results = %v, missing %q", got, want)
				}
			}
		})
	}
	// Out-of-range price levels are ignored, not passed to SQL.
	if page := decodeSearch(t, get("?price_level=9")); len(page.Businesses) != 3 {
		t.Errorf("out-of-range price level filtered the results to %d", len(page.Businesses))
	}

	// ---- paging ----
	page = decodeSearch(t, get("?limit=1"))
	if len(page.Businesses) != 1 || page.Count == nil || *page.Count != 3 || !page.HasMore {
		t.Errorf("first page of 1: businesses=%d count=%v has_more=%v", len(page.Businesses), page.Count, page.HasMore)
	}
	// A last page counts nothing extra: offset + rows IS the exact total, so it
	// is reported even when no count was requested.
	page = decodeSearch(t, get("?limit=2&offset=2"))
	if len(page.Businesses) != 1 || page.Count == nil || *page.Count != 3 || page.HasMore {
		t.Errorf("last page: businesses=%d count=%v has_more=%v, want 1, 3, false",
			len(page.Businesses), page.Count, page.HasMore)
	}
	page = decodeSearch(t, get("?limit=1&offset=1&with_total=1"))
	if page.Count == nil || *page.Count != 3 || !page.HasMore {
		t.Errorf("forced total: count=%v has_more=%v", page.Count, page.HasMore)
	}
	// When more pages exist and no total was asked for, the count is null rather
	// than a guess. (A LAST page returns offset+rows because that is exact.)
	page = decodeSearch(t, get("?limit=1&with_total=0"))
	if page.Count != nil {
		t.Errorf("with_total=0 still ran the count query: %v", *page.Count)
	}
	// Past the end, an empty page must not claim the whole result set is empty.
	// A pager that just lost rows between requests would otherwise render
	// "101-100 of 0".
	page = decodeSearch(t, get("?offset=100"))
	if len(page.Businesses) != 0 || page.Count != nil {
		t.Errorf("empty page past the end: businesses=%d count=%v, want 0 and nil", len(page.Businesses), page.Count)
	}
	page = decodeSearch(t, get("?offset=100&with_total=1"))
	if page.Count == nil || *page.Count != 3 {
		t.Errorf("empty page with a forced total: count=%v, want 3", page.Count)
	}
	// No results is an empty array, not null.
	if w := get("?q=zzzznomatch"); !strings.Contains(w.Body.String(), `"businesses":[]`) {
		t.Errorf("empty result encodes as %s, want an array", w.Body.String())
	}

	// ---- malformed parameters are defaults, not 500s ----
	for _, query := range []string{
		"?limit=abc&offset=-5", "?min_rating=abc", "?bbox=1,2", "?sort=bogus",
		"?lat=notanumber&lng=106.8", "?radius_km=-1",
	} {
		if w := get(query); w.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200: %s", query, w.Code, w.Body.String())
		}
	}

	// ---- a category id reaches a uuid[] column ----
	// The client copies `category` out of the URL, so this is attacker-controlled.
	// An invalid literal must be rejected before it becomes a Postgres 500, and
	// an EMPTY one (a URL with `?category=`) means "no filter", which is what the
	// browser's URLSearchParams sends for an unset value.
	if w := get("?category=not-a-uuid"); w.Code != http.StatusBadRequest {
		t.Errorf("malformed category: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := get("?category="); w.Code != http.StatusOK || len(decodeSearch(t, w).Businesses) != 3 {
		t.Errorf("empty category: status = %d, want an unfiltered 200: %s", w.Code, w.Body.String())
	}
}

func TestCategoryPageRoutes(t *testing.T) {
	s, h := newDBTestServer(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	parent := testutil.Category(t, h, "Dining Parent")
	child := testutil.Category(t, h, "Coffee Child")
	h.Exec(t, `UPDATE categories SET parent_id=$1 WHERE id=$2`, parent.ID, child.ID)
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Child Cafe", Slug: "child-cafe", Category: child, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	// Counts are verified-only; a draft must not inflate the landing page.
	testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Draft Cafe", Slug: "draft-cafe", Category: child, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessDraft,
	})

	get := func(slug string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, authReq(t, http.MethodGet, "/api/v1/categories/"+slug, nil))
		return w
	}

	w := get(child.Slug)
	if w.Code != http.StatusOK {
		t.Fatalf("category page: status = %d: %s", w.Code, w.Body.String())
	}
	var payload struct {
		Category struct {
			ID    string `json:"id"`
			Slug  string `json:"slug"`
			Count int    `json:"count"`
		} `json:"category"`
		Breadcrumbs []struct {
			Slug string `json:"slug"`
		} `json:"breadcrumbs"`
		Leaderboard []struct {
			Slug string `json:"slug"`
		} `json:"leaderboard"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("category page is not JSON: %v", err)
	}
	if payload.Category.ID != child.ID || payload.Category.Count != 1 {
		t.Errorf("category = %+v, want id %s with count 1", payload.Category, child.ID)
	}
	// The breadcrumb must contain the ancestor, not just the leaf: it is how the
	// page links back up the tree.
	if len(payload.Breadcrumbs) != 2 || payload.Breadcrumbs[0].Slug != parent.Slug || payload.Breadcrumbs[1].Slug != child.Slug {
		t.Errorf("breadcrumbs = %+v, want [%s %s]", payload.Breadcrumbs, parent.Slug, child.Slug)
	}
	// No snapshots yet is an empty leaderboard, not a 500 and not a null.
	if w.Code == http.StatusOK && !strings.Contains(w.Body.String(), `"leaderboard":[]`) {
		t.Errorf("a fresh category does not report an empty leaderboard: %s", w.Body.String())
	}

	// The parent rolls up its descendants' counts.
	w = get(parent.Slug)
	if w.Code != http.StatusOK {
		t.Fatalf("parent category: status = %d", w.Code)
	}
	var parentPayload struct {
		Category struct {
			Count int `json:"count"`
		} `json:"category"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &parentPayload); err != nil {
		t.Fatal(err)
	}
	if parentPayload.Category.Count != 1 {
		t.Errorf("parent count = %d, want the child's 1", parentPayload.Category.Count)
	}

	// Once a snapshot exists, the leaderboard carries the listing.
	h.Exec(t, `INSERT INTO trend_snapshots (id, period, business_id, score, velocity, rank_category, taken_at)
		VALUES ($1,'24h',$2,42,1,1,now())`, util.NewUUID(), b.ID)
	w = get(child.Slug)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "child-cafe") {
		t.Errorf("leaderboard is missing the snapped business: status = %d, body = %s", w.Code, w.Body.String())
	}

	if w := get("no-such-category"); w.Code != http.StatusNotFound {
		t.Errorf("unknown category: status = %d, want 404", w.Code)
	}
}
