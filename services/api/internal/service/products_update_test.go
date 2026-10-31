//go:build !race

package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"allstuff/api/internal/config"
	"allstuff/api/internal/domain"
	"allstuff/api/internal/email"
	"allstuff/api/internal/security"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/ws"
)

// products.Update had no coverage - 52 statements - and it is the owner-facing
// edit path, so both the ownership gate and the validation are user-visible.

func newTestProducts(t *testing.T) (*Products, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://allstuff.test"}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test"})
	notifier := NewNotifier(h.Repos, cfg, sender, ws.NewHub(logger, nil), logger)
	billing := NewBilling(h.Repos, cfg, security.NewStripeClient("", ""), logger)
	return NewProducts(h.Repos, notifier, billing), h
}

func ownProduct(t *testing.T, h *testutil.H, owner *domain.User) (*domain.Business, *domain.Product) {
	t.Helper()
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Shop " + owner.ID[:8], Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	return biz, testutil.Product(t, h, biz.ID, "Kopi Susu")
}

func sid(s string) *string   { return &s }
func f64(f float64) *float64 { return &f }
func bln(b bool) *bool       { return &b }

func TestProductsUpdateRequiresOwnership(t *testing.T) {
	svc, h := newTestProducts(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	stranger := testutil.User(t, h)
	_, prod := ownProduct(t, h, owner)

	if _, err := svc.Update(ctx, stranger.ID, prod.ID, ProductInput{Name: sid("Hijacked")}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a non-owner: err = %v, want ErrForbidden", err)
	}
	// The row must be untouched, not merely refused after the write.
	var name string
	if err := h.QueryRow(t, `SELECT name FROM products WHERE id=$1`, prod.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != prod.Name {
		t.Errorf("name = %q after a refused update, want %q", name, prod.Name)
	}

	if _, err := svc.Update(ctx, owner.ID, "00000000-0000-0000-0000-000000000000", ProductInput{Name: sid("Nope")}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("an unknown product: err = %v, want ErrNotFound", err)
	}
}

func TestProductsUpdateValidatesFields(t *testing.T) {
	svc, h := newTestProducts(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	_, prod := ownProduct(t, h, owner)

	tooManyImages := make([]string, 11)
	for i := range tooManyImages {
		tooManyImages[i] = "00000000-0000-0000-0000-00000000000" + string(rune('0'+i%10))
	}

	rejected := []struct {
		name string
		in   ProductInput
	}{
		{"a one-rune name", ProductInput{Name: sid("a")}},
		{"a 101-rune name", ProductInput{Name: sid(strings.Repeat("a", 101))}},
		{"a 101-rune multi-byte name", ProductInput{Name: sid(strings.Repeat("日", 101))}},
		{"a two-character currency", ProductInput{Currency: sid("US")}},
		{"a four-character currency", ProductInput{Currency: sid("USDX")}},
		{"a negative price", ProductInput{BasePrice: f64(-1)}},
		{"eleven images", ProductInput{ImageIDs: &tooManyImages}},
		{"six tags", ProductInput{Tags: &[]string{"a", "b", "c", "d", "e", "f"}}},
		{"an empty edit", ProductInput{}},
	}
	for _, tc := range rejected {
		if _, err := svc.Update(ctx, owner.ID, prod.ID, tc.in); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", tc.name, err)
		}
	}

	// The boundaries that must pass: 2 and 100 runes, a 3-letter currency, and a
	// price of zero.
	ok := []ProductInput{
		{Name: sid("ab")},
		{Name: sid(strings.Repeat("日", 100))},
		{Currency: sid("IDR")},
		{BasePrice: f64(0)},
	}
	for i, in := range ok {
		if _, err := svc.Update(ctx, owner.ID, prod.ID, in); err != nil {
			t.Errorf("valid edit %d was refused: %v", i, err)
		}
	}
}

// "Call for price is only valid for services" is what the error says. The
// condition is narrower than that: it also rejects a service-less product WITH
// variants. Pin the rule rather than the message.
func TestProductsUpdateCallForPriceRule(t *testing.T) {
	svc, h := newTestProducts(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	biz, prod := ownProduct(t, h, owner)

	// A plain product with no variants cannot be call-for-price.
	if _, err := svc.Update(ctx, owner.ID, prod.ID, ProductInput{CallForPrice: bln(true)}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("a plain product: err = %v, want ErrValidation", err)
	}

	// A service can.
	service, err := svc.Create(ctx, owner.ID, biz.ID, ProductInput{
		Type: sid(string(domain.ProductTypeService)), Name: sid("Consulting"), Currency: sid("IDR"),
	})
	if err != nil {
		t.Fatalf("creating a service: %v", err)
	}
	if _, err := svc.Update(ctx, owner.ID, service.ID, ProductInput{CallForPrice: bln(true)}); err != nil {
		t.Errorf("a service was refused call-for-price: %v", err)
	}
}

func TestProductsUpdateAppliesChanges(t *testing.T) {
	svc, h := newTestProducts(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	_, prod := ownProduct(t, h, owner)

	got, err := svc.Update(ctx, owner.ID, prod.ID, ProductInput{
		Name:        sid("  Kopi Susu Spesial  "),
		Description: sid("  Enak  "),
		BasePrice:   f64(25000),
		Tags:        &[]string{"kopi", "susu"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// The name and description are trimmed before they are stored.
	if got.Name != "Kopi Susu Spesial" {
		t.Errorf("Name = %q, want it trimmed", got.Name)
	}
	if got.BasePrice == nil || *got.BasePrice != 25000 {
		t.Errorf("BasePrice = %v", got.BasePrice)
	}
	if len(got.Tags) != 2 {
		t.Errorf("Tags = %v", got.Tags)
	}

	// And it is persisted, not only reflected in the return value.
	var name string
	if err := h.QueryRow(t, `SELECT name FROM products WHERE id=$1`, prod.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Kopi Susu Spesial" {
		t.Errorf("stored name = %q", name)
	}
}
