package service

import (
	"context"
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
		fields["avatar_url"] = *in.AvatarURL
	}
	if in.ProfileLinks != nil {
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
