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
	"bizverse/api/internal/repo"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
	"bizverse/api/internal/ws"
)

// Bookings derive availability from hours + duration + existing appointments +
// blackouts, so the tests are about the derivation: what is offered, what is
// withheld, and who may change it.

func newTestBooking(t *testing.T) (*Booking, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	notifier := NewNotifier(h.Repos, cfg, email.NewSender(email.ResendConfig{AppEnv: "test"}), ws.NewHub(logger, nil), logger)
	return NewBooking(h.Repos, notifier), h
}

// bookableFixture is a verified listing with a published one-hour service, in
// UTC and open every day 09:00-17:00, so the expected slots are fixed.
func bookableFixture(t *testing.T, h *testutil.H) (*domain.Business, *domain.Product, *domain.User) {
	t.Helper()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Clinic")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Booking Clinic", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		Status: domain.BusinessVerified,
	})
	h.Exec(t, `UPDATE businesses SET timezone='UTC' WHERE id=$1`, b.ID)
	p := testutil.Product(t, h, b.ID, "Consultation")
	h.Exec(t, `UPDATE products SET type='service', duration_minutes=60 WHERE id=$1`, p.ID)
	return b, p, owner
}

func nextDay() time.Time {
	return time.Now().UTC().AddDate(0, 0, 1).Truncate(24 * time.Hour)
}

func slotTimes(slots []domain.Slot) []string {
	out := make([]string, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.StartsAt.UTC().Format("15:04"))
	}
	return out
}

func TestBookingSlotsFromHours(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, p, _ := bookableFixture(t, h)

	slots, err := svc.Slots(ctx, b.ID, p.ID, nextDay())
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	// 09:00-17:00 in one-hour steps: the last start is 16:00, because a 17:00
	// start would end at 18:00.
	want := []string{"09:00", "10:00", "11:00", "12:00", "13:00", "14:00", "15:00", "16:00"}
	got := slotTimes(slots)
	if len(got) != len(want) {
		t.Fatalf("slots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slots = %v, want %v", got, want)
		}
	}
	if d := slots[0].EndsAt.Sub(slots[0].StartsAt); d != time.Hour {
		t.Errorf("slot length = %v, want 1h", d)
	}

	// A closed weekday has no slots.
	sat := nextDay()
	for sat.Weekday() != time.Saturday {
		sat = sat.AddDate(0, 0, 1)
	}
	h.Exec(t, `UPDATE businesses SET hours = jsonb_set(hours, '{sat}', '{"closed":true,"open":"09:00","close":"17:00"}'::jsonb) WHERE id=$1`, b.ID)
	slots, err = svc.Slots(ctx, b.ID, p.ID, sat)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Errorf("a closed day offered %d slots", len(slots))
	}
}

func TestBookingSlotsRespectSpecialHours(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, p, _ := bookableFixture(t, h)
	day := nextDay()

	// A special day shortens the window: 10:00-12:00 leaves two one-hour slots.
	h.Exec(t, `UPDATE businesses SET special_hours = jsonb_build_object($2::text, '{"open":"10:00","close":"12:00"}'::jsonb) WHERE id=$1`,
		b.ID, day.Format("2006-01-02"))
	slots, err := svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if got := slotTimes(slots); len(got) != 2 || got[0] != "10:00" || got[1] != "11:00" {
		t.Errorf("special hours slots = %v, want 10:00 and 11:00", got)
	}

	// A special CLOSURE wins over the weekday hours.
	h.Exec(t, `UPDATE businesses SET special_hours = jsonb_build_object($2::text, '{"closed":true,"open":"09:00","close":"17:00"}'::jsonb) WHERE id=$1`,
		b.ID, day.Format("2006-01-02"))
	slots, err = svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Errorf("a closed special day offered %d slots", len(slots))
	}
}

func TestBookingSlotsExcludeAppointmentsAndBlackouts(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, p, owner := bookableFixture(t, h)
	day := nextDay()

	customer := testutil.User(t, h)
	if _, err := svc.Book(ctx, customer.ID, b.ID, p.ID, day.Add(9*time.Hour), ""); err != nil {
		t.Fatalf("Book: %v", err)
	}
	// Black out 10:00-12:00, which removes both of those starts.
	if _, err := svc.AddBlackout(ctx, owner.ID, b.ID, day.Add(10*time.Hour), day.Add(12*time.Hour), "maintenance"); err != nil {
		t.Fatalf("AddBlackout: %v", err)
	}

	slots, err := svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	got := slotTimes(slots)
	for _, gone := range []string{"09:00", "10:00", "11:00"} {
		for _, s := range got {
			if s == gone {
				t.Errorf("slot %s is still offered: %v", gone, got)
			}
		}
	}
	// 12:00 is free again: the blackout is half-open.
	found := false
	for _, s := range got {
		if s == "12:00" {
			found = true
		}
	}
	if !found {
		t.Errorf("12:00 was withheld by a blackout that ends at 12:00: %v", got)
	}

	// A cancellation frees the slot again.
	if err := svc.Cancel(ctx, customer.ID, mustLastID(t, h, b.ID)); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	slots, err = svc.Slots(ctx, b.ID, p.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if got := slotTimes(slots); len(got) == 0 || got[0] != "09:00" {
		t.Errorf("after cancelling, slots = %v, want 09:00 back", got)
	}
}

func mustLastID(t *testing.T, h *testutil.H, businessID string) string {
	t.Helper()
	var id string
	if err := h.QueryRow(t, `SELECT id FROM appointments WHERE business_id=$1 ORDER BY created_at DESC LIMIT 1`, businessID).Scan(&id); err != nil {
		t.Fatalf("last appointment: %v", err)
	}
	return id
}

func TestBookingBookValidation(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, p, _ := bookableFixture(t, h)
	day := nextDay()
	customer := testutil.User(t, h)

	// Off the grid: 09:30 is not a start the service offers.
	if _, err := svc.Book(ctx, customer.ID, b.ID, p.ID, day.Add(9*time.Hour+30*time.Minute), ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("an off-grid time: err = %v, want ErrValidation", err)
	}
	// In the past.
	if _, err := svc.Book(ctx, customer.ID, b.ID, p.ID, time.Now().UTC().Add(-48*time.Hour), ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("a past time: err = %v, want ErrValidation", err)
	}
	// A closed day.
	sat := day
	for sat.Weekday() != time.Saturday {
		sat = sat.AddDate(0, 0, 1)
	}
	h.Exec(t, `UPDATE businesses SET hours = jsonb_set(hours, '{sat}', '{"closed":true,"open":"09:00","close":"17:00"}'::jsonb) WHERE id=$1`, b.ID)
	if _, err := svc.Book(ctx, customer.ID, b.ID, p.ID, sat.Add(9*time.Hour), ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("a closed day: err = %v, want ErrValidation", err)
	}
	// A product that belongs to someone else is not bookable here.
	otherBiz, otherProd, _ := bookableFixture(t, h)
	_ = otherBiz
	if _, err := svc.Book(ctx, customer.ID, b.ID, otherProd.ID, day.Add(9*time.Hour), ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a foreign product: err = %v, want ErrNotFound", err)
	}

	// Notes are bounded.
	if _, err := svc.Book(ctx, customer.ID, b.ID, p.ID, day.Add(9*time.Hour), string(make([]rune, 501))); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("501-rune notes: err = %v, want ErrValidation", err)
	}
}

func TestBookingCancelAuthorization(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, p, owner := bookableFixture(t, h)
	day := nextDay()
	customer := testutil.User(t, h)
	stranger := testutil.User(t, h)

	a, err := svc.Book(ctx, customer.ID, b.ID, p.ID, day.Add(9*time.Hour), "")
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if err := svc.Cancel(ctx, stranger.ID, a.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a stranger: err = %v, want ErrForbidden", err)
	}
	if err := svc.Cancel(ctx, customer.ID, a.ID); err != nil {
		t.Fatalf("the customer: %v", err)
	}
	if err := svc.Cancel(ctx, customer.ID, a.ID); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("a second cancel: err = %v, want ErrValidation", err)
	}

	// The manager may cancel someone else's booking.
	b2, err := svc.Book(ctx, customer.ID, b.ID, p.ID, day.Add(10*time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(ctx, owner.ID, b2.ID); err != nil {
		t.Errorf("the owner: %v", err)
	}

	// An appointment that already happened cannot be cancelled.
	old := &domain.Appointment{
		ID: util.NewUUID(), BusinessID: b.ID, ProductID: p.ID, CustomerID: customer.ID,
		StartsAt: time.Now().Add(-2 * time.Hour), EndsAt: time.Now().Add(-time.Hour),
		Status: domain.AppointmentConfirmed,
	}
	if err := h.Repos.Bookings.CreateAppointment(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(ctx, customer.ID, old.ID); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("a past appointment: err = %v, want ErrValidation", err)
	}
}

// Two overlapping rows cannot both exist: the EXCLUDE constraint is the real
// defence against a double booking, because the slot scan and the insert are not
// atomic with each other.
func TestBookingDoubleBookingIsRejectedByTheDatabase(t *testing.T) {
	_, h := newTestBooking(t)
	ctx := context.Background()
	b, p, _ := bookableFixture(t, h)
	at := nextDay().Add(9 * time.Hour)
	// Real users: customer_id is a foreign key, so a fabricated UUID fails the
	// insert for a reason that has nothing to do with overlap.
	c1 := testutil.User(t, h)
	c2 := testutil.User(t, h)
	c3 := testutil.User(t, h)

	first := &domain.Appointment{
		ID: util.NewUUID(), BusinessID: b.ID, ProductID: p.ID, CustomerID: c1.ID,
		StartsAt: at, EndsAt: at.Add(time.Hour), Status: domain.AppointmentConfirmed,
	}
	if err := h.Repos.Bookings.CreateAppointment(ctx, first); err != nil {
		t.Fatalf("first booking: %v", err)
	}
	// A deliberately overlapping row, inserted around the service's own slot check.
	second := &domain.Appointment{
		ID: util.NewUUID(), BusinessID: b.ID, ProductID: p.ID, CustomerID: c2.ID,
		StartsAt: at.Add(30 * time.Minute), EndsAt: at.Add(90 * time.Minute), Status: domain.AppointmentConfirmed,
	}
	// Back-to-back is allowed: the range is half-open. Checked BEFORE the
	// expected violation below, because a failed statement aborts the shared
	// test transaction and every later query would answer 25P02 instead of
	// whatever it was actually testing.
	third := &domain.Appointment{
		ID: util.NewUUID(), BusinessID: b.ID, ProductID: p.ID, CustomerID: c3.ID,
		StartsAt: at.Add(time.Hour), EndsAt: at.Add(2 * time.Hour), Status: domain.AppointmentConfirmed,
	}
	if err := h.Repos.Bookings.CreateAppointment(ctx, third); err != nil {
		t.Errorf("a back-to-back appointment was refused: %v", err)
	}

	err := h.Repos.Bookings.CreateAppointment(ctx, second)
	if err == nil {
		t.Fatal("an overlapping appointment was accepted")
	}
	if !repo.IsSlotConflict(err) {
		t.Errorf("err = %v, want it recognised as a slot conflict", err)
	}
}

func TestBookingBlackoutRules(t *testing.T) {
	svc, h := newTestBooking(t)
	ctx := context.Background()
	b, _, owner := bookableFixture(t, h)
	stranger := testutil.User(t, h)
	day := nextDay()

	if _, err := svc.AddBlackout(ctx, stranger.ID, b.ID, day, day.Add(time.Hour), ""); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a stranger: err = %v, want ErrForbidden", err)
	}
	if _, err := svc.AddBlackout(ctx, owner.ID, b.ID, day.Add(2*time.Hour), day.Add(time.Hour), ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("an inverted window: err = %v, want ErrValidation", err)
	}
	if _, err := svc.AddBlackout(ctx, owner.ID, b.ID, day, day.AddDate(0, 0, 8), ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("an eight-day blackout: err = %v, want ErrValidation", err)
	}

	bl, err := svc.AddBlackout(ctx, owner.ID, b.ID, day.Add(10*time.Hour), day.Add(12*time.Hour), "training")
	if err != nil {
		t.Fatalf("AddBlackout: %v", err)
	}
	list, err := svc.Blackouts(ctx, owner.ID, b.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Reason != "training" {
		t.Errorf("blackouts = %+v", list)
	}

	if err := svc.RemoveBlackout(ctx, owner.ID, b.ID, bl.ID); err != nil {
		t.Fatalf("RemoveBlackout: %v", err)
	}
	if err := svc.RemoveBlackout(ctx, owner.ID, b.ID, bl.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a second removal: err = %v, want ErrNotFound", err)
	}

	// Re-create a window for the overlap attempt to collide with.
	if _, err := svc.AddBlackout(ctx, owner.ID, b.ID, day.Add(10*time.Hour), day.Add(12*time.Hour), "again"); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	// LAST: the overlap is rejected by the table's EXCLUDE constraint, which
	// aborts the shared test transaction, so anything after it would fail with
	// 25P02 rather than its own error.
	if _, err := svc.AddBlackout(ctx, owner.ID, b.ID, day.Add(11*time.Hour), day.Add(13*time.Hour), "overlap"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("an overlapping blackout: err = %v, want ErrValidation", err)
	}
}
