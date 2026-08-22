package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/security"
)

// handleWS upgrades to the real-time hub (PRD §5.5.3, ARCHITECTURE §3).
// Access via cookie or ?token=<access_jwt>.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	claims, err := s.authenticate(r)
	if err != nil {
		token := r.URL.Query().Get("token")
		if token == "" {
			fail(w, domain.ErrNotAuthenticated)
			return
		}
		claims, err = security.ParseToken(s.deps.Config.JWTSecret, token, security.TokenAccess)
		if err != nil {
			fail(w, err)
			return
		}
	}
	s.deps.Hub.Serve(w, r, claims.UserID)
}
