package service

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
)

// Users — profile management (PRD §5.9.2).
type Users struct {
	repos *repo.Repos
}

func NewUsers(repos *repo.Repos) *Users { return &Users{repos: repos} }

var nameRe = regexp.MustCompile(`^[\p{L}\p{N} _.'-]{1,60}$`)

type UpdateProfileInput struct {
	Name         *string        `json:"name"`
	Bio          *string        `json:"bio"`
	Timezone     *string        `json:"timezone"`
	AvatarURL    *string        `json:"avatar_url"`
	ProfileLinks map[string]any `json:"profile_links"`
}

func (s *Users) UpdateProfile(ctx context.Context, user *domain.User, in UpdateProfileInput) (*domain.User, error) {
	fields := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if !nameRe.MatchString(name) {
			return nil, domain.ErrValidation.WithField("name", "Name is invalid.")
		}
		fields["name"] = name
	}
	if in.Bio != nil {
		if len([]rune(*in.Bio)) > 300 {
			return nil, domain.ErrValidation.WithField("bio", "Bio must be 300 characters or fewer.")
		}
		fields["bio"] = strings.TrimSpace(*in.Bio)
	}
	if in.Timezone != nil && strings.TrimSpace(*in.Timezone) != "" {
		fields["timezone"] = strings.TrimSpace(*in.Timezone)
	}
	if in.AvatarURL != nil {
		av := strings.TrimSpace(*in.AvatarURL)
		// Same write-time gate as logo/cover URLs: avatars render on public
		// profiles, so script-bearing schemes must not be storable.
		if av != "" && !safeMediaRef(av) {
			return nil, domain.ErrValidation.WithField("avatar_url", "Avatar must be an internal media reference or an https URL.")
		}
		fields["avatar_url"] = av
	}
	if in.ProfileLinks != nil {
		if !validProfileLinks(in.ProfileLinks) {
			return nil, domain.ErrValidation.WithField("profile_links", "Links must map known platforms to https or mailto URLs.")
		}
		fields["profile_links"] = in.ProfileLinks
	}
	if len(fields) == 0 {
		return nil, domain.ErrValidation.WithField("_", "Nothing to update.")
	}
	if err := s.repos.Users.UpdateProfile(ctx, user.ID, fields); err != nil {
		return nil, err
	}
	return s.repos.Users.GetByID(ctx, user.ID)
}

// validProfileLinks constrains the public profile_links JSONB: keys from a
// fixed platform set, values https/mailto only. These render on public
// profiles next to identity signals — free-form values would let an account
// plant script-bearing or impersonating URLs.
var profileLinkKeys = map[string]bool{
	"website": true, "instagram": true, "twitter": true, "x": true,
	"facebook": true, "linkedin": true, "tiktok": true, "youtube": true,
	"github": true, "whatsapp": true, "email": true,
}

func validProfileLinks(links map[string]any) bool {
	for k, v := range links {
		s, ok := v.(string)
		if !ok || len(s) > 300 {
			return false
		}
		if k == "email" {
			if !strings.HasPrefix(s, "mailto:") && !strings.Contains(s, "@") {
				return false
			}
			continue
		}
		if !profileLinkKeys[k] {
			return false
		}
		pu, err := url.Parse(s)
		if err != nil || (pu.Scheme != "https" && pu.Scheme != "mailto") || pu.Host == "" && pu.Scheme != "mailto" {
			return false
		}
	}
	return true
}
