package service

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Products — catalog management with variants (PRD §5.4.3, §8.3).
type Products struct {
	repos    *repo.Repos
	notifier *Notifier
}

func NewProducts(repos *repo.Repos, notifier *Notifier) *Products {
	return &Products{repos: repos, notifier: notifier}
}

type ProductInput struct {
	Type          *string   `json:"type"`
	Name          *string   `json:"name"`
	Description   *string   `json:"description"`
	Currency      *string   `json:"currency"`
	BasePrice     *float64  `json:"base_price"`
	CallForPrice  *bool     `json:"call_for_price"`
	CoverImageID  *string   `json:"cover_image_id"`
	ImageIDs      *[]string `json:"image_ids"`
	Tags          *[]string `json:"tags"`
	IsAvailable   *bool     `json:"is_available"`
	IsFeatured    *bool     `json:"is_featured"`
	Badge         *string   `json:"badge"`
	SeoTitle      *string   `json:"seo_title"`
	SortOrder     *int      `json:"sort_order"`
}

// Create starts a product in draft state.
func (s *Products) Create(ctx context.Context, ownerID, businessID string, in ProductInput) (*domain.Product, error) {
	if err := s.own(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	name := "Untitled product"
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		name = strings.TrimSpace(*in.Name)
	}
	if n := len([]rune(name)); n < 2 || n > 100 {
		return nil, domain.ErrValidation.WithField("name", "Name must be 2–100 characters.")
	}
	ptype := domain.ProductTypeProduct
	if in.Type != nil && *in.Type == "service" {
		ptype = domain.ProductTypeService
	}
	currency := "USD"
	if in.Currency != nil && len(*in.Currency) == 3 {
		currency = strings.ToUpper(*in.Currency)
	}
	var desc *string
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		desc = &d
	}
	p := &domain.Product{
		ID:           util.NewUUID(),
		BusinessID:   businessID,
		Type:         ptype,
		Name:         name,
		Description:  desc,
		Currency:     currency,
		BasePrice:    in.BasePrice,
		CallForPrice: in.CallForPrice != nil && *in.CallForPrice,
		Tags:         []string{},
	}
	if err := s.repos.Products.Create(ctx, p); err != nil {
		return nil, err
	}
	return s.repos.Products.GetByID(ctx, p.ID)
}

// Update applies scalar fields (PRD §8.3 validation).
func (s *Products) Update(ctx context.Context, ownerID, id string, in ProductInput) (*domain.Product, error) {
	p, err := s.ownedProduct(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if n := len([]rune(name)); n < 2 || n > 100 {
			return nil, domain.ErrValidation.WithField("name", "Name must be 2–100 characters.")
		}
		fields["name"] = name
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		fields["description"] = d
	}
	if in.Currency != nil {
		cur := strings.ToUpper(strings.TrimSpace(*in.Currency))
		if len(cur) != 3 {
			return nil, domain.ErrValidation.WithField("currency", "Currency must be a 3-letter code.")
		}
		fields["currency"] = cur
	}
	if in.BasePrice != nil {
		if *in.BasePrice < 0 {
			return nil, domain.ErrValidation.WithField("base_price", "Price must be ≥ 0.")
		}
		fields["base_price"] = *in.BasePrice
	}
	if in.CallForPrice != nil {
		if *in.CallForPrice && p.Type != domain.ProductTypeService && len(p.Variants) == 0 {
			return nil, domain.ErrValidation.WithField("call_for_price", "Call for price is only valid for services (PRD §8.3).")
		}
		fields["call_for_price"] = *in.CallForPrice
	}
	if in.CoverImageID != nil {
		fields["cover_image_id"] = *in.CoverImageID
	}
	if in.ImageIDs != nil {
		if len(*in.ImageIDs) > 10 {
			return nil, domain.ErrValidation.WithField("image_ids", "Max 10 images.")
		}
		fields["image_ids"] = *in.ImageIDs
	}
	if in.Tags != nil {
		if len(*in.Tags) > 5 {
			return nil, domain.ErrValidation.WithField("tags", "Max 5 tags.")
		}
		fields["tags"] = *in.Tags
	}
	if in.IsAvailable != nil {
		fields["is_available"] = *in.IsAvailable
	}
	if in.IsFeatured != nil {
		fields["is_featured"] = *in.IsFeatured
	}
	if in.Badge != nil {
		if *in.Badge != "none" && *in.Badge != "new" && *in.Badge != "popular" {
			return nil, domain.ErrValidation.WithField("badge", "Badge must be none, new, or popular.")
		}
		fields["badge"] = *in.Badge
	}
	if in.SeoTitle != nil {
		fields["seo_title"] = strings.TrimSpace(*in.SeoTitle)
	}
	if in.SortOrder != nil {
		fields["sort_order"] = *in.SortOrder
	}
	if len(fields) == 0 {
		return nil, domain.ErrValidation.WithField("_", "Nothing to update.")
	}
	if err := s.repos.Products.Update(ctx, id, fields); err != nil {
		return nil, err
	}
	return s.repos.Products.GetByID(ctx, id)
}

type VariantInput struct {
	ID       *string        `json:"id"`
	Name     string         `json:"name"`
	SKU      string         `json:"sku"`
	Options  map[string]any `json:"options"`
	Price    *float64       `json:"price"`
	Currency string         `json:"currency"`
	StockQty *int           `json:"stock_qty"`
	InStock  bool           `json:"in_stock"`
	ImageID  *string        `json:"image_id"`
}

type OptionInput struct {
	ID     *string  `json:"id"`
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type ReplaceVariantsInput struct {
	Options  []OptionInput  `json:"options"`
	Variants []VariantInput `json:"variants"`
}

// ReplaceVariants rewrites option groups + variant combinations atomically.
func (s *Products) ReplaceVariants(ctx context.Context, ownerID, productID string, in ReplaceVariantsInput) (*domain.Product, error) {
	p, err := s.ownedProduct(ctx, ownerID, productID)
	if err != nil {
		return nil, err
	}
	if len(in.Options) > 3 {
		return nil, domain.ErrValidation.WithField("options", "Max 3 option groups (PRD §8.3).")
	}
	if len(in.Variants) > 200 {
		return nil, domain.ErrValidation.WithField("variants", "Max 200 variants.")
	}
	for i := range in.Options {
		if len(in.Options[i].Values) == 0 || len(in.Options[i].Values) > 20 {
			return nil, domain.ErrValidation.WithField("options", "Each group needs 1–20 values.")
		}
	}
	for i := range in.Variants {
		v := &in.Variants[i]
		v.SKU = strings.TrimSpace(v.SKU)
		if v.SKU == "" {
			return nil, domain.ErrValidation.WithField("variants", "Every variant needs a SKU.")
		}
		if v.Price != nil && *v.Price < 0 {
			return nil, domain.ErrValidation.WithField("variants", "Variant price must be ≥ 0.")
		}
		if v.StockQty != nil && *v.StockQty < 0 {
			return nil, domain.ErrValidation.WithField("variants", "Stock must be ≥ 0.")
		}
		taken, err := s.repos.Products.SKUTaken(ctx, v.SKU, p.BusinessID, derefID(v.ID))
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, domain.ErrValidation.WithField("variants", "SKU "+v.SKU+" is already in use.")
		}
	}

	options := make([]*domain.ProductOption, 0, len(in.Options))
	for i := range in.Options {
		o := &in.Options[i]
		options = append(options, &domain.ProductOption{
			ID:     derefOrNew(o.ID),
			Name:   strings.TrimSpace(o.Name),
			Values: o.Values,
		})
	}
	variants := make([]*domain.ProductVariant, 0, len(in.Variants))
	for i := range in.Variants {
		v := &in.Variants[i]
		cur := v.Currency
		if cur == "" {
			cur = p.Currency
		}
		variants = append(variants, &domain.ProductVariant{
			ID:       derefOrNew(v.ID),
			Name:     strings.TrimSpace(v.Name),
			SKU:      v.SKU,
			Options:  v.Options,
			Price:    v.Price,
			Currency: cur,
			StockQty: v.StockQty,
			InStock:  v.InStock,
			ImageID:  v.ImageID,
		})
	}

	// Restock fan-out: variants that were out of stock and now have stock.
	before, _ := s.repos.Products.ListVariants(ctx, productID)
	beforeStock := map[string]bool{}
	for _, v := range before {
		beforeStock[v.ID] = v.InStock && v.StockQty != nil && *v.StockQty > 0
	}

	tx, err := s.repos.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.repos.Products.ReplaceOptionsVariants(ctx, tx, productID, options, variants); err != nil {
		return nil, err
	}

	// After the commit, notify anyone waiting on restocked variants.
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.notifyRestock(ctx, productID, p.BusinessID, beforeStock)
	return s.ownedProduct(ctx, ownerID, productID)
}

// notifyRestock fans out back_in_stock alerts (PRD §5.7) for variants that
// went out-of-stock → in-stock in this edit.
func (s *Products) notifyRestock(ctx context.Context, productID, businessID string, before map[string]bool) {
	after, err := s.repos.Products.ListVariants(ctx, productID)
	if err != nil {
		return
	}
	restocked := false
	for _, v := range after {
		inStock := v.InStock && v.StockQty != nil && *v.StockQty > 0
		if inStock && !before[v.ID] {
			restocked = true
			break
		}
	}
	if !restocked {
		return
	}
	rows, err := s.repos.Query(ctx, `SELECT user_id FROM product_stock_alerts WHERE product_id = $1`, productID)
	if err != nil {
		return
	}
	var users []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return
		}
		users = append(users, uid)
	}
	rows.Close()
	for _, uid := range users {
		s.notifier.Create(ctx, uid, "back_in_stock", map[string]any{
			"product_id": productID, "business_id": businessID,
		})
	}
	// Alerts are one-shot: clear after notifying.
	_, _ = s.repos.Exec(ctx, `DELETE FROM product_stock_alerts WHERE product_id = $1`, productID)
}

// ToggleStockAlert subscribes/unsubscribes a user to restock alerts.
func (s *Products) ToggleStockAlert(ctx context.Context, userID, productID string, on bool) error {
	if on {
		_, err := s.repos.Exec(ctx, `
			INSERT INTO product_stock_alerts (id, user_id, product_id) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, product_id) DO NOTHING`, newUUID(), userID, productID)
		return err
	}
	_, err := s.repos.Exec(ctx, `
		DELETE FROM product_stock_alerts WHERE user_id = $1 AND product_id = $2`, userID, productID)
	return err
}

// StockAlerted reports whether the user is subscribed to a product.
func (s *Products) StockAlerted(ctx context.Context, userID, productID string) (bool, error) {
	var one int
	err := s.repos.QueryRow(ctx, `
		SELECT 1 FROM product_stock_alerts WHERE user_id = $1 AND product_id = $2`,
		userID, productID).Scan(&one)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (s *Products) Publish(ctx context.Context, ownerID, productID string, published bool) (*domain.Product, error) {
	p, err := s.ownedProduct(ctx, ownerID, productID)
	if err != nil {
		return nil, err
	}
	if published {
		hasImage := p.CoverImageID != nil || len(p.ImageIDs) > 0
		if !hasImage {
			return nil, domain.ErrValidation.WithField("image_ids", "At least 1 image is required to publish (PRD §8.3).")
		}
		if p.BasePrice == nil && len(p.Variants) == 0 && !p.CallForPrice {
			return nil, domain.ErrValidation.WithField("base_price", "Set a price (or Call for price for services) before publishing.")
		}
	}
	if err := s.repos.Products.SetPublished(ctx, productID, published); err != nil {
		return nil, err
	}
	return s.repos.Products.GetByID(ctx, productID)
}

func (s *Products) Delete(ctx context.Context, ownerID, productID string) error {
	p, err := s.ownedProduct(ctx, ownerID, productID)
	if err != nil {
		return err
	}
	_ = p
	return s.repos.Products.SoftDelete(ctx, productID)
}

func (s *Products) Duplicate(ctx context.Context, ownerID, productID string) (*domain.Product, error) {
	p, err := s.ownedProduct(ctx, ownerID, productID)
	if err != nil {
		return nil, err
	}
	newID := util.NewUUID()
	tx, err := s.repos.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.repos.Products.Duplicate(ctx, tx, p.ID, newID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.repos.Products.GetByID(ctx, newID)
}

// List returns all products (with options/variants) for the owner dashboard.
func (s *Products) List(ctx context.Context, ownerID, businessID string) ([]*domain.Product, error) {
	if err := s.own(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	list, err := s.repos.Products.ListByBusiness(ctx, businessID)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		options, err := s.repos.Products.ListOptions(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		variants, err := s.repos.Products.ListVariants(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		p.Options = options
		p.Variants = variants
	}
	return list, nil
}

// ListPublished returns live products for the public page.
func (s *Products) ListPublished(ctx context.Context, businessID string) ([]*domain.Product, error) {
	list, err := s.repos.Products.ListPublished(ctx, businessID)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		variants, err := s.repos.Products.ListVariants(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		p.Variants = variants
		opts, err := s.repos.Products.ListOptions(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		p.Options = opts
	}
	return list, nil
}

func (s *Products) ownedProduct(ctx context.Context, ownerID, productID string) (*domain.Product, error) {
	p, err := s.repos.Products.GetByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.DeletedAt != nil {
		return nil, domain.ErrNotFound
	}
	if err := s.own(ctx, ownerID, p.BusinessID); err != nil {
		return nil, err
	}
	options, err := s.repos.Products.ListOptions(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	variants, err := s.repos.Products.ListVariants(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	p.Options = options
	p.Variants = variants
	return p, nil
}

func (s *Products) own(ctx context.Context, ownerID, businessID string) error {
	b, err := s.repos.Businesses.GetByID(ctx, businessID)
	if err != nil {
		return err
	}
	if b == nil {
		return domain.ErrNotFound
	}
	if b.OwnerID != ownerID {
		return domain.ErrForbidden
	}
	return nil
}

func derefOrNew(id *string) string {
	if id != nil && *id != "" {
		return *id
	}
	return util.NewUUID()
}

func derefID(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}

var _ = errors.Is
var _ = pgx.ErrNoRows
