package service

import (
	"context"
	"sort"
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

func loadTimezone(tz string) *time.Location {
	loc, err := time.LoadLocation(strings.TrimSpace(tz))
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
func openingFor(hours, specialHours map[string]any, day time.Time) (time.Time, time.Time, bool) {
	if raw, ok := specialHours[day.Format("2006-01-02")]; ok {
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
	raw, ok := hours[bookableDayKeys[day.Weekday()]]
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

func shiftsCover(st *domain.Staff, dayStart, start, end time.Time) bool {
	h, ok := st.Hours[bookableDayKeys[dayStart.Weekday()]]
	if !ok {
		// No row for this weekday: inherit the business hours, which already
		// bound the slot under test.
		return true
	}
	if !h.Working {
		return false
	}
	opens, ok1 := clockOn(dayStart, h.OpensAt)
	closes, ok2 := clockOn(dayStart, h.ClosesAt)
	if !ok1 || !ok2 {
		return false
	}
	return !start.Before(opens) && !end.After(closes)
}

func overlapsAny(ranges [][2]time.Time, start, end time.Time) bool {
	for _, r := range ranges {
		if start.Before(r[1]) && end.After(r[0]) {
			return true
		}
	}
	return false
}

// qualifiedStaff is every active staff member who performs this service. An
// empty result means nobody is configured for it, and bookings fall back to the
// unstaffed rule: one at a time for the whole business.
func (s *Booking) qualifiedStaff(ctx context.Context, businessID, productID string) ([]*domain.Staff, error) {
	all, err := s.repos.Staff.ListByBusiness(ctx, businessID, true)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Staff, 0, len(all))
	for _, st := range all {
		for _, pid := range st.Services {
			if pid == productID {
				out = append(out, st)
				break
			}
		}
	}
	return out, nil
}

// computeSlots also returns which staff are free at each start instant, so Book
// does not have to repeat the queries this scan already ran.
func (s *Booking) computeSlots(ctx context.Context, businessID, productID, staffFilter, locationID string, day time.Time) ([]domain.Slot, map[int64][]string, error) {
	b, p, err := s.bookableService(ctx, businessID, productID)
	if err != nil {
		return nil, nil, err
	}
	// Where the calendar comes from: the chosen location, else the primary, else
	// the business's own cached columns. The last case is not a fallback for a
	// broken database - it is what a listing that has not been split into
	// locations yet looks like.
	tz := b.Timezone
	hours, special := b.Hours, b.SpecialHours
	place, err := s.locationFor(ctx, businessID, locationID)
	if err != nil {
		return nil, nil, err
	}
	if place != nil {
		tz, hours, special = place.Timezone, place.Hours, place.SpecialHours
	}
	loc := loadTimezone(tz)
	// The CALENDAR DATE is taken from the caller's year/month/day fields and
	// rebuilt in the business timezone. Converting the instant instead (day.In(loc))
	// moves a UTC-midnight request to the previous day for any negative offset,
	// which would answer "the day before" to a client that asked for a date.
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	open, shut, ok := openingFor(hours, special, dayStart)
	if !ok {
		return []domain.Slot{}, map[int64][]string{}, nil
	}
	// A per-service window NARROWS the place's hours; it never widens them, so a
	// service cannot be booked while the shop is shut.
	if place != nil {
		override, err := s.repos.Locations.ServiceHours(ctx, place.ID, productID)
		if err != nil {
			return nil, nil, err
		}
		if day, ok := override[bookableDayKeys[dayStart.Weekday()]]; ok {
			if !day.Working {
				return []domain.Slot{}, map[int64][]string{}, nil
			}
			so, ok1 := clockOn(dayStart, day.OpensAt)
			sc, ok2 := clockOn(dayStart, day.ClosesAt)
			if !ok1 || !ok2 || !sc.After(so) {
				return []domain.Slot{}, map[int64][]string{}, nil
			}
			if so.After(open) {
				open = so
			}
			if sc.Before(shut) {
				shut = sc
			}
			if !shut.After(open) {
				return []domain.Slot{}, map[int64][]string{}, nil
			}
		}
	}
	dur := time.Duration(*p.DurationMinutes) * time.Minute
	dayEnd := dayStart.AddDate(0, 0, 1)

	staff, err := s.qualifiedStaff(ctx, businessID, productID)
	if err != nil {
		return nil, nil, err
	}
	if staffFilter != "" {
		var only []*domain.Staff
		for _, st := range staff {
			if st.ID == staffFilter {
				only = append(only, st)
			}
		}
		if len(only) == 0 {
			return nil, nil, domain.ErrNotFound
		}
		staff = only
	}

	var unstaffedBusy [][2]time.Time
	var blackouts [][2]time.Time
	busyByStaff := map[string][][2]time.Time{}
	if len(staff) > 0 {
		if blackouts, err = s.repos.Bookings.BlackoutRanges(ctx, businessID, dayStart, dayEnd); err != nil {
			return nil, nil, err
		}
		if busyByStaff, err = s.repos.Staff.BusyByStaff(ctx, businessID, dayStart, dayEnd); err != nil {
			return nil, nil, err
		}
	} else {
		if unstaffedBusy, err = s.repos.Bookings.BusyRanges(ctx, businessID, open.Add(-dur), dayEnd); err != nil {
			return nil, nil, err
		}
	}

	now := time.Now()
	out := make([]domain.Slot, 0, 16)
	free := map[int64][]string{}
	for start := open; !start.Add(dur).After(shut); start = start.Add(dur) {
		if len(out) >= maxSlotsPerDay {
			break
		}
		if !start.After(now) {
			continue
		}
		end := start.Add(dur)

		if len(staff) == 0 {
			if overlapsAny(unstaffedBusy, start, end) {
				continue
			}
			out = append(out, domain.Slot{StartsAt: start, EndsAt: end})
			continue
		}

		if overlapsAny(blackouts, start, end) {
			continue
		}
		ids := make([]string, 0, len(staff))
		for _, st := range staff {
			if !shiftsCover(st, dayStart, start, end) {
				continue
			}
			if overlapsAny(busyByStaff[st.ID], start, end) {
				continue
			}
			ids = append(ids, st.ID)
		}
		if len(ids) == 0 {
			continue
		}
		sort.Strings(ids)
		free[start.Unix()] = ids
		out = append(out, domain.Slot{StartsAt: start, EndsAt: end, StaffAvailable: len(ids)})
	}
	return out, free, nil
}

// Slots returns the bookable start times for one calendar day, in the
// business's timezone. day is any instant inside the wanted date.
func (s *Booking) Slots(ctx context.Context, businessID, productID string, day time.Time) ([]domain.Slot, error) {
	slots, _, err := s.computeSlots(ctx, businessID, productID, "", "", day)
	return slots, err
}

// SlotsAt is Slots for one named place; an empty id means the primary.
func (s *Booking) SlotsAt(ctx context.Context, businessID, productID, locationID string, day time.Time) ([]domain.Slot, error) {
	slots, _, err := s.computeSlots(ctx, businessID, productID, "", locationID, day)
	return slots, err
}

// SlotsForStaff is the same list restricted to one person's calendar.
func (s *Booking) SlotsForStaff(ctx context.Context, businessID, productID, staffID string, day time.Time) ([]domain.Slot, error) {
	slots, _, err := s.computeSlots(ctx, businessID, productID, staffID, "", day)
	return slots, err
}

// Book reserves one slot without naming a staff member; the business's own
// calendar picks whoever is free.
func (s *Booking) Book(ctx context.Context, customerID, businessID, productID string, at time.Time, notes string) (*domain.Appointment, error) {
	return s.BookWithStaff(ctx, customerID, businessID, productID, "", at, notes)
}

// BookWithStaff reserves one slot, optionally for a named staff member.
//
// The chosen time must be an EXACT generated slot: that rejects off-grid times,
// past times and closed days in one check, and it stops a client inventing a
// duration by supplying a start outside the grid. The insert is still not atomic
// with the slot scan - two requests can both see a slot free - so the database's
// EXCLUDE constraints are the real guarantee, and a conflict there is reported as
// ErrSlotTaken rather than as an internal error.
func (s *Booking) BookWithStaff(ctx context.Context, customerID, businessID, productID, staffID string, at time.Time, notes string) (*domain.Appointment, error) {
	return s.BookAt(ctx, customerID, businessID, productID, staffID, "", at, notes)
}

// BookAt is BookWithStaff at a named place.
func (s *Booking) BookAt(ctx context.Context, customerID, businessID, productID, staffID, locationID string, at time.Time, notes string) (*domain.Appointment, error) {
	notes = strings.TrimSpace(notes)
	if len([]rune(notes)) > 500 {
		return nil, domain.ErrValidation.WithField("notes", "Notes must be 500 characters or fewer.")
	}
	b, p, err := s.bookableService(ctx, businessID, productID)
	if err != nil {
		return nil, err
	}
	// computeSlots reads the CALENDAR DATE from the time's own fields, so the
	// instant has to be expressed in the calendar's timezone first.
	tz := b.Timezone
	if place, err := s.locationFor(ctx, businessID, locationID); err != nil {
		return nil, err
	} else if place != nil {
		tz = place.Timezone
	}
	slots, free, err := s.computeSlots(ctx, businessID, productID, staffID, locationID, at.In(loadTimezone(tz)))
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

	// Whoever is free, chosen by id so the result is deterministic rather than
	// map-order. Load balancing across staff is a later concern.
	if staffID == "" {
		ids := free[chosen.StartsAt.Unix()]
		if len(ids) > 0 {
			staffID = ids[0]
		}
	}

	a := &domain.Appointment{
		ID:         util.NewUUID(),
		BusinessID: businessID,
		ProductID:  productID,
		CustomerID: customerID,
		StaffID:    staffID,
		LocationID: locationID,
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

// Reschedule moves a live booking to another generated slot.
//
// The new time must be an EXACT slot for the same service, exactly like BookAt:
// off-grid times, closed days and past times are rejected by the same derived
// availability the customer saw. The update is not atomic with that scan, so the
// EXCLUDE constraints remain the real guarantee and a lost race is ErrSlotTaken.
//
// Reminder claims for the old time are released, otherwise the 24h/2h sweep
// would treat the moved booking as already reminded and the customer would get
// nothing for the new time.
func (s *Booking) Reschedule(ctx context.Context, userID, appointmentID string, at time.Time) (*domain.Appointment, error) {
	a, err := s.repos.Bookings.GetAppointment(ctx, appointmentID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, domain.ErrNotFound
	}
	if a.CustomerID != userID {
		can, err := s.repos.Businesses.CanManageBusiness(ctx, userID, a.BusinessID)
		if err != nil {
			return nil, err
		}
		if !can {
			return nil, domain.ErrForbidden
		}
	}
	if !a.Status.OccupiesSlot() {
		return nil, domain.ErrValidation.WithField("_", "That appointment is no longer active.")
	}
	if !a.StartsAt.After(time.Now()) {
		return nil, domain.ErrValidation.WithField("_", "That appointment has already started.")
	}

	b, _, err := s.bookableService(ctx, a.BusinessID, a.ProductID)
	if err != nil {
		return nil, err
	}
	tz := b.Timezone
	if place, err := s.locationFor(ctx, a.BusinessID, a.LocationID); err != nil {
		return nil, err
	} else if place != nil {
		tz = place.Timezone
	}
	slots, free, err := s.computeSlots(ctx, a.BusinessID, a.ProductID, a.StaffID, a.LocationID, at.In(loadTimezone(tz)))
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

	staffID := a.StaffID
	if staffID == "" {
		if ids := free[chosen.StartsAt.Unix()]; len(ids) > 0 {
			staffID = ids[0]
		}
	}
	ok, err := s.repos.Bookings.RescheduleAppointment(ctx, a.ID, chosen.StartsAt, chosen.EndsAt, staffID)
	if err != nil {
		if repo.IsSlotConflict(err) {
			return nil, domain.ErrSlotTaken
		}
		return nil, err
	}
	if !ok {
		return nil, domain.ErrValidation.WithField("_", "That appointment is no longer active.")
	}
	s.repos.Bookings.ReleaseAppointmentEvent(ctx, a.ID, "reminder_24h")
	s.repos.Bookings.ReleaseAppointmentEvent(ctx, a.ID, "reminder_2h")

	payload := map[string]any{
		"appointment_id": a.ID, "business_id": a.BusinessID, "business_slug": a.BusinessSlug,
		"product": a.ProductName, "starts_at": chosen.StartsAt, "previous_starts_at": a.StartsAt,
	}
	// Tell the other side, same rule as Cancel: a customer moving a booking
	// notifies the owner, a manager moving it notifies the customer.
	if a.CustomerID == userID {
		if b.OwnerID != "" && b.OwnerID != userID { // lint:allow: not authorisation - recipient null-check, not a gate
			s.notifier.Create(ctx, b.OwnerID, "appointment_rescheduled", payload)
		}
	} else {
		s.notifier.Create(ctx, a.CustomerID, "appointment_rescheduled", payload)
	}
	return s.repos.Bookings.GetAppointment(ctx, a.ID)
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

// validateDayHours checks a weekday map: known keys, and every working day a real
// interval. label names the thing in the error ("Shift", "Service hours").
func validateDayHours(hours map[string]domain.DayHours, label string) error {
	valid := map[string]bool{"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true}
	for day, h := range hours {
		if !valid[day] {
			return domain.ErrValidation.WithField("hours", "Unknown weekday "+day+".")
		}
		if !h.Working {
			continue
		}
		opens, err1 := time.Parse("15:04", strings.TrimSpace(h.OpensAt))
		closes, err2 := time.Parse("15:04", strings.TrimSpace(h.ClosesAt))
		if err1 != nil || err2 != nil {
			return domain.ErrValidation.WithField("hours", label+" times must be HH:MM.")
		}
		if !closes.After(opens) {
			return domain.ErrValidation.WithField("hours", "A "+strings.ToLower(label)+" must end after it starts.")
		}
	}
	return nil
}

// ---- staff ----

func (s *Booking) mustManage(ctx context.Context, ownerID, businessID string) error {
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, businessID)
	if err != nil {
		return err
	}
	if !can {
		return domain.ErrForbidden
	}
	return nil
}

func (s *Booking) staffInBusiness(ctx context.Context, businessID, staffID string) (*domain.Staff, error) {
	st, err := s.repos.Staff.Get(ctx, staffID)
	if err != nil {
		return nil, err
	}
	if st == nil || st.BusinessID != businessID {
		return nil, domain.ErrNotFound
	}
	return st, nil
}

func validStaffName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n < 1 || n > 80 {
		return "", domain.ErrValidation.WithField("name", "Name must be 1-80 characters.")
	}
	return name, nil
}

func (s *Booking) Staff(ctx context.Context, ownerID, businessID string) ([]*domain.Staff, error) {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	return s.repos.Staff.ListByBusiness(ctx, businessID, false)
}

func (s *Booking) CreateStaff(ctx context.Context, ownerID, businessID, name, email, phone string) (*domain.Staff, error) {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	name, err := validStaffName(name)
	if err != nil {
		return nil, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email != "" && !emailRe.MatchString(email) {
		return nil, domain.ErrValidation.WithField("email", "Enter a valid email address.")
	}
	phone = strings.TrimSpace(phone)
	if len([]rune(phone)) > 40 {
		return nil, domain.ErrValidation.WithField("phone", "Phone must be 40 characters or fewer.")
	}
	st := &domain.Staff{
		ID: util.NewUUID(), BusinessID: businessID,
		Name: name, Email: email, Phone: phone, Active: true,
	}
	if err := s.repos.Staff.Create(ctx, st); err != nil {
		return nil, err
	}
	return s.repos.Staff.Get(ctx, st.ID)
}

func (s *Booking) UpdateStaff(ctx context.Context, ownerID, businessID, staffID string, name, email, phone *string, active *bool) (*domain.Staff, error) {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	if _, err := s.staffInBusiness(ctx, businessID, staffID); err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if name != nil {
		n, err := validStaffName(*name)
		if err != nil {
			return nil, err
		}
		fields["name"] = n
	}
	if email != nil {
		e := strings.ToLower(strings.TrimSpace(*email))
		if e != "" && !emailRe.MatchString(e) {
			return nil, domain.ErrValidation.WithField("email", "Enter a valid email address.")
		}
		fields["email"] = nilIfEmpty(e)
	}
	if phone != nil {
		p := strings.TrimSpace(*phone)
		if len([]rune(p)) > 40 {
			return nil, domain.ErrValidation.WithField("phone", "Phone must be 40 characters or fewer.")
		}
		fields["phone"] = nilIfEmpty(p)
	}
	if active != nil {
		fields["active"] = *active
	}
	if len(fields) == 0 {
		return nil, domain.ErrValidation.WithField("_", "Nothing to update.")
	}
	if err := s.repos.Staff.Update(ctx, staffID, fields); err != nil {
		return nil, err
	}
	return s.repos.Staff.Get(ctx, staffID)
}

func nilIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Booking) DeleteStaff(ctx context.Context, ownerID, businessID, staffID string) error {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return err
	}
	ok, err := s.repos.Staff.Delete(ctx, businessID, staffID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotFound
	}
	return nil
}

// SetStaffServices replaces the qualifications. Every product must belong to this
// business: a staff row qualified for someone else's service would leak into that
// business's availability computation.
func (s *Booking) SetStaffServices(ctx context.Context, ownerID, businessID, staffID string, productIDs []string) (*domain.Staff, error) {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	if _, err := s.staffInBusiness(ctx, businessID, staffID); err != nil {
		return nil, err
	}
	products, err := s.repos.Products.ListByBusiness(ctx, businessID)
	if err != nil {
		return nil, err
	}
	own := make(map[string]bool, len(products))
	for _, p := range products {
		own[p.ID] = true
	}
	for _, pid := range productIDs {
		if !own[pid] {
			return nil, domain.ErrValidation.WithField("product_ids", "That service does not belong to this business.")
		}
	}
	if err := s.repos.Staff.ReplaceServices(ctx, staffID, productIDs); err != nil {
		return nil, err
	}
	return s.repos.Staff.Get(ctx, staffID)
}

// SetStaffHours replaces the weekly shift. A weekday left out inherits the
// business hours; a weekday marked not working is a day off.
func (s *Booking) SetStaffHours(ctx context.Context, ownerID, businessID, staffID string, hours map[string]domain.DayHours) (*domain.Staff, error) {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	if _, err := s.staffInBusiness(ctx, businessID, staffID); err != nil {
		return nil, err
	}
	if err := validateDayHours(hours, "Shift"); err != nil {
		return nil, err
	}
	if err := s.repos.Staff.ReplaceHours(ctx, staffID, hours); err != nil {
		return nil, err
	}
	return s.repos.Staff.Get(ctx, staffID)
}

// ---- locations ----

// locationFor resolves a place: the named one, else the primary, else nil when
// the business has no location rows yet (which is how a listing looks before it
// is split into places).
func (s *Booking) locationFor(ctx context.Context, businessID, locationID string) (*domain.Location, error) {
	if locationID != "" {
		l, err := s.repos.Locations.Get(ctx, locationID)
		if err != nil {
			return nil, err
		}
		if l == nil || l.BusinessID != businessID {
			return nil, domain.ErrNotFound
		}
		return l, nil
	}
	return s.repos.Locations.Primary(ctx, businessID)
}

// mirrorPrimary copies the primary location back onto the business's cached
// columns. Every existing reader - search, the directory, the storefront, SSR -
// uses those columns, so a location edit that skipped this would leave the map,
// the address on the public page and the hours on the SEO shell disagreeing with
// the location the owner just edited.
func (s *Booking) mirrorPrimary(ctx context.Context, businessID string) error {
	loc, err := s.repos.Locations.Primary(ctx, businessID)
	if err != nil || loc == nil {
		return err
	}
	count, err := s.repos.Locations.Count(ctx, businessID)
	if err != nil {
		return err
	}
	_, err = s.repos.Exec(ctx, `
		UPDATE businesses SET
			address = $2, city = $3, country = $4, lat = $5, lng = $6,
			timezone = $7, hours = $8, special_hours = $9,
			primary_location_id = $10, location_count = $11, updated_at = now()
		 WHERE id = $1`,
		businessID, loc.Address, loc.City, loc.Country, loc.Lat, loc.Lng,
		loc.Timezone, loc.Hours, loc.SpecialHours, loc.ID, count)
	return err
}

func (s *Booking) validateLocation(in domain.Location) error {
	if n := len([]rune(strings.TrimSpace(in.Name))); n < 1 || n > 80 {
		return domain.ErrValidation.WithField("name", "Name must be 1-80 characters.")
	}
	for _, f := range []struct {
		key string
		val string
	}{{"address", in.Address}, {"city", in.City}, {"country", in.Country}} {
		if strings.TrimSpace(f.val) == "" {
			return domain.ErrValidation.WithField(f.key, "This field is required.")
		}
	}
	if in.Lat < -90 || in.Lat > 90 || in.Lng < -180 || in.Lng > 180 {
		return domain.ErrValidation.WithField("lat", "Coordinates look wrong.")
	}
	if _, err := time.LoadLocation(strings.TrimSpace(in.Timezone)); err != nil {
		return domain.ErrValidation.WithField("timezone", "Unknown timezone.")
	}
	if len([]rune(in.Phone)) > 40 {
		return domain.ErrValidation.WithField("phone", "Phone must be 40 characters or fewer.")
	}
	return nil
}

// Locations lists a business's places.
//
// A manager sees them regardless of the listing's status - an owner configuring
// a draft has to be able to see what they just added - while everybody else is
// held to the public rule. Before this, the only route was the public one, so an
// owner of a business still in review got a 404 for their own locations.
func (s *Booking) Locations(ctx context.Context, businessID, viewerID string) ([]*domain.Location, error) {
	if viewerID != "" {
		can, err := s.repos.Businesses.CanManageBusiness(ctx, viewerID, businessID)
		if err != nil {
			return nil, err
		}
		if can {
			return s.repos.Locations.List(ctx, businessID)
		}
	}
	b, err := s.repos.Businesses.GetByID(ctx, businessID)
	if err != nil {
		return nil, err
	}
	if b == nil || (b.Status != domain.BusinessVerified && b.Status != domain.BusinessPaused) {
		return nil, domain.ErrNotFound
	}
	return s.repos.Locations.List(ctx, businessID)
}

func (s *Booking) ManageLocations(ctx context.Context, ownerID, businessID string) ([]*domain.Location, error) {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	return s.repos.Locations.List(ctx, businessID)
}

// CreateLocation adds a place. The first one becomes primary automatically, and
// inherits the business's current hours if none were supplied, because a second
// location with no hours would be bookable for nothing.
func (s *Booking) CreateLocation(ctx context.Context, ownerID, businessID string, in domain.Location) (*domain.Location, error) {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	if err := s.validateLocation(in); err != nil {
		return nil, err
	}
	count, err := s.repos.Locations.Count(ctx, businessID)
	if err != nil {
		return nil, err
	}
	in.ID = util.NewUUID()
	in.BusinessID = businessID
	in.IsPrimary = count == 0
	// Both columns are NOT NULL: an absent map must be an empty one, not NULL.
	if in.Hours == nil {
		in.Hours = map[string]any{}
	}
	if in.SpecialHours == nil {
		in.SpecialHours = map[string]any{}
	}
	if len(in.Hours) == 0 {
		if b, _ := s.repos.Businesses.GetByID(ctx, businessID); b != nil {
			in.Hours = b.Hours
			if len(in.SpecialHours) == 0 {
				in.SpecialHours = b.SpecialHours
			}
		}
	}
	if err := s.repos.Locations.Create(ctx, &in); err != nil {
		return nil, err
	}
	if in.IsPrimary {
		if err := s.mirrorPrimary(ctx, businessID); err != nil {
			return nil, err
		}
	}
	return s.repos.Locations.Get(ctx, in.ID)
}

func (s *Booking) UpdateLocation(ctx context.Context, ownerID, businessID, locationID string, fields map[string]any) (*domain.Location, error) {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	cur, err := s.locationFor(ctx, businessID, locationID)
	if err != nil {
		return nil, err
	}
	if cur == nil || cur.ID != locationID {
		return nil, domain.ErrNotFound
	}
	if len(fields) == 0 {
		return nil, domain.ErrValidation.WithField("_", "Nothing to update.")
	}
	// Validate the MERGED result: a patch that only moves the coordinates must
	// still be judged against the address already stored.
	merged := *cur
	if v, ok := fields["name"].(string); ok {
		merged.Name = v
	}
	if v, ok := fields["address"].(string); ok {
		merged.Address = v
	}
	if v, ok := fields["city"].(string); ok {
		merged.City = v
	}
	if v, ok := fields["country"].(string); ok {
		merged.Country = v
	}
	if v, ok := fields["timezone"].(string); ok {
		merged.Timezone = v
	}
	if v, ok := fields["phone"].(string); ok {
		merged.Phone = v
	}
	if err := s.validateLocation(merged); err != nil {
		return nil, err
	}
	if v, ok := fields["phone"]; ok {
		fields["phone"] = nilIfEmpty(strings.TrimSpace(v.(string)))
	}
	if err := s.repos.Locations.Update(ctx, locationID, fields); err != nil {
		return nil, err
	}
	if cur.IsPrimary {
		if err := s.mirrorPrimary(ctx, businessID); err != nil {
			return nil, err
		}
	}
	return s.repos.Locations.Get(ctx, locationID)
}

// DeleteLocation refuses to remove the last place - a business must have one -
// and promotes another when the primary goes.
func (s *Booking) DeleteLocation(ctx context.Context, ownerID, businessID, locationID string) error {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return err
	}
	count, err := s.repos.Locations.Count(ctx, businessID)
	if err != nil {
		return err
	}
	if count <= 1 {
		return domain.ErrValidation.WithField("_", "A business must keep at least one location.")
	}
	cur, err := s.locationFor(ctx, businessID, locationID)
	if err != nil {
		return err
	}
	if cur == nil || cur.ID != locationID {
		return domain.ErrNotFound
	}
	ok, err := s.repos.Locations.Delete(ctx, businessID, locationID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotFound
	}
	if cur.IsPrimary {
		if remaining, err := s.repos.Locations.List(ctx, businessID); err == nil && len(remaining) > 0 {
			if err := s.repos.Locations.SetPrimary(ctx, businessID, remaining[0].ID); err != nil {
				return err
			}
		}
		if err := s.mirrorPrimary(ctx, businessID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Booking) SetPrimaryLocation(ctx context.Context, ownerID, businessID, locationID string) error {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return err
	}
	if _, err := s.locationFor(ctx, businessID, locationID); err != nil {
		return err
	}
	if err := s.repos.Locations.SetPrimary(ctx, businessID, locationID); err != nil {
		return err
	}
	return s.mirrorPrimary(ctx, businessID)
}

// SlotsForStaffAt narrows to one person at one place.
func (s *Booking) SlotsForStaffAt(ctx context.Context, businessID, productID, staffID, locationID string, day time.Time) ([]domain.Slot, error) {
	slots, _, err := s.computeSlots(ctx, businessID, productID, staffID, locationID, day)
	return slots, err
}

// ---- per-service hours ----

// ServiceHours is the override for one service at one place. An empty map means
// the service follows the place's hours.
func (s *Booking) ServiceHours(ctx context.Context, ownerID, businessID, locationID, productID string) (map[string]domain.DayHours, error) {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	if err := s.serviceHoursTarget(ctx, businessID, locationID, productID); err != nil {
		return nil, err
	}
	return s.repos.Locations.ServiceHours(ctx, locationID, productID)
}

func (s *Booking) SetServiceHours(ctx context.Context, ownerID, businessID, locationID, productID string, hours map[string]domain.DayHours) error {
	if err := s.mustManage(ctx, ownerID, businessID); err != nil {
		return err
	}
	if err := s.serviceHoursTarget(ctx, businessID, locationID, productID); err != nil {
		return err
	}
	if err := validateDayHours(hours, "Service hours"); err != nil {
		return err
	}
	return s.repos.Locations.ReplaceServiceHours(ctx, locationID, productID, hours)
}

// serviceHoursTarget checks the place belongs to this business and the service
// does too. Both matter: a location id from another business would write hours
// onto someone else's calendar.
func (s *Booking) serviceHoursTarget(ctx context.Context, businessID, locationID, productID string) error {
	loc, err := s.locationFor(ctx, businessID, locationID)
	if err != nil {
		return err
	}
	if loc == nil || loc.ID != locationID {
		return domain.ErrNotFound
	}
	p, err := s.repos.Products.GetByID(ctx, productID)
	if err != nil {
		return err
	}
	if p == nil || p.BusinessID != businessID {
		return domain.ErrNotFound
	}
	return nil
}
