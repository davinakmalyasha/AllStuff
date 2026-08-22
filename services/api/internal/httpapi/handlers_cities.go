package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
)

// ---- city landing pages (SEO, PRD §5.1.5) ----

func (s *Server) handleCityPage(w http.ResponseWriter, r *http.Request) {
	page, err := s.deps.Cities.BySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"city": page})
}

func (s *Server) handleCitiesIndex(w http.ResponseWriter, r *http.Request) {
	list, err := s.deps.Cities.All(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"cities": list})
}

var _ = domain.ErrNotFound
