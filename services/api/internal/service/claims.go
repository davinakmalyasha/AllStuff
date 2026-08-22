package service

import (
	"context"
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
		if b.OwnerID != "" {
			return nil, domain.ErrValidation.WithField("business_id", "This listing already has an owner.")
		}
	} else {
		in.BusinessID = nil
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
func (c *Claims) List(ctx context.Context, userID string, admin bool, limit, offset int) ([]*ClaimItem, error) {
	where := ""
	args := []any{limit, offset}
	if !admin {
		where = "WHERE cl.user_id = $3"
		args = append([]any{userID}, args...)
	}
	rows, err := c.repos.Query(ctx, `
		SELECT cl.id, cl.user_id, cl.business_id, cl.name, cl.category_id, cl.address, cl.city,
			cl.country, cl.website, cl.evidence, cl.status, cl.note, cl.created_at, cl.decided_at,
			u.name, u.email
		FROM business_claims cl JOIN users u ON u.id = cl.user_id
		`+where+`
		ORDER BY cl.created_at DESC LIMIT $1 OFFSET $2`, args...)
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
		} else {
			draftID = util.NewUUID()
			_, err := c.repos.Exec(ctx, `
				INSERT INTO businesses (id, owner_id, name, slug, category_id, address, city, country, status)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'draft')`,
				draftID, it.UserID, it.Name, slugify(it.Name), nullableString(it.CategoryID),
				it.Address, it.City, it.Country)
			if err != nil {
				return nil, err
			}
		}
		_, err := c.repos.Exec(ctx, `
			UPDATE business_claims SET status='approved', decided_by=$2, decided_at=now(), note=$3 WHERE id=$1`,
			claimID, adminID, note)
		if err != nil {
			return nil, err
		}
		c.notifier.Create(ctx, it.UserID, "claim_result", map[string]any{
			"claim_id": claimID, "decision": "approved", "business_id": draftID,
		})
	case "reject":
		_, err := c.repos.Exec(ctx, `
			UPDATE business_claims SET status='rejected', decided_by=$2, decided_at=now(), note=$3 WHERE id=$1`,
			claimID, adminID, note)
		if err != nil {
			return nil, err
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

var _ = repo.Repos{}
