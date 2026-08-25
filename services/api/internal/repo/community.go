package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
)

// CommunityRepo — Q&A, follows, owner announcements (B5/B6 additions).
type CommunityRepo struct{ pool pooler }

// ---- Q&A ----

func (r *CommunityRepo) CreateQuestion(ctx context.Context, businessID, userID, text string) (*domain.Question, error) {
	q := &domain.Question{ID: newUUID(), BusinessID: businessID, UserID: userID, Text: text}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO questions (id, business_id, user_id, text) VALUES ($1, $2, $3, $4)`,
		q.ID, q.BusinessID, q.UserID, q.Text)
	if err != nil {
		return nil, err
	}
	return q, nil
}

func (r *CommunityRepo) ListQuestions(ctx context.Context, businessID string, limit, offset int) ([]*domain.Question, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT q.id, q.business_id, q.user_id, q.text, q.status, q.created_at,
			u.name, u.username
		FROM questions q JOIN users u ON u.id = q.user_id
		WHERE q.business_id = $1 ORDER BY q.created_at DESC LIMIT $2 OFFSET $3`,
		businessID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Question
	for rows.Next() {
		var q domain.Question
		if err := rows.Scan(&q.ID, &q.BusinessID, &q.UserID, &q.Text, &q.Status, &q.CreatedAt,
			&q.AuthorName, &q.AuthorUsername); err != nil {
			return nil, err
		}
		out = append(out, &q)
	}
	return out, rows.Err()
}

func (r *CommunityRepo) GetQuestion(ctx context.Context, id string) (*domain.Question, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT q.id, q.business_id, q.user_id, q.text, q.status, q.created_at, u.name, u.username
		FROM questions q JOIN users u ON u.id = q.user_id WHERE q.id = $1`, id)
	var q domain.Question
	if err := row.Scan(&q.ID, &q.BusinessID, &q.UserID, &q.Text, &q.Status, &q.CreatedAt,
		&q.AuthorName, &q.AuthorUsername); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &q, nil
}

func (r *CommunityRepo) CreateAnswer(ctx context.Context, questionID, userID, text string, isOwner bool) (*domain.Answer, error) {
	a := &domain.Answer{ID: newUUID(), QuestionID: questionID, UserID: userID, Text: text, IsOwner: isOwner}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO answers (id, question_id, user_id, text, is_owner) VALUES ($1, $2, $3, $4, $5)`,
		a.ID, a.QuestionID, a.UserID, a.Text, a.IsOwner)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// ListQuestionsWithAnswers loads a question page plus every answer for
// those questions in TWO queries (the per-question ListAnswers loop was 1+N).
func (r *CommunityRepo) ListQuestionsWithAnswers(ctx context.Context, businessID string, limit, offset int) ([]*domain.Question, error) {
	qs, err := r.ListQuestions(ctx, businessID, limit, offset)
	if err != nil || len(qs) == 0 {
		return qs, err
	}
	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.question_id, a.user_id, a.text, a.is_owner, a.created_at, u.name, u.username
		FROM answers a JOIN users u ON u.id = a.user_id
		WHERE a.question_id = ANY($1)
		ORDER BY a.is_owner DESC, a.created_at ASC`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byQuestion := make(map[string][]*domain.Answer, len(qs))
	for rows.Next() {
		var a domain.Answer
		if err := rows.Scan(&a.ID, &a.QuestionID, &a.UserID, &a.Text, &a.IsOwner, &a.CreatedAt,
			&a.AuthorName, &a.AuthorUsername); err != nil {
			return nil, err
		}
		byQuestion[a.QuestionID] = append(byQuestion[a.QuestionID], &a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, q := range qs {
		q.Answers = byQuestion[q.ID]
	}
	return qs, nil
}

func (r *CommunityRepo) ListAnswers(ctx context.Context, questionID string) ([]*domain.Answer, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.question_id, a.user_id, a.text, a.is_owner, a.created_at, u.name, u.username
		FROM answers a JOIN users u ON u.id = a.user_id
		WHERE a.question_id = $1 ORDER BY a.is_owner DESC, a.created_at ASC`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Answer
	for rows.Next() {
		var a domain.Answer
		if err := rows.Scan(&a.ID, &a.QuestionID, &a.UserID, &a.Text, &a.IsOwner, &a.CreatedAt,
			&a.AuthorName, &a.AuthorUsername); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// ---- follows ----

func (r *CommunityRepo) SetFollow(ctx context.Context, userID, businessID string, on bool) error {
	if on {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO follows (id, user_id, business_id) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, business_id) DO NOTHING`, newUUID(), userID, businessID)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`DELETE FROM follows WHERE user_id=$1 AND business_id=$2`, userID, businessID)
	return err
}

func (r *CommunityRepo) IsFollowing(ctx context.Context, userID, businessID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM follows WHERE user_id=$1 AND business_id=$2)`,
		userID, businessID).Scan(&exists)
	return exists, err
}

func (r *CommunityRepo) FollowerIDs(ctx context.Context, businessID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT user_id FROM follows WHERE business_id = $1`, businessID)
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

// CategoryFollowerIDs returns users following a category (approval fan-out).
func (r *CommunityRepo) CategoryFollowerIDs(ctx context.Context, categoryID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT user_id FROM user_category_follows WHERE category_id = $1`, categoryID)
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

// ---- owner announcements ----

func (r *CommunityRepo) CreateUpdate(ctx context.Context, businessID, authorID, title, body string) (*domain.BusinessUpdate, error) {
	u := &domain.BusinessUpdate{ID: newUUID(), BusinessID: businessID, AuthorID: authorID, Title: title, Body: body}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO business_updates (id, business_id, author_id, title, body)
		VALUES ($1, $2, $3, $4, $5)`, u.ID, u.BusinessID, u.AuthorID, u.Title, u.Body)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *CommunityRepo) ListUpdates(ctx context.Context, businessID string, limit int) ([]*domain.BusinessUpdate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, business_id, author_id, title, body, created_at
		FROM business_updates WHERE business_id = $1 ORDER BY created_at DESC LIMIT $2`,
		businessID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.BusinessUpdate
	for rows.Next() {
		var u domain.BusinessUpdate
		if err := rows.Scan(&u.ID, &u.BusinessID, &u.AuthorID, &u.Title, &u.Body, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}

func (r *CommunityRepo) UpdatesForUser(ctx context.Context, userID string, limit int) ([]*domain.BusinessUpdate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id, u.business_id, u.author_id, u.title, u.body, u.created_at, b.name, b.slug
		FROM business_updates u
		JOIN follows f ON f.business_id = u.business_id AND f.user_id = $1
		JOIN businesses b ON b.id = u.business_id AND b.status = 'verified'
		ORDER BY u.created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.BusinessUpdate
	for rows.Next() {
		var u domain.BusinessUpdate
		if err := rows.Scan(&u.ID, &u.BusinessID, &u.AuthorID, &u.Title, &u.Body, &u.CreatedAt,
			&u.BusinessName, &u.BusinessSlug); err != nil {
			return nil, err
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}
