package httpapi

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/security"
)

const (
	cookieAccess  = "bv_access"
	cookieRefresh = "bv_refresh"
	cookieCSRF    = "bv_csrf"
	// CSRF cookie is read by the SPA at "/" via document.cookie, so it must be
	// site-wide (double-submit value is not a secret). Refresh cookie stays
	// scoped to /api/v1/auth.
	csrfCookiePath = "/"
)

// ---- recovery ----

func (s *Server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.deps.Logger.Error("panic", "err", rec, "stack", string(debug.Stack()))
				fail(w, domain.ErrInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---- access log ----

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush lets streaming handlers flush through the access-log wrapper.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack lets WebSocket upgrades succeed through the access-log wrapper;
// without this gorilla/websocket's Upgrader rejects the wrapped writer.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("statusRecorder: underlying ResponseWriter is not a Hijacker")
	}
	return hj.Hijack()
}

func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.deps.Logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"dur_ms", time.Since(start).Milliseconds(),
			"ip", s.clientIP(r),
			"ua", r.UserAgent(),
		)
		if !strings.HasPrefix(r.URL.Path, "/api/v1/health") && r.URL.Path != "/metrics" {
			s.metrics.Observe(r.Method, r.URL.Path, rec.status)
		}
	})
}

// ---- public API keys (PRD §9.6) ----

// apiKeyOnly guards /api/v2/* with X-API-Key (300 req/min per key) and
// injects the owning user as the request principal. The declared scopes are
// enforced: v2 is read-only today, so a "read" scope is required (keys
// created before scope checks default to ["read"]).
func (s *Server) apiKeyOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-API-Key")
		if raw == "" {
			fail(w, domain.ErrNotAuthenticated)
			return
		}
		key, err := s.deps.APIKeys.Valid(r.Context(), raw)
		if err != nil || key == nil {
			fail(w, domain.ErrNotAuthenticated)
			return
		}
		if !key.HasScope("read") {
			fail(w, domain.ErrForbidden)
			return
		}
		_, retry, ok := s.deps.RateLimiter.Allow("apikey:"+key.ID, 300, time.Minute)
		if !ok {
			s.metrics.RateLimited()
			w.Header().Set("Retry-After", seconds(retry))
			fail(w, domain.ErrRateLimited)
			return
		}
		u, err := s.deps.Repos.Users.GetByID(r.Context(), key.UserID)
		if err != nil || u == nil {
			fail(w, domain.ErrNotAuthenticated)
			return
		}
		// Banned users lose API access like session auth does (withAuth).
		if u.Status == domain.UserStatusBanned {
			fail(w, domain.ErrAccountBanned)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyUser, u))
		next(w, r)
	}
}

// ---- admin 2FA mandate (PRD §5.9.1) ----

// withAdmin2FA blocks admins without enrolled 2FA from everything except
// security/2FA enrollment endpoints, auth refresh/logout, and the WS upgrade.
func (s *Server) withAdmin2FA(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, found := currentUser(r)
		if !found || !user.IsAdmin() {
			next.ServeHTTP(w, r)
			return
		}
		st, err := s.deps.Repos.TFA.Get(r.Context(), user.ID)
		if err == nil && st != nil && st.EnabledAt != nil {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/v1/me/security") || strings.HasPrefix(p, "/api/v1/auth/refresh") ||
			strings.HasPrefix(p, "/api/v1/auth/logout") || p == "/api/v1/ws" ||
			strings.HasPrefix(p, "/api/v1/auth/2fa") {
			next.ServeHTTP(w, r)
			return
		}
		fail(w, domain.Err2FAEnrollmentRequired)
	})
}

// ---- CORS (PRD §9.3) ----

func (s *Server) withCORS(next http.Handler) http.Handler {
	origins := map[string]bool{}
	for _, o := range s.deps.Config.CORSOrigins {
		origins[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- baseline security headers (PRD §9.3) ----

// withSecurityHeaders stamps conservative defaults on every API response.
// Handlers may override individual headers (e.g. the OG/media endpoints set
// stricter CSPs); these are the floor, not the ceiling.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if s.deps.Config.CookieSecure {
			// Only meaningful over TLS; avoids advertising HSTS on plain
			// HTTP dev servers.
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// ---- rate limiting (PRD §8.8, §9.3) ----

var authPaths = map[string]bool{
	"/api/v1/auth/register":        true,
	"/api/v1/auth/login":           true,
	"/api/v1/auth/forgot-password": true,
	"/api/v1/auth/reset-password":  true,
	// Restore verifies a real password against a live hash (the row keeps
	// its credentials during the deletion grace window), so it must sit in
	// the same brute-force tier as login.
	"/api/v1/auth/restore": true,
	// NOTE: /auth/refresh deliberately lives in its own tier below — it
	// requires a valid cookie to do anything, and counting it here let
	// routine logged-out page loads (each probing session restore) crowd
	// out real register/login attempts.
}

// engagementPath: mutating engagement calls are user-scoped 30/min (PRD §8.8).
func engagementPath(p string) bool {
	for _, prefix := range []string{
		"/api/v1/likes/", "/api/v1/recommends/", "/api/v1/me/collections",
		"/api/v1/businesses/", "/api/v1/comments/", "/api/v1/reviews/",
		"/api/v1/questions/", "/api/v1/collections/",
	} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func (s *Server) withRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := s.clientIP(r)
		// Tier namespace: buckets MUST NOT be shared across limit classes.
		// With one shared IP key, high-volume global traffic (health checks,
		// crawlers) silently consumed the auth bucket and locked users out
		// of login/register with instant 429s.
		tier := "ip"
		limit, window := s.deps.Config.RateLimitGlobal, time.Minute
		if limit <= 0 {
			limit = 120
		}
		p := r.URL.Path
		switch {
		case authPaths[p]:
			tier = "auth"
			limit, window = 5, time.Minute // PRD §5.9.1: 5/min per IP
		case p == "/api/v1/search" || p == "/api/v1/search/suggest" || p == "/api/v1/users/search":
			tier = "search"
			limit, window = 60, time.Minute
		case p == "/api/v1/sitemap.xml":
			// Full-directory scan; keep crawlers on a tight budget.
			tier = "sitemap"
			limit, window = 10, time.Minute
		case p == "/api/v1/me/export" ||
			(strings.HasPrefix(p, "/api/v1/threads/") && strings.HasSuffix(p, "/export")):
			// Heavy sequential exports: N+1 aggregations / 100k-message dumps
			// previously shared the generic 120/min IP budget.
			tier = "export"
			limit, window = 5, time.Hour
		case p == "/api/v1/auth/2fa/verify":
			tier = "tfa"
			limit, window = 3, 15*time.Minute // PRD §5.9.1: 3 attempts then lockout
		case p == "/api/v1/auth/refresh":
			// Cookie-gated session renewal: generous but bounded.
			tier = "refresh"
			limit, window = 60, time.Minute
		case p == "/api/v1/media" && r.Method == http.MethodPost:
			tier = "media"
			limit, window = 20, time.Hour // media: 20/h per user
			if u, found := currentUser(r); found {
				key = u.ID
			}
		case strings.HasPrefix(p, "/api/v1/threads/") && strings.HasSuffix(p, "/messages") && r.Method == http.MethodPost:
			// Chat sends: burst guard per user (ARCHITECTURE §1: 1/s + 60/h).
			tier = "chat"
			limit, window = 30, time.Minute
			if u, found := currentUser(r); found {
				key = u.ID
			}
		case engagementPath(p) && r.Method != http.MethodGet:
			tier = "engage"
			limit, window = 30, time.Minute // engagement writes: 30/min per user
			if u, found := currentUser(r); found {
				key = u.ID
			}
		}
		remaining, retryAfter, ok := s.deps.RateLimiter.Allow(tier+":"+key, limit, window)
		if !ok {
			s.metrics.RateLimited()
			w.Header().Set("Retry-After", seconds(retryAfter))
			fail(w, domain.ErrRateLimited)
			return
		}
		w.Header().Set("X-RateLimit-Remaining", itoa(remaining))
		next.ServeHTTP(w, r)
	})
}

// ---- CSRF double-submit (PRD §5.9.1) ----

func (s *Server) withCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		headerVal := r.Header.Get(security.CSRFHeader)
		if headerVal == "" {
			fail(w, domain.ErrCSRF)
			return
		}
		// Accept any bv_csrf cookie matching the header (handles stale
		// path-scoped cookies left by older deployments).
		for _, ck := range r.Cookies() {
			if ck.Name == cookieCSRF && security.CSRFValid(ck.Value, headerVal) {
				next.ServeHTTP(w, r)
				return
			}
		}
		fail(w, domain.ErrCSRF)
	})
}

// ---- conditional caching (PRD §8.8, ARCHITECTURE §1) ----

// etagBufferLimit caps how much of a response is held for hashing; larger
// payloads (media files, exports) are streamed straight through.
const etagBufferLimit = 512 << 10 // 512 KiB

// withETag buffers successful JSON GET responses and attaches a content hash
// so clients/proxies can revalidate with If-None-Match (304). Everything else
// — errors, non-JSON bodies, oversized payloads, the WebSocket upgrade —
// passes through untouched.
func (s *Server) withETag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1") ||
			r.URL.Path == "/api/v1/ws" {
			next.ServeHTTP(w, r)
			return
		}
		rec := newEtagRecorder(w)
		next.ServeHTTP(rec, r)
		if rec.passthrough || rec.buf == nil {
			return
		}
		sum := sha256.Sum256(rec.buf)
		etag := `"` + hex.EncodeToString(sum[:16]) + `"`
		w.Header().Set("ETag", etag)
		if inm := r.Header.Get("If-None-Match"); inm == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(rec.buf)
	})
}

type etagRecorder struct {
	http.ResponseWriter
	status      int
	buf         []byte
	passthrough bool
}

func newEtagRecorder(w http.ResponseWriter) *etagRecorder {
	return &etagRecorder{ResponseWriter: w}
}

// bufferable reports whether the response should be held for ETag hashing:
// only 200 responses whose Content-Type is JSON.
func (r *etagRecorder) bufferable() bool {
	if r.status != http.StatusOK && r.status != 0 {
		return false
	}
	ct := r.Header().Get("Content-Type")
	return strings.HasPrefix(ct, "application/json")
}

// WriteHeader defers committing only while the response still looks like a
// small JSON 200; anything else is forwarded immediately and verbatim,
// including its status code (errors must never be swallowed into a 200).
func (r *etagRecorder) WriteHeader(code int) {
	r.status = code
	if !r.bufferable() || r.passthrough {
		r.passthrough = true
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *etagRecorder) Write(b []byte) (int, error) {
	if r.status == 0 && len(b) > 0 {
		r.status = http.StatusOK
	}
	if r.passthrough {
		return r.ResponseWriter.Write(b)
	}
	if !r.bufferable() {
		r.passthrough = true
		r.ResponseWriter.WriteHeader(r.status)
		return r.ResponseWriter.Write(b)
	}
	if len(r.buf)+len(b) > etagBufferLimit {
		// Too large to hash: flush what we kept and stream the rest.
		r.passthrough = true
		r.ResponseWriter.WriteHeader(r.status)
		if len(r.buf) > 0 {
			if _, err := r.ResponseWriter.Write(r.buf); err != nil {
				return 0, err
			}
			r.buf = nil
		}
		return r.ResponseWriter.Write(b)
	}
	r.buf = append(r.buf, b...)
	return len(b), nil
}

// Flush streams buffered data when a handler asks for it (streaming JSON).
func (r *etagRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack lets WebSocket-style upgrades pass through even if this middleware
// ends up wrapping them (the /api/v1/ws path is already exempted upstream).
func (r *etagRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("etagRecorder: underlying ResponseWriter is not a Hijacker")
	}
	return hj.Hijack()
}

// ---- auth (access token → user + session) ----

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authenticated routes declare it; others pass through with optional user.
		claims, err := s.authenticate(r)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if s.deps.Repos == nil {
			// Bare wiring (tests): claims only, no user lookup.
			r = r.WithContext(context.WithValue(r.Context(), ctxKeyClaims, claims))
			next.ServeHTTP(w, r)
			return
		}
		user, err := s.deps.Repos.Users.GetByID(r.Context(), claims.UserID)
		if err != nil || user == nil {
			next.ServeHTTP(w, r)
			return
		}
		if user.Status == domain.UserStatusBanned {
			next.ServeHTTP(w, r)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyClaims, claims))
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyUser, user))
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authenticate(r *http.Request) (*domain.Claims, error) {
	ck, err := r.Cookie(cookieAccess)
	if err != nil {
		return nil, err
	}
	return security.ParseToken(s.deps.Config.JWTSecret, ck.Value, security.TokenAccess)
}

// ---- cookies ----

func (s *Server) setSessionCookies(w http.ResponseWriter, access, refresh, csrf string) {
	secure := s.deps.Config.CookieSecure
	http.SetCookie(w, &http.Cookie{
		Name: cookieAccess, Value: access, Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: 15 * 60,
	})
	http.SetCookie(w, &http.Cookie{
		Name: cookieRefresh, Value: refresh, Path: "/api/v1/auth",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
		MaxAge: 30 * 24 * 3600,
	})
	http.SetCookie(w, &http.Cookie{
		Name: cookieCSRF, Value: csrf, Path: csrfCookiePath,
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: 30 * 24 * 3600,
	})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{cookieAccess, cookieRefresh, cookieCSRF} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/api/v1/auth", MaxAge: -1})
	}
}

// ---- ops metrics guard ----

// metricsGuard keeps /metrics open for local/dev tooling but requires an
// admin session in prod (it exposes traffic counts and internals).
func (s *Server) metricsGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		env := strings.ToLower(s.deps.Config.AppEnv)
		if env != "prod" && env != "production" {
			next(w, r)
			return
		}
		user, found := currentUser(r)
		if !found || !user.IsAdmin() {
			fail(w, domain.ErrNotAuthenticated)
			return
		}
		next(w, r)
	}
}

// ---- helpers ----

func (s *Server) clientIP(r *http.Request) string {
	if s.deps.Config.TrustXForwardedFor {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// The rightmost entry was appended by the proxy we control and is
			// the only hop a client cannot spoof; leftmost entries are
			// attacker-supplied.
			if i := strings.LastIndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[i+1:])
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func seconds(d time.Duration) string {
	return itoa(int(d.Seconds()) + 1)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
