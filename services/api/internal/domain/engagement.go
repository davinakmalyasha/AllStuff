package domain

import "time"

// ---- Engagement (PRD §7.2) ----

type Collection struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	IsPublic  bool      `json:"is_public"`
	ItemCount int       `json:"item_count"`
	CreatedAt time.Time `json:"created_at"`
}

type CollectionItem struct {
	ID           string    `json:"id"`
	CollectionID string    `json:"collection_id"`
	TargetType   string    `json:"target_type"`
	TargetID     string    `json:"target_id"`
	Note         *string   `json:"note"`
	CreatedAt    time.Time `json:"created_at"`
	TargetName   string    `json:"target_name,omitempty"`
	TargetSlug   string    `json:"target_slug,omitempty"`
	TargetLogo   string    `json:"target_logo,omitempty"`
}

type Comment struct {
	ID             string     `json:"id"`
	BusinessID     string     `json:"business_id"`
	UserID         string     `json:"user_id"`
	ParentID       *string    `json:"parent_id"`
	Text           string     `json:"text"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	AuthorName     string     `json:"author_name"`
	AuthorUsername string     `json:"author_username"`
	AuthorAvatar   *string    `json:"author_avatar"`
	LikeCount      int        `json:"like_count"`
	Children       []*Comment `json:"children,omitempty"`
}

type Review struct {
	ID             string     `json:"id"`
	ImageIDs       []string   `json:"image_ids"`
	BusinessID     string     `json:"business_id"`
	ProductID      *string    `json:"product_id"`
	UserID         string     `json:"user_id"`
	Rating         int        `json:"rating"`
	Text           string     `json:"text"`
	Reply          *string    `json:"reply"`
	ReplyAt        *time.Time `json:"reply_at"`
	ReplyEditedAt  *time.Time `json:"reply_edited_at"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	AuthorName     string     `json:"author_name"`
	AuthorUsername string     `json:"author_username"`
	AuthorAvatar   *string    `json:"author_avatar"`
	HelpfulCount   int        `json:"helpful_count"`
	MyVote         *int       `json:"my_vote,omitempty"` // viewer's own vote: 1 | -1 | nil
	BusinessName   string     `json:"business_name,omitempty"`
	BusinessSlug   string     `json:"business_slug,omitempty"`
}

type Notification struct {
	ID        string         `json:"id"`
	UserID    string         `json:"user_id"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	IsRead    bool           `json:"is_read"`
	CreatedAt time.Time      `json:"created_at"`
}
