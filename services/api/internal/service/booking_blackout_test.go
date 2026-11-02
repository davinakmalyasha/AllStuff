//go:build !race

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// A blackout over live appointments silently stranded booked customers:
// availability hid the window but the bookings stayed confirmed and reminders
// still fired. AddBlackout must refuse while appointments are live.
func TestAddBlackoutRefusesLiveAppointments(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, p, owner := bookableFixture(t, h)
	customer := testutil.User(t, h)
	day := nextDay()

	live := &domain.Appointment{
		ID: util.NewUUID(), BusinessID: b.ID, ProductID: p.ID, CustomerID: customer.ID,
		StartsAt: day.Add(9 * time.Hour), EndsAt: day.Add(10 * time.Hour),
		Status: domain.AppointmentConfirmed,
	}
	if err := h.Repos.Bookings.CreateAppointment(ctx, live); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.AddBlackout(ctx, owner.ID, b.ID, day.Add(9*time.Hour), day.Add(10*time.Hour), "stocktake"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("blackout over a confirmed booking: err = %v, want ErrValidation", err)
	}

	// A disjoint window still works.
	if _, err := svc.AddBlackout(ctx, owner.ID, b.ID, day.Add(12*time.Hour), day.Add(13*time.Hour), "lunch"); err != nil {
		t.Errorf("disjoint blackout: %v, want nil", err)
	}
}
