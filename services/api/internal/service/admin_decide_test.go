//go:build !race

package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"allstuff/api/internal/config"
	"allstuff/api/internal/domain"
	"allstuff/api/internal/email"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
	"allstuff/api/internal/ws"
)

// admin.Decide had no coverage - 45 statements. It is the gate that grants a
// trust badge, so the level it sets, the audit row it writes, and the cases it
// refuses all matter.

func newTestAdmin(t *testing.T) (*Admin, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://allstuff.test"}
	notifier := NewNotifier(h.Repos, cfg, email.NewSender(email.ResendConfig{AppEnv: "test"}), ws.NewHub(logger, nil), logger)
	return NewAdmin(h.Repos, notifier), h
}

// pendingBusiness returns a business awaiting review plus its owner.
func pendingBusiness(t *testing.T, h *testutil.H) (*domain.Business, *domain.User) {
	t.Helper()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Pending " + owner.ID[:8], Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	// Business fixtures are created as drafts; review only sees submitted ones.
	h.Exec(t, `UPDATE businesses SET status='pending_review' WHERE id=$1`, b.ID)
	b.Status = domain.BusinessPending
	return b, owner
}

func businessState(t *testing.T, h *testutil.H, id string) (status, level string) {
	t.Helper()
	if err := h.QueryRow(t, `SELECT status::text, COALESCE(verification_level::text,'') FROM businesses WHERE id=$1`, id).
		Scan(&status, &level); err != nil {
		t.Fatal(err)
	}
	return status, level
}

func auditRows(t *testing.T, h *testutil.H, businessID string) int {
	t.Helper()
	var n int
	if err := h.QueryRow(t, `SELECT count(*) FROM moderation_actions WHERE target_type='business' AND target_id=$1`, businessID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAdminDecideOnlyPendingBusinesses(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	biz, _ := pendingBusiness(t, h)

	if _, err := svc.Decide(ctx, admin.ID, biz.ID, DecideInput{Decision: "approve"}); err != nil {
		t.Fatalf("approving a pending business: %v", err)
	}
	// A second decision on the same business must be refused: the badge is
	// granted once, and re-approving would let an admin silently rewrite it.
	before := auditRows(t, h, biz.ID)
	if _, err := svc.Decide(ctx, admin.ID, biz.ID, DecideInput{Decision: "reject", Reason: "changed my mind"}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("a second decision: err = %v, want ErrValidation", err)
	}
	if after := auditRows(t, h, biz.ID); after != before {
		t.Errorf("a refused decision wrote %d audit rows", after-before)
	}
}

func TestAdminDecideUnknownBusiness(t *testing.T) {
	svc, h := newTestAdmin(t)
	admin := testutil.Admin(t, h)
	if _, err := svc.Decide(context.Background(), admin.ID, util.NewUUID(), DecideInput{Decision: "approve"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAdminDecideUnknownDecision(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	biz, _ := pendingBusiness(t, h)

	for _, d := range []string{"", "APPROVE", "suspend"} {
		if _, err := svc.Decide(ctx, admin.ID, biz.ID, DecideInput{Decision: d, Reason: "x"}); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("decision %q: err = %v, want ErrValidation", d, err)
		}
	}
	// Nothing may be written for a decision that was never understood.
	if n := auditRows(t, h, biz.ID); n != 0 {
		t.Errorf("%d audit rows written for unknown decisions", n)
	}
	if status, _ := businessState(t, h, biz.ID); status != string(domain.BusinessPending) {
		t.Errorf("status = %q after unknown decisions, want it untouched", status)
	}
}

func TestAdminDecideApprove(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	biz, _ := pendingBusiness(t, h)

	got, err := svc.Decide(ctx, admin.ID, biz.ID, DecideInput{Decision: "approve"})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	// Approving without an explicit level grants the ordinary Verified badge,
	// never Fully Verified.
	if got.VerificationLevel == nil || *got.VerificationLevel != domain.LevelVerified {
		t.Errorf("level = %v, want %q", got.VerificationLevel, domain.LevelVerified)
	}
	status, level := businessState(t, h, biz.ID)
	if status != string(domain.BusinessVerified) {
		t.Errorf("status = %q, want approved", status)
	}
	if level != string(domain.LevelVerified) {
		t.Errorf("stored level = %q", level)
	}

	// The audit trail names the admin who granted the badge, not only the time.
	var auditedAdmin, action string
	if err := h.QueryRow(t, `
		SELECT admin_id, action FROM moderation_actions
		WHERE target_type='business' AND target_id=$1`, biz.ID).Scan(&auditedAdmin, &action); err != nil {
		t.Fatalf("reading the audit row: %v", err)
	}
	if auditedAdmin != admin.ID {
		t.Errorf("audit admin_id = %q, want %q", auditedAdmin, admin.ID)
	}
	if action != "approve" {
		t.Errorf("audit action = %q, want approve", action)
	}
}

func TestAdminDecideFullyVerifiedNeedsDocuments(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	biz, _ := pendingBusiness(t, h)
	full := domain.LevelFullyVerified

	// No documents: refused, and the business must remain pending rather than
	// being left approved at a lower level by a partially-applied call.
	if _, err := svc.Decide(ctx, admin.ID, biz.ID, DecideInput{Decision: "approve", Level: &full}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
	if status, level := businessState(t, h, biz.ID); status != string(domain.BusinessPending) || level != "" {
		t.Errorf("after a refused upgrade: status=%q level=%q, want it untouched", status, level)
	}

	// With a document on file it goes through at the higher level.
	if err := h.Repos.Businesses.AddDocument(ctx, &domain.VerificationDocument{
		ID: util.NewUUID(), BusinessID: biz.ID, Kind: "license", MediaID: util.NewUUID(), Status: "pending",
	}); err != nil {
		t.Fatalf("adding a document: %v", err)
	}
	got, err := svc.Decide(ctx, admin.ID, biz.ID, DecideInput{Decision: "approve", Level: &full})
	if err != nil {
		t.Fatalf("approve at fully verified: %v", err)
	}
	if got.VerificationLevel == nil || *got.VerificationLevel != domain.LevelFullyVerified {
		t.Errorf("level = %v, want fully_verified", got.VerificationLevel)
	}
}

func TestAdminDecideReject(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	biz, _ := pendingBusiness(t, h)

	// A rejection without a reason is not actionable for the owner.
	if _, err := svc.Decide(ctx, admin.ID, biz.ID, DecideInput{Decision: "reject"}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
	if status, _ := businessState(t, h, biz.ID); status != string(domain.BusinessPending) {
		t.Errorf("status = %q after a refused rejection", status)
	}

	if _, err := svc.Decide(ctx, admin.ID, biz.ID, DecideInput{Decision: "reject", Reason: "The licence does not match the registered name."}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	status, _ := businessState(t, h, biz.ID)
	if status != string(domain.BusinessRejected) {
		t.Errorf("status = %q, want rejected", status)
	}
	// The reason is stored, because it is what the owner is shown.
	var reason string
	if err := h.QueryRow(t, `SELECT reason FROM moderation_actions WHERE target_type='business' AND target_id=$1`, biz.ID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason == "" {
		t.Error("the audit row has no reason")
	}
}
