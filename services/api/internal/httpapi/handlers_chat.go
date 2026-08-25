package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/security"
	"bizverse/api/internal/service"
	"bizverse/api/internal/ws"
)

type Frame = ws.Frame

// ---- threads ----

func (s *Server) handleThreads(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.URL.Query().Get("business_id")
	list, err := s.deps.Chat.Threads(r.Context(), user.ID, businessID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"threads": list})
}

func (s *Server) handleThreadCreate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		UserID     string `json:"user_id"`
		BusinessID string `json:"business_id"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	var t *domain.ChatThread
	var err error
	if in.BusinessID != "" {
		t, err = s.deps.Chat.GetOrCreateBusiness(r.Context(), user.ID, in.BusinessID)
	} else if in.UserID != "" {
		t, err = s.deps.Chat.GetOrCreateDirect(r.Context(), user.ID, in.UserID)
	} else {
		fail(w, domain.ErrValidation.WithField("_", "user_id or business_id is required."))
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"thread": t})
}

func (s *Server) handleThreadDetail(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	threadID := r.PathValue("id")
	msgs, err := s.deps.Chat.Messages(r.Context(), user.ID, threadID, 0, 50)
	if err != nil {
		fail(w, err)
		return
	}
	t, _ := s.deps.Repos.Chat.GetThread(r.Context(), threadID)
	ok(w, map[string]any{"thread": t, "messages": msgs})
}

// ---- messages ----

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	threadID := r.PathValue("id")
	var in struct {
		Body        *string `json:"body"`
		Type        string  `json:"type"`
		MediaID     *string `json:"media_id"`
		ClientMsgID string  `json:"client_msg_id"`
		ReplyToID   *int64  `json:"reply_to_id"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	msg, err := s.deps.Chat.Send(r.Context(), user.ID, threadID, service.SendInput{
		Body: in.Body, Type: in.Type, MediaID: in.MediaID, ClientMsgID: in.ClientMsgID, ReplyToID: in.ReplyToID,
	})
	if err != nil {
		fail(w, err)
		return
	}
	s.broadcastThread(r.Context(), threadID, Frame{Type: "message.new", Payload: msg})
	// Web-push fan-out is best-effort and each endpoint can take seconds:
	// never block the sender's HTTP response on it. Detached context — the
	// request ctx dies with the response.
	go func(threadID string, msg *domain.ChatMessage) {
		pctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		s.pushToThread(pctx, threadID, msg)
	}(threadID, msg)
	created(w, map[string]any{"message": msg})
}

func (s *Server) handleMessageEdit(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	msg, err := s.deps.Chat.Edit(r.Context(), user.ID, parseID(r.PathValue("messageId")), in.Body)
	if err != nil {
		fail(w, err)
		return
	}
	s.broadcastThread(r.Context(), msg.ThreadID, Frame{Type: "message.edited", Payload: msg})
	ok(w, map[string]any{"message": msg})
}

func (s *Server) handleMessageDelete(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "me"
	}
	msg, err := s.deps.Chat.Delete(r.Context(), user.ID, parseID(r.PathValue("messageId")), scope)
	if err != nil {
		fail(w, err)
		return
	}
	s.broadcastThread(r.Context(), msg.ThreadID, Frame{Type: "message.deleted", Payload: map[string]any{
		"message_id": msg.ID, "deleted_for": msg.DeletedFor,
	}})
	ok(w, map[string]any{"message": msg})
}

func (s *Server) handleMessageReaction(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	msgID := parseID(r.PathValue("messageId"))
	if r.Method == http.MethodDelete {
		if err := s.deps.Chat.React(r.Context(), user.ID, msgID, "", false); err != nil {
			fail(w, err)
			return
		}
		s.broadcastMessage(r.Context(), msgID, Frame{Type: "reaction.updated", Payload: map[string]any{"message_id": msgID, "user_id": user.ID, "emoji": ""}})
		ok(w, map[string]any{"reacted": false})
		return
	}
	var in struct {
		Emoji string `json:"emoji"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Chat.React(r.Context(), user.ID, msgID, in.Emoji, true); err != nil {
		fail(w, err)
		return
	}
	s.broadcastMessage(r.Context(), msgID, Frame{Type: "reaction.updated", Payload: map[string]any{"message_id": msgID, "user_id": user.ID, "emoji": in.Emoji}})
	ok(w, map[string]any{"reacted": true})
}

func (s *Server) handleMessageForward(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		ThreadID string `json:"thread_id"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	msg, err := s.deps.Chat.Forward(r.Context(), user.ID, in.ThreadID, parseID(r.PathValue("messageId")))
	if err != nil {
		fail(w, err)
		return
	}
	s.broadcastThread(r.Context(), in.ThreadID, Frame{Type: "message.new", Payload: msg})
	created(w, map[string]any{"message": msg})
}

// ---- receipts / typing ----

func (s *Server) handleThreadRead(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		LastReadMessageID int64 `json:"last_read_message_id"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Chat.MarkRead(r.Context(), user.ID, r.PathValue("id"), in.LastReadMessageID); err != nil {
		fail(w, err)
		return
	}
	s.broadcastThread(r.Context(), r.PathValue("id"), Frame{Type: "receipt.read", Payload: map[string]any{
		"thread_id": r.PathValue("id"), "user_id": user.ID, "last_read_message_id": in.LastReadMessageID,
	}})
	noContent(w)
}

func (s *Server) handleTyping(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	// Membership check first: without it any authenticated caller could
	// broadcast typing frames into arbitrary threads (activity oracle).
	if err := s.deps.Chat.CheckAccess(r.Context(), user.ID, r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	// Typing is a high-frequency, low-value signal: deliver only to
	// connections that subscribed to this thread (no participant query).
	s.deps.Hub.DeliverTypingToThread(r.PathValue("id"), Frame{Type: "typing", Payload: map[string]any{
		"thread_id": r.PathValue("id"), "user_id": user.ID, "is_typing": true,
	}})
	noContent(w)
}

// ---- search / gallery / export ----

func (s *Server) handleThreadSearch(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 20)
	res, err := s.deps.Chat.SearchThread(r.Context(), user.ID, r.PathValue("id"), r.URL.Query().Get("q"), limit)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"messages": res})
}

// Global message search across the user's threads (PRD §5.5.2).
func (s *Server) handleMessageSearch(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		ok(w, map[string]any{"messages": []any{}})
		return
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 20)
	msgs, err := s.deps.Chat.SearchAllMessages(r.Context(), user.ID, q, limit)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"messages": msgs})
}

func (s *Server) handleThreadGallery(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 50)
	res, err := s.deps.Chat.Gallery(r.Context(), user.ID, r.PathValue("id"), limit)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"messages": res})
}

func (s *Server) handleThreadExport(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	data, err := s.deps.Chat.Export(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="thread-`+r.PathValue("id")+`.json"`)
	_, _ = w.Write(data)
}

// ---- close / leave / blocks ----

func (s *Server) handleThreadClose(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Chat.CloseThread(r.Context(), user.ID, r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleThreadLeave(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Chat.LeaveThread(r.Context(), user.ID, r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// Thread mute (PRD §5.5.1): PUT silences notifications for this thread,
// DELETE clears it.
func (s *Server) handleThreadMute(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Chat.SetMuted(r.Context(), user.ID, r.PathValue("id"), r.Method == http.MethodPut); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// Pinned conversations (PRD §5.5.1): PUT/DELETE toggles the thread in the
// caller's pin list (max 5).
func (s *Server) handleThreadPinToggle(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Chat.PinThread(r.Context(), user.ID, r.PathValue("id"), r.Method == http.MethodPut); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handlePinnedThreads(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	list, err := s.deps.Chat.PinnedThreads(r.Context(), user.ID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"threads": list})
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if r.Method == http.MethodGet {
		list, err := s.deps.Chat.BlockList(r.Context(), user.ID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"blocked_ids": list})
		return
	}
	blockedID := r.PathValue("userId")
	on := r.Method == http.MethodPost
	if err := s.deps.Chat.Block(r.Context(), user.ID, blockedID, on); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"blocked": on})
}

// ---- quick replies ----

func (s *Server) handleQuickReplies(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.PathValue("id")
	if r.Method == http.MethodGet {
		list, err := s.deps.Chat.QuickReplies(r.Context(), user.ID, businessID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"quick_replies": list})
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Chat.AddQuickReply(r.Context(), user.ID, businessID, in.Text); err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"created": true})
}

func (s *Server) handleQuickReplyDelete(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Chat.RemoveQuickReply(r.Context(), user.ID, r.PathValue("id"), r.PathValue("replyId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// ---- push subscriptions ----

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Endpoint == "" || in.Keys.P256dh == "" || in.Keys.Auth == "" {
		fail(w, domain.ErrValidation.WithField("_", "endpoint and keys are required."))
		return
	}
	// User-supplied URL: only public https endpoints are accepted, otherwise
	// push delivery becomes an SSRF vector against internal services.
	if !security.ValidPushEndpoint(in.Endpoint) {
		fail(w, domain.ErrValidation.WithField("endpoint", "Invalid push endpoint."))
		return
	}
	sub := security.PushSubscription{Endpoint: in.Endpoint}
	sub.Keys.P256dh = in.Keys.P256dh
	sub.Keys.Auth = in.Keys.Auth
	if err := s.deps.Repos.Push.Subscribe(r.Context(), user.ID, sub, r.UserAgent()); err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"subscribed": true})
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Repos.Push.Unsubscribe(r.Context(), user.ID, in.Endpoint); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// ---- helpers ----

func parseID(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

func (s *Server) broadcastThread(ctx context.Context, threadID string, frame Frame) {
	ids, err := s.deps.Repos.Chat.ParticipantIDs(ctx, threadID)
	if err != nil {
		return
	}
	for _, uid := range ids {
		s.deps.Hub.SendToUser(uid, frame)
	}
}

func (s *Server) broadcastMessage(ctx context.Context, messageID int64, frame Frame) {
	m, err := s.deps.Repos.Chat.MessageByID(ctx, messageID)
	if err != nil || m == nil {
		return
	}
	s.broadcastThread(ctx, m.ThreadID, frame)
}

func (s *Server) pushToThread(ctx context.Context, threadID string, msg *domain.ChatMessage) {
	if s.deps.Config.VAPID == nil {
		return
	}
	// Batched fan-out: one query for participants + prefs, one for every
	// subscription (was two queries per participant per message).
	rows, err := s.deps.Repos.Query(ctx, `
		SELECT p.user_id, u.notification_prefs, u.timezone
		FROM chat_participants p JOIN users u ON u.id = p.user_id
		WHERE p.thread_id = $1 AND p.user_id <> $2 AND p.left_at IS NULL`,
		threadID, msg.SenderID)
	if err != nil {
		return
	}
	type target struct {
		id    string
		prefs map[string]any
	}
	var targets []target
	var ids []string
	for rows.Next() {
		var t target
		var tz string
		if err := rows.Scan(&t.id, &t.prefs, &tz); err != nil {
			break
		}
		t.prefs["_tz"] = tz // recipient timezone for quiet-hours evaluation
		targets = append(targets, t)
		ids = append(ids, t.id)
	}
	rows.Close()
	if len(ids) == 0 {
		return
	}

	subsByUser, _ := s.deps.Repos.Push.ByUsers(ctx, ids)

	title := "New message"
	body := "You have a new message"
	if msg.Body != nil && *msg.Body != "" {
		body = *msg.Body
	}
	for _, t := range targets {
		// Quiet hours (PRD §5.7): defer push between 22:00–08:00 in the
		// RECIPIENT's timezone — the old server-local hour pushed at 3 PM
		// (or silenced midday) for anyone ±8h of the server clock.
		if qh, ok := t.prefs["quiet_hours"].(map[string]any); ok {
			if enabled, _ := qh["enabled"].(bool); enabled && quietHoursNow(t.prefs) {
				continue
			}
		}
		for _, sub := range subsByUser[t.id] {
			if err := s.deps.Config.VAPID.Send(sub, s.deps.Config.PublicURL, title, body, map[string]string{"thread_id": threadID}); err != nil {
				if errors.Is(err, security.ErrSubscriptionGone) {
					// Dead subscription: prune so future fan-outs stop paying
					// dead-end HTTP calls (the row previously lived forever).
					_ = s.deps.Repos.Push.DeleteByEndpoint(ctx, sub.Endpoint)
				}
			}
		}
	}
}

var tzCache sync.Map // name → *time.Location

func locByName(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	if l, ok := tzCache.Load(name); ok {
		return l.(*time.Location)
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		l = time.UTC
	}
	tzCache.Store(name, l)
	return l
}

// quietHoursNow evaluates the recipient's quiet-hours window using their
// stored timezone (prefs come joined from users.notification_prefs).
func quietHoursNow(prefs map[string]any) bool {
	tzName := ""
	if tzv, ok := prefs["_tz"].(string); ok {
		tzName = tzv
	}
	hour := time.Now().In(locByName(tzName)).Hour()
	return hour >= 22 || hour < 8
}
