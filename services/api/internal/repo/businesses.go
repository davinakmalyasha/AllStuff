package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
)

type BusinessRepo struct{ pool pooler }

const BusinessCols = `b.id, b.owner_id, b.name, b.slug, b.tagline, b.description, COALESCE(b.category_id::text, '') AS category_id,
	b.status, b.rejection_reason, b.logo_url, b.cover_url, b.gallery, b.price_level, b.currency,
	b.address, b.lat, b.lng, b.city, b.country, b.timezone, b.hours, b.special_hours, b.contact, b.amenities, b.tags, b.founded_year,
	b.is_featured, b.last_published_at, b.published_snapshot, b.verification_level, b.verified_at, b.created_at, b.updated_at`

const BusinessCounts = `,
	(SELECT avg(r.rating)::float8 FROM reviews r WHERE r.business_id = b.id AND r.deleted_at IS NULL) AS rating_avg,
	(SELECT count(*) FROM reviews r WHERE r.business_id = b.id AND r.deleted_at IS NULL) AS review_count,
	(SELECT count(*) FROM likes l WHERE l.target_type = 'business' AND l.target_id = b.id) AS like_count,
	(SELECT count(*) FROM recommends rc WHERE rc.business_id = b.id) AS recommend_count,
	(SELECT count(*) FROM collection_items ci WHERE ci.target_type = 'business' AND ci.target_id = b.id) AS save_count,
	COALESCE(cat.name,'') AS category_name, COALESCE(cat.slug,'') AS category_slug`

func scanBusiness(row pgx.Row) (*domain.Business, error) {
	var b domain.Business
	if err := row.Scan(&b.ID, &b.OwnerID, &b.Name, &b.Slug, &b.Tagline, &b.Description, &b.CategoryID,
		&b.Status, &b.RejectionReason, &b.LogoURL, &b.CoverURL, &b.Gallery, &b.PriceLevel, &b.Currency,
		&b.Address, &b.Lat, &b.Lng, &b.City, &b.Country, &b.Timezone, &b.Hours, &b.SpecialHours, &b.Contact, &b.Amenities, &b.Tags, &b.FoundedYear,
		&b.IsFeatured, &b.LastPublishedAt, &b.PublishedSnapshot, &b.VerificationLevel, &b.VerifiedAt, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *BusinessRepo) Create(ctx context.Context, b *domain.Business, categoryID *string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO businesses (id, owner_id, name, slug, description, category_id, currency,
			address, lat, lng, city, country, timezone, hours, contact, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		b.ID, b.OwnerID, b.Name, b.Slug, b.Description, categoryID, b.Currency,
		b.Address, b.Lat, b.Lng, b.City, b.Country, b.Timezone, b.Hours, b.Contact, b.Status)
	if err != nil {
		return fmt.Errorf("create business (id=%s owner=%s): %w", b.ID, b.OwnerID, err)
	}
	return err
}

// allowedBusinessFields: whitelist for PATCH (PRD §8.2).
var allowedBusinessFields = map[string]bool{
	"name": true, "tagline": true, "description": true, "category_id": true,
	"logo_url": true, "cover_url": true, "gallery": true, "price_level": true,
	"currency": true, "address": true, "lat": true, "lng": true, "city": true,
	"country": true, "timezone": true, "hours": true, "special_hours": true, "amenities": true, "contact": true, "tags": true, "founded_year": true,
	"slug": true, "theme": true, "layout": true, "published_snapshot": true,
	"last_published_at": true,
}

func (r *BusinessRepo) Update(ctx context.Context, id string, fields map[string]any) error {
	cols := []string{"updated_at = now()"}
	args := []any{id}
	for k, v := range fields {
		if !allowedBusinessFields[k] {
			return fmt.Errorf("field not allowed: %s", k)
		}
		args = append(args, v)
		cols = append(cols, k+" = $"+itoa(len(args)))
	}
	if len(cols) == 1 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		"UPDATE businesses SET "+strings.Join(cols, ", ")+" WHERE id = $1", args...)
	return err
}

func (r *BusinessRepo) SetStatus(ctx context.Context, id string, status domain.BusinessStatus, extra map[string]any) error {
	cols := []string{"status = $2", "updated_at = now()"}
	args := []any{id, string(status)}
	for k, v := range extra {
		args = append(args, v)
		cols = append(cols, k+" = $"+itoa(len(args)))
	}
	_, err := r.pool.Exec(ctx,
		"UPDATE businesses SET "+strings.Join(cols, ", ")+" WHERE id = $1", args...)
	return err
}

func (r *BusinessRepo) SetVerified(ctx context.Context, id string, level domain.VerificationLevel) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE businesses
		SET status = 'verified', verification_level = $2, verified_at = now(), updated_at = now()
		WHERE id = $1`, id, level)
	return err
}

func (r *BusinessRepo) SetRejected(ctx context.Context, id, reason string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE businesses
		SET status = 'rejected', rejection_reason = $2, verification_level = NULL, verified_at = NULL, updated_at = now()
		WHERE id = $1`, id, reason)
	return err
}

func (r *BusinessRepo) GetByID(ctx context.Context, id string) (*domain.Business, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+BusinessCols+BusinessCounts+`
		FROM businesses b LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE b.id = $1 AND b.deleted_at IS NULL`, id)
	b, err := scanBusinessWithCounts(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

func (r *BusinessRepo) GetBySlug(ctx context.Context, slug string) (*domain.Business, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+BusinessCols+BusinessCounts+`
		FROM businesses b LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE b.slug = $1 AND b.deleted_at IS NULL`, slug)
	b, err := scanBusinessWithCounts(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

func scanBusinessWithCounts(row pgx.Row) (*domain.Business, error) {
	var b domain.Business
	if err := row.Scan(&b.ID, &b.OwnerID, &b.Name, &b.Slug, &b.Tagline, &b.Description, &b.CategoryID,
		&b.Status, &b.RejectionReason, &b.LogoURL, &b.CoverURL, &b.Gallery, &b.PriceLevel, &b.Currency,
		&b.Address, &b.Lat, &b.Lng, &b.City, &b.Country, &b.Timezone, &b.Hours, &b.SpecialHours, &b.Contact, &b.Amenities, &b.Tags, &b.FoundedYear,
		&b.IsFeatured, &b.LastPublishedAt, &b.PublishedSnapshot, &b.VerificationLevel, &b.VerifiedAt, &b.CreatedAt, &b.UpdatedAt,
		&b.RatingAvg, &b.ReviewCount, &b.LikeCount, &b.RecommendCount, &b.SaveCount,
		&b.CategoryName, &b.CategorySlug); err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *BusinessRepo) ListByOwner(ctx context.Context, ownerID string) ([]*domain.Business, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+BusinessCols+`
		FROM businesses b LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE b.owner_id = $1 AND b.deleted_at IS NULL
		ORDER BY b.updated_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Business
	for rows.Next() {
		b, err := scanBusiness(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BusinessRepo) SlugTaken(ctx context.Context, slug, excludeID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM businesses WHERE slug = $1 AND deleted_at IS NULL
			AND (NULLIF($2, '') IS NULL OR id <> NULLIF($2, '')::uuid))`,
		slug, excludeID).Scan(&exists)
	return exists, err
}

// ByStatus returns the queue for admin verification (PRD §5.8.1).
func (r *BusinessRepo) ByStatus(ctx context.Context, statuses []string, limit, offset int) ([]*domain.Business, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+BusinessCols+`
		FROM businesses b LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE b.status = ANY($1) AND b.deleted_at IS NULL
		ORDER BY b.updated_at ASC
		LIMIT $2 OFFSET $3`, statuses, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Business
	for rows.Next() {
		b, err := scanBusiness(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Search runs a caller-built query (service layer composes §5.1.2 filters).
// Rows must include distance_km + ts_rank columns.
func (r *BusinessRepo) Search(ctx context.Context, sql string, args []any) ([]*domain.Business, error) {
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Business
	for rows.Next() {
		var b domain.Business
		if err := rows.Scan(&b.ID, &b.OwnerID, &b.Name, &b.Slug, &b.Tagline, &b.Description, &b.CategoryID,
			&b.Status, &b.RejectionReason, &b.LogoURL, &b.CoverURL, &b.Gallery, &b.PriceLevel, &b.Currency,
			&b.Address, &b.Lat, &b.Lng, &b.City, &b.Country, &b.Timezone, &b.Hours, &b.SpecialHours, &b.Contact, &b.Amenities, &b.Tags, &b.FoundedYear,
			&b.IsFeatured, &b.LastPublishedAt, &b.PublishedSnapshot, &b.VerificationLevel, &b.VerifiedAt, &b.CreatedAt, &b.UpdatedAt,
			&b.RatingAvg, &b.ReviewCount, &b.LikeCount, &b.RecommendCount, &b.SaveCount,
			&b.CategoryName, &b.CategorySlug,
			&b.DistanceKM, &rankIgnored); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

var rankIgnored float64

func (r *BusinessRepo) AddDocument(ctx context.Context, d *domain.VerificationDocument) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO verification_documents (id, business_id, kind, media_id)
		VALUES ($1, $2, $3, $4)`, d.ID, d.BusinessID, d.Kind, d.MediaID)
	return err
}

func (r *BusinessRepo) RemoveDocument(ctx context.Context, id, businessID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM verification_documents WHERE id = $1 AND business_id = $2 AND status = 'pending'`,
		id, businessID)
	return err
}

func (r *BusinessRepo) ListDocuments(ctx context.Context, businessID string) ([]*domain.VerificationDocument, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.id, d.business_id, d.kind, d.media_id, d.status, d.review_note, d.reviewed_at, d.created_at, m.original_name
		FROM verification_documents d
		LEFT JOIN media m ON m.id = d.media_id
		WHERE d.business_id = $1
		ORDER BY d.created_at DESC`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.VerificationDocument
	for rows.Next() {
		var d domain.VerificationDocument
		if err := rows.Scan(&d.ID, &d.BusinessID, &d.Kind, &d.MediaID, &d.Status,
			&d.ReviewNote, &d.ReviewedAt, &d.CreatedAt, &d.FileName); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}
func (r *BusinessRepo) ApprovedDocCount(ctx context.Context, businessID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM verification_documents WHERE business_id = $1 AND status = 'approved'`,
		businessID).Scan(&n)
	return n, err
}

// CanManageBusiness: owner OR accepted co-owner (PRD §5.9.3).
func (r *BusinessRepo) CanManageBusiness(ctx context.Context, userID, businessID string) (bool, error) {
	var can bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM businesses b WHERE b.id = $2 AND b.owner_id = $1 AND b.deleted_at IS NULL
			UNION ALL
			SELECT 1 FROM business_invites i
			JOIN users u ON u.email = i.email
			WHERE i.business_id = $2 AND i.accepted_at IS NOT NULL AND i.revoked_at IS NULL
			  AND u.id = $1
		)`, userID, businessID).Scan(&can)
	return can, err
}

// ---- co-owner invites (PRD §5.9.3) ----

type BusinessInvite struct {
	ID         string    `json:"id"`
	BusinessID string    `json:"business_id"`
	Email      string    `json:"email"`
	Role       string    `json:"role"`
	Token      string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	AcceptedAt *time.Time `json:"accepted_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func (r *BusinessRepo) CreateInvite(ctx context.Context, businessID, email, role, token string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO business_invites (id, business_id, email, role, token)
		VALUES ($1, $2, $3, $4, $5)`, newUUID(), businessID, email, role, token)
	return err
}

func (r *BusinessRepo) ListInvites(ctx context.Context, businessID string) ([]*BusinessInvite, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, business_id, email, role, token, created_at, accepted_at, expires_at, revoked_at
		FROM business_invites WHERE business_id = $1 ORDER BY created_at DESC`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BusinessInvite
	for rows.Next() {
		var i BusinessInvite
		if err := rows.Scan(&i.ID, &i.BusinessID, &i.Email, &i.Role, &i.Token, &i.CreatedAt,
			&i.AcceptedAt, &i.ExpiresAt, &i.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, &i)
	}
	return out, rows.Err()
}

func (r *BusinessRepo) RevokeInvite(ctx context.Context, businessID, inviteID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE business_invites SET revoked_at = now() WHERE id = $1 AND business_id = $2 AND accepted_at IS NULL`,
		inviteID, businessID)
	return err
}

func (r *BusinessRepo) GetInviteByToken(ctx context.Context, token string) (*BusinessInvite, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, business_id, email, role, token, created_at, accepted_at, expires_at, revoked_at
		FROM business_invites WHERE token = $1`, token)
	var i BusinessInvite
	if err := row.Scan(&i.ID, &i.BusinessID, &i.Email, &i.Role, &i.Token, &i.CreatedAt,
		&i.AcceptedAt, &i.ExpiresAt, &i.RevokedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &i, nil
}

func (r *BusinessRepo) AcceptInvite(ctx context.Context, inviteID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE business_invites SET accepted_at = now() WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL
		  AND expires_at > now()`, inviteID)
	return err
}

func (r *BusinessRepo) IncResubmitCount(ctx context.Context, businessID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE businesses SET resubmit_count = resubmit_count + 1, updated_at = now() WHERE id = $1`, businessID)
	return err
}

func (r *BusinessRepo) ResubmitCount(ctx context.Context, businessID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT resubmit_count FROM businesses WHERE id = $1`, businessID).Scan(&n)
	return n, err
}

func (r *BusinessRepo) SetDocumentStatuses(ctx context.Context, businessID, status string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE verification_documents SET status = $2, reviewed_at = now() WHERE business_id = $1 AND status = 'pending'`,
		businessID, status)
	return err
}

func (r *BusinessRepo) LogDocumentView(ctx context.Context, id, documentID, adminID string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO verification_document_views (id, document_id, admin_id) VALUES ($1, $2, $3)`,
		id, documentID, adminID)
	return err
}
