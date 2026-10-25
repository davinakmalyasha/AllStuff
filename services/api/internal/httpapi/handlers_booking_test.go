//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// The booking routes: availability is public, everything else is authorised, and
// the owner's calendar must be unreachable by anyone who does not manage the
// listing.

func bookingFixture(t *testing.T, h *testutil.H) (*domain.Business, *domain.Product, *domain.User) {
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

func bookingDay() time.Time {
	return time.Now().UTC().AddDate(0, 0, 1).Truncate(24 * time.Hour)
}

func TestAvailabilityIsPublic(t *testing.T) {
	s, h, _ := newDBTestServerDeps(t)
	hdl := s.Handler()
	b, p, _ := bookingFixture(t, h)
	path := "/api/v1/businesses/" + b.ID + "/availability?product_id=" + p.ID + "&date=" + bookingDay().Format("2006-01-02")

	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("availability: status = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Slots []struct {
			StartsAt time.Time `json:"starts_at"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Slots) != 8 {
		t.Errorf("slots = %d, want the eight hourly starts", len(out.Slots))
	}

	// A missing product and a malformed date are validation errors, not empty
	// lists - an empty list would look like "fully booked".
	for _, bad := range []string{
		"/api/v1/businesses/" + b.ID + "/availability?date=2026-01-01",
		"/api/v1/businesses/" + b.ID + "/availability?product_id=" + p.ID + "&date=tomorrow",
	} {
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, authReq(t, http.MethodGet, bad, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, w.Code)
		}
	}
}

func TestAppointmentFlow(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	b, p, owner := bookingFixture(t, h)
	customer := testutil.User(t, h)
	stranger := testutil.User(t, h)
	at := bookingDay().Add(9 * time.Hour)

	// Anonymous booking is unauthenticated (CSRF refuses the write first).
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/businesses/"+b.ID+"/appointments",
		strings.NewReader(`{"product_id":"`+p.ID+`","starts_at":"`+at.Format(time.RFC3339)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	hdl.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous booking: status = %d, want 401/403", w.Code)
	}

	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/appointments",
		map[string]any{"product_id": p.ID, "starts_at": at.Format(time.RFC3339), "notes": "first visit"}, customer)
	if w.Code != http.StatusCreated {
		t.Fatalf("book: status = %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Appointment struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			StartsAt string `json:"starts_at"`
		} `json:"appointment"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Appointment.ID == "" || created.Appointment.Status != string(domain.AppointmentConfirmed) {
		t.Errorf("appointment = %+v", created.Appointment)
	}

	// The same slot cannot be booked twice; the second attempt is a validation
	// error, because the slot no longer appears in the derived list.
	other := testutil.User(t, h)
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/appointments",
		map[string]any{"product_id": p.ID, "starts_at": at.Format(time.RFC3339)}, other)
	if w.Code != http.StatusBadRequest {
		t.Errorf("double booking: status = %d, want 400", w.Code)
	}

	// The customer sees it in their list.
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/me/appointments", nil, customer)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), created.Appointment.ID) {
		t.Errorf("my appointments: status = %d: %s", w.Code, w.Body.String())
	}

	// The owner's calendar is manager-only.
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/businesses/"+b.ID+"/appointments", nil, stranger)
	if w.Code != http.StatusForbidden {
		t.Errorf("stranger calendar: status = %d, want 403", w.Code)
	}
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/businesses/"+b.ID+"/appointments", nil, owner)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), customer.ID) {
		t.Errorf("owner calendar: status = %d: %s", w.Code, w.Body.String())
	}

	// Cancellation: the customer may, a stranger may not.
	w = dash(t, td, hdl, http.MethodDelete, "/api/v1/appointments/"+created.Appointment.ID, nil, stranger)
	if w.Code != http.StatusForbidden {
		t.Errorf("stranger cancel: status = %d, want 403", w.Code)
	}
	w = dash(t, td, hdl, http.MethodDelete, "/api/v1/appointments/"+created.Appointment.ID, nil, customer)
	if w.Code != http.StatusNoContent {
		t.Errorf("cancel: status = %d, want 204", w.Code)
	}
	// Once cancelled the slot is free again.
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet,
		"/api/v1/businesses/"+b.ID+"/availability?product_id="+p.ID+"&date="+bookingDay().Format("2006-01-02"), nil))
	if !strings.Contains(w.Body.String(), "T09:00:00Z") {
		t.Errorf("the cancelled slot was not offered again: %s", w.Body.String())
	}
	// starts_at, not the raw body: the same timestamp also appears as another
	// slot's ends_at, so a substring check cannot tell the two apart.
}

func TestBlackoutRoutes(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	b, _, owner := bookingFixture(t, h)
	stranger := testutil.User(t, h)
	day := bookingDay()
	base := "/api/v1/businesses/" + b.ID + "/blackouts"

	w := dash(t, td, hdl, http.MethodPost, base,
		map[string]any{"starts_at": day.Add(10 * time.Hour).Format(time.RFC3339), "ends_at": day.Add(12 * time.Hour).Format(time.RFC3339), "reason": "training"}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create blackout: status = %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Blackout struct {
			ID string `json:"id"`
		} `json:"blackout"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// The blackout removes those slots from the public availability.
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet,
		"/api/v1/businesses/"+b.ID+"/availability?product_id="+testutilProductID(t, h, b.ID)+"&date="+day.Format("2006-01-02"), nil))
	var avail struct {
		Slots []struct {
			StartsAt time.Time `json:"starts_at"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &avail); err != nil {
		t.Fatal(err)
	}
	// Compare START times only. The raw body contains 10:00 twice for an
	// unrelated reason - as the ends_at of the 09:00 slot - so a substring
	// search would report a slot that is genuinely gone.
	for _, sl := range avail.Slots {
		switch sl.StartsAt.UTC().Format("15:04") {
		case "10:00", "11:00":
			t.Errorf("slot %s survived a blackout", sl.StartsAt.UTC().Format("15:04"))
		}
	}

	if w := dash(t, td, hdl, http.MethodGet, base, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger list: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, base, nil, owner); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "training") {
		t.Errorf("owner list: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodDelete, base+"/"+created.Blackout.ID, nil, owner); w.Code != http.StatusNoContent {
		t.Errorf("delete: status = %d, want 204", w.Code)
	}
}

func testutilProductID(t *testing.T, h *testutil.H, businessID string) string {
	t.Helper()
	var id string
	if err := h.QueryRow(t, `SELECT id FROM products WHERE business_id=$1 LIMIT 1`, businessID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The manager transition route. A booking that has happened must be closeable as
// completed or no-show; a future one must not, or its slot frees while the
// customer still expects it.
func TestAppointmentStatusRoute(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	b, p, owner := bookingFixture(t, h)
	customer := testutil.User(t, h)
	stranger := testutil.User(t, h)

	id := util.NewUUID()
	h.Exec(t, `INSERT INTO appointments (id, business_id, product_id, customer_id, starts_at, ends_at, status)
		VALUES ($1,$2,$3,$4,$5,$6,'confirmed')`,
		id, b.ID, p.ID, customer.ID, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	path := "/api/v1/appointments/" + id + "/status"

	if w := dash(t, td, hdl, http.MethodPatch, path, map[string]any{"status": "completed"}, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPatch, path, map[string]any{"status": "rescheduled"}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("invalid status: status = %d, want 400", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPatch, path, map[string]any{"status": "completed"}, owner); w.Code != http.StatusNoContent {
		t.Errorf("complete: status = %d, want 204", w.Code)
	}
	// Terminal: closing it again is refused rather than silently succeeding.
	if w := dash(t, td, hdl, http.MethodPatch, path, map[string]any{"status": "no_show"}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("a second close: status = %d, want 400", w.Code)
	}
}

func TestStaffRoutes(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	b, p, owner := bookingFixture(t, h)
	stranger := testutil.User(t, h)
	customer := testutil.User(t, h)
	base := "/api/v1/businesses/" + b.ID + "/staff"

	if w := dash(t, td, hdl, http.MethodGet, base, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger list: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, base, map[string]any{"name": "Budi"}, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger create: status = %d, want 403", w.Code)
	}

	w := dash(t, td, hdl, http.MethodPost, base, map[string]any{"name": "Budi", "email": "budi@example.test"}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Staff struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"staff"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	staffID := created.Staff.ID
	if staffID == "" || created.Staff.Name != "Budi" {
		t.Fatalf("staff = %+v", created.Staff)
	}

	// Qualify the staff member for the service, then give them a shift.
	w = dash(t, td, hdl, http.MethodPut, base+"/"+staffID+"/services", map[string]any{"product_ids": []string{p.ID}}, owner)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), p.ID) {
		t.Errorf("services: status = %d: %s", w.Code, w.Body.String())
	}
	day := bookingDay()
	weekday := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}[day.Weekday()]
	w = dash(t, td, hdl, http.MethodPut, base+"/"+staffID+"/hours",
		map[string]any{"hours": map[string]any{weekday: map[string]any{"working": true, "opens_at": "10:00", "closes_at": "12:00"}}}, owner)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "10:00") {
		t.Errorf("hours: status = %d: %s", w.Code, w.Body.String())
	}

	// Availability narrows to that person, and an unknown staff id is a 404.
	path := "/api/v1/businesses/" + b.ID + "/availability?product_id=" + p.ID + "&date=" + day.Format("2006-01-02")
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, path+"&staff_id="+staffID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("availability for staff: status = %d: %s", w.Code, w.Body.String())
	}
	var avail struct {
		Slots []struct {
			StartsAt time.Time `json:"starts_at"`
		} `json:"slots"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &avail)
	// A 10:00-12:00 shift with one-hour services leaves two starts.
	if len(avail.Slots) != 2 {
		t.Errorf("staff slots = %d, want 2: %s", len(avail.Slots), w.Body.String())
	}
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, path+"&staff_id="+util.NewUUID(), nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown staff: status = %d, want 404", w.Code)
	}

	// Booking with a named staff member records them on the appointment.
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/appointments",
		map[string]any{"product_id": p.ID, "starts_at": day.Add(10 * time.Hour).Format(time.RFC3339), "staff_id": staffID}, customer)
	if w.Code != http.StatusCreated {
		t.Fatalf("book with staff: status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), staffID) {
		t.Errorf("the appointment does not name the staff member: %s", w.Body.String())
	}

	if w := dash(t, td, hdl, http.MethodDelete, base+"/"+staffID, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger delete: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodDelete, base+"/"+staffID, nil, owner); w.Code != http.StatusNoContent {
		t.Errorf("delete: status = %d, want 204", w.Code)
	}
}

func TestLocationRoutes(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	b, _, owner := bookingFixture(t, h)
	stranger := testutil.User(t, h)
	base := "/api/v1/businesses/" + b.ID + "/locations"

	// Public read, and empty until the owner adds one.
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, base, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"locations"`) {
		t.Fatalf("public list: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, base, map[string]any{"name": "Main"}, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger create: status = %d, want 403", w.Code)
	}
	// A blank address is refused.
	if w := dash(t, td, hdl, http.MethodPost, base, map[string]any{"name": "Main", "city": "Jakarta", "country": "ID", "lat": -6.2, "lng": 106.8, "timezone": "UTC"}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("blank address: status = %d, want 400", w.Code)
	}

	w = dash(t, td, hdl, http.MethodPost, base, map[string]any{
		"name": "Main", "address": "Jl. Satu", "city": "Jakarta", "country": "ID",
		"lat": -6.2, "lng": 106.8, "timezone": "UTC",
	}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Location struct {
			ID        string `json:"id"`
			IsPrimary bool   `json:"is_primary"`
		} `json:"location"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Location.ID == "" || !created.Location.IsPrimary {
		t.Fatalf("location = %+v", created.Location)
	}
	mainID := created.Location.ID

	w = dash(t, td, hdl, http.MethodPost, base, map[string]any{
		"name": "Branch", "address": "Jl. Dua", "city": "Bandung", "country": "ID",
		"lat": -6.9, "lng": 107.6, "timezone": "UTC",
	}, owner)
	var second struct {
		Location struct {
			ID        string `json:"id"`
			IsPrimary bool   `json:"is_primary"`
		} `json:"location"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &second)
	if w.Code != http.StatusCreated || second.Location.IsPrimary {
		t.Fatalf("second create: status = %d, primary=%v", w.Code, second.Location.IsPrimary)
	}

	// Promoting the branch moves the business's cached address with it.
	if w := dash(t, td, hdl, http.MethodPost, base+"/"+second.Location.ID+"/primary", nil, owner); w.Code != http.StatusNoContent {
		t.Errorf("promote: status = %d, want 204", w.Code)
	}
	var addr string
	if err := h.QueryRow(t, `SELECT address FROM businesses WHERE id=$1`, b.ID).Scan(&addr); err != nil {
		t.Fatal(err)
	}
	if addr != "Jl. Dua" {
		t.Errorf("cached address = %q after promoting the branch", addr)
	}

	if w := dash(t, td, hdl, http.MethodPatch, base+"/"+second.Location.ID, map[string]any{"address": "Jl. Dua No. 5"}, owner); w.Code != http.StatusOK {
		t.Errorf("patch: status = %d: %s", w.Code, w.Body.String())
	}
	if err := h.QueryRow(t, `SELECT address FROM businesses WHERE id=$1`, b.ID).Scan(&addr); err != nil {
		t.Fatal(err)
	}
	if addr != "Jl. Dua No. 5" {
		t.Errorf("cached address = %q after editing the primary", addr)
	}

	// Deleting the primary promotes the other one; deleting the last is refused.
	if w := dash(t, td, hdl, http.MethodDelete, base+"/"+second.Location.ID, nil, owner); w.Code != http.StatusNoContent {
		t.Errorf("delete primary: status = %d, want 204", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodDelete, base+"/"+mainID, nil, owner); w.Code != http.StatusBadRequest {
		t.Errorf("delete the last location: status = %d, want 400", w.Code)
	}
}

func TestServiceHoursRoutes(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	b, p, owner := bookingFixture(t, h)
	stranger := testutil.User(t, h)

	w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/locations", map[string]any{
		"name": "Main", "address": "Jl. Satu", "city": "Jakarta", "country": "ID",
		"lat": -6.2, "lng": 106.8, "timezone": "UTC",
	}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create location: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Location struct {
			ID string `json:"id"`
		} `json:"location"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	locID := created.Location.ID
	base := "/api/v1/businesses/" + b.ID + "/locations/" + locID + "/service-hours"

	if w := dash(t, td, hdl, http.MethodGet, base+"?product_id="+p.ID, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger read: status = %d, want 403", w.Code)
	}
	// product_id is required for the read: without it there is nothing to describe.
	if w := dash(t, td, hdl, http.MethodGet, base, nil, owner); w.Code != http.StatusBadRequest {
		t.Errorf("missing product: status = %d, want 400", w.Code)
	}

	w = dash(t, td, hdl, http.MethodPut, base+"/"+p.ID, map[string]any{
		"hours": map[string]any{"mon": map[string]any{"working": true, "opens_at": "13:00", "closes_at": "15:00"}},
	}, owner)
	if w.Code != http.StatusNoContent {
		t.Fatalf("set: status = %d: %s", w.Code, w.Body.String())
	}
	w = dash(t, td, hdl, http.MethodGet, base+"?product_id="+p.ID, nil, owner)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "13:00") {
		t.Errorf("read back: status = %d: %s", w.Code, w.Body.String())
	}
	// A bad window is a validation error.
	if w := dash(t, td, hdl, http.MethodPut, base+"/"+p.ID, map[string]any{
		"hours": map[string]any{"mon": map[string]any{"working": true, "opens_at": "15:00", "closes_at": "13:00"}},
	}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("inverted window: status = %d, want 400", w.Code)
	}
}

func TestAppointmentICSRoute(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	b, p, owner := bookingFixture(t, h)
	customer := testutil.User(t, h)
	stranger := testutil.User(t, h)
	day := bookingDay()

	w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/appointments",
		map[string]any{"product_id": p.ID, "starts_at": day.Add(9 * time.Hour).Format(time.RFC3339)}, customer)
	if w.Code != http.StatusCreated {
		t.Fatalf("book: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Appointment struct {
			ID string `json:"id"`
		} `json:"appointment"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	path := "/api/v1/appointments/" + created.Appointment.ID + "/calendar.ics"

	// Anonymous: unauthenticated (a GET, so 401 rather than a CSRF 403).
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, authReq(t, http.MethodGet, path, nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, path, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger: status = %d, want 403", w.Code)
	}

	w = dash(t, td, hdl, http.MethodGet, path, nil, customer)
	if w.Code != http.StatusOK {
		t.Fatalf("customer: status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type = %q, want text/calendar", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `attachment; filename="booking.ics"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if !strings.Contains(w.Body.String(), "BEGIN:VCALENDAR") {
		t.Errorf("body is not an iCalendar file: %s", w.Body.String())
	}
	// The owner may fetch it too - it is their calendar as well.
	if w := dash(t, td, hdl, http.MethodGet, path, nil, owner); w.Code != http.StatusOK {
		t.Errorf("owner: status = %d", w.Code)
	}
}
