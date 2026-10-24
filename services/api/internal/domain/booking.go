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
}
