package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/util"
)

// Tx is a pgx transaction (shared across repos).
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type ProductRepo struct{ pool pooler }

const productCols = `p.id, p.business_id, p.type, p.name, p.description, p.currency, p.base_price,
	p.call_for_price, p.cover_image_id, p.image_ids, p.tags, p.is_available, p.is_featured,
	p.featured_order, p.sort_order, p.is_published, p.badge, p.seo_title, p.deleted_at, p.created_at, p.updated_at`

func scanProduct(row pgx.Row) (*domain.Product, error) {
	var pr domain.Product
	if err := row.Scan(&pr.ID, &pr.BusinessID, &pr.Type, &pr.Name, &pr.Description, &pr.Currency,
		&pr.BasePrice, &pr.CallForPrice, &pr.CoverImageID, &pr.ImageIDs, &pr.Tags, &pr.IsAvailable,
		&pr.IsFeatured, &pr.FeaturedOrder, &pr.SortOrder, &pr.IsPublished, &pr.Badge, &pr.SeoTitle,
		&pr.DeletedAt, &pr.CreatedAt, &pr.UpdatedAt); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (r *ProductRepo) Create(ctx context.Context, p *domain.Product) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO products (id, business_id, type, name, description, currency, base_price,
			call_for_price, tags, is_published)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		p.ID, p.BusinessID, p.Type, p.Name, p.Description, p.Currency, p.BasePrice,
		p.CallForPrice, p.Tags, p.IsPublished)
	return err
}

var allowedProductFields = map[string]bool{
	"name": true, "description": true, "currency": true, "base_price": true,
	"call_for_price": true, "cover_image_id": true, "image_ids": true, "tags": true,
	"is_available": true, "is_featured": true, "featured_order": true, "sort_order": true,
	"badge": true, "seo_title": true,
}

func (r *ProductRepo) Update(ctx context.Context, id string, fields map[string]any) error {
	cols := []string{"updated_at = now()"}
	args := []any{id}
	for k, v := range fields {
		if !allowedProductFields[k] {
			return fmt.Errorf("field not allowed: %s", k)
		}
		args = append(args, v)
		cols = append(cols, k+" = $"+util.Itoa(len(args)))
	}
	if len(cols) == 1 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		"UPDATE products SET "+strings.Join(cols, ", ")+" WHERE id = $1", args...)
	return err
}

func (r *ProductRepo) GetByID(ctx context.Context, id string) (*domain.Product, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+productCols+` FROM products p WHERE p.id = $1`, id)
	pr, err := scanProduct(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return pr, err
}

func (r *ProductRepo) ListByBusiness(ctx context.Context, businessID string) ([]*domain.Product, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+productCols+` FROM products p
		WHERE p.business_id = $1 AND p.deleted_at IS NULL
		ORDER BY p.sort_order, p.created_at DESC`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Product
	for rows.Next() {
		pr, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// ListPublished returns only live products for the public page.
func (r *ProductRepo) ListPublished(ctx context.Context, businessID string) ([]*domain.Product, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+productCols+` FROM products p
		WHERE p.business_id = $1 AND p.deleted_at IS NULL AND p.is_published = true AND p.is_available = true
		ORDER BY p.is_featured DESC, p.sort_order, p.created_at DESC`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Product
	for rows.Next() {
		pr, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// CountByBusinessAndType counts live products of one type, for the plan's
// catalogue cap.
//
// Counted per type rather than in total because products and services are sold
// against the same cap but a salon and a hardware shop have very different
// mixes, and a business that fills its product allowance should still be able to
// list its services.
//
// Soft-deleted rows are excluded for the same reason they are excluded from
// ListByBusiness: a deleted product no longer occupies a slot, and charging for
// one would make the cap impossible to get under without a support request.
// excludeID lets Duplicate skip the row being copied so a business already at
// the cap can still duplicate a product within it.
func (r *ProductRepo) CountByBusinessAndType(ctx context.Context, businessID string, ptype domain.ProductType, excludeID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM products
		 WHERE business_id = $1 AND deleted_at IS NULL AND type = $2
		   AND ($3 = '' OR id <> $3::uuid)`,
		businessID, ptype, excludeID).Scan(&n)
	return n, err
}

// CountByBusiness counts all live products for a business regardless of type.
func (r *ProductRepo) CountByBusiness(ctx context.Context, businessID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM products WHERE business_id = $1 AND deleted_at IS NULL`, businessID).Scan(&n)
	return n, err
}

func (r *ProductRepo) SoftDelete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE products SET deleted_at = now(), is_published = false WHERE id = $1`, id)
	return err
}

func (r *ProductRepo) SetPublished(ctx context.Context, id string, published bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE products SET is_published = $2, updated_at = now() WHERE id = $1`, id, published)
	return err
}

// SKUTaken reports whether a SKU is already in use, across the whole catalogue.
//
// The scope is GLOBAL because `product_variants.sku` carries a global UNIQUE
// constraint, and the check must match the constraint exactly or the two
// disagree. The previous per-business check let two owners both pass validation
// for "SKU-1", and the second insert then failed on the constraint with a 23505
// that nothing mapped to 409 — a 500 for an action the owner's own UI had just
// validated.
//
// `businessID` is retained for call-site readability and is deliberately NOT
// part of the scope: a per-business "unique" partial index is not expressible,
// because a partial index predicate cannot contain a subquery joining products,
// and denormalizing business_id onto product_variants purely to widen
// uniqueness would be a worse trade than the global constraint.
//
// Soft-deleted products are excluded so a retired SKU can be reused, which is
// what an owner expects when re-adding a discontinued product.
func (r *ProductRepo) SKUTaken(ctx context.Context, sku, businessID, excludeID string) (bool, error) {
	_ = businessID // scope is global; see the doc comment
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM product_variants v
			JOIN products p ON p.id = v.product_id
			WHERE v.sku = $1 AND p.deleted_at IS NULL
			  AND (NULLIF($2, '') IS NULL OR v.id <> NULLIF($2, '')::uuid))`,
		sku, excludeID).Scan(&exists)
	return exists, err
}

// ---- options & variants ----

func (r *ProductRepo) ListOptions(ctx context.Context, productID string) ([]*domain.ProductOption, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, product_id, name, values, sort_order FROM product_options
		WHERE product_id = $1 ORDER BY sort_order`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ProductOption
	for rows.Next() {
		var o domain.ProductOption
		if err := rows.Scan(&o.ID, &o.ProductID, &o.Name, &o.Values, &o.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, &o)
	}
	return out, rows.Err()
}

// OptionsForProducts loads options for many products in ONE query.
//
// Added to kill a 2N+1 pattern: ListPublished/ListByBusiness called
// ListOptions then ListVariants per product, so a business page with 50
// products issued 105 round trips, and /compare repeats that per column.
func (r *ProductRepo) OptionsForProducts(ctx context.Context, productIDs []string) (map[string][]*domain.ProductOption, error) {
	out := map[string][]*domain.ProductOption{}
	if len(productIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, product_id, name, values, sort_order FROM product_options
		WHERE product_id = ANY($1::uuid[]) ORDER BY product_id, sort_order`, productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o domain.ProductOption
		if err := rows.Scan(&o.ID, &o.ProductID, &o.Name, &o.Values, &o.SortOrder); err != nil {
			return nil, err
		}
		out[o.ProductID] = append(out[o.ProductID], &o)
	}
	return out, rows.Err()
}

// VariantsForProducts loads variants for many products in ONE query.
// See OptionsForProducts for why.
func (r *ProductRepo) VariantsForProducts(ctx context.Context, productIDs []string) (map[string][]*domain.ProductVariant, error) {
	out := map[string][]*domain.ProductVariant{}
	if len(productIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, product_id, name, sku, options, price, currency, stock_qty, in_stock, image_id, sort_order
		FROM product_variants WHERE product_id = ANY($1::uuid[]) ORDER BY product_id, sort_order`, productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v domain.ProductVariant
		if err := rows.Scan(&v.ID, &v.ProductID, &v.Name, &v.SKU, &v.Options, &v.Price,
			&v.Currency, &v.StockQty, &v.InStock, &v.ImageID, &v.SortOrder); err != nil {
			return nil, err
		}
		out[v.ProductID] = append(out[v.ProductID], &v)
	}
	return out, rows.Err()
}

func (r *ProductRepo) ListVariants(ctx context.Context, productID string) ([]*domain.ProductVariant, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, product_id, name, sku, options, price, currency, stock_qty, in_stock, image_id, sort_order
		FROM product_variants WHERE product_id = $1 ORDER BY sort_order`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ProductVariant
	for rows.Next() {
		var v domain.ProductVariant
		if err := rows.Scan(&v.ID, &v.ProductID, &v.Name, &v.SKU, &v.Options, &v.Price,
			&v.Currency, &v.StockQty, &v.InStock, &v.ImageID, &v.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}

// ReplaceOptionsVariants rewrites options + variants atomically (PRD §5.4.3).
func (r *ProductRepo) ReplaceOptionsVariants(ctx context.Context, tx Tx, productID string,
	options []*domain.ProductOption, variants []*domain.ProductVariant) error {

	if _, err := tx.Exec(ctx, `DELETE FROM product_options WHERE product_id = $1`, productID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_variants WHERE product_id = $1`, productID); err != nil {
		return err
	}
	for i, o := range options {
		o.SortOrder = i
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_options (id, product_id, name, values, sort_order)
			VALUES ($1, $2, $3, $4, $5)`, o.ID, productID, o.Name, o.Values, o.SortOrder); err != nil {
			return err
		}
	}
	for i, v := range variants {
		v.SortOrder = i
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_variants (id, product_id, name, sku, options, price, currency, stock_qty, in_stock, image_id, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			v.ID, productID, v.Name, v.SKU, v.Options, v.Price, v.Currency, v.StockQty, v.InStock, v.ImageID, v.SortOrder); err != nil {
			return err
		}
	}
	return nil
}

// Duplicate copies a product with its options and variants (PRD §5.4.3).
// Every read runs on the caller's transaction, not the pool: pool reads
// outside the tx see a different snapshot and mask connection errors as 404s.
func (r *ProductRepo) Duplicate(ctx context.Context, tx pgx.Tx, productID, newID string) error {
	tr := &ProductRepo{pool: tx}
	pr, err := tr.GetByID(ctx, productID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	}
	if pr == nil {
		return domain.ErrNotFound
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO products (id, business_id, type, name, description, currency, base_price,
			call_for_price, cover_image_id, image_ids, tags, is_available, is_featured, featured_order,
			sort_order, is_published, badge, seo_title)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, false, $16, $17)`,
		newID, pr.BusinessID, pr.Type, pr.Name+" (copy)", pr.Description, pr.Currency, pr.BasePrice,
		pr.CallForPrice, pr.CoverImageID, pr.ImageIDs, pr.Tags, pr.IsAvailable, pr.IsFeatured,
		pr.FeaturedOrder, 0, pr.Badge, pr.SeoTitle)
	if err != nil {
		return err
	}
	options, err := tr.ListOptions(ctx, productID)
	if err != nil {
		return err
	}
	variants, err := tr.ListVariants(ctx, productID)
	if err != nil {
		return err
	}
	copiedOptions := make([]*domain.ProductOption, 0, len(options))
	for _, o := range options {
		copiedOptions = append(copiedOptions, &domain.ProductOption{
			ID: util.NewUUID(), ProductID: newID, Name: o.Name, Values: o.Values,
		})
	}
	copiedVariants := make([]*domain.ProductVariant, 0, len(variants))
	for _, v := range variants {
		sku, err := tr.uniqueCopySKU(ctx, v.SKU, pr.BusinessID)
		if err != nil {
			return err
		}
		copiedVariants = append(copiedVariants, &domain.ProductVariant{
			ID: util.NewUUID(), ProductID: newID, Name: v.Name, SKU: sku,
			Options: v.Options, Price: v.Price, Currency: v.Currency,
			StockQty: v.StockQty, InStock: v.InStock, ImageID: v.ImageID,
		})
	}
	return tr.ReplaceOptionsVariants(ctx, tx, newID, copiedOptions, copiedVariants)
}

// uniqueCopySKU derives a collision-free SKU for a duplicate. The old
// deterministic "SKU-copy" suffix hit the UNIQUE constraint on the second
// copy (or when a sibling was already copied).
func (r *ProductRepo) uniqueCopySKU(ctx context.Context, baseSKU, businessID string) (string, error) {
	candidate := baseSKU + "-copy"
	for i := 0; i < 20; i++ {
		taken, err := r.SKUTaken(ctx, candidate, businessID, "")
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
		candidate = baseSKU + "-copy-" + util.NewUUID()[:6]
	}
	taken, err := r.SKUTaken(ctx, candidate, businessID, "")
	if err != nil {
		return "", err
	}
	if taken {
		return "", domain.ErrConflict
	}
	return candidate, nil
}
