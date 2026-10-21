//go:build !race

package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"bizverse/api/internal/config"
	"bizverse/api/internal/email"
	"bizverse/api/internal/ws"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// The Profiles moderation tail - Contact, Appeal, Appeals, DecideAppeal,
// Anomalies, ResolveAnomaly - had no coverage. Together they are the support
// inbox and the appeal path for suspended accounts.

func newTestProfiles(t *testing.T) (*Profiles, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	notifier := NewNotifier(h.Repos, cfg, email.NewSender(email.ResendConfig{AppEnv: "test"}), ws.NewHub(logger, nil), logger)
	return NewProfiles(h.Repos, notifier), h
}

func TestProfilesContactValidatesAndStores(t *testing.T) {
	svc, h := newTestProfiles(t)
	ctx := context.Background()
	user := testutil.User(t, h)

	// Subject 1-120 runes, message 10-4000.
	bad := []struct{ name, subject, message string }{
		{"an empty subject", "   ", "a long enough message"},
		{"a 121-rune subject", strings.Repeat("s", 121), "a long enough message"},
		{"a message that is too short", "Help", "short"},
		{"a message that is too long", "Help", strings.Repeat("m", 4001)},
	}
	for _, tc := range bad {
		if err := svc.Contact(ctx, user.ID, tc.subject, tc.message); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", tc.name, err)
		}
	}

	if err := svc.Contact(ctx, util.NewUUID(), "Help", "a long enough message"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user: err = %v, want ErrNotFound", err)
	}

	if err := svc.Contact(ctx, user.ID, "  Cannot log in  ", "  my password reset never arrived  "); err != nil {
		t.Fatalf("Contact: %v", err)
	}
	var targetType, targetID, reason string
	if err := h.QueryRow(t, `SELECT target_type, target_id, reason FROM reports WHERE reporter_id=$1`, user.ID).
		Scan(&targetType, &targetID, &reason); err != nil {
		t.Fatal(err)
	}
	if targetType != "support" {
		t.Errorf("target_type = %q, want support", targetType)
	}
	if targetID != user.Email {
		t.Errorf("target_id = %q, want the reporter's email", targetID)
	}
	// Both halves are trimmed before they are joined.
	if reason != "Cannot log in\n\nmy password reset never arrived" {
		t.Errorf("reason = %q, want the trimmed subject and message", reason)
	}
}

func insertAppeal(t *testing.T, h *testutil.H, userID, reason string) string {
	t.Helper()
	id := util.NewUUID()
	h.Exec(t, `INSERT INTO appeals (id, user_id, reason) VALUES ($1, $2, $3)`, id, userID, reason)
	return id
}

func TestProfilesAppeal(t *testing.T) {
	svc, h := newTestProfiles(t)
	ctx := context.Background()
	user := testutil.User(t, h)

	if err := svc.Appeal(ctx, user.ID, "short"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("short reason: err = %v, want ErrValidation", err)
	}
	if err := svc.Appeal(ctx, user.ID, strings.Repeat("a", 2001)); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("long reason: err = %v, want ErrValidation", err)
	}

	if err := svc.Appeal(ctx, user.ID, "I believe the suspension was a mistake."); err != nil {
		t.Fatalf("Appeal: %v", err)
	}
	// One open appeal at a time, or the queue fills with duplicates.
	if err := svc.Appeal(ctx, user.ID, "I am writing again about the same thing."); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("second open appeal: err = %v, want ErrValidation", err)
	}
	var n int
	if err := h.QueryRow(t, `SELECT count(*) FROM appeals WHERE user_id=$1`, user.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d appeals stored, want 1", n)
	}
}

func TestProfilesAppealsListsByStatus(t *testing.T) {
	svc, h := newTestProfiles(t)
	ctx := context.Background()
	a := testutil.User(t, h)
	b := testutil.User(t, h)
	openA := insertAppeal(t, h, a.ID, "please reconsider this suspension")
	openB := insertAppeal(t, h, b.ID, "the suspension was applied to the wrong account")
	resolved := insertAppeal(t, h, b.ID, "an older appeal that was already handled")
	h.Exec(t, `UPDATE appeals SET status='resolved', decision='reject', resolved_at=now() WHERE id=$1`, resolved)

	got, err := svc.Appeals(ctx, "open")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("open appeals = %d, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, row := range got {
		seen[row["id"].(string)] = true
		// The queue needs the account's current state to make sense of a request.
		if row["user_status"] == "" || row["email"] == "" || row["name"] == "" {
			t.Errorf("row %v is missing the joined user fields", row["id"])
		}
	}
	if !seen[openA] || !seen[openB] {
		t.Error("an open appeal is missing from the queue")
	}
	if seen[resolved] {
		t.Error("a resolved appeal was listed as open")
	}
}

func TestProfilesDecideAppeal(t *testing.T) {
	svc, h := newTestProfiles(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)

	suspended := testutil.User(t, h)
	h.Exec(t, `UPDATE users SET status='suspended', suspended_until=now() + interval '7 days' WHERE id=$1`, suspended.ID)
	appeal := insertAppeal(t, h, suspended.ID, "I believe the suspension was a mistake.")

	if err := svc.DecideAppeal(ctx, admin.ID, util.NewUUID(), "approve"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown appeal: err = %v, want ErrNotFound", err)
	}
	// A bad decision must not resolve the appeal.
	if err := svc.DecideAppeal(ctx, admin.ID, appeal, "maybe"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("bad decision: err = %v, want ErrValidation", err)
	}
	var status string
	if err := h.QueryRow(t, `SELECT status FROM appeals WHERE id=$1`, appeal).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "open" {
		t.Errorf("appeal status = %q after a refused decision, want open", status)
	}

	if err := svc.DecideAppeal(ctx, admin.ID, appeal, "approve"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	// Approving lifts the suspension and clears the end date, or the account
	// stays suspended by a timestamp even though its status says active.
	after, err := h.Repos.Users.GetByID(ctx, suspended.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.UserStatusActive {
		t.Errorf("user status = %q, want active", after.Status)
	}
	if after.SuspendedUntil != nil {
		t.Errorf("suspended_until = %v, want nil", after.SuspendedUntil)
	}
	var decision string
	if err := h.QueryRow(t, `SELECT decision FROM appeals WHERE id=$1`, appeal).Scan(&decision); err != nil {
		t.Fatal(err)
	}
	if decision != "approve" {
		t.Errorf("decision = %q", decision)
	}
	// Resolved appeals leave the open queue.
	if err := svc.DecideAppeal(ctx, admin.ID, appeal, "approve"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second decision: err = %v, want ErrNotFound", err)
	}
}

func TestProfilesDecideAppealRejectKeepsTheSuspension(t *testing.T) {
	svc, h := newTestProfiles(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	user := testutil.User(t, h)
	h.Exec(t, `UPDATE users SET status='suspended' WHERE id=$1`, user.ID)
	appeal := insertAppeal(t, h, user.ID, "I believe the suspension was a mistake.")

	if err := svc.DecideAppeal(ctx, admin.ID, appeal, "reject"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	after, err := h.Repos.Users.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.UserStatusSuspended {
		t.Errorf("a rejected appeal lifted the suspension: %q", after.Status)
	}
}

func TestProfilesAnomaliesAndResolve(t *testing.T) {
	svc, h := newTestProfiles(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Trending Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	user := testutil.User(t, h)

	// Two normal views and one flagged burst on the same signal.
	for i := 0; i < 2; i++ {
		h.Exec(t, `INSERT INTO engagement_events (user_id, target_type, target_id, signal, weight, dedupe_key)
			VALUES ($1, 'business', $2, 'view', 1, $3)`, user.ID, biz.ID, util.NewUUID())
	}
	flagged := util.NewUUID()
	h.Exec(t, `INSERT INTO engagement_events (user_id, target_type, target_id, signal, weight, dedupe_key, flagged)
		VALUES ($1, 'business', $2, 'view', 9, $3, true)`, user.ID, biz.ID, flagged)

	got, err := svc.Anomalies(ctx)
	if err != nil {
		t.Fatalf("Anomalies: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("anomalies = %d, want 1", len(got))
	}
	row := got[0]
	if row["business_name"] != "Trending Cafe" {
		t.Errorf("business_name = %v", row["business_name"])
	}
	if row["business_slug"] != biz.Slug {
		t.Errorf("business_slug = %v, want %q", row["business_slug"], biz.Slug)
	}
	// normal_count is the unflagged baseline for the same signal, so a reviewer
	// can see how far out of band the burst was.
	if row["normal_count"] != 2 {
		t.Errorf("normal_count = %v, want 2", row["normal_count"])
	}

	eventID, _ := row["id"].(string)
	if eventID == "" {
		t.Fatalf("the anomaly row has no usable id: %v", row["id"])
	}
	if err := svc.ResolveAnomaly(ctx, eventID); err != nil {
		t.Fatalf("ResolveAnomaly: %v", err)
	}
	if got, err = svc.Anomalies(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("the anomaly is still listed after being resolved: %v", got)
	}
}
