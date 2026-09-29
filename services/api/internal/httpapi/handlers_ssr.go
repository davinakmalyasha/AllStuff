package httpapi

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"bizverse/api/internal/domain"
)

// ---- server-rendered SEO shell (Phase 5) ----
//
// The problem this solves: the frontend is a client-only SPA whose <title>,
// meta description, canonical, Open Graph tags and JSON-LD are all injected
// from a useEffect AFTER hydration. Google must render a second wave to see any
// of it, while the API advertises up to 100,000 /b/{slug} URLs in its sitemap.
// For a business directory, organic search IS the product.
//
// The fix is a server-rendered shell on the API origin. `vercel.json` rewrites
// /b/:slug, /c/:slug and /city/:slug here, so a crawler receives a complete HTML
// document with real crawlable body text, which the SPA then takes over on
// hydration. There is no framework migration and no duplicated page: the
// document is a thin shell around the real bundle, and the SEO block lives in a
// sibling #seo node that the SPA removes on mount.
//
// If WEB_DIST_DIR is unset or the manifest is missing, this degrades to a
// static no-JS document. The SEO content is still served — only hydration is
// lost — so a misconfigured path degrades the experience, never the indexing.

// ssrAsset is the subset of .vite/manifest.json we need.
type ssrAsset struct {
	File    string   `json:"file"`
	CSS     []string `json:"css"`
	Imports []string `json:"imports"`
	IsEntry bool     `json:"isEntry"`
}

type ssrRenderer struct {
	publicURL string
	distDir   string
	logger    *slog.Logger

	mu        sync.RWMutex
	headCSS   []string
	headEntry string
}

func newSSRRenderer(publicURL, distDir string, logger *slog.Logger) *ssrRenderer {
	r := &ssrRenderer{publicURL: strings.TrimRight(publicURL, "/"), distDir: distDir, logger: logger}
	r.loadManifest()
	return r
}

// loadManifest reads .vite/manifest.json. A failure is logged and leaves the
// renderer in no-JS mode; it is not fatal.
func (r *ssrRenderer) loadManifest() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.headCSS, r.headEntry = nil, ""

	if r.distDir == "" {
		return
	}
	raw, err := os.ReadFile(filepath.Join(r.distDir, ".vite", "manifest.json"))
	if err != nil {
		r.logger.Warn("ssr: vite manifest unavailable; serving no-JS SEO shell", "err", err, "dist", r.distDir)
		return
	}
	var m map[string]ssrAsset
	if err := json.Unmarshal(raw, &m); err != nil {
		r.logger.Warn("ssr: vite manifest is not valid JSON; serving no-JS shell", "err", err)
		return
	}
	entry, ok := m["index.html"]
	if !ok {
		r.logger.Warn("ssr: vite manifest has no index.html entry; serving no-JS shell")
		return
	}
	r.headEntry = entry.File
	// Collect CSS transitively: an entry's CSS list plus that of everything it
	// imports, since manualChunks puts react/router/query CSS in the children.
	seen := map[string]bool{}
	var walk func(key string)
	walk = func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		a, ok := m[key]
		if !ok {
			return
		}
		r.headCSS = append(r.headCSS, a.CSS...)
		for _, imp := range a.Imports {
			walk(imp)
		}
	}
	walk("index.html")
	r.logger.Info("ssr: manifest loaded", "entry", r.headEntry, "css", len(r.headCSS))
}

// Reload re-reads the manifest. Called by the maintenance path after a deploy
// replaces dist/ on disk.
func (r *ssrRenderer) Reload() { r.loadManifest() }

func (r *ssrRenderer) assetURL(file string) string { return "/" + strings.TrimPrefix(file, "/") }

// ---- document assembly ----

type ssrPage struct {
	Title       string
	Description string
	Canonical   string
	OGImage     string
	OGType      string
	Robots      string
	// Breadcrumb renders as a visible nav and as BreadcrumbList JSON-LD.
	Breadcrumb [][2]string
	JSONLD     []map[string]any
	// Body is the crawlable content; the caller passes HTML-escaped values.
	Body string
	// Status defaults to 200.
	Status int
}

const ssrDefaultDescription = "Find businesses near you — browse verified listings, compare, and get in touch."

// render emits the HTML document. Everything interpolated is either escaped
// here or pre-escaped by the caller; there is no third path.
func (r *ssrRenderer) render(w http.ResponseWriter, p ssrPage) {
	r.mu.RLock()
	entry, css := r.headEntry, r.headCSS
	r.mu.RUnlock()

	var cssLinks strings.Builder
	for _, c := range css {
		fmt.Fprintf(&cssLinks, `<link rel="stylesheet" href="%s">`, html.EscapeString(r.assetURL(c)))
	}
	desc := firstNonEmpty(p.Description, ssrDefaultDescription)
	robots := firstNonEmpty(p.Robots, "index,follow")
	ogType := firstNonEmpty(p.OGType, "website")

	var jsonld strings.Builder
	for _, block := range p.JSONLD {
		b, err := json.Marshal(block)
		if err != nil {
			continue
		}
		// "<" is escaped so a user-controlled string containing "</script>"
		// cannot break out of the tag. This is the stored-XSS vector the
		// frontend's useJsonLd already guards against on the client.
		fmt.Fprintf(&jsonld, `<script type="application/ld+json">%s</script>`,
			strings.ReplaceAll(string(b), "<", `<`))
	}

	var crumbs strings.Builder
	if len(p.Breadcrumb) > 0 {
		crumbs.WriteString(`<nav class="bv-crumbs" aria-label="Breadcrumb"><ol>`)
		for i, c := range p.Breadcrumb {
			crumbs.WriteString("<li>")
			if i == len(p.Breadcrumb)-1 {
				fmt.Fprintf(&crumbs, `<span aria-current="page">%s</span>`, html.EscapeString(c[1]))
			} else {
				fmt.Fprintf(&crumbs, `<a href="%s">%s</a>`, html.EscapeString(c[0]), html.EscapeString(c[1]))
			}
			crumbs.WriteString("</li>")
		}
		crumbs.WriteString("</ol></nav>")
	}

	status := p.Status
	if status == 0 {
		status = http.StatusOK
	}

	var sb strings.Builder
	sb.Grow(len(p.Body) + 4096)
	sb.WriteString(`<!doctype html><html lang="en"><head>`)
	sb.WriteString(`<meta charset="utf-8">`)
	sb.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	fmt.Fprintf(&sb, `<title>%s</title>`, html.EscapeString(p.Title))
	fmt.Fprintf(&sb, `<meta name="description" content="%s">`, html.EscapeString(desc))
	fmt.Fprintf(&sb, `<meta name="robots" content="%s">`, html.EscapeString(robots))
	if p.Canonical != "" {
		fmt.Fprintf(&sb, `<link rel="canonical" href="%s">`, html.EscapeString(p.Canonical))
	}
	fmt.Fprintf(&sb, `<meta property="og:title" content="%s">`, html.EscapeString(p.Title))
	fmt.Fprintf(&sb, `<meta property="og:description" content="%s">`, html.EscapeString(desc))
	fmt.Fprintf(&sb, `<meta property="og:type" content="%s">`, html.EscapeString(ogType))
	if p.Canonical != "" {
		fmt.Fprintf(&sb, `<meta property="og:url" content="%s">`, html.EscapeString(p.Canonical))
	}
	if p.OGImage != "" {
		fmt.Fprintf(&sb, `<meta property="og:image" content="%s">`, html.EscapeString(p.OGImage))
		sb.WriteString(`<meta name="twitter:card" content="summary_large_image">`)
	}
	sb.WriteString(cssLinks.String())
	sb.WriteString(jsonld.String())
	sb.WriteString(`</head>`)

	// The SEO content lives in #seo, a sibling of #root. The SPA mounts into
	// #root and never disturbs it, so a crawler that does not execute JS still
	// reads the content, and a browser that does gets the app with the SEO
	// block removed on mount (see main.tsx).
	sb.WriteString(`<body><div id="root"></div><div id="seo" class="bv-seo">`)
	sb.WriteString(crumbs.String())
	sb.WriteString(p.Body)
	sb.WriteString(`</div>`)

	if entry != "" {
		fmt.Fprintf(&sb, `<script type="module" src="%s"></script>`, html.EscapeString(r.assetURL(entry)))
	}
	sb.WriteString(`</body></html>`)

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// Crawlers re-fetch often; the payload is small and the origin is cheap.
	h.Set("Cache-Control", "public, max-age=300, s-maxage=3600")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", robots)
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, sb.String())
}

func (r *ssrRenderer) absURL(p string) string {
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	return r.publicURL + p
}

// ---- path building ----

// href builds a site-absolute path from untrusted segments. Every byte outside
// the unreserved set is percent-encoded, so a slug can never inject a scheme, a
// host, or an extra path segment. This is the same class of guard as
// safeExternalUrl on the client, applied server-side.
func href(parts ...string) string {
	var b strings.Builder
	b.WriteString("/")
	for i, p := range parts {
		if i > 0 {
			b.WriteString("/")
		}
		b.WriteString(escapePathSegment(p))
	}
	return b.String()
}

func escapePathSegment(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ---- handlers ----

// handleSSRBusiness renders a public business page.
//
// Mirrors the JSON endpoint's visibility rule exactly: only a `verified`
// business is public. A draft must never be indexed, and a non-verified listing
// must not appear in search results either.
func (s *Server) handleSSRBusiness(w http.ResponseWriter, r *http.Request) {
	b, err := s.deps.Businesses.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil || b == nil || b.Status != domain.BusinessVerified {
		s.ssr.notFound(w, "This business doesn't exist or isn't public yet.")
		return
	}
	products, err := s.deps.Products.ListPublished(r.Context(), b.ID)
	if err != nil {
		// A catalog failure must not cost the page its indexability; render the
		// business without products.
		slog.Warn("ssr: products unavailable", "slug", b.Slug, "err", err)
		products = nil
	}

	path := href("b", b.Slug)
	title := b.Name
	if b.CategoryName != "" {
		title += " — " + b.CategoryName
	}
	title += " | BizVerse"

	desc := derefS(b.Tagline)
	if desc == "" {
		desc = truncate(stripHTML(b.Description), 160)
	}
	if desc == "" {
		desc = "View contact details, hours, products and reviews for " + b.Name + " on BizVerse."
	}

	canonical := s.ssr.absURL(path)
	ogImage := s.ssr.absURL(href("og", "b", b.Slug))

	var body strings.Builder
	fmt.Fprintf(&body, `<article><h1>%s</h1>`, html.EscapeString(b.Name))
	if b.Tagline != nil && *b.Tagline != "" {
		fmt.Fprintf(&body, `<p class="bv-tagline">%s</p>`, html.EscapeString(*b.Tagline))
	}
	fmt.Fprintf(&body, `<p class="bv-loc">%s</p>`,
		html.EscapeString(strings.Trim(strings.TrimSpace(b.City+", "+b.Country), ", ")))

	if b.RatingAvg != nil && b.ReviewCount > 0 {
		fmt.Fprintf(&body, `<p class="bv-rating">Rated %s out of 5 from %d review%s.</p>`,
			html.EscapeString(fmt.Sprintf("%.1f", *b.RatingAvg)), b.ReviewCount, plural(b.ReviewCount))
	}
	if b.Description != "" {
		fmt.Fprintf(&body, `<section class="bv-about"><h2>About</h2><p>%s</p></section>`,
			html.EscapeString(b.Description))
	}

	if rows := ssrHours(b); rows != "" {
		fmt.Fprintf(&body, `<section class="bv-hours"><h2>Hours</h2><dl>%s</dl></section>`, rows)
	}

	if section := ssrContact(b); section != "" {
		fmt.Fprintf(&body, `<section class="bv-contact"><h2>Contact</h2>%s</section>`, section)
	}

	if len(products) > 0 {
		var items strings.Builder
		for _, p := range products {
			price := ""
			if p.BasePrice != nil {
				price = fmt.Sprintf(" — %s %.2f", p.Currency, *p.BasePrice)
			}
			fmt.Fprintf(&items, `<li><span class="bv-product">%s</span><span class="bv-price">%s</span></li>`,
				html.EscapeString(p.Name), html.EscapeString(price))
		}
		fmt.Fprintf(&body, `<section class="bv-products"><h2>Products &amp; services</h2><ul>%s</ul></section>`, items.String())
	}

	fmt.Fprintf(&body, `<p class="bv-cta"><a href="%s">View the full storefront on BizVerse</a></p>`, html.EscapeString(path))
	body.WriteString(`</article>`)

	// JSON-LD: LocalBusiness + BreadcrumbList + ItemList of Products.
	// Product and BreadcrumbList were both missing from the client-side emitter
	// and they are the two that make a listing eligible for product rich
	// results and for a proper breadcrumb in the SERP.
	lb := map[string]any{
		"@context":    "https://schema.org",
		"@type":       "LocalBusiness",
		"name":        b.Name,
		"url":         canonical,
		"description": desc,
		"address": map[string]any{
			"@type":           "PostalAddress",
			"streetAddress":   b.Address,
			"addressLocality": b.City,
			"addressCountry":  b.Country,
		},
		"geo": map[string]any{
			"@type":     "GeoCoordinates",
			"latitude":  b.Lat,
			"longitude": b.Lng,
		},
		"priceRange": ssrPriceRange(b.PriceLevel),
	}
	if img := firstNonEmpty(derefS(b.LogoURL), derefS(b.CoverURL), ogImage); img != "" {
		lb["image"] = img
	}
	if ph := contactString(b.Contact, "phone"); ph != "" {
		lb["telephone"] = ph
	}
	if same := ssrSameAs(b.Contact); len(same) > 0 {
		lb["sameAs"] = same
	}
	if oh := ssrOpeningHours(b); len(oh) > 0 {
		lb["openingHoursSpecification"] = oh
	}
	// An AggregateRating with a zero review count is a spam signal, so it is
	// only emitted when there really are reviews.
	if b.RatingAvg != nil && b.ReviewCount > 0 {
		lb["aggregateRating"] = map[string]any{
			"@type":       "AggregateRating",
			"ratingValue": *b.RatingAvg,
			"reviewCount": b.ReviewCount,
			"bestRating":  5,
			"worstRating": 1,
		}
	}

	blocks := []map[string]any{lb}
	// Declared here rather than assigned to a package var: the handler runs
	// concurrently, so shared mutable state would cross breadcrumbs between
	// requests.
	var crumbs [][2]string
	if b.CategorySlug != "" {
		crumbs = [][2]string{
			{s.ssr.absURL("/"), "BizVerse"},
			{s.ssr.absURL(href("c", b.CategorySlug)), b.CategoryName},
			{canonical, b.Name},
		}
		items := make([]map[string]any, 0, len(crumbs))
		for i, c := range crumbs {
			items = append(items, map[string]any{
				"@type": "ListItem", "position": i + 1, "name": c[1], "item": c[0],
			})
		}
		blocks = append(blocks, map[string]any{
			"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": items,
		})
	}
	if len(products) > 0 {
		items := make([]map[string]any, 0, len(products))
		for _, p := range products {
			entry := map[string]any{"@type": "Product", "name": p.Name, "url": canonical}
			if d := derefS(p.Description); d != "" {
				entry["description"] = truncate(stripHTML(d), 300)
			}
			if p.BasePrice != nil {
				entry["offers"] = map[string]any{
					"@type":         "Offer",
					"price":         fmt.Sprintf("%.2f", *p.BasePrice),
					"priceCurrency": p.Currency,
					"availability":  "https://schema.org/InStock",
					"url":           canonical,
				}
			}
			items = append(items, entry)
		}
		blocks = append(blocks, map[string]any{
			"@context": "https://schema.org", "@type": "ItemList", "itemListElement": items,
		})
	}

	s.ssr.render(w, ssrPage{
		Title:       title,
		Description: desc,
		Canonical:   canonical,
		OGImage:     ogImage,
		OGType:      "business.business",
		Breadcrumb:  crumbs,
		JSONLD:      blocks,
		Body:        body.String(),
	})
}

// handleSSRCity renders a city landing page.
func (s *Server) handleSSRCity(w http.ResponseWriter, r *http.Request) {
	city, err := s.deps.Cities.BySlug(r.Context(), r.PathValue("slug"))
	if err != nil || city == nil {
		s.ssr.notFound(w, "We don't have that city yet.")
		return
	}
	path := href("city", city.Slug)
	title := "Businesses in " + city.Name + " | BizVerse"
	desc := fmt.Sprintf("Browse %d verified businesses in %s. Compare hours, products and reviews.",
		city.Count, city.Name)
	canonical := s.ssr.absURL(path)

	var body strings.Builder
	fmt.Fprintf(&body, `<article><h1>Businesses in %s</h1>`, html.EscapeString(city.Name))
	fmt.Fprintf(&body, `<p>%d verified listing%s on BizVerse.</p>`, city.Count, plural(city.Count))

	// The city page groups by category; those sublists are genuinely useful
	// crawlable content and cheap to emit.
	if len(city.Categories) > 0 {
		body.WriteString(`<section class="bv-cat-list"><h2>Categories</h2><ul>`)
		for _, c := range city.Categories {
			catURL := s.ssr.absURL("/discover") + "?category=" + escapeQueryValue(c.CategorySlug)
			fmt.Fprintf(&body, `<li><a href="%s">%s</a> (%d)</li>`,
				html.EscapeString(catURL), html.EscapeString(c.CategoryName), c.Count)
		}
		body.WriteString(`</ul></section>`)
	}
	if len(city.TopRated) > 0 {
		body.WriteString(`<section class="bv-top"><h2>Top rated</h2><ul>`)
		for _, b := range city.TopRated {
			if b == nil {
				continue
			}
			fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`,
				html.EscapeString(s.ssr.absURL(href("b", b.Slug))), html.EscapeString(b.Name))
		}
		body.WriteString(`</ul></section>`)
	}

	fmt.Fprintf(&body, `<p class="bv-cta"><a href="%s">Open the full directory for %s</a></p>`,
		html.EscapeString(canonical), html.EscapeString(city.Name))
	body.WriteString(`</article>`)

	crumbs := [][2]string{
		{s.ssr.absURL("/"), "BizVerse"},
		{s.ssr.absURL("/discover"), "Discover"},
		{canonical, city.Name},
	}
	items := make([]map[string]any, 0, len(crumbs))
	for i, c := range crumbs {
		items = append(items, map[string]any{"@type": "ListItem", "position": i + 1, "name": c[1], "item": c[0]})
	}

	s.ssr.render(w, ssrPage{
		Title:       title,
		Description: desc,
		Canonical:   canonical,
		Breadcrumb:  crumbs,
		JSONLD: []map[string]any{
			{
				"@context": "https://schema.org", "@type": "CollectionPage",
				"name": title, "url": canonical, "description": desc,
				"isPartOf": map[string]any{"@type": "WebSite", "name": "BizVerse", "url": s.ssr.absURL("/")},
			},
			{"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": items},
		},
		Body: body.String(),
	})
}

// handleSSRCategory renders a category landing page.
func (s *Server) handleSSRCategory(w http.ResponseWriter, r *http.Request) {
	cat, children, err := s.deps.Categories.GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || cat == nil {
		s.ssr.notFound(w, "We don't have that category yet.")
		return
	}
	path := href("c", cat.Slug)
	title := cat.Name + " near you | BizVerse"
	desc := firstNonEmpty(derefS(cat.Description),
		"Browse verified "+strings.ToLower(cat.Name)+" listings, compare hours and reviews, and get in touch.")
	canonical := s.ssr.absURL(path)

	var body strings.Builder
	fmt.Fprintf(&body, `<article><h1>%s</h1>`, html.EscapeString(cat.Name))
	if cat.Description != nil {
		fmt.Fprintf(&body, `<p>%s</p>`, html.EscapeString(*cat.Description))
	}
	if cat.Count > 0 {
		fmt.Fprintf(&body, `<p>%d verified listing%s.</p>`, cat.Count, plural(cat.Count))
	}
	// A category's child taxonomy is crawlable content in its own right.
	for _, child := range children {
		if child == nil {
			continue
		}
		fmt.Fprintf(&body, `<p><a href="%s">%s</a></p>`,
			html.EscapeString(s.ssr.absURL(href("c", child.Slug))), html.EscapeString(child.Name))
	}
	fmt.Fprintf(&body, `<p class="bv-cta"><a href="%s">See all %s businesses</a></p>`,
		html.EscapeString(canonical), html.EscapeString(strings.ToLower(cat.Name)))
	body.WriteString(`</article>`)

	crumbs := [][2]string{
		{s.ssr.absURL("/"), "BizVerse"},
		{s.ssr.absURL("/categories"), "Categories"},
		{canonical, cat.Name},
	}
	items := make([]map[string]any, 0, len(crumbs))
	for i, c := range crumbs {
		items = append(items, map[string]any{"@type": "ListItem", "position": i + 1, "name": c[1], "item": c[0]})
	}

	s.ssr.render(w, ssrPage{
		Title:       title,
		Description: desc,
		Canonical:   canonical,
		Breadcrumb:  crumbs,
		JSONLD: []map[string]any{
			{
				"@context": "https://schema.org", "@type": "CollectionPage",
				"name": title, "url": canonical, "description": desc,
			},
			{"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": items},
		},
		Body: body.String(),
	})
}

// notFound renders a real 404 document with noindex. A JSON error body here
// would be useless to a crawler and risks a soft-404 being indexed.
func (r *ssrRenderer) notFound(w http.ResponseWriter, message string) {
	r.render(w, ssrPage{
		Title:       "Not found | BizVerse",
		Description: message,
		Robots:      "noindex,follow",
		Body: fmt.Sprintf(`<article><h1>Not found</h1><p>%s</p><p><a href="/">Back to BizVerse</a></p></article>`,
			html.EscapeString(message)),
		Status: http.StatusNotFound,
	})
}

// ---- content helpers ----

var ssrDayOrder = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var ssrDayNames = map[string]string{
	"mon": "Monday", "tue": "Tuesday", "wed": "Wednesday", "thu": "Thursday",
	"fri": "Friday", "sat": "Saturday", "sun": "Sunday",
}

func ssrDayName(day string) string {
	if n, ok := ssrDayNames[day]; ok {
		return n
	}
	return day
}

// ssrHours reads the untyped `hours` jsonb. Values are map[string]any, so every
// field is asserted rather than assumed — a malformed entry is skipped instead
// of panicking or rendering "Closed" for a day that is actually open.
func ssrHours(b *domain.Business) string {
	var rows strings.Builder
	for _, day := range ssrDayOrder {
		raw, ok := b.Hours[day]
		if !ok || raw == nil {
			continue
		}
		h, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		when := "Closed"
		if closed, _ := h["closed"].(bool); !closed {
			open, _ := h["open"].(string)
			close, _ := h["close"].(string)
			if open != "" || close != "" {
				when = strings.TrimSpace(open + " – " + close)
			}
		}
		fmt.Fprintf(&rows, `<div><dt>%s</dt><dd>%s</dd></div>`, html.EscapeString(ssrDayName(day)), html.EscapeString(when))
	}
	return rows.String()
}

func ssrContact(b *domain.Business) string {
	return ssrContactHTML(b.Contact, b.Address)
}

// ssrContactHTML renders the contact block from untyped contact jsonb.
//
// The website is only emitted when it is an absolute https URL. validateContact
// does not constrain the scheme yet (a known gap in the audit), so a
// javascript: or data: value stored before that validation existed must never
// reach an href here. Values are also HTML-escaped on the way out, so a
// quote-breakout cannot occur either.
func ssrContactHTML(contact map[string]any, address string) string {
	var out strings.Builder
	if strings.TrimSpace(address) != "" {
		fmt.Fprintf(&out, `<p class="bv-address">%s</p>`, html.EscapeString(address))
	}
	if ph := contactString(contact, "phone"); ph != "" {
		fmt.Fprintf(&out, `<p>Phone: %s</p>`, html.EscapeString(ph))
	}
	if em := contactString(contact, "email"); em != "" {
		fmt.Fprintf(&out, `<p>Email: %s</p>`, html.EscapeString(em))
	}
	if web := strings.TrimSpace(contactString(contact, "website")); web != "" {
		if strings.HasPrefix(web, "https://") {
			fmt.Fprintf(&out, `<p><a rel="nofollow noopener" href="%s">Website</a></p>`, html.EscapeString(web))
		}
	}
	return out.String()
}

func ssrOpeningHours(b *domain.Business) []map[string]any {
	var out []map[string]any
	for _, day := range ssrDayOrder {
		raw, ok := b.Hours[day]
		if !ok || raw == nil {
			continue
		}
		h, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if closed, _ := h["closed"].(bool); closed {
			continue
		}
		open, _ := h["open"].(string)
		close, _ := h["close"].(string)
		if open == "" || close == "" {
			continue
		}
		out = append(out, map[string]any{
			"@type":     "OpeningHoursSpecification",
			"dayOfWeek": "https://schema.org/" + ssrDayName(day),
			"opens":     open,
			"closes":    close,
		})
	}
	return out
}

// ssrPriceRange maps the 1-4 price level to the conventional "$$" notation.
func ssrPriceRange(level *int) string {
	if level == nil || *level < 1 || *level > 4 {
		return ""
	}
	return strings.Repeat("$", *level)
}

// ssrSameAs forwards only absolute https URLs. A javascript: or data: value
// stored in contact must never reach JSON-LD.
func ssrSameAs(contact map[string]any) []string {
	var out []string
	for _, key := range []string{"website"} {
		if v := strings.TrimSpace(contactString(contact, key)); strings.HasPrefix(v, "https://") {
			out = append(out, v)
		}
	}
	return out
}

func contactString(contact map[string]any, key string) string {
	if contact == nil {
		return ""
	}
	s, _ := contact[key].(string)
	return strings.TrimSpace(s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// stripHTML removes tags. Descriptions are validated as plain text on write, but
// a value that predates that validation could contain markup, and a meta
// description is not a place for HTML.
var tagStripper = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	s = tagStripper.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// escapeQueryValue percent-encodes a value destined for a query string.
func escapeQueryValue(s string) string { return escapePathSegment(s) }
