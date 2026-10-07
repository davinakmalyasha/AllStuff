package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Claims — claim-a-business (PRD §6.1): users submit an unlisted business or
// claim an existing listing; admins approve → a draft is created for the
// claimer to complete, or reject with a note.
type Claims struct {
	repos    *repo.Repos
	notifier *Notifier
}

func NewClaims(repos *repo.Repos, notifier *Notifier) *Claims {
	return &Claims{repos: repos, notifier: notifier}
}

type ClaimInput struct {
	Name       string  `json:"name"`
	CategoryID string  `json:"category_id"`
	Address    string  `json:"address"`
	City       string  `json:"city"`
	Country    string  `json:"country"`
	Website    string  `json:"website"`
	Evidence   string  `json:"evidence"`
	BusinessID *string `json:"business_id"` // optional: claiming an existing listing
}

type ClaimItem struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	BusinessID *string    `json:"business_id"`
	Name       string     `json:"name"`
	CategoryID string     `json:"category_id"`
	Address    string     `json:"address"`
	City       string     `json:"city"`
	Country    string     `json:"country"`
	Website    string     `json:"website"`
	Evidence   string     `json:"evidence"`
	Status     string     `json:"status"`
	Note       string     `json:"note"`
	CreatedAt  time.Time  `json:"created_at"`
	DecidedAt  *time.Time `json:"decided_at,omitempty"`
	UserName   string     `json:"user_name,omitempty"`
	UserEmail  string     `json:"user_email,omitempty"`
}

func (c *Claims) Submit(ctx context.Context, userID string, in ClaimInput) (*ClaimItem, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.TrimSpace(in.Address)
	in.City = strings.TrimSpace(in.City)
	if in.Name == "" || len([]rune(in.Name)) > 100 {
		return nil, domain.ErrValidation.WithField("name", "Business name is required (max 100).")
	}
	if in.Address == "" || in.City == "" {
		return nil, domain.ErrValidation.WithField("_", "Address and city are required.")
	}
	if in.BusinessID != nil && *in.BusinessID != "" {
		b, err := c.repos.Businesses.GetByID(ctx, *in.BusinessID)
		if err != nil || b == nil {
			return nil, domain.ErrValidation.WithField("business_id", "Listing not found.")
		}
		if b.OwnerID != "" { // lint:allow: not authorisation - asks whether the column is populated, not who may act
			return nil, domain.ErrValidation.WithField("business_id", "This listing already has an owner.")
		}
	} else {
		in.BusinessID = nil
	}
	if in.CategoryID != "" {
		cat, err := c.repos.Categories.GetByID(ctx, in.CategoryID)
		if err != nil {
			return nil, err
		}
		if cat == nil {
			return nil, domain.ErrValidation.WithField("category_id", "Category not found.")
		}
	}
	item := &ClaimItem{
		ID: util.NewUUID(), UserID: userID, BusinessID: in.BusinessID, Name: in.Name,
		CategoryID: in.CategoryID, Address: in.Address, City: in.City, Country: in.Country,
		Website: in.Website, Evidence: in.Evidence, Status: "open", CreatedAt: time.Now(),
	}
	_, err := c.repos.Exec(ctx, `
		INSERT INTO business_claims (id, user_id, business_id, name, category_id, address, city, country, website, evidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		item.ID, item.UserID, item.BusinessID, item.Name, nullableString(in.CategoryID),
		item.Address, item.City, item.Country, item.Website, item.Evidence)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// List returns claims for the admin queue (open first) or a user's own.
// Placeholders are built positionally so the user filter and LIMIT/OFFSET
// can never swap bind-parameter order.
func (c *Claims) List(ctx context.Context, userID string, admin bool, limit, offset int) ([]*ClaimItem, error) {
	where := ""
	args := []any{}
	if !admin {
		where = "WHERE cl.user_id = $1"
		args = append(args, userID)
	}
	args = append(args, limit, offset)
	limitPh, offsetPh := "$"+strconv.Itoa(len(args)-1), "$"+strconv.Itoa(len(args))
	rows, err := c.repos.Query(ctx, `
		SELECT cl.id, cl.user_id, cl.business_id, cl.name, cl.category_id, cl.address, cl.city,
			cl.country, cl.website, cl.evidence, cl.status, cl.note, cl.created_at, cl.decided_at,
			u.name, u.email
		FROM business_claims cl JOIN users u ON u.id = cl.user_id
		`+where+`
		ORDER BY cl.created_at DESC, cl.id ASC LIMIT `+limitPh+` OFFSET `+offsetPh, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ClaimItem
	for rows.Next() {
		var it ClaimItem
		if err := rows.Scan(&it.ID, &it.UserID, &it.BusinessID, &it.Name, &it.CategoryID, &it.Address,
			&it.City, &it.Country, &it.Website, &it.Evidence, &it.Status, &it.Note, &it.CreatedAt,
			&it.DecidedAt, &it.UserName, &it.UserEmail); err != nil {
			return nil, err
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

// Decide approves (creating a draft business for the claimer) or rejects.
func (c *Claims) Decide(ctx context.Context, adminID, claimID, decision, note string) (*ClaimItem, error) {
	var it ClaimItem
	if err := c.repos.QueryRow(ctx, `
		SELECT id, user_id, business_id, name, category_id, address, city, country, website, status
		FROM business_claims WHERE id = $1`, claimID).Scan(&it.ID, &it.UserID, &it.BusinessID,
		&it.Name, &it.CategoryID, &it.Address, &it.City, &it.Country, &it.Website, &it.Status); err != nil {
		return nil, domain.ErrNotFound
	}
	if it.Status != "open" {
		return nil, domain.ErrValidation.WithField("_", "Claim already decided.")
	}
	switch decision {
	case "approve":
		var draftID string
		if it.BusinessID != nil && *it.BusinessID != "" {
			draftID = *it.BusinessID
			// Transfer ownership of the unowned listing atomically with the
			// decision flip — previously the claimer was told they won while
			// owner_id never changed.
			tx, err := c.repos.Pool().Begin(ctx)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback(ctx)
			txr := repo.NewForTx(tx)
			ct, err := txr.Exec(ctx,
				`UPDATE businesses SET owner_id=$2, updated_at=now() WHERE id=$1 AND deleted_at IS NULL`,
				draftID, it.UserID)
			if err != nil {
				return nil, err
			}
			if ct.RowsAffected() == 0 {
				return nil, domain.ErrNotFound
			}
			res, err := txr.Exec(ctx, `
				UPDATE business_claims SET status='approved', decided_by=$2, decided_at=now(), note=$3
				WHERE id=$1 AND status='open'`, claimID, adminID, note)
			if err != nil {
				return nil, err
			}
			if res.RowsAffected() == 0 {
				return nil, domain.ErrValidation.WithField("_", "Claim already decided.")
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
		} else {
			draftID = util.NewUUID()
			// Drafts need placeholder coordinates (NOT NULL schema columns);
			// the claimer completes them in the wizard. Slug collisions are
			// resolved with a suffix (globally unique column).
			slug := slugify(it.Name)
			found := false
			for i := 0; i < 10; i++ {
				taken, err := c.repos.Businesses.SlugTaken(ctx, slug, "")
				if err != nil {
					return nil, err
				}
				if !taken {
					found = true
					break
				}
				slug = slugify(it.Name) + "-" + util.NewUUID()[:6]
			}
			if !found {
				return nil, domain.ErrInternal
			}
			if _, err := c.repos.Exec(ctx, `
			INSERT INTO businesses (id, owner_id, name, slug, description, category_id, address, city, country, lat, lng, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 0, 0, 'draft')`,
				draftID, it.UserID, it.Name, slug, draftDescription(it.Evidence),
				nullableString(it.CategoryID), it.Address, it.City, it.Country); err != nil {
				return nil, err
			}
			res, err := c.repos.Exec(ctx, `
				UPDATE business_claims SET status='approved', decided_by=$2, decided_at=now(), note=$3
				WHERE id=$1 AND status='open'`, claimID, adminID, note)
			if err != nil {
				return nil, err
			}
			if res.RowsAffected() == 0 {
				return nil, domain.ErrValidation.WithField("_", "Claim already decided.")
			}
		}
		c.notifier.Create(ctx, it.UserID, "claim_result", map[string]any{
			"claim_id": claimID, "decision": "approved", "business_id": draftID,
		})
	case "reject":
		res, err := c.repos.Exec(ctx, `
			UPDATE business_claims SET status='rejected', decided_by=$2, decided_at=now(), note=$3
			WHERE id=$1 AND status='open'`, claimID, adminID, note)
		if err != nil {
			return nil, err
		}
		if res.RowsAffected() == 0 {
			return nil, domain.ErrValidation.WithField("_", "Claim already decided.")
		}
		c.notifier.Create(ctx, it.UserID, "claim_result", map[string]any{
			"claim_id": claimID, "decision": "rejected", "note": note,
		})
	default:
		return nil, domain.ErrValidation.WithField("decision", "Must be approve or reject.")
	}
	// Return the decided row.
	var decided ClaimItem
	if err := c.repos.QueryRow(ctx, `
		SELECT id, user_id, business_id, name, category_id, address, city, country, website,
			evidence, status, note, created_at, decided_at
		FROM business_claims WHERE id = $1`, claimID).Scan(&decided.ID, &decided.UserID, &decided.BusinessID,
		&decided.Name, &decided.CategoryID, &decided.Address, &decided.City, &decided.Country,
		&decided.Website, &decided.Evidence, &decided.Status, &decided.Note, &decided.CreatedAt,
		&decided.DecidedAt); err != nil {
		return nil, err
	}
	return &decided, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// draftDescription seeds a new draft's description.
//
// `businesses.description` is NOT NULL. Migration 0004 dropped the
// `char_length >= 50` CHECK but left the NOT NULL, so an INSERT that omits the
// column fails with 23502 — which is why EVERY approval of a newly-listed claim
// returned 500 while the (separate) existing-listing branch looked fine.
//
// The evidence text is the natural seed: the claimer wrote it to justify the
// business. It is truncated so a long submission cannot be pushed into the row
// wholesale, and absent evidence still yields something the wizard replaces.
func draftDescription(evidence string) string {
	d := strings.TrimSpace(evidence)
	if d == "" {
		d = "Draft created from a business claim. Complete the profile before submitting for verification."
	}
	if r := []rune(d); len(r) > 2000 {
		d = string(r[:2000])
	}
	return d
}
