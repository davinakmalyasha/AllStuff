//go:build !race

package service_test

import (
	"context"
	"errors"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/service"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// `businesses.highlighted_product_ids` has been in the schema since 0001 and appears
// nowhere else in the Go source. PRD 5.3.2 specifies it - "highlighted products
// (owner-pinned, max 3)" - so it was a specified feature that was never built.
//
// Pins are written through the existing PATCH /businesses/{id} rather than a new
// endpoint: the service already has a per-field validation block and an allowlist, so
// adding a route would be surface for no gain.
//
// The notifier is nil, as in collection_cap_test.go: the paths under test do not use it.

// anySlice converts to the shape JSON decoding actually produces.
//
// BusinessInput values come from decodeBody, where a JSON array lands as []any.
// asStringSlice asserts exactly that type, so passing a Go []string - which reads as
// the natural thing to write - fails with "Expected an array" and blames the service
// for a mismatch in the fixture.
func anySlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

func newTestBusinesses(h *testutil.H) *service.Businesses {
	return service.NewBusinesses(h.Repos, nil)
}

// productsFor creates n published products on a business.
func productsFor(t *testing.T, h *testutil.H, bizID string, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	for range n {
		p := testutil.Product(t, h, bizID, "p"+util.NewUUID()[:6])
		out = append(out, p.ID)
	}
	return out
}

func pins(t *testing.T, h *testutil.H, bizID string) []string {
	t.Helper()
	got, err := h.Repos.Businesses.HighlightedProductIDs(context.Background(), bizID)
	if err != nil {
		t.Fatalf("HighlightedProductIDs: %v", err)
	}
	return got
}

func TestHighlightedProductsArePinnedInOrder(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	svc := newTestBusinesses(h)

	owner := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	ids := productsFor(t, h, biz.ID, 3)

	if _, err := svc.Update(ctx, owner.ID, biz.ID, service.BusinessInput{
		"highlighted_product_ids": []any{ids[2], ids[0]},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := pins(t, h, biz.ID)
	if len(got) != 2 {
		t.Fatalf("pinned %d products, want 2", len(got))
	}
	// Order is the owner's, not the database's: the storefront renders them in it.
	if got[0] != ids[2] || got[1] != ids[0] {
		t.Errorf("pins = %v, want [%s %s] in that order", got, ids[2], ids[0])
	}
}

// The cap is 3, and it is stated in the PRD - so it is a product rule, not a
// preference. A boundary test on 4 rather than a sweeping one.
func TestHighlightedProductsAreCappedAtThree(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	svc := newTestBusinesses(h)

	owner := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	ids := productsFor(t, h, biz.ID, 4)

	_, err := svc.Update(ctx, owner.ID, biz.ID, service.BusinessInput{
		"highlighted_product_ids": anySlice(ids),
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("pinning 4 products: err = %v, want ErrValidation", err)
	}
	if got := pins(t, h, biz.ID); len(got) != 0 {
		t.Errorf("the rejected update still wrote %d pins", len(got))
	}
}

// Duplicates are collapsed BEFORE the cap is applied, so a caller repeating an id is
// not refused for exceeding a limit they did not actually exceed.
func TestHighlightedProductsDeduplicateBeforeApplyingTheCap(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	svc := newTestBusinesses(h)

	owner := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	ids := productsFor(t, h, biz.ID, 3)

	if _, err := svc.Update(ctx, owner.ID, biz.ID, service.BusinessInput{
		"highlighted_product_ids": []any{ids[0], ids[1], ids[2], ids[0], ids[1]},
	}); err != nil {
		t.Fatalf("Update with repeats of three distinct ids: %v", err)
	}
	if got := pins(t, h, biz.ID); len(got) != 3 {
		t.Errorf("pinned %d products, want 3 distinct", len(got))
	}
}

// A pin is a reference the storefront dereferences. A product belonging to ANOTHER
// business would render as somebody else's product on this storefront.
func TestHighlightedProductsMustBelongToThisBusiness(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	svc := newTestBusinesses(h)

	owner := testutil.User(t, h)
	mine := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	theirs := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	foreign := productsFor(t, h, theirs.ID, 1)

	_, err := svc.Update(ctx, owner.ID, mine.ID, service.BusinessInput{
		"highlighted_product_ids": anySlice(foreign),
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("pinning another business's product: err = %v, want ErrValidation", err)
	}
	if got := pins(t, h, mine.ID); len(got) != 0 {
		t.Errorf("the rejected update still wrote %d pins", len(got))
	}
}

// A soft-deleted product must not be pinnable, for the same reason.
func TestHighlightedProductsRejectDeletedProducts(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	svc := newTestBusinesses(h)

	owner := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	ids := productsFor(t, h, biz.ID, 1)
	h.Exec(t, `UPDATE products SET deleted_at = now() WHERE id = $1::uuid`, ids[0])

	_, err := svc.Update(ctx, owner.ID, biz.ID, service.BusinessInput{
		"highlighted_product_ids": anySlice(ids),
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("pinning a deleted product: err = %v, want ErrValidation", err)
	}
}

// An empty list clears the pins, and clearing is a legitimate update on its own.
//
// This is the case the existing "Nothing to update" guard would otherwise swallow: a
// request whose ONLY field is highlighted_product_ids must not be rejected for
// carrying no changes. The guard accounts for it explicitly.
func TestHighlightedProductsCanBeClearedOnTheirOwn(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	svc := newTestBusinesses(h)

	owner := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	ids := productsFor(t, h, biz.ID, 2)

	if _, err := svc.Update(ctx, owner.ID, biz.ID, service.BusinessInput{
		"highlighted_product_ids": anySlice(ids),
	}); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if len(pins(t, h, biz.ID)) != 2 {
		t.Fatal("fixture: pins were not set")
	}

	// The only field in the request.
	if _, err := svc.Update(ctx, owner.ID, biz.ID, service.BusinessInput{
		"highlighted_product_ids": []any{},
	}); err != nil {
		t.Fatalf("clearing as the only field: %v - the nothing-to-update guard does not account for it", err)
	}
	if got := pins(t, h, biz.ID); len(got) != 0 {
		t.Errorf("pins = %v after a clear, want empty", got)
	}
}
