package service

import (
	"context"
	"regexp"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Invites — co-owner invitations (PRD §5.9.3).
type Invites struct {
	repos *repo.Repos
}

func NewInvites(repos *repo.Repos) *Invites { return &Invites{repos: repos} }

var inviteEmailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func (s *Invites) Create(ctx context.Context, ownerID, businessID, email, role string) error {
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, businessID)
	if err != nil {
		return err
	}
	if !can {
		return domain.ErrForbidden
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !inviteEmailRe.MatchString(email) {
		return domain.ErrValidation.WithField("email", "Enter a valid email address.")
	}
	if role != "co_owner" && role != "viewer" {
		role = "co_owner"
	}
	return s.repos.Businesses.CreateInvite(ctx, ownerID, businessID, email, role, util.NewUUID()+"-"+util.NewUUID())
}

func (s *Invites) List(ctx context.Context, ownerID, businessID string) ([]*repo.BusinessInvite, error) {
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, businessID)
	if err != nil {
		return nil, err
	}
	if !can {
		return nil, domain.ErrForbidden
	}
	return s.repos.Businesses.ListInvites(ctx, businessID)
}

func (s *Invites) Revoke(ctx context.Context, ownerID, businessID, inviteID string) error {
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, businessID)
	if err != nil {
		return err
	}
	if !can {
		return domain.ErrForbidden
	}
	return s.repos.Businesses.RevokeInvite(ctx, businessID, inviteID)
}

func (s *Invites) Accept(ctx context.Context, userID, token string) error {
	inv, err := s.repos.Businesses.GetInviteByToken(ctx, token)
	if err != nil {
		return err
	}
	if inv == nil || inv.RevokedAt != nil || inv.AcceptedAt != nil {
		return domain.ErrTokenInvalid
	}
	if time.Now().After(inv.ExpiresAt) {
		return domain.ErrTokenInvalid
	}
	user, err := s.repos.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return domain.ErrNotFound
	}
	if !strings.EqualFold(user.Email, inv.Email) {
		return domain.ErrForbidden
	}
	return s.repos.Businesses.AcceptInvite(ctx, inv.ID)
}

// ---- KPI analytics (PRD §5.8.6) ----

func (s *Invites) KPIs(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	var users, businesses, verified, pending, reviews, comments, messages, openReports int
	err := s.repos.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM users WHERE deleted_at IS NULL),
			(SELECT count(*) FROM businesses WHERE deleted_at IS NULL),
			(SELECT count(*) FROM businesses WHERE status='verified' AND deleted_at IS NULL),
			(SELECT count(*) FROM businesses WHERE status='pending_review' AND deleted_at IS NULL),
			(SELECT count(*) FROM reviews WHERE deleted_at IS NULL),
			(SELECT count(*) FROM comments),
			(SELECT count(*) FROM chat_messages),
			(SELECT count(*) FROM reports WHERE status='open')`).
		Scan(&users, &businesses, &verified, &pending, &reviews, &comments, &messages, &openReports)
	if err != nil {
		return nil, err
	}
	out["users"] = users
	out["businesses"] = businesses
	out["verified_businesses"] = verified
	out["pending_reviews"] = pending
	out["reviews"] = reviews
	out["comments"] = comments
	out["messages"] = messages
	out["open_reports"] = openReports

	rows, err := s.repos.Query(ctx, `
		SELECT to_char(date_trunc('day', created_at), 'YYYY-MM-DD') AS day, count(*)
		FROM users WHERE created_at > now() - interval '14 days' GROUP BY 1 ORDER BY 1`)
	if err == nil {
		defer rows.Close()
		series := []map[string]any{}
		for rows.Next() {
			var day string
			var n int
			if err := rows.Scan(&day, &n); err == nil {
				series = append(series, map[string]any{"day": day, "count": n})
			}
		}
		out["registrations_14d"] = series
	}
	return out, nil
}
