package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"

	"bizverse/api/internal/domain"
)

const oauthStateCookie = "bv_oauth_state"

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
	// The state cookie is cleared on every exit from here, not only the success
	// path. It previously survived all four failure redirects, so a stale state
	// cookie outlived the flow it belonged to.
	defer http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.deps.Config.CookieSecure,
	})

	tokens, err := s.deps.Auth.IssueSessionForUser(r.Context(), user, r.UserAgent())
	if err != nil {
		// A 2FA challenge is a SUCCESSFUL first factor. Treating it as a failure
		// is what made this a bypass: the account has TOTP enrolled, Google
		// vouched for the identity, and the only thing left is the code - so the
		// user must be routed to the 2FA screen carrying the challenge, exactly
		// as the password path does.
		//
		// The challenge goes in the URL FRAGMENT, never the query string. The
		// fragment is not sent to the server, so it does not land in access logs,
		// Referer headers on outbound navigation, or proxy history - which is the
		// same class of leak the WebSocket JWT-in-query fix closed.
		if domain.FromError(err) == domain.Err2FARequired && tokens != nil && tokens.RefreshToken != "" {
			http.Redirect(w, r, "/2fa#challenge="+url.QueryEscape(tokens.RefreshToken), http.StatusFound)
			return
		}
		http.Redirect(w, r, "/login?error=oauth_failed", http.StatusFound)
		return
	}
	s.setSessionCookies(w, tokens.AccessToken, tokens.RefreshToken, tokens.CSRFToken)
	http.Redirect(w, r, "/me", http.StatusFound)
}
