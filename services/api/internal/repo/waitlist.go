package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
)

// WaitlistRepo — requests to be told when a full day frees up.
type WaitlistRepo struct{ pool pooler }

const waitlistCols = `w.id, w.business_id, b.name, b.slug, w.product_id, p.name,
	w.customer_id, to_char(w.day, 'YYYY-MM-DD'), w.created_at, w.notified_at`

const waitlistFrom = `
	FROM booking_waitlist w
	JOIN businesses b ON b.id = w.business_id
	JOIN products p ON p.id = w.product_id`

func scanWaitlist(row pgx.Row) (*domain.WaitlistEntry, error) {
	var w domain.WaitlistEntry
	if err := row.Scan(&w.ID, &w.BusinessID, &w.BusinessName, &w.BusinessSlug,
		&w.ProductID, &w.ProductName, &w.CustomerID, &w.Day, &w.CreatedAt, &w.NotifiedAt); err != nil {
		return nil, err
	}
	return &w, nil
}

// Join is idempotent: asking twice for the same day returns the existing entry
// rather than a duplicate or an error. The unique key is the arbiter, so two
// concurrent clicks cannot both insert.
func (r *WaitlistRepo) Join(ctx context.Context, id, businessID, productID, customerID, day string) (*domain.WaitlistEntry, error) {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO booking_waitlist (id, business_id, product_id, customer_id, day)
		VALUES ($1, $2, $3, $4, $5::date)
		ON CONFLICT (business_id, product_id, customer_id, day) DO NOTHING`,
		id, businessID, productID, customerID, day)
	if err != nil {
		return nil, err
	}
	row := r.pool.QueryRow(ctx, `SELECT `+waitlistCols+waitlistFrom+`
		WHERE w.business_id=$1 AND w.product_id=$2 AND w.customer_id=$3 AND w.day=$4::date`,
		businessID, productID, customerID, day)
	w, err := scanWaitlist(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

// Leave removes one of the caller's own entries; another user's id matches no
// row, so ownership is enforced by the predicate rather than a prior read.
func (r *WaitlistRepo) Leave(ctx context.Context, customerID, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM booking_waitlist WHERE id = $1 AND customer_id = $2`, id, customerID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ForCustomer lists live (today or later) entries with the names the page
// shows. A timezone mismatch of one day at the boundary is acceptable here:
// this is presentation, and booking itself uses the business timezone.
func (r *WaitlistRepo) ForCustomer(ctx context.Context, customerID string) ([]*domain.WaitlistEntry, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+waitlistCols+waitlistFrom+`
		WHERE w.customer_id = $1 AND w.day >= current_date
		ORDER BY w.day ASC, w.created_at ASC`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.WaitlistEntry
	for rows.Next() {
		w, err := scanWaitlist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// FirstOpen returns the earliest waiter for one business, service and day who
// has not been notified yet. It does not lock the row: the caller marks it
// immediately, and a duplicate notification from a simultaneous cancellation
// is a notification, not corrupted state.
func (r *WaitlistRepo) FirstOpen(ctx context.Context, businessID, productID, day string) (*domain.WaitlistEntry, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+waitlistCols+waitlistFrom+`
		WHERE w.business_id=$1 AND w.product_id=$2 AND w.day=$3::date
		  AND w.notified_at IS NULL
		ORDER BY w.created_at ASC, w.id ASC
		LIMIT 1`, businessID, productID, day)
	w, err := scanWaitlist(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

// MarkNotified records that the waiter heard about a freed time. The WHERE
// clause keeps a second cancellation from re-notifying the same entry.
func (r *WaitlistRepo) MarkNotified(ctx context.Context, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE booking_waitlist SET notified_at = now() WHERE id = $1 AND notified_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// DemandForBusiness groups the queue by day from fromDay onward. Counts only -
// the owner learns "Saturday has four people waiting", not who they are.
func (r *WaitlistRepo) DemandForBusiness(ctx context.Context, businessID, fromDay string) ([]*domain.WaitlistDemand, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT to_char(day, 'YYYY-MM-DD'),
		       count(*) FILTER (WHERE notified_at IS NULL)::int,
		       count(*) FILTER (WHERE notified_at IS NOT NULL)::int
		FROM booking_waitlist
		WHERE business_id = $1 AND day >= $2::date
		GROUP BY day
		ORDER BY day ASC
		LIMIT 60`, businessID, fromDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.WaitlistDemand
	for rows.Next() {
		var d domain.WaitlistDemand
		if err := rows.Scan(&d.Day, &d.Waiting, &d.Notified); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

// DayIn formats an instant as the calendar date the business is living in.
func DayIn(t time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("2006-01-02")
}
