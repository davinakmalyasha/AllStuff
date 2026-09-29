package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/util"
)

type BusinessRepo struct{ pool pooler }

// BusinessCols is the shared column list for every business read.
//
// owner_id is COALESCEd to an empty string, for the same reason category_id
// is: the column is nullable (migration 0029 made it so, so an UNOWNED listing is
// representable) and pgx cannot scan SQL NULL into a Go `string`. Without the
// COALESCE, reading an unowned business fails with
// "can't scan NULL into *string" — so making the column nullable was necessary
// but not sufficient, and claim-an-existing-listing was still broken on the READ
// side even after the write side was fixed.
//
// Empty string is the right sentinel rather than a pointer because that is the
// contract the service layer already codes against: service/claims.go detects an
// unowned listing with `if b.OwnerID != ""`, and CanManageBusiness is a
// positive check so "" simply means nobody manages it.
const BusinessCols = `b.id, COALESCE(b.owner_id::text, '') AS owner_id,
	b.name, b.slug, b.tagline, b.description, COALESCE(b.category_id::text, '') AS category_id,
	b.status, b.rejection_reason, b.logo_url, b.cover_url, b.gallery, b.price_level, b.currency,
	b.address, b.lat, b.lng, b.city, b.country, b.timezone, b.hours, b.special_hours, b.theme, b.layout, b.contact, b.amenities, b.tags, b.founded_year,
	b.is_featured, b.last_published_at, b.published_snapshot, b.verification_level, b.verified_at, b.slug_changed_at, b.created_at, b.updated_at`

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
		&b.Address, &b.Lat, &b.Lng, &b.City, &b.Country, &b.Timezone, &b.Hours, &b.SpecialHours, &b.Theme, &b.Layout, &b.Contact, &b.Amenities, &b.Tags, &b.FoundedYear,
		&b.IsFeatured, &b.LastPublishedAt, &b.PublishedSnapshot, &b.VerificationLevel, &b.VerifiedAt, &b.SlugChangedAt, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

// Create inserts a business.
//
// An empty OwnerID is written as SQL NULL, not as the empty string. This is not
// cosmetic: `owner_id` is `uuid`, so binding `""` asks Postgres to cast the
// empty string to a uuid and it raises `22P02 invalid input syntax for type
// uuid: ""`. Migration 0029 made the column nullable specifically so that an
// UNOWNED listing is representable — which is what claim-an-existing-listing
// depends on, and what any imported directory contains — but the write path
// could not produce the state the schema was changed to permit.
//
// So the nullable column was necessary and not sufficient. The symptom was
// masked because every current caller passes a real owner, and unowned listings
// would arrive through a raw import rather than through this function, so the
// failure would have appeared at integration time rather than in development.
func (r *BusinessRepo) Create(ctx context.Context, b *domain.Business, categoryID *string) error {
	// domain.Business.OwnerID is a plain string so that a zero value is
	// representable and CanManageBusiness can use "" to mean "nobody manages
	// this". Translate that at the boundary rather than making every caller
	// remember to pass a pointer.
	var ownerID *string
	if b.OwnerID != "" {
		ownerID = &b.OwnerID
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO businesses (id, owner_id, name, slug, description, category_id, currency,
			address, lat, lng, city, country, timezone, hours, contact, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		b.ID, ownerID, b.Name, b.Slug, b.Description, categoryID, b.Currency,
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
	"slug": true, "slug_changed_at": true, "theme": true, "layout": true, "published_snapshot": true,
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
		cols = append(cols, k+" = $"+util.Itoa(len(args)))
	}
	if len(cols) == 1 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		"UPDATE businesses SET "+strings.Join(cols, ", ")+" WHERE id = $1", args...)
	return err
}

// setStatusExtraFields whitelists the extra columns SetStatus may write.
// Without a guard this interpolates map keys straight into SQL.
//
// `deleted_at` was added in 0032, when retirement started actually setting it.
// See that migration for why the column existed but was never written.
var setStatusExtraFields = map[string]bool{
	"rejection_reason": true, "published_snapshot": true,
	"verification_level": true, "verified_at": true,
	"last_published_at": true,
	"deleted_at":        true,
}

func (r *BusinessRepo) SetStatus(ctx context.Context, id string, status domain.BusinessStatus, extra map[string]any) error {
	cols := []string{"status = $2", "updated_at = now()"}
	args := []any{id, string(status)}
	for k, v := range extra {
		if !setStatusExtraFields[k] {
			return fmt.Errorf("field not allowed in SetStatus: %s", k)
		}
		args = append(args, v)
		cols = append(cols, k+" = $"+util.Itoa(len(args)))
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

// GetByID resolves a listing by id regardless of whether it has been retired.
//
// It stopped filtering `deleted_at IS NULL` in 0032. Before that, a retired
// listing became invisible to the owner who closed it: they could neither see
// it in ListByOwner, resolve it here, nor pass CanManageBusiness, so it was an
// unmanageable black hole that still consumed a slug and a quota slot.
//
// Dropping the filter is safe for public exposure because no public path trusts
// this method on its own. GetPublic goes through GetBySlug, which still filters
// retired rows, and the compare endpoint re-checks the status itself
// (httpapi/handlers_directory.go). Retired rows carry status = 'closed', which
// is on no public whitelist.
func (r *BusinessRepo) GetByID(ctx context.Context, id string) (*domain.Business, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+BusinessCols+BusinessCounts+`
		FROM businesses b LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE b.id = $1`, id)
	b, err := scanBusinessWithCounts(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// GetBySlug resolves a LIVE listing by slug, and deliberately still filters
// `deleted_at IS NULL` even though GetByID no longer does.
//
// This is the asymmetry that makes releasing a slug work. A retired listing
// keeps its old slug string on the row for the historical record, so once a new
// business claims that slug, two rows carry it. Only the live one may resolve.
// If this stopped filtering, a retired listing would shadow whichever business
// currently owns its former URL, and the old shop's page would sit in front of
// the new shop's.
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
		&b.Address, &b.Lat, &b.Lng, &b.City, &b.Country, &b.Timezone, &b.Hours, &b.SpecialHours, &b.Theme, &b.Layout, &b.Contact, &b.Amenities, &b.Tags, &b.FoundedYear,
		&b.IsFeatured, &b.LastPublishedAt, &b.PublishedSnapshot, &b.VerificationLevel, &b.VerifiedAt, &b.SlugChangedAt, &b.CreatedAt, &b.UpdatedAt,
		&b.RatingAvg, &b.ReviewCount, &b.LikeCount, &b.RecommendCount, &b.SaveCount,
		&b.CategoryName, &b.CategorySlug); err != nil {
		return nil, err
	}
	return &b, nil
}

// ListByOwner returns every listing an owner holds, including retired ones.
//
// It stopped filtering `deleted_at IS NULL` in 0032, for the same reason
// GetByID did: filtering here made a closed listing disappear from its owner's
// own dashboard with no indication it existed. Retired rows sort last rather
// than being dropped, so an owner can see the full history of what they have
// operated and distinguish a live listing from a closed one.
func (r *BusinessRepo) ListByOwner(ctx context.Context, ownerID string) ([]*domain.Business, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+BusinessCols+`
		FROM businesses b LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE b.owner_id = $1
		ORDER BY b.deleted_at IS NULL DESC, b.updated_at DESC`, ownerID)
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

// ChangeSlugOnce rotates the slug only when the one-time change is still
// unconsumed; false means a concurrent request won the race (PRD §8.2).
// The guard lives in this single UPDATE so two requests can never both see
// slug_changed_at IS NULL and both rotate.
func (r *BusinessRepo) ChangeSlugOnce(ctx context.Context, id, slug string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE businesses SET slug = $2, slug_changed_at = now(), updated_at = now()
		WHERE id = $1 AND slug_changed_at IS NULL AND deleted_at IS NULL`, id, slug)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListFeatured returns featured verified businesses in curation order
// (homepage strip, PRD §5.8.5).
func (r *BusinessRepo) ListFeatured(ctx context.Context, limit int) ([]*domain.Business, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+BusinessCols+BusinessCounts+`
		FROM businesses b
		LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE b.is_featured = true AND b.status = 'verified' AND b.deleted_at IS NULL
		ORDER BY b.featured_order, b.name LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Business
	for rows.Next() {
		b, err := scanBusinessWithCounts(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PublicSlug is one sitemap entry: the slug plus its last-modified stamp.
type PublicSlug struct {
	Slug      string
	UpdatedAt time.Time
}

// PublicSlugs returns slugs for the sitemap — selecting only the slug column
// instead of full rows (the old path pulled hours + published_snapshot JSONB
// for up to 100k businesses per sitemap hit).
func (r *BusinessRepo) PublicSlugs(ctx context.Context, status string, limit int) ([]PublicSlug, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT slug, updated_at FROM businesses WHERE status = $1 AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT $2`,
		status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PublicSlug
	for rows.Next() {
		var s PublicSlug
		if err := rows.Scan(&s.Slug, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ByStatus returns the queue for admin verification (PRD §5.8.1).
//
// `b.id ASC` completes the order so paging the queue cannot show one listing
// twice while skipping another: admins work this queue by status and they change
// rows out from under each other, so equal updated_at is the normal case rather
// than an edge case.
//
// No supporting index was added for it in 0033, on purpose. `status = ANY($1)` is
// a multi-value comparison, so an index on (status, updated_at, id) still could
// not yield one total order across the array — Postgres would merge and sort. The
// query already sorts today, so the tiebreaker is free, and the index would be
// storage never used for ordering. This is an admin-volume query.
func (r *BusinessRepo) ByStatus(ctx context.Context, statuses []string, limit, offset int) ([]*domain.Business, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+BusinessCols+`
		FROM businesses b LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE b.status = ANY($1) AND b.deleted_at IS NULL
		ORDER BY b.updated_at ASC, b.id ASC
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
		var rankIgnored float64 // per-call destination: a shared package var here is a data race across concurrent searches
		if err := rows.Scan(&b.ID, &b.OwnerID, &b.Name, &b.Slug, &b.Tagline, &b.Description, &b.CategoryID,
			&b.Status, &b.RejectionReason, &b.LogoURL, &b.CoverURL, &b.Gallery, &b.PriceLevel, &b.Currency,
			&b.Address, &b.Lat, &b.Lng, &b.City, &b.Country, &b.Timezone, &b.Hours, &b.SpecialHours, &b.Theme, &b.Layout, &b.Contact, &b.Amenities, &b.Tags, &b.FoundedYear,
			&b.IsFeatured, &b.LastPublishedAt, &b.PublishedSnapshot, &b.VerificationLevel, &b.VerifiedAt, &b.SlugChangedAt, &b.CreatedAt, &b.UpdatedAt,
			&b.RatingAvg, &b.ReviewCount, &b.LikeCount, &b.RecommendCount, &b.SaveCount,
			&b.CategoryName, &b.CategorySlug,
			&b.DistanceKM, &rankIgnored); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

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

// CanManageBusiness: owner OR accepted co-owner invite (PRD §5.9.3).
// The role matters: "viewer" invites are read-only and must never gain
// management rights over the listing.
//
// The owner branch stopped filtering `deleted_at IS NULL` in 0032. A retired
// listing has no owner-side escape hatch: `Reopen` only reopens from `paused`,
// so `closed` is terminal, and denying management would leave the row
// permanently unadministrable — no edits, no collaborator changes, and no way
// for the owner to see the state they put it in.
//
// Management rights are not publication rights. Every state transition that
// would make a listing visible re-checks status first, so granting management
// of a closed listing cannot republish it.
func (r *BusinessRepo) CanManageBusiness(ctx context.Context, userID, businessID string) (bool, error) {
	var can bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM businesses b WHERE b.id = $2 AND b.owner_id = $1
			UNION ALL
			SELECT 1 FROM business_invites i
			JOIN users u ON u.email = i.email
			WHERE i.business_id = $2 AND i.accepted_at IS NOT NULL AND i.revoked_at IS NULL
			  AND i.role = 'co_owner'
			  AND u.id = $1
		)`, userID, businessID).Scan(&can)
	return can, err
}

// IsBusinessViewer reports whether the user holds an accepted viewer invite
// (read-only collaborator access to the dashboard).
func (r *BusinessRepo) IsBusinessViewer(ctx context.Context, userID, businessID string) (bool, error) {
	var can bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM business_invites i
			JOIN users u ON u.email = i.email
			WHERE i.business_id = $2 AND i.accepted_at IS NOT NULL AND i.revoked_at IS NULL
			  AND i.role = 'viewer'
			  AND u.id = $1
		)`, userID, businessID).Scan(&can)
	return can, err
}

// ---- co-owner invites (PRD §5.9.3) ----

type BusinessInvite struct {
	ID         string     `json:"id"`
	BusinessID string     `json:"business_id"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	Token      string     `json:"-"`
	InvitedBy  string     `json:"invited_by"`
	CreatedAt  time.Time  `json:"created_at"`
	AcceptedAt *time.Time `json:"accepted_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func (r *BusinessRepo) CreateInvite(ctx context.Context, invitedBy, businessID, email, role, token string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO business_invites (id, invited_by, business_id, email, role, token)
		VALUES ($1, $2, $3, $4, $5, $6)`, util.NewUUID(), invitedBy, businessID, email, role, token)
	return err
}

func (r *BusinessRepo) ListInvites(ctx context.Context, businessID string) ([]*BusinessInvite, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, business_id, email, role, token, invited_by, created_at, accepted_at, expires_at, revoked_at
		FROM business_invites WHERE business_id = $1 ORDER BY created_at DESC`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BusinessInvite
	for rows.Next() {
		var i BusinessInvite
		if err := rows.Scan(&i.ID, &i.BusinessID, &i.Email, &i.Role, &i.Token, &i.InvitedBy, &i.CreatedAt,
			&i.AcceptedAt, &i.ExpiresAt, &i.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, &i)
	}
	return out, rows.Err()
}

// CountActiveSeats counts the people who can currently manage or view the
// business, for the plan's team_seats cap.
//
// The owner counts as one seat. An invite counts once it is ACCEPTED and counts
// as nothing while it is pending, revoked, or expired — a pending invitation is
// an intention, not a seat, and blocking on it would let a business fill its
// allowance with invitations nobody has accepted and then be unable to invite
// anyone who actually turns up.
//
// The accepted-invite arm matches CanManageBusiness and IsBusinessViewer
// exactly, including their citext email comparison. A seat that the permission
// check does not grant would be a cap the user pays for and cannot use.
// CountLiveByOwner counts an owner's non-retired listings, for the per-user cap
// in service.Businesses.Create.
//
// "Live" is `deleted_at IS NULL` rather than a status test, deliberately: it is
// the same predicate the slug partial index uses. One definition of "live",
// applied by both, means the cap and the slug namespace can never disagree
// about whether a closed shop still occupies a resource. Retiring a listing is
// what frees the slot, which is the whole point of closing it.
func (r *BusinessRepo) CountLiveByOwner(ctx context.Context, ownerID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM businesses WHERE owner_id = $1 AND deleted_at IS NULL`, ownerID).Scan(&n)
	return n, err
}

func (r *BusinessRepo) CountActiveSeats(ctx context.Context, businessID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM businesses b
		      WHERE b.id = $1 AND b.owner_id IS NOT NULL)
		  + (SELECT count(*) FROM business_invites i
		      WHERE i.business_id = $1
		        AND i.accepted_at IS NOT NULL
		        AND i.revoked_at IS NULL
		        AND i.expires_at > now())`, businessID).Scan(&n)
	return n, err
}

func (r *BusinessRepo) RevokeInvite(ctx context.Context, businessID, inviteID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE business_invites SET revoked_at = now() WHERE id = $1 AND business_id = $2 AND accepted_at IS NULL`,
		inviteID, businessID)
	return err
}

func (r *BusinessRepo) GetInviteByToken(ctx context.Context, token string) (*BusinessInvite, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, business_id, email, role, token, invited_by, created_at, accepted_at, expires_at, revoked_at
		FROM business_invites WHERE token = $1`, token)
	var i BusinessInvite
	if err := row.Scan(&i.ID, &i.BusinessID, &i.Email, &i.Role, &i.Token, &i.InvitedBy, &i.CreatedAt,
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
