package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"bizverse/api/internal/domain"
)

type BookingRepo struct{ pool pooler }

const appointmentCols = `a.id, a.business_id, b.name, b.slug, b.timezone, a.product_id, pr.name,
	a.customer_id, u.name, coalesce(a.location_id::text, ''), coalesce(l.name, ''),
	coalesce(a.staff_id::text, ''), coalesce(st.name, ''),
	a.starts_at, a.ends_at, a.status, coalesce(a.notes, ''), a.created_at`

func scanAppointment(row pgx.Row) (*domain.Appointment, error) {
	var a domain.Appointment
	if err := row.Scan(&a.ID, &a.BusinessID, &a.BusinessName, &a.BusinessSlug, &a.BusinessTimezone, &a.ProductID,
		&a.ProductName, &a.CustomerID, &a.CustomerName,
		&a.LocationID, &a.LocationName, &a.StaffID, &a.StaffName,
		&a.StartsAt, &a.EndsAt, &a.Status,
		&a.Notes, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

const appointmentFrom = `
	FROM appointments a
	JOIN businesses b ON b.id = a.business_id
	JOIN products pr  ON pr.id = a.product_id
	JOIN users u      ON u.id = a.customer_id
	LEFT JOIN business_staff st ON st.id = a.staff_id
	LEFT JOIN business_locations l ON l.id = a.location_id`

// CreateAppointment inserts a booking.
//
// A concurrent booking of the same slot fails here with SQLSTATE 23P01 (the
// EXCLUDE constraint): callers must translate that into a conflict rather than
// an internal error, because it is the expected outcome of a race and not a bug.
func (r *BookingRepo) CreateAppointment(ctx context.Context, a *domain.Appointment) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO appointments (id, business_id, product_id, customer_id, staff_id, location_id, starts_at, ends_at, status, notes)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, NULLIF($6, '')::uuid, $7, $8, $9, $10)`,
		a.ID, a.BusinessID, a.ProductID, a.CustomerID, a.StaffID, a.LocationID,
		a.StartsAt, a.EndsAt, string(a.Status), a.Notes)
	return err
}

func (r *BookingRepo) GetAppointment(ctx context.Context, id string) (*domain.Appointment, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+appointmentCols+appointmentFrom+` WHERE a.id = $1`, id)
	a, err := scanAppointment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// SetAppointmentStatus moves a booking out of an occupying state. The WHERE
// clause re-checks the current status so two simultaneous cancellations cannot
// both "win".
func (r *BookingRepo) SetAppointmentStatus(ctx context.Context, id string, status domain.AppointmentStatus, by string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE appointments
		   SET status = $2, cancelled_by = $3, cancelled_at = now(), updated_at = now()
		 WHERE id = $1 AND status IN ('pending', 'confirmed')`,
		id, string(status), by)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetAppointmentState moves a booking to `to`, but only from one of the `from`
// states. The caller passes the states its transition allows, so the UPDATE
// itself is the guard: two managers clicking at once cannot both apply one.
func (r *BookingRepo) SetAppointmentState(ctx context.Context, id string, to domain.AppointmentStatus, from ...domain.AppointmentStatus) (bool, error) {
	allowed := make([]string, 0, len(from))
	for _, f := range from {
		allowed = append(allowed, string(f))
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE appointments SET status = $2, updated_at = now()
		 WHERE id = $1 AND status = ANY($3)`, id, string(to), allowed)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *BookingRepo) ListForCustomer(ctx context.Context, customerID string, from time.Time) ([]*domain.Appointment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+appointmentCols+appointmentFrom+`
		WHERE a.customer_id = $1 AND a.ends_at >= $2
		ORDER BY a.starts_at ASC`, customerID, from)
	if err != nil {
		return nil, err
	}
	return collectAppointments(rows)
}

func (r *BookingRepo) ListForBusiness(ctx context.Context, businessID string, from, to time.Time) ([]*domain.Appointment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+appointmentCols+appointmentFrom+`
		WHERE a.business_id = $1 AND a.starts_at < $3 AND a.ends_at > $2
		ORDER BY a.starts_at ASC`, businessID, from, to)
	if err != nil {
		return nil, err
	}
	return collectAppointments(rows)
}

func collectAppointments(rows pgx.Rows) ([]*domain.Appointment, error) {
	defer rows.Close()
	var out []*domain.Appointment
	for rows.Next() {
		a, err := scanAppointment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// BusyRanges is everything that occupies the calendar in [from, to): live
// appointments and blackouts alike, in one round trip. Slot generation needs
// both, and two queries for the same range would let a booking land between
// them under concurrency.
func (r *BookingRepo) BusyRanges(ctx context.Context, businessID string, from, to time.Time) ([][2]time.Time, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT starts_at, ends_at FROM appointments
		 WHERE business_id = $1 AND status IN ('pending', 'confirmed')
		   AND starts_at < $3 AND ends_at > $2
		UNION ALL
		SELECT starts_at, ends_at FROM booking_blackouts
		 WHERE business_id = $1 AND starts_at < $3 AND ends_at > $2
		ORDER BY 1`, businessID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]time.Time
	for rows.Next() {
		var s, e time.Time
		if err := rows.Scan(&s, &e); err != nil {
			return nil, err
		}
		out = append(out, [2]time.Time{s, e})
	}
	return out, rows.Err()
}

func (r *BookingRepo) CreateBlackout(ctx context.Context, b *domain.Blackout) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO booking_blackouts (id, business_id, starts_at, ends_at, reason)
		VALUES ($1, $2, $3, $4, $5)`, b.ID, b.BusinessID, b.StartsAt, b.EndsAt, b.Reason)
	return err
}

func (r *BookingRepo) ListBlackouts(ctx context.Context, businessID string, from time.Time) ([]*domain.Blackout, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, business_id, starts_at, ends_at, coalesce(reason, '')
		FROM booking_blackouts
		WHERE business_id = $1 AND ends_at > $2
		ORDER BY starts_at ASC`, businessID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Blackout
	for rows.Next() {
		var b domain.Blackout
		if err := rows.Scan(&b.ID, &b.BusinessID, &b.StartsAt, &b.EndsAt, &b.Reason); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

func (r *BookingRepo) DeleteBlackout(ctx context.Context, businessID, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM booking_blackouts WHERE id = $1 AND business_id = $2`, id, businessID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// RescheduleAppointment moves a live booking to a new start/end. The WHERE
// clause re-checks the status so a booking cancelled while the dialog was open
// cannot be moved back into the calendar. Overlap with another booking is still
// the EXCLUDE constraint's call: callers must map 23P01 to ErrSlotTaken.
func (r *BookingRepo) RescheduleAppointment(ctx context.Context, id string, starts, ends time.Time, staffID string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE appointments
		   SET starts_at = $2, ends_at = $3, staff_id = NULLIF($4, '')::uuid, updated_at = now()
		 WHERE id = $1 AND status IN ('pending', 'confirmed')`,
		id, starts, ends, staffID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimAppointmentEvent records that one timed action for one appointment has
// been handled. It returns false when the row already exists, which is how a
// retry or a second replica knows not to notify twice, and false when the
// appointment has since left the state the action was for (a booking cancelled
// between the scan and the claim). The claim is written BEFORE the
// notification: a crash between the two loses the nudge instead of
// duplicating it.
func (r *BookingRepo) ClaimAppointmentEvent(ctx context.Context, appointmentID, kind string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO appointment_events (id, appointment_id, kind)
		SELECT gen_random_uuid(), a.id, $2
		  FROM appointments a
		 WHERE a.id = $1
		   AND (
		        (a.status = 'confirmed' AND $2 IN ('reminder_24h', 'reminder_2h'))
		     OR (a.status = 'completed' AND $2 = 'review_nudge')
		   )
		ON CONFLICT (appointment_id, kind) DO NOTHING`, appointmentID, kind)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseAppointmentEvent removes a claim so the next run can retry. Used when
// the notification could not be created at all - keeping the claim would turn
// a transient failure into a permanently missing reminder.
func (r *BookingRepo) ReleaseAppointmentEvent(ctx context.Context, appointmentID, kind string) {
	_, _ = r.pool.Exec(ctx,
		`DELETE FROM appointment_events WHERE appointment_id = $1 AND kind = $2`, appointmentID, kind)
}

// HasClaimedEvent reports whether an appointment already has this action.
func (r *BookingRepo) HasClaimedEvent(ctx context.Context, appointmentID, kind string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM appointment_events WHERE appointment_id = $1 AND kind = $2)`,
		appointmentID, kind).Scan(&exists)
	return exists, err
}

// DueAppointment is the minimum a reminder needs: who to notify and what the
// message should say. It deliberately does not carry the full appointment row -
// the job runs over every tenant and only needs these fields.
type DueAppointment struct {
	ID           string
	CustomerID   string
	CustomerName string
	BusinessID   string
	BusinessName string
	BusinessSlug string
	ProductName  string
	Timezone     string
	StartsAt     time.Time
}

// DueReminders returns CONFIRMED appointments starting inside [from, to) that
// have not yet claimed the given reminder kind. Pending is deliberately
// excluded: it means the owner has not accepted the booking yet, and reminding
// the customer about a slot that may be declined is worse than silence. The claim is NOT taken here: the
// job claims each row one at a time, so a slow send cannot hold a transaction
// open over the whole window.
func (r *BookingRepo) DueReminders(ctx context.Context, kind string, from, to time.Time) ([]DueAppointment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.customer_id, u.name, a.business_id, b.name, b.slug,
		       pr.name, b.timezone, a.starts_at
		FROM appointments a
		JOIN users u ON u.id = a.customer_id
		JOIN businesses b ON b.id = a.business_id
		JOIN products pr ON pr.id = a.product_id
		WHERE a.status = 'confirmed'
		  AND a.starts_at >= $2 AND a.starts_at < $3
		  AND NOT EXISTS (
			SELECT 1 FROM appointment_events e
			 WHERE e.appointment_id = a.id AND e.kind = $1)
		ORDER BY a.starts_at ASC`, kind, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueAppointment
	for rows.Next() {
		var d DueAppointment
		if err := rows.Scan(&d.ID, &d.CustomerID, &d.CustomerName, &d.BusinessID,
			&d.BusinessName, &d.BusinessSlug, &d.ProductName, &d.Timezone, &d.StartsAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DueReviewNudges returns completed appointments that ended inside [from, to)
// and whose customer has not reviewed the business yet. A business with one
// review per customer is the schema's rule, so the NOT EXISTS is exact rather
// than a heuristic.
func (r *BookingRepo) DueReviewNudges(ctx context.Context, from, to time.Time) ([]DueAppointment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.customer_id, u.name, a.business_id, b.name, b.slug,
		       pr.name, b.timezone, a.starts_at
		FROM appointments a
		JOIN users u ON u.id = a.customer_id
		JOIN businesses b ON b.id = a.business_id
		JOIN products pr ON pr.id = a.product_id
		WHERE a.status = 'completed'
		  AND a.ends_at >= $1 AND a.ends_at < $2
		  AND NOT EXISTS (
			SELECT 1 FROM appointment_events e
			 WHERE e.appointment_id = a.id AND e.kind = 'review_nudge')
		  AND NOT EXISTS (
			SELECT 1 FROM reviews r
			 WHERE r.business_id = a.business_id AND r.user_id = a.customer_id
			   AND r.deleted_at IS NULL)
		ORDER BY a.ends_at ASC`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueAppointment
	for rows.Next() {
		var d DueAppointment
		if err := rows.Scan(&d.ID, &d.CustomerID, &d.CustomerName, &d.BusinessID,
			&d.BusinessName, &d.BusinessSlug, &d.ProductName, &d.Timezone, &d.StartsAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// IsSlotConflict reports whether err is the appointments_no_overlap EXCLUDE
// violation. Two clients booking the same time is an ordinary race, not a bug,
// so the caller has to distinguish it from a real write failure.
func IsSlotConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23P01"
}

// BlackoutRanges is the business-wide unavailability in [from, to). Separate from
// BusyRanges because a staffed calendar needs the two apart: a blackout blocks
// everyone, while an appointment blocks only the person assigned to it.
func (r *BookingRepo) BlackoutRanges(ctx context.Context, businessID string, from, to time.Time) ([][2]time.Time, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT starts_at, ends_at FROM booking_blackouts
		 WHERE business_id = $1 AND starts_at < $3 AND ends_at > $2
		ORDER BY 1`, businessID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]time.Time
	for rows.Next() {
		var s, e time.Time
		if err := rows.Scan(&s, &e); err != nil {
			return nil, err
		}
		out = append(out, [2]time.Time{s, e})
	}
	return out, rows.Err()
}
