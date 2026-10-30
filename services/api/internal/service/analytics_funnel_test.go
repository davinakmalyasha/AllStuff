//go:build !race

package service

import (
	"context"
	"testing"
	"time"

	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// The funnel answers the three questions owners actually ask: how many views
// become bookings, when is peak, and do customers come back. All three derive
// from appointments the shop already collects.

func seedAppointment(t *testing.T, h *testutil.H, bizID, productID, customerID, status string, starts time.Time) {
	t.Helper()
	h.Exec(t, `INSERT INTO appointments (id, business_id, product_id, customer_id, starts_at, ends_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		util.NewUUID(), bizID, productID, customerID, starts, starts.Add(time.Hour), status)
}

func TestAnalyticsFunnelConversionPeakRepeat(t *testing.T) {
	svc, h := newTestAnalytics(t)
	ctx := context.Background()
	biz, owner := ownerBusiness(t, h)
	h.Exec(t, `UPDATE businesses SET timezone='Asia/Jakarta' WHERE id=$1`, biz.ID)

	rater := testutil.User(t, h)
	repeat := testutil.User(t, h)
	for i := 0; i < 4; i++ {
		addView(t, h, rater.ID, biz.ID)
	}
	prod := testutil.Product(t, h, biz.ID, "Cut")

	// 10:00 and 10:30 Jakarta = 03:00/03:30 UTC. Three bookings at hour 10
	// Jakarta, one at 14:00 Jakarta, so peak is unambiguously 10.
	base := time.Date(2026, 1, 5, 3, 0, 0, 0, time.UTC)
	seedAppointment(t, h, biz.ID, prod.ID, rater.ID, "completed", base)
	seedAppointment(t, h, biz.ID, prod.ID, rater.ID, "completed", base.Add(30*time.Minute))
	seedAppointment(t, h, biz.ID, prod.ID, repeat.ID, "completed", base.Add(15*time.Minute))
	seedAppointment(t, h, biz.ID, prod.ID, repeat.ID, "no_show", time.Date(2026, 1, 6, 7, 0, 0, 0, time.UTC))
	// Cancelled bookings hold no slot and must not count as demand.
	seedAppointment(t, h, biz.ID, prod.ID, repeat.ID, "cancelled", base)

	got, err := svc.ForBusiness(ctx, owner.ID, biz.ID, "all")
	if err != nil {
		t.Fatal(err)
	}
	if got.Bookings != 4 {
		t.Errorf("Bookings = %d, want 4 (cancelled excluded)", got.Bookings)
	}
	if got.CompletedBookings != 3 {
		t.Errorf("CompletedBookings = %d, want 3", got.CompletedBookings)
	}
	if got.NoShows != 1 {
		t.Errorf("NoShows = %d, want 1", got.NoShows)
	}
	if got.ConversionRate != 1.0 {
		t.Errorf("ConversionRate = %v, want 1.0 (4 bookings / 4 views)", got.ConversionRate)
	}
	if got.PeakHour == nil || *got.PeakHour != 10 {
		t.Errorf("PeakHour = %v, want 10 (Jakarta)", got.PeakHour)
	}
	// rater completed twice (repeat), repeat-customer completed once:
	// 1 of 2 distinct completed customers repeats = 0.5.
	if got.RepeatRate == nil || *got.RepeatRate != 0.5 {
		t.Errorf("RepeatRate = %v, want 0.5", got.RepeatRate)
	}
}

func TestAnalyticsFunnelEmpty(t *testing.T) {
	svc, h := newTestAnalytics(t)
	ctx := context.Background()
	biz, owner := ownerBusiness(t, h)

	got, err := svc.ForBusiness(ctx, owner.ID, biz.ID, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Bookings != 0 || got.ConversionRate != 0 {
		t.Errorf("empty funnel = %+v, want zeros", got)
	}
	if got.PeakHour != nil {
		t.Errorf("PeakHour = %v, want nil with no bookings", *got.PeakHour)
	}
	if got.RepeatRate != nil {
		t.Errorf("RepeatRate = %v, want nil with no completions", *got.RepeatRate)
	}
}
