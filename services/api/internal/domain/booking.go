package domain

import "time"

// AppointmentStatus is the lifecycle of a booking.
//
// 'pending' exists for businesses that want to confirm by hand; the default is
// 'confirmed' because a self-service booking that silently waits is worse than
// one that is immediately in the calendar.
type AppointmentStatus string

const (
	AppointmentPending   AppointmentStatus = "pending"
	AppointmentConfirmed AppointmentStatus = "confirmed"
	AppointmentCancelled AppointmentStatus = "cancelled"
	AppointmentCompleted AppointmentStatus = "completed"
	AppointmentNoShow    AppointmentStatus = "no_show"
)

// OccupiesSlot reports whether an appointment in this state holds its time
// range. Cancelled and finished bookings must stop blocking the calendar, and
// the database's EXCLUDE constraint carries the same predicate.
func (s AppointmentStatus) OccupiesSlot() bool {
	return s == AppointmentPending || s == AppointmentConfirmed
}

type Appointment struct {
	ID           string            `json:"id"`
	BusinessID   string            `json:"business_id"`
	BusinessName string            `json:"business_name,omitempty"`
	BusinessSlug string            `json:"business_slug,omitempty"`
	ProductID    string            `json:"product_id"`
	ProductName  string            `json:"product_name,omitempty"`
	CustomerID   string            `json:"customer_id"`
	CustomerName string            `json:"customer_name,omitempty"`
	LocationID   string            `json:"location_id,omitempty"`
	LocationName string            `json:"location_name,omitempty"`
	StaffID      string            `json:"staff_id,omitempty"`
	StaffName    string            `json:"staff_name,omitempty"`
	StartsAt     time.Time         `json:"starts_at"`
	EndsAt       time.Time         `json:"ends_at"`
	Status       AppointmentStatus `json:"status"`
	Notes        string            `json:"notes,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

// Blackout is time the business is unavailable regardless of its hours.
type Blackout struct {
	ID         string    `json:"id"`
	BusinessID string    `json:"business_id"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
	Reason     string    `json:"reason,omitempty"`
}

// Slot is one bookable start time. The end is implied by the service duration,
// but it is returned so a client does not have to know the duration to render it.
type Slot struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	// StaffAvailable is how many qualified staff are free at this time. Zero
	// never appears - a slot with nobody free is not offered - but the number
	// lets a client show "3 slots left" or hide a nearly-full day.
	StaffAvailable int `json:"staff_available,omitempty"`
}

// Staff is a bookable person on a business. Not every staff member has a
// platform account: many shops will never give their staff a login, and
// requiring one would block the calendar for the businesses that need it most.
type Staff struct {
	ID         string               `json:"id"`
	BusinessID string               `json:"business_id"`
	UserID     string               `json:"user_id,omitempty"`
	Name       string               `json:"name"`
	Email      string               `json:"email,omitempty"`
	Phone      string               `json:"phone,omitempty"`
	Active     bool                 `json:"active"`
	Services   []string             `json:"services"` // product ids this person performs
	Hours      map[string]StaffHour `json:"hours"`    // weekday -> shift; absent means "inherit the business hours"
	CreatedAt  time.Time            `json:"created_at"`
}

// StaffHour is one weekday's shift. A row with Working false is a day off, which
// is different from an absent row (inherit the business hours).
type StaffHour struct {
	Working  bool   `json:"working"`
	OpensAt  string `json:"opens_at,omitempty"`
	ClosesAt string `json:"closes_at,omitempty"`
}

// Location is one place a business operates from. The business's own address and
// hours columns stay as a cached copy of the PRIMARY location, so every existing
// reader (search, directory, storefront, SSR) keeps working unchanged.
type Location struct {
	ID           string         `json:"id"`
	BusinessID   string         `json:"business_id"`
	Name         string         `json:"name"`
	Address      string         `json:"address"`
	City         string         `json:"city"`
	Country      string         `json:"country"`
	Lat          float64        `json:"lat"`
	Lng          float64        `json:"lng"`
	Timezone     string         `json:"timezone"`
	Hours        map[string]any `json:"hours"`
	SpecialHours map[string]any `json:"special_hours"`
	Phone        string         `json:"phone,omitempty"`
	IsPrimary    bool           `json:"is_primary"`
	CreatedAt    time.Time      `json:"created_at"`
}
