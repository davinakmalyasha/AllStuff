package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"

	"bizverse/api/internal/domain"
)

const oauthStateCookie = "bv_oauth_state"

// handleVapidKey exposes the public VAPID key for client push subscriptions.
func (s *Server) handleVapidKey(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config.VAPID == nil {
		fail(w, domain.ErrValidation.WithField("_", "Push is not configured."))
		return
	}
	ok(w, map[string]any{"public_key": s.deps.Config.VAPID.PublicKey})
}

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if !s.deps.OAuth.Enabled() {
		fail(w, domain.ErrValidation.WithField("_", "Google sign-in is not configured."))
		return
	}
	state := make([]byte, 24)
	if _, err := rand.Read(state); err != nil {
		fail(w, err)
		return
	}
	stateStr := base64.RawURLEncoding.EncodeToString(state)
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: stateStr, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 600, Secure: s.deps.Config.CookieSecure,
	})
	http.Redirect(w, r, s.deps.OAuth.AuthURL(stateStr), http.StatusFound)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !s.deps.OAuth.Enabled() {
		fail(w, domain.ErrValidation.WithField("_", "Google sign-in is not configured."))
		return
	}
	ck, err := r.Cookie(oauthStateCookie)
	if err != nil || ck.Value == "" || ck.Value != r.URL.Query().Get("state") {
		http.Redirect(w, r, "/login?error=oauth_state", http.StatusFound)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/login?error=oauth_denied", http.StatusFound)
		return
	}
	user, err := s.deps.OAuth.Exchange(r.Context(), code)
	if err != nil {
		http.Redirect(w, r, "/login?error=oauth_failed", http.StatusFound)
		return
	}
	tokens, err := s.deps.Auth.IssueSessionForUser(r.Context(), user, r.UserAgent())
	if err != nil {
		http.Redirect(w, r, "/login?error=oauth_failed", http.StatusFound)
		return
	}
	s.setSessionCookies(w, tokens.AccessToken, tokens.RefreshToken, tokens.CSRFToken)
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/me", http.StatusFound)
}
