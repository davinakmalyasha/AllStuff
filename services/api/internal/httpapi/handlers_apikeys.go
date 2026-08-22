package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
)

// ---- public API key management (PRD §9.6) ----

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.deps.APIKeys.List(r.Context(), user.ID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"keys": list})
	case http.MethodPost:
		var in struct {
			Name string `json:"name"`
		}
		if err := decodeBody(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		key, raw, err := s.deps.APIKeys.Create(r.Context(), user.ID, in.Name)
		if err != nil {
			fail(w, err)
			return
		}
		// The raw value is returned exactly once.
		created(w, map[string]any{"key": key, "raw_key": raw})
	default:
		fail(w, domain.ErrValidation.WithField("_", "Method not allowed."))
	}
}

func (s *Server) handleAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.APIKeys.Revoke(r.Context(), user.ID, r.PathValue("keyId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}
