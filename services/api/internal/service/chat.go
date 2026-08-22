package service

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/ratelimit"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Chat — full messaging (PRD §5.5, §8.5): direct + business threads,
// media, reply, edit, delete (me/everyone), forward, reactions, receipts,
// typing, search, gallery, pin, export, blocks, quick replies.
type Chat struct {
	repos    *repo.Repos
	limiter  *ratelimit.RateLimiter
	notifier *Notifier
}

func NewChat(repos *repo.Repos, notifier *Notifier) *Chat {
	l := ratelimit.NewInMemory()
	return &Chat{repos: repos, limiter: &l, notifier: notifier}
}

// ---- thread access ----

func (c *Chat) GetOrCreateDirect(ctx context.Context, userID, otherID string) (*domain.ChatThread, error) {
	if userID == otherID {
		return nil, domain.ErrValidation.WithField("user_id", "You can't message yourself.")
	}
	other, err := c.repos.Users.GetByID(ctx, otherID)
	if err != nil || other == nil {
		return nil, domain.ErrNotFound
	}
	existing, err := c.repos.Chat.FindDirectThread(ctx, userID, otherID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	t, err := c.repos.Chat.CreateThread(ctx, "direct", "", userID)
	if err != nil {
		return nil, err
	}
	if err := c.repos.Chat.AddParticipant(ctx, t.ID, otherID, "user"); err != nil {
		return nil, err
	}
	return t, nil
}

func (c *Chat) GetOrCreateBusiness(ctx context.Context, userID, businessID string) (*domain.ChatThread, error) {
	b, err := c.repos.Businesses.GetByID(ctx, businessID)
	if err != nil || b == nil {
		return nil, domain.ErrNotFound
	}
	if b.OwnerID == userID {
		return nil, domain.ErrValidation.WithField("_", "You can't message your own business.")
	}
	existing, err := c.repos.Chat.FindBusinessThread(ctx, businessID, userID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	t, err := c.repos.Chat.CreateThread(ctx, "business", businessID, userID)
	if err != nil {
		return nil, err
	}
	if err := c.repos.Chat.AddParticipant(ctx, t.ID, b.OwnerID, "owner"); err != nil {
		return nil, err
	}
	return t, nil
}

func (c *Chat) checkAccess(ctx context.Context, threadID, userID string) (*domain.ChatThread, error) {
	t, err := c.repos.Chat.GetThread(ctx, threadID)
	if err != nil || t == nil {
		return nil, domain.ErrNotFound
	}
	ok, err := c.repos.Chat.IsParticipant(ctx, threadID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.ErrForbidden
	}
	return t, nil
}

// CheckAccess exposes membership verification for handlers that broadcast
// into a thread without otherwise touching it (e.g. typing).
func (c *Chat) CheckAccess(ctx context.Context, userID, threadID string) error {
	_, err := c.checkAccess(ctx, threadID, userID)
	return err
}

// ---- sending ----

type SendInput struct {
	Body            *string
	Type            string
	MediaID         *string
	ClientMsgID     string
	ReplyToID       *int64
	ForwardedFromID *int64
}

func (c *Chat) Send(ctx context.Context, userID, threadID string, in SendInput) (*domain.ChatMessage, error) {
	t, err := c.checkAccess(ctx, threadID, userID)
	if err != nil {
		return nil, err
	}

	// §8.5 rate limits: 1 msg/sec, 60/hour per user per thread.
	if c.limiter != nil {
		if _, _, ok := (*c.limiter).Allow("chat:sec:"+userID+":"+threadID, 1, time.Second); !ok {
			return nil, domain.ErrRateLimited
		}
		if _, retry, ok := (*c.limiter).Allow("chat:hour:"+userID+":"+threadID, 60, time.Hour); !ok {
			w := &domain.Error{Code: "rate_limited", Message: "Message limit reached. Try again in " + time.Until(time.Now().Add(retry)).Round(time.Minute).String() + ".", Status: 429}
			return nil, w
		}
	}

	// Dedupe by client_msg_id (at-least-once, PRD §5.5.3).
	if in.ClientMsgID != "" {
		existing, err := c.repos.Chat.MessageByClientID(ctx, threadID, in.ClientMsgID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return existing, nil
		}
	}

	// Blocks both ways (PRD §5.5.4).
	if t.Type == "direct" {
		participants, err := c.repos.Chat.ParticipantIDs(ctx, threadID)
		if err != nil {
			return nil, err
		}
		for _, pid := range participants {
			if pid == userID {
				continue
			}
			blocked, err := c.repos.Chat.IsBlocked(ctx, userID, pid)
			if err != nil {
				return nil, err
			}
			if blocked {
				return nil, domain.ErrValidation.WithField("_", "Messaging is unavailable with this user.")
			}
		}
	}

	msgType := in.Type
	if msgType == "" {
		msgType = "text"
	}
	if msgType == "text" {
		body := strings.TrimSpace(derefString(in.Body))
		if body == "" || len([]rune(body)) > 4000 {
			return nil, domain.ErrValidation.WithField("body", "Message must be 1–4000 characters.")
		}
		if err := c.checkBanned(ctx, body); err != nil {
			return nil, err
		}
	}

	role := "user"
	if t.Type == "business" {
		b, err := c.repos.Businesses.GetByID(ctx, derefString(t.BusinessID))
		if err == nil && b != nil && b.OwnerID == userID {
			role = "owner"
		}
	}
	if userID != "" && role == "user" {
		u, _ := c.repos.Users.GetByID(ctx, userID)
		if u != nil && u.IsAdmin() {
			role = "admin"
		}
	}

	// Reply target must be in the same thread.
	if in.ReplyToID != nil {
		ref, err := c.repos.Chat.MessageByID(ctx, *in.ReplyToID)
		if err != nil || ref == nil || ref.ThreadID != threadID {
			return nil, domain.ErrValidation.WithField("reply_to_id", "Reply target not found in this thread.")
		}
	}

	clientID := in.ClientMsgID
	if clientID == "" {
		clientID = util.NewUUID()
	}
	m := &domain.ChatMessage{
		ThreadID:       threadID,
		SenderID:       userID,
		SenderRole:     role,
		Type:           msgType,
		Body:           in.Body,
		ReplyToID:      in.ReplyToID,
		ForwardedFromID: in.ForwardedFromID,
		MediaID:        in.MediaID,
		ClientMsgID:    clientID,
		EditHistory:    []map[string]any{},
	}
	created, err := c.repos.Chat.CreateMessage(ctx, m)
	if err != nil {
		return nil, err
	}
	_ = c.repos.Chat.TouchLastMessage(ctx, threadID)

	// Link preview: enrich after insert (best-effort).
	if created.Type == "text" && created.Body != nil && urlRe.MatchString(*created.Body) && created.LinkPreview == nil {
		if preview, ok := fetchLinkPreview(ctx, *created.Body); ok {
			if _, err := c.repos.Exec(ctx, `
				UPDATE chat_messages SET link_preview = $2 WHERE id = $1`,
				created.ID, preview); err == nil {
				created.LinkPreview = preview
			}
		}
	}

	// chat_start event once per user/day (PRD §3 weights).
	key := userID + ":business:" + derefString(t.BusinessID) + ":chat_start:" + time.Now().Format("2006-01-02")
	_, _ = c.repos.Engagement.InsertEvent(ctx, userID, "business", derefString(t.BusinessID), "chat_start", 8, key)

	// Notify other participants (in-app + push handled at the WS layer).
	participants, err := c.repos.Chat.ParticipantIDs(ctx, threadID)
	if err == nil {
		for _, pid := range participants {
			if pid == userID {
				continue
			}
			c.notifier.Create(ctx, pid, "message_received", map[string]any{
				"thread_id": threadID, "message_id": created.ID, "by": userID,
				"body": derefString(created.Body),
			})
		}
	}

	return created, nil
}

func (c *Chat) checkBanned(ctx context.Context, text string) error {
	words, err := c.repos.Chat.BannedWords(ctx)
	if err != nil {
		return err
	}
	// Allowlist (site config) exempts false positives (PRD E13).
	allow := map[string]bool{}
	var allowWords []string
	_ = c.repos.QueryRow(ctx, `
		SELECT coalesce(value->'words', '[]'::jsonb) FROM site_config WHERE key = 'banned_words_allowlist'`).
		Scan(&allowWords)
	for _, w := range allowWords {
		allow[strings.ToLower(w)] = true
	}
	lower := strings.ToLower(text)
	for _, w := range words {
		if w != "" && !allow[strings.ToLower(w)] && strings.Contains(lower, strings.ToLower(w)) {
			return domain.ErrValidation.WithField("body", "Your message contains a blocked word.")
		}
	}
	return nil
}

// ---- pinned messages (PRD §5.5.2) ----

func (c *Chat) PinMessage(ctx context.Context, userID, threadID string, messageID int64, on bool) error {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return err
	}
	m, err := c.repos.Chat.MessageByID(ctx, messageID)
	if err != nil || m == nil || m.ThreadID != threadID {
		return domain.ErrNotFound
	}
	var pinned []string
	if err := c.repos.QueryRow(ctx, `
		SELECT pinned_message_ids FROM chat_participants WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID).Scan(&pinned); err != nil {
		return err
	}
	if pinned == nil {
		pinned = []string{}
	}
	if on {
		for _, p := range pinned {
			if p == itoaInt64(messageID) {
				return nil
			}
		}
		if len(pinned) >= 5 {
			return domain.ErrValidation.WithField("_", "Max 5 pinned messages.")
		}
		pinned = append(pinned, itoaInt64(messageID))
	} else {
		filtered := pinned[:0]
		for _, p := range pinned {
			if p != itoaInt64(messageID) {
				filtered = append(filtered, p)
			}
		}
		pinned = filtered
	}
	_, err = c.repos.Exec(ctx, `
		UPDATE chat_participants SET pinned_message_ids = $3 WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID, pinned)
	return err
}

func (c *Chat) Pinned(ctx context.Context, userID, threadID string) ([]int64, error) {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return nil, err
	}
	var pinned []string
	if err := c.repos.QueryRow(ctx, `
		SELECT pinned_message_ids FROM chat_participants WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID).Scan(&pinned); err != nil {
		return nil, err
	}
	out := []int64{}
	for _, p := range pinned {
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out, nil
}

func itoaInt64(n int64) string { return strconv.FormatInt(n, 10) }

// ---- link previews (PRD §5.5.2) ----

var urlRe = regexp.MustCompile(`https?://[^\s]+`)

// fetchLinkPreview extracts og: metadata from the first URL in a message.
// SSRF-safe: http/https only, 2s timeout, 256KB cap, private IPs rejected (§9.3).
func fetchLinkPreview(ctx context.Context, text string) (map[string]any, bool) {
	m := urlRe.FindString(text)
	if m == "" {
		return nil, false
	}
	u, err := url.Parse(m)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return nil, false
	}
	if !strings.Contains(host, ".") {
		return nil, false // bare hostnames resolved internally could hit the loopback
	}

	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("User-Agent", "BizVerseBot/1.0 (+https://bizverse.app)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, false
	}
	title := firstMeta(body, "og:title", "twitter:title", "title")
	desc := firstMeta(body, "og:description", "twitter:description", "description")
	image := firstMeta(body, "og:image", "twitter:image")
	if title == "" && desc == "" && image == "" {
		return nil, false
	}
	// Only absolute image URLs (no data: or file:).
	if image != "" {
		if iu, err := url.Parse(image); err != nil || (iu.Scheme != "http" && iu.Scheme != "https") {
			image = ""
		}
	}
	return map[string]any{"url": m, "title": title, "description": desc, "image": image}, true
}

func firstMeta(html []byte, keys ...string) string {
	lower := strings.ToLower(string(html))
	for _, key := range keys {
		// Match name="x" or property="x" (double or single quotes).
		for _, attr := range []string{`name="`, `property="`, `name='`, `property='`} {
			needle := attr + key + `"`
			idx := strings.Index(lower, needle)
			if idx < 0 {
				needle = attr + key + `'`
				idx = strings.Index(lower, needle)
			}
			if idx < 0 {
				continue
			}
			contentIdx := strings.Index(lower[idx:], `content=`)
			if contentIdx < 0 {
				continue
			}
			start := idx + contentIdx + len(`content=`)
			if start < len(lower) && (lower[start] == '"' || lower[start] == '\'') {
				q := lower[start]
				start++
				end := strings.Index(lower[start:], string(q))
				if end > 0 && end < 500 {
					out := string(html[start : start+end])
					out = htmlUnescape(out)
					if len(out) > 300 {
						out = out[:300] + "…"
					}
					return out
				}
			}
		}
	}
	// Last resort: the <title> element.
	if i := strings.Index(lower, "<title>"); i >= 0 {
		start := i + len("<title>")
		end := strings.Index(lower[start:], "</title>")
		if end > 0 && end < 500 {
			out := string(html[start : start+end])
			out = htmlUnescape(out)
			if len(out) > 300 {
				out = out[:300] + "…"
			}
			return out
		}
	}
	return ""
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
}

// ---- edit / delete ----

func (c *Chat) Edit(ctx context.Context, userID string, messageID int64, text string) (*domain.ChatMessage, error) {
	m, err := c.repos.Chat.MessageByID(ctx, messageID)
	if err != nil || m == nil {
		return nil, domain.ErrNotFound
	}
	if m.SenderID != userID {
		return nil, domain.ErrForbidden
	}
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 4000 {
		return nil, domain.ErrValidation.WithField("body", "Message must be 1–4000 characters.")
	}
	if err := c.checkBanned(ctx, text); err != nil {
		return nil, err
	}
	history := m.EditHistory
	history = append(history, map[string]any{"text": m.Body, "at": time.Now()})
	if err := c.repos.Chat.UpdateMessageText(ctx, messageID, text, history); err != nil {
		return nil, err
	}
	return c.repos.Chat.MessageByID(ctx, messageID)
}

func (c *Chat) Delete(ctx context.Context, userID string, messageID int64, scope string) (*domain.ChatMessage, error) {
	if scope != "me" && scope != "everyone" {
		return nil, domain.ErrValidation.WithField("scope", "Scope must be me or everyone.")
	}
	m, err := c.repos.Chat.MessageByID(ctx, messageID)
	if err != nil || m == nil {
		return nil, domain.ErrNotFound
	}
	if m.SenderID != userID {
		return nil, domain.ErrForbidden
	}
	if scope == "everyone" {
		participants, err := c.repos.Chat.ParticipantIDs(ctx, m.ThreadID)
		if err != nil {
			return nil, err
		}
		readByOther := false
		for _, pid := range participants {
			if pid == userID {
				continue
			}
			lastRead, err := c.repos.Chat.LastRead(ctx, m.ThreadID, pid)
			if err == nil && lastRead >= m.ID {
				readByOther = true
				break
			}
		}
		// §8.5: delete for everyone only within 15 min, or if never read.
		if time.Since(m.CreatedAt) > 15*time.Minute && readByOther {
			return nil, domain.ErrValidation.WithField("_", "This message was already read. Delete for yourself only.")
		}
	}
	if err := c.repos.Chat.DeleteMessage(ctx, messageID, scope); err != nil {
		return nil, err
	}
	return c.repos.Chat.MessageByID(ctx, messageID)
}

// ---- reactions ----

func (c *Chat) React(ctx context.Context, userID string, messageID int64, emoji string, on bool) error {
	m, err := c.repos.Chat.MessageByID(ctx, messageID)
	if err != nil || m == nil {
		return domain.ErrNotFound
	}
	// Membership check: message IDs are sequential and enumerable, so a
	// missing access check would let any user react inside any thread.
	if _, err := c.checkAccess(ctx, m.ThreadID, userID); err != nil {
		return err
	}
	if on {
		if emoji == "" {
			return domain.ErrValidation.WithField("emoji", "Emoji is required.")
		}
		existing, _ := c.repos.Chat.ReactionByUser(ctx, messageID, userID)
		if err := c.repos.Chat.SetReaction(ctx, messageID, userID, emoji); err != nil {
			return err
		}
		// Only notify when a new reaction lands (not on emoji swaps).
		if m.SenderID != userID && existing == "" {
			c.notifier.Create(ctx, m.SenderID, "reaction_added", map[string]any{
				"thread_id": m.ThreadID, "message_id": messageID, "by": userID, "emoji": emoji,
			})
		}
		return nil
	}
	return c.repos.Chat.RemoveReaction(ctx, messageID, userID)
}

// ---- forward ----

func (c *Chat) Forward(ctx context.Context, userID, threadID string, messageID int64) (*domain.ChatMessage, error) {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return nil, err
	}
	m, err := c.repos.Chat.MessageByID(ctx, messageID)
	if err != nil || m == nil {
		return nil, domain.ErrNotFound
	}
	// The forwarder must also be a participant of the SOURCE thread —
	// otherwise guessed IDs leak other threads' message bodies (PRD §5.5.4).
	if _, err := c.checkAccess(ctx, m.ThreadID, userID); err != nil {
		return nil, err
	}
	if m.DeletedFor == "everyone" {
		return nil, domain.ErrValidation.WithField("_", "This message was deleted.")
	}
	body := m.Body
	if body != nil {
		b := *body
		body = &b
	}
	return c.Send(ctx, userID, threadID, SendInput{
		Body:            body,
		Type:            m.Type,
		MediaID:         m.MediaID,
		ClientMsgID:     util.NewUUID(),
		ForwardedFromID: &messageID,
	})
}

// ---- receipts & read ----

func (c *Chat) MarkRead(ctx context.Context, userID, threadID string, lastRead int64) error {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return err
	}
	return c.repos.Chat.SetLastRead(ctx, threadID, userID, lastRead)
}

// ---- blocks ----

func (c *Chat) Block(ctx context.Context, userID, blockedID string, on bool) error {
	if userID == blockedID {
		return domain.ErrValidation.WithField("_", "You can't block yourself.")
	}
	return c.repos.Chat.SetBlock(ctx, userID, blockedID, on)
}

func (c *Chat) BlockList(ctx context.Context, userID string) ([]string, error) {
	return c.repos.Chat.BlockList(ctx, userID)
}

// ---- quick replies (owner) ----

func (c *Chat) QuickReplies(ctx context.Context, ownerID, businessID string) ([]*domain.QuickReply, error) {
	if err := c.ownBusiness(ctx, ownerID, businessID); err != nil {
		return nil, err
	}
	return c.repos.Chat.ListQuickReplies(ctx, businessID)
}

func (c *Chat) AddQuickReply(ctx context.Context, ownerID, businessID, text string) error {
	if err := c.ownBusiness(ctx, ownerID, businessID); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 500 {
		return domain.ErrValidation.WithField("text", "Quick reply must be 1–500 characters.")
	}
	list, err := c.repos.Chat.ListQuickReplies(ctx, businessID)
	if err != nil {
		return err
	}
	if len(list) >= 20 {
		return domain.ErrValidation.WithField("_", "Max 20 quick replies (PRD §5.5.2).")
	}
	return c.repos.Chat.CreateQuickReply(ctx, businessID, text)
}

func (c *Chat) RemoveQuickReply(ctx context.Context, ownerID, businessID, id string) error {
	if err := c.ownBusiness(ctx, ownerID, businessID); err != nil {
		return err
	}
	return c.repos.Chat.DeleteQuickReply(ctx, businessID, id)
}

// ---- thread lifecycle ----

func (c *Chat) CloseThread(ctx context.Context, userID, threadID string) error {
	t, err := c.checkAccess(ctx, threadID, userID)
	if err != nil {
		return err
	}
	if t.Type == "business" {
		b, err := c.repos.Businesses.GetByID(ctx, derefString(t.BusinessID))
		if err == nil && b != nil && b.OwnerID == userID {
			return c.repos.Chat.SetThreadStatus(ctx, threadID, "closed")
		}
	}
	return domain.ErrForbidden
}

func (c *Chat) LeaveThread(ctx context.Context, userID, threadID string) error {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return err
	}
	return c.repos.Chat.LeaveThread(ctx, threadID, userID)
}

// ---- reads ----

func (c *Chat) Threads(ctx context.Context, userID, businessID string) ([]*domain.ThreadListItem, error) {
	return c.repos.Chat.ThreadsByUser(ctx, userID, businessID)
}

func (c *Chat) Messages(ctx context.Context, userID, threadID string, before int64, limit int) ([]*domain.ChatMessage, error) {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return nil, err
	}
	return c.repos.Chat.MessagesByThread(ctx, threadID, before, limit)
}

func (c *Chat) SearchThread(ctx context.Context, userID, threadID, q string, limit int) ([]*domain.ChatMessage, error) {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return nil, err
	}
	return c.repos.Chat.SearchMessages(ctx, threadID, q, limit)
}

// SearchAllMessages searches the user's threads (PRD §5.5.2).
func (c *Chat) SearchAllMessages(ctx context.Context, userID, q string, limit int) ([]*domain.ChatMessage, error) {
	return c.repos.Chat.SearchAllMessages(ctx, userID, q, limit)
}

func (c *Chat) Gallery(ctx context.Context, userID, threadID string, limit int) ([]*domain.ChatMessage, error) {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return nil, err
	}
	return c.repos.Chat.MediaInThread(ctx, threadID, limit)
}

// Export builds the full thread JSON for the user (PRD §5.5.2).
func (c *Chat) Export(ctx context.Context, userID, threadID string) ([]byte, error) {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return nil, err
	}
	msgs, err := c.repos.Chat.MessagesByThread(ctx, threadID, 0, 100000)
	if err != nil {
		return nil, err
	}
	t, err := c.repos.Chat.GetThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"thread_id": threadID, "type": t.Type, "exported_at": time.Now(), "messages": msgs})
}

func (c *Chat) ownBusiness(ctx context.Context, ownerID, businessID string) error {
	b, err := c.repos.Businesses.GetByID(ctx, businessID)
	if err != nil || b == nil {
		return domain.ErrNotFound
	}
	if b.OwnerID != ownerID {
		return domain.ErrForbidden
	}
	return nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var _ = repo.Repos{}
