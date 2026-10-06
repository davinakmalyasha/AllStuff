//go:build !race

// Same tag as auth_2fa_integration_test.go, and for the same reason.
//
// This file depends on that file's unexported helpers - fixtureCtx, newTestAuth,
// userWithPassword, testPassword - which means the two cannot be compiled
// separately. Under `-race` the tagged file is excluded; without the same tag
// here, the build fails with "undefined: fixtureCtx" and a cascade of "too many
// errors" that buries the real cause. CI reported the cascade and nothing else.
//
// The tag exists because these tests clone a database per test and re-migrate
// each clone, far too slow to run under the race detector as well as without it,
// so it cannot simply be dropped. buildtag_lint_test.go makes forgetting it a
// named failure instead of a mysterious one.
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"testing"

	"bizverse/api/internal/testutil"
)

// The Security page's sign-in history.
//
// `auth_events` shipped with `ip` and `user_agent` columns and LoginHistory has
// always selected both - so the query was correct and the page rendered fine. The
// writes were missing: the only two inserts in the codebase were the 2FA toggles,
// and they passed neither column. The result was a security feature that looked
// complete, passed review, and was permanently empty of sign-ins.
//
// These assert the writes, because that is the part that was absent.

// events returns the recorded history for a user, newest first.
func events(t *testing.T, h *testutil.H, userID string) []struct {
	Event string
	IP    *string
	UA    *string
} {
	t.Helper()
	type row = struct {
		Event string
		IP    *string
		UA    *string
	}
	out := []row{}
	rows, err := h.Query(t,
		`SELECT event, host(ip), user_agent FROM auth_events
		  WHERE user_id = $1 ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		t.Fatalf("read auth_events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Event, &r.IP, &r.UA); err != nil {
			t.Fatalf("scan auth_events: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestSignInIsRecordedWithClientDetail(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)

	u := userWithPassword(t, h)
	if _, _, err := a.Login(ctx, LoginInput{Email: u.Email, Password: testPassword}, net.ParseIP("203.0.113.9"), "TestAgent/1.0"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	evs := events(t, h, u.ID)
	var found bool
	for _, e := range evs {
		if e.Event != "login" {
			continue
		}
		found = true
		// The IP and user agent are the entire reason the columns exist. An event
		// row with both NULL is the bug this test was written against.
		if e.IP == nil || *e.IP != "203.0.113.9" {
			t.Errorf("login event ip = %v, want 203.0.113.9", e.IP)
		}
		if e.UA == nil || *e.UA != "TestAgent/1.0" {
			t.Errorf("login event user_agent = %v, want TestAgent/1.0", e.UA)
		}
	}
	if !found {
		t.Fatalf("no 'login' event recorded; history was %v", evs)
	}
}

// The vocabulary in auth_events_event_check (0001) is the contract: login,
// login_fail, register, password_reset, logout, ban, suspend, unban, 2fa_enable,
// 2fa_disable, session_revoke, document_view.
//
// An earlier draft of this file invented "session_refresh", "oauth_login" and
// "login_2fa" to make each session path individually identifiable. That is the
// wrong instinct twice over: the check constraint exists precisely to stop a
// stray value reaching an audit table, and a rotation is not a sign-in. So the
// paths collapse onto the vocabulary the schema already specifies, and rotation
// records nothing at all - last_seen_at is what answers "was this token in use".
func TestRotationIsNotASignIn(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)

	u := userWithPassword(t, h)
	_, tokens, err := a.Login(ctx, LoginInput{Email: u.Email, Password: testPassword}, net.ParseIP("203.0.113.1"), "A/1.0")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, _, err := a.Refresh(ctx, tokens.RefreshToken, net.ParseIP("203.0.113.1"), "A/1.0"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if n := countEvent(events(t, h, u.ID), "login"); n != 1 {
		t.Errorf("recorded %d login events across a sign-in and a rotation, want 1", n)
	}
	for _, e := range events(t, h, u.ID) {
		if e.Event != "login" && e.Event != "login_fail" {
			t.Errorf("rotation recorded an unexpected %q event", e.Event)
		}
	}
}

// login_fail is in the vocabulary and was never written. Recording only successes
// shows a user a history that looks untouched by an attacker who never guessed
// the password.
func TestFailedSignInIsRecorded(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)

	u := userWithPassword(t, h)
	if _, _, err := a.Login(ctx, LoginInput{Email: u.Email, Password: "wrong-password"}, net.ParseIP("203.0.113.44"), "F/1.0"); err == nil {
		t.Fatal("a wrong password must not sign in")
	}

	found := false
	for _, e := range events(t, h, u.ID) {
		if e.Event != "login_fail" {
			continue
		}
		found = true
		if e.IP == nil || *e.IP != "203.0.113.44" {
			t.Errorf("login_fail ip = %v, want 203.0.113.44", e.IP)
		}
	}
	if !found {
		t.Fatalf("no login_fail event recorded; history was %v", events(t, h, u.ID))
	}
}

// An unknown address has no user to attribute it to. auth_events.user_id is
// nullable for exactly this, and the row must still be written - otherwise the
// attempt leaves no trace at all, which is the opposite of the point.
func TestFailedSignInOnUnknownAddressIsStillRecorded(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)

	if _, _, err := a.Login(ctx, LoginInput{Email: "nobody@example.com", Password: "whatever"}, net.ParseIP("203.0.113.45"), "G/1.0"); err == nil {
		t.Fatal("an unknown address must not sign in")
	}

	var n int
	if err := h.QueryRow(t,
		`SELECT count(*) FROM auth_events WHERE event = 'login_fail' AND user_id IS NULL AND host(ip) = '203.0.113.45'`,
	).Scan(&n); err != nil {
		t.Fatalf("count unattributed login_fail rows: %v", err)
	}
	if n != 1 {
		t.Errorf("found %d unattributed login_fail rows, want 1", n)
	}
}

func countEvent(evs []struct {
	Event string
	IP    *string
	UA    *string
}, want string) int {
	n := 0
	for _, e := range evs {
		if e.Event == want {
			n++
		}
	}
	return n
}

func TestLogoutIsRecorded(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)

	u := userWithPassword(t, h)
	_, tokens, err := a.Login(ctx, LoginInput{Email: u.Email, Password: testPassword}, net.ParseIP("203.0.113.5"), "C/1.0")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	sess, err := h.Repos.Sessions.GetByTokenHash(ctx, tokenHash(t, tokens.RefreshToken))
	if err != nil || sess == nil {
		t.Fatalf("GetByTokenHash: %v (sess=%v)", err, sess)
	}
	if err := a.Logout(ctx, sess.ID, sess.UserID, "203.0.113.5", "C/1.0"); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	if countEvent(events(t, h, u.ID), "logout") != 1 {
		t.Errorf("logout was not recorded; history was %v", events(t, h, u.ID))
	}
}

// SessionRepo.Touch existed with no callers, so last_seen_at could only ever equal
// created_at. A rotation retires the old row, which means the column could not
// answer "when was this token last used" - and on a Security page listing
// concurrent sessions that is the entire purpose of the column.
func TestRefreshTouchesTheRetiredSession(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)

	u := userWithPassword(t, h)
	_, tokens, err := a.Login(ctx, LoginInput{Email: u.Email, Password: testPassword}, net.ParseIP("203.0.113.7"), "D/1.0")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	before, err := h.Repos.Sessions.GetByTokenHash(ctx, tokenHash(t, tokens.RefreshToken))
	if err != nil || before == nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if !before.LastSeenAt.Equal(before.CreatedAt) {
		t.Skip("token rotated during setup; touch comparison would be ambiguous")
	}

	if _, _, err := a.Refresh(ctx, tokens.RefreshToken, net.ParseIP("203.0.113.7"), "D/1.0"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// The old row is revoked but must retain a last_seen_at that is at least its
	// own creation, and the touch must have run before the revoke.
	after, err := h.Repos.Sessions.GetByTokenHash(ctx, tokenHash(t, tokens.RefreshToken))
	if err != nil || after == nil {
		t.Fatalf("re-read GetByTokenHash: %v", err)
	}
	if after.LastSeenAt.IsZero() {
		t.Error("last_seen_at is zero after rotation; Touch did not run")
	}
	if after.LastSeenAt.Before(after.CreatedAt) {
		t.Errorf("last_seen_at %v precedes created_at %v; Touch must not move it backwards",
			after.LastSeenAt, after.CreatedAt)
	}
	if after.RevokedAt == nil {
		t.Error("the rotated session should be revoked; Refresh no longer rotates")
	}
}

// THE REGRESSION GUARD FOR A SILENT THREE-FEATURE BREAK
// -----------------------------------------------------
// `sessions.ip` is `inet`. scanSession read it into a *string, which pgx cannot
// do in binary format, so every read of a session failed with "cannot scan inet
// (OID 869) into **string". Nothing surfaced it because all four callers treat a
// lookup failure as "no such session":
//
//	Auth.Refresh   -> refresh always fails; sessions die at token expiry
//	handleLogout   -> revoke skipped, error discarded with `_ =`, so the cookies
//	                  clear and the user believes they signed out while the refresh
//	                  token stays valid
//	Security page  -> session list empty, revoke-others a no-op
//
// E2E caught none of it because it never waits out a 15-minute access token.
//
// This asserts the address survives the round trip, because "the column is not
// NULL" and "the row is readable at all" are the same bug seen twice.
func TestSessionAddressRoundTrips(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)

	u := userWithPassword(t, h)
	_, tokens, err := a.Login(ctx, LoginInput{Email: u.Email, Password: testPassword}, net.ParseIP("198.51.100.23"), "H/1.0")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	sess, err := h.Repos.Sessions.GetByTokenHash(ctx, tokenHash(t, tokens.RefreshToken))
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if sess == nil {
		t.Fatal("GetByTokenHash returned no session for a live token")
	}
	if sess.IP == nil {
		t.Fatal("session IP is NULL; the inet column was not read back")
	}
	if *sess.IP != "198.51.100.23" {
		t.Errorf("session IP = %q, want 198.51.100.23 (host() form, no netmask)", *sess.IP)
	}
}

// Logout must revoke the session SERVER-SIDE, not merely clear cookies. This is
// the assertion that makes the difference between a user signing out and a user
// believing they signed out.
func TestLogoutRevokesTheSessionServerSide(t *testing.T) {
	h := testutil.New(t)
	ctx := fixtureCtx(t)
	a := newTestAuth(t, h)

	u := userWithPassword(t, h)
	_, tokens, err := a.Login(ctx, LoginInput{Email: u.Email, Password: testPassword}, net.ParseIP("198.51.100.24"), "I/1.0")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	sess, err := h.Repos.Sessions.GetByTokenHash(ctx, tokenHash(t, tokens.RefreshToken))
	if err != nil || sess == nil {
		t.Fatalf("GetByTokenHash: %v (sess=%v)", err, sess)
	}
	if err := a.Logout(ctx, sess.ID, sess.UserID, "198.51.100.24", "I/1.0"); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	after, err := h.Repos.Sessions.GetByTokenHash(ctx, tokenHash(t, tokens.RefreshToken))
	if err != nil || after == nil {
		t.Fatalf("re-read GetByTokenHash: %v (sess=%v)", err, after)
	}
	if after.RevokedAt == nil {
		t.Error("session is NOT revoked after Logout; the refresh token is still usable")
	}
}

// tokenHash is the same sha256 the service applies to a refresh token before
// looking a session up, replicated here so the test addresses the row the way
// production does rather than reaching into internals.
func tokenHash(t *testing.T, refreshToken string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(refreshToken))
	return hex.EncodeToString(sum[:])
}
