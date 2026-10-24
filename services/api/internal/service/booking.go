package service

import (
	"context"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Booking derives availability from what the business already has - hours,
// special hours, service duration, live appointments and blackouts - rather than
// from a materialised slot table. See migration 0044 for why there is no
// booking_slots table.
type Booking struct {
	repos    *repo.Repos
	notifier *Notifier
}

func NewBooking(repos *repo.Repos, notifier *Notifier) *Booking {
	return &Booking{repos: repos, notifier: notifier}
}

// maxSlotsPerDay bounds the response. A 5-minute service over a 24-hour window
// is 288 slots; nobody pages through that, and it is a cheap way to turn one
// request into a large payload.
const maxSlotsPerDay = 96

var bookableDayKeys = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

func businessLocation(b *domain.Business) *time.Location {
	loc, err := time.LoadLocation(strings.TrimSpace(b.Timezone))
	if err != nil || loc == nil {
		return time.UTC
	}
	return loc
}

func clockOn(day time.Time, v string) (time.Time, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, day.Location()), true
}

// openingFor resolves a calendar day's window: special hours for that date,
// then the weekday's hours. Closed, missing or half-specified days are not
// bookable.
func openingFor(b *domain.Business, day time.Time) (time.Time, time.Time, bool) {
	if raw, ok := b.SpecialHours[day.Format("2006-01-02")]; ok {
		if m, ok := raw.(map[string]any); ok {
			if closed, _ := m["closed"].(bool); closed {
				return time.Time{}, time.Time{}, false
			}
			openS, _ := m["open"].(string)
			closeS, _ := m["close"].(string)
			if open, ok1 := clockOn(day, openS); ok1 {
				if shut, ok2 := clockOn(day, closeS); ok2 && shut.After(open) {
					return open, shut, true
				}
			}
			return time.Time{}, time.Time{}, false
		}
	}
	raw, ok := b.Hours[bookableDayKeys[day.Weekday()]]
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	if closed, _ := m["closed"].(bool); closed {
		return time.Time{}, time.Time{}, false
	}
	openS, _ := m["open"].(string)
	closeS, _ := m["close"].(string)
	open, ok1 := clockOn(day, openS)
	shut, ok2 := clockOn(day, closeS)
	if !ok1 || !ok2 || !shut.After(open) {
		return time.Time{}, time.Time{}, false
	}
	return open, shut, true
}

// bookableService loads the product and checks it can be booked at all.
func (s *Booking) bookableService(ctx context.Context, businessID, productID string) (*domain.Business, *domain.Product, error) {
	b, err := s.repos.Businesses.GetByID(ctx, businessID)
	if err != nil {
		return nil, nil, err
	}
	if b == nil || (b.Status != domain.BusinessVerified && b.Status != domain.BusinessPaused) {
		return nil, nil, domain.ErrNotFound
	}
	p, err := s.repos.Products.GetByID(ctx, productID)
	if err != nil {
		return nil, nil, err
	}
	if p == nil || p.BusinessID != businessID || p.DeletedAt != nil || !p.IsPublished {
		return nil, nil, domain.ErrNotFound
	}
	if p.Type != domain.ProductTypeService || p.DurationMinutes == nil || *p.DurationMinutes <= 0 {
		return nil, nil, domain.ErrValidation.WithField("product_id", "That service is not bookable.")
	}
	return b, p, nil
}

// Slots returns the bookable start times for one calendar day, in the
// business's timezone. day is any instant inside the wanted date.
func (s *Booking) Slots(ctx context.Context, businessID, productID string, day time.Time) ([]domain.Slot, error) {
	b, p, err := s.bookableService(ctx, businessID, productID)
	if err != nil {
		return nil, err
	}
	loc := businessLocation(b)
	// The CALENDAR DATE is taken from the caller's year/month/day fields and
	// rebuilt in the business timezone. Converting the instant instead (day.In(loc))
	// moves a UTC-midnight request to the previous day for any negative offset,
	// which would answer "the day before" to a client that asked for a date.
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	open, shut, ok := openingFor(b, dayStart)
	if !ok {
		return []domain.Slot{}, nil
	}
	dur := time.Duration(*p.DurationMinutes) * time.Minute
	dayEnd := dayStart.AddDate(0, 0, 1)
	busy, err := s.repos.Bookings.BusyRanges(ctx, businessID, open.Add(-dur), dayEnd)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	out := make([]domain.Slot, 0, 16)
	for start := open; !start.Add(dur).After(shut); start = start.Add(dur) {
		if len(out) >= maxSlotsPerDay {
			break
		}
		if !start.After(now) {
			continue
		}
		end := start.Add(dur)
		clash := false
		for _, r := range busy {
			if start.Before(r[1]) && end.After(r[0]) {
				clash = true
				break
			}
		}
		if clash {
			continue
		}
		out = append(out, domain.Slot{StartsAt: start, EndsAt: end})
	}
	return out, nil
}

// Book reserves one slot.
//
// The chosen time must be an EXACT generated slot: that rejects off-grid times,
// past times and closed days in one check, and it stops a client inventing a
// duration by supplying a start outside the grid. The insert is still not atomic
// with the slot scan - two requests can both see a slot free - so the database's
// EXCLUDE constraint is the real guarantee, and a conflict there is reported as
// ErrSlotTaken rather than as an internal error.
func (s *Booking) Book(ctx context.Context, customerID, businessID, productID string, at time.Time, notes string) (*domain.Appointment, error) {
	notes = strings.TrimSpace(notes)
	if len([]rune(notes)) > 500 {
		return nil, domain.ErrValidation.WithField("notes", "Notes must be 500 characters or fewer.")
	}
	b, p, err := s.bookableService(ctx, businessID, productID)
	if err != nil {
		return nil, err
	}
	slots, err := s.Slots(ctx, businessID, productID, at.In(businessLocation(b)))
	if err != nil {
		return nil, err
	}
	var chosen *domain.Slot
	for i := range slots {
		if slots[i].StartsAt.Equal(at) {
			chosen = &slots[i]
			break
		}
	}
	if chosen == nil {
		return nil, domain.ErrValidation.WithField("starts_at", "That time is not available.")
	}

	a := &domain.Appointment{
		ID:         util.NewUUID(),
		BusinessID: businessID,
		ProductID:  productID,
		CustomerID: customerID,
		StartsAt:   chosen.StartsAt,
		EndsAt:     chosen.EndsAt,
		Status:     domain.AppointmentConfirmed,
		Notes:      notes,
	}
	if err := s.repos.Bookings.CreateAppointment(ctx, a); err != nil {
		if repo.IsSlotConflict(err) {
			return nil, domain.ErrSlotTaken
		}
		return nil, err
	}
	if b.OwnerID != customerID { // lint:allow: not authorisation - suppresses notifying someone about their own booking
		s.notifier.Create(ctx, b.OwnerID, "appointment_booked", map[string]any{
			"appointment_id": a.ID, "business_id": businessID,
			"product": p.Name, "starts_at": a.StartsAt,
		})
	}
	return s.repos.Bookings.GetAppointment(ctx, a.ID)
}

// Cancel is available to the customer and to anyone who manages the listing.
func (s *Booking) Cancel(ctx context.Context, userID, appointmentID string) error {
	a, err := s.repos.Bookings.GetAppointment(ctx, appointmentID)
	if err != nil {
		return err
	}
	if a == nil {
		return domain.ErrNotFound
	}
	if a.CustomerID != userID {
		can, err := s.repos.Businesses.CanManageBusiness(ctx, userID, a.BusinessID)
		if err != nil {
			return err
		}
		if !can {
			return domain.ErrForbidden
		}
	}
	// "Cancelling" an appointment that already happened rewrites history.
	if !a.EndsAt.After(time.Now()) {
		return domain.ErrValidation.WithField("_", "That appointment has already passed.")
	}
	ok, err := s.repos.Bookings.SetAppointmentStatus(ctx, appointmentID, domain.AppointmentCancelled, userID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrValidation.WithField("_", "That appointment is no longer active.")
	}
	// Tell the other side: a customer cancelling notifies the owner, the owner
	// cancelling notifies the customer.
	if a.CustomerID == userID {
		if b, _ := s.repos.Businesses.GetByID(ctx, a.BusinessID); b != nil && b.OwnerID != "" { // lint:allow: not authorisation - a null-check on the notification RECIPIENT
			s.notifier.Create(ctx, b.OwnerID, "appointment_cancelled", map[string]any{
				"appointment_id": a.ID, "business_id": a.BusinessID, "starts_at": a.StartsAt,
			})
		}
	} else {
		s.notifier.Create(ctx, a.CustomerID, "appointment_cancelled", map[string]any{
			"appointment_id": a.ID, "business_id": a.BusinessID, "starts_at": a.StartsAt,
		})
	}
	return nil
}

func (s *Booking) Mine(ctx context.Context, customerID string, from time.Time) ([]*domain.Appointment, error) {
	return s.repos.Bookings.ListForCustomer(ctx, customerID, from)
}

// ForBusiness is the owner's calendar; from/to bound the window.
func (s *Booking) ForBusiness(ctx context.Context, ownerID, businessID string, from, to time.Time) ([]*domain.Appointment, error) {
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, businessID)
	if err != nil {
		return nil, err
	}
	if !can {
		return nil, domain.ErrForbidden
	}
	return s.repos.Bookings.ListForBusiness(ctx, businessID, from, to)
}

func (s *Booking) Blackouts(ctx context.Context, ownerID, businessID string, from time.Time) ([]*domain.Blackout, error) {
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, businessID)
	if err != nil {
		return nil, err
	}
	if !can {
		return nil, domain.ErrForbidden
	}
	return s.repos.Bookings.ListBlackouts(ctx, businessID, from)
}

// AddBlackout blocks a window. The table's overlap constraint rejects a window
// that overlaps another, so that error is translated rather than surfaced as a
// 500.
func (s *Booking) AddBlackout(ctx context.Context, ownerID, businessID string, startsAt, endsAt time.Time, reason string) (*domain.Blackout, error) {
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, businessID)
	if err != nil {
		return nil, err
	}
	if !can {
		return nil, domain.ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 200 {
		return nil, domain.ErrValidation.WithField("reason", "Reason must be 200 characters or fewer.")
	}
	if !endsAt.After(startsAt) {
		return nil, domain.ErrValidation.WithField("ends_at", "The end must be after the start.")
	}
	if endsAt.Sub(startsAt) > 7*24*time.Hour {
		return nil, domain.ErrValidation.WithField("ends_at", "A blackout can be at most 7 days long.")
	}
	b := &domain.Blackout{
		ID: util.NewUUID(), BusinessID: businessID,
		StartsAt: startsAt, EndsAt: endsAt, Reason: reason,
	}
	if err := s.repos.Bookings.CreateBlackout(ctx, b); err != nil {
		if repo.IsSlotConflict(err) {
			return nil, domain.ErrValidation.WithField("starts_at", "That window overlaps another blackout.")
		}
		return nil, err
	}
	return b, nil
}

func (s *Booking) RemoveBlackout(ctx context.Context, ownerID, businessID, id string) error {
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, businessID)
	if err != nil {
		return err
	}
	if !can {
		return domain.ErrForbidden
	}
	ok, err := s.repos.Bookings.DeleteBlackout(ctx, businessID, id)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotFound
	}
	return nil
}

// SetStatus is the manager's side of the lifecycle.
//
// A booking is created 'confirmed', because a self-service slot that silently
// waits for approval is worse than one already in the calendar. What an owner
// actually needs is to close a booking that has happened, and to confirm one
// that was created 'pending' by a future manual-approval flow - so those are the
// transitions, and the UPDATE carries the allowed FROM states so two managers
// clicking at once cannot both apply one.
func (s *Booking) SetStatus(ctx context.Context, actorID, appointmentID string, status domain.AppointmentStatus) error {
	a, err := s.repos.Bookings.GetAppointment(ctx, appointmentID)
	if err != nil {
		return err
	}
	if a == nil {
		return domain.ErrNotFound
	}
	can, err := s.repos.Businesses.CanManageBusiness(ctx, actorID, a.BusinessID)
	if err != nil {
		return err
	}
	if !can {
		return domain.ErrForbidden
	}

	switch status {
	case domain.AppointmentConfirmed:
		ok, err := s.repos.Bookings.SetAppointmentState(ctx, appointmentID, status, domain.AppointmentPending)
		if err != nil {
			return err
		}
		if !ok {
			return domain.ErrValidation.WithField("status", "Only a pending appointment can be confirmed.")
		}
	case domain.AppointmentCompleted, domain.AppointmentNoShow:
		// A booking cannot have happened before it starts: closing a future
		// appointment would free its slot while the customer still expects it.
		if a.StartsAt.After(time.Now()) {
			return domain.ErrValidation.WithField("status", "That appointment has not started yet.")
		}
		ok, err := s.repos.Bookings.SetAppointmentState(ctx, appointmentID, status, domain.AppointmentConfirmed)
		if err != nil {
			return err
		}
		if !ok {
			return domain.ErrValidation.WithField("status", "Only a confirmed appointment can be closed.")
		}
	default:
		return domain.ErrValidation.WithField("status",
			"Status must be confirmed, completed or no_show. Cancelling has its own endpoint.")
	}
	return nil
}
