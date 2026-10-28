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
		// This endpoint is only ever hit by browser navigation (the Google
		// button links straight at it), so a JSON 400 would render as a dead
		// error page. Redirect to the login page with the reason instead, the
		// same way every callback failure already behaves.
		http.Redirect(w, r, "/login?error=oauth_disabled", http.StatusFound)
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
	// Every redirect out of the callback expires the state cookie in the SAME
	// response. A deferred SetCookie cannot do this: http.Redirect writes the
	// header before returning, so a cookie added afterwards is discarded. The
	// cookie used to survive every failure redirect despite a comment claiming
	// otherwise; putting the clear in front of each WriteHeader fixes the
	// ordering rather than restating the intent.
	redirect := func(target string) {
		http.SetCookie(w, &http.Cookie{
			Name: oauthStateCookie, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.deps.Config.CookieSecure,
		})
		http.Redirect(w, r, target, http.StatusFound)
	}
	ck, err := r.Cookie(oauthStateCookie)
	if err != nil || ck.Value == "" || ck.Value != r.URL.Query().Get("state") {
		redirect("/login?error=oauth_state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		redirect("/login?error=oauth_denied")
		return
	}
	user, err := s.deps.OAuth.Exchange(r.Context(), code)
	if err != nil {
		redirect("/login?error=oauth_failed")
		return
	}
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
			redirect("/2fa#challenge=" + url.QueryEscape(tokens.RefreshToken))
			return
		}
		redirect("/login?error=oauth_failed")
		return
	}
	s.setSessionCookies(w, tokens.AccessToken, tokens.RefreshToken, tokens.CSRFToken)
	redirect("/me")
}
