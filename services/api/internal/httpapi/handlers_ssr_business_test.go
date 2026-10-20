//go:build !race

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
)

// handleSSRBusiness had no coverage - 73 statements, the largest single
// uncovered function. It is the page a search engine indexes, so both the
// refusal path and the "keep it indexable when the catalog is down" path matter.

func getSSRBusiness(s *Server, slug string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/b/"+slug, nil)
	req.SetPathValue("slug", slug)
	w := httptest.NewRecorder()
	s.handleSSRBusiness(w, req)
	return w
}

func verifiedBusiness(t *testing.T, h *testutil.H, opts testutil.BusinessOpts) *domain.Business {
	t.Helper()
	cat := testutil.Category(t, h, "Cafe")
	if opts.Owner == nil {
		opts.Owner = testutil.User(t, h)
	}
	opts.Category = cat
	if opts.Status == "" {
		opts.Status = domain.BusinessVerified
	}
	if opts.Name == "" {
		opts.Name = "Kopi Senja"
	}
	if opts.City == "" {
		opts.City = "Jakarta"
		opts.Country = "ID"
	}
	return testutil.Business(t, h, opts)
}

func TestSSRBusinessRefusesNonPublic(t *testing.T) {
	s, h := newDBTestServer(t)

	// An unknown slug and a business that has not been approved both have to
	// answer 404, and the 404 has to be noindex or the page can be indexed as
	// an error.
	w := getSSRBusiness(s, "does-not-exist")
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown slug: status = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "noindex") {
		t.Error("the not-found page is not noindex")
	}

	draft := verifiedBusiness(t, h, testutil.BusinessOpts{Status: domain.BusinessDraft, Name: "Draft Cafe"})
	if w := getSSRBusiness(s, draft.Slug); w.Code != http.StatusNotFound {
		t.Errorf("a draft: status = %d, want 404", w.Code)
	}

	// The service layer treats a paused listing as publicly readable; the SSR
	// page is stricter and hides it, because a suspended-for-review page should
	// not be crawled. Pin that difference rather than assume it.
	paused := verifiedBusiness(t, h, testutil.BusinessOpts{Status: domain.BusinessPaused, Name: "Paused Cafe"})
	if w := getSSRBusiness(s, paused.Slug); w.Code != http.StatusNotFound {
		t.Errorf("a paused business: status = %d, want 404", w.Code)
	}
}

func TestSSRBusinessRendersAndEscapes(t *testing.T) {
	s, h := newDBTestServer(t)
	// A hostile name travels through the <h1>, the <title> and the JSON-LD.
	b := verifiedBusiness(t, h, testutil.BusinessOpts{Name: `Kopi <script>alert(1)</script>`})
	testutil.Product(t, h, b.ID, "Es Kopi Susu")

	w := getSSRBusiness(s, b.Slug)
	body := w.Body.String()

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(body, "index,follow") {
		t.Error("a public business page is not indexable")
	}
	// The canonical uses the slug, not the id and not the raw name.
	if !strings.Contains(body, `https://bizverse.test/b/`+b.Slug) {
		t.Errorf("canonical for %q is missing", b.Slug)
	}
	if !strings.Contains(body, "<article>") {
		t.Error("no article body")
	}
	// The name reaches the page escaped - no live script tag from owner input.
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("the business name was not escaped")
	}
	if !strings.Contains(body, "script") {
		t.Error("expected the escaped name to still be present as text")
	}

	// Structured data: LocalBusiness always, ItemList when there are products.
	for _, want := range []string{`"@type":"LocalBusiness"`, `"@type":"ItemList"`, "Es Kopi Susu"} {
		if !strings.Contains(body, want) {
			t.Errorf("JSON-LD is missing %s", want)
		}
	}
}

// A catalog failure must not cost the page its indexability: the business page
// is the valuable URL, and a products outage is transient. The handler is
// written for that; this asserts it stays that way.
func TestSSRBusinessCatalogFailureStaysIndexable(t *testing.T) {
	s, h := newDBTestServer(t)
	b := verifiedBusiness(t, h, testutil.BusinessOpts{Name: "Toko Roti"})
	testutil.Product(t, h, b.ID, "Roti Cokelat")

	// Break the catalog query the handler uses, without touching the business.
	if _, err := h.Repos.Exec(t.Context(), `ALTER TABLE products RENAME TO products_unavailable`); err != nil {
		t.Fatalf("breaking the products table: %v", err)
	}

	w := getSSRBusiness(s, b.Slug)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d after a catalog failure, want 200", w.Code)
	}
	if !strings.Contains(body, "index,follow") {
		t.Error("a catalog failure made the page non-indexable")
	}
	if !strings.Contains(body, "Toko Roti") {
		t.Error("the business itself is missing from the page")
	}
	if strings.Contains(body, "bv-products") {
		t.Error("the products section was rendered despite the catalog failing")
	}
}

// An AggregateRating with a zero review count is a spam signal in search
// results, so the handler emits it only when there are real reviews. The rating
// is computed from the reviews table rather than stored on the business.
func TestSSRBusinessAggregateRatingNeedsReviews(t *testing.T) {
	s, h := newDBTestServer(t)
	b := verifiedBusiness(t, h, testutil.BusinessOpts{Name: "Warung Malam"})

	if body := getSSRBusiness(s, b.Slug).Body.String(); strings.Contains(body, "AggregateRating") {
		t.Error("an AggregateRating was emitted for a business with no reviews")
	}

	testutil.Review(t, h, b.ID, testutil.User(t, h).ID, 5)
	testutil.Review(t, h, b.ID, testutil.User(t, h).ID, 4)

	body := getSSRBusiness(s, b.Slug).Body.String()
	if !strings.Contains(body, "AggregateRating") {
		t.Error("an AggregateRating was not emitted despite reviews")
	}
	if !strings.Contains(body, "4.5") {
		t.Error("the average of 5 and 4 is missing from the page")
	}
}
