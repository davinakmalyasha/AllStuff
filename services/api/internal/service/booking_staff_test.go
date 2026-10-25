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

// Staff turn availability from "is the shop open" into "is somebody free".
// These tests cover the management surface and the two ways a slot can be
// withheld: qualifications and shifts.

func TestStaffCRUD(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, p, owner := bookableFixture(t, h)
	stranger := testutil.User(t, h)

	if _, err := svc.Staff(ctx, stranger.ID, b.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger list: err = %v, want ErrForbidden", err)
	}
	if _, err := svc.CreateStaff(ctx, stranger.ID, b.ID, "X", "", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger create: err = %v, want ErrForbidden", err)
	}
	if _, err := svc.CreateStaff(ctx, owner.ID, b.ID, "   ", "", ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("blank name: err = %v, want ErrValidation", err)
	}
	if _, err := svc.CreateStaff(ctx, owner.ID, b.ID, "Budi", "not-an-email", ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("bad email: err = %v, want ErrValidation", err)
	}

	st, err := svc.CreateStaff(ctx, owner.ID, b.ID, "  Budi  ", "  BUDI@Example.test ", "+62 812")
	if err != nil {
		t.Fatalf("CreateStaff: %v", err)
	}
	if st.Name != "Budi" {
		t.Errorf("name = %q, want it trimmed", st.Name)
	}
	if st.Email != "budi@example.test" {
		t.Errorf("email = %q, want it lowercased and trimmed", st.Email)
	}
	if !st.Active {
		t.Error("a new staff member is inactive")
	}
	if len(st.Services) != 0 {
		t.Errorf("a new staff member is qualified for %v; qualification must be explicit", st.Services)
	}

	// Partial update.
	off := false
	newName := "Budi Santoso"
	upd, err := svc.UpdateStaff(ctx, owner.ID, b.ID, st.ID, &newName, nil, nil, &off)
	if err != nil {
		t.Fatalf("UpdateStaff: %v", err)
	}
	if upd.Name != "Budi Santoso" || upd.Active {
		t.Errorf("after update: name=%q active=%v", upd.Name, upd.Active)
	}
	// An empty patch is refused rather than silently doing nothing.
	if _, err := svc.UpdateStaff(ctx, owner.ID, b.ID, st.ID, nil, nil, nil, nil); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("empty patch: err = %v, want ErrValidation", err)
	}

	// Qualifications must be this business's own services.
	if _, err := svc.SetStaffServices(ctx, owner.ID, b.ID, st.ID, []string{p.ID}); err != nil {
		t.Fatalf("SetStaffServices: %v", err)
	}
	otherBiz, otherProd, _ := bookableFixture(t, h)
	_ = otherBiz
	if _, err := svc.SetStaffServices(ctx, owner.ID, b.ID, st.ID, []string{otherProd.ID}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("a foreign service: err = %v, want ErrValidation", err)
	}

	// Hours: a shift must be a real interval, and the weekday must exist.
	if _, err := svc.SetStaffHours(ctx, owner.ID, b.ID, st.ID, map[string]domain.DayHours{
		"mon": {Working: true, OpensAt: "14:00", ClosesAt: "10:00"},
	}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("an inverted shift: err = %v, want ErrValidation", err)
	}
	if _, err := svc.SetStaffHours(ctx, owner.ID, b.ID, st.ID, map[string]domain.DayHours{
		"funday": {Working: true, OpensAt: "10:00", ClosesAt: "12:00"},
	}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("an unknown weekday: err = %v, want ErrValidation", err)
	}
	got, err := svc.SetStaffHours(ctx, owner.ID, b.ID, st.ID, map[string]domain.DayHours{
		"mon": {Working: true, OpensAt: "10:00", ClosesAt: "12:00"},
		"tue": {Working: false},
	})
	if err != nil {
		t.Fatalf("SetStaffHours: %v", err)
	}
	if h := got.Hours["mon"]; !h.Working || h.OpensAt != "10:00" || h.ClosesAt != "12:00" {
		t.Errorf("monday = %+v", h)
	}
	if h := got.Hours["tue"]; h.Working {
		t.Errorf("tuesday = %+v, want a day off", got.Hours["tue"])
	}

	if err := svc.DeleteStaff(ctx, owner.ID, b.ID, st.ID); err != nil {
		t.Fatalf("DeleteStaff: %v", err)
	}
	if err := svc.DeleteStaff(ctx, owner.ID, b.ID, st.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete: err = %v, want ErrNotFound", err)
	}
}

func TestStaffAwareSlots(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, p, owner := bookableFixture(t, h)
	day := nextDay()
	weekday := bookableDayKeys[day.Weekday()]

	// A qualified staff member with no hours rows inherits the business hours.
	a, err := svc.CreateStaff(ctx, owner.ID, b.ID, "Ani", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetStaffServices(ctx, owner.ID, b.ID, a.ID, []string{p.ID}); err != nil {
		t.Fatal(err)
	}

	slots, err := svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if len(slots) != 8 {
		t.Fatalf("slots = %d, want the eight business-hours starts", len(slots))
	}
	if slots[0].StaffAvailable != 1 {
		t.Errorf("StaffAvailable = %d, want 1", slots[0].StaffAvailable)
	}

	// Booking the first slot consumes Ani, and with nobody else qualified the
	// slot disappears rather than being double-booked.
	customer := testutil.User(t, h)
	first := slots[0].StartsAt
	appt, err := svc.Book(ctx, customer.ID, b.ID, p.ID, first, "")
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if appt.StaffID != a.ID {
		t.Errorf("staff = %q, want the only qualified member", appt.StaffID)
	}
	slots, err = svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range slots {
		if s.StartsAt.Equal(first) {
			t.Error("a booked slot is still offered")
		}
	}

	// A second staff member, working a narrower shift on that weekday, restores
	// availability only inside their shift.
	bi, err := svc.CreateStaff(ctx, owner.ID, b.ID, "Bima", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetStaffServices(ctx, owner.ID, b.ID, bi.ID, []string{p.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetStaffHours(ctx, owner.ID, b.ID, bi.ID, map[string]domain.DayHours{
		weekday: {Working: true, OpensAt: "10:00", ClosesAt: "11:00"},
	}); err != nil {
		t.Fatal(err)
	}

	// A staff member with NO qualifications must not appear: qualification is
	// explicit, so an unassigned person cannot silently absorb bookings.
	chef, err := svc.CreateStaff(ctx, owner.ID, b.ID, "Chef", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(chef.Services) != 0 {
		t.Fatalf("a new staff member is qualified for %v", chef.Services)
	}

	slots, err = svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	byTime := map[string]domain.Slot{}
	for _, s := range slots {
		byTime[s.StartsAt.UTC().Format("15:04")] = s
	}
	// 09:00: Ani is booked and Bima has not started.
	if _, ok := byTime["09:00"]; ok {
		t.Error("09:00 is offered with nobody free")
	}
	// 10:00: both are free.
	if got := byTime["10:00"].StaffAvailable; got != 2 {
		t.Errorf("10:00 StaffAvailable = %d, want 2", got)
	}
	// 11:00: Bima's shift has ended, so only Ani.
	if got := byTime["11:00"].StaffAvailable; got != 1 {
		t.Errorf("11:00 StaffAvailable = %d, want 1", got)
	}

	// A named staff member must actually be free.
	if _, err := svc.BookWithStaff(ctx, customer.ID, b.ID, p.ID, bi.ID, day.Add(10*time.Hour), ""); err != nil {
		t.Fatalf("book with Bima: %v", err)
	}
	if _, err := svc.BookWithStaff(ctx, customer.ID, b.ID, p.ID, bi.ID, day.Add(11*time.Hour), ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("Bima outside her shift: err = %v, want ErrValidation", err)
	}
	// ...and the slot can still be taken by the other staff member.
	if _, err := svc.Book(ctx, customer.ID, b.ID, p.ID, day.Add(11*time.Hour), ""); err != nil {
		t.Errorf("Ani at 11:00: %v", err)
	}

	// SlotsForStaff narrows the list to one person's calendar.
	only, err := svc.SlotsForStaff(ctx, b.ID, p.ID, bi.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 0 {
		// Bima is booked 10:00 and works only 10:00-11:00.
		t.Errorf("Bima's remaining slots = %v, want none", only)
	}
	// A day off is a day off even when the times are still configured - which is
	// what the dashboard toggle leaves behind.
	if _, err := svc.SetStaffHours(ctx, owner.ID, b.ID, bi.ID, map[string]domain.DayHours{
		weekday: {Working: false, OpensAt: "10:00", ClosesAt: "12:00"},
	}); err != nil {
		t.Fatal(err)
	}
	if off, err := svc.SlotsForStaff(ctx, b.ID, p.ID, bi.ID, day); err != nil {
		t.Fatal(err)
	} else if len(off) != 0 {
		t.Errorf("a staff member on a day off has %d slots", len(off))
	}

	if _, err := svc.SlotsForStaff(ctx, b.ID, p.ID, "not-a-staff-id", day); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown staff: err = %v, want ErrNotFound", err)
	}
}
