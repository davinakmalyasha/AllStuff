package httpapi

import (
	"io"
	"net/http"
	"strings"
	"time"

	"bizverse/api/internal/domain"
)

// Bookmarks: bookings (PRD §5.6.4).
//
// Availability is PUBLIC - a visitor deciding whether to book has to see the
// times before signing up. Everything else is authenticated, and the owner's
// calendar re-checks management on every call.

// parseTimeParam accepts RFC3339, or returns def when the parameter is empty.
func parseTimeParam(v string, def time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return def, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, domain.ErrValidation.WithField("time", "Use an RFC3339 timestamp.")
	}
	return t, nil
}

// handleAvailability answers the bookable start times for one date.
//
// date is a CALENDAR DATE (YYYY-MM-DD) on purpose, not an instant: "what can I
// book on the 14th" is a date question, and letting a client send an instant
// invites the off-by-one-day that timezones produce.
func (s *Server) handleAvailability(w http.ResponseWriter, r *http.Request) {
	productID := strings.TrimSpace(r.URL.Query().Get("product_id"))
	if productID == "" {
		fail(w, domain.ErrValidation.WithField("product_id", "product_id is required."))
		return
	}
	dateStr := strings.TrimSpace(r.URL.Query().Get("date"))
	day, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		fail(w, domain.ErrValidation.WithField("date", "Use YYYY-MM-DD."))
		return
	}
	staffID := strings.TrimSpace(r.URL.Query().Get("staff_id"))
	locationID := strings.TrimSpace(r.URL.Query().Get("location_id"))
	var slots []domain.Slot
	var err2 error
	switch {
	case staffID != "" && locationID != "":
		slots, err2 = s.deps.Booking.SlotsForStaffAt(r.Context(), r.PathValue("id"), productID, staffID, locationID, day)
	case staffID != "":
		slots, err2 = s.deps.Booking.SlotsForStaff(r.Context(), r.PathValue("id"), productID, staffID, day)
	case locationID != "":
		slots, err2 = s.deps.Booking.SlotsAt(r.Context(), r.PathValue("id"), productID, locationID, day)
	default:
		slots, err2 = s.deps.Booking.Slots(r.Context(), r.PathValue("id"), productID, day)
	}
	if err2 != nil {
		fail(w, err2)
		return
	}
	ok(w, map[string]any{"date": dateStr, "slots": slots})
}

func (s *Server) handleAppointmentCreate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		ProductID  string `json:"product_id"`
		StaffID    string `json:"staff_id"`
		LocationID string `json:"location_id"`
		StartsAt   string `json:"starts_at"`
		Notes      string `json:"notes"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(in.StartsAt))
	if err != nil {
		fail(w, domain.ErrValidation.WithField("starts_at", "Use an RFC3339 timestamp."))
		return
	}
	a, err := s.deps.Booking.BookAt(r.Context(), user.ID, r.PathValue("id"), in.ProductID, in.StaffID, in.LocationID, at, in.Notes)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"appointment": a})
}

// handleMyAppointments is the customer's list, newest window first.
func (s *Server) handleMyAppointments(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	from, err := parseTimeParam(r.URL.Query().Get("from"), time.Now().Add(-24*time.Hour))
	if err != nil {
		fail(w, err)
		return
	}
	list, err := s.deps.Booking.Mine(r.Context(), user.ID, from)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"appointments": list})
}

// handleBusinessAppointments is the owner's calendar. The window defaults to a
// month back and a month forward - enough for the dashboard, and bounded so the
// response cannot grow without limit.
func (s *Server) handleBusinessAppointments(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	now := time.Now()
	from, err := parseTimeParam(r.URL.Query().Get("from"), now.AddDate(0, -1, 0))
	if err != nil {
		fail(w, err)
		return
	}
	to, err := parseTimeParam(r.URL.Query().Get("to"), now.AddDate(0, 1, 0))
	if err != nil {
		fail(w, err)
		return
	}
	if !to.After(from) {
		fail(w, domain.ErrValidation.WithField("to", "The end must be after the start."))
		return
	}
	list, err := s.deps.Booking.ForBusiness(r.Context(), user.ID, r.PathValue("id"), from, to)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"appointments": list})
}

func (s *Server) handleAppointmentCancel(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Booking.Cancel(r.Context(), user.ID, r.PathValue("appointmentId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleBlackouts(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		from, err := parseTimeParam(r.URL.Query().Get("from"), time.Now())
		if err != nil {
			fail(w, err)
			return
		}
		list, err := s.deps.Booking.Blackouts(r.Context(), user.ID, businessID, from)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"blackouts": list})
	case http.MethodPost:
		var in struct {
			StartsAt string `json:"starts_at"`
			EndsAt   string `json:"ends_at"`
			Reason   string `json:"reason"`
		}
		if err := decodeBody(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		start, err := time.Parse(time.RFC3339, strings.TrimSpace(in.StartsAt))
		if err != nil {
			fail(w, domain.ErrValidation.WithField("starts_at", "Use an RFC3339 timestamp."))
			return
		}
		end, err := time.Parse(time.RFC3339, strings.TrimSpace(in.EndsAt))
		if err != nil {
			fail(w, domain.ErrValidation.WithField("ends_at", "Use an RFC3339 timestamp."))
			return
		}
		b, err := s.deps.Booking.AddBlackout(r.Context(), user.ID, businessID, start, end, in.Reason)
		if err != nil {
			fail(w, err)
			return
		}
		created(w, map[string]any{"blackout": b})
	default:
		fail(w, domain.ErrValidation.WithField("_", "Method not allowed."))
	}
}

func (s *Server) handleBlackoutDelete(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Booking.RemoveBlackout(r.Context(), user.ID, r.PathValue("id"), r.PathValue("blackoutId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// handleAppointmentStatus is the manager's transition endpoint. It takes no
// business id: the appointment names its own business, and the service checks
// management against that.
func (s *Server) handleAppointmentStatus(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Booking.SetStatus(r.Context(), user.ID, r.PathValue("appointmentId"), domain.AppointmentStatus(in.Status)); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// ---- staff (manager only) ----

func (s *Server) handleStaff(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		list, err := s.deps.Booking.Staff(r.Context(), user.ID, businessID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"staff": list})
	case http.MethodPost:
		var in struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Phone string `json:"phone"`
		}
		if err := decodeBody(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		st, err := s.deps.Booking.CreateStaff(r.Context(), user.ID, businessID, in.Name, in.Email, in.Phone)
		if err != nil {
			fail(w, err)
			return
		}
		created(w, map[string]any{"staff": st})
	default:
		fail(w, domain.ErrValidation.WithField("_", "Method not allowed."))
	}
}

func (s *Server) handleStaffUpdate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.PathValue("id")
	staffID := r.PathValue("staffId")
	if r.Method == http.MethodDelete {
		if err := s.deps.Booking.DeleteStaff(r.Context(), user.ID, businessID, staffID); err != nil {
			fail(w, err)
			return
		}
		noContent(w)
		return
	}
	var in struct {
		Name   *string `json:"name"`
		Email  *string `json:"email"`
		Phone  *string `json:"phone"`
		Active *bool   `json:"active"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	st, err := s.deps.Booking.UpdateStaff(r.Context(), user.ID, businessID, staffID, in.Name, in.Email, in.Phone, in.Active)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"staff": st})
}

func (s *Server) handleStaffServices(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		ProductIDs []string `json:"product_ids"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	st, err := s.deps.Booking.SetStaffServices(r.Context(), user.ID, r.PathValue("id"), r.PathValue("staffId"), in.ProductIDs)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"staff": st})
}

func (s *Server) handleStaffHours(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Hours map[string]domain.DayHours `json:"hours"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	st, err := s.deps.Booking.SetStaffHours(r.Context(), user.ID, r.PathValue("id"), r.PathValue("staffId"), in.Hours)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"staff": st})
}

// handleAppointmentICS serves the booking as an iCalendar file, so a customer
// can put it in their own calendar without the platform having to write to it.
func (s *Server) handleAppointmentICS(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	ics, err := s.deps.Booking.CalendarInvite(r.Context(), user.ID, r.PathValue("appointmentId"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="booking.ics"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ics)
}
