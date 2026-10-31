//go:build !race

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
)

// Waitlist: a full day is lost demand, and a cancellation is the only moment
// it can be recovered. The tests cover the queue's exactly-one-per-cancellation
// rule and the ownership rules around leaving.

func newTestWaitlist(t *testing.T) (*Waitlist, *Booking, *testutil.H) {
	t.Helper()
	_, booking, h := newTestReliability(t)
	return NewWaitlist(h.Repos), booking, h
}

func countWaitlist(t *testing.T, h *testutil.H) int {
	t.Helper()
	var n int
	if err := h.Repos.QueryRow(context.Background(), `SELECT count(*) FROM booking_waitlist`).Scan(&n); err != nil {
		t.Fatalf("count waitlist: %v", err)
	}
	return n
}

func TestWaitlistJoinIsIdempotent(t *testing.T) {
	wl, _, h := newTestWaitlist(t)
	ctx := context.Background()
	b, p, _ := bookableFixture(t, h)
	customer := testutil.User(t, h)
	day := nextDay().Format("2006-01-02")

	first, err := wl.Join(ctx, customer.ID, b.ID, p.ID, day)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if first.Day != day || first.ProductName != p.Name {
		t.Errorf("entry = %+v, want day %s and product %q", first, day, p.Name)
	}
	second, err := wl.Join(ctx, customer.ID, b.ID, p.ID, day)
	if err != nil {
		t.Fatalf("second join: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second join created a new entry: %s vs %s", second.ID, first.ID)
	}
	if n := countWaitlist(t, h); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}

	// A past day is not a request anyone can serve.
	past := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	if _, err := wl.Join(ctx, customer.ID, b.ID, p.ID, past); err == nil {
		t.Error("joining a past day was accepted")
	}

	// Malformed day is a validation error, not a database error.
	if _, err := wl.Join(ctx, customer.ID, b.ID, p.ID, "tomorrow"); err == nil {
		t.Error("malformed day was accepted")
	}
}

func TestWaitlistLeaveIsOwnershipScoped(t *testing.T) {
	wl, _, h := newTestWaitlist(t)
	ctx := context.Background()
	b, p, _ := bookableFixture(t, h)
	customer := testutil.User(t, h)
	day := nextDay().Format("2006-01-02")

	entry, err := wl.Join(ctx, customer.ID, b.ID, p.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	stranger := testutil.User(t, h)
	if err := wl.Leave(ctx, stranger.ID, entry.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger leave: err = %v, want ErrNotFound", err)
	}
	if n := countWaitlist(t, h); n != 1 {
		t.Errorf("stranger removed the entry: rows = %d", n)
	}
	if err := wl.Leave(ctx, customer.ID, entry.ID); err != nil {
		t.Fatalf("owner leave: %v", err)
	}
	if n := countWaitlist(t, h); n != 0 {
		t.Errorf("rows after leave = %d, want 0", n)
	}
}

func TestCancellationNotifiesFirstWaiterOnce(t *testing.T) {
	wl, booking, h := newTestWaitlist(t)
	ctx := context.Background()
	b, p, _ := bookableFixture(t, h)
	day := nextDay()
	dayStr := day.Format("2006-01-02")

	firstWaiter := testutil.User(t, h)
	secondWaiter := testutil.User(t, h)
	if _, err := wl.Join(ctx, firstWaiter.ID, b.ID, p.ID, dayStr); err != nil {
		t.Fatal(err)
	}
	// Ordering is created_at; two inserts in the same millisecond would tie, so
	// pin the timestamps explicitly to make "first" unambiguous.
	h.Exec(t, `UPDATE booking_waitlist SET created_at = now() - interval '2 minutes' WHERE customer_id = $1`, firstWaiter.ID)
	if _, err := wl.Join(ctx, secondWaiter.ID, b.ID, p.ID, dayStr); err != nil {
		t.Fatal(err)
	}

	booker := testutil.User(t, h)
	appt, err := booking.Book(ctx, booker.ID, b.ID, p.ID, day.Add(10*time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := booking.Cancel(ctx, booker.ID, appt.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if n := countNotifications(t, h, firstWaiter.ID, "slot_open"); n != 1 {
		t.Errorf("first waiter notifications = %d, want 1", n)
	}
	if n := countNotifications(t, h, secondWaiter.ID, "slot_open"); n != 0 {
		t.Errorf("second waiter notifications = %d, want 0 until the next cancellation", n)
	}

	// A second cancellation on the same day reaches the next waiter, not the
	// one already told.
	second, err := booking.Book(ctx, booker.ID, b.ID, p.ID, day.Add(11*time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := booking.Cancel(ctx, booker.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if n := countNotifications(t, h, secondWaiter.ID, "slot_open"); n != 1 {
		t.Errorf("second waiter notifications = %d, want 1", n)
	}
	if n := countNotifications(t, h, firstWaiter.ID, "slot_open"); n != 1 {
		t.Errorf("first waiter was notified again: %d", n)
	}
}

func TestWaitlistDemandIsManagerOnlyAndCountsByDay(t *testing.T) {
	wl, _, h := newTestWaitlist(t)
	ctx := context.Background()
	b, p, owner := bookableFixture(t, h)
	day := nextDay().Format("2006-01-02")

	first := testutil.User(t, h)
	second := testutil.User(t, h)
	if _, err := wl.Join(ctx, first.ID, b.ID, p.ID, day); err != nil {
		t.Fatal(err)
	}
	if _, err := wl.Join(ctx, second.ID, b.ID, p.ID, day); err != nil {
		t.Fatal(err)
	}
	// One of the two has already been told about a freed time.
	h.Exec(t, `UPDATE booking_waitlist SET notified_at = now() WHERE customer_id = $1`, first.ID)

	days, err := wl.Demand(ctx, owner.ID, b.ID)
	if err != nil {
		t.Fatalf("demand: %v", err)
	}
	if len(days) != 1 || days[0].Day != day {
		t.Fatalf("days = %+v, want one row for %s", days, day)
	}
	if days[0].Waiting != 1 || days[0].Notified != 1 {
		t.Errorf("counts = %+v, want 1 waiting and 1 told", days[0])
	}

	stranger := testutil.User(t, h)
	if _, err := wl.Demand(ctx, stranger.ID, b.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger demand: err = %v, want ErrForbidden", err)
	}
	// A read-only viewer is not a manager and does not get demand.
	viewer := testutil.User(t, h)
	inviteAccepted(t, h, b.ID, owner.ID, viewer.ID, "viewer")
	if _, err := wl.Demand(ctx, viewer.ID, b.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer demand: err = %v, want ErrForbidden", err)
	}
}
