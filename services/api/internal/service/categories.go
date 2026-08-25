package service

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Categories - tree management (PRD §5.8.3).
type Categories struct {
	repos *repo.Repos
}

func NewCategories(repos *repo.Repos) *Categories { return &Categories{repos: repos} }

var slugRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
var categoryNameRe = regexp.MustCompile(`^[\p{L}\p{N} &'(),.-]{1,60}$`)

func (s *Categories) Tree(ctx context.Context) ([]*domain.Category, error) {
	flat, err := s.repos.Categories.ListWithCounts(ctx)
	if err != nil {
		return nil, err
	}
	return buildTree(flat), nil
}

func (s *Categories) GetBySlug(ctx context.Context, slug string) (*domain.Category, []*domain.Category, error) {
	flat, err := s.repos.Categories.ListWithCounts(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID := map[string]*domain.Category{}
	for _, c := range flat {
		byID[c.ID] = c
	}
	cat, err := s.repos.Categories.GetBySlug(ctx, slug)
	if err != nil || cat == nil {
		return nil, nil, domain.ErrNotFound
	}
	var path []*domain.Category
	cur := cat
	for cur != nil {
		path = append([]*domain.Category{cur}, path...)
		if cur.ParentID != nil {
			parent := byID[*cur.ParentID]
			if parent == nil {
				break
			}
			cur = parent
		} else {
			break
		}
	}
	return cat, path, nil
}

type CategoryInput struct {
	Name        *string `json:"name"`
	Slug        *string `json:"slug"`
	Icon        *string `json:"icon"`
	Description *string `json:"description"`
	ParentID    *string `json:"parent_id"`
	SortOrder   *int    `json:"sort_order"`
}

func (s *Categories) Create(ctx context.Context, in CategoryInput) (*domain.Category, error) {
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return nil, domain.ErrValidation.WithField("name", "Name is required.")
	}
	name := strings.TrimSpace(*in.Name)
	if !categoryNameRe.MatchString(name) {
		return nil, domain.ErrValidation.WithField("name", "Name is invalid.")
	}
	slug := slugify(name)
	if in.Slug != nil && strings.TrimSpace(*in.Slug) != "" {
		slug = strings.ToLower(strings.TrimSpace(*in.Slug))
	}
	if !slugRe.MatchString(slug) {
		return nil, domain.ErrValidation.WithField("slug", "Slug must be lowercase letters, numbers, hyphens.")
	}
	if in.ParentID != nil && *in.ParentID != "" {
		parent, err := s.repos.Categories.GetByID(ctx, *in.ParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, domain.ErrValidation.WithField("parent_id", "Parent category not found.")
		}
		if parent.ParentID != nil {
			return nil, domain.ErrValidation.WithField("parent_id", "Only one nesting level is supported.")
		}
	}
	unique := slug
	for i := 2; ; i++ {
		taken, err := s.repos.Categories.SlugTaken(ctx, unique, "")
		if err != nil {
			return nil, err
		}
		if !taken {
			break
		}
		unique = slug + "-" + util.Itoa(i)
	}
	icon := "tag"
	if in.Icon != nil && strings.TrimSpace(*in.Icon) != "" {
		icon = strings.TrimSpace(*in.Icon)
	}
	cat := &domain.Category{
		ID:        util.NewUUID(),
		ParentID:  in.ParentID,
		Name:      name,
		Slug:      unique,
		Icon:      icon,
		Description: in.Description,
		SortOrder: 0,
	}
	if in.SortOrder != nil {
		cat.SortOrder = *in.SortOrder
	}
	if err := s.repos.Categories.Create(ctx, cat); err != nil {
		return nil, err
	}
	return cat, nil
}

func (s *Categories) Update(ctx context.Context, id string, in CategoryInput) (*domain.Category, error) {
	cur, err := s.repos.Categories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, domain.ErrNotFound
	}
	fields := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if !categoryNameRe.MatchString(name) {
			return nil, domain.ErrValidation.WithField("name", "Name is invalid.")
		}
		fields["name"] = name
	}
	if in.Slug != nil && strings.TrimSpace(*in.Slug) != "" {
		slug := strings.ToLower(strings.TrimSpace(*in.Slug))
		if !slugRe.MatchString(slug) {
			return nil, domain.ErrValidation.WithField("slug", "Slug is invalid.")
		}
		taken, err := s.repos.Categories.SlugTaken(ctx, slug, id)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, domain.ErrValidation.WithField("slug", "Slug already in use.")
		}
		fields["slug"] = slug
	}
	if in.Icon != nil {
		fields["icon"] = strings.TrimSpace(*in.Icon)
	}
	if in.Description != nil {
		fields["description"] = *in.Description
	}
	if in.SortOrder != nil {
		fields["sort_order"] = *in.SortOrder
	}
	if in.ParentID != nil {
		if *in.ParentID == id {
			return nil, domain.ErrValidation.WithField("parent_id", "A category cannot be its own parent.")
		}
		if *in.ParentID != "" {
			parent, err := s.repos.Categories.GetByID(ctx, *in.ParentID)
			if err != nil {
				return nil, err
			}
			if parent == nil || parent.ParentID != nil {
				return nil, domain.ErrValidation.WithField("parent_id", "Parent must exist and be a top-level category.")
			}
		}
		fields["parent_id"] = nullableString(*in.ParentID)
	}
	if len(fields) == 0 {
		return nil, domain.ErrValidation.WithField("_", "Nothing to update.")
	}
	if err := s.repos.Categories.Update(ctx, id, fields); err != nil {
		return nil, err
	}
	return s.repos.Categories.GetByID(ctx, id)
}

// Delete removes a category; businesses must be moved first (PRD §5.8.3: no orphans).
func (s *Categories) Delete(ctx context.Context, id string, forceMoveTo *string) error {	cat, err := s.repos.Categories.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if cat == nil {
		return domain.ErrNotFound
	}
	hasChildren, err := s.repos.Categories.HasChildren(ctx, id)
	if err != nil {
		return err
	}
	if hasChildren {
		return domain.ErrValidation.WithField("_", "Delete or move child categories first.")
	}
	count, err := s.repos.Categories.BusinessCount(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		if forceMoveTo == nil || *forceMoveTo == "" || *forceMoveTo == id {
			return domain.ErrValidation.WithField("_",
				"This category has businesses. Pass move_to to reassign them (PRD §5.8.3).")
		}
		target, err := s.repos.Categories.GetByID(ctx, *forceMoveTo)
		if err != nil || target == nil {
			return domain.ErrValidation.WithField("move_to", "Target category not found.")
		}
		if _, err := s.repos.Categories.MoveBusinesses(ctx, id, *forceMoveTo); err != nil {
			return err
		}
	}
	return s.repos.Categories.Delete(ctx, id)
}

// Merge moves every business and child category from one node into a target
// of the same level, then deletes the source (PRD §5.8.3).
func (s *Categories) Merge(ctx context.Context, fromID, intoID string) (*domain.Category, error) {
	if fromID == intoID {
		return nil, domain.ErrValidation.WithField("_", "Cannot merge a category into itself.")
	}
	from, err := s.repos.Categories.GetByID(ctx, fromID)
	if err != nil {
		return nil, err
	}
	if from == nil {
		return nil, domain.ErrNotFound
	}
	into, err := s.repos.Categories.GetByID(ctx, intoID)
	if err != nil {
		return nil, err
	}
	if into == nil {
		return nil, domain.ErrValidation.WithField("into_id", "Target category not found.")
	}
	if (from.ParentID == nil) != (into.ParentID == nil) {
		return nil, domain.ErrValidation.WithField("into_id", "Categories must share the same tree level.")
	}
	if _, err := s.repos.Categories.MoveBusinesses(ctx, fromID, intoID); err != nil {
		return nil, err
	}
	if _, err := s.repos.Exec(ctx,
		`UPDATE categories SET parent_id = $2 WHERE parent_id = $1`, fromID, intoID); err != nil {
		return nil, err
	}
	if err := s.repos.Categories.Delete(ctx, fromID); err != nil {
		return nil, err
	}
	return s.repos.Categories.GetByID(ctx, intoID)
}

func buildTree(flat []*domain.Category) []*domain.Category {
	byParent := map[string][]*domain.Category{}
	var roots []*domain.Category
	for _, c := range flat {
		if c.ParentID == nil || *c.ParentID == "" {
			roots = append(roots, c)
		} else {
			byParent[*c.ParentID] = append(byParent[*c.ParentID], c)
		}
	}
	for _, c := range flat {
		c.Children = byParent[c.ID]
	}
	return roots
}

// slugify: lowercase ascii, spaces -> hyphens, strip others (PRD §8.2).
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "category"
	}
	return out
}

var _ = errors.New
