package httpapi

import (
	"net/http"
	"strings"

	"bizverse/api/internal/domain"
)

// ---- user profile (PRD §6.1) ----

func (s *Server) handlePublicProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.deps.Profiles.ByUsername(r.Context(), r.PathValue("username"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, profile)
}

// ---- support contact ----

func (s *Server) handleContact(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Subject string `json:"subject"`
		Message string `json:"message"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Profiles.Contact(r.Context(), user.ID, in.Subject, in.Message); err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"sent": true})
}

// ---- appeals ----

func (s *Server) handleAppeal(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Profiles.Appeal(r.Context(), user.ID, in.Reason); err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"submitted": true})
}

func (s *Server) handleAdminAppeals(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	list, err := s.deps.Profiles.Appeals(r.Context(), status)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"appeals": list})
}

func (s *Server) handleAdminAppealDecide(w http.ResponseWriter, r *http.Request) {
	admin, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Decision string `json:"decision"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Profiles.DecideAppeal(r.Context(), admin.ID, r.PathValue("appealId"), in.Decision); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"decided": true})
}

// ---- trending anomalies ----

func (s *Server) handleAdminAnomalies(w http.ResponseWriter, r *http.Request) {
	list, err := s.deps.Profiles.Anomalies(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"anomalies": list})
}

func (s *Server) handleAdminAnomalyResolve(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Profiles.ResolveAnomaly(r.Context(), r.PathValue("eventId")); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"resolved": true})
}

// ---- co-owner invites (PRD §5.9.3) ----

func (s *Server) handleInvites(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.PathValue("id")
	if r.Method == http.MethodGet {
		list, err := s.deps.Invites.List(r.Context(), user.ID, businessID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"invites": list})
		return
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Invites.Create(r.Context(), user.ID, businessID, in.Email, in.Role); err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"invited": true})
}

func (s *Server) handleInviteRevoke(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Invites.Revoke(r.Context(), user.ID, r.PathValue("id"), r.PathValue("inviteId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// handleInvitePreview is PUBLIC: powers the invite landing page before the
// visitor decides to sign in. Unknown/expired/revoked/consumed → 404.
func (s *Server) handleInvitePreview(w http.ResponseWriter, r *http.Request) {
	info, err := s.deps.Invites.Preview(r.Context(), r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, info)
}

func (s *Server) handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Invites.Accept(r.Context(), user.ID, r.PathValue("token")); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"accepted": true})
}

// ---- pinned messages ----

func (s *Server) handlePinMessage(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	on := r.Method == http.MethodPut
	if err := s.deps.Chat.PinMessage(r.Context(), user.ID, r.PathValue("id"), parseID(r.PathValue("messageId")), on); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"pinned": on})
}

func (s *Server) handlePinned(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	pinned, err := s.deps.Chat.Pinned(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"pinned_message_ids": pinned})
}

// ---- admin KPIs (PRD §5.8.6) ----

func (s *Server) handleAdminKPIs(w http.ResponseWriter, r *http.Request) {
	kpis, err := s.deps.Invites.KPIs(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, kpis)
}

// ---- user search (mention autocomplete) ----

func (s *Server) handleUserSearch(w http.ResponseWriter, r *http.Request) {
	if _, found := currentUser(r); !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		ok(w, map[string]any{"users": []any{}})
		return
	}
	rows, err := s.deps.Repos.Query(r.Context(), `
		SELECT id, name, username FROM users
		WHERE (username ILIKE $1 || '%' OR name ILIKE '%' || $1 || '%')
		  AND status = 'active' AND deleted_at IS NULL
		ORDER BY username ILIKE $1 || '%' DESC LIMIT 8`, q)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, name, username string
		if err := rows.Scan(&id, &name, &username); err != nil {
			fail(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "username": username})
	}
	ok(w, map[string]any{"users": out})
}
