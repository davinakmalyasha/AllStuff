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
	"allstuff/api/internal/security"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/ws"
)

// Duplicate opens its own transaction (Pool().Begin), which panics on a
// tx-bound Repos, so these tests bind to the pool BEFORE creating fixtures
// (see testutil.UsePool: fixtures and code must share one connection).
// The per-test database is still dropped at cleanup, so isolation holds.
func newPoolProducts(t *testing.T) (*Products, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	h.UsePool()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://allstuff.test"}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test"})
	notifier := NewNotifier(h.Repos, cfg, sender, ws.NewHub(logger, nil), logger)
	billing := NewBilling(h.Repos, cfg, security.NewStripeClient("", ""), logger)
	return NewProducts(h.Repos, notifier, billing), h
}

// A service created through Products.Create without a duration could never be
// booked: Booking.bookableService rejects a service whose duration_minutes is
// NULL, so the storefront showed a dead Book button. This pins the fix.
func TestServiceDurationPersistsEndToEnd(t *testing.T) {
	svc, h := newPoolProducts(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Barbershop")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Cut " + owner.ID[:8], Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})

	sixty := 60
	created, err := svc.Create(ctx, owner.ID, biz.ID, ProductInput{
		Type: sid("service"), Name: sid("Haircut"), DurationMinutes: &sixty,
	})
	if err != nil {
		t.Fatalf("create service with duration: %v", err)
	}
	if created.DurationMinutes == nil || *created.DurationMinutes != 60 {
		t.Fatalf("created duration = %v, want 60", created.DurationMinutes)
	}

	thirty := 30
	updated, err := svc.Update(ctx, owner.ID, created.ID, ProductInput{DurationMinutes: &thirty})
	if err != nil {
		t.Fatalf("update duration: %v", err)
	}
	if updated.DurationMinutes == nil || *updated.DurationMinutes != 30 {
		t.Fatalf("updated duration = %v, want 30", updated.DurationMinutes)
	}

	bad := 0
	if _, err := svc.Update(ctx, owner.ID, created.ID, ProductInput{DurationMinutes: &bad}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("zero duration: err = %v, want ErrValidation", err)
	}

	dup, err := svc.Duplicate(ctx, owner.ID, created.ID)
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if dup.DurationMinutes == nil || *dup.DurationMinutes != 30 {
		t.Errorf("duplicate duration = %v, want 30 (copy must stay bookable)", dup.DurationMinutes)
	}
}

// Publishing a service with no duration must fail: the old gate checked image
// and price only, so a service published cleanly and then failed every BookAt
// with ErrNotFound.
func TestPublishServiceRequiresDuration(t *testing.T) {
	svc, h := newTestProducts(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Spa")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Glow " + owner.ID[:8], Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})

	created, err := svc.Create(ctx, owner.ID, biz.ID, ProductInput{
		Type: sid("service"), Name: sid("Facial"),
	})
	if err != nil {
		t.Fatalf("create draft service: %v", err)
	}
	// Give it the image + price the old gate required, but no duration.
	img := "00000000-0000-0000-0000-000000000001"
	price := 150000.0
	if _, err := svc.Update(ctx, owner.ID, created.ID, ProductInput{
		CoverImageID: &img, BasePrice: &price,
	}); err != nil {
		t.Fatalf("add image+price: %v", err)
	}
	if _, err := svc.Publish(ctx, owner.ID, created.ID, true); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("publish without duration: err = %v, want ErrValidation", err)
	}

	dur := 45
	if _, err := svc.Update(ctx, owner.ID, created.ID, ProductInput{DurationMinutes: &dur}); err != nil {
		t.Fatalf("set duration: %v", err)
	}
	if _, err := svc.Publish(ctx, owner.ID, created.ID, true); err != nil {
		t.Errorf("publish with duration: %v, want nil", err)
	}
}
