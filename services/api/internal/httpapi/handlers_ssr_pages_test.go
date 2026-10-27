//go:build !race

package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
)

// The city and category landing pages are crawler entry points. The SSR shell
// tests cover the renderer's escaping, but the handlers themselves had never
// run: their data selection (which categories, which businesses), the
// breadcrumb/child distinction, and the status codes on a miss were untested.

func TestSSRCityPage(t *testing.T) {
	s, h, _ := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)

	coffee := testutil.Category(t, h, "Coffee <script>alert(1)</script>")
	bakery := testutil.Category(t, h, "Bakeries")
	testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Bandung Roastery", Slug: "bandung-roastery", Category: coffee, Owner: owner,
		City: "Bandung", Country: "ID", Status: domain.BusinessVerified,
	})
	testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Bandung Bakery", Slug: "bandung-bakery", Category: bakery, Owner: owner,
		City: "Bandung", Country: "ID", Status: domain.BusinessVerified,
	})
	// A listing in another city must not leak onto this page.
	testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Surabaya Roastery", Slug: "surabaya-roastery", Category: coffee, Owner: owner,
		City: "Surabaya", Country: "ID", Status: domain.BusinessVerified,
	})

	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ssr/city/bandung", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("city page: status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Businesses in Bandung",
		"2 verified listings on BizVerse",
		"https://bizverse.test/city/bandung",
		"https://bizverse.test/discover?category=" + coffee.Slug,
		"https://bizverse.test/b/bandung-roastery",
		"https://bizverse.test/b/bandung-bakery",
		// Owner-controlled category text is escaped before it lands in HTML.
		"Coffee &lt;script&gt;alert(1)&lt;/script&gt;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("city page missing %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("category name reached the page as live markup")
	}
	if strings.Contains(body, "Surabaya Roastery") {
		t.Error("a business from another city is listed")
	}

	// One listing is singular: the plural helper is not just cosmetic in a
	// sentence a crawler reads.
	testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Ubud Cafe", Slug: "ubud-cafe", Category: bakery, Owner: owner,
		City: "Ubud", Country: "ID", Status: domain.BusinessVerified,
	})
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ssr/city/ubud", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("single city page: status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "1 verified listing on BizVerse") {
		t.Errorf("single listing was not singular: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "1 verified listings") {
		t.Error("one listing was pluralised")
	}

	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ssr/city/atlantis", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown city: status = %d, want 404", w.Code)
	}
	if got := w.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Errorf("unknown city X-Robots-Tag = %q, want noindex", got)
	}
}

func TestSSRCategoryPage(t *testing.T) {
	s, h, _ := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)

	food := testutil.Category(t, h, "Food & Drink")
	coffee := testutil.ChildCategory(t, h, food, "Coffee")
	espresso := testutil.ChildCategory(t, h, coffee, "Espresso")
	description := `A <script>alert(1)</script> place`
	h.Exec(t, `UPDATE categories SET description = $2 WHERE id = $1`, coffee.ID, description)
	testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Deep Coffee", Slug: "deep-coffee", Category: coffee, Owner: owner,
		City: "Bandung", Country: "ID", Status: domain.BusinessVerified,
	})

	get := func(t *testing.T, slug string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ssr/c/"+slug, nil))
		return w
	}

	// GetBySlug's second return is the breadcrumb. A parent page must still
	// link its direct children; the old code rendered the breadcrumb instead,
	// so "Food & Drink" listed only itself and never Coffee.
	w := get(t, food.Slug)
	if w.Code != http.StatusOK {
		t.Fatalf("parent category: status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "https://bizverse.test/c/"+coffee.Slug) || !strings.Contains(body, "Coffee") {
		t.Errorf("parent category does not link its child: %s", body)
	}
	if !strings.Contains(body, "1 verified listing.") {
		// The count includes descendants, so the parent reports Coffee's one
		// business rather than zero.
		t.Errorf("parent count does not include descendants: %s", body)
	}
	if strings.Contains(body, "/c/"+espresso.Slug) {
		t.Error("parent category lists a grandchild as a direct child")
	}

	w = get(t, coffee.Slug)
	if w.Code != http.StatusOK {
		t.Fatalf("child category: status = %d: %s", w.Code, w.Body.String())
	}
	body = w.Body.String()
	if !strings.Contains(body, "https://bizverse.test/c/"+espresso.Slug) {
		t.Errorf("child category does not link the grandchild: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("category description was not escaped: %s", body)
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("category description reached the page as live markup")
	}

	w = get(t, "no-such-category")
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown category: status = %d, want 404", w.Code)
	}
}

// escapeQueryValue is a byte-whitelist, not url.PathEscape: every byte that is
// not unreserved is percent-encoded, so a value cannot break out of a query
// parameter by smuggling "&" or "=".
func TestEscapeQueryValue(t *testing.T) {
	cases := map[string]string{
		"coffee": "coffee",
		"a&b=c":  "a%26b%3Dc",
		"a b":    "a%20b",
		"café":   "caf%C3%A9",
		"/x?y#z": "%2Fx%3Fy%23z",
		"100%":   "100%25",
		"a+b":    "a%2Bb",
	}
	for in, want := range cases {
		if got := escapeQueryValue(in); got != want {
			t.Errorf("escapeQueryValue(%q) = %q, want %q", in, got, want)
		}
	}
}

// Reload exists for the maintenance path that swaps dist/ on disk during a
// deploy. A no-op would leave every page pointing at the previous hashed
// bundle, which 404s once the old assets are gone.
func TestSSRReload(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	writeManifest := func(t *testing.T, m map[string]ssrAsset) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".vite"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".vite", "manifest.json"), raw, 0o644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}

	// The entry imports a vendor chunk; the renderer must collect CSS from
	// both, because manualChunks puts dependency CSS in the child.
	writeManifest(t, map[string]ssrAsset{
		"index.html": {File: "assets/index-a.js", CSS: []string{"assets/a.css"}, Imports: []string{"vendor"}},
		"vendor":     {File: "assets/vendor.js", CSS: []string{"assets/vendor.css"}},
	})
	r := newSSRRenderer("https://bizverse.test", dir, logger)
	if r.headEntry != "assets/index-a.js" {
		t.Errorf("headEntry = %q", r.headEntry)
	}
	if strings.Join(r.headCSS, ",") != "assets/a.css,assets/vendor.css" {
		t.Errorf("headCSS = %v", r.headCSS)
	}

	// A deploy replaces the manifest. Until Reload runs the old entry must stay
	// (that is the point of the cache); Reload then swaps both entry and CSS.
	writeManifest(t, map[string]ssrAsset{
		"index.html": {File: "assets/index-b.js", CSS: []string{"assets/b.css"}},
	})
	if r.headEntry != "assets/index-a.js" {
		t.Fatal("the manifest was re-read without Reload")
	}
	r.Reload()
	if r.headEntry != "assets/index-b.js" {
		t.Errorf("after Reload headEntry = %q, want assets/index-b.js", r.headEntry)
	}
	if strings.Join(r.headCSS, ",") != "assets/b.css" {
		t.Errorf("after Reload headCSS = %v, want only the new stylesheet", r.headCSS)
	}

	// A missing manifest degrades to the no-JS shell and must not keep serving
	// the previous bundle's tags.
	if err := os.RemoveAll(filepath.Join(dir, ".vite")); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	r.Reload()
	if r.headEntry != "" || len(r.headCSS) != 0 {
		t.Errorf("after removing the manifest: entry %q, css %v", r.headEntry, r.headCSS)
	}
}
