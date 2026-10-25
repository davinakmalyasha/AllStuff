package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
)

// Locations are the places a business operates from. The list is PUBLIC - a
// customer picking which branch to visit needs it - and every mutation is
// manager-only.

func (s *Server) handleLocations(w http.ResponseWriter, r *http.Request) {
	businessID := r.PathValue("id")
	if r.Method == http.MethodGet {
		list, err := s.deps.Booking.Locations(r.Context(), businessID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"locations": list})
		return
	}

	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Name         string         `json:"name"`
		Address      string         `json:"address"`
		City         string         `json:"city"`
		Country      string         `json:"country"`
		Lat          float64        `json:"lat"`
		Lng          float64        `json:"lng"`
		Timezone     string         `json:"timezone"`
		Hours        map[string]any `json:"hours"`
		SpecialHours map[string]any `json:"special_hours"`
		Phone        string         `json:"phone"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	loc, err := s.deps.Booking.CreateLocation(r.Context(), user.ID, businessID, domain.Location{
		Name: in.Name, Address: in.Address, City: in.City, Country: in.Country,
		Lat: in.Lat, Lng: in.Lng, Timezone: in.Timezone,
		Hours: in.Hours, SpecialHours: in.SpecialHours, Phone: in.Phone,
	})
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"location": loc})
}

func (s *Server) handleLocationUpdate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.PathValue("id")
	locationID := r.PathValue("locationId")

	if r.Method == http.MethodDelete {
		if err := s.deps.Booking.DeleteLocation(r.Context(), user.ID, businessID, locationID); err != nil {
			fail(w, err)
			return
		}
		noContent(w)
		return
	}

	var in struct {
		Name         *string        `json:"name"`
		Address      *string        `json:"address"`
		City         *string        `json:"city"`
		Country      *string        `json:"country"`
		Lat          *float64       `json:"lat"`
		Lng          *float64       `json:"lng"`
		Timezone     *string        `json:"timezone"`
		Hours        map[string]any `json:"hours"`
		SpecialHours map[string]any `json:"special_hours"`
		Phone        *string        `json:"phone"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	fields := map[string]any{}
	for k, v := range map[string]*string{
		"name": in.Name, "address": in.Address, "city": in.City,
		"country": in.Country, "timezone": in.Timezone, "phone": in.Phone,
	} {
		if v != nil {
			fields[k] = *v
		}
	}
	if in.Lat != nil {
		fields["lat"] = *in.Lat
	}
	if in.Lng != nil {
		fields["lng"] = *in.Lng
	}
	if in.Hours != nil {
		fields["hours"] = in.Hours
	}
	if in.SpecialHours != nil {
		fields["special_hours"] = in.SpecialHours
	}
	loc, err := s.deps.Booking.UpdateLocation(r.Context(), user.ID, businessID, locationID, fields)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"location": loc})
}

func (s *Server) handleLocationPrimary(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Booking.SetPrimaryLocation(r.Context(), user.ID, r.PathValue("id"), r.PathValue("locationId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// Service hours: the window one service runs inside a location's own hours.
func (s *Server) handleServiceHours(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.PathValue("id")
	locationID := r.PathValue("locationId")

	if r.Method == http.MethodGet {
		productID := r.URL.Query().Get("product_id")
		if productID == "" {
			fail(w, domain.ErrValidation.WithField("product_id", "product_id is required."))
			return
		}
		hours, err := s.deps.Booking.ServiceHours(r.Context(), user.ID, businessID, locationID, productID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"hours": hours})
		return
	}

	var in struct {
		Hours map[string]domain.DayHours `json:"hours"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Booking.SetServiceHours(r.Context(), user.ID, businessID, locationID, r.PathValue("productId"), in.Hours); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}
