package config

import (
	"encoding/hex"
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
	// LogLevel is the slog threshold from LOG_LEVEL (debug|info|warn|error).
	LogLevel slog.Level
	// Allowed WebSocket origins; when empty, only the same-origin host and
	// CORSOrigins are accepted (PRD §9.3 CSWSH protection).
	WSOrigins []string
	// TrustXForwardedFor: only set behind a proxy that strips spoofed values.
	TrustXForwardedFor bool
	// MetricsToken: Bearer token that may scrape /metrics in prod without an
	// admin session. Empty = admin session required (previous behavior).
	MetricsToken string
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

	// MEDIA_ENCRYPTION_KEY: 32-byte hex key sealing document_verification
	// uploads at rest (PRD §9.3). Required in prod.
	MediaEncryptionKey string
	// CLAMAV_ADDR: host:port of a clamd instance for INSTREAM virus
	// scanning of uploads. Required in prod.
	ClamAVAddr string

	// Google OAuth (PRD §5.9.1); disabled when empty.
	GoogleOAuthClientID string
	GoogleOAuthSecret   string

	// VAPID (Web Push, PRD §5.5.3); nil when not configured.
	VAPID *security.VAPIDKeypair
}

func Load() Config {
	cfg := Config{
		Port:                env("PORT", "8080"),
		AppEnv:              env("APP_ENV", "dev"),
		PublicURL:           env("PUBLIC_URL", "http://localhost:5173"),
		DatabaseURL:         env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/bizverse?sslmode=disable"),
		JWTSecret:           env("JWT_SECRET", "dev-secret-change-me-in-production-0123456789abcdef"),
		MigrationsDir:       env("MIGRATIONS_DIR", "../../infra/postgres/migrations"),
		CookieSecure:        env("COOKIE_SECURE", "false") == "true",
		LogLevel:            envLogLevel(os.Getenv("LOG_LEVEL")),
		CORSOrigins:         splitCSV(env("CORS_ORIGINS", "http://localhost:5173")),
		WSOrigins:           splitCSV(env("WS_ORIGINS", "")),
		TrustXForwardedFor:  env("TRUST_X_FORWARDED_FOR", "false") == "true",
		RateLimitGlobal:     envInt("RATELIMIT_GLOBAL", 120),
		MetricsToken:        os.Getenv("METRICS_TOKEN"),
		RedisURL:            os.Getenv("REDIS_URL"),
		ResendAPIKey:        os.Getenv("RESEND_API_KEY"),
		EmailFrom:           env("EMAIL_FROM", "no-reply@bizverse.app"),
		SMTPAddr:            os.Getenv("SMTP_ADDR"),
		SMTPUser:            os.Getenv("SMTP_USER"),
		SMTPPass:            os.Getenv("SMTP_PASS"),
		MediaDir:            env("MEDIA_DIR", "./data/media"),
		MediaBase:           env("MEDIA_BASE", "http://localhost:8080/api/v1/media"),
		MediaEncryptionKey:  os.Getenv("MEDIA_ENCRYPTION_KEY"),
		ClamAVAddr:          os.Getenv("CLAMAV_ADDR"),
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
		// Verification documents are PII: refuse to boot with plaintext
		// storage or no malware scanning rather than degrade silently.
		if len(c.MediaEncryptionKey) != 64 {
			return errors.New("MEDIA_ENCRYPTION_KEY must be a 64-char hex string (32 bytes) when APP_ENV=prod")
		}
		if _, err := hex.DecodeString(c.MediaEncryptionKey); err != nil {
			return errors.New("MEDIA_ENCRYPTION_KEY is not valid hex")
		}
		if c.ClamAVAddr == "" {
			return errors.New("CLAMAV_ADDR must point at a clamd instance when APP_ENV=prod")
		}
	} else if c.JWTSecret == "" || c.JWTSecret == devSecret {
		slog.Warn("using default JWT secret; set JWT_SECRET before deploying")
	}
	if c.MediaEncryptionKey != "" && len(c.MediaEncryptionKey) != 64 {
		return errors.New("MEDIA_ENCRYPTION_KEY must be a 64-char hex string (32 bytes)")
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

// envLogLevel parses LOG_LEVEL (debug|info|warn|error), defaulting to info.
func envLogLevel(v string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "", "info":
		return slog.LevelInfo
	default:
		slog.Warn("invalid LOG_LEVEL; using info", "value", v)
		return slog.LevelInfo
	}
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
