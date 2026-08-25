package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"bizverse/api/internal/domain"
)

// handleOGImage serves an SVG share card for a business (B4).
func (s *Server) handleOGImage(w http.ResponseWriter, r *http.Request) {
	b, err := s.deps.Businesses.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil {
		fail(w, err)
		return
	}
	if b.Status != domain.BusinessVerified {
		fail(w, domain.ErrNotFound)
		return
	}
	name := truncate(b.Name, 34)
	tagline := truncate(derefS(b.Tagline), 56)
	city := b.City + ", " + b.Country
	rating := ""
	if b.ReviewCount > 0 {
		rating = fmt.Sprintf("★ %.1f (%d reviews)", *b.RatingAvg, b.ReviewCount)
	}
	logo := ""
	if b.LogoURL != nil && safeMediaRef(*b.LogoURL) {
		logo = fmt.Sprintf(`<image x="440" y="40" width="120" height="120" href="%s"/>`, xmlEscape(*b.LogoURL))
	}

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="630" viewBox="0 0 1200 630">
  <rect width="1200" height="630" fill="#0a0a0a"/>
  <rect x="40" y="40" width="1120" height="550" rx="24" fill="#ffffff"/>
  %s
  <text x="80" y="180" font-family="Inter, Arial, sans-serif" font-size="64" font-weight="700" fill="#0a0a0a">%s</text>
  <text x="80" y="240" font-family="Inter, Arial, sans-serif" font-size="30" fill="#52525b">%s</text>
  <text x="80" y="300" font-family="Inter, Arial, sans-serif" font-size="30" fill="#52525b">%s</text>
  <text x="80" y="360" font-family="Inter, Arial, sans-serif" font-size="26" fill="#a1a1aa">%s · %s</text>
  <rect x="80" y="420" width="200" height="48" rx="24" fill="#0a0a0a"/>
  <text x="110" y="453" font-family="Inter, Arial, sans-serif" font-size="22" fill="#ffffff">BizVerse</text>
</svg>`, logo, xmlEscape(name), xmlEscape(tagline), xmlEscape(rating), xmlEscape(city), xmlEscape(b.CategoryName))

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	// SVG executes scripts when navigated to directly — lock it down so an
	// injected payload (or a future escaping bug) can never touch the API
	// origin or read cookies.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(svg))
}

// safeMediaRef allows only internal media references and plain https images
// in owner-controlled URL fields rendered inside SVG/HTML surfaces.
func safeMediaRef(u string) bool {
	pu, err := url.Parse(strings.TrimSpace(u))
	if err != nil || pu.Host != "" || pu.Scheme != "" {
		// Not a relative path: allow absolute https images only.
		if err != nil || pu.Scheme != "https" || pu.Host == "" {
			return false
		}
		return true
	}
	return strings.HasPrefix(pu.Path, "/api/v1/media/") && !strings.Contains(u, "..")
}

func derefS(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}
