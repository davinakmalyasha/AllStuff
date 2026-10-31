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

// claims.Decide had no coverage at all - 58 statements - and it is an ownership
// transfer, so the failure that matters is a claimer being told they won while
// the listing's owner_id never changes. The comment on the transfer says exactly
// that: "previously the claimer was told they won while owner_id never changed".

func newTestClaims(t *testing.T) (*Claims, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	// Approving a claim against an existing listing opens its own transaction
	// through repos.Pool(), which is nil on the default tx-bound harness.
	h.UsePool()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://allstuff.test"}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test"})
	notifier := NewNotifier(h.Repos, cfg, sender, ws.NewHub(logger, nil), logger)
	return NewClaims(h.Repos, notifier), h
}

func insertClaim(t *testing.T, h *testutil.H, claimerID string, businessID *string, name string) string {
	t.Helper()
	id := util.NewUUID()
	h.ExecPool(t, `INSERT INTO business_claims (id, user_id, business_id, name, address, city, country, evidence)
		VALUES ($1,$2,$3,$4,'Jl. Test 1','Jakarta','ID','I manage it')`,
		id, claimerID, businessID, name)
	return id
}

func claimStatus(t *testing.T, h *testutil.H, id string) (status string, decidedBy *string, note string) {
	t.Helper()
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT status, decided_by, note FROM business_claims WHERE id=$1`, id).
		Scan(&status, &decidedBy, &note); err != nil {
		t.Fatalf("reading the claim: %v", err)
	}
	return
}

func TestClaimsDecideRejectsUnknownAndMalformedInput(t *testing.T) {
	c, h := newTestClaims(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	claimer := testutil.User(t, h)

	t.Run("an unknown claim", func(t *testing.T) {
		if _, err := c.Decide(ctx, admin.ID, util.NewUUID(), "approve", ""); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("an unrecognised decision", func(t *testing.T) {
		id := insertClaim(t, h, claimer.ID, nil, "Corner Cafe")
		if _, err := c.Decide(ctx, admin.ID, id, "maybe", ""); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("err = %v, want ErrValidation", err)
		}
		// The claim must be untouched, not half-decided.
		if status, _, _ := claimStatus(t, h, id); status != "open" {
			t.Errorf("status = %q after an invalid decision, want open", status)
		}
	})

	t.Run("a claim that is already decided", func(t *testing.T) {
		id := insertClaim(t, h, claimer.ID, nil, "Second Cafe")
		if _, err := c.Decide(ctx, admin.ID, id, "reject", "no"); err != nil {
			t.Fatalf("first decision: %v", err)
		}
		if _, err := c.Decide(ctx, admin.ID, id, "approve", "changed my mind"); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("second decision: err = %v, want ErrValidation", err)
		}
		if status, _, note := claimStatus(t, h, id); status != "rejected" || note != "no" {
			t.Errorf("the claim was modified by a second decision: %s / %q", status, note)
		}
	})
}

func TestClaimsDecideApprovalTransfersOwnership(t *testing.T) {
	c, h := newTestClaims(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	claimer := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")

	// An unowned listing, which is the state a claim exists to resolve.
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Unowned Co", Category: cat,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	claimID := insertClaim(t, h, claimer.ID, &biz.ID, "Unowned Co")

	got, err := c.Decide(ctx, admin.ID, claimID, "approve", "verified documents")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("returned status = %q, want approved", got.Status)
	}
	if got.Note != "verified documents" {
		t.Errorf("returned note = %q", got.Note)
	}
	if got.DecidedAt == nil {
		t.Error("DecidedAt is nil on an approved claim")
	}

	// The point of the whole operation: the listing now has this owner.
	var owner *string
	if err := h.Pool.QueryRow(ctx, `SELECT owner_id FROM businesses WHERE id=$1`, biz.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner == nil || *owner != claimer.ID {
		t.Errorf("owner_id = %v, want the claimer %s", owner, claimer.ID)
	}

	status, decidedBy, note := claimStatus(t, h, claimID)
	if status != "approved" || note != "verified documents" {
		t.Errorf("claim = %s / %q", status, note)
	}
	if decidedBy == nil || *decidedBy != admin.ID {
		t.Errorf("decided_by = %v, want the admin", decidedBy)
	}

	// The claimer is told, and told which listing.
	var n int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id=$1 AND type='claim_result'`, claimer.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("the claimer got %d notifications, want 1", n)
	}
}

// Approval is one transaction: if the listing cannot be updated - here because it
// has been deleted - the claim must stay open rather than being marked approved
// against a listing nobody owns.
func TestClaimsDecideApprovalOfADeletedListingChangesNothing(t *testing.T) {
	c, h := newTestClaims(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	claimer := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")

	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Gone Co", Category: cat,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	claimID := insertClaim(t, h, claimer.ID, &biz.ID, "Gone Co")
	h.ExecPool(t, `UPDATE businesses SET deleted_at = now() WHERE id=$1`, biz.ID)

	if _, err := c.Decide(ctx, admin.ID, claimID, "approve", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if status, _, _ := claimStatus(t, h, claimID); status != "open" {
		t.Errorf("status = %q after a failed approval, want open (the transaction must roll back)", status)
	}
}

func TestClaimsDecideApprovalCreatesADraft(t *testing.T) {
	c, h := newTestClaims(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	claimer := testutil.User(t, h)

	// No business_id: the claim is for a listing that does not exist yet.
	claimID := insertClaim(t, h, claimer.ID, nil, "Brand New Cafe")

	if _, err := c.Decide(ctx, admin.ID, claimID, "approve", ""); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var id, owner, name, status, slug string
	var lat, lng float64
	err := h.Pool.QueryRow(ctx,
		`SELECT id, owner_id, name, status, slug, lat, lng FROM businesses WHERE owner_id=$1`, claimer.ID).
		Scan(&id, &owner, &name, &status, &slug, &lat, &lng)
	if err != nil {
		t.Fatalf("no draft business was created: %v", err)
	}
	if name != "Brand New Cafe" || status != "draft" {
		t.Errorf("draft = %q / %q, want the claimed name and status draft", name, status)
	}
	if lat != 0 || lng != 0 {
		t.Errorf("draft coordinates = %v,%v; the schema requires a placeholder", lat, lng)
	}
	if slug == "" || slug[:1] == "-" {
		t.Errorf("slug = %q", slug)
	}

	// The claim records the draft it produced. Without this the claimer learns the
	// id only from the notification and the claim itself has no durable link to
	// its outcome, which is what an audit of "what did this claim create" needs.
	var linked *string
	if err := h.Pool.QueryRow(ctx, `SELECT business_id FROM business_claims WHERE id=$1`, claimID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked == nil || *linked != id {
		t.Errorf("claim.business_id = %v, want the draft %s it created", linked, id)
	}
}

func TestClaimsDecideRejection(t *testing.T) {
	c, h := newTestClaims(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)
	claimer := testutil.User(t, h)

	claimID := insertClaim(t, h, claimer.ID, nil, "Disputed Cafe")
	got, err := c.Decide(ctx, admin.ID, claimID, "reject", "not the owner")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Status != "rejected" || got.Note != "not the owner" {
		t.Errorf("returned = %s / %q", got.Status, got.Note)
	}

	// No draft may be created for a rejected claim.
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM businesses WHERE owner_id=$1`, claimer.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d business(es) were created for a rejected claim", n)
	}

	var notified int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id=$1 AND type='claim_result'`, claimer.ID).Scan(&notified); err != nil {
		t.Fatal(err)
	}
	if notified != 1 {
		t.Errorf("the claimer got %d notifications, want 1", notified)
	}
}
