//go:build !race

package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/security"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// This file is `package service`, not `service_test`, for one reason: the
// assertions below need the sealed TOTP secret and the configured JWT secret,
// both of which are deliberately unexported. Exporting either purely for a test
// would put test-only surface on the production type, which is the thing
// internal/security and internal/testutil go out of their way to avoid.
//
// testutil imports repo and domain only, so there is no import cycle.

const testPassword = "correct-horse-battery"

func newTestAuth(t *testing.T, h *testutil.H) *Auth {
	t.Helper()
	cfg := config.Config{
		JWTSecret: "test-secret-for-the-2fa-gate-tests-only-0123456789abcdef",
		AppEnv:    "test",
	}
	return NewAuth(h.Repos, cfg, nil, slog.New(slog.DiscardHandler))
}

// userWithPassword inserts a user whose password actually verifies, because
// every step-up assertion is about the password being checked.
func userWithPassword(t *testing.T, h *testutil.H) *domain.User {
	t.Helper()
	u := testutil.User(t, h)
	hash, err := security.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := h.Repos.Users.SetPassword(fixtureCtx(t), u.ID, hash); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	// The returned struct still carries the fixture's placeholder hash; reload so
	// callers see what the database holds.
	fresh, err := h.Repos.Users.GetByID(fixtureCtx(t), u.ID)
	if err != nil || fresh == nil {
		t.Fatalf("reload user: %v (user=%v)", err, fresh)
	}
	return fresh
}

func fixtureCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// currentCode returns a valid TOTP for the user's enrolled secret. Read back out
// of the database and decrypted rather than captured from Enroll2FA, so the test
// exercises the same at-rest-encryption path production does.
func currentCode(t *testing.T, a *Auth, userID string) string {
	t.Helper()
	ctx := fixtureCtx(t)
	st, err := a.repos.TFA.Get(ctx, userID)
	if err != nil || st == nil {
		t.Fatalf("TFA.Get: %v (st=%v)", err, st)
	}
	secret, err := a.openSecret(st.SecretEncrypted)
	if err != nil {
		t.Fatalf("openSecret: %v", err)
	}
	code, err := security.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	return code
}

func enrolledUser(t *testing.T, h *testutil.H, a *Auth) *domain.User {
	t.Helper()
	u := userWithPassword(t, h)
	if _, _, err := a.Enroll2FA(fixtureCtx(t), u.ID, testPassword); err != nil {
		t.Fatalf("Enroll2FA: %v", err)
	}
	if _, err := a.Confirm2FA(fixtureCtx(t), u.ID, currentCode(t, a, u.ID)); err != nil {
		t.Fatalf("Confirm2FA: %v", err)
	}
	return u
}

func isEnabled(t *testing.T, h *testutil.H, userID string) bool {
	t.Helper()
	st, err := h.Repos.TFA.Get(fixtureCtx(t), userID)
	if err != nil {
		t.Fatalf("TFA.Get: %v", err)
	}
	return st != nil && st.EnabledAt != nil
}

// TestIssueSessionForUserHonoursTheTwoFAGate is the regression test for the
// OAuth 2FA bypass.
//
// IssueSessionForUser is the path a Google login takes. It used to call
// createSession directly, so it never consulted the TOTP table: an account with
// a second factor enrolled received a FULL SESSION from an OAuth login and was
// never asked for a code. Any compromise that could also drive a Google login -
// a phished password, an unrevoked Google session, which routinely outlives a
// password change - was therefore sufficient to take over a 2FA account.
//
// The password path went through Login, which had the gate. Two paths, one
// control, and only one of them enforced it.
func TestIssueSessionForUserHonoursTheTwoFAGate(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)
	u := enrolledUser(t, h, a)

	tokens, err := a.IssueSessionForUser(ctx, u, "test-agent")
	if !errors.Is(err, domain.Err2FARequired) {
		t.Fatalf("IssueSessionForUser on an enrolled account: err = %v, want Err2FARequired", err)
	}
	if tokens == nil || tokens.AccessToken != "" {
		t.Fatalf("an access token was minted despite 2FA being enrolled: %+v", tokens)
	}
	if tokens == nil || tokens.RefreshToken == "" {
		t.Fatal("expected a 2FA challenge in RefreshToken")
	}

	// The challenge must actually be redeemable, or the fix is a dead end: the
	// user is told to enter a code for a token that cannot be exchanged for one.
	claims, perr := security.ParseToken(a.cfg.JWTSecret, tokens.RefreshToken, security.Token2FAChallenge)
	if perr != nil {
		t.Fatalf("challenge is not a valid 2FA challenge token: %v", perr)
	}
	if claims.UserID != u.ID {
		t.Fatalf("challenge is for user %q, want %q", claims.UserID, u.ID)
	}
}

// TestIssueSessionForUserStillSucceedsWithoutAnEnrolledFactor is the negative
// case, so the gate above cannot be "fixed" by refusing everyone.
func TestIssueSessionForUserStillSucceedsWithoutAnEnrolledFactor(t *testing.T) {
	h := testutil.New(t)
	a := newTestAuth(t, h)
	u := testutil.User(t, h)

	tokens, err := a.IssueSessionForUser(fixtureCtx(t), u, "test-agent")
	if err != nil {
		t.Fatalf("IssueSessionForUser with no 2FA enrolled: %v", err)
	}
	if tokens == nil || tokens.AccessToken == "" {
		t.Fatal("expected an access token")
	}
	if tokens.RefreshToken == "" {
		t.Fatal("expected a refresh token")
	}
}

// TestChallengeTTLDoesNotOutliveTheReplayWindow pins the relationship between
// the two constants. A challenge that outlives the code window lets an
// interceptor spend it against a code captured much later.
func TestChallengeTTLDoesNotOutliveTheReplayWindow(t *testing.T) {
	if twoFAChallengeTTL >= usedTOTPStepTTL {
		t.Errorf("a 2FA challenge lives for %v but a consumed TOTP step is remembered for %v; "+
			"the challenge should expire first", twoFAChallengeTTL, usedTOTPStepTTL)
	}
}

// TestMFAChangesRequireStepUp covers the second gap: a valid 15-minute access
// token used to be enough to rewrite MFA state.
//
// The realistic sources of a leaked access token are an XSS, a shared or kiosk
// browser, a tab never closed after logout, or a proxy log. None of those should
// be able to silently remove the second factor and leave the account reachable by
// password alone - which is exactly the risk 2FA exists to bound.
//
// OWASP ASVS 3.3.1 / 2.5 require re-authentication before a security setting
// changes. The password is the right credential here: a TOTP code does not exist
// yet at enrollment, and Disable2FA already demands one.
func TestMFAChangesRequireStepUp(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)
	u := userWithPassword(t, h)

	t.Run("enroll refuses without the password", func(t *testing.T) {
		if _, _, err := a.Enroll2FA(ctx, u.ID, ""); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("no password: err = %v, want ErrValidation", err)
		}
		if _, _, err := a.Enroll2FA(ctx, u.ID, "wrong-password"); !errors.Is(err, domain.ErrInvalidCreds) {
			t.Fatalf("wrong password: err = %v, want ErrInvalidCreds", err)
		}
		// A refused step-up must leave nothing behind, or the next attempt would
		// look like it was already enrolled.
		st, err := h.Repos.TFA.Get(ctx, u.ID)
		if err != nil {
			t.Fatalf("TFA.Get: %v", err)
		}
		if st != nil {
			t.Fatal("a refused Enroll2FA left a TOTP row behind")
		}
	})

	if _, _, err := a.Enroll2FA(ctx, u.ID, testPassword); err != nil {
		t.Fatalf("Enroll2FA with the password: %v", err)
	}
	if _, err := a.Confirm2FA(ctx, u.ID, currentCode(t, a, u.ID)); err != nil {
		t.Fatalf("Confirm2FA: %v", err)
	}

	t.Run("enroll refuses to overwrite an enabled factor", func(t *testing.T) {
		// Otherwise a token holder could mint a secret they know and silently
		// replace a working authenticator, leaving the owner's phone showing
		// codes that no longer work.
		if _, _, err := a.Enroll2FA(ctx, u.ID, testPassword); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("re-enroll over an enabled factor: err = %v, want ErrValidation", err)
		}
	})

	t.Run("disable refuses without the password", func(t *testing.T) {
		// A genuinely VALID code, so the only thing that can refuse this is the
		// step-up. That is what proves the ordering: stepUp runs first.
		code := currentCode(t, a, u.ID)
		if err := a.Disable2FA(ctx, u.ID, code, ""); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("no password: err = %v, want ErrValidation", err)
		}
		if !isEnabled(t, h, u.ID) {
			t.Fatal("a refused Disable2FA turned 2FA off anyway")
		}
		if err := a.Disable2FA(ctx, u.ID, code, "wrong-password"); !errors.Is(err, domain.ErrInvalidCreds) {
			t.Fatalf("wrong password: err = %v, want ErrInvalidCreds", err)
		}
		if !isEnabled(t, h, u.ID) {
			t.Fatal("a refused Disable2FA turned 2FA off anyway")
		}
	})

	t.Run("disable succeeds with the password", func(t *testing.T) {
		if err := a.Disable2FA(ctx, u.ID, currentCode(t, a, u.ID), testPassword); err != nil {
			t.Fatalf("Disable2FA with the password: %v", err)
		}
		if isEnabled(t, h, u.ID) {
			t.Fatal("2FA is still enabled after a successful disable")
		}
	})
}

// TestDisable2FARejectsAPendingEnrollment covers a guard that tested only
// `st == nil`. A pending, never-confirmed enrollment used to be accepted as
// "disable 2FA": a no-op that returned 204, which reads to a client as "2FA is
// off" when it was never on.
func TestDisable2FARejectsAPendingEnrollment(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)
	u := userWithPassword(t, h)

	// Enroll but do NOT confirm: a row exists, enabled_at is NULL.
	if _, _, err := a.Enroll2FA(ctx, u.ID, testPassword); err != nil {
		t.Fatalf("Enroll2FA: %v", err)
	}
	if isEnabled(t, h, u.ID) {
		t.Fatal("fixture is wrong: the factor is already enabled")
	}
	if err := a.Disable2FA(ctx, u.ID, "000000", testPassword); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Disable2FA on a pending enrollment: err = %v, want ErrValidation", err)
	}
}

// TestStepUpDoesNotLeakAccountExistence guards the error-message choice. A
// distinct "no such user" error would turn the step-up into an account-existence
// oracle for anyone holding a token.
func TestStepUpDoesNotLeakAccountExistence(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)
	u := testutil.User(t, h)

	_, _, unknownUser := a.Enroll2FA(ctx, util.NewUUID(), testPassword)
	_, _, wrongPassword := a.Enroll2FA(ctx, u.ID, "not-the-password")

	if !errors.Is(unknownUser, domain.ErrInvalidCreds) {
		t.Errorf("unknown user: err = %v, want ErrInvalidCreds", unknownUser)
	}
	if !errors.Is(wrongPassword, domain.ErrInvalidCreds) {
		t.Errorf("wrong password: err = %v, want ErrInvalidCreds", wrongPassword)
	}
	if got, want := domain.FromError(unknownUser).Code, domain.FromError(wrongPassword).Code; got != want {
		t.Errorf("the two failures are distinguishable (%q vs %q), which is an account-existence oracle", got, want)
	}
}
