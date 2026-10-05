package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/service"
)

// ---- claim-a-business (PRD §6.1) ----

func (s *Server) handleClaims(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if r.Method == http.MethodGet {
		limit := parsePositiveInt(r.URL.Query().Get("limit"), 30)
		offset := parseOffset(r.URL.Query().Get("offset"), 0)
		list, err := s.deps.Claims.List(r.Context(), user.ID, false, limit, offset)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"claims": list})
		return
	}
	var in service.ClaimInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	claim, err := s.deps.Claims.Submit(r.Context(), user.ID, in)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"claim": claim})
}

// Admin queue.
func (s *Server) handleAdminClaims(w http.ResponseWriter, r *http.Request) {
	admin, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if r.Method == http.MethodGet {
		limit := parsePositiveInt(r.URL.Query().Get("limit"), 30)
		list, err := s.deps.Claims.List(r.Context(), admin.ID, true, limit, 0)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"claims": list})
		return
	}
	var in struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	claim, err := s.deps.Claims.Decide(r.Context(), admin.ID, r.PathValue("claimId"), in.Decision, in.Note)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"claim": claim})
}
