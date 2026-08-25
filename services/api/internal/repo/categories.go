package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/util"
)

type CategoryRepo struct{ pool pooler }

const categoryCols = `c.id, c.parent_id, c.name, c.slug, c.icon, c.description, c.sort_order`

func scanCategory(row pgx.Row) (*domain.Category, error) {
	var c domain.Category
	if err := row.Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Icon, &c.Description, &c.SortOrder); err != nil {
		return nil, err
	}
	return &c, nil
}

// ListWithCounts returns the full tree flattened, each with verified-business counts.
func (r *CategoryRepo) ListWithCounts(ctx context.Context) ([]*domain.Category, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+categoryCols+`,
			(SELECT count(*) FROM businesses b
			 WHERE b.category_id = c.id AND b.status = 'verified' AND b.deleted_at IS NULL) AS count
		FROM categories c
		ORDER BY c.sort_order, c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Category
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Icon, &c.Description,
			&c.SortOrder, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *CategoryRepo) GetBySlug(ctx context.Context, slug string) (*domain.Category, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+categoryCols+` FROM categories c WHERE c.slug = $1`, slug)
	c, err := scanCategory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (r *CategoryRepo) GetByID(ctx context.Context, id string) (*domain.Category, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+categoryCols+` FROM categories c WHERE c.id = $1`, id)
	c, err := scanCategory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (r *CategoryRepo) SlugTaken(ctx context.Context, slug, excludeID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM categories WHERE slug = $1
			AND (NULLIF($2, '') IS NULL OR id <> NULLIF($2, '')::uuid))`,
		slug, excludeID).Scan(&exists)
	return exists, err
}

func (r *CategoryRepo) HasChildren(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM categories WHERE parent_id = $1)`, id).Scan(&exists)
	return exists, err
}

func (r *CategoryRepo) BusinessCount(ctx context.Context, id string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM businesses WHERE category_id = $1 AND deleted_at IS NULL`, id).Scan(&n)
	return n, err
}

func (r *CategoryRepo) Create(ctx context.Context, c *domain.Category) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO categories (id, parent_id, name, slug, icon, description, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		c.ID, c.ParentID, c.Name, c.Slug, c.Icon, c.Description, c.SortOrder)
	return err
}

// allowedCategoryFields: whitelist for PATCH-style updates (SQL-injection
// guard consistent with the other repo update builders).
var allowedCategoryFields = map[string]bool{
	"parent_id": true, "name": true, "slug": true, "icon": true,
	"description": true, "sort_order": true,
}

func (r *CategoryRepo) Update(ctx context.Context, id string, fields map[string]any) error {
	cols := []string{"updated_at = now()"}
	args := []any{id}
	for k, v := range fields {
		if !allowedCategoryFields[k] {
			return fmt.Errorf("field not allowed: %s", k)
		}
		args = append(args, v)
		cols = append(cols, k+" = $"+util.Itoa(len(args)))
	}
	_, err := r.pool.Exec(ctx,
		"UPDATE categories SET "+joinComma(cols)+" WHERE id = $1", args...)
	return err
}

func (r *CategoryRepo) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
	return err
}

// MoveBusinesses reassigns businesses to another category (PRD §5.8.3: no orphans).
func (r *CategoryRepo) MoveBusinesses(ctx context.Context, fromID, toID string) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE businesses SET category_id = $2, updated_at = now() WHERE category_id = $1 AND deleted_at IS NULL`,
		fromID, toID)
	return tag.RowsAffected(), err
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
