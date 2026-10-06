package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
)

// Engagement — likes, recommends, collections, comments, reviews, helpful
// votes, notifications (PRD §5.6, §8.4). Every action records a weighted
// signal into engagement_events for the trending engine (§5.6.3).
type Engagement struct {
	repos    *repo.Repos
	notifier *Notifier
}

// eventDay is the "today" component of an engagement_events.dedupe_key.
//
// UTC, always, and that is load-bearing rather than cosmetic.
//
// The key lands in a GLOBAL UNIQUE index (0001_initial.sql: `dedupe_key text
// NOT NULL UNIQUE`). A server-LOCAL day boundary means two replicas in different
// zones - or one TZ change at deploy - mint two different keys for the same
// user/target/signal/day, and because the index is global rather than scoped to
// a user, BOTH are accepted. The anti-gaming cap of one signal per user per
// target per day is then silently doubled, and trending.Compute's score
// double-counts the signal.
//
// jobs.go already uses `time.Now().UTC()` for its ISO-week period keys, so the
// two subsystems that both mint period keys agreed on this already; only the
// engagement path was local.
//
// The five call sites were each open-coding the same fmt.Sprintf, which is how
// four of them were missed in the first place.
func eventDay() string { return time.Now().UTC().Format("2006-01-02") }

func NewEngagement(repos *repo.Repos, notifier *Notifier) *Engagement {
	return &Engagement{repos: repos, notifier: notifier}
}

// Weights from §3 (view=1, save=3, like=5, comment=8, recommend=10, review=12, chat=8).
const (
	wSave      = 3
	wLike      = 5
	wComment   = 8
	wRecommend = 10
	wReview    = 12
)

var mentionRe = regexp.MustCompile(`@([a-z0-9_]{3,30})`)

// ---- likes ----

func (s *Engagement) ToggleLike(ctx context.Context, userID, targetType, targetID string, on bool) (bool, error) {
	if err := s.targetExists(ctx, targetType, targetID); err != nil {
		return false, err
	}
	if on && s.isOwner(ctx, userID, targetType, targetID) {
		// §8.6: owner self-actions on own business are excluded from the score.
		if err := s.repos.Engagement.SetLike(ctx, userID, targetType, targetID, true); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := s.repos.Engagement.SetLike(ctx, userID, targetType, targetID, on); err != nil {
		return false, err
	}
	if on {
		key := fmt.Sprintf("%s:%s:%s:like:%s", userID, targetType, targetID, eventDay())
		_, _ = s.repos.Engagement.InsertEvent(ctx, userID, targetType, targetID, "like", wLike, key)
	}
	return on, nil
}

func (s *Engagement) MyLikes(ctx context.Context, userID, targetType, targetID string) (bool, error) {
	return s.repos.Engagement.HasLike(ctx, userID, targetType, targetID)
}

// ---- recommends ----

func (s *Engagement) ToggleRecommend(ctx context.Context, userID, businessID string, on bool) (bool, error) {
	b, err := s.repos.Businesses.GetByID(ctx, businessID)
	if err != nil || b == nil {
		return false, domain.ErrNotFound
	}
	if on && b.OwnerID != userID {
		if err := s.repos.Engagement.SetRecommend(ctx, userID, businessID, true); err != nil {
			return false, err
		}
		key := fmt.Sprintf("%s:business:%s:recommend:%s", userID, businessID, eventDay())
		_, _ = s.repos.Engagement.InsertEvent(ctx, userID, "business", businessID, "recommend", wRecommend, key)
		return true, nil
	}
	if err := s.repos.Engagement.SetRecommend(ctx, userID, businessID, false); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Engagement) MyRecommends(ctx context.Context, userID, businessID string) (bool, error) {
	return s.repos.Engagement.HasRecommend(ctx, userID, businessID)
}

// ---- collections (PRD D3) ----

func (s *Engagement) SaveTo(ctx context.Context, userID, collectionID, targetType, targetID, note string) error {
	if collectionID == "" {
		c, err := s.repos.Engagement.DefaultCollection(ctx, userID)
		if err != nil {
			return err
		}
		collectionID = c.ID
	} else {
		c, err := s.repos.Engagement.GetCollection(ctx, userID, collectionID)
		if err != nil || c == nil {
			return domain.ErrNotFound
		}
	}
	if err := s.targetExists(ctx, targetType, targetID); err != nil {
		return err
	}
	if err := s.repos.Engagement.AddItem(ctx, collectionID, targetType, targetID, note); err != nil {
		return err
	}
	key := fmt.Sprintf("%s:%s:%s:collection_save:%s", userID, targetType, targetID, eventDay())
	_, _ = s.repos.Engagement.InsertEvent(ctx, userID, targetType, targetID, "collection_save", wSave, key)
	return nil
}

func (s *Engagement) CreateCollection(ctx context.Context, userID, name string) (*domain.Collection, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 50 {
		return nil, domain.ErrValidation.WithField("name", "Name must be 1–50 characters.")
	}
	count, err := s.collectionCount(ctx, userID)
	if err != nil {
		return nil, err
	}
	if count >= 50 {
		return nil, domain.ErrValidation.WithField("_", "Max 50 collections (PRD §7.2).")
	}
	return s.repos.Engagement.CreateCollection(ctx, userID, name)
}

func (s *Engagement) collectionCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.repos.QueryRow(ctx,
		`SELECT count(*) FROM collections WHERE user_id=$1 AND deleted_at IS NULL`, userID).Scan(&n)
	return n, err
}

func (s *Engagement) ListCollections(ctx context.Context, userID string) ([]*domain.Collection, error) {
	return s.repos.Engagement.ListCollections(ctx, userID)
}

func (s *Engagement) UpdateCollection(ctx context.Context, userID, id string, name *string, isPublic *bool) (*domain.Collection, error) {
	if _, err := s.repos.Engagement.GetCollection(ctx, userID, id); err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || len([]rune(n)) > 50 {
			return nil, domain.ErrValidation.WithField("name", "Name must be 1–50 characters.")
		}
		fields["name"] = n
	}
	if isPublic != nil {
		fields["is_public"] = *isPublic
	}
	if len(fields) == 0 {
		return nil, domain.ErrValidation.WithField("_", "Nothing to update.")
	}
	if err := s.repos.Engagement.UpdateCollection(ctx, id, fields); err != nil {
		return nil, err
	}
	return s.repos.Engagement.GetCollection(ctx, userID, id)
}

func (s *Engagement) DeleteCollection(ctx context.Context, userID, id string) error {
	c, err := s.repos.Engagement.GetCollection(ctx, userID, id)
	if err != nil {
		return err
	}
	if c == nil {
		return domain.ErrNotFound
	}
	return s.repos.Engagement.DeleteCollection(ctx, id)
}

func (s *Engagement) ListItems(ctx context.Context, userID, collectionID string) ([]*domain.CollectionItem, error) {
	if _, err := s.repos.Engagement.GetCollection(ctx, userID, collectionID); err != nil {
		return nil, err
	}
	return s.repos.Engagement.ListItems(ctx, collectionID)
}

func (s *Engagement) RemoveItem(ctx context.Context, userID, collectionID, itemID string) error {
	if _, err := s.repos.Engagement.GetCollection(ctx, userID, collectionID); err != nil {
		return err
	}
	return s.repos.Engagement.RemoveItem(ctx, collectionID, itemID)
}

func (s *Engagement) InCollections(ctx context.Context, userID, targetType, targetID string) ([]string, error) {
	return s.repos.Engagement.InCollections(ctx, userID, targetType, targetID)
}

// ---- comments (PRD §5.6.2: nested any depth, likes, mentions) ----

// requiresAcceptingActivity reports whether a listing is in a state where new
// public activity should be recorded against it.
//
// The READ side of moderation was the defect this pairs with: hiding a listing's
// page while leaving its reviews served is not moderation. But the WRITE side
// was wrong too, and in a way that looks harmless.
//
// Businesses.GetByID deliberately filters neither status nor deleted_at, because
// the owner dashboard and the admin tools must read exactly those rows. That is
// correct for an internal accessor, and it means every caller has to decide for
// itself. CreateReview, CreateComment, community.Ask and community.PostUpdate
// all resolved the target and never checked it, so an admin suspending a
// fraudulent listing did not stop it accruing reviews.
//
// Nothing new becomes PUBLIC - the readers now filter - so this is invisible
// rather than catastrophic. But it is not inert: the suspended owner keeps
// receiving notifications, engagement_events keeps feeding the trending engine,
// and the moment the listing is restored every item accumulated in the meantime
// goes live at once.
func requiresAcceptingActivity(b *domain.Business) error {
	switch b.Status {
	case domain.BusinessVerified, domain.BusinessPaused:
		return nil
	default:
		return domain.ErrValidation.WithField("_",
			"This listing is not accepting new activity right now.")
	}
}

// CreateComment records a comment on a listing.
func (s *Engagement) CreateComment(ctx context.Context, userID, businessID, parentID, text string) (*domain.Comment, error) {
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 500 {
		return nil, domain.ErrValidation.WithField("text", "Comment must be 1–500 characters.")
	}
	b, err := s.repos.Businesses.GetByID(ctx, businessID)
	if err != nil || b == nil {
		return nil, domain.ErrNotFound
	}
	if err := requiresAcceptingActivity(b); err != nil {
		return nil, err
	}
	if parentID != "" {
		parent, err := s.repos.Engagement.GetComment(ctx, parentID)
		if err != nil || parent == nil || parent.BusinessID != businessID {
			return nil, domain.ErrValidation.WithField("parent_id", "Parent comment not found in this business.")
		}
	}
	c, err := s.repos.Engagement.CreateComment(ctx, businessID, userID, parentID, text)
	if err != nil {
		return nil, err
	}
	// mentions → notifications (§5.7); dedupe repeated @names in one comment
	seenMention := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		if seenMention[m[1]] {
			continue
		}
		seenMention[m[1]] = true
		u, err := s.repos.Users.GetByUsername(ctx, m[1])
		if err == nil && u != nil && u.ID != userID {
			s.notifier.Create(ctx, u.ID, "comment_mention", map[string]any{
				"comment_id": c.ID, "business_id": businessID, "business_slug": b.Slug, "by": userID,
			})
		}
	}
	// owner notified
	if b.OwnerID != userID {
		s.notifier.Create(ctx, b.OwnerID, "comment_on_business", map[string]any{
			"comment_id": c.ID, "business_id": businessID, "business_slug": b.Slug, "by": userID,
		})
	}
	key := fmt.Sprintf("%s:business:%s:comment:%s", userID, businessID, eventDay())
	_, _ = s.repos.Engagement.InsertEvent(ctx, userID, "business", businessID, "comment", wComment, key)
	return c, nil
}

func (s *Engagement) ListComments(ctx context.Context, businessID string, limit, offset int) ([]*domain.Comment, error) {
	list, err := s.repos.Engagement.ListComments(ctx, businessID, limit, offset)
	if err != nil {
		return nil, err
	}
	return buildCommentTree(list), nil
}

func buildCommentTree(list []*domain.Comment) []*domain.Comment {
	byID := map[string]*domain.Comment{}
	var roots []*domain.Comment
	for _, c := range list {
		c.Children = []*domain.Comment{}
		byID[c.ID] = c
	}
	for _, c := range list {
		if c.ParentID != nil {
			if p, ok := byID[*c.ParentID]; ok {
				p.Children = append(p.Children, c)
				continue
			}
		}
		roots = append(roots, c)
	}
	return roots
}

func (s *Engagement) UpdateComment(ctx context.Context, userID, commentID, text string) (*domain.Comment, error) {
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 500 {
		return nil, domain.ErrValidation.WithField("text", "Comment must be 1–500 characters.")
	}
	c, err := s.repos.Engagement.GetComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, domain.ErrNotFound
	}
	if c.UserID != userID {
		return nil, domain.ErrForbidden
	}
	if time.Since(c.CreatedAt) > 10*time.Minute {
		return nil, domain.ErrValidation.WithField("_", "Comments can only be edited within 10 minutes (PRD §8.4).")
	}
	if err := s.repos.Engagement.UpdateComment(ctx, commentID, text); err != nil {
		return nil, err
	}
	return s.repos.Engagement.GetComment(ctx, commentID)
}

func (s *Engagement) DeleteComment(ctx context.Context, userID, commentID string) error {
	c, err := s.repos.Engagement.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	if c == nil {
		return domain.ErrNotFound
	}
	if c.UserID != userID {
		return domain.ErrForbidden
	}
	return s.repos.Engagement.DeleteComment(ctx, commentID)
}

func (s *Engagement) ToggleCommentLike(ctx context.Context, userID, commentID string, on bool) error {
	c, err := s.repos.Engagement.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	if c == nil {
		return domain.ErrNotFound
	}
	return s.repos.Engagement.SetCommentLike(ctx, commentID, userID, on)
}

// ---- reviews (PRD §5.6.2) ----

func (s *Engagement) CreateReview(ctx context.Context, userID, businessID string, productID *string, rating int, text string, imageIDs []string) (*domain.Review, error) {
	if rating < 1 || rating > 5 {
		return nil, domain.ErrValidation.WithField("rating", "Rating must be 1–5.")
	}
	text = strings.TrimSpace(text)
	if n := len([]rune(text)); n < 10 || n > 2000 {
		return nil, domain.ErrValidation.WithField("text", "Review must be 10–2000 characters.")
	}
	b, err := s.repos.Businesses.GetByID(ctx, businessID)
	if err != nil || b == nil {
		return nil, domain.ErrNotFound
	}
	if err := requiresAcceptingActivity(b); err != nil {
		return nil, err
	}
	if productID != nil && *productID != "" {
		p, err := s.repos.Products.GetByID(ctx, *productID)
		if err != nil || p == nil || p.BusinessID != businessID {
			return nil, domain.ErrValidation.WithField("product_id", "Product not found in this business.")
		}
	} else {
		productID = nil
	}
	existing, err := s.repos.Engagement.GetReviewByUser(ctx, businessID, productID, userID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, domain.ErrValidation.WithField("_", "You already reviewed this. Edit it instead (PRD §8.4).")
	}
	if len(imageIDs) > 6 {
		return nil, domain.ErrValidation.WithField("image_ids", "Max 6 photos per review.")
	}
	if err := s.repos.Engagement.CreateReview(ctx, businessID, productID, userID, rating, text, imageIDs); err != nil {
		// The partial unique indexes back the check-then-insert above; a
		// concurrent duplicate submit surfaces as a constraint violation —
		// translate it into a friendly 409 instead of a generic 500.
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return nil, domain.ErrValidation.WithField("_", "You already reviewed this. Edit it instead (PRD §8.4).")
		}
		return nil, err
	}
	// review event feeds trending
	key := fmt.Sprintf("%s:business:%s:review:%s", userID, businessID, eventDay())
	_, _ = s.repos.Engagement.InsertEvent(ctx, userID, "business", businessID, "review", wReview, key)
	// notify owner (product reviews get their own type, PRD §5.7)
	if b.OwnerID != userID {
		ntype := "review_posted"
		payload := map[string]any{"business_id": businessID, "business_slug": b.Slug, "by": userID, "rating": rating}
		if productID != nil && *productID != "" {
			ntype = "product_review"
			payload["product_id"] = *productID
		}
		s.notifier.Create(ctx, b.OwnerID, ntype, payload)
	}
	created, err := s.repos.Engagement.GetReviewByUser(ctx, businessID, productID, userID)
	if err != nil || created == nil {
		// The write committed; never hand a nil entity to the handler
		// (previously produced {"review": null} with a 201).
		return nil, domain.ErrInternal
	}
	return created, nil
}

func (s *Engagement) ListReviews(ctx context.Context, businessID string, productID *string, sort string, limit, offset int, viewerID *string) ([]*domain.Review, error) {
	return s.repos.Engagement.ListReviews(ctx, businessID, productID, sort, limit, offset, viewerID)
}

// MyReviews: a user's own reviews with business context (PRD §5.6.2).
func (s *Engagement) MyReviews(ctx context.Context, userID string, limit, offset int) ([]*domain.Review, error) {
	return s.repos.Engagement.MyReviews(ctx, userID, limit, offset)
}

func (s *Engagement) UpdateReview(ctx context.Context, userID, reviewID string, rating int, text string, imageIDs []string) (*domain.Review, error) {
	rw, err := s.repos.Engagement.GetReview(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	if rw == nil {
		return nil, domain.ErrNotFound
	}
	if rw.UserID != userID {
		return nil, domain.ErrForbidden
	}
	if rating < 1 || rating > 5 {
		return nil, domain.ErrValidation.WithField("rating", "Rating must be 1–5.")
	}
	text = strings.TrimSpace(text)
	if n := len([]rune(text)); n < 10 || n > 2000 {
		return nil, domain.ErrValidation.WithField("text", "Review must be 10–2000 characters.")
	}
	if len(imageIDs) > 6 {
		return nil, domain.ErrValidation.WithField("image_ids", "Max 6 photos per review.")
	}
	if err := s.repos.Engagement.UpdateReview(ctx, reviewID, userID, rating, text, imageIDs); err != nil {
		return nil, err
	}
	return s.repos.Engagement.GetReview(ctx, reviewID)
}

func (s *Engagement) DeleteReview(ctx context.Context, userID, reviewID string) error {
	rw, err := s.repos.Engagement.GetReview(ctx, reviewID)
	if err != nil {
		return err
	}
	if rw == nil {
		return domain.ErrNotFound
	}
	if rw.UserID != userID {
		return domain.ErrForbidden
	}
	return s.repos.Engagement.DeleteReview(ctx, reviewID, userID)
}

// ownerOfReview loads the review + owning business and verifies the caller
// owns that business (shared by reply create/edit/delete).
func (s *Engagement) ownerOfReview(ctx context.Context, ownerID, reviewID string) (*domain.Review, error) {
	rw, err := s.repos.Engagement.GetReview(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	if rw == nil {
		return nil, domain.ErrNotFound
	}
	if _, err := s.repos.Businesses.GetByID(ctx, rw.BusinessID); err != nil {
		return nil, err
	}
	// The nil-business case is handled by GetByID's own contract - a soft-deleted
	// listing returns no row - so this is a not-found either way.
	// CO-OWNER PARITY, as in isOwner and products.own. This is an AUTHORIZATION
	// gate, not a notification-dedup check: it decides who may delete a review. A
	// co-owner moderating their own listing's reviews is exactly the case the
	// collaborator role exists for, and owner-only here made the co-owner invite
	// half-real in a second way.
	can, err := s.repos.Businesses.CanManageBusiness(ctx, ownerID, rw.BusinessID)
	if err != nil {
		return nil, err
	}
	if !can {
		return nil, domain.ErrForbidden
	}
	return rw, nil
}

func validateReply(reply *string) (string, error) {
	out := strings.TrimSpace(*reply)
	if out == "" || len([]rune(out)) > 1000 {
		return "", domain.ErrValidation.WithField("reply", "Reply must be 1–1000 characters.")
	}
	return out, nil
}

func (s *Engagement) ReplyToReview(ctx context.Context, ownerID, reviewID, reply string) (*domain.Review, error) {
	reply, err := validateReply(&reply)
	if err != nil {
		return nil, err
	}
	rw, err := s.ownerOfReview(ctx, ownerID, reviewID)
	if err != nil {
		return nil, err
	}
	if err := s.repos.Engagement.SetReviewReply(ctx, reviewID, ownerID, reply); err != nil {
		return nil, err
	}
	s.notifier.Create(ctx, rw.UserID, "review_replied", map[string]any{
		"review_id": reviewID, "business_id": rw.BusinessID,
	})
	return s.repos.Engagement.GetReview(ctx, reviewID)
}

// EditReviewReply updates an existing reply and stamps reply_edited_at
// (PRD §5.6.2: owner replies stay editable).
func (s *Engagement) EditReviewReply(ctx context.Context, ownerID, reviewID, reply string) (*domain.Review, error) {
	reply, err := validateReply(&reply)
	if err != nil {
		return nil, err
	}
	if _, err := s.ownerOfReview(ctx, ownerID, reviewID); err != nil {
		return nil, err
	}
	if err := s.repos.Engagement.SetReviewReplyEdit(ctx, reviewID, ownerID, reply); err != nil {
		return nil, err
	}
	return s.repos.Engagement.GetReview(ctx, reviewID)
}

// DeleteReviewReply removes an owner reply.
func (s *Engagement) DeleteReviewReply(ctx context.Context, ownerID, reviewID string) error {
	if _, err := s.ownerOfReview(ctx, ownerID, reviewID); err != nil {
		return err
	}
	return s.repos.Engagement.ClearReviewReply(ctx, reviewID, ownerID)
}

func (s *Engagement) ToggleHelpful(ctx context.Context, userID, reviewID string, vote int) error {
	if vote != 1 && vote != -1 && vote != 0 {
		return domain.ErrValidation.WithField("vote", "Vote must be -1, 0 or 1.")
	}
	rw, err := s.repos.Engagement.GetReview(ctx, reviewID)
	if err != nil {
		return err
	}
	if rw == nil {
		return domain.ErrNotFound
	}
	if rw.UserID == userID {
		return domain.ErrValidation.WithField("_", "You can't vote on your own review (PRD §8.4).")
	}
	if vote == 0 {
		// Toggle-off: remove the vote row entirely.
		return s.repos.Engagement.RemoveHelpful(ctx, reviewID, userID)
	}
	if err := s.repos.Engagement.SetHelpful(ctx, reviewID, userID, vote); err != nil {
		return err
	}
	// notify the author when the vote is positive (PRD §5.7) — deduped so
	// vote toggling doesn't flood (and email) the author on every flip.
	if vote == 1 {
		s.notifier.CreateDeduped(ctx, rw.UserID, "helpful_vote",
			fmt.Sprintf("helpful:%s:%s", reviewID, userID), 30*24*time.Hour, map[string]any{
				"review_id": reviewID, "business_id": rw.BusinessID, "by": userID,
			})
	}
	return nil
}

// ---- notifications ----

func (s *Engagement) ListNotifications(ctx context.Context, userID, ntype string, limit, offset int) ([]*domain.Notification, int, error) {
	list, err := s.repos.Engagement.ListNotifications(ctx, userID, ntype, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	unread, err := s.repos.Engagement.UnreadCount(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	return list, unread, nil
}

func (s *Engagement) MarkRead(ctx context.Context, userID string, ids []string, all bool) error {
	return s.repos.Engagement.MarkNotificationsRead(ctx, userID, ids, all)
}

// ---- helpers ----

func (s *Engagement) targetExists(ctx context.Context, targetType, targetID string) error {
	switch targetType {
	case "business":
		b, err := s.repos.Businesses.GetByID(ctx, targetID)
		if err != nil || b == nil {
			return domain.ErrNotFound
		}
	case "product":
		p, err := s.repos.Products.GetByID(ctx, targetID)
		if err != nil || p == nil || p.DeletedAt != nil {
			return domain.ErrNotFound
		}
	default:
		return domain.ErrValidation.WithField("target_type", "Unknown target.")
	}
	return nil
}

// isOwner decides whether an engagement reply may be attributed to the business
// itself rather than to the individual user.
//
// CO-OWNER PARITY, the same defect products.own already fixed. This compared
// `b.OwnerID == userID`, i.e. owner-only, while every other business-scoped path
// - businesses.owned, analytics, chat quick replies, community, the catalog - goes
// through CanManageBusiness. The observable effect was that a co-owner could edit
// the storefront, read analytics and post announcements, and then have their reply
// published under their own name because the "reply as the business" affordance
// silently did not apply to them.
//
// Products.own carries the longer version of this note; this is the same bug in a
// second place, which is why it is worth stating that the rule is the PREDICATE and
// not "compare the owner_id column".
func (s *Engagement) isOwner(ctx context.Context, userID, targetType, targetID string) bool {
	if targetType == "business" {
		can, err := s.repos.Businesses.CanManageBusiness(ctx, userID, targetID)
		return err == nil && can
	}
	if targetType == "product" {
		p, err := s.repos.Products.GetByID(ctx, targetID)
		if err != nil || p == nil {
			return false
		}
		can, err := s.repos.Businesses.CanManageBusiness(ctx, userID, p.BusinessID)
		return err == nil && can
	}
	return false
}
