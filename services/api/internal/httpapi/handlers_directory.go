package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/service"
)

// ---- public directory ----

func (s *Server) handleCategoriesTree(w http.ResponseWriter, r *http.Request) {
	tree, err := s.deps.Categories.Tree(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"categories": tree})
}

func (s *Server) handleCategoryPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	cat, path, err := s.deps.Categories.GetBySlug(r.Context(), slug)
	if err != nil {
		fail(w, err)
		return
	}
	// Category leaderboard (PRD §5.6.3): score-ranked + guaranteed Rising slots.
	top, updatedAt, err := s.deps.Trending.CategoryLeaderboard(r.Context(), cat.ID, 5)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{
		"category":    cat,
		"breadcrumbs": path,
		"leaderboard": top,
		"updated_at":  updatedAt,
	})
}

func (s *Server) handleBusinessPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	b, err := s.deps.Businesses.GetPublic(r.Context(), slug)
	if err != nil {
		fail(w, err)
		return
	}
	// Preview as guest: the owner can see their draft theme/layout (PRD §5.4.2).
	isOwner := false
	if user, found := currentUser(r); found {
		isOwner = b.OwnerID == user.ID
	}
	if r.URL.Query().Get("draft") == "1" && isOwner {
		// Overlay the draft onto the payload as `preview` — the public page
		// still renders from the published snapshot unless told otherwise.
		draft := map[string]any{}
		_ = s.deps.Repos.QueryRow(r.Context(), `
			SELECT jsonb_build_object('theme', theme, 'layout', layout) FROM businesses WHERE id = $1`,
			b.ID).Scan(&draft)
		ok(w, map[string]any{"business": b, "preview": draft, "similar": []*domain.Business{}, "products": []*domain.Product{}, "is_owner": true})
		return
	}
	// Similar businesses: same category, exclude self (PRD §5.3.6).
	similar, _, err := s.deps.Search.Businesses(r.Context(), service.SearchParams{
		CategoryIDs: []string{b.CategoryID},
		Sort:        "rating",
		Limit:       6,
	})
	if err != nil {
		fail(w, err)
		return
	}
	filtered := similar[:0]
	for _, sb := range similar {
		if sb.ID != b.ID {
			filtered = append(filtered, sb)
		}
	}
	// Published products (PRD §5.3.3).
	products, err := s.deps.Products.ListPublished(r.Context(), b.ID)
	if err != nil {
		fail(w, err)
		return
	}
	// View event (deduped per user/day, PRD §5.6.3).
	if user, found := currentUser(r); found {
		isOwner = b.OwnerID == user.ID
		if !isOwner {
			key := user.ID + ":business:" + b.ID + ":view:" + time.Now().Format("2006-01-02")
			_, _ = s.deps.Repos.Engagement.InsertEvent(r.Context(), user.ID, "business", b.ID, "view", 1, key)
		}
	}
	// Trend flags from the latest 24h snapshot (PRD §5.3.1 badges).
	var booming, rising bool
	_ = s.deps.Repos.QueryRow(r.Context(), `
		SELECT coalesce(is_booming, false), coalesce(is_rising, false)
		FROM trend_snapshots WHERE business_id = $1 AND period = '24h'
		ORDER BY taken_at DESC LIMIT 1`, b.ID).Scan(&booming, &rising)
	ok(w, map[string]any{
		"business": b, "similar": filtered, "products": products, "is_owner": isOwner,
		"trend": map[string]any{"is_booming": booming, "is_rising": rising},
	})
}

// ---- search & suggest ----

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := service.SearchParams{
		Q:                 q.Get("q"),
		City:              q.Get("city"),
		Sort:              q.Get("sort"),
		MinRating:         parseFloatDefault(q.Get("min_rating"), 0),
		OpenNow:           q.Get("open_now") == "true",
		VerifiedOnly:      q.Get("verified_only") == "true",
		FullyVerifiedOnly: q.Get("fully_verified_only") == "true",
		HasChat:           q.Get("has_chat") == "true",
		Limit:             parsePositiveInt(q.Get("limit"), 24),
		Offset:            parsePositiveInt(q.Get("offset"), 0),
	}
	if cats := q["category"]; len(cats) > 0 {
		p.CategoryIDs = cats
	}
	if lat, ok := parseFloatPtr(q.Get("lat")); ok {
		p.Lat = lat
	}
	if lng, ok := parseFloatPtr(q.Get("lng")); ok {
		p.Lng = lng
	}
	// Viewport bounding box: bbox=minLng,minLat,maxLng,maxLat (PRD §5.2).
	if bbox := q.Get("bbox"); bbox != "" {
		parts := strings.Split(bbox, ",")
		if len(parts) == 4 {
			vals := make([]*float64, 4)
			ok := true
			for i, part := range parts {
				v, err := strconv.ParseFloat(part, 64)
				if err != nil {
					ok = false
					break
				}
				vals[i] = &v
			}
			if ok {
				p.MinLng, p.MinLat, p.MaxLng, p.MaxLat = vals[0], vals[1], vals[2], vals[3]
			}
		}
	}
	p.RadiusKM = parseFloatDefault(q.Get("radius_km"), 10)
	if p.RadiusKM <= 0 || p.RadiusKM > 200 {
		p.RadiusKM = 10
	}
	if p.Lat == nil || p.Lng == nil {
		p.RadiusKM = 0
	}
	for _, pl := range q["price_level"] {
		if n, err := strconv.Atoi(pl); err == nil && n >= 1 && n <= 4 {
			p.PriceLevels = append(p.PriceLevels, n)
		}
	}
	results, total, err := s.deps.Search.Businesses(r.Context(), p)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"businesses": results, "count": total})
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		ok(w, map[string]any{"businesses": []any{}, "categories": []any{}})
		return
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 6)
	out, err := s.deps.Search.Suggestions(r.Context(), q, limit)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, out)
}

// ---- sitemap (PRD §9.4) ----

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	ids := strings.Split(r.URL.Query().Get("b"), ",")
	if len(ids) < 2 || len(ids) > 4 {
		fail(w, domain.ErrValidation.WithField("b", "Compare 2–4 business ids (?b=id,id,id)."))
		return
	}
	var businesses []*domain.Business
	for _, id := range ids {
		if id == "" {
			continue
		}
		b, err := s.deps.Repos.Businesses.GetByID(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		if b == nil || (b.Status != domain.BusinessVerified && b.Status != domain.BusinessPaused) {
			fail(w, domain.ErrNotFound)
			return
		}
		businesses = append(businesses, b)
	}
	if len(businesses) < 2 {
		fail(w, domain.ErrValidation.WithField("b", "At least 2 valid businesses are required."))
		return
	}
	// Top-3 published products per column (PRD §5.1.5 compare rows).
	topProducts := map[string][]*domain.Product{}
	for _, b := range businesses {
		list, err := s.deps.Products.ListPublished(r.Context(), b.ID)
		if err == nil && len(list) > 0 {
			if len(list) > 3 {
				list = list[:3]
			}
			topProducts[b.ID] = list
		}
	}
	ok(w, map[string]any{"businesses": businesses, "top_products": topProducts})
}

func (s *Server) handleFeatured(w http.ResponseWriter, r *http.Request) {
	featured, err := s.deps.Repos.Businesses.ListFeatured(r.Context(), 8)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"businesses": featured})
}

func (s *Server) handleRates(w http.ResponseWriter, r *http.Request) {
	rates, updated, err := s.deps.Currency.Rates(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"base": "USD", "rates": rates, "updated_at": updated})
}

func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	base := s.deps.Config.PublicURL
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	sb.WriteString(`  <url><loc>` + base + `/</loc></url>` + "\n")

	tree, err := s.deps.Categories.Tree(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	var walk func(cats []*domain.Category)
	walk = func(cats []*domain.Category) {
		for _, c := range cats {
			sb.WriteString(`  <url><loc>` + base + `/c/` + c.Slug + `</loc></url>` + "\n")
			walk(c.Children)
		}
	}
	walk(tree)

	// City landing pages (SEO).
	cities, err := s.deps.Cities.All(r.Context())
	if err == nil {
		for _, c := range cities {
			if slug, ok := c["slug"].(string); ok {
				sb.WriteString(`  <url><loc>` + base + `/city/` + slug + `</loc></url>` + "\n")
			}
		}
	}

	// All public business pages.
	biz, err := s.deps.Repos.Businesses.ByStatus(r.Context(), []string{"verified"}, 100000, 0)
	if err != nil {
		fail(w, err)
		return
	}
	for _, b := range biz {
		sb.WriteString(`  <url><loc>` + base + `/b/` + b.Slug + `</loc></url>` + "\n")
	}
	sb.WriteString(`</urlset>`)

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(sb.String()))
}

// ---- helpers ----

func parseFloatDefault(s string, def float64) float64 {
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	return def
}

func parseFloatPtr(s string) (*float64, bool) {
	if s == "" {
		return nil, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, false
	}
	return &v, true
}

// parsePositiveInt parses a positive int with a hard ceiling so absurd
// values never reach SQL LIMIT clauses.
func parsePositiveInt(s string, def int) int {
	return parsePositiveIntMax(s, def, 100)
}

func parsePositiveIntMax(s string, def, max int) int {
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		if v > max {
			return max
		}
		return v
	}
	return def
}
