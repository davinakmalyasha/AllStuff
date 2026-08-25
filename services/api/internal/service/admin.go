package service

import (
	"context"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Admin — verification decisions + moderation trail (PRD §5.8.1, §8.7).
type Admin struct {
	repos    *repo.Repos
	notifier *Notifier
}

func NewAdmin(repos *repo.Repos, notifier *Notifier) *Admin {
	return &Admin{repos: repos, notifier: notifier}
}

type DecideInput struct {
	Decision string              `json:"decision"` // approve | reject
	Level    *domain.VerificationLevel `json:"level"`
	Reason   string              `json:"reason"`
}

// Decide approves (with level) or rejects a pending business. Every decision
// is appended to the immutable moderation trail (PRD §8.7).
func (a *Admin) Decide(ctx context.Context, adminID, businessID string, in DecideInput) (*domain.Business, error) {
	b, err := a.repos.Businesses.GetByID(ctx, businessID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, domain.ErrNotFound
	}
	if b.Status != domain.BusinessPending {
		return nil, domain.ErrValidation.WithField("_", "Business is not pending review.")
	}

	reason := strings.TrimSpace(in.Reason)
	action := "approve"

	switch in.Decision {
	case "approve":
		level := domain.LevelVerified
		if in.Level != nil && *in.Level == domain.LevelFullyVerified {
			docs, err := a.repos.Businesses.ListDocuments(ctx, businessID)
			if err != nil {
				return nil, err
			}
			if len(docs) == 0 {
				return nil, domain.ErrValidation.WithField("level",
					"Fully Verified requires at least one verification document.")
			}
			level = domain.LevelFullyVerified
		}
		if err := a.repos.Businesses.SetVerified(ctx, businessID, level); err != nil {
			return nil, err
		}
		_ = a.repos.Businesses.SetDocumentStatuses(ctx, businessID, "approved")
	case "reject":
		if reason == "" {
			return nil, domain.ErrValidation.WithField("reason", "A rejection reason is required.")
		}
		action = "reject"
		if err := a.repos.Businesses.SetRejected(ctx, businessID, reason); err != nil {
			return nil, err
		}
		_ = a.repos.Businesses.SetDocumentStatuses(ctx, businessID, "rejected")
	default:
		return nil, domain.ErrValidation.WithField("decision", "Decision must be approve or reject.")
	}

	// Audit trail (PRD §8.7).
	_, err = a.repos.Exec(ctx, `
		INSERT INTO moderation_actions (id, admin_id, action, target_type, target_id, reason, payload)
		VALUES ($1, $2, $3, 'business', $4, $5, $6)`,
		util.NewUUID(), adminID, action, businessID, reason,
		map[string]any{"level": in.Level})
	if err != nil {
		return nil, err
	}

	// Notify the owner of the decision (PRD §5.7).
	if b.OwnerID != "" {
		payload := map[string]any{"business_id": businessID, "decision": action, "reason": reason}
		if action == "approve" && in.Level != nil {
			payload["level"] = string(*in.Level)
		}
		a.notifier.Create(ctx, b.OwnerID, "verification_result", payload)
	}

	// Category followers get a heads-up when a new business goes live.
	if action == "approve" {
		followers, err := a.repos.Community.CategoryFollowerIDs(ctx, b.CategoryID)
		if err == nil {
			catName := b.CategoryName
			for _, uid := range followers {
				if uid == b.OwnerID {
					continue
				}
				a.notifier.Create(ctx, uid, "category_new_business", map[string]any{
					"business_id": businessID, "category_name": catName,
				})
			}
		}
	}

	return a.repos.Businesses.GetByID(ctx, businessID)
}

// RequestDocument asks the owner to re-submit a specific document kind
// (PRD §5.8.1): matching pending/approved docs are marked rejected with the
// note, and the owner receives a doc_re_request notification.
func (a *Admin) RequestDocument(ctx context.Context, adminID, businessID, kind, note string) error {
	b, err := a.repos.Businesses.GetByID(ctx, businessID)
	if err != nil {
		return err
	}
	if b == nil {
		return domain.ErrNotFound
	}
	valid := map[string]bool{"registration": true, "license": true, "tax_id": true, "identity": true, "utility": true}
	if !valid[kind] {
		return domain.ErrValidation.WithField("kind", "Invalid document kind.")
	}
	if _, err := a.repos.Exec(ctx, `
		UPDATE verification_documents SET status='rejected', review_note=$2, reviewed_at=now()
		WHERE business_id=$1 AND kind=$3 AND status <> 'rejected'`, businessID, note, kind); err != nil {
		return err
	}
	if _, err := a.repos.Exec(ctx, `
		INSERT INTO moderation_actions (id, admin_id, action, target_type, target_id, reason, payload)
		VALUES ($1, $2, 'doc_re_request', 'business', $3, $4, $5)`,
		util.NewUUID(), adminID, businessID, note, map[string]any{"kind": kind}); err != nil {
		return err
	}
	if b.OwnerID != "" {
		a.notifier.Create(ctx, b.OwnerID, "doc_re_request", map[string]any{
			"business_id": businessID, "kind": kind, "note": note,
		})
	}
	return nil
}

// VerifyQueue lists pending/rejected businesses for the queue UI.
func (a *Admin) VerifyQueue(ctx context.Context, statuses []string, limit, offset int) ([]*domain.Business, error) {
	if len(statuses) == 0 {
		statuses = []string{"pending_review"}
	}
	return a.repos.Businesses.ByStatus(ctx, statuses, limit, offset)
}

// LogDocumentView records an admin document view (PRD §9.3 audit).
func (a *Admin) LogDocumentView(ctx context.Context, adminID, documentID string) error {
	return a.repos.Businesses.LogDocumentView(ctx, util.NewUUID(), documentID, adminID)
}

// ---- moderation (PRD §5.8.2) ----

type ReportItem struct {
	ID          string         `json:"id"`
	ReporterID  string         `json:"reporter_id"`
	TargetType  string         `json:"target_type"`
	TargetID    string         `json:"target_id"`
	Reason      string         `json:"reason"`
	Status      string         `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	Evidence    map[string]any `json:"evidence"`
	TargetSnippet string       `json:"target_snippet,omitempty"`
}

func (a *Admin) Reports(ctx context.Context, status string, limit, offset int) ([]*ReportItem, error) {
	rows, err := a.repos.Query(ctx, `
		SELECT id, reporter_id, target_type, target_id, reason, status, created_at, evidence
		FROM reports WHERE status = $1 ORDER BY created_at ASC LIMIT $2 OFFSET $3`,
		status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReportItem
	for rows.Next() {
		var r ReportItem
		if err := rows.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason,
			&r.Status, &r.CreatedAt, &r.Evidence); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (a *Admin) ReportSnippet(ctx context.Context, r *ReportItem) {
	switch r.TargetType {
	case "review":
		_ = a.repos.QueryRow(ctx, `SELECT text FROM reviews WHERE id = $1`, r.TargetID).Scan(&r.TargetSnippet)
	case "comment":
		_ = a.repos.QueryRow(ctx, `SELECT text FROM comments WHERE id = $1`, r.TargetID).Scan(&r.TargetSnippet)
	case "message":
		_ = a.repos.QueryRow(ctx, `SELECT coalesce(body,'') FROM chat_messages WHERE id = $1::bigint`, r.TargetID).Scan(&r.TargetSnippet)
	case "product":
		_ = a.repos.QueryRow(ctx, `SELECT name FROM products WHERE id = $1`, r.TargetID).Scan(&r.TargetSnippet)
	case "business":
		_ = a.repos.QueryRow(ctx, `SELECT name FROM businesses WHERE id = $1`, r.TargetID).Scan(&r.TargetSnippet)
	case "user":
		_ = a.repos.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, r.TargetID).Scan(&r.TargetSnippet)
	}
}

// DecideReport resolves a report; every action lands in the audit trail (§8.7).
func (a *Admin) DecideReport(ctx context.Context, adminID, reportID, action, note string) error {
	var report ReportItem
	if err := a.repos.QueryRow(ctx, `
		SELECT id, reporter_id, target_type, target_id, reason, status, created_at, evidence
		FROM reports WHERE id = $1`, reportID).Scan(&report.ID, &report.ReporterID,
		&report.TargetType, &report.TargetID, &report.Reason, &report.Status,
		&report.CreatedAt, &report.Evidence); err != nil {
		return domain.ErrNotFound
	}
	if report.Status != "open" {
		return domain.ErrValidation.WithField("_", "Report already resolved.")
	}
	switch action {
	case "dismiss":
		if _, err := a.repos.Exec(ctx, `
			UPDATE reports SET status='dismissed', resolved_by=$1, resolved_at=now() WHERE id=$2`,
			adminID, reportID); err != nil {
			return err
		}
	case "hide", "warn", "suspend", "delete_for_everyone":
		// Resolve the report.
		if _, err := a.repos.Exec(ctx, `
			UPDATE reports SET status='resolved', resolved_by=$1, resolved_at=now() WHERE id=$2`,
			adminID, reportID); err != nil {
			return err
		}
		switch action {
		case "hide":
			if err := a.HideContent(ctx, adminID, report.TargetType, report.TargetID, note); err != nil {
				return err
			}
		case "warn":
			// Warn the AUTHOR of the reported content — TargetID is the
			// content row, not a user (warning it previously notified nobody
			// or a bogus principal).
			authorID, err := a.contentAuthorID(ctx, report.TargetType, report.TargetID)
			if err != nil {
				return err
			}
			if authorID == "" {
				return domain.ErrValidation.WithField("action", "Cannot warn: content author not found.")
			}
			a.notifier.Create(ctx, authorID, "moderation_warning", map[string]any{"note": note})
		case "suspend":
			subject := report.TargetID
			if report.TargetType != "user" {
				var err error
				subject, err = a.contentAuthorID(ctx, report.TargetType, report.TargetID)
				if err != nil {
					return err
				}
				if subject == "" {
					return domain.ErrValidation.WithField("action", "Cannot suspend: content author not found.")
				}
			}
			if _, err := a.repos.Exec(ctx, `
				UPDATE users SET status='suspended', suspended_until = now() + interval '7 days'
				WHERE id=$1`, subject); err != nil {
				return err
			}
		case "delete_for_everyone":
			// Messages only (PRD §5.8.2): remove from every participant's
			// view and tell the sender why.
			if report.TargetType != "message" {
				return domain.ErrValidation.WithField("action", "Only messages support delete-for-everyone.")
			}
			if err := a.HideContent(ctx, adminID, "message", report.TargetID, note); err != nil {
				return err
			}
			var sender string
			_ = a.repos.QueryRow(ctx,
				`SELECT sender_id FROM chat_messages WHERE id=$1::bigint`, report.TargetID).Scan(&sender)
			if sender != "" {
				a.notifier.Create(ctx, sender, "moderation_warning", map[string]any{
					"note": note, "reason": "A message you sent was removed by moderators.",
				})
			}
		}
	default:
		return domain.ErrValidation.WithField("action", "Unknown action.")
	}
	// Audit trail (§8.7).
	_, err := a.repos.Exec(ctx, `
		INSERT INTO moderation_actions (id, admin_id, action, target_type, target_id, reason)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		util.NewUUID(), adminID, action, report.TargetType, report.TargetID, note)
	return err
}

// contentAuthorID resolves the author/owner of a reported content row so
// moderation actions act on a person, not a UUID of a review/message/etc.
func (a *Admin) contentAuthorID(ctx context.Context, targetType, targetID string) (string, error) {
	var q string
	switch targetType {
	case "review":
		q = `SELECT user_id FROM reviews WHERE id = $1`
	case "comment":
		q = `SELECT user_id FROM comments WHERE id = $1`
	case "message":
		q = `SELECT sender_id FROM chat_messages WHERE id = $1::bigint`
	case "product":
		q = `SELECT b.owner_id FROM products p JOIN businesses b ON b.id = p.business_id WHERE p.id = $1`
	case "business":
		q = `SELECT owner_id FROM businesses WHERE id = $1`
	case "attachment":
		q = `SELECT uploader_id FROM media WHERE id = $1`
	case "reaction":
		q = `SELECT user_id FROM message_reactions WHERE id = $1`
	default:
		return "", nil
	}
	var uid string
	if err := a.repos.QueryRow(ctx, q, targetID).Scan(&uid); err != nil {
		return "", nil // unknown/deleted target: caller surfaces a validation error
	}
	return uid, nil
}

// HideContent hides a review/comment/product (author sees "removed by moderator").
func (a *Admin) HideContent(ctx context.Context, adminID, targetType, targetID, reason string) error {
	switch targetType {
	case "review":
		_, err := a.repos.Exec(ctx, `
			UPDATE reviews SET status='hidden', hidden_by=$1, hidden_reason=$2 WHERE id=$3`,
			adminID, reason, targetID)
		return err
	case "comment":
		_, err := a.repos.Exec(ctx, `
			UPDATE comments SET status='hidden' WHERE id=$1`, targetID)
		return err
	case "product":
		_, err := a.repos.Exec(ctx, `
			UPDATE products SET is_published=false WHERE id=$1`, targetID)
		return err
	case "message":
		_, err := a.repos.Exec(ctx, `
			UPDATE chat_messages SET deleted_for='everyone' WHERE id=$1::bigint`, targetID)
		return err
	}
	return domain.ErrValidation.WithField("target_type", "Cannot hide this type.")
}

// ---- users management (PRD §5.8.4) ----

func (a *Admin) SearchUsers(ctx context.Context, q string, limit, offset int) ([]*domain.User, error) {
	rows, err := a.repos.Query(ctx, `
		SELECT `+repo.UserColumns+` FROM users
		WHERE name ILIKE '%' || $1 || '%' OR email ILIKE '%' || $1 || '%' OR username ILIKE '%' || $1 || '%'
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.User
	for rows.Next() {
		u, err := repo.ScanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (a *Admin) UserAction(ctx context.Context, adminID, userID, action, reason string) error {
	var err error
	switch action {
	case "warn":
		a.notifier.Create(ctx, userID, "moderation_warning", map[string]any{"note": reason})
	case "suspend":
		_, err = a.repos.Exec(ctx, `
			UPDATE users SET status='suspended', suspended_until = now() + interval '7 days', updated_at=now() WHERE id=$1`, userID)
	case "ban":
		_, err = a.repos.Exec(ctx, `
			UPDATE users SET status='banned', ban_reason=$2, updated_at=now() WHERE id=$1`, userID, reason)
	case "unban":
		_, err = a.repos.Exec(ctx, `
			UPDATE users SET status='active', ban_reason=NULL, suspended_until=NULL, updated_at=now() WHERE id=$1`, userID)
	default:
		return domain.ErrValidation.WithField("action", "Unknown action.")
	}
	if err != nil {
		return err
	}
	_, err = a.repos.Exec(ctx, `
		INSERT INTO moderation_actions (id, admin_id, action, target_type, target_id, reason)
		VALUES ($1, $2, $3, 'user', $4, $5)`,
		util.NewUUID(), adminID, action, userID, reason)
	return err
}

// ---- curation & config (PRD §5.8.5) ----

type CurationConfig struct {
	FeaturedIDs     []string         `json:"featured_ids"`
	Leaderboard     map[string]any   `json:"leaderboard"`
	Announcement    *string          `json:"announcement"`
}

func (a *Admin) GetCuration(ctx context.Context) (*CurationConfig, error) {
	rows, err := a.repos.Query(ctx, `
		SELECT id FROM businesses WHERE is_featured = true ORDER BY featured_order, name LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cfg CurationConfig
	cfg.Leaderboard = map[string]any{"weights": map[string]any{
		"view": 1, "collection_save": 3, "like": 5, "comment": 8, "recommend": 10, "review": 12, "chat_start": 8,
	}}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		cfg.FeaturedIDs = append(cfg.FeaturedIDs, id)
	}
	return &cfg, nil
}

func (a *Admin) SetCuration(ctx context.Context, adminID string, cfg *CurationConfig) error {
	if cfg.FeaturedIDs != nil {
		if len(cfg.FeaturedIDs) > 50 {
			cfg.FeaturedIDs = cfg.FeaturedIDs[:50]
		}
		// Atomic swap: previously the full-table clear + per-row updates ran
		// in autocommit, so a crash mid-way left the homepage strip wiped.
		tx, err := a.repos.Pool().Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `UPDATE businesses SET is_featured=false, featured_order=NULL WHERE is_featured OR featured_order IS NOT NULL`); err != nil {
			return err
		}
		for i, id := range cfg.FeaturedIDs {
			if _, err := tx.Exec(ctx, `
				UPDATE businesses SET is_featured=true, featured_order=$2 WHERE id=$1`, id, i); err != nil {
				return err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	_ = adminID
	return nil
}

// ---- banned words (PRD §8.7) ----

func (a *Admin) ListBannedWords(ctx context.Context) ([]string, error) {
	return a.repos.Chat.BannedWords(ctx)
}

func (a *Admin) Allowlist(ctx context.Context) ([]string, error) {
	var list []string
	_ = a.repos.QueryRow(ctx, `
		SELECT coalesce(value->'words', '[]'::jsonb) FROM site_config WHERE key = 'banned_words_allowlist'`).
		Scan(&list)
	return list, nil
}

func (a *Admin) SetAllowlist(ctx context.Context, words []string) error {
	return a.SetSiteConfig(ctx, "banned_words_allowlist", map[string]any{"words": words})
}

func (a *Admin) AddBannedWord(ctx context.Context, word string) error {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" || len([]rune(word)) > 40 {
		return domain.ErrValidation.WithField("word", "Word must be 1–40 characters.")
	}
	_, err := a.repos.Exec(ctx, `
		INSERT INTO banned_words (id, word) VALUES ($1, $2) ON CONFLICT (word) DO NOTHING`,
		util.NewUUID(), word)
	return err
}

func (a *Admin) RemoveBannedWord(ctx context.Context, word string) error {
	_, err := a.repos.Exec(ctx, `DELETE FROM banned_words WHERE word = $1`, strings.ToLower(strings.TrimSpace(word)))
	return err
}

// ---- site config (announcement banner, PRD §5.8.5) ----

func (a *Admin) GetSiteConfig(ctx context.Context) (map[string]any, error) {
	rows, err := a.repos.Query(ctx, `SELECT key, value FROM site_config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var k string
		var v any
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func (a *Admin) SetSiteConfig(ctx context.Context, key string, value any) error {
	_, err := a.repos.Exec(ctx, `
		INSERT INTO site_config (id, key, value, updated_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		util.NewUUID(), key, value)
	return err
}

// ---- audit trail ----

func (a *Admin) AuditTrail(ctx context.Context, limit, offset int) ([]map[string]any, error) {	rows, err := a.repos.Query(ctx, `
		SELECT ma.id, ma.action, ma.target_type, ma.target_id, ma.reason, ma.created_at,
			u.name AS admin_name
		FROM moderation_actions ma JOIN users u ON u.id = ma.admin_id
		ORDER BY ma.created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, action, ttype, tid, reason, adminName string
		var createdAt time.Time
		if err := rows.Scan(&id, &action, &ttype, &tid, &reason, &createdAt, &adminName); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "action": action, "target_type": ttype, "target_id": tid,
			"reason": reason, "created_at": createdAt, "admin_name": adminName,
		})
	}
	return out, rows.Err()
}
