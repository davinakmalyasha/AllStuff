package config

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"bizverse/api/internal/security"
)

type Config struct {
	Port          string
	AppEnv        string
	PublicURL     string
	DatabaseURL   string
	JWTSecret     string
	MigrationsDir string
	CookieSecure  bool
	CORSOrigins   []string
	// Allowed WebSocket origins; when empty, only the same-origin host and
	// CORSOrigins are accepted (PRD §9.3 CSWSH protection).
	WSOrigins []string
	// TrustXForwardedFor: only set behind a proxy that strips spoofed values.
	TrustXForwardedFor bool
	// RateLimitGlobal overrides the default 120 req/min/IP global bucket.
	// Useful behind corporate NAT/proxies where many users share one egress
	// IP, and for E2E runs where all browser traffic funnels through the
	// dev-server proxy.
	RateLimitGlobal int
	// RedisURL enables multi-instance WS fan-out ("" = single instance).
	RedisURL string

	ResendAPIKey string
	EmailFrom    string

	// SMTP relay (e.g. Mailpit from the compose stack); when set, dev email
	// goes through SMTP instead of console logging.
	SMTPAddr string
	SMTPUser string
	SMTPPass string

	MediaDir  string
	MediaBase string

	// Google OAuth (PRD §5.9.1); disabled when empty.
	GoogleOAuthClientID string
	GoogleOAuthSecret   string

	// VAPID (Web Push, PRD §5.5.3); nil when not configured.
	VAPID *security.VAPIDKeypair
}

func Load() Config {
	cfg := Config{
		Port:          env("PORT", "8080"),
		AppEnv:        env("APP_ENV", "dev"),
		PublicURL:     env("PUBLIC_URL", "http://localhost:5173"),
		DatabaseURL:   env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/bizverse?sslmode=disable"),
		JWTSecret:     env("JWT_SECRET", "dev-secret-change-me-in-production-0123456789abcdef"),
		MigrationsDir: env("MIGRATIONS_DIR", "../../infra/postgres/migrations"),
		CookieSecure:  env("COOKIE_SECURE", "false") == "true",
		CORSOrigins:   splitCSV(env("CORS_ORIGINS", "http://localhost:5173")),
		WSOrigins:     splitCSV(env("WS_ORIGINS", "")),
		TrustXForwardedFor: env("TRUST_X_FORWARDED_FOR", "false") == "true",
		RateLimitGlobal:    envInt("RATELIMIT_GLOBAL", 120),
		RedisURL:           os.Getenv("REDIS_URL"),
		ResendAPIKey:  os.Getenv("RESEND_API_KEY"),
		EmailFrom:     env("EMAIL_FROM", "no-reply@bizverse.app"),
		SMTPAddr:      os.Getenv("SMTP_ADDR"),
		SMTPUser:      os.Getenv("SMTP_USER"),
		SMTPPass:      os.Getenv("SMTP_PASS"),
		MediaDir:      env("MEDIA_DIR", "./data/media"),
		MediaBase:     env("MEDIA_BASE", "http://localhost:8080/api/v1/media"),
		GoogleOAuthClientID: os.Getenv("GOOGLE_OAUTH_CLIENT_ID"),
		GoogleOAuthSecret:   os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET"),
	}
	// VAPID keys are base64url-encoded raw 65-byte P-256 public key + DER private.
	if pub := os.Getenv("VAPID_PUBLIC_KEY"); pub != "" && os.Getenv("VAPID_PRIVATE_KEY") != "" {
		if kp, err := security.ParseVAPIDKeypair(pub, os.Getenv("VAPID_PRIVATE_KEY")); err == nil {
			cfg.VAPID = kp
		} else {
			slog.Warn("invalid VAPID keypair; web push disabled", "err", err)
		}
	}
	return cfg
}

// Validate enforces production safety invariants. It must be called after
// Load() and abort startup when an unsafe configuration reaches prod.
func (c Config) Validate() error {
	const devSecret = "dev-secret-change-me-in-production-0123456789abcdef"
	if c.AppEnv == "prod" || c.AppEnv == "production" {
		if c.JWTSecret == "" || c.JWTSecret == devSecret {
			return errors.New("JWT_SECRET must be set to a strong random value when APP_ENV=prod")
		}
		if !c.CookieSecure {
			slog.Warn("COOKIE_SECURE=false in prod: session cookies will not be marked Secure")
		}
	} else if c.JWTSecret == "" || c.JWTSecret == devSecret {
		slog.Warn("using default JWT secret; set JWT_SECRET before deploying")
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		slog.Warn("invalid RATELIMIT_GLOBAL; using default", "value", v)
	}
	return fallback
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
