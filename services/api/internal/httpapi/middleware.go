package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// injects the owning user as the request principal.
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

// ---- rate limiting (PRD §8.8, §9.3) ----

var authPaths = map[string]bool{
	"/api/v1/auth/register":       true,
	"/api/v1/auth/login":          true,
	"/api/v1/auth/refresh":        true,
	"/api/v1/auth/forgot-password": true,
	"/api/v1/auth/reset-password": true,
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
		limit, window := 120, time.Minute
		p := r.URL.Path
		switch {
		case authPaths[p]:
			limit, window = 5, time.Minute // PRD §5.9.1: 5/min per IP
		case p == "/api/v1/search" || p == "/api/v1/suggest":
			limit, window = 60, time.Minute
		case p == "/api/v1/auth/2fa/verify":
			limit, window = 3, 15*time.Minute // PRD §5.9.1: 3 attempts then lockout
		case p == "/api/v1/media/upload":
			limit, window = 20, time.Hour // media: 20/h per user
			if u, found := currentUser(r); found {
				key = u.ID
			}
		case engagementPath(p) && r.Method != http.MethodGet:
			limit, window = 30, time.Minute // engagement writes: 30/min per user
			if u, found := currentUser(r); found {
				key = u.ID
			}
		}
		remaining, retryAfter, ok := s.deps.RateLimiter.Allow(key, limit, window)
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

// withETag buffers successful GET responses and attaches a content hash so
// clients/proxies can revalidate with If-None-Match (304).
func (s *Server) withETag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1") {
			next.ServeHTTP(w, r)
			return
		}
		rec := &etagRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status != http.StatusOK || rec.buf == nil {
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
	status int
	buf    []byte
}

// WriteHeader only records: the real commit happens in withETag after the
// body is buffered, so the ETag header can be attached first.
func (r *etagRecorder) WriteHeader(code int) {
	r.status = code
}

func (r *etagRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.status == http.StatusOK {
		r.buf = append(r.buf, b...)
	}
	return len(b), nil
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

// ---- helpers ----

func (s *Server) clientIP(r *http.Request) string {
	if s.deps.Config.TrustXForwardedFor {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i > 0 {
				return strings.TrimSpace(xff[:i])
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
