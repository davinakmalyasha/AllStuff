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

// The OG share card is an SVG served from the API origin and built by string
// interpolation. That makes two things worth testing that no service test can
// see: every owner-controlled field is escaped, and every URL that becomes an
// <image href> passed safeMediaRef.

func TestOGImage(t *testing.T) {
	s, h, _ := newDBTestServerDeps(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	reviewer := testutil.User(t, h)
	cat := testutil.Category(t, h, "OG Cafe")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "OG Coffee", Slug: "og-coffee", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})
	testutil.Review(t, h, b.ID, reviewer.ID, 5)
	logo := "/api/v1/media/" + util.NewUUID() + "/file"
	h.Exec(t, `UPDATE businesses SET tagline = $2, logo_url = $3 WHERE id = $1`,
		b.ID, "Best coffee in town", logo)

	get := func(t *testing.T, slug string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/og/b/"+slug, nil))
		return w
	}

	w := get(t, b.Slug)
	if w.Code != http.StatusOK {
		t.Fatalf("og image: status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "public") || !strings.Contains(cc, "max-age=3600") {
		t.Errorf("Cache-Control = %q, want public, max-age=3600", cc)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "sandbox") {
		t.Errorf("CSP = %q, want default-src 'none' and sandbox", csp)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
	body := w.Body.String()
	for _, want := range []string{
		"OG Coffee",
		"Best coffee in town",
		"Jakarta, ID",
		cat.Name,
		"★ 5.0 (1 reviews)",
		`<image x="440" y="40" width="120" height="120" href="` + logo + `"/>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("og image missing %q: %s", want, body)
		}
	}

	// A name longer than the card's budget is truncated with an ellipsis, but
	// only the rendered copy - the canonical page keeps the full name.
	long := strings.Repeat("N", 40)
	h.Exec(t, `UPDATE businesses SET name = $2 WHERE id = $1`, b.ID, long)
	body = get(t, b.Slug).Body.String()
	if !strings.Contains(body, strings.Repeat("N", 33)+"…") {
		t.Errorf("long name was not truncated: %s", body)
	}
	if strings.Contains(body, long) {
		t.Error("the full 40-character name reached the card")
	}

	// ---- hostile text ----
	h.Exec(t, `UPDATE businesses SET name = $2, tagline = $3, logo_url = NULL WHERE id = $1`,
		b.ID, `OG <script>alert(1)</script>`, `"><img src=x onerror=alert(1)>`)
	w = get(t, b.Slug)
	if w.Code != http.StatusOK {
		t.Fatalf("hostile og image: status = %d: %s", w.Code, w.Body.String())
	}
	body = w.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, "<img") {
		t.Errorf("owner text reached the SVG as live markup: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("script payload was not escaped: %s", body)
	}

	// ---- logo policy ----
	// safeMediaRef allows an internal media reference or an absolute https
	// image. Every other scheme, a protocol-relative host, and traversal are
	// refused, so the card cannot be turned into a beacon or a local-file read.
	reject := []string{
		"javascript:alert(1)",
		"data:image/svg+xml,<svg onload=alert(1)>",
		"http://cdn.example.test/logo.png",
		"//evil.example/logo.png",
		"/api/v1/media/../etc/passwd",
		"https://",
		"/not-media/logo.png",
	}
	for _, logo := range reject {
		h.Exec(t, `UPDATE businesses SET logo_url = $2 WHERE id = $1`, b.ID, logo)
		body = get(t, b.Slug).Body.String()
		if strings.Contains(body, "<image") {
			t.Errorf("logo %q was rendered: %s", logo, body)
		}
	}
	for _, logo := range []string{
		"https://cdn.example.test/logo.png",
		"/api/v1/media/" + util.NewUUID() + "/file",
	} {
		h.Exec(t, `UPDATE businesses SET logo_url = $2 WHERE id = $1`, b.ID, logo)
		body = get(t, b.Slug).Body.String()
		if !strings.Contains(body, `href="`+logo+`"`) {
			t.Errorf("allowed logo %q was not rendered: %s", logo, body)
		}
	}

	// ---- visibility ----
	paused := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Paused Cafe", Slug: "paused-cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessPaused,
	})
	// GetPublic resolves paused listings; the card is stricter, because an OG
	// image must never advertise a storefront that is not taking customers.
	if w := get(t, paused.Slug); w.Code != http.StatusNotFound {
		t.Errorf("paused business: status = %d, want 404", w.Code)
	}
	draft := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Draft Cafe", Slug: "draft-cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessDraft,
	})
	if w := get(t, draft.Slug); w.Code != http.StatusNotFound {
		t.Errorf("draft business: status = %d, want 404", w.Code)
	}
	if w := get(t, "does-not-exist"); w.Code != http.StatusNotFound {
		t.Errorf("unknown slug: status = %d, want 404", w.Code)
	}
}

func TestSafeMediaRef(t *testing.T) {
	cases := map[string]bool{
		"/api/v1/media/abc/file":      true,
		"https://cdn.example/x.png":   true,
		"http://cdn.example/x.png":    false,
		"javascript:alert(1)":         false,
		"data:image/png;base64,AAA":   false,
		"//evil.example/x.png":        false,
		"/api/v1/media/../etc/passwd": false,
		"/other/path.png":             false,
		"":                            false,
		"https://":                    false,
	}
	for in, want := range cases {
		if got := safeMediaRef(in); got != want {
			t.Errorf("safeMediaRef(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestOGHelpers(t *testing.T) {
	if got := xmlEscape(`<&>"'`); got != "&lt;&amp;&gt;&quot;&apos;" {
		t.Errorf("xmlEscape = %q", got)
	}
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcde", 5, "abcde"},
		{"abcdef", 5, "abcd…"},
		{"café", 4, "café"},
		{"cafés", 4, "caf…"},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
	if got := derefS(nil); got != "" {
		t.Errorf("derefS(nil) = %q", got)
	}
	s := "x"
	if got := derefS(&s); got != "x" {
		t.Errorf("derefS(&x) = %q", got)
	}
}
