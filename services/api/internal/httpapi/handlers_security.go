package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
)

// ---- 2FA (PRD §5.9.1) ----

func (s *Server) handle2FAStatus(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	status, err := s.deps.Auth.TFAStatus(r.Context(), user.ID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, status)
}

func (s *Server) handle2FAEnroll(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	secret, otpauth, err := s.deps.Auth.Enroll2FA(r.Context(), user.ID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"secret": secret, "otpauth_url": otpauth})
}

func (s *Server) handle2FAConfirm(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	codes, err := s.deps.Auth.Confirm2FA(r.Context(), user.ID, in.Code)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"enabled": true, "recovery_codes": codes})
}

func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Auth.Disable2FA(r.Context(), user.ID, in.Code); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handle2FAVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	user, tokens, err := s.deps.Auth.Verify2FA(r.Context(), in.Challenge, in.Code, nil, r.UserAgent())
	if err != nil {
		fail(w, err)
		return
	}
	s.setSessionCookies(w, tokens.AccessToken, tokens.RefreshToken, tokens.CSRFToken)
	ok(w, s.publicUser(user))
}

// ---- sessions & security (PRD §5.9.2) ----

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	sessions, err := s.deps.Auth.Sessions(r.Context(), user.ID)
	if err != nil {
		fail(w, err)
		return
	}
	history, err := s.deps.Auth.LoginHistory(r.Context(), user.ID, 20)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"sessions": sessions, "history": history})
}

func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Auth.RevokeSession(r.Context(), user.ID, r.PathValue("sessionId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleSessionRevokeOthers(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	ck, err := r.Cookie(cookieRefresh)
	if err != nil {
		fail(w, domain.ErrSessionInvalid)
		return
	}
	sess, err := s.deps.Repos.Sessions.GetByTokenHash(r.Context(), hashToken(ck.Value))
	if err != nil || sess == nil {
		fail(w, domain.ErrSessionInvalid)
		return
	}
	if err := s.deps.Auth.RevokeOthers(r.Context(), user.ID, sess.ID); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// ---- notification preferences (PRD §5.7) ----

func (s *Server) handleNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if r.Method == http.MethodGet {
		prefs, err := s.deps.Auth.NotificationPrefs(r.Context(), user.ID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, prefs)
		return
	}
	var in map[string]any
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Auth.SetNotificationPrefs(r.Context(), user.ID, in); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"saved": true})
}

// ---- data export (PRD §5.9.2) ----

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	data, err := s.deps.Auth.ExportData(r.Context(), user.ID)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="bizverse-export-`+user.Username+`.json"`)
	writeJSON(w, http.StatusOK, data)
}

// ---- password / email / recovery (identity-standard) ----

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Auth.ChangePassword(r.Context(), user.ID, in.CurrentPassword, in.NewPassword); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleChangeEmail(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Auth.ChangeEmail(r.Context(), user.ID, in.Password, in.Email); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"changed": true, "verify_required": true})
}

func (s *Server) handleRegenerateRecovery(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	codes, err := s.deps.Auth.RegenerateRecoveryCodes(r.Context(), user.ID, in.Code)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"recovery_codes": codes})
}

// ---- saved searches (search alerts) ----

func (s *Server) handleSavedSearches(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if r.Method == http.MethodGet {
		list, err := s.deps.Auth.ListSavedSearches(r.Context(), user.ID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"searches": list})
		return
	}
	var in struct {
		Name        string         `json:"name"`
		Query       map[string]any `json:"query"`
		NotifyDaily bool           `json:"notify_daily"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	saved, err := s.deps.Auth.SaveSearch(r.Context(), user.ID, in.Name, in.Query, in.NotifyDaily)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"search": saved})
}

func (s *Server) handleSavedSearchPatch(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		NotifyDaily *bool `json:"notify_daily"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.NotifyDaily == nil {
		fail(w, domain.ErrValidation.WithField("notify_daily", "Required."))
		return
	}
	if err := s.deps.Auth.SetSearchAlert(r.Context(), user.ID, r.PathValue("searchId"), *in.NotifyDaily); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleSavedSearchDelete(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Auth.DeleteSavedSearch(r.Context(), user.ID, r.PathValue("searchId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// ---- public collections ----

func (s *Server) handlePublicCollection(w http.ResponseWriter, r *http.Request) {
	var col struct {
		ID     string
		UserID string
		Name   string
		Slug   string
	}
	err := s.deps.Repos.QueryRow(r.Context(), `
		SELECT id, user_id, name, slug FROM collections
		WHERE id = $1 AND is_public = true AND deleted_at IS NULL`, r.PathValue("id")).
		Scan(&col.ID, &col.UserID, &col.Name, &col.Slug)
	if err != nil {
		fail(w, domain.ErrNotFound)
		return
	}
	items, err := s.deps.Repos.Engagement.ListItems(r.Context(), col.ID)
	if err != nil {
		fail(w, err)
		return
	}
	owner, _ := s.deps.Repos.Users.GetByID(r.Context(), col.UserID)
	ok(w, map[string]any{
		"collection": col, "items": items,
		"owner": map[string]any{"name": owner.Name, "username": owner.Username},
	})
}

// ---- client-side error reporting (Sentry-style) ----

func (s *Server) handleClientError(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message string `json:"message"`
		Stack   string `json:"stack"`
		URL     string `json:"url"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Message == "" {
		fail(w, domain.ErrValidation.WithField("message", "Message is required."))
		return
	}
	_, err := s.deps.Repos.Exec(r.Context(), `
		INSERT INTO reports (id, reporter_id, target_type, target_id, reason, evidence)
		VALUES ($1, $2, 'error', 'client', $3, $4)`,
		newUUID(), "", in.Message, map[string]any{"stack": in.Stack, "url": in.URL})
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"reported": true})
}

// ---- account deletion (PRD §5.9.2) ----
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Auth.RequestDeletion(r.Context(), user.ID, in.Password); err != nil {
		fail(w, err)
		return
	}
	s.clearSessionCookies(w)
	noContent(w)
}

func (s *Server) handleCancelDeletion(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Auth.CancelDeletion(r.Context(), user.ID); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}
