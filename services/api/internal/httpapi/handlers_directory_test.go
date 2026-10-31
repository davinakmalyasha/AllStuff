//go:build !race

package httpapi

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// The public directory routes - sitemap, robots, suggest, compare, featured,
// rates and the city pages - were never executed by a test. They are all
// unauthenticated, so the questions are what they expose and to whom: a draft
// listing must not leak into any of them, and a malformed id must be a 400
// rather than a 500 from the uuid-typed columns underneath.

func TestPublicDirectoryRoutes(t *testing.T) {
	// No pool needed: none of these paths opens its own transaction.
	s, h := newDBTestServer(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Directory Coffee")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Sitemap Coffee", Slug: "sitemap-coffee", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	draft := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Draft Coffee", Slug: "hidden-draft", Category: cat, Owner: owner,
		City: "Bandung", Country: "ID", Status: domain.BusinessDraft,
	})
	// Both are flagged featured: only the verified one may be recommended.
	h.Exec(t, `UPDATE businesses SET is_featured=true WHERE id = ANY($1)`, []string{b.ID, draft.ID})
	testutil.Product(t, h, b.ID, "Espresso")
	h.Exec(t, `INSERT INTO currency_rates (id, code, rate_usd, fetched_at)
		VALUES (gen_random_uuid(), 'EUR', 0.91, now())`)

	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, authReq(t, http.MethodGet, path, nil))
		return w
	}

	// ---- sitemap ----
	w := get("/api/v1/sitemap.xml")
	if w.Code != http.StatusOK {
		t.Fatalf("sitemap: status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("sitemap Content-Type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=3600") {
		t.Errorf("sitemap Cache-Control = %q, want an hourly cache", cc)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("sitemap does not start with an XML declaration: %.40q", body)
	}
	// Well-formed, not merely string-shaped. A tag left unclosed by a
	// future branch would still pass a Contains check and break every crawler.
	var doc struct {
		XMLName xml.Name `xml:"urlset"`
		URLs    []struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("sitemap is not well-formed XML: %v", err)
	}
	if len(doc.URLs) < 4 {
		t.Fatalf("sitemap has %d urls, want at least the root, category, city and business", len(doc.URLs))
	}
	locs := map[string]string{}
	for _, u := range doc.URLs {
		if !strings.HasPrefix(u.Loc, "https://allstuff.test/") {
			t.Errorf("sitemap loc %q is not on the public origin", u.Loc)
		}
		locs[u.Loc] = u.LastMod
	}
	for _, want := range []string{
		"https://allstuff.test/",
		"https://allstuff.test/c/" + cat.Slug,
		"https://allstuff.test/city/jakarta",
		"https://allstuff.test/b/sitemap-coffee",
	} {
		if _, found := locs[want]; !found {
			t.Errorf("sitemap is missing %s", want)
		}
	}
	// Drafts are not published pages. A sitemap entry is an invitation to
	// index content the owner has not finished.
	if _, found := locs["https://allstuff.test/b/hidden-draft"]; found {
		t.Error("sitemap lists a draft business")
	}
	if lm := locs["https://allstuff.test/b/sitemap-coffee"]; len(lm) != 10 || lm[4] != '-' || lm[7] != '-' {
		t.Errorf("business lastmod = %q, want a YYYY-MM-DD date", lm)
	}

	// ---- robots.txt ----
	w = get("/robots.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("robots: status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("robots Content-Type = %q", ct)
	}
	robots := w.Body.String()
	if !strings.Contains(robots, "User-agent: *") || !strings.Contains(robots, "Allow: /") {
		t.Errorf("robots does not allow crawling: %q", robots)
	}
	if !strings.Contains(robots, "Sitemap: https://allstuff.test/api/v1/sitemap.xml") {
		t.Errorf("robots points at the wrong sitemap: %q", robots)
	}

	// ---- suggest ----
	// An empty query is answered without touching the database, and must be a
	// well-formed empty result rather than null.
	w = get("/api/v1/search/suggest")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"businesses":[]`) || !strings.Contains(w.Body.String(), `"categories":[]`) {
		t.Errorf("empty suggest: status = %d, body = %s", w.Code, w.Body.String())
	}
	w = get("/api/v1/search/suggest?q=coffee")
	if w.Code != http.StatusOK {
		t.Fatalf("suggest: status = %d: %s", w.Code, w.Body.String())
	}
	suggest := w.Body.String()
	if !strings.Contains(suggest, "Sitemap Coffee") || !strings.Contains(suggest, `"type":"business"`) {
		t.Errorf("suggest does not find the verified business: %s", suggest)
	}
	if !strings.Contains(suggest, `"type":"category"`) || !strings.Contains(suggest, cat.Slug) {
		t.Errorf("suggest does not find the category: %s", suggest)
	}
	// LIKE metacharacters are data, not wildcards. Unescaped, "%" would match
	// every verified business in the directory.
	if w := get("/api/v1/search/suggest?q=%25"); strings.Contains(w.Body.String(), "Sitemap Coffee") {
		t.Errorf("a literal %% in the query matched every business: %s", w.Body.String())
	}

	// ---- compare ----
	cases := []struct {
		query string
		want  int
	}{
		{"", http.StatusBadRequest},
		{"?b=" + b.ID, http.StatusBadRequest},
		{"?b=" + b.ID + "," + b.ID + "," + b.ID + "," + b.ID + "," + b.ID, http.StatusBadRequest},
		{"?b=not-a-uuid," + b.ID, http.StatusBadRequest},
		{"?b=" + util.NewUUID() + "," + b.ID, http.StatusNotFound},
		{"?b=" + draft.ID + "," + b.ID, http.StatusNotFound},
	}
	for _, c := range cases {
		if w := get("/api/v1/compare" + c.query); w.Code != c.want {
			t.Errorf("compare %q: status = %d, want %d: %s", c.query, w.Code, c.want, w.Body.String())
		}
	}
	// Two verified listings are the legitimate case, with their products.
	other := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Compare Bakery", Slug: "compare-bakery", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	w = get("/api/v1/compare?b=" + b.ID + "," + other.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("compare: status = %d: %s", w.Code, w.Body.String())
	}
	cmp := w.Body.String()
	for _, want := range []string{"Sitemap Coffee", "Compare Bakery", `"top_products"`, "Espresso"} {
		if !strings.Contains(cmp, want) {
			t.Errorf("compare is missing %q: %s", want, cmp)
		}
	}

	// ---- featured ----
	w = get("/api/v1/featured")
	if w.Code != http.StatusOK {
		t.Fatalf("featured: status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "sitemap-coffee") {
		t.Errorf("featured is missing the featured verified business: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "hidden-draft") {
		t.Errorf("featured recommends a draft business: %s", w.Body.String())
	}

	// ---- rates ----
	w = get("/api/v1/rates")
	if w.Code != http.StatusOK {
		t.Fatalf("rates: status = %d: %s", w.Code, w.Body.String())
	}
	rates := w.Body.String()
	if !strings.Contains(rates, `"base":"USD"`) || !strings.Contains(rates, `"USD":1`) || !strings.Contains(rates, `"EUR":0.91`) {
		t.Errorf("rates payload = %s", rates)
	}
	if strings.Contains(rates, `"updated_at":"0001-01-01`) {
		t.Errorf("rates claims no feed has ever run despite a stored rate: %s", rates)
	}

	// ---- cities ----
	w = get("/api/v1/cities")
	if w.Code != http.StatusOK {
		t.Fatalf("cities index: status = %d: %s", w.Code, w.Body.String())
	}
	cities := w.Body.String()
	// Two verified listings share Jakarta by this point; the count is the number
	// of verified businesses, not the number of rows the test happened to create
	// first.
	if !strings.Contains(cities, `"slug":"jakarta"`) || !strings.Contains(cities, `"count":2`) {
		t.Errorf("cities index = %s", cities)
	}
	// The draft's city exists only on a draft listing, so it has no landing page.
	if strings.Contains(cities, "bandung") {
		t.Errorf("cities index lists a city with no verified business: %s", cities)
	}
	w = get("/api/v1/cities/jakarta")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"Jakarta"`) {
		t.Errorf("city page: status = %d, body = %s", w.Code, w.Body.String())
	}
	// Resolution is case-insensitive, because a crawler may preserve case and a
	// canonical URL must still resolve.
	if w := get("/api/v1/cities/JAKARTA"); w.Code != http.StatusOK {
		t.Errorf("uppercase city slug: status = %d, want 200", w.Code)
	}
	if w := get("/api/v1/cities/nowhere"); w.Code != http.StatusNotFound {
		t.Errorf("unknown city: status = %d, want 404: %s", w.Code, w.Body.String())
	}
}
