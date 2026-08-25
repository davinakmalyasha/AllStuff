package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
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

	// §8.5 anti-abuse state (per instance; resets are acceptable).
	burstMu   sync.Mutex
	bursts    map[string][]burstEntry // userID → recent sends
	strikeMu  sync.Mutex
	strikes   map[string][]time.Time // userID → recent blocked-word rejections
	cooldowns map[string]time.Time   // userID → send lockout until

	// Banned-word + allowlist cache: one query per message on every send was
	// a hot-path N+1; 60s TTL keeps moderation near-real-time.
	cacheMu   sync.Mutex
	banWords  []string
	banAllow  map[string]bool
	banLoaded time.Time
}

type burstEntry struct {
	threadID string
	body     string
	at       time.Time
}

func NewChat(repos *repo.Repos, notifier *Notifier) *Chat {
	l := ratelimit.NewInMemory()
	return &Chat{
		repos: repos, limiter: &l, notifier: notifier,
		bursts:    map[string][]burstEntry{},
		strikes:   map[string][]time.Time{},
		cooldowns: map[string]time.Time{},
	}
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
		// Two concurrent first-messages can race past the find; prefer the
		// winner's thread over creating a duplicate.
		if again, ferr := c.repos.Chat.FindDirectThread(ctx, userID, otherID); ferr == nil && again != nil {
			return again, nil
		}
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

	// §8.5 anti-abuse: blocked-word cooldown and identical-message bursts.
	if err := c.abuseGate(userID); err != nil {
		return nil, err
	}
	if err := c.noteBurst(userID, threadID, derefString(in.Body)); err != nil {
		return nil, err
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

	// Blocks both ways, enforced in ANY thread type (PRD §5.5.4).
	{
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
		if err := c.checkBannedStriked(ctx, userID, body); err != nil {
			return nil, err
		}
	}

	// Media attachments: the caller must own the upload and its kind must
	// match the declared message type. Without this check a participant
	// could plant someone else's private media UUID (e.g. a verification
	// document or another thread's image) into their own message and then
	// pass the chat-media membership gate to download it.
	if in.MediaID != nil && *in.MediaID != "" {
		var wantKind domain.MediaKind
		switch msgType {
		case "image":
			wantKind = domain.MediaChatImage
		case "file":
			wantKind = domain.MediaChatFile
		case "audio":
			wantKind = domain.MediaChatAudio
		case "video":
			wantKind = domain.MediaChatVideo
		default:
			return nil, domain.ErrValidation.WithField("media_id", "This message type cannot carry an attachment.")
		}
		item, err := c.repos.Media.GetByID(ctx, *in.MediaID)
		if err != nil {
			return nil, err
		}
		if item == nil || item.UploaderID != userID || item.Kind != wantKind {
			return nil, domain.ErrValidation.WithField("media_id", "Attachment not found or not yours.")
		}
	} else if msgType == "image" || msgType == "file" || msgType == "audio" || msgType == "video" {
		return nil, domain.ErrValidation.WithField("media_id", "This message type requires an attachment.")
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

	// Closed business threads: the owner can't send, a customer's reply
	// reopens the conversation (PRD §5.5.2).
	if t.Status == "closed" && t.Type == "business" {
		if role == "owner" {
			return nil, domain.ErrValidation.WithField("_", "This conversation is closed.")
		}
		if err := c.repos.Chat.SetThreadStatus(ctx, threadID, "open"); err != nil {
			return nil, err
		}
		if _, serr := c.systemMessage(ctx, threadID, "Conversation reopened."); serr != nil {
			slog.Warn("chat system message", "err", serr, "thread", threadID)
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
	// Atomic core write: message insert + thread preview/touch commit or
	// roll back together (PRD §7.3). Best-effort side effects below run
	// after the commit so they can never undo a delivered message.
	tx, err := c.repos.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	trx := repo.NewForTx(tx)
	created, err := trx.Chat.CreateMessage(ctx, m)
	if err == nil {
		err = trx.Chat.TouchLastMessage(ctx, threadID)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

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

	// Notify other participants (skipping thread-muted ones, PRD §5.5.1).
	participants, err := c.repos.Chat.ParticipantIDs(ctx, threadID)
	if err == nil {
		muted := c.mutedParticipants(ctx, threadID)
		for _, pid := range participants {
			if pid == userID || muted[pid] {
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
	words, allow, err := c.bannedLists(ctx)
	if err != nil {
		return err
	}
	lower := strings.ToLower(text)
	for _, w := range words {
		if w != "" && !allow[strings.ToLower(w)] && strings.Contains(lower, strings.ToLower(w)) {
			return domain.ErrValidation.WithField("body", "Your message contains a blocked word.")
		}
	}
	return nil
}

// bannedLists returns the banned words and allowlist with a 60s cache.
func (c *Chat) bannedLists(ctx context.Context) ([]string, map[string]bool, error) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.banWords != nil && time.Since(c.banLoaded) < 60*time.Second {
		return c.banWords, c.banAllow, nil
	}
	words, err := c.repos.Chat.BannedWords(ctx)
	if err != nil {
		return nil, nil, err
	}
	var allowWords []string
	_ = c.repos.QueryRow(ctx, `
		SELECT coalesce(value->'words', '[]'::jsonb) FROM site_config WHERE key = 'banned_words_allowlist'`).
		Scan(&allowWords)
	allow := make(map[string]bool, len(allowWords))
	for _, w := range allowWords {
		allow[strings.ToLower(w)] = true
	}
	c.banWords = words
	c.banAllow = allow
	c.banLoaded = time.Now()
	return words, allow, nil
}

// checkBannedStriked wraps the banned-word check with the §8.5 escalation:
// three rejections within 10 minutes lock sending for 5 minutes.
func (c *Chat) checkBannedStriked(ctx context.Context, userID, body string) error {
	err := c.checkBanned(ctx, body)
	if err == nil {
		return nil
	}
	c.strikeMu.Lock()
	defer c.strikeMu.Unlock()
	now := time.Now()
	recent := c.strikes[userID][:0]
	for _, t := range c.strikes[userID] {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	if len(recent) >= 3 {
		c.cooldowns[userID] = now.Add(5 * time.Minute)
		delete(c.strikes, userID)
	} else {
		c.strikes[userID] = recent
	}
	return err
}

// abuseGate enforces the blocked-word cooldown (§8.5/E13).
func (c *Chat) abuseGate(userID string) error {
	c.strikeMu.Lock()
	defer c.strikeMu.Unlock()
	if until, ok := c.cooldowns[userID]; ok {
		if time.Now().Before(until) {
			return domain.ErrValidation.WithField("_",
				"Too many blocked attempts. You can send again in "+time.Until(until).Round(time.Second).String()+".")
		}
		delete(c.cooldowns, userID)
	}
	return nil
}

// noteBurst blocks identical-message blasts across threads (§8.5): the same
// non-empty body sent to more than 5 distinct threads within 10 minutes.
func (c *Chat) noteBurst(userID, threadID, body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	now := time.Now()
	c.burstMu.Lock()
	defer c.burstMu.Unlock()
	recent := c.bursts[userID][:0]
	for _, e := range c.bursts[userID] {
		if now.Sub(e.at) < 10*time.Minute {
			recent = append(recent, e)
		}
	}
	distinct := map[string]bool{}
	sameBody := 0
	for _, e := range recent {
		if e.body == body && !distinct[e.threadID] {
			distinct[e.threadID] = true
			sameBody++
		}
	}
	if sameBody >= 5 && !distinct[threadID] {
		return domain.ErrValidation.WithField("_", "You're sending the same message to too many conversations. Slow down.")
	}
	recent = append(recent, burstEntry{threadID: threadID, body: body, at: now})
	c.bursts[userID] = recent
	if len(c.bursts) > 10_000 { // crude memory bound
		for u, es := range c.bursts {
			if len(es) == 0 || now.Sub(es[len(es)-1].at) > 30*time.Minute {
				delete(c.bursts, u)
			}
		}
	}
	return nil
}

// mutedParticipants returns the set of participant IDs whose muted_until is
// in the future (PRD §5.5.1 thread mute).
func (c *Chat) mutedParticipants(ctx context.Context, threadID string) map[string]bool {
	out := map[string]bool{}
	rows, err := c.repos.Query(ctx, `
		SELECT user_id FROM chat_participants
		WHERE thread_id = $1 AND muted_until IS NOT NULL AND muted_until > now()`, threadID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// SetMuted toggles thread mute for a participant (~8 days ≈ "until I look").
// Passing false clears it. Uses chat_participants.muted_until.
func (c *Chat) SetMuted(ctx context.Context, userID, threadID string, on bool) error {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return err
	}
	if on {
		_, err := c.repos.Exec(ctx,
			`UPDATE chat_participants SET muted_until = now() + interval '8 days' WHERE thread_id=$1 AND user_id=$2`,
			threadID, userID)
		return err
	}
	_, err := c.repos.Exec(ctx,
		`UPDATE chat_participants SET muted_until = NULL WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID)
	return err
}

// systemMessage inserts a type=system notice visible to every participant.
func (c *Chat) systemMessage(ctx context.Context, threadID, text string) (*domain.ChatMessage, error) {
	m := &domain.ChatMessage{
		ThreadID:    threadID,
		SenderRole:  "system",
		Type:        "system",
		Body:        &text,
		ClientMsgID: util.NewUUID(),
		EditHistory: []map[string]any{},
	}
	return c.repos.Chat.CreateMessage(ctx, m)
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
	var pinned []int64
	if err := c.repos.QueryRow(ctx, `
		SELECT pinned_message_ids FROM chat_participants WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID).Scan(&pinned); err != nil {
		return err
	}
	if pinned == nil {
		pinned = []int64{}
	}
	if on {
		for _, p := range pinned {
			if p == messageID {
				return nil
			}
		}
		if len(pinned) >= 5 {
			return domain.ErrValidation.WithField("_", "Max 5 pinned messages.")
		}
		pinned = append(pinned, messageID)
	} else {
		filtered := pinned[:0]
		for _, p := range pinned {
			if p != messageID {
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
	var pinned []int64
	if err := c.repos.QueryRow(ctx, `
		SELECT pinned_message_ids FROM chat_participants WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID).Scan(&pinned); err != nil {
		return nil, err
	}
	if pinned == nil {
		pinned = []int64{}
	}
	return pinned, nil
}

// ---- link previews (PRD §5.5.2) ----

var urlRe = regexp.MustCompile(`https?://[^\s]+`)

// isPublicIP reports whether ip is safe to dial: not loopback, private,
// link-local, unspecified, or multicast. IPv4-mapped IPv6 (::ffff:127.0.0.1)
// is normalized first — those accessors return false on the mapped form.
func isPublicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}

// previewTransport dials only after resolving the hostname and validating
// every resolved address. Because it sits at the dial layer it covers ALL
// fetch paths: direct URLs, every redirect hop, and DNS rebinding (the name
// is re-resolved per dial and checked again).
var previewClient = &http.Client{
	Timeout: 4 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if port != "80" && port != "443" {
				return nil, fmt.Errorf("link preview: port %s not allowed", port)
			}
			if net.ParseIP(host) == nil && !strings.Contains(host, ".") {
				return nil, fmt.Errorf("link preview: bare hostname rejected")
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ia := range ips {
				if !isPublicIP(ia.IP) {
					return nil, fmt.Errorf("link preview: private address blocked")
				}
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return http.ErrUseLastResponse
		}
		return nil // target validation happens at dial time for every hop
	},
}

// fetchLinkPreview extracts og: metadata from the first URL in a message.
// SSRF-safe: http/https only, dial-level IP validation on every redirect hop
// and DNS resolution, 256KB cap (§9.3).
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
	if ip := net.ParseIP(host); ip != nil && !isPublicIP(ip) {
		return nil, false
	}
	if net.ParseIP(host) == nil && !strings.Contains(host, ".") {
		return nil, false // bare hostnames resolved internally could hit the loopback
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("User-Agent", "BizVerseBot/1.0 (+https://bizverse.app)")
	resp, err := previewClient.Do(req)
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
		// §8.5: max 20 reaction actions per 5 minutes.
		if c.limiter != nil {
			if _, _, ok := (*c.limiter).Allow("react:"+userID, 20, 5*time.Minute); !ok {
				return domain.ErrRateLimited
			}
		}
		if emoji == "" {
			return domain.ErrValidation.WithField("emoji", "Emoji is required.")
		}
		existing, _ := c.repos.Chat.ReactionByUser(ctx, messageID, userID)
		if err := c.repos.Chat.SetReaction(ctx, messageID, userID, emoji); err != nil {
			return err
		}
		// Only notify when a new reaction lands (not on emoji swaps) and
		// deduped: remove→re-add cycles previously re-notified every time.
		if m.SenderID != userID && existing == "" {
			c.notifier.CreateDeduped(ctx, m.SenderID, "reaction_added",
				fmt.Sprintf("react:%d:%s", messageID, userID), 24*time.Hour, map[string]any{
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

// ---- pinned threads (PRD §5.5.1: max 5 per participant) ----

func (c *Chat) PinThread(ctx context.Context, userID, threadID string, on bool) error {
	if _, err := c.checkAccess(ctx, threadID, userID); err != nil {
		return err
	}
	var pinned []string
	if err := c.repos.QueryRow(ctx,
		`SELECT pinned_thread_ids FROM chat_participants WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID).Scan(&pinned); err != nil {
		return err
	}
	if pinned == nil {
		pinned = []string{}
	}
	has := false
	for _, p := range pinned {
		if p == threadID {
			has = true
			break
		}
	}
	if on == has {
		return nil // already in desired state
	}
	if on {
		if len(pinned) >= 5 {
			return domain.ErrValidation.WithField("_", "Max 5 pinned conversations (PRD §5.5.1).")
		}
		pinned = append(pinned, threadID)
	} else {
		out := pinned[:0]
		for _, p := range pinned {
			if p != threadID {
				out = append(out, p)
			}
		}
		pinned = out
	}
	_, err := c.repos.Exec(ctx,
		`UPDATE chat_participants SET pinned_thread_ids=$3 WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID, pinned)
	return err
}

// PinnedThreads returns the caller's pinned conversations in pin order.
func (c *Chat) PinnedThreads(ctx context.Context, userID string) ([]*domain.ThreadListItem, error) {
	var pinned []string
	if err := c.repos.QueryRow(ctx, `
		SELECT coalesce(array_agg(tid ORDER BY ord), '{}')
		FROM chat_participants p,
		     unnest(p.pinned_thread_ids) WITH ORDINALITY AS u(tid, ord)
		WHERE p.user_id = $1`, userID).Scan(&pinned); err != nil || len(pinned) == 0 {
		return []*domain.ThreadListItem{}, err
	}
	all, err := c.repos.Chat.ThreadsByUser(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, id := range pinned {
		idx[id] = i
	}
	out := make([]*domain.ThreadListItem, 0, len(pinned))
	for _, t := range all {
		if _, ok := idx[t.ID]; ok {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return idx[out[i].ID] < idx[out[j].ID] })
	return out, nil
}

func (c *Chat) CloseThread(ctx context.Context, userID, threadID string) error {
	t, err := c.checkAccess(ctx, threadID, userID)
	if err != nil {
		return err
	}
	if t.Type == "business" {
		b, err := c.repos.Businesses.GetByID(ctx, derefString(t.BusinessID))
		if err == nil && b != nil && b.OwnerID == userID {
			if err := c.repos.Chat.SetThreadStatus(ctx, threadID, "closed"); err != nil {
				return err
			}
			// System notice so every participant sees why (PRD §5.5.2).
			if _, serr := c.systemMessage(ctx, threadID, "The business closed this conversation."); serr != nil {
				slog.Warn("chat system message", "err", serr, "thread", threadID)
			}
			return nil
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
