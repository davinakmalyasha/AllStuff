package domain

import "time"

type UserStatus string

const (
	UserStatusActive    UserStatus = "active"
	UserStatusSuspended UserStatus = "suspended"
	UserStatusBanned    UserStatus = "banned"
)

type UserRole string

const (
	RoleUser  UserRole = "user"
	RoleAdmin UserRole = "admin"
)

// User mirrors the users table (PRD §7.1).
type User struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	PasswordHash    string     `json:"-"`
	Name            string     `json:"name"`
	Username        string     `json:"username"`
	AvatarURL       *string    `json:"avatar_url"`
	Bio             *string    `json:"bio"`
	Timezone        string     `json:"timezone"`
	ProfileLinks    map[string]any `json:"profile_links,omitempty"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	Role            UserRole   `json:"role"`
	Status          UserStatus `json:"status"`
	SuspendedUntil  *time.Time `json:"suspended_until"`
	BanReason       *string    `json:"ban_reason"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (u *User) IsAdmin() bool         { return u.Role == RoleAdmin }
func (u *User) EmailVerified() bool   { return u.EmailVerifiedAt != nil }

type Claims struct {
	UserID    string
	Role      UserRole
	Username  string
	TokenType string // access | verify_email | reset_password
	ExpiresAt time.Time
	TokenID   string
}

type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	TokenHash  string    `json:"-"`
	IP         *string   `json:"ip"`
	UserAgent  *string   `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}
