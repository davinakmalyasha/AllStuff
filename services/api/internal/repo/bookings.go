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
