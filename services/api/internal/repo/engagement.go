package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/util"
)

// EngagementRepo — likes, recommends, collections, comments, reviews,
// helpful votes, notifications, engagement events (PRD §7.2, §7.4).
type EngagementRepo struct{ pool pooler }

// ---- likes / recommends ----

func (r *EngagementRepo) SetLike(ctx context.Context, userID, targetType, targetID string, on bool) error {
	if on {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO likes (id, user_id, target_type, target_id)
			VALUES ($1, $2, $3, $4) ON CONFLICT (user_id, target_type, target_id) DO NOTHING`,
			util.NewUUID(), userID, targetType, targetID)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`DELETE FROM likes WHERE user_id = $1 AND target_type = $2 AND target_id = $3`,
		userID, targetType, targetID)
	return err
}

func (r *EngagementRepo) HasLike(ctx context.Context, userID, targetType, targetID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM likes WHERE user_id=$1 AND target_type=$2 AND target_id=$3)`,
		userID, targetType, targetID).Scan(&exists)
	return exists, err
}

func (r *EngagementRepo) SetRecommend(ctx context.Context, userID, businessID string, on bool) error {
	if on {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO recommends (id, user_id, business_id) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, business_id) DO NOTHING`, util.NewUUID(), userID, businessID)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`DELETE FROM recommends WHERE user_id = $1 AND business_id = $2`, userID, businessID)
	return err
}

func (r *EngagementRepo) HasRecommend(ctx context.Context, userID, businessID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM recommends WHERE user_id=$1 AND business_id=$2)`,
		userID, businessID).Scan(&exists)
	return exists, err
}

// ---- collections (PRD D3) ----

func (r *EngagementRepo) CreateCollection(ctx context.Context, userID, name string) (*domain.Collection, error) {
	c := &domain.Collection{ID: util.NewUUID(), UserID: userID, Name: name}
	slug, err := r.collectionSlug(ctx, userID, name)
	if err != nil {
		return nil, err
	}
	c.Slug = slug
	_, err = r.pool.Exec(ctx, `
		INSERT INTO collections (id, user_id, name, slug) VALUES ($1, $2, $3, $4)`,
		c.ID, c.UserID, c.Name, c.Slug)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (r *EngagementRepo) collectionSlug(ctx context.Context, userID, name string) (string, error) {
	base := slugFromName(name)
	slug := base
	for i := 2; ; i++ {
		var exists bool
		err := r.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM collections WHERE user_id=$1 AND slug=$2)`,
			userID, slug).Scan(&exists)
		if err != nil {
			return "", err
		}
		if !exists {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

// slugFromName: lowercase ascii, spaces → hyphens, strip the rest.
func slugFromName(s string) string {
	out := ""
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			out += string(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if !prevDash {
				out += "-"
				prevDash = true
			}
		}
	}
	out = strings.Trim(out, "-")
	if out == "" {
		out = "collection"
	}
	return out
}

// DefaultCollection returns or creates the user's "Favorites" collection.
func (r *EngagementRepo) DefaultCollection(ctx context.Context, userID string) (*domain.Collection, error) {
	var c domain.Collection
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, name, slug, is_public, created_at FROM collections
		WHERE user_id=$1 AND is_default = true AND deleted_at IS NULL`, userID).
		Scan(&c.ID, &c.UserID, &c.Name, &c.Slug, &c.IsPublic, &c.CreatedAt)
	if err == nil {
		return &c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// create default
	c = domain.Collection{ID: util.NewUUID(), UserID: userID, Name: "Favorites", Slug: "favorites", IsPublic: false}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO collections (id, user_id, name, slug, is_default)
		VALUES ($1, $2, $3, $4, true)`, c.ID, c.UserID, c.Name, c.Slug)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *EngagementRepo) ListCollections(ctx context.Context, userID string) ([]*domain.Collection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.user_id, c.name, c.slug, c.is_public, c.created_at,
			(SELECT count(*) FROM collection_items i WHERE i.collection_id = c.id) AS item_count
		FROM collections c
		WHERE c.user_id=$1 AND c.deleted_at IS NULL
		ORDER BY c.is_default DESC, c.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Collection
	for rows.Next() {
		var c domain.Collection
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.Slug, &c.IsPublic, &c.CreatedAt, &c.ItemCount); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *EngagementRepo) GetCollection(ctx context.Context, userID, id string) (*domain.Collection, error) {
	var c domain.Collection
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, name, slug, is_public, created_at FROM collections
		WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID).
		Scan(&c.ID, &c.UserID, &c.Name, &c.Slug, &c.IsPublic, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

func (r *EngagementRepo) UpdateCollection(ctx context.Context, id string, fields map[string]any) error {
	cols := []string{"updated_at = now()"}
	args := []any{id}
	for k, v := range fields {
		args = append(args, v)
		cols = append(cols, k+" = $"+util.Itoa(len(args)))
	}
	_, err := r.pool.Exec(ctx,
		"UPDATE collections SET "+joinComma(cols)+" WHERE id = $1", args...)
	return err
}

func (r *EngagementRepo) DeleteCollection(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM collections WHERE id = $1`, id)
	return err
}

func (r *EngagementRepo) AddItem(ctx context.Context, collectionID, targetType, targetID, note string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO collection_items (id, collection_id, target_type, target_id, note)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (collection_id, target_type, target_id) DO NOTHING`,
		util.NewUUID(), collectionID, targetType, targetID, note)
	return err
}

func (r *EngagementRepo) RemoveItem(ctx context.Context, collectionID, itemID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM collection_items WHERE collection_id=$1 AND id=$2`, collectionID, itemID)
	return err
}

func (r *EngagementRepo) ListItems(ctx context.Context, collectionID string) ([]*domain.CollectionItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT i.id, i.collection_id, i.target_type, i.target_id, i.note, i.created_at,
			coalesce(b.name, p.name, '') AS target_name,
			coalesce(b.slug, p2.slug, '') AS target_slug,
			coalesce(b.logo_url, '') AS target_logo
		FROM collection_items i
		LEFT JOIN businesses b ON i.target_type = 'business' AND b.id = i.target_id AND b.deleted_at IS NULL
		LEFT JOIN products p ON i.target_type = 'product' AND p.id = i.target_id AND p.deleted_at IS NULL
		LEFT JOIN businesses p2 ON p2.id = p.business_id
		WHERE i.collection_id = $1 ORDER BY i.sort_order, i.created_at DESC`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.CollectionItem
	for rows.Next() {
		var it domain.CollectionItem
		var name, slug, logo string
		if err := rows.Scan(&it.ID, &it.CollectionID, &it.TargetType, &it.TargetID, &it.Note, &it.CreatedAt,
			&name, &slug, &logo); err != nil {
			return nil, err
		}
		it.TargetName = name
		it.TargetSlug = slug
		it.TargetLogo = logo
		out = append(out, &it)
	}
	return out, rows.Err()
}

func (r *EngagementRepo) InCollections(ctx context.Context, userID, targetType, targetID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id FROM collection_items i JOIN collections c ON c.id = i.collection_id
		WHERE c.user_id=$1 AND i.target_type=$2 AND i.target_id=$3 AND c.deleted_at IS NULL`, userID, targetType, targetID)
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

// ---- comments (nested, PRD §5.6.2) ----

func (r *EngagementRepo) CreateComment(ctx context.Context, businessID, userID, parentID, text string) (*domain.Comment, error) {
	var parent *string
	if parentID != "" {
		parent = &parentID
	}
	c := &domain.Comment{ID: util.NewUUID(), BusinessID: businessID, UserID: userID, ParentID: parent, Text: text}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO comments (id, business_id, user_id, parent_id, text) VALUES ($1,$2,$3,$4,$5)`,
		c.ID, c.BusinessID, c.UserID, c.ParentID, c.Text)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// MyReviews lists a user's reviews with business context (PRD §5.6.2).
func (r *EngagementRepo) MyReviews(ctx context.Context, userID string, limit, offset int) ([]*domain.Review, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.id, r.business_id, r.product_id, r.user_id, r.rating, r.text, r.image_ids, r.reply, r.reply_at, r.reply_edited_at,
			r.status, r.created_at, u.name, u.username, u.avatar_url,
			(SELECT coalesce(sum(vote),0) FROM review_helpful_votes v WHERE v.review_id = r.id) AS helpful_count,
			b.name AS business_name, b.slug AS business_slug
		FROM reviews r JOIN users u ON u.id = r.user_id
		JOIN businesses b ON b.id = r.business_id
		WHERE r.user_id = $1 AND r.deleted_at IS NULL
		ORDER BY r.created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Review
	for rows.Next() {
		var rv domain.Review
		if err := rows.Scan(&rv.ID, &rv.BusinessID, &rv.ProductID, &rv.UserID, &rv.Rating, &rv.Text, &rv.ImageIDs,
			&rv.Reply, &rv.ReplyAt, &rv.ReplyEditedAt, &rv.Status, &rv.CreatedAt, &rv.AuthorName, &rv.AuthorUsername, &rv.AuthorAvatar,
			&rv.HelpfulCount, &rv.BusinessName, &rv.BusinessSlug); err != nil {
			return nil, err
		}
		out = append(out, &rv)
	}
	return out, rows.Err()
}

func (r *EngagementRepo) ListComments(ctx context.Context, businessID string, limit, offset int) ([]*domain.Comment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.business_id, c.user_id, c.parent_id, c.text, c.status, c.created_at,
			u.name, u.username, u.avatar_url,
			(SELECT count(*) FROM comment_likes cl WHERE cl.comment_id = c.id) AS like_count
		FROM comments c JOIN users u ON u.id = c.user_id
		WHERE c.business_id = $1 AND c.status = 'visible'
		ORDER BY c.created_at ASC
		LIMIT $2 OFFSET $3`, businessID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanComments(rows)
}

func scanComments(rows pgx.Rows) ([]*domain.Comment, error) {
	var out []*domain.Comment
	for rows.Next() {
		var c domain.Comment
		if err := rows.Scan(&c.ID, &c.BusinessID, &c.UserID, &c.ParentID, &c.Text, &c.Status,
			&c.CreatedAt, &c.AuthorName, &c.AuthorUsername, &c.AuthorAvatar, &c.LikeCount); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *EngagementRepo) GetComment(ctx context.Context, id string) (*domain.Comment, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT c.id, c.business_id, c.user_id, c.parent_id, c.text, c.status, c.created_at,
			u.name, u.username, u.avatar_url, 0
		FROM comments c JOIN users u ON u.id = c.user_id WHERE c.id = $1`, id)
	c, err := scanComment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func scanComment(row pgx.Row) (*domain.Comment, error) {
	var c domain.Comment
	if err := row.Scan(&c.ID, &c.BusinessID, &c.UserID, &c.ParentID, &c.Text, &c.Status,
		&c.CreatedAt, &c.AuthorName, &c.AuthorUsername, &c.AuthorAvatar, &c.LikeCount); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *EngagementRepo) UpdateComment(ctx context.Context, id, text string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE comments SET text=$2, updated_at=now() WHERE id=$1`, id, text)
	return err
}

func (r *EngagementRepo) DeleteComment(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM comments WHERE id = $1`, id)
	return err
}

func (r *EngagementRepo) SetCommentLike(ctx context.Context, commentID, userID string, on bool) error {
	if on {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO comment_likes (id, comment_id, user_id) VALUES ($1,$2,$3)
			ON CONFLICT (comment_id, user_id) DO NOTHING`, util.NewUUID(), commentID, userID)
		return err
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM comment_likes WHERE comment_id=$1 AND user_id=$2`, commentID, userID)
	return err
}

// ---- reviews (business + product, PRD §5.6.2) ----

func (r *EngagementRepo) CreateReview(ctx context.Context, businessID string, productID *string, userID string, rating int, text string, imageIDs []string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO reviews (id, business_id, product_id, user_id, rating, text, image_ids)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		util.NewUUID(), businessID, productID, userID, rating, text, imageIDs)
	return err
}

func (r *EngagementRepo) GetReviewByUser(ctx context.Context, businessID string, productID *string, userID string) (*domain.Review, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT r.id, r.business_id, r.product_id, r.user_id, r.rating, r.text, r.image_ids, r.reply, r.reply_at, r.reply_edited_at,
			r.status, r.created_at, u.name, u.username, u.avatar_url
		FROM reviews r JOIN users u ON u.id = r.user_id
		WHERE r.business_id=$1 AND r.deleted_at IS NULL
		  AND (NULLIF($2::text,'') IS NULL AND r.product_id IS NULL OR r.product_id::text = $2::text)
		  AND r.user_id=$3`, businessID, deref(productID), userID)
	rw, err := scanReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return rw, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *EngagementRepo) ListReviews(ctx context.Context, businessID string, productID *string, sort string, limit, offset int, viewerID *string) ([]*domain.Review, error) {
	order := "r.created_at DESC"
	switch sort {
	case "highest":
		order = "r.rating DESC, r.created_at DESC"
	case "helpful":
		order = "helpful_count DESC, r.created_at DESC"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT r.id, r.business_id, r.product_id, r.user_id, r.rating, r.text, r.image_ids, r.reply, r.reply_at, r.reply_edited_at,
			r.status, r.created_at, u.name, u.username, u.avatar_url,
			(SELECT coalesce(sum(vote),0) FROM review_helpful_votes v WHERE v.review_id = r.id) AS helpful_count,
			(SELECT vote FROM review_helpful_votes v WHERE v.review_id = r.id AND v.user_id = NULLIF($5::text,'')::uuid) AS my_vote
		FROM reviews r JOIN users u ON u.id = r.user_id
		WHERE r.business_id=$1 AND r.deleted_at IS NULL
		  AND (NULLIF($2::text,'') IS NULL AND r.product_id IS NULL OR r.product_id::text = $2::text)
		ORDER BY `+order+` LIMIT $3 OFFSET $4`, businessID, deref(productID), limit, offset, deref(viewerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Review
	for rows.Next() {
		var rw domain.Review
		if err := rows.Scan(&rw.ID, &rw.BusinessID, &rw.ProductID, &rw.UserID, &rw.Rating, &rw.Text,
			&rw.ImageIDs, &rw.Reply, &rw.ReplyAt, &rw.ReplyEditedAt, &rw.Status, &rw.CreatedAt, &rw.AuthorName, &rw.AuthorUsername,
			&rw.AuthorAvatar, &rw.HelpfulCount, &rw.MyVote); err != nil {
			return nil, err
		}
		out = append(out, &rw)
	}
	return out, rows.Err()
}

func scanReview(row pgx.Row) (*domain.Review, error) {
	var rw domain.Review
	if err := row.Scan(&rw.ID, &rw.BusinessID, &rw.ProductID, &rw.UserID, &rw.Rating, &rw.Text,
		&rw.ImageIDs, &rw.Reply, &rw.ReplyAt, &rw.ReplyEditedAt, &rw.Status, &rw.CreatedAt, &rw.AuthorName, &rw.AuthorUsername,
		&rw.AuthorAvatar); err != nil {
		return nil, err
	}
	return &rw, nil
}

func (r *EngagementRepo) UpdateReview(ctx context.Context, id, userID string, rating int, text string, imageIDs []string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE reviews SET rating=$3, text=$4, image_ids=$5, edited_at=now(), updated_at=now()
		WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID, rating, text, imageIDs)
	return err
}

func (r *EngagementRepo) DeleteReview(ctx context.Context, id, userID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE reviews SET deleted_at=now() WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}

func (r *EngagementRepo) SetReviewReply(ctx context.Context, id, ownerID, reply string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE reviews SET reply=$3, reply_at=now(), updated_at=now()
		WHERE id=$1 AND business_id IN (SELECT id FROM businesses WHERE owner_id=$2)`,
		id, ownerID, reply)
	return err
}

// SetReviewReplyEdit updates an existing owner reply and stamps
// reply_edited_at (PRD §5.6.2: replies stay editable).
func (r *EngagementRepo) SetReviewReplyEdit(ctx context.Context, id, ownerID, reply string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE reviews SET reply=$3, reply_edited_at=now(), updated_at=now()
		WHERE id=$1 AND business_id IN (SELECT id FROM businesses WHERE owner_id=$2)`,
		id, ownerID, reply)
	return err
}

// ClearReviewReply removes an owner reply entirely.
func (r *EngagementRepo) ClearReviewReply(ctx context.Context, id, ownerID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE reviews SET reply=NULL, reply_at=NULL, reply_edited_at=NULL, updated_at=now()
		WHERE id=$1 AND business_id IN (SELECT id FROM businesses WHERE owner_id=$2)`,
		id, ownerID)
	return err
}

func (r *EngagementRepo) GetReview(ctx context.Context, id string) (*domain.Review, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT r.id, r.business_id, r.product_id, r.user_id, r.rating, r.text, r.image_ids, r.reply, r.reply_at, r.reply_edited_at,
			r.status, r.created_at, u.name, u.username, u.avatar_url
		FROM reviews r JOIN users u ON u.id = r.user_id WHERE r.id=$1 AND r.deleted_at IS NULL`, id)
	rw, err := scanReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return rw, err
}

func (r *EngagementRepo) SetHelpful(ctx context.Context, reviewID, userID string, vote int) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO review_helpful_votes (id, review_id, user_id, vote) VALUES ($1,$2,$3,$4)
		ON CONFLICT (review_id, user_id) DO UPDATE SET vote = EXCLUDED.vote`,
		util.NewUUID(), reviewID, userID, vote)
	return err
}

// RemoveHelpful clears the viewer's vote entirely (toggle-off).
func (r *EngagementRepo) RemoveHelpful(ctx context.Context, reviewID, userID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM review_helpful_votes WHERE review_id=$1 AND user_id=$2`, reviewID, userID)
	return err
}

// ---- notifications (PRD §5.7) ----

func (r *EngagementRepo) CreateNotification(ctx context.Context, userID, ntype string, payload map[string]any) (*domain.Notification, error) {
	n := &domain.Notification{ID: util.NewUUID(), UserID: userID, Type: ntype, Payload: payload}
	// Retention tiers (PRD §5.7): 90d users, 180d business owners, 365d admins.
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notifications (id, user_id, type, payload, expires_at)
		SELECT $1,$2,$3,$4, now() + CASE
				WHEN u.role = 'admin' THEN interval '365 days'
				WHEN EXISTS (SELECT 1 FROM businesses b WHERE b.owner_id = u.id AND b.deleted_at IS NULL) THEN interval '180 days'
				ELSE interval '90 days' END
		FROM users u WHERE u.id = $2`,
		n.ID, userID, ntype, payload)
	if err != nil {
		// Recipient vanished mid-flight: fall back to the default window.
		_, err = r.pool.Exec(ctx, `
			INSERT INTO notifications (id, user_id, type, payload, expires_at)
			VALUES ($1,$2,$3,$4, now() + interval '90 days')`,
			n.ID, userID, ntype, payload)
	}
	return n, err
}

func (r *EngagementRepo) ListNotifications(ctx context.Context, userID, ntype string, limit, offset int) ([]*domain.Notification, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, type, payload, is_read, created_at FROM notifications
		WHERE user_id=$1 AND ($2 = '' OR type = $2)
		ORDER BY created_at DESC LIMIT $3 OFFSET $4`, userID, ntype, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Notification
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Payload, &n.IsRead, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

// CreateNotificationsForFollowers inserts an in-app notification for every
// follower of a business in ONE statement. The previous per-follower loop
// (INSERT + prefs SELECT + unread COUNT each) serialized ~4 queries per
// follower inside the announcement request and starved the pool.
func (r *EngagementRepo) CreateNotificationsForFollowers(ctx context.Context, businessID, ntype string, payload map[string]any) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO notifications (id, user_id, type, payload, expires_at)
		SELECT gen_random_uuid(), f.user_id, $2, $3, now() + interval '90 days'
		FROM follows f
		WHERE f.business_id = $1`, businessID, ntype, payload)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PurgeExpired removes notifications past their retention window (PRD §5.7).
// Batched: a single unbounded DELETE on a large table held locks and spiked
// WAL; ctid-chunks keep each transaction short.
func (r *EngagementRepo) PurgeExpired(ctx context.Context) (int64, error) {
	var total int64
	for {
		tag, err := r.pool.Exec(ctx, `DELETE FROM notifications
			WHERE ctid IN (
				SELECT ctid FROM notifications WHERE expires_at < now() LIMIT 50000
			)`)
		if err != nil {
			return total, err
		}
		n := tag.RowsAffected()
		total += n
		if n < 50000 {
			return total, nil
		}
	}
}

// GetLatestUnread returns the newest unread notification of a type for a
// user (used after bulk fan-out inserts to hydrate WS/email dispatches).
func (r *EngagementRepo) GetLatestUnread(ctx context.Context, userID, ntype string) (*domain.Notification, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, type, payload, is_read, created_at FROM notifications
		WHERE user_id=$1 AND type=$2 AND is_read=false
		ORDER BY created_at DESC LIMIT 1`, userID, ntype)
	var n domain.Notification
	if err := row.Scan(&n.ID, &n.UserID, &n.Type, &n.Payload, &n.IsRead, &n.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &n, nil
}

func (r *EngagementRepo) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id=$1 AND is_read=false`, userID).Scan(&n)
	return n, err
}

func (r *EngagementRepo) MarkNotificationsRead(ctx context.Context, userID string, ids []string, all bool) error {
	if all {
		_, err := r.pool.Exec(ctx,
			`UPDATE notifications SET is_read=true WHERE user_id=$1`, userID)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE notifications SET is_read=true WHERE user_id=$1 AND id = ANY($2)`, userID, ids)
	return err
}

// ---- engagement events (append-only, PRD §5.6.3) ----

// InsertEvent records a signal; dedupe_key prevents double counting per user/day.
// Returns false when deduped. Owner self-actions on their own business are
// excluded at the service layer (§8.6).
func (r *EngagementRepo) InsertEvent(ctx context.Context, userID, targetType, targetID, signal string, weight int, dedupeKey string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO engagement_events (user_id, target_type, target_id, signal, weight, dedupe_key)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (dedupe_key) DO NOTHING`,
		userID, targetType, targetID, signal, weight, dedupeKey)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
