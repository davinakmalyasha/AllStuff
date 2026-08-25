package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/security"
	"bizverse/api/internal/service"
)

// accountThrottle caps authentication attempts per target ACCOUNT (10 per
// 15 min). The per-IP limits alone let a modest IP pool run unlimited
// guesses against a single victim — including the 10^6 TOTP code space.
func (s *Server) accountThrottle(kind, key string) bool {
	_, _, ok := s.deps.RateLimiter.Allow("acct:"+kind+":"+key, 10, 15*time.Minute)
	return !ok
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var in service.RegisterInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	user, tokens, err := s.deps.Auth.Register(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	s.setSessionCookies(w, tokens.AccessToken, tokens.RefreshToken, tokens.CSRFToken)
	created(w, s.publicUser(user))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in service.LoginInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if s.accountThrottle("login", strings.ToLower(strings.TrimSpace(in.Email))) {
		s.metrics.RateLimited()
		w.Header().Set("Retry-After", "900")
		fail(w, domain.ErrRateLimited)
		return
	}
	user, tokens, err := s.deps.Auth.Login(r.Context(), in, clientIPValue(s.clientIP(r)), r.UserAgent())
	if err != nil {
		if de := domain.FromError(err); de == domain.Err2FARequired && tokens != nil && tokens.RefreshToken != "" {
			// 2FA gate: hand the challenge token to the client (PRD §5.9.1).
			ok(w, map[string]any{"2fa_required": true, "challenge": tokens.RefreshToken, "user": s.publicUser(user)})
			return
		}
		fail(w, err)
		return
	}
	s.setSessionCookies(w, tokens.AccessToken, tokens.RefreshToken, tokens.CSRFToken)
	ok(w, s.publicUser(user))
}

// handleRestore signs back in an account that is inside its deletion grace
// period (PRD §5.9.2). Password proof required; anonymized accounts past the
// window fail verification naturally.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var in service.LoginInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	// Same per-account brute-force budget as login: the target row holds a
	// live argon2id hash for the entire grace window.
	if s.accountThrottle("login", strings.ToLower(strings.TrimSpace(in.Email))) {
		s.metrics.RateLimited()
		w.Header().Set("Retry-After", "900")
		fail(w, domain.ErrRateLimited)
		return
	}
	user, tokens, err := s.deps.Auth.RestoreAccount(r.Context(), in.Email, in.Password, clientIPValue(s.clientIP(r)), r.UserAgent())
	if err != nil {
		fail(w, err)
		return
	}
	s.setSessionCookies(w, tokens.AccessToken, tokens.RefreshToken, tokens.CSRFToken)
	ok(w, s.publicUser(user))
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie(cookieRefresh)
	if err != nil {
		fail(w, domain.ErrSessionInvalid)
		return
	}
	user, tokens, err := s.deps.Auth.Refresh(r.Context(), ck.Value, clientIPValue(s.clientIP(r)), r.UserAgent())
	if err != nil {
		fail(w, err)
		return
	}
	s.setSessionCookies(w, tokens.AccessToken, tokens.RefreshToken, tokens.CSRFToken)
	ok(w, s.publicUser(user))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(cookieRefresh); err == nil {
		sess, err := s.deps.Repos.Sessions.GetByTokenHash(r.Context(), hashToken(ck.Value))
		if err == nil && sess != nil {
			_ = s.deps.Auth.Logout(r.Context(), sess.ID)
		}
	}
	s.clearSessionCookies(w)
	noContent(w)
}

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Auth.VerifyEmail(r.Context(), in.Token); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// handleResendVerification re-mails the verification link (PRD §5.9.1).
// Always 200 {"sent":true} — unknown/verified/throttled accounts must be
// indistinguishable from success (no account enumeration). Budget: 5 sends
// per hour per target account via the RateLimiter directly.
func (s *Server) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if _, _, allow := s.deps.RateLimiter.Allow("acct:resend:"+email, 5, time.Hour); allow {
		s.deps.Auth.ResendVerification(r.Context(), email)
	}
	ok(w, map[string]any{"sent": true})
}

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	_ = s.deps.Auth.ForgotPassword(r.Context(), in.Email) // never leaks account existence (PRD §9.3)
	noContent(w)
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Auth.ResetPassword(r.Context(), in.Token, in.Password); err != nil {
		fail(w, err)
		return
	}
	s.clearSessionCookies(w)
	noContent(w)
}

// handleCSRF issues a CSRF cookie (idempotent) for the SPA bootstrap.// Always re-sets so a fresh Path=/ cookie is stored even if a stale
// path-scoped copy exists; the stale variant is expired explicitly.
func (s *Server) handleCSRF(w http.ResponseWriter, r *http.Request) {
	token, err := security.GenerateCSRFToken()
	if err != nil {
		fail(w, err)
		return
	}
	secure := s.deps.Config.CookieSecure
	http.SetCookie(w, &http.Cookie{
		Name: cookieCSRF, Value: token, Path: "/",
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: 30 * 24 * 3600,
	})
	// Expire any legacy Path=/api/v1 copy.
	http.SetCookie(w, &http.Cookie{
		Name: cookieCSRF, Value: "", Path: "/api/v1",
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
	noContent(w)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	ok(w, s.publicUser(user))
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in service.UpdateProfileInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	updated, err := s.deps.Users.UpdateProfile(r.Context(), user, in)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, s.publicUser(updated))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Depth (PRD §5.8.6 / ARCHITECTURE §4): WS connections + last trend run.
	var lastTrend time.Time
	_ = s.deps.Repos.QueryRow(r.Context(),
		`SELECT max(taken_at) FROM trend_snapshots WHERE period='24h'`).Scan(&lastTrend)
	lastTrendStr := ""
	if !lastTrend.IsZero() {
		lastTrendStr = lastTrend.UTC().Format(time.RFC3339)
	}
	ok(w, map[string]any{
		"status":         "ok",
		"service":        "bizverse-api",
		"time":           time.Now().UTC().Format(time.RFC3339),
		"env":            s.deps.Config.AppEnv,
		"ws_connections": s.deps.Hub.Count(),
		"last_trend_run": lastTrendStr,
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var lastTrend string
	var ts time.Time
	_ = s.deps.Repos.QueryRow(r.Context(),
		`SELECT max(taken_at) FROM trend_snapshots WHERE period='24h'`).Scan(&ts)
	if !ts.IsZero() {
		lastTrend = ts.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(s.metrics.Render(s.deps.Hub.Count(), lastTrend)))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.deps.Repos.Pool().Ping(ctx); err != nil {
		fail(w, domain.ErrInternal)
		return
	}
	ok(w, map[string]any{"ready": true})
}

// publicUser — response DTO (never leaks the password hash; email is self-only).
func (s *Server) publicUser(u *domain.User) map[string]any {
	return map[string]any{
		"id":             u.ID,
		"email":          u.Email,
		"name":           u.Name,
		"username":       u.Username,
		"avatar_url":     u.AvatarURL,
		"bio":            u.Bio,
		"timezone":       u.Timezone,
		"profile_links":  u.ProfileLinks,
		"role":           u.Role,
		"status":         u.Status,
		"email_verified": u.EmailVerified(),
		"is_admin":       u.IsAdmin(),
		"created_at":     u.CreatedAt,
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return domain.ErrValidation.WithField("_", "Request body is invalid.")
	}
	return nil
}

func currentUser(r *http.Request) (*domain.User, bool) {
	u, ok := r.Context().Value(ctxKeyUser).(*domain.User)
	return u, ok
}

func clientIPValue(s string) net.IP {
	if s == "" {
		return nil
	}
	return net.ParseIP(s)
}
