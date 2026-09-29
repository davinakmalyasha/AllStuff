package config

import (
	"encoding/hex"
	"errors"
	"fmt"
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

	// Stripe (Phase 7.1 monetization). Billing is disabled when
	// StripeSecretKey is empty; the free plan still resolves, so a dev
	// checkout works with no Stripe account configured.
	StripeSecretKey      string
	StripeWebhookSecret  string
	StripePublishableKey string

	// WebDistDir points at the built frontend (apps/web/dist). The server-
	// rendered SEO shell reads .vite/manifest.json from here to reference the
	// hashed entry script. When empty the shell still serves crawlable HTML,
	// it just cannot hydrate the SPA.
	WebDistDir string

	// VAPID (Web Push, PRD §5.5.3); nil when not configured.
	VAPID *security.VAPIDKeypair
}

func Load() Config {
	cfg := Config{
		Port:      env("PORT", "8080"),
		AppEnv:    envAppEnv(os.Getenv("APP_ENV")),
		PublicURL: env("PUBLIC_URL", "http://localhost:5173"),
		// NOTE: sslmode default is disable for local only. Validate() refuses
		// a non-TLS DSN when APP_ENV=prod, so the default can never reach prod.
		DatabaseURL:         env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/bizverse?sslmode=disable"),
		JWTSecret:           env("JWT_SECRET", devJWTSecret),
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
		SMTPPass:            os.Getenv("SMTPPASS"),
		MediaDir:            env("MEDIA_DIR", "./data/media"),
		MediaBase:           env("MEDIA_BASE", "http://localhost:8080/api/v1/media"),
		MediaEncryptionKey:  os.Getenv("MEDIA_ENCRYPTION_KEY"),
		ClamAVAddr:          os.Getenv("CLAMAV_ADDR"),
		GoogleOAuthClientID: os.Getenv("GOOGLE_OAUTH_CLIENT_ID"),
		GoogleOAuthSecret:   os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET"),
		StripeSecretKey:     os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		// Safe to ship to the browser; only the publishable key belongs in
		// VITE_-prefixed frontend config.
		StripePublishableKey: os.Getenv("STRIPE_PUBLISHABLE_KEY"),
		WebDistDir:           os.Getenv("WEB_DIST_DIR"),
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

// devJWTSecret is the development-only fallback. It is never a valid prod
// value: Validate() rejects it, and so does knownPlaceholder below.
const devJWTSecret = "dev-secret-change-me-in-production-0123456789abcdef"

// minJWTSecretLen is the floor for an HS256 key. 32 bytes is the minimum
// defensible secret; 64+ characters of base64/hex is what we ask operators for.
const minJWTSecretLen = 32

// envAppEnv normalises APP_ENV once, at load. Previously this was read raw in
// Config.Validate (case-sensitive `== "prod"`) and lower-cased separately in
// the metrics guard, so `APP_ENV=Production` — an entirely routine
// capitalisation — silently disabled every production invariant while the
// metrics guard still enforced its own. One normalisation, one comparison.
func envAppEnv(v string) string {
	e := strings.ToLower(strings.TrimSpace(v))
	if e == "production" {
		return "prod"
	}
	if e == "" {
		return "dev"
	}
	return e
}

// knownPlaceholder rejects secrets that are syntactically valid but obviously
// not secret. A denylist of the dev literal alone is not enough: the shipped
// .env.example carried a *different* equally well-known placeholder, and an
// operator who copied it and set APP_ENV=prod booted with a public signing key.
func knownPlaceholder(s string) bool {
	s = strings.ToLower(s)
	for _, p := range []string{
		"change-me", "changeme", "change_me", "example", "placeholder",
		"your-", "your_", "dev-secret", "devsecret", "secret", "password",
		"replace-me", "replaceme", "todo", "xxx", "abcdef",
	} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// Validate enforces production safety invariants. It must be called after
// Load() and abort startup when an unsafe configuration reaches prod.
//
// The policy is fail-closed on *absence* and loud on *weakness*. Anything
// where degrading silently is worse than refusing to boot is a hard error.
func (c Config) Validate() error {
	isProd := c.AppEnv == "prod"

	// ---- secret strength (all environments) ----
	// A weak JWT secret is a forged-admin vulnerability in prod and a
	// confusing test failure in dev, so the length floor is universal.
	if len(c.JWTSecret) < minJWTSecretLen {
		return fmt.Errorf("JWT_SECRET must be at least %d characters (got %d)", minJWTSecretLen, len(c.JWTSecret))
	}
	if isProd && knownPlaceholder(c.JWTSecret) {
		return errors.New("JWT_SECRET looks like a placeholder; generate one with `openssl rand -hex 32`")
	}
	if !isProd && (c.JWTSecret == devJWTSecret || knownPlaceholder(c.JWTSecret)) {
		slog.Warn("using a placeholder JWT secret; fine for dev, must be replaced before deploying")
	}

	if c.MediaEncryptionKey != "" && len(c.MediaEncryptionKey) != 64 {
		return errors.New("MEDIA_ENCRYPTION_KEY must be a 64-char hex string (32 bytes)")
	}
	if c.MediaEncryptionKey != "" {
		if _, err := hex.DecodeString(c.MediaEncryptionKey); err != nil {
			return errors.New("MEDIA_ENCRYPTION_KEY is not valid hex")
		}
	}

	if isProd {
		// ---- transport ----
		// Without Secure, session cookies are sent over plaintext HTTP on any
		// request the client is redirected to, and on a first-visit SSL strip.
		if !c.CookieSecure {
			return errors.New("COOKIE_SECURE=true is mandatory when APP_ENV=prod (session cookies would be sent over plaintext HTTP)")
		}
		// The whole session — PII, password hashes, session registry — would
		// otherwise cross the network unencrypted.
		if !hasTLSDatabase(c.DatabaseURL) {
			return errors.New("DATABASE_URL must set sslmode=require (or verify-full) when APP_ENV=prod")
		}
		// The WS pub/sub channel carries live chat frames.
		if c.RedisURL != "" && !strings.HasPrefix(c.RedisURL, "rediss://") {
			return errors.New("REDIS_URL must use rediss:// when APP_ENV=prod (chat fan-out would cross the network in cleartext)")
		}
		// ---- PII and malware scanning ----
		if c.MediaEncryptionKey == "" {
			return errors.New("MEDIA_ENCRYPTION_KEY must be set when APP_ENV=prod (verification documents are PII)")
		}
		if c.ClamAVAddr == "" {
			return errors.New("CLAMAV_ADDR must point at a clamd instance when APP_ENV=prod")
		}
		// ---- email transport ----
		// With no sender configured, email.NewSender falls back to logSender,
		// which prints the body — including the raw password-reset JWT — to
		// stdout. That outlives the token's TTL in any log aggregator.
		if c.ResendAPIKey == "" && c.SMTPAddr == "" {
			return errors.New("RESEND_API_KEY (or SMTP_ADDR) must be set when APP_ENV=prod, otherwise password-reset tokens are written to logs")
		}
		// ---- billing ----
		// Half-configured Stripe is worse than absent: a secret key with no
		// webhook secret means live subscription changes are never reconciled.
		if c.StripeSecretKey != "" && c.StripeWebhookSecret == "" {
			return errors.New("STRIPE_WEBHOOK_SECRET must be set when STRIPE_SECRET_KEY is set (otherwise no subscription change is ever applied)")
		}
	}
	return nil
}

func hasTLSDatabase(dsn string) bool {
	// Reject any explicit insecure mode, then require a TLS one to be present.
	// Parsed loosely on purpose: this is a startup guard, not a DSN validator,
	// and pgx will surface a genuinely malformed DSN with a better message.
	lower := strings.ToLower(dsn)
	if strings.Contains(lower, "sslmode=disable") {
		return false
	}
	return strings.Contains(lower, "sslmode=require") || strings.Contains(lower, "sslmode=verify-full")
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
		// Warn, don't fail: a malformed tuning knob should not stop a deploy.
		// docs/DEPLOYMENT.md documented `RATELIMIT_GLOBAL=120/min`, which
		// Atoi rejects — the message must name the offending key so that
		// mistake is diagnosable from the log line alone.
		slog.Warn("invalid integer env var; using default", "key", key, "value", v, "default", fallback)
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
