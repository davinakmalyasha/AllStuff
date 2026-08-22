package domain

import "time"

// ---- Directory (PRD §7.1) ----

type Category struct {
	ID          string    `json:"id"`
	ParentID    *string   `json:"parent_id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Icon        string    `json:"icon"`
	Description *string   `json:"description"`
	SortOrder   int       `json:"sort_order"`
	Count       int       `json:"count"` // verified businesses (computed)
	Children    []*Category `json:"children,omitempty"`
}

type BusinessStatus string

const (
	BusinessDraft        BusinessStatus = "draft"
	BusinessPending      BusinessStatus = "pending_review"
	BusinessVerified     BusinessStatus = "verified"
	BusinessRejected     BusinessStatus = "rejected"
	BusinessSuspended    BusinessStatus = "suspended"
	BusinessPaused       BusinessStatus = "paused"
	BusinessClosed       BusinessStatus = "closed"
)

type VerificationLevel string

const (
	LevelVerified      VerificationLevel = "verified"
	LevelFullyVerified VerificationLevel = "fully_verified"
)

type Business struct {
	ID                    string             `json:"id"`
	OwnerID               string             `json:"-"`
	Name                  string             `json:"name"`
	Slug                  string             `json:"slug"`
	Tagline               *string            `json:"tagline"`
	Description           string             `json:"description"`
	CategoryID            string             `json:"category_id"`
	Status                BusinessStatus     `json:"status"`
	RejectionReason       *string            `json:"rejection_reason"`
	LogoURL               *string            `json:"logo_url"`
	CoverURL              *string            `json:"cover_url"`
	Gallery               []string           `json:"gallery"`
	PriceLevel            *int               `json:"price_level"`
	Currency              string             `json:"currency"`
	Address               string             `json:"address"`
	Lat                   float64            `json:"lat"`
	Lng                   float64            `json:"lng"`
	City                  string             `json:"city"`
	Country               string             `json:"country"`
	Timezone              string             `json:"timezone"`
	Hours                 map[string]any     `json:"hours"`
	SpecialHours          map[string]any     `json:"special_hours,omitempty"`
	Amenities             []string           `json:"amenities"`
	Contact               map[string]any     `json:"contact"`
	Tags                  []string           `json:"tags"`
	FoundedYear           *int               `json:"founded_year"`
	IsFeatured            bool               `json:"is_featured"`
	LastPublishedAt       *time.Time         `json:"last_published_at"`
	PublishedSnapshot     map[string]any     `json:"published_snapshot,omitempty"`
	VerificationLevel     *VerificationLevel `json:"verification_level"`
	VerifiedAt            *time.Time         `json:"verified_at"`
	CreatedAt             time.Time          `json:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at"`

	// Computed (public page / search)
	RatingAvg     *float64 `json:"rating_avg,omitempty"`
	ReviewCount   int      `json:"review_count,omitempty"`
	LikeCount     int      `json:"like_count,omitempty"`
	RecommendCount int     `json:"recommend_count,omitempty"`
	SaveCount     int      `json:"save_count,omitempty"`
	IsOpenNow     *bool    `json:"is_open_now,omitempty"`
	DistanceKM    *float64 `json:"distance_km,omitempty"`
	CategoryName  string   `json:"category_name,omitempty"`
	CategorySlug  string   `json:"category_slug,omitempty"`
}

// ---- Media (PRD §7.4) ----

type MediaKind string

const (
	MediaLogo       MediaKind = "logo"
	MediaCover      MediaKind = "cover"
	MediaGallery    MediaKind = "gallery"
	MediaProduct    MediaKind = "product"
	MediaAvatar     MediaKind = "avatar"
	MediaDocVerif   MediaKind = "document_verification"
	// Chat media (PRD §5.5.2): images, files, voice notes, video clips.
	MediaChatImage  MediaKind = "chat_image"
	MediaChatFile   MediaKind = "chat_file"
	MediaChatAudio  MediaKind = "chat_audio"
	MediaChatVideo  MediaKind = "chat_video"
)

type MediaItem struct {
	ID          string    `json:"id"`
	UploaderID  string    `json:"-"`
	Kind        MediaKind `json:"kind"`
	OriginalName string   `json:"original_name"`
	Mime        string    `json:"mime"`
	Size        int64     `json:"size"`
	Width       *int      `json:"width"`
	Height      *int      `json:"height"`
	Path        string    `json:"-"`
	URL         string    `json:"url"`
	ThumbURL    *string   `json:"thumb_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ---- Products & variants (PRD §7.1) ----

type ProductType string

const (
	ProductTypeProduct ProductType = "product"
	ProductTypeService ProductType = "service"
)

type Product struct {
	ID            string      `json:"id"`
	BusinessID    string      `json:"business_id"`
	Type          ProductType `json:"type"`
	Name          string      `json:"name"`
	Description   *string     `json:"description"`
	Currency      string      `json:"currency"`
	BasePrice     *float64    `json:"base_price"`
	CallForPrice  bool        `json:"call_for_price"`
	CoverImageID  *string     `json:"cover_image_id"`
	ImageIDs      []string    `json:"image_ids"`
	Tags          []string    `json:"tags"`
	IsAvailable   bool        `json:"is_available"`
	IsFeatured    bool        `json:"is_featured"`
	FeaturedOrder *int        `json:"featured_order"`
	SortOrder     int         `json:"sort_order"`
	IsPublished   bool        `json:"is_published"`
	Badge         string      `json:"badge"`
	SeoTitle      *string     `json:"seo_title"`
	DeletedAt     *time.Time  `json:"-"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`

	// Nested (owner views)
	Options  []*ProductOption  `json:"options,omitempty"`
	Variants []*ProductVariant `json:"variants,omitempty"`
}

type ProductOption struct {
	ID        string   `json:"id"`
	ProductID string   `json:"product_id"`
	Name      string   `json:"name"`
	Values    []string `json:"values"`
	SortOrder int      `json:"sort_order"`
}

type ProductVariant struct {
	ID        string         `json:"id"`
	ProductID string         `json:"product_id"`
	Name      string         `json:"name"`
	SKU       string         `json:"sku"`
	Options   map[string]any `json:"options"`
	Price     *float64       `json:"price"`
	Currency  string         `json:"currency"`
	StockQty  *int           `json:"stock_qty"`
	InStock   bool           `json:"in_stock"`
	ImageID   *string        `json:"image_id"`
	SortOrder int            `json:"sort_order"`
}

// ---- Trending (PRD §5.6.3) ----

type TrendEntry struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Slug               string              `json:"slug"`
	LogoURL            *string             `json:"logo_url"`
	City               string              `json:"city"`
	Category           *string             `json:"category"`
	Score              float64             `json:"score"`
	Velocity           float64             `json:"velocity"`
	IsBooming          bool                `json:"is_booming"`
	IsRising           bool                `json:"is_rising"`
	RankCategory       int                 `json:"rank_category"`
	RankCity           int                 `json:"rank_city"`
	VerificationLevel  *VerificationLevel  `json:"verification_level"`
}

// ---- Verification documents (PRD §7.4) ----

type VerificationDocument struct {
	ID         string    `json:"id"`
	BusinessID string    `json:"business_id"`
	Kind       string    `json:"kind"`
	MediaID    string    `json:"media_id"`
	Status     string    `json:"status"`
	ReviewNote *string   `json:"review_note"`
	ReviewedAt *time.Time `json:"reviewed_at"`
	CreatedAt  time.Time `json:"created_at"`
	FileName   string    `json:"file_name,omitempty"`
}
