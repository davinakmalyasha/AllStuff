package service

import (
	"context"
	"strings"
	"time"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/repo"
	"allstuff/api/internal/util"
)

// Waitlist turns "the day is full" into a queue the cancellation sweep can
// serve. It deliberately does not reserve anything: a notified customer books
// through the normal flow, and the EXCLUDE constraint still decides races.
type Waitlist struct {
	repos *repo.Repos
}

func NewWaitlist(repos *repo.Repos) *Waitlist { return &Waitlist{repos: repos} }

// Join validates that the service is a published, bookable service of a live
// business, and that the requested day has not passed in the BUSINESS's
// timezone. Asking twice is idempotent.
func (w *Waitlist) Join(ctx context.Context, userID, businessID, productID, day string) (*domain.WaitlistEntry, error) {
	// Malformed ids must be a 404, not a Postgres uuid-cast 500 (and, under the
	// test transaction, an abort that turns every later query in the test into a
	// misleading 401).
	if !util.IsUUID(businessID) || !util.IsUUID(productID) {
		return nil, domain.ErrNotFound
	}
	day = strings.TrimSpace(day)
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		return nil, domain.ErrValidation.WithField("day", "Use YYYY-MM-DD.")
	}
	b, err := w.repos.Businesses.GetByID(ctx, businessID)
	if err != nil {
		return nil, err
	}
	if b == nil || (b.Status != domain.BusinessVerified && b.Status != domain.BusinessPaused) {
		return nil, domain.ErrNotFound
	}
	today := time.Now().In(loadTimezone(b.Timezone)).Format("2006-01-02")
	if parsed.Format("2006-01-02") < today {
		return nil, domain.ErrValidation.WithField("day", "That day has already passed.")
	}
	p, err := w.repos.Products.GetByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.BusinessID != businessID || p.DeletedAt != nil || !p.IsPublished {
		return nil, domain.ErrNotFound
	}
	if p.Type != domain.ProductTypeService || p.DurationMinutes == nil || *p.DurationMinutes <= 0 {
		return nil, domain.ErrValidation.WithField("product_id", "That service is not bookable.")
	}
	entry, err := w.repos.Waitlist.Join(ctx, util.NewUUID(), businessID, productID, userID, day)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		// Join is idempotent and reads the row back; a nil here means the row
		// vanished between the insert and the read.
		return nil, domain.ErrNotFound
	}
	return entry, nil
}

// Leave removes one of the caller's own entries.
func (w *Waitlist) Leave(ctx context.Context, userID, id string) error {
	if !util.IsUUID(id) {
		return domain.ErrNotFound
	}
	ok, err := w.repos.Waitlist.Leave(ctx, userID, id)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotFound
	}
	return nil
}

// Demand is the manager's view of how many people are waiting per day. It
// re-checks management on every call; a viewer (read-only analytics) does not
// get it.
func (w *Waitlist) Demand(ctx context.Context, userID, businessID string) ([]*domain.WaitlistDemand, error) {
	if !util.IsUUID(businessID) {
		return nil, domain.ErrNotFound
	}
	can, err := w.repos.Businesses.CanManageBusiness(ctx, userID, businessID)
	if err != nil {
		return nil, err
	}
	if !can {
		return nil, domain.ErrForbidden
	}
	b, err := w.repos.Businesses.GetByID(ctx, businessID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, domain.ErrNotFound
	}
	today := time.Now().In(loadTimezone(b.Timezone)).Format("2006-01-02")
	return w.repos.Waitlist.DemandForBusiness(ctx, businessID, today)
}

// Mine is the customer's own queue, today onward.
func (w *Waitlist) Mine(ctx context.Context, userID string) ([]*domain.WaitlistEntry, error) {
	return w.repos.Waitlist.ForCustomer(ctx, userID)
}
