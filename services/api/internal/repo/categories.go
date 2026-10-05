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
// ListWithCounts returns every category with the number of verified businesses
// in its whole SUBTREE, not just those assigned to it directly.
//
// The direct-count version was quietly wrong for every parent category. Businesses
// are assigned to leaf categories - "Café", not "Food & Dining" - so a parent
// whose own count was measured directly was structurally guaranteed to be 0. Not
// "stale" or "wrong for edge cases": every group, always. The landing page
// rendered all six groups as "0 businesses" while the same page's own stats
// banner said "2 Verified businesses", which is what finally made it visible.
//
// A parent that means "everything under Food & Dining" should say so, and that is
// the number a user is asking for when they see the tile. The recursion also means
// the category page and its breadcrumb no longer contradict the sidebar, which
// GetBySlug was already having to paper over by preferring this list's copy.
//
// lineage materialises one row per (descendant, ancestor) pair, self included, so
// summing the direct counts of c's descendants is the subtree total in a single
// pass and works at any depth rather than assuming exactly two levels.
func (r *CategoryRepo) ListWithCounts(ctx context.Context) ([]*domain.Category, error) {
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE lineage AS (
			SELECT c.id AS descendant, c.id AS ancestor
			  FROM categories c
			UNION ALL
			SELECT l.descendant, c.parent_id
			  FROM lineage l
			  JOIN categories c ON c.id = l.ancestor
			 WHERE c.parent_id IS NOT NULL
		),
		direct AS (
			SELECT c.id,
			       (SELECT count(*) FROM businesses b
			         WHERE b.category_id = c.id
			           AND b.status = 'verified'
			           AND b.deleted_at IS NULL) AS n
			  FROM categories c
		)
		SELECT `+categoryCols+`,
			COALESCE((
				SELECT sum(d.n)
				  FROM lineage l
				  JOIN direct d ON d.id = l.descendant
				 WHERE l.ancestor = c.id
			), 0) AS count
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

// BusinessCount counts every business attached to a category, retired ones
// included. It is the guard behind "this category has businesses, pass move_to",
// so it answers a REFERENTIAL question — does any row still point here — not a
// visibility one.
//
// The `deleted_at` filter that used to be here is what migration 0032 turned
// into a bug. It was inert while nothing wrote the column, so the guard worked
// by accident. Once Close started setting `deleted_at`, a category whose only
// listings were closed read as empty: the guard passed, MoveBusinesses was never
// called, and the subsequent DELETE hit businesses_category_id_fkey — which is
// NO ACTION — raising a 23503 where the user should have seen the validation
// message telling them to reassign the listings.
//
// A retired row still holds category_id, so it still blocks the delete.
func (r *CategoryRepo) BusinessCount(ctx context.Context, id string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM businesses WHERE category_id = $1`, id).Scan(&n)
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
//
// Retired listings are moved too, and deliberately. They are exactly the rows
// that would otherwise be orphaned: a closed listing keeps its category_id, so
// leaving it behind means the category still has a referrer and the DELETE that
// follows this call fails with 23503. Reassigning everything is also the only
// way "no orphans" can be true, so the filter is gone for the same reason
// BusinessCount lost it.
func (r *CategoryRepo) MoveBusinesses(ctx context.Context, fromID, toID string) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE businesses SET category_id = $2, updated_at = now() WHERE category_id = $1`,
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
