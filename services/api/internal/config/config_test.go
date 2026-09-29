package config

import (
	"strings"
	"testing"
)

// A strong secret that must pass every check, for negative-case construction.
const goodSecret = "3f8a1c9e2b7d4056a1e8c3f95b27d40e6a8c1f35d90b7e24a6c8f0d1e5b93a7c"

// --- Phase 0.2: the .env.example placeholder must not boot in prod ---

// Regression: Validate() used to blacklist exactly one literal (the Go
// dev default) while the shipped .env.example carried a *different*,
// equally well-known placeholder. An operator who copied the example and set
// APP_ENV=prod booted with a publicly-known HS256 signing key.
func TestValidate_RejectsShippedPlaceholderInProd(t *testing.T) {
	for _, placeholder := range []string{
		"change-me-to-a-random-64-char-secret", // the literal in .env.example
		"changeme-please-0123456789abcdefghijklmnop",
		"your-secret-here-0123456789abcdefghijklmnop",
		"example-secret-0123456789abcdefghijklmnopqr",
		"placeholder-secret-0123456789abcdefghijklmn",
		"dev-secret-0123456789abcdefghijklmnopqrst",
		"replace-me-0123456789abcdefghijklmnopqrstuv",
		"totally-a-secret-0123456789abcdefghijklmnop",
	} {
		cfg := baseConfig()
		cfg.JWTSecret = placeholder
		if err := cfg.Validate(); err == nil {
			t.Errorf("placeholder %q was accepted in prod", placeholder)
		}
	}
}

func TestValidate_AcceptsStrongSecretInProd(t *testing.T) {
	cfg := baseConfig()
	cfg.JWTSecret = goodSecret
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a strong secret must validate: %v", err)
	}
}

// The length floor is universal, not prod-only: a weak secret in dev is a
// confusing test failure and a weak secret in prod is a forged admin.
func TestValidate_RejectsShortSecretInEveryEnv(t *testing.T) {
	for _, env := range []string{"dev", "staging", "prod"} {
		cfg := baseConfig()
		cfg.AppEnv = env
		cfg.JWTSecret = "short"
		if err := cfg.Validate(); err == nil {
			t.Errorf("APP_ENV=%s accepted a 5-character JWT secret", env)
		}
	}
}

// --- Phase 0.3: APP_ENV must be normalised once, at load ---

// Regression: Config.Validate compared `== "prod"` while the metrics guard
// lower-cased. `APP_ENV=Production` therefore disabled every prod invariant
// (plaintext verification documents, unscanned uploads, unset JWT secret)
// while the metrics endpoint stayed locked down.
func TestEnvAppEnv_Normalisation(t *testing.T) {
	cases := map[string]string{
		"Production":  "prod",
		"PROD":        "prod",
		" production": "prod",
		"Prod":        "prod",
		"prod":        "prod",
		"production":  "prod",
		"staging":     "staging",
		"  staging  ": "staging",
		"":            "dev",
		"   ":         "dev",
		"DEV":         "dev",
	}
	for in, want := range cases {
		if got := envAppEnv(in); got != want {
			t.Errorf("envAppEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every spelling of "prod" must reach the prod branch, because that is the
// branch holding the safety invariants.
func TestValidate_ProdInvariantsApplyForEveryProdSpelling(t *testing.T) {
	for _, spelling := range []string{"Production", "PROD", "Prod", " prod "} {
		cfg := baseConfig()
		cfg.AppEnv = envAppEnv(spelling)
		// COOKIE_SECURE is a prod-only hard error; leave it false.
		cfg.CookieSecure = false
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("APP_ENV=%q skipped the prod invariants", spelling)
		}
		if !strings.Contains(err.Error(), "COOKIE_SECURE") {
			t.Errorf("APP_ENV=%q: got %v, want the COOKIE_SECURE prod error", spelling, err)
		}
	}
}

// --- prod transport + PII invariants ---

func TestValidate_ProdRequiresSecureCookies(t *testing.T) {
	cfg := baseConfig()
	cfg.CookieSecure = false
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("expected a COOKIE_SECURE prod error, got %v", err)
	}
}

// The code default is sslmode=disable for local Postgres. It must never be
// allowed to reach prod, where the whole session crosses the network.
func TestValidate_ProdRequiresDatabaseTLS(t *testing.T) {
	for _, dsn := range []string{
		"postgres://u:p@h:5432/bizverse?sslmode=disable",
		"postgres://u:p@h:5432/bizverse",
		"postgres://u:p@h:5432/bizverse?sslmode=allow",
	} {
		cfg := baseConfig()
		cfg.DatabaseURL = dsn
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "sslmode") {
			t.Errorf("DSN %q was accepted in prod", dsn)
		}
	}
	for _, dsn := range []string{
		"postgres://u:p@h:5432/bizverse?sslmode=require",
		"postgres://u:p@h:5432/bizverse?sslmode=verify-full",
	} {
		cfg := baseConfig()
		cfg.DatabaseURL = dsn
		if err := cfg.Validate(); err != nil {
			t.Errorf("DSN %q should be accepted in prod: %v", dsn, err)
		}
	}
}

func TestValidate_ProdRequiresRedissForRedis(t *testing.T) {
	cfg := baseConfig()
	cfg.RedisURL = "redis://h:6379"
	if err := cfg.Validate(); err == nil {
		t.Fatal("plaintext redis:// was accepted in prod (it carries live chat frames)")
	}
	cfg.RedisURL = "rediss://h:6379"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("rediss:// should be accepted: %v", err)
	}
}

// Regression: with no email sender configured, email.NewSender falls back to
// logSender, which prints password-reset JWTs to stdout. Validate() now makes
// that a hard error rather than a silent leak into the log aggregator.
func TestValidate_ProdRequiresAnEmailSender(t *testing.T) {
	cfg := baseConfig()
	cfg.ResendAPIKey = ""
	cfg.SMTPAddr = ""
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "RESEND_API_KEY") {
		t.Fatalf("expected a missing-email-sender prod error, got %v", err)
	}
	cfg.ResendAPIKey = "re_test"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("RESEND_API_KEY should satisfy the check: %v", err)
	}
}

// Half-configured Stripe is worse than absent: live subscription changes would
// never be reconciled.
func TestValidate_ProdRejectsStripeKeyWithoutWebhookSecret(t *testing.T) {
	cfg := baseConfig()
	cfg.StripeSecretKey = "sk_live_xxx"
	cfg.StripeWebhookSecret = ""
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "STRIPE_WEBHOOK_SECRET") {
		t.Fatalf("expected a STRIPE_WEBHOOK_SECRET prod error, got %v", err)
	}
	cfg.StripeWebhookSecret = "whsec_xxx"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a fully configured Stripe should validate: %v", err)
	}
}

func TestValidate_ProdRequiresEncryptionKeyAndScanner(t *testing.T) {
	cfg := baseConfig()
	cfg.MediaEncryptionKey = ""
	if err := cfg.Validate(); err == nil {
		t.Error("prod accepted an empty MEDIA_ENCRYPTION_KEY (verification documents are PII)")
	}
	cfg = baseConfig()
	cfg.ClamAVAddr = ""
	if err := cfg.Validate(); err == nil {
		t.Error("prod accepted an empty CLAMAV_ADDR (uploads would go unscanned)")
	}
}

func TestValidate_MediaEncryptionKeyShape(t *testing.T) {
	cfg := baseConfig()
	cfg.MediaEncryptionKey = "not-hex-but-the-right-length-0000000000000000"
	if err := cfg.Validate(); err == nil {
		t.Error("a 64-char non-hex MEDIA_ENCRYPTION_KEY was accepted")
	}
}

// --- non-prod must stay permissive enough to run locally ---

func TestValidate_DevAllowsLocalDefaults(t *testing.T) {
	cfg := baseConfig()
	cfg.AppEnv = "dev"
	cfg.JWTSecret = devJWTSecret
	cfg.CookieSecure = false
	cfg.DatabaseURL = "postgres://postgres:postgres@localhost:5432/bizverse?sslmode=disable"
	cfg.MediaEncryptionKey = ""
	cfg.ClamAVAddr = ""
	cfg.ResendAPIKey = ""
	cfg.SMTPAddr = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("dev must run with local defaults, got %v", err)
	}
}

// --- helper ---

// baseConfig is a minimal prod-valid configuration; each test knocks out
// exactly one invariant so a failure names the invariant that regressed.
func baseConfig() Config {
	return Config{
		AppEnv:             "prod",
		JWTSecret:          goodSecret,
		CookieSecure:       true,
		DatabaseURL:        "postgres://u:p@h:5432/bizverse?sslmode=verify-full",
		MediaEncryptionKey: strings.Repeat("ab", 32),
		ClamAVAddr:         "localhost:3310",
		ResendAPIKey:       "re_test",
		PublicURL:          "https://bizverse.app",
	}
}
