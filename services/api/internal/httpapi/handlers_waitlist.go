package httpapi

import (
	"net/http"
	"strings"

	"allstuff/api/internal/domain"
)

// Waitlist: the customer side of a full day. Joining is idempotent, leaving is
// scoped to the caller's own entry, and a cancellation notifies the first
// waiter from the booking service (see Booking.notifyWaitlist).

func (s *Server) handleWaitlistJoin(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		ProductID string `json:"product_id"`
		Day       string `json:"day"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	entry, err := s.deps.Waitlist.Join(r.Context(), user.ID, r.PathValue("id"),
		strings.TrimSpace(in.ProductID), strings.TrimSpace(in.Day))
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"waitlist": entry})
}

func (s *Server) handleMyWaitlist(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	list, err := s.deps.Waitlist.Mine(r.Context(), user.ID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"waitlist": list})
}

func (s *Server) handleBusinessWaitlist(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	days, err := s.deps.Waitlist.Demand(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"days": days})
}

func (s *Server) handleWaitlistLeave(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Waitlist.Leave(r.Context(), user.ID, r.PathValue("waitlistId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}
