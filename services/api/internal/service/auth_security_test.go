//go:build !race

package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/security"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// ChangePassword, ChangeEmail and CancelDeletion had no coverage. They are the
// credential-changing paths, so what they refuse matters more than what they do.

// newTestAuthPool uses the pool rather than the test transaction: ChangePassword
// opens its own transaction (password write + session revocation must be atomic)
// and Repos.Pool() is nil inside a tx-bound Repos.
func newTestAuthPool(t *testing.T) (*Auth, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	h.UsePool()
	cfg := config.Config{
		AppEnv:    "test",
		PublicURL: "https://bizverse.test",
		JWTSecret: "test-signing-secret-for-the-handler-harness-0123456789",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sender := email.NewSender(email.ResendConfig{AppEnv: "test", PublicURL: cfg.PublicURL})
	return NewAuth(h.Repos, cfg, sender, logger), h
}

func userWithHash(t *testing.T, h *testutil.H, password string) *domain.User {
	t.Helper()
	u := testutil.User(t, h)
	hash, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := h.Repos.Users.SetPassword(context.Background(), u.ID, hash); err != nil {
		t.Fatalf("set password: %v", err)
	}
	return u
}

func newSession(t *testing.T, h *testutil.H, userID string) string {
	t.Helper()
	id := util.NewUUID()
	h.ExecPool(t, `INSERT INTO sessions (id, user_id, token_hash) VALUES ($1, $2, $3)`, id, userID, "h-"+id)
	return id
}

func sessionRevoked(t *testing.T, h *testutil.H, id string) bool {
	t.Helper()
	var revoked bool
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT revoked_at IS NOT NULL FROM sessions WHERE id=$1`, id).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	return revoked
}

func TestChangePasswordRefuses(t *testing.T) {
	svc, h := newTestAuthPool(t)
	ctx := context.Background()
	user := userWithHash(t, h, "old-password-1")

	if err := svc.ChangePassword(ctx, util.NewUUID(), "old-password-1", "new-password-1", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user: err = %v, want ErrNotFound", err)
	}
	// The current password is the authorisation for the change.
	if err := svc.ChangePassword(ctx, user.ID, "wrong-password-1", "new-password-1", ""); !errors.Is(err, domain.ErrInvalidCreds) {
		t.Errorf("wrong current password: err = %v, want ErrInvalidCreds", err)
	}
	bad := []string{
		"short1",     // under 8
		"allletters", // no digit
		"12345678",   // no letter
		"abcdefgh",   // no digit
		"        ",   // whitespace only
	}
	for _, pw := range bad {
		if err := svc.ChangePassword(ctx, user.ID, "old-password-1", pw, ""); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%q: err = %v, want ErrValidation", pw, err)
		}
	}
	// A refused change must not have rotated anything.
	after, err := h.Repos.Users.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := security.VerifyPassword("old-password-1", after.PasswordHash); !ok {
		t.Error("the old password stopped working after refused changes")
	}
}

func TestChangePasswordRotatesAndRevokesOtherSessions(t *testing.T) {
	svc, h := newTestAuthPool(t)
	ctx := context.Background()
	user := userWithHash(t, h, "old-password-1")
	keep := newSession(t, h, user.ID)
	other := newSession(t, h, user.ID)

	if err := svc.ChangePassword(ctx, user.ID, "old-password-1", "new-password-2", keep); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	after, err := h.Repos.Users.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := security.VerifyPassword("old-password-1", after.PasswordHash); ok {
		t.Error("the old password still works")
	}
	if ok, _ := security.VerifyPassword("new-password-2", after.PasswordHash); !ok {
		t.Error("the new password does not work")
	}
	// Every other session is killed; the caller keeps theirs.
	if sessionRevoked(t, h, other) != true {
		t.Error("a different session survived a password change")
	}
	if sessionRevoked(t, h, keep) {
		t.Error("the caller's own session was revoked")
	}
}

func TestChangePasswordWithoutASessionRevokesAll(t *testing.T) {
	svc, h := newTestAuthPool(t)
	ctx := context.Background()
	user := userWithHash(t, h, "old-password-1")
	a := newSession(t, h, user.ID)
	b := newSession(t, h, user.ID)

	// "" means the caller has no refresh cookie. It must revoke everything
	// rather than compare the empty string inside a uuid query.
	if err := svc.ChangePassword(ctx, user.ID, "old-password-1", "new-password-2", ""); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if !sessionRevoked(t, h, a) || !sessionRevoked(t, h, b) {
		t.Error("not every session was revoked when no session was kept")
	}
}

func TestChangeEmailRefuses(t *testing.T) {
	svc, h := newTestAuthPool(t)
	ctx := context.Background()
	user := userWithHash(t, h, "old-password-1")
	taken := testutil.User(t, h)

	// The format is checked before any lookup.
	if err := svc.ChangeEmail(ctx, user.ID, "old-password-1", "not-an-email", ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("bad email: err = %v, want ErrValidation", err)
	}
	if err := svc.ChangeEmail(ctx, user.ID, "wrong-password-1", "new@example.test", ""); !errors.Is(err, domain.ErrInvalidCreds) {
		t.Errorf("wrong password: err = %v, want ErrInvalidCreds", err)
	}
	if err := svc.ChangeEmail(ctx, user.ID, "old-password-1", taken.Email, ""); !errors.Is(err, domain.ErrEmailTaken) {
		t.Errorf("taken email: err = %v, want ErrEmailTaken", err)
	}
	if err := svc.ChangeEmail(ctx, util.NewUUID(), "old-password-1", "new@example.test", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user: err = %v, want ErrNotFound", err)
	}
}

func TestChangeEmailUpdatesAndNormalises(t *testing.T) {
	svc, h := newTestAuthPool(t)
	ctx := context.Background()
	user := userWithHash(t, h, "old-password-1")

	if err := svc.ChangeEmail(ctx, user.ID, "old-password-1", "  New.Address@Example.TEST  ", ""); err != nil {
		t.Fatalf("ChangeEmail: %v", err)
	}
	after, err := h.Repos.Users.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Email != "new.address@example.test" {
		t.Errorf("Email = %q, want it trimmed and lowercased", after.Email)
	}
	// The password is untouched by an email change.
	if ok, _ := security.VerifyPassword("old-password-1", after.PasswordHash); !ok {
		t.Error("an email change altered the password")
	}
}

func TestCancelDeletion(t *testing.T) {
	svc, h := newTestAuthPool(t)
	ctx := context.Background()
	user := userWithHash(t, h, "old-password-1")

	if err := svc.CancelDeletion(ctx, util.NewUUID(), "old-password-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user: err = %v, want ErrNotFound", err)
	}
	// A stolen access token must not resurrect an account, so the password is
	// required even though the session is already authenticated.
	if err := svc.CancelDeletion(ctx, user.ID, "wrong-password-1"); !errors.Is(err, domain.ErrInvalidCreds) {
		t.Errorf("wrong password: err = %v, want ErrInvalidCreds", err)
	}
	// Nothing to cancel is not an error.
	if err := svc.CancelDeletion(ctx, user.ID, "old-password-1"); err != nil {
		t.Errorf("account not deleted: %v", err)
	}

	h.ExecPool(t, `UPDATE users SET deleted_at=now() WHERE id=$1`, user.ID)
	if err := svc.CancelDeletion(ctx, user.ID, "old-password-1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	after, err := h.Repos.Users.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.DeletedAt != nil {
		t.Error("deleted_at was not cleared")
	}
}
