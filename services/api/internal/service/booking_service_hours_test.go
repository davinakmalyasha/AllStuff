//go:build !race

package service

import (
	"context"
	"errors"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
)

// Per-service hours narrow one service inside a place's own hours. A missing
// weekday means "follow the place", so the override is written only for the days
// that differ.

func serviceHoursFixture(t *testing.T) (*Booking, *testutil.H, *domain.Business, *domain.Product, *domain.User, *domain.Location) {
	t.Helper()
	svc, h := newTestBooking(t)
	b, p, owner := bookableFixture(t, h)
	loc, err := svc.CreateLocation(context.Background(), owner.ID, b.ID, domain.Location{
		Name: "Main", Address: "Jl. Satu", City: "Jakarta", Country: "ID",
		Lat: -6.2, Lng: 106.8, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}
	return svc, h, b, p, owner, loc
}

func TestServiceHoursCRUD(t *testing.T) {
	svc, h, b, p, owner, loc := serviceHoursFixture(t)
	ctx := context.Background()
	stranger := testutil.User(t, h)

	if err := svc.SetServiceHours(ctx, stranger.ID, b.ID, loc.ID, p.ID, map[string]domain.DayHours{
		"mon": {Working: true, OpensAt: "13:00", ClosesAt: "15:00"},
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger: err = %v, want ErrForbidden", err)
	}
	if err := svc.SetServiceHours(ctx, owner.ID, b.ID, loc.ID, p.ID, map[string]domain.DayHours{
		"funday": {Working: true, OpensAt: "13:00", ClosesAt: "15:00"},
	}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("unknown weekday: err = %v, want ErrValidation", err)
	}
	if err := svc.SetServiceHours(ctx, owner.ID, b.ID, loc.ID, p.ID, map[string]domain.DayHours{
		"mon": {Working: true, OpensAt: "15:00", ClosesAt: "13:00"},
	}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("inverted window: err = %v, want ErrValidation", err)
	}

	if err := svc.SetServiceHours(ctx, owner.ID, b.ID, loc.ID, p.ID, map[string]domain.DayHours{
		"mon": {Working: true, OpensAt: "13:00", ClosesAt: "15:00"},
		"tue": {Working: false},
	}); err != nil {
		t.Fatalf("SetServiceHours: %v", err)
	}
	got, err := svc.ServiceHours(ctx, owner.ID, b.ID, loc.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h := got["mon"]; !h.Working || h.OpensAt != "13:00" || h.ClosesAt != "15:00" {
		t.Errorf("monday = %+v", h)
	}
	if h := got["tue"]; h.Working {
		t.Errorf("tuesday = %+v, want unavailable", h)
	}

	// The service must belong to this business, and the place must too.
	otherBiz, otherProd, otherOwner := bookableFixture(t, h)
	_ = otherBiz
	if err := svc.SetServiceHours(ctx, owner.ID, b.ID, loc.ID, otherProd.ID, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a foreign service: err = %v, want ErrNotFound", err)
	}
	foreignLoc, err := svc.CreateLocation(ctx, otherOwner.ID, otherBiz.ID, domain.Location{
		Name: "Foreign", Address: "Jl. Lain", City: "Jakarta", Country: "ID",
		Lat: -6.2, Lng: 106.8, Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetServiceHours(ctx, owner.ID, b.ID, foreignLoc.ID, p.ID, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a foreign location: err = %v, want ErrNotFound", err)
	}
}

func TestServiceHoursNarrowTheSlots(t *testing.T) {
	svc, _, b, p, owner, loc := serviceHoursFixture(t)
	ctx := context.Background()
	day := nextDay()
	weekday := bookableDayKeys[day.Weekday()]

	// The place is open 09:00-17:00; this service runs 13:00-15:00.
	if err := svc.SetServiceHours(ctx, owner.ID, b.ID, loc.ID, p.ID, map[string]domain.DayHours{
		weekday: {Working: true, OpensAt: "13:00", ClosesAt: "15:00"},
	}); err != nil {
		t.Fatal(err)
	}

	slots, err := svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if got := slotTimes(slots); len(got) != 2 || got[0] != "13:00" || got[1] != "14:00" {
		t.Errorf("slots = %v, want the service window only", got)
	}

	// A window WIDER than the place does not extend it: the service cannot be
	// booked while the shop is shut.
	if err := svc.SetServiceHours(ctx, owner.ID, b.ID, loc.ID, p.ID, map[string]domain.DayHours{
		weekday: {Working: true, OpensAt: "08:00", ClosesAt: "20:00"},
	}); err != nil {
		t.Fatal(err)
	}
	slots, err = svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if got := slotTimes(slots); len(got) != 8 || got[0] != "09:00" || got[len(got)-1] != "16:00" {
		t.Errorf("slots = %v, want the place's eight hours", got)
	}

	// A day the service does not run has no slots even though the place is open.
	// The times stay in the payload while the day is toggled off, which is what a
	// dashboard switch does - omitting them would make the day empty for a
	// different reason and would not prove the flag is honoured.
	if err := svc.SetServiceHours(ctx, owner.ID, b.ID, loc.ID, p.ID, map[string]domain.DayHours{
		weekday: {Working: false, OpensAt: "13:00", ClosesAt: "15:00"},
	}); err != nil {
		t.Fatal(err)
	}
	slots, err = svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Errorf("a day the service does not run offered %d slots", len(slots))
	}
}
