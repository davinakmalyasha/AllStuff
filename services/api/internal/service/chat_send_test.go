//go:build !race

package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/ratelimit"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
	"bizverse/api/internal/ws"
)

// chat.Send had no coverage at all in the last measured profile - 114
// statements, the largest uncovered function in the service package. It is also
// the most authorization-sensitive: it decides whether a user may write into a
// thread, what they may attach, and who is notified.

func newTestChat(t *testing.T) (*Chat, *testutil.H) {
	t.Helper()
	h := testutil.New(t)

	// Mandatory, and the reason is not obvious: Send opens its own transaction
	// through repos.Pool(). The default harness binds Repos to a test
	// transaction, so Pool() returns nil and Begin panics. Rebinding to the pool
	// BEFORE any fixture keeps the fixtures and the code under test on one
	// connection - the opposite order leaves the fixtures invisible.
	//
	// The rollback isolation goes with it, but the whole per-test database is
	// dropped at cleanup, so nothing outlives the test either way.
	h.UsePool()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test"})
	// The notify loop dereferences c.notifier without a nil guard, so it has to
	// be real. Its email channel is off unless a user opts in, so nothing sends.
	notifier := NewNotifier(h.Repos, cfg, sender, ws.NewHub(logger, nil), logger)
	return NewChat(h.Repos, notifier), h
}

// newTestChatNoLimit is the harness for tests that send more than once to the
// same thread. Send guards the limiter with `if c.limiter != nil`, so clearing it
// is the seam - the alternative is one thread per send, which changes what the
// test is about. TestChatSendRateLimitsToOnePerSecond keeps it.
func newTestChatNoLimit(t *testing.T) (*Chat, *testutil.H) {
	t.Helper()
	c, h := newTestChat(t)
	c.limiter = nil
	return c, h
}

type participant struct {
	userID string
	role   string
	left   bool
	muted  bool
}

// makeThread inserts a thread and its participants. After UsePool the raw-SQL
// helpers must be the pool ones (ExecPool), or the rows land in a transaction the
// pool-bound code cannot see.
func makeThread(t *testing.T, h *testutil.H, typ string, businessID *string, status string, ps ...participant) string {
	t.Helper()
	if typ == "" {
		typ = "direct"
	}
	if status == "" {
		status = "open"
	}
	id := util.NewUUID()
	h.ExecPool(t, `INSERT INTO chat_threads (id, type, business_id, status) VALUES ($1,$2,$3,$4)`,
		id, typ, businessID, status)
	for _, p := range ps {
		role := p.role
		if role == "" {
			role = "user"
		}
		var left, muted any
		if p.left {
			left = time.Now().Add(-time.Hour)
		}
		if p.muted {
			muted = time.Now().Add(time.Hour)
		}
		h.ExecPool(t, `INSERT INTO chat_participants (id, thread_id, user_id, role, left_at, muted_until) VALUES ($1,$2,$3,$4,$5,$6)`,
			util.NewUUID(), id, p.userID, role, left, muted)
	}
	return id
}

func body(s string) *string { return &s }

func sendText(t *testing.T, c *Chat, ctx context.Context, userID, threadID, text string) (*domain.ChatMessage, error) {
	t.Helper()
	return c.Send(ctx, userID, threadID, SendInput{Type: "text", Body: body(text)})
}

// ---- authorization ----

func TestChatSendRequiresParticipation(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	member := testutil.User(t, h)
	outsider := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: member.ID}, participant{userID: outsider.ID})

	t.Run("an unknown thread is not found", func(t *testing.T) {
		if _, err := sendText(t, c, ctx, member.ID, util.NewUUID(), "hi"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("a non-participant is forbidden", func(t *testing.T) {
		stranger := testutil.User(t, h)
		if _, err := sendText(t, c, ctx, stranger.ID, thread, "hi"); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
		var n int
		if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE thread_id=$1`, thread).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%d message(s) were written by a non-participant", n)
		}
	})

	t.Run("a participant writes", func(t *testing.T) {
		msg, err := sendText(t, c, ctx, member.ID, thread, "hello")
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if msg.ThreadID != thread || msg.SenderID != member.ID {
			t.Errorf("message = %#v", msg)
		}
		var n int
		if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE thread_id=$1`, thread).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%d rows, want 1", n)
		}
	})
}

func TestChatSendDeduplicatesByClientMsgID(t *testing.T) {
	c, h := newTestChatNoLimit(t)
	ctx := fixtureCtx(t)
	u := testutil.User(t, h)
	peer := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: u.ID}, participant{userID: peer.ID})

	const clientID = "client-msg-1"
	first, err := c.Send(ctx, u.ID, thread, SendInput{Type: "text", Body: body("one"), ClientMsgID: clientID})
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	// At-least-once delivery: the same client id must return the same message
	// rather than inserting a duplicate.
	second, err := c.Send(ctx, u.ID, thread, SendInput{Type: "text", Body: body("one"), ClientMsgID: clientID})
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("dedupe returned a different message: %d vs %d", second.ID, first.ID)
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE thread_id=$1`, thread).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows after two sends with one client id, want 1", n)
	}

	// The row count alone does not prove the early return: the table has
	// `ON CONFLICT (thread_id, client_msg_id)` with a no-op upsert, so the
	// database deduplicates the insert even if the service check is removed.
	// What the early return actually prevents is the rest of the function running
	// again - including the notify fan-out, which would ping the peer twice for
	// one message every time a client retried.
	var notified int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id=$1 AND type='message_received'`, peer.ID).Scan(&notified); err != nil {
		t.Fatal(err)
	}
	if notified != 1 {
		t.Errorf("the peer was notified %d times for one message (and one retry), want 1", notified)
	}
}

func TestChatSendValidatesText(t *testing.T) {
	c, h := newTestChatNoLimit(t)
	ctx := fixtureCtx(t)
	u := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: u.ID})

	rejected := []struct {
		name string
		in   SendInput
	}{
		{"empty", SendInput{Type: "text", Body: body("")}},
		{"whitespace only", SendInput{Type: "text", Body: body("   \n\t ")}},
		{"nil body", SendInput{Type: "text"}},
		{"4001 runes", SendInput{Type: "text", Body: body(strings.Repeat("a", 4001))}},
		{"4001 multi-byte runes", SendInput{Type: "text", Body: body(strings.Repeat("日", 4001))}},
	}
	for _, tc := range rejected {
		if _, err := c.Send(ctx, u.ID, thread, tc.in); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", tc.name, err)
		}
	}

	// The limit is on runes, not bytes: 4000 multi-byte characters are 12000
	// bytes and must still be accepted (and >= 4000 anyway).
	if _, err := c.Send(ctx, u.ID, thread, SendInput{Type: "text", Body: body(strings.Repeat("a", 4000))}); err != nil {
		t.Errorf("4000 runes rejected: %v", err)
	}
}

func TestChatSendRefusesMessagingWhenBlocked(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	a := testutil.User(t, h)
	b := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: a.ID}, participant{userID: b.ID})

	h.ExecPool(t, `INSERT INTO blocks (id, blocker_id, blocked_id) VALUES ($1,$2,$3)`, util.NewUUID(), a.ID, b.ID)

	// A block is one-way in the table but two-way in effect: whoever is blocked
	// cannot message, and neither can the blocker.
	for _, from := range []string{a.ID, b.ID} {
		if _, err := sendText(t, c, ctx, from, thread, "hi"); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("send from %s with a block in place: err = %v, want ErrValidation", from, err)
		}
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE thread_id=$1`, thread).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d message(s) were written while blocked", n)
	}
}

// The comment on this check states the stakes: without it a participant could
// plant someone else's private media UUID into their own message and then pass
// the chat-media membership gate to download it.
func TestChatSendValidatesAttachments(t *testing.T) {
	c, h := newTestChatNoLimit(t)
	ctx := fixtureCtx(t)
	sender := testutil.User(t, h)
	other := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: sender.ID}, participant{userID: other.ID})

	ownImage := insertMedia(t, h, sender.ID, domain.MediaChatImage, "image/png")
	ownFile := insertMedia(t, h, sender.ID, domain.MediaChatFile, "application/pdf")
	foreignImage := insertMedia(t, h, other.ID, domain.MediaChatImage, "image/png")

	rejected := []struct {
		name string
		in   SendInput
	}{
		{"an image with no attachment", SendInput{Type: "image"}},
		{"a file with no attachment", SendInput{Type: "file"}},
		{"an audio message with no attachment", SendInput{Type: "audio"}},
		{"a video message with no attachment", SendInput{Type: "video"}},
		{"text carrying an attachment", SendInput{Type: "text", Body: body("hi"), MediaID: &ownImage}},
		{"another user's media", SendInput{Type: "image", MediaID: &foreignImage}},
		{"an image pointing at a file", SendInput{Type: "image", MediaID: &ownFile}},
		{"a file pointing at an image", SendInput{Type: "file", MediaID: &ownImage}},
		{"a media id that does not exist", SendInput{Type: "image", MediaID: ptr(util.NewUUID())}},
	}
	for _, tc := range rejected {
		if _, err := c.Send(ctx, sender.ID, thread, tc.in); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", tc.name, err)
		}
	}

	// The correct pair is accepted.
	msg, err := c.Send(ctx, sender.ID, thread, SendInput{Type: "image", MediaID: &ownImage})
	if err != nil {
		t.Fatalf("a correctly owned image was rejected: %v", err)
	}
	if msg.MediaID == nil || *msg.MediaID != ownImage {
		t.Errorf("MediaID = %v", msg.MediaID)
	}
}

func insertMedia(t *testing.T, h *testutil.H, uploaderID string, kind domain.MediaKind, mime string) string {
	t.Helper()
	id := util.NewUUID()
	h.ExecPool(t, `INSERT INTO media (id, uploader_id, kind, original_name, mime, size, path) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		id, uploaderID, string(kind), "f.bin", mime, 10, string(kind)+"/"+id+".bin")
	return id
}

// An owner's reply carries a business badge, which is why the attribution is
// resolved through CanManageBusiness rather than the owner_id column: a co-owner
// running the inbox used to be labelled as speaking for themselves.
func TestChatSendAttributesTheSenderRole(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	owner := testutil.User(t, h)
	customer := testutil.User(t, h)
	admin := testutil.Admin(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Inbox Co", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})

	bizID := biz.ID
	thread := makeThread(t, h, "business", &bizID, "",
		participant{userID: owner.ID, role: "owner"},
		participant{userID: customer.ID},
		participant{userID: admin.ID},
	)

	if msg, err := sendText(t, c, ctx, owner.ID, thread, "owner here"); err != nil {
		t.Fatalf("owner send: %v", err)
	} else if msg.SenderRole != "owner" {
		t.Errorf("owner role = %q, want owner", msg.SenderRole)
	}

	if msg, err := sendText(t, c, ctx, customer.ID, thread, "customer here"); err != nil {
		t.Fatalf("customer send: %v", err)
	} else if msg.SenderRole != "user" {
		t.Errorf("customer role = %q, want user", msg.SenderRole)
	}

	if msg, err := sendText(t, c, ctx, admin.ID, thread, "admin here"); err != nil {
		t.Fatalf("admin send: %v", err)
	} else if msg.SenderRole != "admin" {
		t.Errorf("admin role = %q, want admin", msg.SenderRole)
	}
}

func TestChatSendReopensAClosedBusinessThread(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	owner := testutil.User(t, h)
	customer := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Closed Co", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})

	bizID := biz.ID
	thread := makeThread(t, h, "business", &bizID, "closed",
		participant{userID: owner.ID, role: "owner"},
		participant{userID: customer.ID},
	)

	// The owner may not reopen a conversation they closed.
	if _, err := sendText(t, c, ctx, owner.ID, thread, "hello again"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("owner send into a closed thread: err = %v, want ErrValidation", err)
	}

	// A customer's reply reopens it, and that is recorded as a system message.
	if _, err := sendText(t, c, ctx, customer.ID, thread, "one more question"); err != nil {
		t.Fatalf("customer send: %v", err)
	}
	var status string
	if err := h.Pool.QueryRow(ctx, `SELECT status FROM chat_threads WHERE id=$1`, thread).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "open" {
		t.Errorf("thread status = %q, want open", status)
	}
	var systems int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE thread_id=$1 AND type='system'`, thread).Scan(&systems); err != nil {
		t.Fatal(err)
	}
	if systems == 0 {
		t.Error("no system message recorded for the reopen")
	}
}

func TestChatSendValidatesTheReplyTarget(t *testing.T) {
	c, h := newTestChatNoLimit(t)
	ctx := fixtureCtx(t)
	u := testutil.User(t, h)
	other := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: u.ID})
	otherThread := makeThread(t, h, "direct", nil, "", participant{userID: u.ID}, participant{userID: other.ID})

	first, err := sendText(t, c, ctx, u.ID, thread, "first")
	if err != nil {
		t.Fatalf("first send: %v", err)
	}

	// Replying to a message from a different thread must be refused: the target
	// would otherwise leak across threads on render.
	if _, err := c.Send(ctx, u.ID, otherThread, SendInput{Type: "text", Body: body("cross"), ReplyToID: &first.ID}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("cross-thread reply: err = %v, want ErrValidation", err)
	}

	if msg, err := c.Send(ctx, u.ID, thread, SendInput{Type: "text", Body: body("same"), ReplyToID: &first.ID}); err != nil {
		t.Fatalf("same-thread reply: %v", err)
	} else if msg.ReplyToID == nil || *msg.ReplyToID != first.ID {
		t.Errorf("ReplyToID = %v, want %d", msg.ReplyToID, first.ID)
	}
}

// The 1/sec tier is real time, so a stalled runner could let the second send
// through. The assertion is therefore conditional on the two calls having
// actually landed inside the window.
func TestChatSendRateLimitsToOnePerSecond(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	u := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: u.ID})

	start := time.Now()
	_, firstErr := sendText(t, c, ctx, u.ID, thread, "one")
	_, secondErr := sendText(t, c, ctx, u.ID, thread, "two")
	elapsed := time.Since(start)

	if firstErr != nil {
		t.Fatalf("the first send was refused: %v", firstErr)
	}
	if elapsed >= time.Second {
		t.Skipf("the two sends took %v, outside the 1s window; the limiter could not be observed", elapsed)
	}
	if !errors.Is(secondErr, domain.ErrRateLimited) {
		t.Errorf("second send within %v: err = %v, want ErrRateLimited", elapsed, secondErr)
	}
	// The hour tier sits behind this one and cannot be reached without a clock or
	// limiter seam, so it is not asserted here.
}

func TestChatSendRefusesIdenticalBurstsAcrossThreads(t *testing.T) {
	c, h := newTestChatNoLimit(t)
	ctx := fixtureCtx(t)
	u := testutil.User(t, h)

	// Five identical messages to five different threads. Different threads mean
	// different rate-limit keys, so the burst gate is what has to catch this.
	for i := 0; i < 5; i++ {
		th := makeThread(t, h, "direct", nil, "", participant{userID: u.ID})
		if _, err := sendText(t, c, ctx, u.ID, th, "same message"); err != nil {
			t.Fatalf("send %d: %v", i+1, err)
		}
	}

	sixth := makeThread(t, h, "direct", nil, "", participant{userID: u.ID})
	if _, err := sendText(t, c, ctx, u.ID, sixth, "same message"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("the sixth identical message: err = %v, want ErrValidation", err)
	}
	// A different message is fine.
	if _, err := sendText(t, c, ctx, u.ID, sixth, "something else"); err != nil {
		t.Errorf("a distinct message was refused: %v", err)
	}
}

func TestChatSendCoolsDownAfterBlockedWords(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	u := testutil.User(t, h)

	// Seeded before the first send: the list is cached per Chat instance for 60s.
	h.ExecPool(t, `INSERT INTO banned_words (id, word) VALUES ($1,$2)`, util.NewUUID(), "forbidden")

	// Three strikes, each in its own thread so the rate limiter does not fire
	// first.
	for i := 0; i < 3; i++ {
		th := makeThread(t, h, "direct", nil, "", participant{userID: u.ID})
		if _, err := sendText(t, c, ctx, u.ID, th, "this is forbidden content"); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("strike %d: err = %v, want ErrValidation", i+1, err)
		}
	}

	fresh := makeThread(t, h, "direct", nil, "", participant{userID: u.ID})
	_, err := sendText(t, c, ctx, u.ID, fresh, "a perfectly ordinary message")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("after three strikes: err = %v, want the cooldown", err)
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %T, want a *domain.Error", err)
	}
	if !strings.Contains(derr.Fields["_"], "Too many blocked attempts") {
		t.Errorf("fields = %v, want the cooldown message under _", derr.Fields)
	}
}

// The notify fan-out is observed through the notifications table rather than the
// hub: Notifier.Create writes the row before pushing a frame, so the table says
// exactly who was notified, and the transport stays a separate concern.
func TestChatSendNotifiesTheRightParticipants(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	sender := testutil.User(t, h)
	peer := testutil.User(t, h)
	muted := testutil.User(t, h)
	left := testutil.User(t, h)

	thread := makeThread(t, h, "direct", nil, "",
		participant{userID: sender.ID},
		participant{userID: peer.ID},
		participant{userID: muted.ID, muted: true},
		participant{userID: left.ID, left: true},
	)

	if _, err := sendText(t, c, ctx, sender.ID, thread, "hello everyone"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	notified := func(userID string) int {
		t.Helper()
		var n int
		if err := h.Pool.QueryRow(ctx,
			`SELECT count(*) FROM notifications WHERE user_id=$1 AND type='message_received'`, userID).Scan(&n); err != nil {
			t.Fatalf("counting notifications: %v", err)
		}
		return n
	}

	if got := notified(peer.ID); got != 1 {
		t.Errorf("the peer got %d notifications, want 1", got)
	}
	if got := notified(sender.ID); got != 0 {
		t.Errorf("the sender notified themselves %d time(s)", got)
	}
	if got := notified(muted.ID); got != 0 {
		t.Errorf("a muted participant got %d notifications, want 0", got)
	}
	// ParticipantIDs filters left_at, so a participant who left is not notified.
	if got := notified(left.ID); got != 0 {
		t.Errorf("a participant who left got %d notifications, want 0", got)
	}
}

// hourLimiter allows the per-second tier and refuses the per-hour one, which is
// the only way to reach the hour branch: in production the 1/sec tier rejects
// first, so the hour limit is unreachable without waiting an hour.
type hourLimiter struct {
	mu    sync.Mutex
	seen  []string
	limit int
}

func (h *hourLimiter) Allow(key string, limit int, window time.Duration) (int, time.Duration, bool) {
	h.mu.Lock()
	h.seen = append(h.seen, key)
	h.mu.Unlock()
	if strings.Contains(key, "chat:hour:") {
		h.limit = limit
		return 0, 42 * time.Minute, false
	}
	return limit - 1, 0, true
}

func TestChatSendReportsTheHourlyLimit(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	u := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: u.ID})

	fake := &hourLimiter{}
	var rl ratelimit.RateLimiter = fake
	c.limiter = &rl

	_, err := sendText(t, c, ctx, u.ID, thread, "one")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %T, want *domain.Error", err)
	}
	// The message tells the user how long to wait, computed from the retry the
	// limiter reported rather than a fixed string.
	if !strings.Contains(derr.Message, "Message limit reached") {
		t.Errorf("message = %q", derr.Message)
	}
	if fake.limit != 60 {
		t.Errorf("the hour tier asked for a limit of %d, want 60", fake.limit)
	}

	// Nothing was written: the refusal happens before the insert.
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE thread_id=$1`, thread).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d message(s) were written despite the hour limit", n)
	}
}
