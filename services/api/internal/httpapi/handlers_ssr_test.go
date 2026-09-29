package httpapi

import (
	"encoding/json"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestSSR() *ssrRenderer {
	return newSSRRenderer("https://bizverse.test", os.Getenv("TEST_WEB_DIST"), slog.Default())
}

// esc mirrors what the handler does to owner-controlled text before it lands
// in the body, so the test's "did it escape?" assertion is meaningful.
func esc(s string) string { return html.EscapeString(s) }

// The SSR shell renders owner-controlled text on the API origin, which sits
// under the same registrable domain as the session cookies. An escaping bug
// here is materially worse than in the SPA, so the escaping is asserted
// directly rather than inferred from a snapshot.
//
// Note on what is NOT asserted: inert escaped text may still CONTAIN the
// substring "onerror=alert" as visible characters. That is harmless. What must
// never appear is an unescaped tag a parser would treat as markup, or a
// javascript: URL inside an attribute the browser will act on.
func TestSSRRender_EscapesUserContent(t *testing.T) {
	r := newTestSSR()
	payloads := []string{
		`<script>alert(1)</script>`,
		`</script><script>alert('xss')</script>`,
		`"><img src=x onerror=alert(1)>`,
		`"><svg/onload=alert(1)>`,
		`javascript:alert(1)`,
		`--><script>alert(1)</script><!--`,
		`"><iframe src=javascript:alert(1)>`,
		`<style>body{display:none}</style>`,
	}
	// <script> is counted separately; <link> is emitted by the template itself
	// (stylesheet + canonical), so it is not evidence of injection.
	liveTags := []string{"<img", "<svg", "<iframe", "<object", "<embed", "<style"}

	for _, p := range payloads {
		w := httptest.NewRecorder()
		r.render(w, ssrPage{
			Title:       p,
			Description: p,
			Canonical:   "https://bizverse.test/b/x",
			Body:        "<article><h1>" + esc(p) + "</h1><p>" + esc(p) + "</p></article>",
			JSONLD:      []map[string]any{{"@context": "https://schema.org", "name": p, "description": p}},
		})
		body := w.Body.String()

		// Count the tags the template itself emits, then assert nothing else
		// opened one.
		blocks := strings.Count(body, `<script type="application/ld+json">`)
		modules := strings.Count(body, `<script type="module"`)
		if got := strings.Count(body, "<script"); got != blocks+modules {
			t.Errorf("payload %q: %d <script tags opened, want %d (jsonld+module only)", p, got, blocks+modules)
		}
		if got := strings.Count(body, "</script>"); got != blocks+modules {
			t.Errorf("payload %q: %d </script> terminators, want %d", p, got, blocks+modules)
		}
		for _, tag := range liveTags {
			if strings.Contains(body, tag) {
				t.Errorf("payload %q opened a live %s tag", p, tag)
			}
		}

		// A hostile title/description must not break out of a
		// content="..." attribute, which would let it inject a new tag.
		for _, quote := range []string{`"><script>alert(1)</script>`, `"><img src=x>`} {
			if strings.Contains(body, quote) {
				t.Errorf("payload %q broke out of a meta attribute", p)
			}
		}
		if n := strings.Count(body, `content="`); n < 6 {
			t.Errorf("payload %q: expected the full meta set, got %d content attributes", p, n)
		}
	}
}

func TestSSRRender_NotFoundIsNoindex(t *testing.T) {
	r := newTestSSR()
	w := httptest.NewRecorder()
	r.notFound(w, "gone")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if got := w.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Errorf("X-Robots-Tag = %q, want noindex so a soft 404 is not indexed", got)
	}
	// A JSON error body would be useless to a crawler.
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// A path segment must never be able to inject a scheme or a host.
func TestSSRHref_EscapesUntrustedSegments(t *testing.T) {
	cases := map[string]string{
		"rumah-kopi":       "/b/rumah-kopi",
		"../../etc/passwd": "/b/..%2F..%2Fetc%2Fpasswd",
		"a?b=c":            "/b/a%3Fb%3Dc",
		"a#b":              "/b/a%23b",
		"//evil.com":       "/b/%2F%2Fevil.com",
		`a"onmouseover=x`:  "/b/a%22onmouseover%3Dx",
		"café":             "/b/caf%C3%A9",
	}
	for in, want := range cases {
		if got := href("b", in); got != want {
			t.Errorf("href(b, %q) = %q, want %q", in, got, want)
		}
	}
}

func TestSSRHref_MultiSegment(t *testing.T) {
	if got := href("og", "b", "my-slug"); got != "/og/b/my-slug" {
		t.Errorf("href(og,b,my-slug) = %q", got)
	}
}

// Contact values are stored without a scheme constraint today, so a
// javascript: website must not be emitted as an href.
func TestSSRContact_RejectsNonHTTPSWebsite(t *testing.T) {
	if got := ssrContactHTML(map[string]any{"website": "javascript:alert(1)"}, ""); strings.Contains(got, "javascript:") {
		t.Errorf("a javascript: website reached an href: %s", got)
	}
	if got := ssrContactHTML(map[string]any{"website": "https://example.com"}, ""); !strings.Contains(got, `href="https://example.com"`) {
		t.Errorf("an https website should be linked: %s", got)
	}
	// A data: URL must not be linked either.
	if got := ssrContactHTML(map[string]any{"website": "data:text/html,<script>alert(1)</script>"}, ""); strings.Contains(got, "data:") {
		t.Errorf("a data: URL reached an href: %s", got)
	}
	// A quote-breakout attempt in an otherwise-valid https URL must be escaped.
	q := `https://example.com/"onmouseover="alert(1)`
	got := ssrContactHTML(map[string]any{"website": q}, "")
	if strings.Contains(got, `"onmouseover="`) {
		t.Errorf("an attribute breakout survived: %s", got)
	}
}

func TestSSRPriceRange(t *testing.T) {
	one, two, nilLevel := 1, 3, (*int)(nil)
	cases := []struct {
		in   *int
		want string
	}{
		{&one, "$"}, {&two, "$$$"}, {nilLevel, ""},
	}
	for i, c := range cases {
		if got := ssrPriceRange(c.in); got != c.want {
			t.Errorf("case %d: ssrPriceRange = %q, want %q", i, got, c.want)
		}
	}
	// Out-of-band values must not produce nonsense.
	bad := 9
	if got := ssrPriceRange(&bad); got != "" {
		t.Errorf("out-of-range price level produced %q, want empty", got)
	}
}

func TestSSRRender_NoManifestIsNoJSButStillCrawlable(t *testing.T) {
	// With no dist dir the renderer must still emit the SEO content; losing
	// hydration must not lose indexability.
	r := newSSRRenderer("https://bizverse.test", "", slog.Default())
	w := httptest.NewRecorder()
	r.render(w, ssrPage{
		Title:  "Kopi | BizVerse",
		Body:   "<article><h1>Kopi</h1><p>Great coffee</p></article>",
		JSONLD: []map[string]any{{"@context": "https://schema.org", "@type": "LocalBusiness", "name": "Kopi"}},
	})
	body := w.Body.String()
	if !strings.Contains(body, "<h1>Kopi</h1>") {
		t.Error("crawlable body content was dropped when the manifest is missing")
	}
	if !strings.Contains(body, "application/ld+json") {
		t.Error("JSON-LD was dropped when the manifest is missing")
	}
	if strings.Contains(body, "<script type=\"module\"") {
		t.Error("a module script was emitted with no manifest entry")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// JSON-LD must remain parseable after the "<" escaping, or a consumer
// silently discards the block.
func TestSSRRender_JSONLDRemainsValid(t *testing.T) {
	r := newTestSSR()
	w := httptest.NewRecorder()
	r.render(w, ssrPage{
		Title:  "x",
		JSONLD: []map[string]any{{"@context": "https://schema.org", "name": `A <b> & "c"`}},
	})
	body := w.Body.String()
	start := strings.Index(body, `<script type="application/ld+json">`)
	if start < 0 {
		t.Fatal("no JSON-LD block emitted")
	}
	rest := body[start+len(`<script type="application/ld+json">`):]
	end := strings.Index(rest, "</script>")
	if end < 0 {
		t.Fatal("JSON-LD block not terminated")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(rest[:end]), &out); err != nil {
		t.Fatalf("JSON-LD is not valid JSON after escaping: %v\n%s", err, rest[:end])
	}
	if out["name"] != `A <b> & "c"` {
		t.Errorf("name round-tripped to %q", out["name"])
	}
}

func TestSSRSameAs_OnlyHTTPS(t *testing.T) {
	out := ssrSameAs(map[string]any{
		"website": "https://example.com",
		"email":   "nope@example.com",
	})
	if len(out) != 1 || out[0] != "https://example.com" {
		t.Errorf("ssrSameAs = %v, want only the https website", out)
	}
	bad := ssrSameAs(map[string]any{"website": "javascript:alert(1)"})
	if len(bad) != 0 {
		t.Errorf("ssrSameAs accepted a javascript: URL: %v", bad)
	}
}

// The manifest walk must collect CSS transitively: manualChunks puts the
// entry's stylesheet on the entry, but a chunk that imports another chunk can
// carry CSS on the child, and a page that loads without its CSS is a visibly
// broken page.
func TestSSRManifest_CollectsCSSTransitively(t *testing.T) {
	dir := t.TempDir()
	viteDir := filepath.Join(dir, ".vite")
	if err := os.MkdirAll(viteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
      "index.html": {
        "file": "assets/index-AAA.js", "isEntry": true,
        "src": "index.html",
        "css": ["assets/index-AAA.css"],
        "imports": ["_react-BBB.js"]
      },
      "_react-BBB.js": {
        "file": "assets/react-BBB.js", "isEntry": false,
        "css": ["assets/react-BBB.css"]
      }
    }`
	if err := os.WriteFile(filepath.Join(viteDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newSSRRenderer("https://bizverse.test", dir, slog.Default())
	if r.headEntry != "assets/index-AAA.js" {
		t.Fatalf("headEntry = %q", r.headEntry)
	}
	if len(r.headCSS) != 2 {
		t.Fatalf("headCSS = %v, want the entry CSS and the imported chunk's CSS", r.headCSS)
	}

	w := httptest.NewRecorder()
	r.render(w, ssrPage{Title: "x", Body: "<p>x</p>"})
	body := w.Body.String()
	for _, want := range []string{
		`<link rel="stylesheet" href="/assets/index-AAA.css">`,
		`<link rel="stylesheet" href="/assets/react-BBB.css">`,
		`<script type="module" src="/assets/index-AAA.js">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("document missing %s\ngot:\n%s", want, body)
		}
	}
}

// A corrupt manifest must degrade to no-JS, never to a 500 or a page that
// references a file that does not exist.
func TestSSRManifest_CorruptDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vite"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".vite", "manifest.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newSSRRenderer("https://bizverse.test", dir, slog.Default())
	if r.headEntry != "" {
		t.Errorf("headEntry = %q, want empty for a corrupt manifest", r.headEntry)
	}
	w := httptest.NewRecorder()
	r.render(w, ssrPage{Title: "t", Body: "<p>keep me</p>"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "keep me") {
		t.Error("a corrupt manifest should still serve crawlable HTML")
	}
}

// A manifest with no index.html entry (e.g. pointed at the wrong directory).
func TestSSRManifest_MissingEntryDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vite"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".vite", "manifest.json"), []byte(`{"other.js":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newSSRRenderer("https://bizverse.test", dir, slog.Default())
	if r.headEntry != "" {
		t.Errorf("headEntry = %q, want empty when index.html is absent", r.headEntry)
	}
}
