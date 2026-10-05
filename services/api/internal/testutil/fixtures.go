package testutil

import (
	"context"
	"testing"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/util"
)

// Fixtures. Deliberately thin: each helper produces the MINIMUM valid row for
// one concept, so a test reads as a statement about behaviour rather than a
// recital of NOT NULL constraints.
//
// Anything more elaborate belongs in a test that is about that specific
// structure. A shared builder that grows optional fields is how fixtures end up
// encoding the very bug they are supposed to detect.

// fixtureCtx bounds fixture writes. Fixtures are not the subject of a test, so
// they do not need the generous budget the test body gets, and a short one turns
// a hung fixture into a fast, obvious failure.
func fixtureCtx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	// Cancelling on a timer would abort the write it is protecting; the context
	// is deliberately never cancelled and relies on the timeout alone.
	_ = cancel
	return ctx
}

// UserOpts are the knobs a test needs to vary on a user.
type UserOpts struct {
	// Role defaults to a normal user.
	Role domain.UserRole
	// Unverified leaves email_verified_at NULL, which is the state every
	// "you must verify your email first" path exists for.
	Unverified bool
	// Banned and Suspended cover the states that must suppress API keys and
	// engagement writes.
	Banned    bool
	Suspended bool
}

// User inserts a user, active and email-verified unless told otherwise.
func User(t *testing.T, h *H) *domain.User {
	t.Helper()
	return UserOpts_(t, h, UserOpts{})
}

// UserOpts_ inserts a user with the given state. The trailing underscore avoids
// colliding with the type name, which Go has no way to disambiguate here.
func UserOpts_(t *testing.T, h *H, o UserOpts) *domain.User {
	t.Helper()
	if o.Role == "" {
		o.Role = domain.RoleUser
	}
	u := &domain.User{
		ID:           util.NewUUID(),
		Email:        util.NewUUID() + "@example.test",
		PasswordHash: "x",
		Name:         "Test User",
		Username:     "u" + util.NewUUID()[:8],
		Timezone:     "UTC",
		Role:         o.Role,
		Status:       domain.UserStatusActive,
	}
	if o.Banned {
		u.Status = domain.UserStatusBanned
	} else if o.Suspended {
		u.Status = domain.UserStatusSuspended
	}
	if !o.Unverified {
		now := time.Now()
		u.EmailVerifiedAt = &now
	}
	if err := h.Repos.Users.Create(fixtureCtx(), u); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

// Admin inserts an active user carrying the admin role.
func Admin(t *testing.T, h *H) *domain.User {
	t.Helper()
	return UserOpts_(t, h, UserOpts{Role: domain.RoleAdmin})
}

// Category inserts a top-level category.
func Category(t *testing.T, h *H, name string) *domain.Category {
	t.Helper()
	c := &domain.Category{
		ID:        util.NewUUID(),
		Name:      name,
		Slug:      "cat-" + util.NewUUID()[:10],
		Icon:      "store",
		SortOrder: 1,
	}
	if err := h.Repos.Categories.Create(fixtureCtx(), c); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	return c
}

// ChildCategory inserts a category nested under parent.
//
// Separate from Category because the parent/child split is a real schema feature
// (categories.parent_id) rather than a test convenience: the catalogue is two
// levels deep in production, and any test about how counts aggregate has to be
// able to build a hierarchy to aggregate over.
func ChildCategory(t *testing.T, h *H, parent *domain.Category, name string) *domain.Category {
	t.Helper()
	c := &domain.Category{
		ID:        util.NewUUID(),
		ParentID:  &parent.ID,
		Name:      name,
		Slug:      "cat-" + util.NewUUID()[:10],
		Icon:      "store",
		SortOrder: 1,
	}
	if err := h.Repos.Categories.Create(fixtureCtx(), c); err != nil {
		t.Fatalf("seed child category: %v", err)
	}
	return c
}

// BusinessOpts are the knobs a test needs to vary. Everything else defaults to a
// verified, owned, located listing, because that is what almost every query
// filters to — a fixture that defaulted to anything else would make every test
// that forgot to set the status look like a search bug.
type BusinessOpts struct {
	Name     string
	Category *domain.Category
	Owner    *domain.User // nil leaves the business UNOWNED, which is a real
	// state now that migration 0029 made owner_id nullable, and the state
	// claim-an-existing-listing depends on
	Status   domain.BusinessStatus
	City     string
	Country  string
	Keywords []string
	// Level leaves the business unverified unless set.
	Level domain.VerificationLevel
	Lat   float64
	Lng   float64
	// Slug, when set, is used verbatim. Useful for tests that need a collision.
	Slug string
}

// Business inserts a business.
func Business(t *testing.T, h *H, o BusinessOpts) *domain.Business {
	t.Helper()
	if o.Name == "" {
		o.Name = "Test Business " + util.NewUUID()[:8]
	}
	if o.Status == "" {
		o.Status = domain.BusinessVerified
	}
	if o.City == "" {
		o.City = "Jakarta"
	}
	if o.Country == "" {
		o.Country = "Indonesia"
	}
	if o.Category == nil {
		o.Category = Category(t, h, "Cat "+util.NewUUID()[:6])
	}
	if o.Keywords == nil {
		o.Keywords = []string{}
	}
	if o.Slug == "" {
		o.Slug = "biz-" + util.NewUUID()[:12]
	}
	if o.Lat == 0 && o.Lng == 0 {
		o.Lat, o.Lng = -6.2, 106.8
	}
	tagline := "a tagline about " + o.Name

	b := &domain.Business{
		ID:                util.NewUUID(),
		Name:              o.Name,
		Slug:              o.Slug,
		Tagline:           &tagline,
		Description:       "a description long enough to clear any minimum length the service layer or the schema imposes on a business listing",
		CategoryID:        o.Category.ID,
		Status:            o.Status,
		Currency:          "USD",
		Address:           "1 Test Street",
		Lat:               o.Lat,
		Lng:               o.Lng,
		City:              o.City,
		Country:           o.Country,
		Timezone:          "Asia/Jakarta",
		Hours:             DefaultHours(),
		Contact:           map[string]any{"phone": "+62-21-5550101", "email": "hello@example.test"},
		Tags:              o.Keywords,
		Gallery:           []string{},
		Amenities:         []string{},
		VerificationLevel: nil,
	}
	if o.Owner != nil {
		b.OwnerID = o.Owner.ID
	}
	if o.Level != "" {
		lvl := o.Level
		b.VerificationLevel = &lvl
	}
	// Create takes the category separately, so a business can be created before
	// its category exists — which is what the wizard does when an owner picks a
	// leaf category at the last step.
	if err := h.Repos.Businesses.Create(fixtureCtx(), b, &o.Category.ID); err != nil {
		t.Fatalf("seed business: %v", err)
	}
	return b
}

// Product inserts a published, available product.
func Product(t *testing.T, h *H, businessID, name string) *domain.Product {
	t.Helper()
	p := &domain.Product{
		ID:          util.NewUUID(),
		BusinessID:  businessID,
		Type:        domain.ProductTypeProduct,
		Name:        name,
		Currency:    "USD",
		Tags:        []string{},
		ImageIDs:    []string{},
		IsPublished: true,
		IsAvailable: true,
	}
	if err := h.Repos.Products.Create(fixtureCtx(), p); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return p
}

// Review inserts a visible review and returns it.
//
// The repository mints the id, so the row is read back rather than fabricated.
// (business, user) is unique by schema, so GetReviewByUser is the natural key and
// a second read is unnecessary.
func Review(t *testing.T, h *H, businessID, userID string, rating int) *domain.Review {
	t.Helper()
	if err := h.Repos.Engagement.CreateReview(fixtureCtx(), businessID, nil, userID, rating,
		"a review body long enough to clear the minimum length the schema imposes on review text", nil); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	r, err := h.Repos.Engagement.GetReviewByUser(fixtureCtx(), businessID, nil, userID)
	if err != nil {
		t.Fatalf("read back seeded review: %v", err)
	}
	return r
}

// DefaultHours is a 09:00-17:00 week, matching what the wizard seeds.
func DefaultHours() map[string]any {
	day := func(closed bool) map[string]any {
		if closed {
			return map[string]any{"closed": true}
		}
		return map[string]any{"open": "09:00", "close": "17:00", "closed": false}
	}
	return map[string]any{
		"mon": day(false), "tue": day(false), "wed": day(false),
		"thu": day(false), "fri": day(false), "sat": day(false), "sun": day(false),
	}
}
