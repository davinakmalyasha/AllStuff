package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"allstuff/api/internal/config"
	"allstuff/api/internal/domain"
	"allstuff/api/internal/repo"
	"allstuff/api/internal/security"
	"allstuff/api/internal/service"
)

// Demo credentials for the seed accounts (scripts/seed.sql).
//
// The seed ships with placeholder password hashes by design: applying it must
// never create logins on its own, because a seed file applied to the wrong
// database is a backup-restore away from production. This command is the only
// path that turns those rows into working logins, and it refuses prod outright
// (see the guard below, which is mutation-tested). The password is printed on
// every run because a documented dev password you have to look up is one you
// will replace with something weaker, and a dev password that also works
// anywhere real is a breach.
//
// Reviewer path: migrate, seed, dev-login, then sign in as the owner to click
// through the dashboard or as the admin (scan the printed QR into any
// authenticator app) for /admin/*.
const (
	devDemoEmail  = "demo@allstuff.test"
	devAdminEmail = "admin@allstuff.test"
	devPassword   = "DemoPass123!"
)

var errDevLoginRefusedInProd = errors.New(
	"-dev-login refuses to run when APP_ENV=prod: it writes documented passwords")

// runDevLogin sets the documented dev password on both seed accounts, verifies
// their emails, and enrolls 2FA for the admin (the admin 2FA gate blocks every
// /admin route otherwise). Idempotent: re-running resets the same password and
// skips 2FA enrollment when a factor is already enabled.
func runDevLogin(ctx context.Context, cfg config.Config, repos *repo.Repos, auth *service.Auth, logger *slog.Logger) error {
	if cfg.AppEnv == "prod" {
		return errDevLoginRefusedInProd
	}
	hash, err := security.HashPassword(devPassword)
	if err != nil {
		return fmt.Errorf("hash dev password: %w", err)
	}
	demo, err := devLoginAccount(ctx, repos, devDemoEmail, hash)
	if err != nil {
		return err
	}
	admin, err := devLoginAccount(ctx, repos, devAdminEmail, hash)
	if err != nil {
		return err
	}
	otpauth, recovery, err := ensureAdmin2FA(ctx, repos, auth, admin.ID)
	if err != nil {
		return err
	}

	fmt.Println("dev logins ready (DEV ONLY - never use these passwords anywhere real):")
	fmt.Printf("  owner  %s / %s\n", demo.Email, devPassword)
	fmt.Printf("  admin  %s / %s  (2FA enrolled; scan the QR URL into an authenticator app)\n", admin.Email, devPassword)
	if otpauth != "" {
		fmt.Printf("  admin otpauth url: %s\n", otpauth)
		fmt.Printf("  admin recovery codes: %v\n", recovery)
	} else {
		fmt.Println("  admin 2FA was already enrolled; existing authenticator still applies.")
	}
	logger.Info("dev-login applied", "demo", demo.ID, "admin", admin.ID)
	return nil
}

// devLoginAccount looks up a seed account, sets the dev password, and verifies
// the email. A missing row is an error naming the fix, not a silent skip:
// creating the account here would fork from what seed.sql defines.
func devLoginAccount(ctx context.Context, repos *repo.Repos, email, hash string) (*domain.User, error) {
	u, err := repos.Users.GetByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("no %s row: run scripts/seed.sql first", email)
	}
	if err := repos.Users.SetPassword(ctx, u.ID, hash); err != nil {
		return nil, fmt.Errorf("set dev password for %s: %w", email, err)
	}
	if err := repos.Users.MarkEmailVerified(ctx, u.ID); err != nil {
		return nil, fmt.Errorf("verify %s: %w", email, err)
	}
	return u, nil
}

// ensureAdmin2FA enrolls and confirms a TOTP factor for the seed admin, so the
// admin 2FA gate (which blocks every /admin route for unenrolled admins) lets
// a reviewer through. The secret never touches disk: it is sealed into the
// database by Enroll2FA and printed once as an otpauth URL for the reviewer's
// authenticator app. Already-enrolled admins are left alone.
func ensureAdmin2FA(ctx context.Context, repos *repo.Repos, auth *service.Auth, adminID string) (otpauth string, recovery []string, err error) {
	st, err := repos.TFA.Get(ctx, adminID)
	if err != nil {
		return "", nil, err
	}
	if st != nil && st.EnabledAt != nil {
		return "", nil, nil
	}
	secret, otpauthURL, err := auth.Enroll2FA(ctx, adminID, devPassword)
	if err != nil {
		return "", nil, fmt.Errorf("enroll admin 2FA: %w", err)
	}
	code, err := security.TOTPCode(secret, time.Now())
	if err != nil {
		return "", nil, fmt.Errorf("sign admin 2FA code: %w", err)
	}
	codes, err := auth.Confirm2FA(ctx, adminID, code)
	if err != nil {
		return "", nil, fmt.Errorf("confirm admin 2FA: %w", err)
	}
	return otpauthURL, codes, nil
}
