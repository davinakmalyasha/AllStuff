package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	mail "bizverse/api/internal/email"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Invites — co-owner invitations (PRD §5.9.3).
type Invites struct {
	repos *repo.Repos
	cfg   config.Config
	email mail.Sender
}

func NewInvites(repos *repo.Repos, cfg config.Config, sender mail.Sender) *Invites {
	return &Invites{repos: repos, cfg: cfg, email: sender}
}

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
	token := util.NewUUID() + "-" + util.NewUUID()
	if err := s.repos.Businesses.CreateInvite(ctx, ownerID, businessID, email, role, token); err != nil {
		return err
	}
	// Best-effort delivery: the invite row exists regardless, and the token
	// remains retrievable from the dashboard invite list (PRD §5.9.3).
	_ = s.sendInviteEmail(ctx, ownerID, businessID, email, role, token)
	return nil
}

// sendInviteEmail notifies the invitee with an accept link. Mirrors the
// branded WrapHTML template used by every other transactional email (the dev
// console sender renders its plain-text fallback by stripping tags).
func (s *Invites) sendInviteEmail(ctx context.Context, ownerID, businessID, email, role, token string) error {
	businessName := ""
	if b, err := s.repos.Businesses.GetByID(ctx, businessID); err == nil && b != nil {
		businessName = b.Name
	}
	inviterName := ""
	if u, err := s.repos.Users.GetByID(ctx, ownerID); err == nil && u != nil {
		inviterName = u.Name
	}
	roleLabel := "a co-owner"
	if role == "viewer" {
		roleLabel = "a viewer"
	}
	subject := "You've been invited to co-manage " + businessName + " on BizVerse"
	link := s.cfg.PublicURL + "/invite/" + token
	return s.email.Send(ctx, email, subject,
		mail.WrapHTML(s.cfg.PublicURL, subject,
			// Human label only; the raw token stays in the href.
			fmt.Sprintf(`<p>Hi %s,</p><p>%s has invited you to help manage <strong>%s</strong> on BizVerse as %s.</p>
			<p><a href="%s">Accept your invitation</a>.</p><p>The link expires in 7 days and can be used once.</p>`,
				htmlEscape(email), htmlEscape(inviterName), htmlEscape(businessName), roleLabel, link)))
}

// Preview serves the public invite landing page (PRD §5.9.3). Unknown,
// expired, revoked, or already-accepted invites all read as 404 so stale
// links cannot be probed. Email is returned plain so the page can tell a
// signed-in-as-wrong-account user why acceptance will fail.
func (s *Invites) Preview(ctx context.Context, token string) (map[string]any, error) {
	inv, err := s.repos.Businesses.GetInviteByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if inv == nil || inv.RevokedAt != nil || inv.AcceptedAt != nil || time.Now().After(inv.ExpiresAt) {
		return nil, domain.ErrNotFound
	}
	b, err := s.repos.Businesses.GetByID(ctx, inv.BusinessID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, domain.ErrNotFound
	}
	inviterName := ""
	if u, err := s.repos.Users.GetByID(ctx, inv.InvitedBy); err == nil && u != nil {
		inviterName = u.Name
	}
	return map[string]any{
		"business_name": b.Name,
		"inviter_name":  inviterName,
		"role":          inv.Role,
		"email":         inv.Email,
	}, nil
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
	// A deleted or suspended business must not gain new collaborators.
	// (GetByID already filters deleted_at; nil therefore covers deletion.)
	b, err := s.repos.Businesses.GetByID(ctx, inv.BusinessID)
	if err != nil {
		return err
	}
	if b == nil || b.Status == domain.BusinessSuspended {
		return domain.ErrValidation.WithField("_", "This business is no longer accepting invitations.")
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
