package domain

import "time"

// ---- Q&A (business questions & answers) ----

type Question struct {
	ID             string    `json:"id"`
	BusinessID     string    `json:"business_id"`
	UserID         string    `json:"user_id"`
	Text           string    `json:"text"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	AuthorName     string    `json:"author_name"`
	AuthorUsername string    `json:"author_username"`
	Answers        []*Answer `json:"answers,omitempty"`
}

type Answer struct {
	ID             string    `json:"id"`
	QuestionID     string    `json:"question_id"`
	UserID         string    `json:"user_id"`
	Text           string    `json:"text"`
	IsOwner        bool      `json:"is_owner"`
	CreatedAt      time.Time `json:"created_at"`
	AuthorName     string    `json:"author_name"`
	AuthorUsername string    `json:"author_username"`
}

// ---- follows & owner announcements ----

type BusinessUpdate struct {
	ID           string    `json:"id"`
	BusinessID   string    `json:"business_id"`
	AuthorID     string    `json:"author_id"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	CreatedAt    time.Time `json:"created_at"`
	BusinessName string    `json:"business_name,omitempty"`
	BusinessSlug string    `json:"business_slug,omitempty"`
}
