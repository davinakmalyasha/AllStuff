package repo

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/util"
)

type LocationRepo struct{ pool pooler }

const locationCols = `l.id, l.business_id, l.name, l.address, l.city, l.country, l.lat, l.lng,
	l.timezone, l.hours, l.special_hours, coalesce(l.phone, ''), l.is_primary, l.created_at`

func scanLocation(row pgx.Row) (*domain.Location, error) {
	var l domain.Location
	if err := row.Scan(&l.ID, &l.BusinessID, &l.Name, &l.Address, &l.City, &l.Country,
		&l.Lat, &l.Lng, &l.Timezone, &l.Hours, &l.SpecialHours, &l.Phone, &l.IsPrimary,
		&l.CreatedAt); err != nil {
		return nil, err
	}
	return &l, nil
}

func (r *LocationRepo) List(ctx context.Context, businessID string) ([]*domain.Location, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+locationCols+` FROM business_locations l WHERE l.business_id = $1
		 ORDER BY l.is_primary DESC, l.created_at ASC`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Location
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *LocationRepo) Get(ctx context.Context, id string) (*domain.Location, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+locationCols+` FROM business_locations l WHERE l.id = $1`, id)
	l, err := scanLocation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

func (r *LocationRepo) Primary(ctx context.Context, businessID string) (*domain.Location, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+locationCols+` FROM business_locations l WHERE l.business_id = $1 AND l.is_primary LIMIT 1`, businessID)
	l, err := scanLocation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

func (r *LocationRepo) Create(ctx context.Context, l *domain.Location) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO business_locations (id, business_id, name, address, city, country, lat, lng,
			timezone, hours, special_hours, phone, is_primary)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),$13)`,
		l.ID, l.BusinessID, l.Name, l.Address, l.City, l.Country, l.Lat, l.Lng,
		l.Timezone, l.Hours, l.SpecialHours, l.Phone, l.IsPrimary)
	return err
}

func (r *LocationRepo) Update(ctx context.Context, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	cols := []string{"updated_at = now()"}
	args := []any{id}
	i := 2
	for _, k := range []string{"name", "address", "city", "country", "lat", "lng", "timezone", "hours", "special_hours", "phone"} {
		v, ok := fields[k]
		if !ok {
			continue
		}
		cols = append(cols, k+" = $"+util.Itoa(i))
		args = append(args, v)
		i++
	}
	_, err := r.pool.Exec(ctx, "UPDATE business_locations SET "+strings.Join(cols, ", ")+" WHERE id = $1", args...)
	return err
}

func (r *LocationRepo) Delete(ctx context.Context, businessID, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM business_locations WHERE id = $1 AND business_id = $2`, id, businessID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *LocationRepo) Count(ctx context.Context, businessID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM business_locations WHERE business_id = $1`, businessID).Scan(&n)
	return n, err
}

// SetPrimary moves the primary flag, which is a partial unique index, so the old
// one has to be cleared first.
func (r *LocationRepo) SetPrimary(ctx context.Context, businessID, id string) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE business_locations SET is_primary = false WHERE business_id = $1 AND is_primary`, businessID); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE business_locations SET is_primary = true WHERE id = $1 AND business_id = $2`, id, businessID)
	return err
}

// ServiceHours is the per-service override for one place: weekday -> window. An
// empty map means the service follows the location's own hours.
func (r *LocationRepo) ServiceHours(ctx context.Context, locationID, productID string) (map[string]domain.DayHours, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT weekday, is_working, COALESCE(to_char(opens_at, 'HH24:MI'), ''), COALESCE(to_char(closes_at, 'HH24:MI'), '')
		FROM location_service_hours WHERE location_id = $1 AND product_id = $2`, locationID, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.DayHours{}
	for rows.Next() {
		var day string
		var h domain.DayHours
		if err := rows.Scan(&day, &h.Working, &h.OpensAt, &h.ClosesAt); err != nil {
			return nil, err
		}
		out[day] = h
	}
	return out, rows.Err()
}

func (r *LocationRepo) ReplaceServiceHours(ctx context.Context, locationID, productID string, hours map[string]domain.DayHours) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM location_service_hours WHERE location_id = $1 AND product_id = $2`, locationID, productID); err != nil {
		return err
	}
	for day, h := range hours {
		// The times are stored even on a day that is switched off: a dashboard
		// toggle keeps them, and dropping them would make "closed" and "never
		// configured" indistinguishable the next time the day is switched back on.
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO location_service_hours (location_id, product_id, weekday, is_working, opens_at, closes_at)
			VALUES ($1, $2, $3, $4, NULLIF($5, '')::time, NULLIF($6, '')::time)`,
			locationID, productID, day, h.Working, h.OpensAt, h.ClosesAt); err != nil {
			return err
		}
	}
	return nil
}
