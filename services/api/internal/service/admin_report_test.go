//go:build !race

package service

import (
	"context"
	"errors"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// admin.DecideReport had no coverage - 40 statements. It is the moderation
// decision path: it hides content, suspends people and writes the audit trail,
// so a partially-applied decision is worse than a refused one.

func insertReport(t *testing.T, h *testutil.H, reporterID, targetType, targetID, reason string) string {
	t.Helper()
	id := util.NewUUID()
	h.Exec(t, `INSERT INTO reports (id, reporter_id, target_type, target_id, reason)
		VALUES ($1, $2, $3, $4, $5)`, id, reporterID, targetType, targetID, reason)
	return id
}

func reportStatus(t *testing.T, h *testutil.H, id string) string {
	t.Helper()
	var s string
	if err := h.QueryRow(t, `SELECT status FROM reports WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAdminDecideReportRefusesUnknownAndResolved(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	reporter := testutil.User(t, h)
	id := insertReport(t, h, reporter.ID, "user", admin.ID, "being rude")

	if err := svc.DecideReport(ctx, admin.ID, util.NewUUID(), "dismiss", "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown report: err = %v, want ErrNotFound", err)
	}

	if err := svc.DecideReport(ctx, admin.ID, id, "dismiss", "not actionable"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	// A resolved report cannot be decided twice.
	if err := svc.DecideReport(ctx, admin.ID, id, "dismiss", "again"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("second decision: err = %v, want ErrValidation", err)
	}
}

func TestAdminDecideReportUnknownAction(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	reporter := testutil.User(t, h)
	id := insertReport(t, h, reporter.ID, "user", admin.ID, "being rude")

	if err := svc.DecideReport(ctx, admin.ID, id, "explode", "x"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
	if got := reportStatus(t, h, id); got != "open" {
		t.Errorf("status = %q after an unknown action, want it still open", got)
	}
	var n int
	if err := h.QueryRow(t, `SELECT count(*) FROM moderation_actions WHERE target_id=$1`, admin.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d audit rows written for an unknown action", n)
	}
}

func TestAdminDecideReportDismiss(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	reporter := testutil.User(t, h)
	id := insertReport(t, h, reporter.ID, "user", admin.ID, "being rude")

	if err := svc.DecideReport(ctx, admin.ID, id, "dismiss", "no rule broken"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if got := reportStatus(t, h, id); got != "dismissed" {
		t.Errorf("status = %q, want dismissed", got)
	}
	var resolvedBy *string
	if err := h.QueryRow(t, `SELECT resolved_by::text FROM reports WHERE id=$1`, id).Scan(&resolvedBy); err != nil {
		t.Fatal(err)
	}
	if resolvedBy == nil || *resolvedBy != admin.ID {
		t.Errorf("resolved_by = %v, want %s", resolvedBy, admin.ID)
	}
	// The audit row must be writable: "dismiss" is not in the action CHECK, and
	// writing it failed the insert after the report had already been dismissed.
	// A dismissal is recorded as "reject" with the report's own status carrying
	// the distinction.
	var action string
	if err := h.QueryRow(t, `SELECT action FROM moderation_actions WHERE target_id=$1`, admin.ID).Scan(&action); err != nil {
		t.Fatalf("reading the audit row: %v", err)
	}
	if action != "reject" {
		t.Errorf("audit action = %q, want reject", action)
	}
}

func TestAdminDecideReportHideHidesTheContent(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	owner := testutil.User(t, h)
	reporter := testutil.User(t, h)
	biz, _ := pendingBusiness(t, h)
	_ = owner
	review := testutil.Review(t, h, biz.ID, reporter.ID, 1)
	id := insertReport(t, h, reporter.ID, "review", review.ID, "abusive")

	if err := svc.DecideReport(ctx, admin.ID, id, "hide", "abusive review"); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if got := reportStatus(t, h, id); got != "resolved" {
		t.Errorf("report status = %q, want resolved", got)
	}
	var status string
	if err := h.QueryRow(t, `SELECT status FROM reviews WHERE id=$1`, review.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "hidden" {
		t.Errorf("review status = %q, want hidden", status)
	}
}

func TestAdminDecideReportSuspendTargetsTheUser(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	reporter := testutil.User(t, h)
	offender := testutil.User(t, h)
	id := insertReport(t, h, reporter.ID, "user", offender.ID, "harassment")

	if err := svc.DecideReport(ctx, admin.ID, id, "suspend", "seven days"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	var status string
	if err := h.QueryRow(t, `SELECT status::text FROM users WHERE id=$1`, offender.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.UserStatusSuspended) {
		t.Errorf("user status = %q, want suspended", status)
	}
	// The reporter is untouched.
	if after, _ := h.Repos.Users.GetByID(context.Background(), reporter.ID); after.Status != domain.UserStatusActive {
		t.Errorf("the reporter was suspended instead: %q", after.Status)
	}
}

// A refused action must leave the report OPEN. Resolving first and then
// returning an error makes the decision permanent: the content was never
// touched, no audit row was written, and the retry fails with "Report already
// resolved."
func TestAdminDecideReportRefusedActionLeavesReportOpen(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	reporter := testutil.User(t, h)
	biz, _ := pendingBusiness(t, h)
	review := testutil.Review(t, h, biz.ID, reporter.ID, 1)

	// delete-for-everyone is a messages-only action (PRD §5.8.2).
	id := insertReport(t, h, reporter.ID, "review", review.ID, "abusive")
	if err := svc.DecideReport(ctx, admin.ID, id, "delete_for_everyone", "x"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if got := reportStatus(t, h, id); got != "open" {
		t.Errorf("status = %q after a refused action, want open - the decision is now unrepeatable", got)
	}
	if err := h.QueryRow(t, `SELECT status FROM reviews WHERE id=$1`, review.ID).Scan(new(string)); err != nil {
		t.Fatal(err)
	}

	// The same applies when the content author cannot be resolved: warn and
	// suspend both need one.
	id2 := insertReport(t, h, reporter.ID, "review", util.NewUUID(), "ghost content")
	if err := svc.DecideReport(ctx, admin.ID, id2, "warn", "x"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("warn on missing content: err = %v, want ErrValidation", err)
	}
	if got := reportStatus(t, h, id2); got != "open" {
		t.Errorf("status = %q after a refused warn, want open", got)
	}
}
