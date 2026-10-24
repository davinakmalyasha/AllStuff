package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/util"
)

type StaffRepo struct{ pool pooler }

const staffCols = `s.id, s.business_id, COALESCE(s.user_id::text, ''), s.name,
	COALESCE(s.email::text, ''), COALESCE(s.phone, ''), s.active, s.created_at`

func (r *StaffRepo) Create(ctx context.Context, s *domain.Staff) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO business_staff (id, business_id, user_id, name, email, phone, active)
		VALUES ($1, $2, NULLIF($3, '')::uuid, $4, NULLIF($5, '')::citext, NULLIF($6, ''), $7)`,
		s.ID, s.BusinessID, s.UserID, s.Name, s.Email, s.Phone, s.Active)
	return err
}

func (r *StaffRepo) Get(ctx context.Context, id string) (*domain.Staff, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+staffCols+` FROM business_staff s WHERE s.id = $1`, id)
	s, err := scanStaff(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := r.loadQualifications(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

func (r *StaffRepo) ListByBusiness(ctx context.Context, businessID string, activeOnly bool) ([]*domain.Staff, error) {
	q := `SELECT ` + staffCols + ` FROM business_staff s WHERE s.business_id = $1`
	if activeOnly {
		q += ` AND s.active`
	}
	q += ` ORDER BY s.name ASC`
	rows, err := r.pool.Query(ctx, q, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Staff
	for rows.Next() {
		s, err := scanStaff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, s := range out {
		if err := r.loadQualifications(ctx, s); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanStaff(row pgx.Row) (*domain.Staff, error) {
	var s domain.Staff
	if err := row.Scan(&s.ID, &s.BusinessID, &s.UserID, &s.Name, &s.Email, &s.Phone, &s.Active, &s.CreatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *StaffRepo) loadQualifications(ctx context.Context, s *domain.Staff) error {
	s.Services = []string{}
	s.Hours = map[string]domain.StaffHour{}

	rows, err := r.pool.Query(ctx, `SELECT product_id::text FROM staff_services WHERE staff_id = $1`, s.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			rows.Close()
			return err
		}
		s.Services = append(s.Services, pid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	hrows, err := r.pool.Query(ctx, `
		SELECT weekday, is_working, COALESCE(to_char(opens_at, 'HH24:MI'), ''), COALESCE(to_char(closes_at, 'HH24:MI'), '')
		FROM staff_hours WHERE staff_id = $1`, s.ID)
	if err != nil {
		return err
	}
	defer hrows.Close()
	for hrows.Next() {
		var day string
		var h domain.StaffHour
		if err := hrows.Scan(&day, &h.Working, &h.OpensAt, &h.ClosesAt); err != nil {
			return err
		}
		s.Hours[day] = h
	}
	return hrows.Err()
}

// Update applies a name/contact/active change. Only the supplied fields move.
func (r *StaffRepo) Update(ctx context.Context, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	cols := []string{"updated_at = now()"}
	args := []any{id}
	i := 2
	for k, v := range fields {
		cols = append(cols, k+" = $"+util.Itoa(i))
		args = append(args, v)
		i++
	}
	q := "UPDATE business_staff SET " + strings.Join(cols, ", ") + " WHERE id = $1"
	_, err := r.pool.Exec(ctx, q, args...)
	return err
}

func (r *StaffRepo) Delete(ctx context.Context, businessID, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM business_staff WHERE id = $1 AND business_id = $2`, id, businessID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReplaceServices sets exactly the given qualifications.
func (r *StaffRepo) ReplaceServices(ctx context.Context, staffID string, productIDs []string) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM staff_services WHERE staff_id = $1`, staffID); err != nil {
		return err
	}
	for _, pid := range productIDs {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO staff_services (staff_id, product_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			staffID, pid); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceHours sets exactly the given weekday rows. A weekday left out of the map
// inherits the business hours.
func (r *StaffRepo) ReplaceHours(ctx context.Context, staffID string, hours map[string]domain.StaffHour) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM staff_hours WHERE staff_id = $1`, staffID); err != nil {
		return err
	}
	for day, h := range hours {
		if h.Working {
			if _, err := r.pool.Exec(ctx, `
				INSERT INTO staff_hours (staff_id, weekday, is_working, opens_at, closes_at)
				VALUES ($1, $2, true, $3::time, $4::time)`,
				staffID, day, h.OpensAt, h.ClosesAt); err != nil {
				return err
			}
			continue
		}
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO staff_hours (staff_id, weekday, is_working) VALUES ($1, $2, false)`,
			staffID, day); err != nil {
			return err
		}
	}
	return nil
}

// BusyByStaff returns live bookings per staff member in [from, to). Blackouts are
// business-wide and are handled separately, so they are not repeated per person.
func (r *StaffRepo) BusyByStaff(ctx context.Context, businessID string, from, to time.Time) (map[string][][2]time.Time, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT staff_id::text, starts_at, ends_at FROM appointments
		 WHERE business_id = $1 AND staff_id IS NOT NULL
		   AND status IN ('pending', 'confirmed')
		   AND starts_at < $3 AND ends_at > $2`, businessID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][][2]time.Time{}
	for rows.Next() {
		var id string
		var s, e time.Time
		if err := rows.Scan(&id, &s, &e); err != nil {
			return nil, err
		}
		out[id] = append(out[id], [2]time.Time{s, e})
	}
	return out, rows.Err()
}
