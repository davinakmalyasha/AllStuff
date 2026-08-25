package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/ws"
)

// Notifier — notification creation with channel routing (PRD §5.7):
// in-app rows always, email when the type is on the user's email list,
// plus a real-time WS frame so open tabs update instantly.
//
// Channel matrix lives in users.notification_prefs as:
//
//	{ "channels": { "email": ["verification_result", ...] }, "quiet_hours": {...} }
type Notifier struct {
	repos  *repo.Repos
	cfg    config.Config
	email  email.Sender
	hub    *ws.Hub
	logger *slog.Logger
}

func NewNotifier(repos *repo.Repos, cfg config.Config, sender email.Sender, hub *ws.Hub, logger *slog.Logger) *Notifier {
	return &Notifier{repos: repos, cfg: cfg, email: sender, hub: hub, logger: logger}
}

// CreateDeduped suppresses duplicate notifications for the same
// (user, type, dedupeKey) within `window`. Toggleable events (helpful votes,
// reaction remove/re-add) previously notified — and emailed — on every
// toggle-on, flooding recipients. The dedupe key is stored in the payload so
// the check is a single indexed-ish query.
func (n *Notifier) CreateDeduped(ctx context.Context, userID, ntype, dedupeKey string, window time.Duration, payload map[string]any) {
	if dedupeKey != "" {
		var dup bool
		err := n.repos.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM notifications
				WHERE user_id = $1 AND type = $2 AND created_at > now() - ($3 || ' seconds')::interval
				  AND payload->>'_d' = $4
			)`, userID, ntype, int(window.Seconds()), dedupeKey).Scan(&dup)
		if err == nil && dup {
			return
		}
		if payload == nil {
			payload = map[string]any{}
		}
		payload["_d"] = dedupeKey
	}
	n.Create(ctx, userID, ntype, payload)
}

// Create stores a notification for userID and dispatches it on all enabled
// channels. Best-effort: failures never break the caller's flow.
func (n *Notifier) Create(ctx context.Context, userID, ntype string, payload map[string]any) {
	notif, err := n.repos.Engagement.CreateNotification(ctx, userID, ntype, payload)
	if err != nil {
		n.logger.Warn("notification create", "err", err, "user", userID, "type", ntype)
		return
	}

	// Email channel: only when the user opted the type into email.
	if n.emailOn(ctx, userID, ntype) {
		go n.sendEmail(notif)
	}

	// Real-time frame: unread count refreshed so the badge stays accurate.
	unread, err := n.repos.Engagement.UnreadCount(ctx, userID)
	if err != nil {
		unread = 0
	}
	n.hub.SendToUser(userID, ws.Frame{Type: "notification.new", Payload: map[string]any{
		"notification": notif,
		"unread":       unread,
	}})
}

// CreateForFollowers fans a notification out to every follower of a
// business: one bulk INSERT for the in-app rows, then email/WS dispatch in
// bounded worker goroutines. Returns the number of recipients.
func (n *Notifier) CreateForFollowers(ctx context.Context, businessID, ntype string, payload map[string]any) (int64, error) {
	nCreated, err := n.repos.Engagement.CreateNotificationsForFollowers(ctx, businessID, ntype, payload)
	if err != nil {
		return 0, err
	}
	if nCreated == 0 {
		return 0, nil
	}
	followers, err := n.repos.Community.FollowerIDs(ctx, businessID)
	if err != nil {
		return nCreated, nil // rows are in; channel dispatch is best-effort
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, fid := range followers {
		wg.Add(1)
		go func(userID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			notif, err := n.repos.Engagement.GetLatestUnread(ctx, userID, ntype)
			if err == nil && notif != nil {
				if n.emailOn(ctx, userID, ntype) {
					go n.sendEmail(notif)
				}
				unread, err := n.repos.Engagement.UnreadCount(ctx, userID)
				if err != nil {
					unread = 0
				}
				n.hub.SendToUser(userID, ws.Frame{Type: "notification.new", Payload: map[string]any{
					"notification": notif,
					"unread":       unread,
				}})
			}
		}(fid)
	}
	wg.Wait()
	return nCreated, nil
}

func (n *Notifier) emailOn(ctx context.Context, userID, ntype string) bool {
	var prefs map[string]any
	if err := n.repos.QueryRow(ctx,
		`SELECT notification_prefs FROM users WHERE id = $1`, userID).Scan(&prefs); err != nil || prefs == nil {
		return false
	}
	channels, ok := prefs["channels"].(map[string]any)
	if !ok {
		return false
	}
	switch v := channels["email"].(type) {
	case string:
		// Legacy values: immediate/digest mean "all email-worthy types".
		return v == "immediate" || v == "digest"
	case []any:
		for _, t := range v {
			if s, ok := t.(string); ok && s == ntype {
				return true
			}
		}
	}
	return false
}

func (n *Notifier) sendEmail(notif *domain.Notification) {
	title, body := describe(notif)
	if title == "" {
		return
	}
	var to string
	if err := n.repos.QueryRow(context.Background(),
		`SELECT email FROM users WHERE id = $1`, notif.UserID).Scan(&to); err != nil || to == "" {
		return
	}
	bodyHTML := fmt.Sprintf(`<p>%s</p><p style="color:#999;font-size:12px">Manage preferences: %s/me/security</p>`, body, n.cfg.PublicURL)
	// Detached goroutine: the originating request ctx may already be done.
	_ = n.email.Send(context.Background(), to, title, email.WrapHTML(n.cfg.PublicURL, title, bodyHTML))
}

// describe renders a human-readable title/body for email channel. The user_id
// placeholder addresses are never delivered outside dev; production senders
// look the address up by ID.
func describe(n *domain.Notification) (string, string) {
	switch n.Type {
	case "message_received":
		return "New message on BizVerse", "You have a new message in a conversation."
	case "reaction_added":
		return "Someone reacted to your message", "A message you sent received a reaction."
	case "helpful_vote":
		return "Your review was marked helpful", "A member found your review helpful."
	case "product_review":
		return "New product review", "Someone reviewed one of your products."
	case "review_posted":
		return "New review on your business", "Someone reviewed your business."
	case "review_replied":
		return "The owner replied to your review", "Your review got a reply."
	case "comment_on_business":
		return "New comment on your business", "Someone commented on your business."
	case "comment_mention":
		return "You were mentioned", "You were mentioned in a comment."
	case "question_asked":
		return "New question about your business", "Someone asked a question about your business."
	case "question_answered":
		return "Your question was answered", "Your question got an answer."
	case "business_update":
		return "New announcement", "A business you follow posted an update."
	case "verification_result":
		return "Verification decision", "Your business verification has a new status."
	case "doc_re_request":
		return "Verification document requested", "The team needs an additional document."
	case "business_suspended":
		return "Your business was suspended", "Your business is suspended pending review."
	case "business_restored":
		return "Your business was restored", "Your business is live again."
	case "moderation_warning":
		return "Moderation notice", "You received a moderation notice."
	case "appeal_result":
		return "Appeal decision", "Your account appeal was decided."
	case "category_new_business":
		return "New business in a category you follow", "A new verified business was added."
	case "back_in_stock":
		return "Back in stock", "An item you're watching is available again."
	case "claim_result":
		return "Claim decision", "Your business claim was reviewed."
	case "trend_anomaly":
		return "Trend anomaly", "An engagement spike was detected and flagged."
	default:
		return "", ""
	}
}

// SupportedTypes enumerates every notification type for docs/tests.
var SupportedTypes = []string{
	"message_received", "reaction_added", "helpful_vote", "product_review",
	"review_posted", "review_replied", "comment_on_business", "comment_mention",
	"question_asked", "question_answered", "business_update", "verification_result",
	"doc_re_request", "business_suspended", "business_restored", "moderation_warning",
	"appeal_result", "category_new_business", "back_in_stock", "claim_result",
	"trend_anomaly",
}
