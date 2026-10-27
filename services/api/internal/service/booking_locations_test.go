//go:build !race

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
)

// A location is both a public fact (customers pick a branch) and the source of
// the calendar. The business's own address columns are a cached copy of the
// PRIMARY location, and every existing reader depends on that mirror.

func TestLocationCRUDAndMirror(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, _, owner := bookableFixture(t, h)
	stranger := testutil.User(t, h)

	if _, err := svc.CreateLocation(ctx, stranger.ID, b.ID, domain.Location{Name: "X"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger create: err = %v, want ErrForbidden", err)
	}
	// Validation is on the merged record: a blank address is refused.
	if _, err := svc.CreateLocation(ctx, owner.ID, b.ID, domain.Location{
		Name: "Main", City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8, Timezone: "UTC",
	}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("blank address: err = %v, want ErrValidation", err)
	}
	if _, err := svc.CreateLocation(ctx, owner.ID, b.ID, domain.Location{
		Name: "Main", Address: "Jl. Satu", City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8, Timezone: "Mars/Olympus",
	}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("bad timezone: err = %v, want ErrValidation", err)
	}

	main, err := svc.CreateLocation(ctx, owner.ID, b.ID, domain.Location{
		Name: "Main", Address: "Jl. Satu", City: "Jakarta", Country: "ID",
		Lat: -6.2, Lng: 106.8, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}
	// The first location is primary, and it inherits the business's hours rather
	// than starting with an empty (unbookable) week.
	if !main.IsPrimary {
		t.Error("the first location is not primary")
	}
	if len(main.Hours) == 0 {
		t.Error("the first location did not inherit the business hours")
	}
	// The cached columns move with it.
	var addr, primary string
	var count int
	if err := h.QueryRow(t, `SELECT address, coalesce(primary_location_id::text,''), location_count FROM businesses WHERE id=$1`, b.ID).
		Scan(&addr, &primary, &count); err != nil {
		t.Fatal(err)
	}
	if addr != "Jl. Satu" || primary != main.ID || count != 1 {
		t.Errorf("business cache: address=%q primary=%q count=%d", addr, primary, count)
	}

	branch, err := svc.CreateLocation(ctx, owner.ID, b.ID, domain.Location{
		Name: "Branch", Address: "Jl. Dua", City: "Bandung", Country: "ID",
		Lat: -6.9, Lng: 107.6, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("second location: %v", err)
	}
	if branch.IsPrimary {
		t.Error("a second location became primary")
	}

	// Moving the primary moves the cached copy.
	if err := svc.SetPrimaryLocation(ctx, owner.ID, b.ID, branch.ID); err != nil {
		t.Fatalf("SetPrimaryLocation: %v", err)
	}
	if err := h.QueryRow(t, `SELECT address FROM businesses WHERE id=$1`, b.ID).Scan(&addr); err != nil {
		t.Fatal(err)
	}
	if addr != "Jl. Dua" {
		t.Errorf("after promoting the branch, cached address = %q", addr)
	}

	// Editing the primary edits the cached copy too.
	newAddr := "Jl. Dua No. 5"
	if _, err := svc.UpdateLocation(ctx, owner.ID, b.ID, branch.ID, map[string]any{"address": newAddr}); err != nil {
		t.Fatalf("UpdateLocation: %v", err)
	}
	if err := h.QueryRow(t, `SELECT address FROM businesses WHERE id=$1`, b.ID).Scan(&addr); err != nil {
		t.Fatal(err)
	}
	if addr != newAddr {
		t.Errorf("after editing the primary, cached address = %q", addr)
	}
	// An empty patch is refused.
	if _, err := svc.UpdateLocation(ctx, owner.ID, b.ID, branch.ID, map[string]any{}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("empty patch: err = %v, want ErrValidation", err)
	}

	// Deleting the primary promotes the remaining one and re-mirrors.
	if err := svc.DeleteLocation(ctx, owner.ID, b.ID, branch.ID); err != nil {
		t.Fatalf("DeleteLocation: %v", err)
	}
	if err := h.QueryRow(t, `SELECT address, location_count FROM businesses WHERE id=$1`, b.ID).Scan(&addr, &count); err != nil {
		t.Fatal(err)
	}
	if addr != "Jl. Satu" || count != 1 {
		t.Errorf("after deleting the primary: address=%q count=%d", addr, count)
	}

	// The last location cannot be removed: a business must have one.
	if err := svc.DeleteLocation(ctx, owner.ID, b.ID, main.ID); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("deleting the last location: err = %v, want ErrValidation", err)
	}
}

func TestSlotsUseTheLocationCalendar(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, p, owner := bookableFixture(t, h)
	day := nextDay()
	customer := testutil.User(t, h)

	// A location open 10:00-12:00 replaces the business's 09:00-17:00 for slot
	// purposes.
	loc, err := svc.CreateLocation(ctx, owner.ID, b.ID, domain.Location{
		Name: "Late", Address: "Jl. Telat", City: "Jakarta", Country: "ID",
		Lat: -6.2, Lng: 106.8, Timezone: "UTC",
		Hours: map[string]any{bookableDayKeys[day.Weekday()]: map[string]any{"open": "10:00", "close": "12:00", "closed": false}},
	})
	if err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}
	if len(loc.Hours) == 0 {
		t.Fatal("the supplied hours were dropped")
	}

	slots, err := svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	// The primary location is used when none is named.
	if got := slotTimes(slots); len(got) != 2 || got[0] != "10:00" || got[1] != "11:00" {
		t.Errorf("slots = %v, want the location's two hours", got)
	}

	// A SECOND location with different hours proves the calendar is read from the
	// place rather than from the business cache. The cache mirrors the PRIMARY, so
	// a single-location test cannot tell the two apart - that is exactly why this
	// one exists.
	branch, err := svc.CreateLocation(ctx, owner.ID, b.ID, domain.Location{
		Name: "Later", Address: "Jl. Sore", City: "Jakarta", Country: "ID",
		Lat: -6.2, Lng: 106.8, Timezone: "UTC",
		Hours: map[string]any{bookableDayKeys[day.Weekday()]: map[string]any{"open": "13:00", "close": "15:00", "closed": false}},
	})
	if err != nil {
		t.Fatalf("second location: %v", err)
	}
	branchSlots, err := svc.SlotsAt(ctx, b.ID, p.ID, branch.ID, day)
	if err != nil {
		t.Fatalf("SlotsAt(branch): %v", err)
	}
	if got := slotTimes(branchSlots); len(got) != 2 || got[0] != "13:00" || got[1] != "14:00" {
		t.Errorf("branch slots = %v, want the branch's own hours", got)
	}
	// ...while the primary still answers with its own.
	if slots, err := svc.Slots(ctx, b.ID, p.ID, day); err != nil {
		t.Error(err)
	} else if got := slotTimes(slots); len(got) != 2 || got[0] != "10:00" {
		t.Errorf("primary slots = %v, want 10:00 and 11:00", got)
	}

	// Booking records WHERE it happens.
	a, err := svc.BookAt(ctx, customer.ID, b.ID, p.ID, "", loc.ID, day.Add(10*time.Hour), "")
	if err != nil {
		t.Fatalf("BookAt: %v", err)
	}
	if a.LocationID != loc.ID || a.LocationName != "Late" {
		t.Errorf("appointment location = %q (%q)", a.LocationID, a.LocationName)
	}

	// A location belonging to another business is not addressable.
	otherBiz, otherProd, otherOwner := bookableFixture(t, h)
	_ = otherProd
	foreign, err := svc.CreateLocation(ctx, otherOwner.ID, otherBiz.ID, domain.Location{
		Name: "Foreign", Address: "Jl. Lain", City: "Jakarta", Country: "ID",
		Lat: -6.2, Lng: 106.8, Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SlotsAt(ctx, b.ID, p.ID, foreign.ID, day); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another business's location: err = %v, want ErrNotFound", err)
	}
}

// The public list exposes places only for a listing the public may see.
func TestLocationsArePublicOnlyForPublicListings(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, _, owner := bookableFixture(t, h)
	if _, err := svc.CreateLocation(ctx, owner.ID, b.ID, domain.Location{
		Name: "Main", Address: "Jl. Satu", City: "Jakarta", Country: "ID",
		Lat: -6.2, Lng: 106.8, Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}
	list, err := svc.Locations(ctx, b.ID, "")
	if err != nil || len(list) != 1 {
		t.Fatalf("public list: %d locations, err=%v", len(list), err)
	}

	h.Exec(t, `UPDATE businesses SET status='draft' WHERE id=$1`, b.ID)
	if _, err := svc.Locations(ctx, b.ID, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a draft listing: err = %v, want ErrNotFound", err)
	}
	// ...but its OWNER still sees them: a business in review still has places to
	// configure, and the only route used to be the public one.
	if got, err := svc.Locations(ctx, b.ID, owner.ID); err != nil || len(got) != 1 {
		t.Errorf("the owner of a draft: %d locations, err = %v", len(got), err)
	}
	if _, err := svc.Locations(ctx, b.ID, testutil.User(t, h).ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a stranger on a draft: err = %v, want ErrNotFound", err)
	}
}
