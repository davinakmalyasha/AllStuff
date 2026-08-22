package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Businesses â€” wizard, submission, verification (PRD Â§5.4.1, Â§8.2).
type Businesses struct {
	repos *repo.Repos
}

func NewBusinesses(repos *repo.Repos) *Businesses { return &Businesses{repos: repos} }

const defaultHours = `{"mon":{"open":"09:00","close":"17:00","closed":false},"tue":{"open":"09:00","close":"17:00","closed":false},"wed":{"open":"09:00","close":"17:00","closed":false},"thu":{"open":"09:00","close":"17:00","closed":false},"fri":{"open":"09:00","close":"17:00","closed":false},"sat":{"closed":true},"sun":{"closed":true}}`

// Create starts the wizard: a draft business with sane defaults (PRD Â§5.4.1).
func (s *Businesses) Create(ctx context.Context, ownerID string) (*domain.Business, error) {
	var hours map[string]any
	_ = json.Unmarshal([]byte(defaultHours), &hours)
	b := &domain.Business{
		ID:        util.NewUUID(),
		OwnerID:   ownerID,
		Name:      "Untitled business",
		Slug:      "untitled-business",
		Currency:  "USD",
		Timezone:  "UTC",
		Hours:     hours,
		Contact:   map[string]any{},
		Status:    domain.BusinessDraft,
	}
	slug, err := s.uniqueSlug(ctx, b.Name, "")
	if err != nil {
		return nil, err
	}
	b.Slug = slug
	if err := s.repos.Businesses.Create(ctx, b, nil); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, b.ID)
}

type BusinessInput map[string]any

// Update applies wizard fields with per-field validation (PRD §8.2).
// Owners may edit at any time (PRD J4.7); slugs are never regenerated here
// (immutable, PRD §8.2) and a category switch on a verified listing sends
// it back to review (PRD §5.4.4).
func (s *Businesses) Update(ctx context.Context, ownerID, id string, in BusinessInput) (*domain.Business, error) {
	b, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}

	fields := map[string]any{}
	if v, ok := in["name"]; ok {
		name := strings.TrimSpace(asString(v))
		if n := len([]rune(name)); n < 2 || n > 80 {
			return nil, domain.ErrValidation.WithField("name", "Name must be 2–80 characters.")
		}
		fields["name"] = name
		// Slug intentionally NOT touched: URLs are stable (PRD §8.2).
	}
	if v, ok := in["tagline"]; ok {
		tagline := strings.TrimSpace(asString(v))
		if len([]rune(tagline)) > 120 {
			return nil, domain.ErrValidation.WithField("tagline", "Tagline must be 120 characters or fewer.")
		}
		fields["tagline"] = tagline
	}
	if v, ok := in["description"]; ok {
		desc := strings.TrimSpace(asString(v))
		if len([]rune(desc)) < 50 {
			return nil, domain.ErrValidation.WithField("description", "Description must be at least 50 characters.")
		}
		fields["description"] = desc
	}
	if v, ok := in["category_id"]; ok {
		cid := asString(v)
		cat, err := s.repos.Categories.GetByID(ctx, cid)
		if err != nil {
			return nil, err
		}
		if cat == nil {
			return nil, domain.ErrValidation.WithField("category_id", "Category not found.")
		}
		if cat.ParentID == nil {
			return nil, domain.ErrValidation.WithField("category_id", "Pick a sub-category (leaf).")
		}
		fields["category_id"] = cid
	}
	if v, ok := in["address"]; ok {
		addr := strings.TrimSpace(asString(v))
		if addr == "" {
			return nil, domain.ErrValidation.WithField("address", "Address is required.")
		}
		fields["address"] = addr
	}
	if v, ok := in["city"]; ok {
		fields["city"] = strings.TrimSpace(asString(v))
	}
	if v, ok := in["country"]; ok {
		fields["country"] = strings.TrimSpace(asString(v))
	}
	if v, ok := in["lat"]; ok && v != nil {
		lat, ok := asFloat(v)
		if !ok || lat < -90 || lat > 90 {
			return nil, domain.ErrValidation.WithField("lat", "Latitude must be between -90 and 90.")
		}
		fields["lat"] = lat
	}
	if v, ok := in["lng"]; ok && v != nil {
		lng, ok := asFloat(v)
		if !ok || lng < -180 || lng > 180 {
			return nil, domain.ErrValidation.WithField("lng", "Longitude must be between -180 and 180.")
		}
		fields["lng"] = lng
	}
	if v, ok := in["timezone"]; ok {
		tz := strings.TrimSpace(asString(v))
		if _, err := time.LoadLocation(tz); err != nil || tz == "" {
			return nil, domain.ErrValidation.WithField("timezone", "Invalid IANA timezone.")
		}
		fields["timezone"] = tz
	}
	if v, ok := in["hours"]; ok && v != nil {
		hours, err := validateHours(v)
		if err != nil {
			return nil, err
		}
		fields["hours"] = hours
	}
	if v, ok := in["contact"]; ok && v != nil {
		contact, err := validateContact(v)
		if err != nil {
			return nil, err
		}
		fields["contact"] = contact
	}
	if v, ok := in["logo_url"]; ok {
		fields["logo_url"] = asString(v)
	}
	if v, ok := in["cover_url"]; ok {
		fields["cover_url"] = asString(v)
	}
	if v, ok := in["gallery"]; ok && v != nil {
		gallery, err := asStringSlice(v)
		if err != nil || len(gallery) > 10 {
			return nil, domain.ErrValidation.WithField("gallery", "Gallery must be an array of media ids (max 10).")
		}
		fields["gallery"] = gallery
	}
	if v, ok := in["price_level"]; ok && v != nil {
		pl, ok := asInt(v)
		if !ok || pl < 1 || pl > 4 {
			return nil, domain.ErrValidation.WithField("price_level", "Price level must be 1â€“4.")
		}
		fields["price_level"] = pl
	}
	if v, ok := in["currency"]; ok {
		cur := strings.ToUpper(strings.TrimSpace(asString(v)))
		if len(cur) != 3 {
			return nil, domain.ErrValidation.WithField("currency", "Currency must be a 3-letter code.")
		}
		fields["currency"] = cur
	}
	if v, ok := in["tags"]; ok && v != nil {
		tags, err := asStringSlice(v)
		if err != nil || len(tags) > 10 {
			return nil, domain.ErrValidation.WithField("tags", "Tags must be an array (max 10).")
		}
		fields["tags"] = tags
	}
	if v, ok := in["founded_year"]; ok && v != nil {
		yr, ok := asInt(v)
		if !ok || yr < 1800 || yr > 2100 {
			return nil, domain.ErrValidation.WithField("founded_year", "Founded year is invalid.")
		}
		fields["founded_year"] = yr
	}
	if len(fields) == 0 {
		return nil, domain.ErrValidation.WithField("_", "Nothing to update.")
	}
	// Category switches on verified listings require re-verification
	// (PRD §5.4.4): the listing goes back to the review queue.
	if cid, changed := fields["category_id"]; changed && b.Status == domain.BusinessVerified {
		if asString(cid) != b.CategoryID {
			fields["status"] = string(domain.BusinessPending)
			fields["verification_level"] = nil
			fields["verified_at"] = nil
		}
	}
	if err := s.repos.Businesses.Update(ctx, id, fields); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

// RequestSlugChange implements PRD §8.2: slugs are immutable except for a
// single owner-requested change over the business's lifetime.
func (s *Businesses) RequestSlugChange(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	b, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if b.SlugChangedAt != nil {
		return nil, domain.ErrValidation.WithField("_", "The URL slug can only be changed once (PRD §8.2).")
	}
	slug, err := s.uniqueSlug(ctx, b.Name, b.ID)
	if err != nil {
		return nil, err
	}
	if err := s.repos.Businesses.Update(ctx, id, map[string]any{
		"slug":            slug,
		"slug_changed_at": time.Now().UTC(),
	}); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

// Submit validates the full draft (PRD Â§8.2 mandatory set) and moves to pending_review.
func (s *Businesses) Submit(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	b, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if b.Status != domain.BusinessDraft && b.Status != domain.BusinessRejected {
		return nil, domain.ErrValidation.WithField("_", "Business is already in review.")
	}
	if err := s.validateForSubmission(b); err != nil {
		return nil, err
	}
	if err := s.repos.Businesses.SetStatus(ctx, id, domain.BusinessPending, map[string]any{
		"rejection_reason": nil,
	}); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

// Resubmit after rejection (PRD Â§8.2: max 3 attempts).
func (s *Businesses) Resubmit(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	b, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if b.Status != domain.BusinessRejected {
		return nil, domain.ErrValidation.WithField("_", "Only rejected businesses can be resubmitted.")
	}
	count, err := s.repos.Businesses.ResubmitCount(ctx, id)
	if err != nil {
		return nil, err
	}
	if count >= 3 {
		return nil, domain.ErrValidation.WithField("_", "Maximum 3 resubmissions reached. Contact support (PRD §8.2).")
	}
	if err := s.validateForSubmission(b); err != nil {
		return nil, err
	}
	if err := s.repos.Businesses.SetStatus(ctx, id, domain.BusinessPending, nil); err != nil {
		return nil, err
	}
	if err := s.repos.Businesses.IncResubmitCount(ctx, id); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

// AddDocument uploads a verification document (owner, pending business).
func (s *Businesses) AddDocument(ctx context.Context, ownerID, businessID, kind, mediaID string) (*domain.VerificationDocument, error) {
	b, err := s.owned(ctx, ownerID, businessID)
	if err != nil {
		return nil, err
	}
	if b.Status != domain.BusinessDraft && b.Status != domain.BusinessRejected {
		return nil, domain.ErrValidation.WithField("_", "Documents can only be managed on drafts.")
	}
	validKinds := map[string]bool{"registration": true, "license": true, "tax_id": true, "identity": true, "utility": true}
	if !validKinds[kind] {
		return nil, domain.ErrValidation.WithField("kind", "Invalid document kind.")
	}
	d := &domain.VerificationDocument{
		ID:         util.NewUUID(),
		BusinessID: businessID,
		Kind:       kind,
		MediaID:    mediaID,
		Status:     "pending",
	}
	if err := s.repos.Businesses.AddDocument(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Businesses) RemoveDocument(ctx context.Context, ownerID, businessID, docID string) error {
	b, err := s.owned(ctx, ownerID, businessID)
	if err != nil {
		return err
	}
	if b.Status != domain.BusinessDraft && b.Status != domain.BusinessRejected {
		return domain.ErrValidation.WithField("_", "Documents can only be managed on drafts.")
	}
	return s.repos.Businesses.RemoveDocument(ctx, docID, businessID)
}

func (s *Businesses) ListDocuments(ctx context.Context, businessID string) ([]*domain.VerificationDocument, error) {
	return s.repos.Businesses.ListDocuments(ctx, businessID)
}

func (s *Businesses) GetOwned(ctx context.Context, ownerID string) ([]*domain.Business, error) {
	return s.repos.Businesses.ListByOwner(ctx, ownerID)
}

func (s *Businesses) GetOwnedOne(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	return s.owned(ctx, ownerID, id)
}

func (s *Businesses) GetPublic(ctx context.Context, slug string) (*domain.Business, error) {
	b, err := s.repos.Businesses.GetBySlug(ctx, slug)
	if err != nil || b == nil {
		return nil, domain.ErrNotFound
	}
	if b.Status == domain.BusinessClosed || b.Status == domain.BusinessDraft {
		return nil, domain.ErrNotFound
	}
	return b, nil
}

// ---- storefront draft & publish (PRD §5.4.2, §10.5) ----

// UpdateStorefront saves the draft theme/layout (auto-saved, never public).
func (s *Businesses) UpdateStorefront(ctx context.Context, ownerID, id string, theme, layout map[string]any) (*domain.Business, error) {
	if _, err := s.owned(ctx, ownerID, id); err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if theme != nil {
		fields["theme"] = theme
	}
	if layout != nil {
		fields["layout"] = layout
	}
	if len(fields) == 0 {
		return nil, domain.ErrValidation.WithField("_", "Nothing to update.")
	}
	if err := s.repos.Businesses.Update(ctx, id, fields); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

// Publish copies the draft theme/layout into published_snapshot atomically (PRD §10.5).
func (s *Businesses) Publish(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	b, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if b.Status != domain.BusinessVerified {
		return nil, domain.ErrValidation.WithField("_", "Only verified businesses can be published.")
	}
	tx, err := s.repos.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row := tx.QueryRow(ctx, `
		SELECT theme, layout FROM businesses WHERE id = $1 FOR UPDATE`, id)
	var theme, layout map[string]any
	if err := row.Scan(&theme, &layout); err != nil {
		return nil, err
	}
	snapshot := map[string]any{"theme": theme, "layout": layout}
	if _, err := tx.Exec(ctx, `
		UPDATE businesses SET published_snapshot = $2, last_published_at = now(), updated_at = now()
		WHERE id = $1`, id, snapshot); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

// Unpublish clears the snapshot; the page falls back to draft preview only for the owner.
func (s *Businesses) Unpublish(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	if _, err := s.owned(ctx, ownerID, id); err != nil {
		return nil, err
	}
	if err := s.repos.Businesses.Update(ctx, id, map[string]any{"published_snapshot": nil, "last_published_at": nil}); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

// ---- danger zone (PRD §5.4.4) ----

func (s *Businesses) Pause(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	b, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if b.Status != domain.BusinessVerified && b.Status != domain.BusinessPaused {
		return nil, domain.ErrValidation.WithField("_", "Only verified businesses can be paused.")
	}
	if err := s.repos.Businesses.SetStatus(ctx, id, domain.BusinessPaused, nil); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

func (s *Businesses) Reopen(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	b, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if b.Status != domain.BusinessPaused {
		return nil, domain.ErrValidation.WithField("_", "Only paused businesses can be reopened.")
	}
	if err := s.repos.Businesses.SetStatus(ctx, id, domain.BusinessVerified, nil); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

func (s *Businesses) Close(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	if _, err := s.owned(ctx, ownerID, id); err != nil {
		return nil, err
	}
	if err := s.repos.Businesses.SetStatus(ctx, id, domain.BusinessClosed, map[string]any{
		"published_snapshot": nil, "last_published_at": nil,
	}); err != nil {
		return nil, err
	}
	return s.repos.Businesses.GetByID(ctx, id)
}

func (s *Businesses) owned(ctx context.Context, ownerID, id string) (*domain.Business, error) {
	b, err := s.repos.Businesses.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, domain.ErrNotFound
	}
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, b.ID)
	if err != nil {
		return nil, err
	}
	if !can {
		return nil, domain.ErrForbidden
	}
	return b, nil
}

func (s *Businesses) uniqueSlug(ctx context.Context, name, excludeID string) (string, error) {
	base := slugify(name)
	slug := base
	for i := 2; ; i++ {
		taken, err := s.repos.Businesses.SlugTaken(ctx, slug, excludeID)
		if err != nil {
			return "", err
		}
		if !taken {
			return slug, nil
		}
		slug = base + "-" + util.Itoa(i)
	}
}

// validateForSubmission enforces the PRD Â§8.2 mandatory set.
func (s *Businesses) validateForSubmission(b *domain.Business) error {
	if n := len([]rune(b.Name)); n < 2 || n > 80 {
		return domain.ErrValidation.WithField("name", "Name must be 2â€“80 characters.")
	}
	if len([]rune(b.Description)) < 50 {
		return domain.ErrValidation.WithField("description", "Description must be at least 50 characters.")
	}
	if b.CategoryID == "" {
		return domain.ErrValidation.WithField("category_id", "Category is required.")
	}
	if strings.TrimSpace(b.Address) == "" || b.Lat == 0 && b.Lng == 0 {
		return domain.ErrValidation.WithField("address", "Address and coordinates are required.")
	}
	contact, _ := b.Contact["phone"].(string)
	email, _ := b.Contact["email"].(string)
	website, _ := b.Contact["website"].(string)
	if strings.TrimSpace(contact) == "" && strings.TrimSpace(email) == "" && strings.TrimSpace(website) == "" {
		return domain.ErrValidation.WithField("contact", "At least one contact method (phone, email, or website) is required.")
	}
	if b.LogoURL == nil || strings.TrimSpace(*b.LogoURL) == "" {
		return domain.ErrValidation.WithField("logo_url", "A logo is required.")
	}
	days := 0
	for _, d := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
		if day, ok := b.Hours[d].(map[string]any); ok {
			if closed, _ := day["closed"].(bool); !closed {
				days++
			}
		}
	}
	if days < 5 {
		return domain.ErrValidation.WithField("hours", "Hours must be set for at least 5 days (PRD Â§8.2).")
	}
	return nil
}

func validateHours(v any) (map[string]any, error) {
	hours, ok := v.(map[string]any)
	if !ok {
		return nil, domain.ErrValidation.WithField("hours", "Hours must be an object with day keys.")
	}
	out := map[string]any{}
	for _, d := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
		raw, ok := hours[d]
		if !ok {
			continue
		}
		day, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		closed, _ := day["closed"].(bool)
		if closed {
			out[d] = map[string]any{"closed": true}
			continue
		}
		open, _ := day["open"].(string)
		closeT, _ := day["close"].(string)
		if !validTime(open) || !validTime(closeT) {
			return nil, domain.ErrValidation.WithField("hours", "Times must be HH:MM.")
		}
		out[d] = map[string]any{"open": open, "close": closeT, "closed": false}
	}
	return out, nil
}

func validTime(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	hh, ok1 := asInt(string(s[0:2]))
	mm, ok2 := asInt(string(s[3:5]))
	return ok1 && ok2 && hh >= 0 && hh <= 23 && mm >= 0 && mm <= 59
}

func validateContact(v any) (map[string]any, error) {
	in, ok := v.(map[string]any)
	if !ok {
		return nil, domain.ErrValidation.WithField("contact", "Contact must be an object.")
	}
	out := map[string]any{}
	socials := []string{"whatsapp", "instagram", "tiktok", "facebook", "x", "youtube", "line", "telegram"}
	for _, k := range append([]string{"phone", "email", "website"}, socials...) {
		if raw, ok := in[k]; ok {
			if s, ok := raw.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out[k] = s
				}
			}
		}
	}
	return out, nil
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	}
	return 0, false
}

func asStringSlice(v any) ([]string, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, domain.ErrValidation.WithField("_", "Expected an array.")
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, domain.ErrValidation.WithField("_", "Array items must be strings.")
		}
		out = append(out, s)
	}
	return out, nil
}
