package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
)

// handleWS upgrades to the real-time hub (PRD §5.5.3, ARCHITECTURE §3).
// Cookie-authenticated only: putting the access JWT in the query string
// leaked live credentials into proxy/access logs and browser history.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	claims, err := s.authenticate(r)
	if err != nil {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	s.deps.Hub.Serve(w, r, claims.UserID)
}
