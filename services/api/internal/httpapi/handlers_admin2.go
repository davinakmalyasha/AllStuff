package httpapi

import (
	"net/http"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/service"
	"bizverse/api/internal/util"
)

// ---- moderation (PRD §5.8.2) ----

func (s *Server) handleAdminReports(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 50)
	offset := parseOffset(r.URL.Query().Get("offset"), 0)
	reports, err := s.deps.Admin.Reports(r.Context(), status, limit, offset)
	if err != nil {
		fail(w, err)
		return
	}
	for _, rep := range reports {
		s.deps.Admin.ReportSnippet(r.Context(), rep)
	}
	ok(w, map[string]any{"reports": reports})
}

func (s *Server) handleAdminReportDecide(w http.ResponseWriter, r *http.Request) {
	admin, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Admin.DecideReport(r.Context(), admin.ID, r.PathValue("reportId"), in.Action, in.Note); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"resolved": true})
}

func (s *Server) handleAdminHide(w http.ResponseWriter, r *http.Request) {
	admin, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	targetType := r.PathValue("type")
	targetID := r.PathValue("id")
	action := "hide"
	if strings.HasSuffix(r.URL.Path, "/restore") {
		action = "restore"
	}
	var err error
	if action == "restore" {
		err = s.deps.Admin.RestoreContent(r.Context(), targetType, targetID)
	} else {
		err = s.deps.Admin.HideContent(r.Context(), admin.ID, targetType, targetID, action)
	}
	if err != nil {
		fail(w, err)
		return
	}
	// audit trail
	_, _ = s.deps.Repos.Exec(r.Context(), `
		INSERT INTO moderation_actions (id, admin_id, action, target_type, target_id, reason)
		VALUES ($1, $2, $3, $4, $5, '')`, util.NewUUID(), admin.ID, action, targetType, targetID)
	ok(w, map[string]any{"done": true})
}

// ---- users (PRD §5.8.4) ----

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		q = "%"
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 30)
	offset := parseOffset(r.URL.Query().Get("offset"), 0)
	users, err := s.deps.Admin.SearchUsers(r.Context(), q, limit, offset)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.ID, "name": u.Name, "email": u.Email, "username": u.Username,
			"status": u.Status, "role": u.Role, "email_verified": u.EmailVerified(),
			"created_at": u.CreatedAt,
		})
	}
	ok(w, map[string]any{"users": out})
}

func (s *Server) handleAdminUserAction(w http.ResponseWriter, r *http.Request) {
	admin, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/users/")
	action = strings.TrimPrefix(action, r.PathValue("userId")+"/")
	var in struct {
		Reason string `json:"reason"`
	}
	_ = decodeBody(w, r, &in)
	if err := s.deps.Admin.UserAction(r.Context(), admin.ID, r.PathValue("userId"), action, in.Reason); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"done": true})
}

// ---- curation (PRD §5.8.5) ----

func (s *Server) handleAdminCuration(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg, err := s.deps.Admin.GetCuration(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, cfg)
		return
	}
	admin, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in service.CurationConfig
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Admin.SetCuration(r.Context(), admin.ID, &in); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"saved": true})
}

// ---- audit trail ----

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 50)
	offset := parseOffset(r.URL.Query().Get("offset"), 0)
	trail, err := s.deps.Admin.AuditTrail(r.Context(), limit, offset)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"actions": trail})
}

// ---- banned words ----

func (s *Server) handleAdminBannedWords(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		words, err := s.deps.Admin.ListBannedWords(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"words": words})
		return
	}
	var in struct {
		Word string `json:"word"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Admin.AddBannedWord(r.Context(), in.Word); err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"added": true})
}

func (s *Server) handleAdminBannedWordDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Admin.RemoveBannedWord(r.Context(), r.PathValue("word")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleAdminAllowlist(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		list, err := s.deps.Admin.Allowlist(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"words": list})
		return
	}
	var in struct {
		Words []string `json:"words"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Admin.SetAllowlist(r.Context(), in.Words); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"saved": true})
}

// ---- site config (announcement) ----

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg, err := s.deps.Admin.GetSiteConfig(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, cfg)
		return
	}
	var in map[string]any
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	for k, v := range in {
		if err := s.deps.Admin.SetSiteConfig(r.Context(), k, v); err != nil {
			fail(w, err)
			return
		}
	}
	ok(w, map[string]any{"saved": true})
}

// ---- admin business suspension/restore ----

func (s *Server) handleAdminBusinessSuspend(w http.ResponseWriter, r *http.Request) {
	admin, found := currentUser(r)
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
	action := "suspend"
	if strings.HasSuffix(r.URL.Path, "/restore") {
		action = "restore"
	}
	if action == "suspend" && strings.TrimSpace(in.Reason) == "" {
		fail(w, domain.ErrValidation.WithField("reason", "A reason is required."))
		return
	}
	// Restore returns the business to its pre-suspension status instead of
	// forcing 'verified' (a suspended draft must not come back verified).
	restored := "verified"
	if action == "restore" {
		var prev string
		err := s.deps.Repos.QueryRow(r.Context(), `
			SELECT coalesce(pre_suspend_status, '') FROM businesses WHERE id = $1`,
			r.PathValue("id")).Scan(&prev)
		switch {
		case err == nil && prev != "":
			restored = prev
		case err != nil:
			fail(w, err)
			return
		}
	}
	tag, err := s.deps.Repos.Exec(r.Context(), `
		UPDATE businesses SET
			status = $2,
			pre_suspend_status = CASE WHEN $3 = 'suspend' THEN status ELSE pre_suspend_status END,
			updated_at = now()
		WHERE id = $1`,
		r.PathValue("id"), map[string]string{"suspend": "suspended", "restore": restored}[action], action)
	if err != nil {
		fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, domain.ErrNotFound)
		return
	}
	_, _ = s.deps.Repos.Exec(r.Context(), `
		INSERT INTO moderation_actions (id, admin_id, action, target_type, target_id, reason)
		VALUES ($1, $2, $3, 'business', $4, $5)`,
		util.NewUUID(), admin.ID, action, r.PathValue("id"), in.Reason)
	// Notify the owner.
	var ownerID string
	_ = s.deps.Repos.QueryRow(r.Context(),
		`SELECT owner_id FROM businesses WHERE id = $1`, r.PathValue("id")).Scan(&ownerID)
	if ownerID != "" {
		ntype := "business_suspended"
		payload := map[string]any{"business_id": r.PathValue("id"), "reason": in.Reason}
		if action == "restore" {
			ntype = "business_restored"
		}
		s.deps.Notifier.Create(r.Context(), ownerID, ntype, payload)
	}
	ok(w, map[string]any{"done": action})
}

// Public meta: announcement banner + rates staleness (PRD §5.8.5, D5).
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.deps.Admin.GetSiteConfig(r.Context())
	var updatedAt time.Time
	_ = s.deps.Repos.QueryRow(r.Context(), `
		SELECT coalesce(max(fetched_at), to_timestamp(0)) FROM currency_rates`).Scan(&updatedAt)
	ok(w, map[string]any{"site": cfg, "rates": map[string]any{"updated_at": updatedAt}})
}
