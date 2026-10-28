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
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
	"bizverse/api/internal/ws"
)

// The plan-capacity gates had no coverage: checkCatalogCapacity,
// checkGalleryCapacity, the product/seat counters behind them, and the noun
// helper in the refusal message. A gate that never runs is a comment, and
// these decide whether an owner is locked out of their own storefront.

func newGatedProducts(t *testing.T, ents EntitlementSource) (*Products, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test"})
	notifier := NewNotifier(h.Repos, cfg, sender, ws.NewHub(logger, nil), logger)
	return NewProducts(h.Repos, notifier, ents), h
}

func capMessage(t *testing.T, err error) string {
	t.Helper()
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	return domain.FromError(err).Fields["_"]
}

func gatedBusiness(t *testing.T, h *testutil.H, name string, owner *domain.User) *domain.Business {
	t.Helper()
	cat := testutil.Category(t, h, "Cafe")
	return testutil.Business(t, h, testutil.BusinessOpts{
		Name: name, Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
}

func createProduct(t *testing.T, p *Products, ctx context.Context, ownerID, bizID, typ, name string) {
	t.Helper()
	in := ProductInput{Name: &name}
	if typ != "" {
		in.Type = &typ
	}
	if _, err := p.Create(ctx, ownerID, bizID, in); err != nil {
		t.Fatalf("create %s %q: %v", typ, name, err)
	}
}

func TestCatalogCapacityEnforcesPerTypeCaps(t *testing.T) {
	p, h := newGatedProducts(t, stubEntitlements{e: &Entitlements{ProductLimit: 2}})
	ctx := context.Background()
	owner := testutil.User(t, h)
	biz := gatedBusiness(t, h, "Cap Cafe", owner)

	createProduct(t, p, ctx, owner.ID, biz.ID, "", "First Product")
	createProduct(t, p, ctx, owner.ID, biz.ID, "", "Second Product")
	// Services are counted separately: a full product allowance must not block
	// listing services, and each side caps at two.
	createProduct(t, p, ctx, owner.ID, biz.ID, "service", "First Service")
	createProduct(t, p, ctx, owner.ID, biz.ID, "service", "Second Service")

	if _, err := p.Create(ctx, owner.ID, biz.ID, ProductInput{Name: strptr("Third Product")}); err == nil {
		t.Error("a third product on a limit-2 plan was allowed")
	} else if msg := capMessage(t, err); !strings.Contains(msg, "up to 2 products") {
		t.Errorf("refusal = %q, want the plan-cap message naming products", msg)
	}
	if _, err := p.Create(ctx, owner.ID, biz.ID, ProductInput{Name: strptr("Third Service"), Type: strptr("service")}); err == nil {
		t.Error("a third service on a limit-2 plan was allowed")
	} else if msg := capMessage(t, err); !strings.Contains(msg, "up to 2 services") {
		t.Errorf("refusal = %q, want the plan-cap message naming services", msg)
	}

	// A soft-deleted product frees its slot: charging for a deleted row would
	// make the cap impossible to get under without a support request.
	var doomed string
	if err := h.QueryRow(t, `SELECT id FROM products WHERE business_id = $1 AND name = 'Second Product'`, biz.ID).Scan(&doomed); err != nil {
		t.Fatalf("find the product to delete: %v", err)
	}
	if err := p.Delete(ctx, owner.ID, doomed); err != nil {
		t.Fatalf("delete: %v", err)
	}
	createProduct(t, p, ctx, owner.ID, biz.ID, "", "Replacement Product")
}

func TestCatalogCapacityUncappedWithoutEntitlements(t *testing.T) {
	// A nil EntitlementSource is ungated (not zero): the gate checks the
	// interface before resolving any plan.
	p, h := newGatedProducts(t, nil)
	ctx := context.Background()
	owner := testutil.User(t, h)
	biz := gatedBusiness(t, h, "Uncapped Cafe", owner)
	// Nil entitlements means ungated, so this is a smoke test that Create works
	// at all through the gate when no plan is attached.
	createProduct(t, p, ctx, owner.ID, biz.ID, "", "Anything Goes")
}

func TestCatalogNoun(t *testing.T) {
	if catalogNoun(domain.ProductTypeService) != "services" {
		t.Errorf("service noun = %q", catalogNoun(domain.ProductTypeService))
	}
	if catalogNoun(domain.ProductTypeProduct) != "products" {
		t.Errorf("product noun = %q", catalogNoun(domain.ProductTypeProduct))
	}
}

func TestGalleryCapacityViaUpdate(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	biz := gatedBusiness(t, h, "Gallery Cafe", owner)
	bizSvc := NewBusinesses(h.Repos, stubEntitlements{e: &Entitlements{GalleryLimit: 3}})

	three := BusinessInput{"gallery": []any{"m1", "m2", "m3"}}
	if _, err := bizSvc.Update(ctx, owner.ID, biz.ID, three); err != nil {
		t.Fatalf("three photos on a limit-3 plan: %v", err)
	}
	four := BusinessInput{"gallery": []any{"m1", "m2", "m3", "m4"}}
	if _, err := bizSvc.Update(ctx, owner.ID, biz.ID, four); err == nil {
		t.Error("four photos on a limit-3 plan were allowed")
	} else if msg := domain.FromError(err).Fields["gallery"]; !strings.Contains(msg, "up to 3 gallery photos") {
		t.Errorf("refusal = %q, want the plan-cap message", msg)
	}

	// The stored array is the submitted one: removing and re-adding in one
	// save is judged on the result, not penalised for the churn.
	after, err := h.Repos.Businesses.GetByID(ctx, biz.ID)
	if err != nil || after == nil {
		t.Fatalf("read back: %+v, %v", after, err)
	}
	if len(after.Gallery) != 3 {
		t.Errorf("gallery = %v, want the three-photo save retained", after.Gallery)
	}
}

func TestProductCounts(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	biz := gatedBusiness(t, h, "Count Cafe", owner)

	mk := func(typ domain.ProductType, name string, deleted bool) string {
		id := util.NewUUID()
		if err := h.Repos.Products.Create(ctx, &domain.Product{
			ID: id, BusinessID: biz.ID, Type: typ, Name: name,
			Currency: "USD", Tags: []string{},
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		if deleted {
			if err := h.Repos.Products.SoftDelete(ctx, id); err != nil {
				t.Fatalf("delete %s: %v", name, err)
			}
		}
		return id
	}
	mk(domain.ProductTypeProduct, "P1", false)
	mk(domain.ProductTypeProduct, "P2", false)
	gone := mk(domain.ProductTypeProduct, "P3", true)
	mk(domain.ProductTypeService, "S1", false)

	if n, err := h.Repos.Products.CountByBusinessAndType(ctx, biz.ID, domain.ProductTypeProduct, ""); err != nil || n != 2 {
		t.Errorf("products = %d, %v; want 2 live", n, err)
	}
	if n, err := h.Repos.Products.CountByBusinessAndType(ctx, biz.ID, domain.ProductTypeService, ""); err != nil || n != 1 {
		t.Errorf("services = %d, %v; want 1", n, err)
	}
	// excludeID lets Duplicate skip the row being copied: a business at the
	// cap can still duplicate a product within it.
	if n, err := h.Repos.Products.CountByBusinessAndType(ctx, biz.ID, domain.ProductTypeProduct, gone); err != nil || n != 2 {
		t.Errorf("excluding the deleted row = %d, %v; want 2", n, err)
	}
	if n, err := h.Repos.Products.CountByBusiness(ctx, biz.ID); err != nil || n != 3 {
		t.Errorf("all live = %d, %v; want 3", n, err)
	}
}

func seatInvites(t *testing.T, h *testutil.H) *Invites {
	t.Helper()
	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test"})
	return NewInvites(h.Repos, cfg, sender, stubEntitlements{e: &Entitlements{TeamSeats: 10}})
}

func acceptInvite(t *testing.T, h *testutil.H, inv *Invites, ctx context.Context, addr string) {
	t.Helper()
	invitee := testutil.User(t, h)
	h.Exec(t, `UPDATE users SET email=$1 WHERE id=$2`, addr, invitee.ID)
	var token string
	if err := h.QueryRow(t, `SELECT token FROM business_invites WHERE email=$1`, addr).Scan(&token); err != nil {
		t.Fatalf("token for %s: %v", addr, err)
	}
	if err := inv.Accept(ctx, invitee.ID, token); err != nil {
		t.Fatalf("accept %s: %v", addr, err)
	}
}

func seatCounts(t *testing.T, h *testutil.H, bizID string) (active, reserved int) {
	t.Helper()
	ctx := context.Background()
	var err error
	if active, err = h.Repos.Businesses.CountActiveSeats(ctx, bizID); err != nil {
		t.Fatalf("CountActiveSeats: %v", err)
	}
	if reserved, err = h.Repos.Businesses.CountReservedSeats(ctx, bizID); err != nil {
		t.Fatalf("CountReservedSeats: %v", err)
	}
	return active, reserved
}

func TestSeatCounts(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	biz := gatedBusiness(t, h, "Seat Cafe", owner)
	inv := seatInvites(t, h)

	// The owner alone occupies exactly one seat either way.
	if a, r := seatCounts(t, h, biz.ID); a != 1 || r != 1 {
		t.Errorf("owner only: active=%d reserved=%d, want 1/1", a, r)
	}

	// A pending invitation reserves but does not activate: it is an intention,
	// not a seat, and the gate counts it so the allowance cannot be oversold
	// before the first acceptance.
	if err := inv.Create(ctx, owner.ID, biz.ID, "pending@example.test", "co_owner"); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if a, r := seatCounts(t, h, biz.ID); a != 1 || r != 2 {
		t.Errorf("pending: active=%d reserved=%d, want 1/2", a, r)
	}

	// Acceptance activates the seat.
	acceptInvite(t, h, inv, ctx, "pending@example.test")
	if a, r := seatCounts(t, h, biz.ID); a != 2 || r != 2 {
		t.Errorf("accepted: active=%d reserved=%d, want 2/2", a, r)
	}

	// Revocation frees both the seat and the reservation.
	h.Exec(t, `UPDATE business_invites SET revoked_at = now() WHERE business_id = $1`, biz.ID)
	if a, r := seatCounts(t, h, biz.ID); a != 1 || r != 1 {
		t.Errorf("revoked: active=%d reserved=%d, want 1/1", a, r)
	}

	// Expiry frees the reservation the same way.
	if err := inv.Create(ctx, owner.ID, biz.ID, "expired@example.test", "viewer"); err != nil {
		t.Fatalf("invite: %v", err)
	}
	h.Exec(t, `UPDATE business_invites SET expires_at = now() - interval '1 minute' WHERE email = 'expired@example.test'`)
	if a, r := seatCounts(t, h, biz.ID); a != 1 || r != 1 {
		t.Errorf("expired: active=%d reserved=%d, want 1/1", a, r)
	}

	// An unowned listing has no owner seat, but an accepted collaborator still
	// counts: the permission the seat pays for is real either way.
	unowned := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Unowned Cafe", Category: testutil.Category(t, h, "Cafe"), Owner: nil,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	h.Exec(t, `INSERT INTO business_invites (id, business_id, email, role, token, invited_by)
		VALUES ($1, $2, 'helper@example.test', 'co_owner', $3, $4)`,
		util.NewUUID(), unowned.ID, util.NewUUID(), owner.ID)
	if a, r := seatCounts(t, h, unowned.ID); a != 0 || r != 1 {
		t.Errorf("unowned pending: active=%d reserved=%d, want 0/1", a, r)
	}
	h.Exec(t, `UPDATE business_invites SET accepted_at = now() WHERE business_id = $1`, unowned.ID)
	if a, r := seatCounts(t, h, unowned.ID); a != 1 || r != 1 {
		t.Errorf("unowned accepted: active=%d reserved=%d, want 1/1", a, r)
	}
}

func strptr(s string) *string { return &s }

func TestAcceptedSeatsSurviveExpiry(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	biz := gatedBusiness(t, h, "Aged Seat Cafe", owner)
	inv := seatInvites(t, h)

	if err := inv.Create(ctx, owner.ID, biz.ID, "aged@example.test", "co_owner"); err != nil {
		t.Fatalf("invite: %v", err)
	}
	acceptInvite(t, h, inv, ctx, "aged@example.test")
	if a, r := seatCounts(t, h, biz.ID); a != 2 || r != 2 {
		t.Fatalf("accepted: active=%d reserved=%d, want 2/2", a, r)
	}

	// A week later the invitation row expires. The collaborator was accepted:
	// CanManageBusiness still grants them access (it never checked expiry),
	// so the seat they occupy must still count. If it does not, the owner can
	// invite past the plan cap while everyone retains access.
	h.Exec(t, `UPDATE business_invites SET expires_at = now() - interval '1 minute' WHERE business_id = $1`, biz.ID)
	can, err := h.Repos.Businesses.CanManageBusiness(ctx, inviteeOf(t, h, "aged@example.test"), biz.ID)
	if err != nil || !can {
		t.Fatalf("the accepted collaborator lost access on expiry: %v, %v", can, err)
	}
	if a, r := seatCounts(t, h, biz.ID); a != 2 || r != 2 {
		t.Errorf("accepted+expired: active=%d reserved=%d, want 2/2 - the seat leaked", a, r)
	}
}

func inviteeOf(t *testing.T, h *testutil.H, addr string) string {
	t.Helper()
	var id string
	if err := h.QueryRow(t, `SELECT id FROM users WHERE email = $1`, addr).Scan(&id); err != nil {
		t.Fatalf("invitee %s: %v", addr, err)
	}
	return id
}
