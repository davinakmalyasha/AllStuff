//go:build !race

package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/security"
	"bizverse/api/internal/service"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// -dev-login is the only path that turns seed rows into working logins, and it
// must refuse prod: a seed file applied to the wrong database plus a
// documented password is a breach with instructions attached.

func devLoginTestDeps(t *testing.T, h *testutil.H) (config.Config, *service.Auth) {
	t.Helper()
	cfg := config.Config{
		AppEnv:      "test",
		PublicURL:   "https://bizverse.test",
		JWTSecret:   "test-signing-secret-for-the-dev-login-harness-0123456789",
		MediaDir:    t.TempDir(),
		MediaBase:   "/media",
		CORSOrigins: []string{"https://bizverse.test"},
		WSOrigins:   []string{"https://bizverse.test"},
	}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test", PublicURL: cfg.PublicURL})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return cfg, service.NewAuth(h.Repos, cfg, sender, logger)
}

func seedLoginRow(t *testing.T, h *testutil.H, addr string, role domain.UserRole) {
	t.Helper()
	// Deliberately unverified: the command under test is what verifies the
	// email, so a pre-verified fixture would let a removed MarkEmailVerified
	// call pass unnoticed. (The real seed rows are verified; this exercises
	// the path that matters when they are not.)
	u := &domain.User{
		ID:           util.NewUUID(),
		Email:        addr,
		PasswordHash: "seed-only-no-login",
		Name:         "Seed User",
		Username:     "u" + util.NewUUID()[:8],
		Timezone:     "UTC",
		Role:         role,
		Status:       domain.UserStatusActive,
	}
	if err := h.Repos.Users.Create(context.Background(), u); err != nil {
		t.Fatalf("seed %s: %v", addr, err)
	}
}

func TestDevLoginRefusesProd(t *testing.T) {
	// No database needed: the refusal happens before anything is touched.
	cfg := config.Config{AppEnv: "prod"}
	err := runDevLogin(context.Background(), cfg, nil, nil, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "prod") {
		t.Fatalf("err = %v, want the prod refusal", err)
	}
}

func TestDevLoginNeedsSeedRows(t *testing.T) {
	h := testutil.New(t)
	cfg, auth := devLoginTestDeps(t, h)
	seedLoginRow(t, h, devDemoEmail, domain.RoleUser)
	// The admin row is missing: the fix is running the seed, not inventing an
	// admin here, or the command would fork from what seed.sql defines.
	err := runDevLogin(context.Background(), cfg, h.Repos, auth, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "seed.sql") {
		t.Fatalf("err = %v, want the run-seed-first error", err)
	}
}

func TestDevLoginSetsPasswordsAndEnrollsAdmin2FA(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	cfg, auth := devLoginTestDeps(t, h)
	seedLoginRow(t, h, devDemoEmail, domain.RoleUser)
	seedLoginRow(t, h, devAdminEmail, domain.RoleAdmin)

	if err := runDevLogin(ctx, cfg, h.Repos, auth, slog.Default()); err != nil {
		t.Fatalf("runDevLogin: %v", err)
	}

	for _, target := range []struct {
		email string
		admin bool
	}{
		{devDemoEmail, false},
		{devAdminEmail, true},
	} {
		u, err := h.Repos.Users.GetByEmail(ctx, target.email)
		if err != nil || u == nil {
			t.Fatalf("read %s: %+v, %v", target.email, u, err)
		}
		if ok, err := security.VerifyPassword(devPassword, u.PasswordHash); err != nil || !ok {
			t.Errorf("%s: the dev password does not verify: %v", target.email, err)
		}
		if u.EmailVerifiedAt == nil {
			t.Errorf("%s: email is not verified", target.email)
		}
		st, err := h.Repos.TFA.Get(ctx, u.ID)
		if err != nil {
			t.Fatalf("TFA.Get %s: %v", target.email, err)
		}
		if target.admin {
			if st == nil || st.EnabledAt == nil {
				t.Errorf("admin 2FA is not enabled; the admin gate would still block /admin/*")
			}
		} else if st != nil && st.EnabledAt != nil {
			t.Errorf("the owner must not gain a 2FA factor from this command")
		}
	}

	// Idempotent: the admin keeps their existing authenticator instead of
	// getting it replaced out from under them.
	if err := runDevLogin(ctx, cfg, h.Repos, auth, slog.Default()); err != nil {
		t.Fatalf("second runDevLogin: %v", err)
	}
}
