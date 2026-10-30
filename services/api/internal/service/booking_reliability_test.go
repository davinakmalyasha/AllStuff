//go:build !race

package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
	"bizverse/api/internal/ws"
)

// Booking Reliability Pack: moving a booking, reminding before it, and asking
// for a review after it. All three are timing problems, so the tests are about
// the window arithmetic and the exactly-once claim.

func newTestReliability(t *testing.T) (*Reliability, *Booking, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	notifier := NewNotifier(h.Repos, cfg, email.NewSender(email.ResendConfig{AppEnv: "test"}), ws.NewHub(logger, nil), logger)
	return NewReliability(h.Repos, notifier, logger), NewBooking(h.Repos, notifier), h
}

func countNotifications(t *testing.T, h *testutil.H, userID, ntype string) int {
	t.Helper()
	var n int
	// h.Repos (the test transaction), not h.Pool: fixtures and the code under
	// test share the transaction, and a pool read would see none of it.
	if err := h.Repos.QueryRow(context.Background(),
		`SELECT count(*) FROM notifications WHERE user_id=$1 AND type=$2`, userID, ntype).Scan(&n); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return n
}

func TestBookingReschedule(t *testing.T) {
	_, svc, h := newTestReliability(t)
	ctx := context.Background()
	b, p, owner := bookableFixture(t, h)
	day := nextDay()
	customer := testutil.User(t, h)

	booked, err := svc.Book(ctx, customer.ID, b.ID, p.ID, day.Add(10*time.Hour), "")
	if err != nil {
		t.Fatalf("book: %v", err)
	}
	// Claims from the reminder sweep must not survive a move, or the new time
	// is silently never reminded.
	for _, kind := range []string{"reminder_24h", "reminder_2h"} {
		h.Exec(t, `INSERT INTO appointment_events (id, appointment_id, kind) VALUES ($1, $2, $3)`,
			util.NewUUID(), booked.ID, kind)
	}

	moved, err := svc.Reschedule(ctx, customer.ID, booked.ID, day.Add(11*time.Hour))
	if err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	if !moved.StartsAt.Equal(day.Add(11 * time.Hour)) {
		t.Errorf("starts_at = %v, want 11:00", moved.StartsAt)
	}
	if !moved.EndsAt.Equal(day.Add(12 * time.Hour)) {
		t.Errorf("ends_at = %v, want the same one-hour duration", moved.EndsAt)
	}
	var claims int
	if err := h.Repos.QueryRow(ctx,
		`SELECT count(*) FROM appointment_events WHERE appointment_id=$1`, booked.ID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims != 0 {
		t.Errorf("reminder claims = %d, want them released for the new time", claims)
	}
	if n := countNotifications(t, h, owner.ID, "appointment_rescheduled"); n != 1 {
		t.Errorf("owner notifications = %d, want 1", n)
	}

	// A stranger cannot move someone else's booking.
	stranger := testutil.User(t, h)
	if _, err := svc.Reschedule(ctx, stranger.ID, booked.ID, day.Add(13*time.Hour)); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger: err = %v, want ErrForbidden", err)
	}

	// Off-grid times are rejected by the same derived availability as booking.
	if _, err := svc.Reschedule(ctx, customer.ID, booked.ID, day.Add(10*time.Hour+30*time.Minute)); err == nil {
		t.Error("off-grid reschedule was accepted")
	}

	// A slot another customer already holds is a conflict, not a silent move.
	other := testutil.User(t, h)
	if _, err := svc.Book(ctx, other.ID, b.ID, p.ID, day.Add(12*time.Hour), ""); err != nil {
		t.Fatalf("second book: %v", err)
	}
	// The slot is already taken, so it is no longer in the derived availability
	// and the service rejects it before writing. ErrSlotTaken is reserved for the
	// true race where two requests pass the scan and the EXCLUDE constraint
	// catches the second; both outcomes must refuse the move.
	if _, err := svc.Reschedule(ctx, customer.ID, booked.ID, day.Add(12*time.Hour)); err == nil {
		t.Error("reschedule onto a taken slot was accepted")
	}
}

func TestReliabilityRemindersClaimedOnce(t *testing.T) {
	svc, _, h := newTestReliability(t)
	ctx := context.Background()
	b, p, _ := bookableFixture(t, h)
	customer := testutil.User(t, h)

	now := time.Now().UTC()
	seedAppointment(t, h, b.ID, p.ID, customer.ID, "confirmed", now.Add(24*time.Hour+30*time.Minute))
	seedAppointment(t, h, b.ID, p.ID, customer.ID, "confirmed", now.Add(2*time.Hour+30*time.Minute))

	svc.Run(ctx)
	if n := countNotifications(t, h, customer.ID, "appointment_reminder"); n != 2 {
		t.Errorf("reminders after first run = %d, want 2 (24h and 2h)", n)
	}
	// A second tick inside the same windows must not repeat either one.
	svc.Run(ctx)
	if n := countNotifications(t, h, customer.ID, "appointment_reminder"); n != 2 {
		t.Errorf("reminders after second run = %d, want still 2", n)
	}

	// Neither a cancelled booking nor one still awaiting owner confirmation is
	// reminded: only a confirmed slot is a fact the customer can act on. The
	// pending row lives on a second business because unstaffed appointments are
	// exclusive per business, and the first one already holds those windows.
	skipped := testutil.User(t, h)
	b2, p2, _ := bookableFixture(t, h)
	seedAppointment(t, h, b2.ID, p2.ID, skipped.ID, "cancelled", now.Add(24*time.Hour+10*time.Minute))
	seedAppointment(t, h, b2.ID, p2.ID, skipped.ID, "pending", now.Add(2*time.Hour+10*time.Minute))
	svc.Run(ctx)
	if n := countNotifications(t, h, skipped.ID, "appointment_reminder"); n != 0 {
		t.Errorf("non-confirmed bookings got %d reminders, want 0", n)
	}
}

func TestReviewNudgeClaimedOnceAndSkippedAfterReview(t *testing.T) {
	svc, _, h := newTestReliability(t)
	ctx := context.Background()
	b, p, _ := bookableFixture(t, h)

	nostalgic := testutil.User(t, h)
	alreadyReviewed := testutil.User(t, h)
	seedAppointment(t, h, b.ID, p.ID, nostalgic.ID, "completed", time.Now().UTC().Add(-3*time.Hour))
	seedAppointment(t, h, b.ID, p.ID, alreadyReviewed.ID, "completed", time.Now().UTC().Add(-3*time.Hour))
	testutil.Review(t, h, b.ID, alreadyReviewed.ID, 5)

	svc.Run(ctx)
	if n := countNotifications(t, h, nostalgic.ID, "review_nudge"); n != 1 {
		t.Errorf("nudges = %d, want 1", n)
	}
	if n := countNotifications(t, h, alreadyReviewed.ID, "review_nudge"); n != 0 {
		t.Errorf("customer who already reviewed got %d nudges, want 0", n)
	}
	svc.Run(ctx)
	if n := countNotifications(t, h, nostalgic.ID, "review_nudge"); n != 1 {
		t.Errorf("nudges after second run = %d, want still 1", n)
	}
}
