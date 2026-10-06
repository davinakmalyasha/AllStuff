package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/util"
)

// ChatRepo — threads, participants, messages, reactions, quick replies,
// blocks (PRD §7.3, §5.5).
type ChatRepo struct{ pool pooler }

// ---- threads ----

// CreateThread inserts the thread and its first participant atomically —
// previously two autocommit statements could orphan a thread when the second
// failed.
func (r *ChatRepo) CreateThread(ctx context.Context, ttype, businessID, userID string) (*domain.ChatThread, error) {
	var biz *string
	if businessID != "" {
		biz = &businessID
	}
	t := &domain.ChatThread{ID: util.NewUUID(), Type: ttype, BusinessID: biz}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat_threads (id, type, business_id) VALUES ($1, $2, $3)`,
		t.ID, t.Type, t.BusinessID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat_participants (id, thread_id, user_id, role)
		VALUES ($1, $2, $3, 'user')`, util.NewUUID(), t.ID, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return t, nil
}

// FindBusinessThread returns the (user, business) thread if it exists.
func (r *ChatRepo) FindBusinessThread(ctx context.Context, businessID, userID string) (*domain.ChatThread, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT t.id, t.type, t.business_id, t.status, t.last_message_at, t.created_at
		FROM chat_threads t
		JOIN chat_participants p ON p.thread_id = t.id
		WHERE t.type = 'business' AND t.business_id = $1 AND p.user_id = $2
		  AND p.left_at IS NULL AND t.status <> 'left'
		LIMIT 1`, businessID, userID)
	return scanThread(row)
}

// FindDirectThread returns the thread between two users if it exists.
func (r *ChatRepo) FindDirectThread(ctx context.Context, a, b string) (*domain.ChatThread, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT t.id, t.type, t.business_id, t.status, t.last_message_at, t.created_at
		FROM chat_threads t
		WHERE t.type = 'direct'
		  AND EXISTS (SELECT 1 FROM chat_participants WHERE thread_id = t.id AND user_id = $1 AND left_at IS NULL)
		  AND EXISTS (SELECT 1 FROM chat_participants WHERE thread_id = t.id AND user_id = $2 AND left_at IS NULL)
		LIMIT 1`, a, b)
	return scanThread(row)
}

func scanThread(row pgx.Row) (*domain.ChatThread, error) {
	var t domain.ChatThread
	if err := row.Scan(&t.ID, &t.Type, &t.BusinessID, &t.Status, &t.LastMessageAt, &t.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *ChatRepo) GetThread(ctx context.Context, id string) (*domain.ChatThread, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, type, business_id, status, last_message_at, created_at
		FROM chat_threads WHERE id = $1`, id)
	return scanThread(row)
}

func (r *ChatRepo) AddParticipant(ctx context.Context, threadID, userID, role string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1, $2, $3, $4)
		ON CONFLICT (thread_id, user_id) DO UPDATE SET left_at = NULL`,
		util.NewUUID(), threadID, userID, role)
	return err
}

func (r *ChatRepo) ParticipantIDs(ctx context.Context, threadID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id FROM chat_participants WHERE thread_id = $1 AND left_at IS NULL`, threadID)
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

func (r *ChatRepo) IsParticipant(ctx context.Context, threadID, userID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM chat_participants WHERE thread_id=$1 AND user_id=$2 AND left_at IS NULL)`,
		threadID, userID).Scan(&exists)
	return exists, err
}

// ThreadsByUser lists the user's threads with counterpart + unread counts.
func (r *ChatRepo) ThreadsByUser(ctx context.Context, userID, businessID string) ([]*domain.ThreadListItem, error) {
	query := `
		SELECT t.id, t.type, t.business_id, t.status, t.last_message_at, t.created_at,
			b.name AS business_name, b.slug AS business_slug, b.logo_url AS business_logo,
			u.id AS other_id, u.name AS other_name, u.username AS other_username, u.avatar_url AS other_avatar,
			(SELECT body FROM chat_messages m WHERE m.thread_id = t.id AND m.deleted_for <> 'everyone'
			 ORDER BY m.id DESC LIMIT 1) AS last_body,
			(SELECT count(*) FROM chat_messages m WHERE m.thread_id = t.id AND m.id > COALESCE(p.last_read_message_id, 0)
			 AND m.sender_id <> $1 AND m.deleted_for <> 'everyone') AS unread,
			t.id = ANY(p.pinned_thread_ids) AS pinned
		FROM chat_threads t
		JOIN chat_participants p ON p.thread_id = t.id AND p.user_id = $1 AND p.left_at IS NULL
		LEFT JOIN businesses b ON b.id = t.business_id
		LEFT JOIN LATERAL (
			SELECT u2.id, u2.name, u2.username, u2.avatar_url
			FROM chat_participants p2 JOIN users u2 ON u2.id = p2.user_id
			WHERE p2.thread_id = t.id AND p2.user_id <> $1 AND p2.left_at IS NULL
			LIMIT 1
		) u ON true
		WHERE t.status <> 'left'`
	args := []any{userID}
	if businessID != "" {
		query += ` AND t.business_id = $2`
		args = append(args, businessID)
	}
	query += ` ORDER BY t.last_message_at DESC NULLS LAST, t.created_at DESC LIMIT 100`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ThreadListItem
	for rows.Next() {
		var it domain.ThreadListItem
		if err := rows.Scan(&it.ID, &it.Type, &it.BusinessID, &it.Status, &it.LastMessageAt, &it.CreatedAt,
			&it.BusinessName, &it.BusinessSlug, &it.BusinessLogo,
			&it.OtherID, &it.OtherName, &it.OtherUsername, &it.OtherAvatar,
			&it.LastBody, &it.Unread, &it.Pinned); err != nil {
			return nil, err
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

// ---- messages ----

// CreateMessage inserts and returns the created message (or the existing one
// when the client_msg_id already exists — at-least-once dedupe, PRD §5.5.3).
func (r *ChatRepo) CreateMessage(ctx context.Context, m *domain.ChatMessage) (*domain.ChatMessage, error) {
	// System notices carry no sender: an empty SenderID maps to SQL NULL
	// (the column is nullable for sender_role='system' only).
	var senderID any
	if m.SenderID != "" {
		senderID = m.SenderID
	}
	var scannedSender *string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, body, reply_to_id,
			forwarded_from_message_id, media_id, link_preview, client_msg_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (thread_id, client_msg_id) DO UPDATE SET client_msg_id = EXCLUDED.client_msg_id
		RETURNING `+messageCols,
		m.ThreadID, senderID, m.SenderRole, m.Type, m.Body, m.ReplyToID,
		m.ForwardedFromID, m.MediaID, m.LinkPreview, m.ClientMsgID).Scan(
		&m.ID, &m.ThreadID, &scannedSender, &m.SenderRole, &m.Type, &m.Body,
		&m.ReplyToID, &m.ForwardedFromID, &m.MediaID, &m.LinkPreview, &m.ClientMsgID,
		&m.ReadCount, &m.EditedAt, &m.EditHistory, &m.DeletedFor, &m.DeletedAt, &m.CreatedAt)
	if err == nil && scannedSender != nil {
		m.SenderID = *scannedSender
	}
	return m, err
}

func (r *ChatRepo) MessageByClientID(ctx context.Context, threadID, clientMsgID string) (*domain.ChatMessage, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+messageCols+` FROM chat_messages WHERE thread_id=$1 AND client_msg_id=$2`,
		threadID, clientMsgID)
	m, err := scanMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func (r *ChatRepo) MessageByID(ctx context.Context, id int64) (*domain.ChatMessage, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+messageCols+` FROM chat_messages WHERE id=$1`, id)
	m, err := scanMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

const messageCols = `id, thread_id, sender_id, sender_role, type, body, reply_to_id,
	forwarded_from_message_id, media_id, link_preview, client_msg_id, read_count, edited_at,
	edit_history, deleted_for, deleted_at, created_at`

func scanMessage(row pgx.Row) (*domain.ChatMessage, error) {
	var m domain.ChatMessage
	var senderID *string // NULL for sender_role='system'
	if err := row.Scan(&m.ID, &m.ThreadID, &senderID, &m.SenderRole, &m.Type, &m.Body,
		&m.ReplyToID, &m.ForwardedFromID, &m.MediaID, &m.LinkPreview, &m.ClientMsgID,
		&m.ReadCount, &m.EditedAt, &m.EditHistory, &m.DeletedFor, &m.DeletedAt, &m.CreatedAt); err != nil {
		return nil, err
	}
	if senderID != nil {
		m.SenderID = *senderID
	}
	return &m, nil
}

func (r *ChatRepo) MessagesByThread(ctx context.Context, threadID string, before int64, limit int) ([]*domain.ChatMessage, error) {
	// Withdrawn messages ("delete for everyone") never come back on refetch
	// or export — every reader path funnels through this query.
	query := `SELECT ` + messageCols + ` FROM chat_messages WHERE thread_id = $1 AND deleted_for <> 'everyone'`
	args := []any{threadID}
	if before > 0 {
		query += ` AND id < $2`
		args = append(args, before)
	}
	query += ` ORDER BY id DESC LIMIT $` + util.Itoa(len(args)+1)
	args = append(args, limit)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ChatMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *ChatRepo) UpdateMessageText(ctx context.Context, id int64, text string, history []map[string]any) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE chat_messages SET body=$2, edited_at=now(), edit_history=$3 WHERE id=$1`,
		id, text, history)
	return err
}

func (r *ChatRepo) DeleteMessage(ctx context.Context, id int64, scope string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE chat_messages SET deleted_for=$2, deleted_at=now() WHERE id=$1`, id, scope)
	return err
}

func (r *ChatRepo) TouchLastMessage(ctx context.Context, threadID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE chat_threads SET last_message_at = now() WHERE id=$1`, threadID)
	return err
}

// SetLastRead advances a participant's read marker and credits the messages it
// passes.
//
// `chat_messages.read_count` was declared, selected, serialised to clients as
// `read_count`, and never written - so every message reported zero reads
// forever, including to the sender. The surrounding machinery was all present:
// this marker, ChatRepo.LastRead, the POST /threads/{id}/read handler and its
// receipt.read broadcast. Only the increment was missing.
//
// One statement, deliberately. The marker and the counts have to move together:
// if the marker committed and the counts did not, the user would never be
// re-credited for those messages, because GREATEST makes the operation
// permanently idempotent from the marker's point of view. Two statements in a
// transaction would work; one statement cannot drift.
//
// `prev` reads the pre-update value from the same snapshot, so the credited range
// is exactly the span the marker crossed this call. Re-sending the same
// last_read_message_id - which the frontend does on every render - advances
// nothing, so `id > prev.old_id` matches nothing and no count is double-increased.
//
// A participant's OWN messages are never credited to them: read_count answers "how
// many other people have seen this", and counting your own message as read by
// yourself would report 1 on a message the sender is looking at.
func (r *ChatRepo) SetLastRead(ctx context.Context, threadID, userID string, msgID int64) error {
	_, err := r.pool.Exec(ctx, `
		WITH prev AS (
			SELECT coalesce(last_read_message_id, 0) AS old_id
			  FROM chat_participants
			 WHERE thread_id = $1 AND user_id = $2
		),
		advanced AS (
			UPDATE chat_participants
			   SET last_read_message_id = GREATEST(coalesce(last_read_message_id, 0), $3)
			 WHERE thread_id = $1 AND user_id = $2
			RETURNING last_read_message_id
		)
		UPDATE chat_messages m
		   SET read_count = m.read_count + 1
		  FROM prev, advanced a
		 WHERE m.thread_id = $1
		   AND m.id > prev.old_id
		   AND m.id <= a.last_read_message_id
		   AND m.sender_id IS DISTINCT FROM $2`,
		threadID, userID, msgID)
	return err
}

func (r *ChatRepo) LastRead(ctx context.Context, threadID, userID string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`SELECT coalesce(last_read_message_id, 0) FROM chat_participants WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID).Scan(&id)
	return id, err
}

// ---- reactions ----

func (r *ChatRepo) SetReaction(ctx context.Context, messageID int64, userID, emoji string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO message_reactions (id, message_id, user_id, emoji) VALUES ($1, $2, $3, $4)
		ON CONFLICT (message_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji`,
		util.NewUUID(), messageID, userID, emoji)
	return err
}

func (r *ChatRepo) RemoveReaction(ctx context.Context, messageID int64, userID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM message_reactions WHERE message_id=$1 AND user_id=$2`, messageID, userID)
	return err
}

// ReactionByUser returns the emoji (or "") for a user on a message.
func (r *ChatRepo) ReactionByUser(ctx context.Context, messageID int64, userID string) (string, error) {
	var emoji string
	err := r.pool.QueryRow(ctx,
		`SELECT emoji FROM message_reactions WHERE message_id=$1 AND user_id=$2`,
		messageID, userID).Scan(&emoji)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return emoji, nil
}

func (r *ChatRepo) Reactions(ctx context.Context, messageID int64) ([]*domain.MessageReaction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, message_id, user_id, emoji FROM message_reactions WHERE message_id=$1`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.MessageReaction
	for rows.Next() {
		var x domain.MessageReaction
		if err := rows.Scan(&x.ID, &x.MessageID, &x.UserID, &x.Emoji); err != nil {
			return nil, err
		}
		out = append(out, &x)
	}
	return out, rows.Err()
}

// ---- blocks ----

func (r *ChatRepo) SetBlock(ctx context.Context, blockerID, blockedID string, on bool) error {
	if on {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO blocks (id, blocker_id, blocked_id) VALUES ($1,$2,$3)
			ON CONFLICT (blocker_id, blocked_id) DO NOTHING`, util.NewUUID(), blockerID, blockedID)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`DELETE FROM blocks WHERE blocker_id=$1 AND blocked_id=$2`, blockerID, blockedID)
	return err
}

func (r *ChatRepo) IsBlocked(ctx context.Context, a, b string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM blocks WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1))`,
		a, b).Scan(&exists)
	return exists, err
}

func (r *ChatRepo) BlockList(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT blocked_id FROM blocks WHERE blocker_id=$1 ORDER BY created_at DESC`, userID)
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

// ---- quick replies (business side, PRD §5.5.2) ----

func (r *ChatRepo) ListQuickReplies(ctx context.Context, businessID string) ([]*domain.QuickReply, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, business_id, text FROM quick_replies
		WHERE business_id=$1 ORDER BY sort_order`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.QuickReply
	for rows.Next() {
		var q domain.QuickReply
		if err := rows.Scan(&q.ID, &q.BusinessID, &q.Text); err != nil {
			return nil, err
		}
		out = append(out, &q)
	}
	return out, rows.Err()
}

func (r *ChatRepo) CreateQuickReply(ctx context.Context, businessID, text string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO quick_replies (id, business_id, text, sort_order)
		VALUES ($1, $2, $3, COALESCE((SELECT max(sort_order)+1 FROM quick_replies WHERE business_id = $2), 0))`,
		util.NewUUID(), businessID, text)
	return err
}

func (r *ChatRepo) DeleteQuickReply(ctx context.Context, businessID, id string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM quick_replies WHERE id=$1 AND business_id=$2`, id, businessID)
	return err
}

// ---- search & gallery ----

// SearchMessages searches one thread.
func (r *ChatRepo) SearchMessages(ctx context.Context, threadID, q string, limit int) ([]*domain.ChatMessage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+messageCols+` FROM chat_messages
		WHERE thread_id=$1 AND deleted_for <> 'everyone'
		  AND to_tsvector('simple', coalesce(body,'')) @@ plainto_tsquery('simple', $2)
		ORDER BY id DESC LIMIT $3`, threadID, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ChatMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SearchAllMessages searches across every thread the user participates in.
func (r *ChatRepo) SearchAllMessages(ctx context.Context, userID, q string, limit int) ([]*domain.ChatMessage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+messageCols+` FROM chat_messages m
		JOIN chat_participants cp ON cp.thread_id = m.thread_id AND cp.user_id = $1 AND cp.left_at IS NULL
		WHERE m.deleted_for <> 'everyone'
		  AND to_tsvector('simple', coalesce(m.body,'')) @@ plainto_tsquery('simple', $2)
		ORDER BY m.id DESC LIMIT $3`, userID, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ChatMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *ChatRepo) MediaInThread(ctx context.Context, threadID string, limit int) ([]*domain.ChatMessage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+messageCols+` FROM chat_messages
		WHERE thread_id=$1 AND type IN ('image','file','audio','video') AND deleted_for <> 'everyone'
		ORDER BY id DESC LIMIT $2`, threadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ChatMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- thread lifecycle ----

func (r *ChatRepo) SetThreadStatus(ctx context.Context, threadID, status string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE chat_threads SET status=$2 WHERE id=$1`, threadID, status)
	return err
}

func (r *ChatRepo) LeaveThread(ctx context.Context, threadID, userID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE chat_participants SET left_at = now() WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID)
	return err
}

// BannedWords returns the current banned word list.
func (r *ChatRepo) BannedWords(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT word FROM banned_words`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
