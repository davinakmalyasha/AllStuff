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
		cols = append(cols, k+" = $"+itoa(len(args)))
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

func (r *ProductRepo) SKUTaken(ctx context.Context, sku, businessID, excludeID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM product_variants v
			JOIN products p ON p.id = v.product_id
			WHERE v.sku = $1 AND p.business_id = $2 AND p.deleted_at IS NULL
			  AND (NULLIF($3, '') IS NULL OR v.id <> NULLIF($3, '')::uuid))`,
		sku, businessID, excludeID).Scan(&exists)
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
func (r *ProductRepo) Duplicate(ctx context.Context, tx Tx, productID, newID string) error {
	pr, err := r.GetByID(ctx, productID)
	if err != nil || pr == nil {
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
	options, err := r.ListOptions(ctx, productID)
	if err != nil {
		return err
	}
	variants, err := r.ListVariants(ctx, productID)
	if err != nil {
		return err
	}
	copiedOptions := make([]*domain.ProductOption, 0, len(options))
	for _, o := range options {
		copiedOptions = append(copiedOptions, &domain.ProductOption{
			ID: newUUID(), ProductID: newID, Name: o.Name, Values: o.Values,
		})
	}
	copiedVariants := make([]*domain.ProductVariant, 0, len(variants))
	for _, v := range variants {
		copiedVariants = append(copiedVariants, &domain.ProductVariant{
			ID: newUUID(), ProductID: newID, Name: v.Name, SKU: v.SKU+"-copy",
			Options: v.Options, Price: v.Price, Currency: v.Currency,
			StockQty: v.StockQty, InStock: v.InStock, ImageID: v.ImageID,
		})
	}
	return r.ReplaceOptionsVariants(ctx, tx, newID, copiedOptions, copiedVariants)
}

func newUUID() string { return util.NewUUID() }
