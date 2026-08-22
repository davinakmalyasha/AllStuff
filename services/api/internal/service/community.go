package service

import (
	"context"
	"strings"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
)

// Community — Q&A, follows, owner announcements (B5/B6).
type Community struct {
	repos    *repo.Repos
	notifier *Notifier
}

func NewCommunity(repos *repo.Repos, notifier *Notifier) *Community {
	return &Community{repos: repos, notifier: notifier}
}

// ---- Q&A ----

func (s *Community) Ask(ctx context.Context, userID, businessID, text string) (*domain.Question, error) {
	b, err := s.repos.Businesses.GetByID(ctx, businessID)
	if err != nil || b == nil {
		return nil, domain.ErrNotFound
	}
	text = strings.TrimSpace(text)
	if n := len([]rune(text)); n < 3 || n > 500 {
		return nil, domain.ErrValidation.WithField("text", "Question must be 3–500 characters.")
	}
	q, err := s.repos.Community.CreateQuestion(ctx, businessID, userID, text)
	if err != nil {
		return nil, err
	}
	// Notify the owner.
	if b.OwnerID != userID {
		s.notifier.Create(ctx, b.OwnerID, "question_asked", map[string]any{
			"question_id": q.ID, "business_id": businessID, "by": userID,
		})
	}
	return q, nil
}

func (s *Community) Questions(ctx context.Context, businessID string, limit, offset int) ([]*domain.Question, error) {
	list, err := s.repos.Community.ListQuestions(ctx, businessID, limit, offset)
	if err != nil {
		return nil, err
	}
	for _, q := range list {
		answers, err := s.repos.Community.ListAnswers(ctx, q.ID)
		if err != nil {
			return nil, err
		}
		q.Answers = answers
	}
	return list, nil
}

func (s *Community) Answer(ctx context.Context, userID, questionID, text string) (*domain.Answer, error) {
	q, err := s.repos.Community.GetQuestion(ctx, questionID)
	if err != nil {
		return nil, err
	}
	if q == nil {
		return nil, domain.ErrNotFound
	}
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 1000 {
		return nil, domain.ErrValidation.WithField("text", "Answer must be 1–1000 characters.")
	}
	b, _ := s.repos.Businesses.GetByID(ctx, q.BusinessID)
	isOwner := b != nil && b.OwnerID == userID
	a, err := s.repos.Community.CreateAnswer(ctx, questionID, userID, text, isOwner)
	if err != nil {
		return nil, err
	}
	if q.UserID != userID {
		s.notifier.Create(ctx, q.UserID, "question_answered", map[string]any{
			"question_id": questionID, "business_id": q.BusinessID, "by": userID,
		})
	}
	return a, nil
}

// ---- follows ----

func (s *Community) Follow(ctx context.Context, userID, businessID string, on bool) error {
	b, err := s.repos.Businesses.GetByID(ctx, businessID)
	if err != nil || b == nil {
		return domain.ErrNotFound
	}
	if b.OwnerID == userID {
		return domain.ErrValidation.WithField("_", "You can't follow your own business.")
	}
	return s.repos.Community.SetFollow(ctx, userID, businessID, on)
}

func (s *Community) Following(ctx context.Context, userID, businessID string) (bool, error) {
	return s.repos.Community.IsFollowing(ctx, userID, businessID)
}

// ---- owner announcements ----

func (s *Community) PostUpdate(ctx context.Context, ownerID, businessID, title, body string) (*domain.BusinessUpdate, error) {
	b, err := s.repos.Businesses.GetByID(ctx, businessID)
	if err != nil || b == nil {
		return nil, domain.ErrNotFound
	}
	if b.OwnerID != ownerID {
		return nil, domain.ErrForbidden
	}
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if n := len([]rune(title)); n < 3 || n > 120 {
		return nil, domain.ErrValidation.WithField("title", "Title must be 3–120 characters.")
	}
	if n := len([]rune(body)); n < 10 || n > 2000 {
		return nil, domain.ErrValidation.WithField("body", "Body must be 10–2000 characters.")
	}
	u, err := s.repos.Community.CreateUpdate(ctx, businessID, ownerID, title, body)
	if err != nil {
		return nil, err
	}
	// Notify followers (in-app).
	followers, err := s.repos.Community.FollowerIDs(ctx, businessID)
	if err == nil {
		for _, fid := range followers {
			s.notifier.Create(ctx, fid, "business_update", map[string]any{
				"update_id": u.ID, "business_id": businessID, "title": title,
			})
		}
	}
	return u, nil
}

func (s *Community) Updates(ctx context.Context, businessID string, limit int) ([]*domain.BusinessUpdate, error) {
	return s.repos.Community.ListUpdates(ctx, businessID, limit)
}

func (s *Community) FollowingFeed(ctx context.Context, userID string, limit int) ([]*domain.BusinessUpdate, error) {
	return s.repos.Community.UpdatesForUser(ctx, userID, limit)
}

// ---- category follows (PRD §5.7) ----

func (s *Community) FollowCategory(ctx context.Context, userID, categoryID string, on bool) error {
	if on {
		if _, err := s.repos.Exec(ctx, `
			INSERT INTO user_category_follows (id, user_id, category_id) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, category_id) DO NOTHING`,
			newUUID(), userID, categoryID); err != nil {
			return err
		}
		return nil
	}
	_, err := s.repos.Exec(ctx, `
		DELETE FROM user_category_follows WHERE user_id = $1 AND category_id = $2`, userID, categoryID)
	return err
}

func (s *Community) FollowingCategory(ctx context.Context, userID, categoryID string) (bool, error) {
	var one int
	err := s.repos.QueryRow(ctx, `
		SELECT 1 FROM user_category_follows WHERE user_id = $1 AND category_id = $2`,
		userID, categoryID).Scan(&one)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// CategoryFollowerIDs returns users following a category (for approval fan-out).
func (s *Community) CategoryFollowerIDs(ctx context.Context, categoryID string) ([]string, error) {
	rows, err := s.repos.Query(ctx, `
		SELECT user_id FROM user_category_follows WHERE category_id = $1`, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
