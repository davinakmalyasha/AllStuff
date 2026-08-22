package domain

import "time"

// ---- Messaging (PRD §7.3) ----

type ChatThread struct {
	ID            string     `json:"id"`
	Type          string     `json:"type"` // direct | business
	BusinessID    *string    `json:"business_id"`
	Status        string     `json:"status"`
	LastMessageAt *time.Time `json:"last_message_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

type ThreadListItem struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	BusinessID     *string    `json:"business_id"`
	Status         string     `json:"status"`
	LastMessageAt  *time.Time `json:"last_message_at"`
	CreatedAt      time.Time  `json:"created_at"`
	BusinessName   *string    `json:"business_name"`
	BusinessSlug   *string    `json:"business_slug"`
	BusinessLogo   *string    `json:"business_logo"`
	OtherID        *string    `json:"other_id"`
	OtherName      *string    `json:"other_name"`
	OtherUsername  *string    `json:"other_username"`
	OtherAvatar    *string    `json:"other_avatar"`
	LastBody       *string    `json:"last_body"`
	Unread         int        `json:"unread"`
	Pinned         bool       `json:"pinned"`
}

type ChatMessage struct {
	ID               int64          `json:"id"`
	ThreadID         string         `json:"thread_id"`
	SenderID         string         `json:"sender_id"`
	SenderRole       string         `json:"sender_role"`
	Type             string         `json:"type"` // text|image|file|audio|video|link|system
	Body             *string        `json:"body"`
	ReplyToID        *int64         `json:"reply_to_id"`
	ForwardedFromID  *int64         `json:"forwarded_from_message_id"`
	MediaID          *string        `json:"media_id"`
	LinkPreview      map[string]any `json:"link_preview"`
	ClientMsgID      string         `json:"client_msg_id"`
	ReadCount        int            `json:"read_count"`
	EditedAt         *time.Time     `json:"edited_at"`
	EditHistory      []map[string]any `json:"edit_history"`
	DeletedFor       string         `json:"deleted_for"` // none|me|everyone
	DeletedAt        *time.Time     `json:"deleted_at"`
	CreatedAt        time.Time      `json:"created_at"`
}

type MessageReaction struct {
	ID        string    `json:"id"`
	MessageID int64     `json:"message_id"`
	UserID    string    `json:"user_id"`
	Emoji     string    `json:"emoji"`
}

type QuickReply struct {
	ID         string `json:"id"`
	BusinessID string `json:"business_id"`
	Text       string `json:"text"`
}
