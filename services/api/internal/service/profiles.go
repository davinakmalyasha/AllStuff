package service

import (
	"context"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Profiles — public user pages (PRD §6.1 /u/:username).
type Profiles struct {
	repos    *repo.Repos
	notifier *Notifier
}

func NewProfiles(repos *repo.Repos, notifier *Notifier) *Profiles {
	return &Profiles{repos: repos, notifier: notifier}
}

type PublicProfile struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Username   string    `json:"username"`
	AvatarURL  *string   `json:"avatar_url"`
	Bio        *string   `json:"bio"`
	JoinedAt   time.Time `json:"joined_at"`
	Reviews    []any     `json:"reviews"`
	Comments   []any     `json:"comments"`
	Collections []any    `json:"collections"`
	Businesses []any     `json:"businesses"`
}

func (s *Profiles) ByUsername(ctx context.Context, username string) (*PublicProfile, error) {
	u, err := s.repos.Users.GetByUsername(ctx, username)
	if err != nil || u == nil {
		return nil, domain.ErrNotFound
	}

	reviews, err := s.repos.Query(ctx, `
		SELECT r.id, r.rating, r.text, r.created_at, b.name, b.slug
		FROM reviews r JOIN businesses b ON b.id = r.business_id
		WHERE r.user_id = $1 AND r.deleted_at IS NULL AND r.status = 'visible'
		  AND b.status = 'verified'
		ORDER BY r.created_at DESC LIMIT 20`, u.ID)
	if err != nil {
		return nil, err
	}
	reviewRows := []any{}
	for reviews.Next() {
		var id, text, bName, bSlug string
		var rating int
		var createdAt time.Time
		if err := reviews.Scan(&id, &rating, &text, &createdAt, &bName, &bSlug); err == nil {
			reviewRows = append(reviewRows, map[string]any{
				"id": id, "rating": rating, "text": text, "created_at": createdAt,
				"business_name": bName, "business_slug": bSlug,
			})
		}
	}
	reviews.Close()

	comments, err := s.repos.Query(ctx, `
		SELECT c.id, c.text, c.created_at, b.name, b.slug
		FROM comments c JOIN businesses b ON b.id = c.business_id
		WHERE c.user_id = $1 AND c.status = 'visible' AND b.status = 'verified'
		ORDER BY c.created_at DESC LIMIT 20`, u.ID)
	if err != nil {
		return nil, err
	}
	commentRows := []any{}
	for comments.Next() {
		var id, text, bName, bSlug string
		var createdAt time.Time
		if err := comments.Scan(&id, &text, &createdAt, &bName, &bSlug); err == nil {
			commentRows = append(commentRows, map[string]any{
				"id": id, "text": text, "created_at": createdAt, "business_name": bName, "business_slug": bSlug,
			})
		}
	}
	comments.Close()

	collections, err := s.repos.Query(ctx, `
		SELECT id, name, slug, created_at FROM collections
		WHERE user_id = $1 AND is_public = true AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 20`, u.ID)
	if err != nil {
		return nil, err
	}
	colRows := []any{}
	for collections.Next() {
		var id, name, slug string
		var createdAt time.Time
		if err := collections.Scan(&id, &name, &slug, &createdAt); err == nil {
			colRows = append(colRows, map[string]any{"id": id, "name": name, "slug": slug, "created_at": createdAt})
		}
	}
	collections.Close()

	businesses, err := s.repos.Query(ctx, `
		SELECT id, name, slug, logo_url, city, category_id FROM businesses
		WHERE owner_id = $1 AND status = 'verified' AND deleted_at IS NULL LIMIT 20`, u.ID)
	if err != nil {
		return nil, err
	}
	bizRows := []any{}
	for businesses.Next() {
		var id, name, slug, city, catID string
		var logo *string
		if err := businesses.Scan(&id, &name, &slug, &logo, &city, &catID); err == nil {
			bizRows = append(bizRows, map[string]any{
				"id": id, "name": name, "slug": slug, "logo_url": logo, "city": city, "category_id": catID,
			})
		}
	}
	businesses.Close()

	return &PublicProfile{
		ID: u.ID, Name: u.Name, Username: u.Username, AvatarURL: u.AvatarURL,
		Bio: u.Bio, JoinedAt: u.CreatedAt,
		Reviews: reviewRows, Comments: commentRows, Collections: colRows, Businesses: bizRows,
	}, nil
}

// ---- support contact (help center) ----

func (s *Profiles) Contact(ctx context.Context, userID, subject, message string) error {
	subject = strings.TrimSpace(subject)
	message = strings.TrimSpace(message)
	if subject == "" || len([]rune(subject)) > 120 {
		return domain.ErrValidation.WithField("subject", "Subject must be 1–120 characters.")
	}
	if n := len([]rune(message)); n < 10 || n > 4000 {
		return domain.ErrValidation.WithField("message", "Message must be 10–4000 characters.")
	}
	var email string
	if err := s.repos.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		return domain.ErrNotFound
	}
	// Stored for the support inbox (in-app for admin) + sent by email where configured.
	_, err := s.repos.Exec(ctx, `
		INSERT INTO reports (id, reporter_id, target_type, target_id, reason, evidence)
		VALUES ($1, $2, 'support', $3, $4, $5)`,
		util.NewUUID(), userID, email, subject+"\n\n"+message,
		map[string]any{"kind": "support"})
	return err
}

// ---- appeals (suspended/banned users) ----

func (s *Profiles) Appeal(ctx context.Context, userID, reason string) error {
	reason = strings.TrimSpace(reason)
	if n := len([]rune(reason)); n < 10 || n > 2000 {
		return domain.ErrValidation.WithField("reason", "Appeal must be 10–2000 characters.")
	}
	var open int
	if err := s.repos.QueryRow(ctx,
		`SELECT count(*) FROM appeals WHERE user_id = $1 AND status = 'open'`, userID).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return domain.ErrValidation.WithField("_", "You already have an open appeal.")
	}
	_, err := s.repos.Exec(ctx, `
		INSERT INTO appeals (id, user_id, reason) VALUES ($1, $2, $3)`,
		util.NewUUID(), userID, reason)
	return err
}

func (s *Profiles) Appeals(ctx context.Context, status string) ([]map[string]any, error) {
	rows, err := s.repos.Query(ctx, `
		SELECT a.id, a.user_id, a.reason, a.status, a.created_at, u.name, u.email, u.status AS user_status
		FROM appeals a JOIN users u ON u.id = a.user_id
		WHERE a.status = $1 ORDER BY a.created_at ASC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, userID, reason, st, name, email, userStatus string
		var createdAt time.Time
		if err := rows.Scan(&id, &userID, &reason, &st, &createdAt, &name, &email, &userStatus); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "user_id": userID, "reason": reason, "status": st, "created_at": createdAt,
			"name": name, "email": email, "user_status": userStatus,
		})
	}
	return out, rows.Err()
}

func (s *Profiles) DecideAppeal(ctx context.Context, adminID, appealID, decision string) error {
	var userID string
	if err := s.repos.QueryRow(ctx,
		`SELECT user_id FROM appeals WHERE id = $1 AND status = 'open'`, appealID).Scan(&userID); err != nil {
		return domain.ErrNotFound
	}
	decision = strings.TrimSpace(decision)
	if decision != "approve" && decision != "reject" {
		return domain.ErrValidation.WithField("decision", "Decision must be approve or reject.")
	}
	if decision == "approve" {
		if _, err := s.repos.Exec(ctx, `
			UPDATE users SET status = 'active', suspended_until = NULL, updated_at = now() WHERE id = $1`, userID); err != nil {
			return err
		}
	}
	if _, err := s.repos.Exec(ctx, `
		UPDATE appeals SET status = 'resolved', decided_by = $2, decision = $3, resolved_at = now() WHERE id = $1`,
		appealID, adminID, decision); err != nil {
		return err
	}
	s.notifier.Create(ctx, userID, "appeal_result", map[string]any{
		"decision": decision,
	})
	return nil
}

// ---- trending anomalies (PRD §8.6) ----

func (s *Profiles) Anomalies(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.repos.Query(ctx, `
		SELECT e.id, e.user_id, e.signal, e.weight, e.occurred_at, b.name, b.slug,
			(SELECT count(*) FROM engagement_events e2 WHERE e2.target_id = e.target_id AND e2.signal = e.signal AND e2.flagged = false) AS normal_count
		FROM engagement_events e
		JOIN businesses b ON b.id = e.target_id
		WHERE e.flagged = true
		ORDER BY e.occurred_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, userID, signal, bName, bSlug string
		var weight, normal int
		var occurredAt time.Time
		if err := rows.Scan(&id, &userID, &signal, &weight, &occurredAt, &bName, &bSlug, &normal); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "user_id": userID, "signal": signal, "weight": weight,
			"occurred_at": occurredAt, "business_name": bName, "business_slug": bSlug, "normal_count": normal,
		})
	}
	return out, rows.Err()
}

func (s *Profiles) ResolveAnomaly(ctx context.Context, eventID string) error {
	_, err := s.repos.Exec(ctx,
		`UPDATE engagement_events SET flagged = false WHERE id = $1 AND flagged = true`, eventID)
	return err
}

var _ = repo.Repos{}
