//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
)

// The chat ACTION routes - search, gallery, export, forward, receipts, typing,
// mute, pin, leave, close, blocks, quick replies - were never executed by a test.
// TestChatFlow covers the conversation itself, so everything below is about the
// edges: who is refused, what a refused caller can still learn, and whether the
// action actually changed something.

func createThread(t *testing.T, td *testDeps, hdl http.Handler, u *domain.User, body map[string]any) string {
	t.Helper()
	w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads", body, u)
	if w.Code != http.StatusCreated {
		t.Fatalf("create thread: status = %d: %s", w.Code, w.Body.String())
	}
	var env threadEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Thread.ID == "" {
		t.Fatal("create thread: empty id")
	}
	return env.Thread.ID
}

func TestChatActionRoutes(t *testing.T) {
	// Pool-bound because Chat.Send opens its own transaction.
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	alice := testutil.User(t, h)
	bob := testutil.User(t, h)
	mallory := testutil.User(t, h)

	threadID := createThread(t, td, hdl, alice, map[string]any{"user_id": bob.ID})
	payload := "zqxpayload"
	code, msgID := chatSend(t, td, hdl, alice, threadID, payload)
	if code != http.StatusCreated {
		t.Fatalf("send: status = %d", code)
	}
	msgPath := "/api/v1/messages/" + strconv.FormatInt(msgID, 10)

	// ---- read receipts ----
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/read", map[string]any{"last_read_message_id": msgID}, bob); w.Code != http.StatusNoContent {
		t.Errorf("read receipt: status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/read", map[string]any{"last_read_message_id": msgID}, mallory); w.Code != http.StatusForbidden {
		t.Errorf("read receipt by an outsider: status = %d, want 403", w.Code)
	}

	// ---- typing ----
	// The membership check comes first on purpose: without it any signed-in user
	// could broadcast typing into another conversation and observe activity.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/typing", nil, alice); w.Code != http.StatusNoContent {
		t.Errorf("typing: status = %d, want 204", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/typing", nil, mallory); w.Code != http.StatusForbidden {
		t.Errorf("typing by an outsider: status = %d, want 403", w.Code)
	}

	// ---- search inside a thread ----
	w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"/search?q="+payload, nil, bob)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), payload) {
		t.Errorf("thread search: status = %d, body = %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"/search?q="+payload, nil, mallory); w.Code != http.StatusForbidden {
		t.Errorf("thread search by an outsider: status = %d, want 403", w.Code)
	}

	// ---- global search ----
	// The dangerous failure here is a search that returns messages from threads
	// the caller is not in, so the outsider case asserts on the BODY, not a status.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/messages/search?q="+payload, nil, bob); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), payload) {
		t.Errorf("global search as a participant: status = %d, body = %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/messages/search?q="+payload, nil, mallory); strings.Contains(w.Body.String(), payload) {
		t.Errorf("global search leaked another user's message: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/messages/search?q=%20", nil, bob); !strings.Contains(w.Body.String(), `"messages":[]`) {
		t.Errorf("empty search: status = %d, body = %s", w.Code, w.Body.String())
	}

	// ---- gallery ----
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"/media", nil, bob); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"messages"`) {
		t.Errorf("gallery: status = %d, body = %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"/media", nil, mallory); w.Code != http.StatusForbidden {
		t.Errorf("gallery by an outsider: status = %d, want 403", w.Code)
	}

	// ---- export ----
	// Delete the message for everyone first: an export that kept it would survive
	// the deletion the participant was promised.
	if w := dash(t, td, hdl, http.MethodDelete, msgPath+"?scope=everyone", nil, alice); w.Code != http.StatusOK {
		t.Fatalf("delete for everyone: status = %d: %s", w.Code, w.Body.String())
	}
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"/export", nil, bob)
	if w.Code != http.StatusOK {
		t.Fatalf("export: status = %d: %s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, threadID) {
		t.Errorf("export Content-Disposition = %q", cd)
	}
	var dump struct {
		ThreadID string `json:"thread_id"`
		Messages []struct {
			Body string `json:"body"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dump); err != nil {
		t.Fatalf("export is not JSON: %v", err)
	}
	if dump.ThreadID != threadID {
		t.Errorf("export thread_id = %q, want %q", dump.ThreadID, threadID)
	}
	for _, m := range dump.Messages {
		if m.Body == payload {
			t.Error("export still contains a message deleted for everyone")
		}
	}
	// A deleted message is invisible to BOTH searches. They are separate queries,
	// and the global one is the easier of the two to forget.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"/search?q="+payload, nil, bob); strings.Contains(w.Body.String(), payload) {
		t.Errorf("thread search still finds a message deleted for everyone: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/messages/search?q="+payload, nil, bob); strings.Contains(w.Body.String(), payload) {
		t.Errorf("global search still finds a message deleted for everyone: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"/export", nil, mallory); w.Code != http.StatusForbidden {
		t.Errorf("export by an outsider: status = %d, want 403", w.Code)
	}

	// ---- search respects a departure and a per-user hide ----
	// Two fresh messages, because the one above is gone for everyone and would
	// prove nothing here. Send allows one message per second per (user, thread).
	leavePayload := "zqxleave"
	time.Sleep(1100 * time.Millisecond)
	if code, _ := chatSend(t, td, hdl, alice, threadID, leavePayload); code != http.StatusCreated {
		t.Fatalf("send before leaving: status = %d", code)
	}
	hidePayload := "zqxhide"
	time.Sleep(1100 * time.Millisecond)
	code, hideID := chatSend(t, td, hdl, alice, threadID, hidePayload)
	if code != http.StatusCreated {
		t.Fatalf("send before hiding: status = %d", code)
	}
	// The author deletes it for themselves only. It must vanish from THEIR
	// searches while Bob still finds it - a hide that hides for everyone is a
	// deletion, and a hide that hides for nobody is a no-op.
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/messages/"+strconv.FormatInt(hideID, 10)+"?scope=me", nil, alice); w.Code != http.StatusNoContent {
		t.Fatalf("delete for me: status = %d", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/messages/search?q="+hidePayload, nil, alice); strings.Contains(w.Body.String(), hidePayload) {
		t.Errorf("the author's global search still finds a message they hid: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/messages/search?q="+hidePayload, nil, bob); !strings.Contains(w.Body.String(), hidePayload) {
		t.Errorf("another participant lost a message the author hid for themselves: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/messages/search?q="+leavePayload, nil, bob); !strings.Contains(w.Body.String(), leavePayload) {
		t.Errorf("global search does not find a message in the caller's thread: %s", w.Body.String())
	}

	// ---- mute ----
	if w := dash(t, td, hdl, http.MethodPut, "/api/v1/threads/"+threadID+"/mute", nil, bob); w.Code != http.StatusNoContent {
		t.Errorf("mute: status = %d, want 204", w.Code)
	}
	var muted bool
	if err := h.Pool.QueryRow(t.Context(), `SELECT muted_until IS NOT NULL FROM chat_participants WHERE thread_id=$1 AND user_id=$2`, threadID, bob.ID).Scan(&muted); err != nil {
		t.Fatalf("read mute state: %v", err)
	}
	if !muted {
		t.Error("mute returned 204 but did not set muted_until")
	}
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/threads/"+threadID+"/mute", nil, bob); w.Code != http.StatusNoContent {
		t.Errorf("unmute: status = %d, want 204", w.Code)
	}
	if err := h.Pool.QueryRow(t.Context(), `SELECT muted_until IS NOT NULL FROM chat_participants WHERE thread_id=$1 AND user_id=$2`, threadID, bob.ID).Scan(&muted); err != nil {
		t.Fatalf("read mute state after unmute: %v", err)
	}
	if muted {
		t.Error("unmute left muted_until set")
	}
	if w := dash(t, td, hdl, http.MethodPut, "/api/v1/threads/"+threadID+"/mute", nil, mallory); w.Code != http.StatusForbidden {
		t.Errorf("mute by an outsider: status = %d, want 403", w.Code)
	}

	// ---- pin ----
	if w := dash(t, td, hdl, http.MethodPut, "/api/v1/threads/"+threadID+"/pinned-thread", nil, alice); w.Code != http.StatusNoContent {
		t.Errorf("pin: status = %d, want 204", w.Code)
	}
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/me/pinned-threads", nil, alice)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), threadID) {
		t.Errorf("pinned list after pin: status = %d, body = %s", w.Code, w.Body.String())
	}
	// The pin list is per participant: Bob never pinned it.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me/pinned-threads", nil, bob); strings.Contains(w.Body.String(), threadID) {
		t.Errorf("one participant's pin showed up in another's list: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/threads/"+threadID+"/pinned-thread", nil, alice); w.Code != http.StatusNoContent {
		t.Errorf("unpin: status = %d, want 204", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me/pinned-threads", nil, alice); strings.Contains(w.Body.String(), threadID) {
		t.Errorf("unpin left the thread pinned: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPut, "/api/v1/threads/"+threadID+"/pinned-thread", nil, mallory); w.Code != http.StatusForbidden {
		t.Errorf("pin by an outsider: status = %d, want 403", w.Code)
	}

	// ---- close ----
	// A direct thread has no business behind it, so there is nobody entitled to
	// close it - the refusal is the rule, not a missing feature.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/close", nil, alice); w.Code != http.StatusForbidden {
		t.Errorf("close on a direct thread: status = %d, want 403", w.Code)
	}

	// ---- leave ----
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/leave", nil, mallory); w.Code != http.StatusForbidden {
		t.Errorf("leave by an outsider: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/leave", nil, bob); w.Code != http.StatusNoContent {
		t.Errorf("leave: status = %d, want 204", w.Code)
	}
	// Having left, the thread is gone for him - including its history, and
	// including global search. A left_at row that search forgets to read is a
	// way to keep reading a conversation you walked out of.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID, nil, bob); w.Code != http.StatusForbidden {
		t.Errorf("read after leaving: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/messages/search?q="+leavePayload, nil, bob); strings.Contains(w.Body.String(), leavePayload) {
		t.Errorf("global search still finds a thread the caller left: %s", w.Body.String())
	}
}

func TestChatCloseOnABusinessThread(t *testing.T) {
	// Closing is a manager action on a BUSINESS thread. It used to be reachable
	// by any participant of any thread; the direct-thread case above pins the
	// refusal, and this pins the one caller who may.
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	customer := testutil.User(t, h)
	b := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner, Status: domain.BusinessVerified})

	threadID := createThread(t, td, hdl, customer, map[string]any{"business_id": b.ID})
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/close", nil, customer); w.Code != http.StatusForbidden {
		t.Errorf("customer close: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/close", nil, owner); w.Code != http.StatusNoContent {
		t.Fatalf("owner close: status = %d: %s", w.Code, w.Body.String())
	}
	var status string
	if err := h.Pool.QueryRow(t.Context(), `SELECT coalesce(status,'') FROM chat_threads WHERE id=$1`, threadID).Scan(&status); err != nil {
		t.Fatalf("read thread status: %v", err)
	}
	if status != "closed" {
		t.Errorf("thread status = %q, want closed", status)
	}
}

func TestChatForward(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	alice := testutil.User(t, h)
	bob := testutil.User(t, h)
	mallory := testutil.User(t, h)

	source := createThread(t, td, hdl, alice, map[string]any{"user_id": bob.ID})
	dest := createThread(t, td, hdl, alice, map[string]any{"user_id": mallory.ID})
	payload := "forwardpayload"
	code, msgID := chatSend(t, td, hdl, alice, source, payload)
	if code != http.StatusCreated {
		t.Fatalf("send: status = %d", code)
	}
	msgPath := "/api/v1/messages/" + strconv.FormatInt(msgID, 10) + "/forward"

	// Missing destination is a validation error, not a crash.
	if w := dash(t, td, hdl, http.MethodPost, msgPath, map[string]any{}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("forward without a thread: status = %d, want 400", w.Code)
	}

	// The forwarder must be in BOTH threads. Bob is in the source only; Mallory
	// is in the destination only. Either half missing must be refused, or guessed
	// message ids become a way to read other conversations.
	if w := dash(t, td, hdl, http.MethodPost, msgPath, map[string]any{"thread_id": dest}, bob); w.Code != http.StatusForbidden {
		t.Errorf("forward into a thread the caller is not in: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, msgPath, map[string]any{"thread_id": dest}, mallory); w.Code != http.StatusForbidden {
		t.Errorf("forward of a message from a thread the caller is not in: status = %d, want 403", w.Code)
	}

	// The legitimate forward copies the body and records where it came from.
	w := dash(t, td, hdl, http.MethodPost, msgPath, map[string]any{"thread_id": dest}, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("forward: status = %d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Message struct {
			Body            string `json:"body"`
			ForwardedFromID *int64 `json:"forwarded_from_message_id"`
		} `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Message.Body != payload {
		t.Errorf("forwarded body = %q, want %q", env.Message.Body, payload)
	}
	if env.Message.ForwardedFromID == nil || *env.Message.ForwardedFromID != msgID {
		t.Errorf("forwarded_from_id = %v, want %d", env.Message.ForwardedFromID, msgID)
	}

	// A message deleted for everyone is not forwardable: forwarding would be a
	// copy that survives the deletion.
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/messages/"+strconv.FormatInt(msgID, 10)+"?scope=everyone", nil, alice); w.Code != http.StatusOK {
		t.Fatalf("delete source: status = %d", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, msgPath, map[string]any{"thread_id": dest}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("forward of a deleted message: status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestChatBlocksAndQuickReplies(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	other := testutil.User(t, h)
	b := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner, Status: domain.BusinessVerified})

	// ---- blocks ----
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/blocks/"+other.ID, nil, owner); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"blocked":true`) {
		t.Fatalf("block: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/blocks", nil, owner); !strings.Contains(w.Body.String(), other.ID) {
		t.Errorf("block list does not contain the blocked user: %s", w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/blocks/"+owner.ID, nil, owner); w.Code != http.StatusBadRequest {
		t.Errorf("blocking yourself: status = %d, want 400", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/blocks/"+other.ID, nil, owner); w.Code != http.StatusOK {
		t.Errorf("unblock: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/blocks", nil, owner); strings.Contains(w.Body.String(), other.ID) {
		t.Errorf("unblock left the user in the list: %s", w.Body.String())
	}

	// ---- quick replies ----
	w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/quick-replies", map[string]any{"text": "Thanks for reaching out!"}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("add quick reply: status = %d: %s", w.Code, w.Body.String())
	}
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/businesses/"+b.ID+"/quick-replies", nil, owner)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Thanks for reaching out!") {
		t.Fatalf("list quick replies: status = %d: %s", w.Code, w.Body.String())
	}
	// Only a manager of THIS business, and the refusal is the same for a stranger
	// as for the owner of a different one.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/businesses/"+b.ID+"/quick-replies", nil, other); w.Code != http.StatusForbidden {
		t.Errorf("quick replies as a stranger: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/quick-replies", map[string]any{"text": "sneaky"}, other); w.Code != http.StatusForbidden {
		t.Errorf("add quick reply as a stranger: status = %d, want 403", w.Code)
	}
	// An empty reply is rejected where it is created.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+b.ID+"/quick-replies", map[string]any{"text": "  "}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("empty quick reply: status = %d, want 400", w.Code)
	}

	var replyID string
	if err := h.Pool.QueryRow(t.Context(), `SELECT id FROM quick_replies WHERE business_id=$1 LIMIT 1`, b.ID).Scan(&replyID); err != nil {
		t.Fatalf("read quick reply id: %v", err)
	}
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/businesses/"+b.ID+"/quick-replies/"+replyID, nil, other); w.Code != http.StatusForbidden {
		t.Errorf("delete quick reply as a stranger: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/businesses/"+b.ID+"/quick-replies/"+replyID, nil, owner); w.Code != http.StatusNoContent {
		t.Errorf("delete quick reply: status = %d, want 204", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/businesses/"+b.ID+"/quick-replies", nil, owner); strings.Contains(w.Body.String(), "Thanks for reaching out!") {
		t.Errorf("deleted quick reply is still listed: %s", w.Body.String())
	}
}

func TestPushSubscriptionRoutes(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	u := testutil.User(t, h)
	endpoint := "https://8.8.8.8/sub/zqx-" + u.ID
	body := map[string]any{
		"endpoint": endpoint,
		"keys":     map[string]any{"p256dh": "BFakeKeyForTests", "auth": "FakeAuthForTests"},
	}

	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/push/subscribe", body, u); w.Code != http.StatusCreated {
		t.Fatalf("subscribe: status = %d: %s", w.Code, w.Body.String())
	}
	// Re-subscribing is an upsert, not a duplicate row.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/push/subscribe", body, u); w.Code != http.StatusCreated {
		t.Errorf("re-subscribe: status = %d: %s", w.Code, w.Body.String())
	}
	var n int
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM push_subscriptions WHERE endpoint=$1`, endpoint).Scan(&n); err != nil {
		t.Fatalf("count subscriptions: %v", err)
	}
	if n != 1 {
		t.Errorf("subscription rows = %d, want 1", n)
	}

	// Push endpoints are user-supplied URLs that the server later POSTs to, so
	// the SSRF guard is load-bearing: loopback and plain HTTP must both be refused
	// before anything is stored.
	for name, ep := range map[string]string{
		"plain http":  "http://8.8.8.8/sub/x",
		"loopback":    "https://127.0.0.1/sub/x",
		"private lan": "https://10.0.0.5/sub/x",
	} {
		bad := map[string]any{"endpoint": ep, "keys": map[string]any{"p256dh": "x", "auth": "y"}}
		if w := dash(t, td, hdl, http.MethodPost, "/api/v1/push/subscribe", bad, u); w.Code != http.StatusBadRequest {
			t.Errorf("%s endpoint: status = %d, want 400", name, w.Code)
		}
	}
	// ...and an incomplete subscription is refused as well.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/push/subscribe", map[string]any{"endpoint": endpoint}, u); w.Code != http.StatusBadRequest {
		t.Errorf("subscribe without keys: status = %d, want 400", w.Code)
	}

	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/push/subscribe", map[string]any{"endpoint": endpoint}, u); w.Code != http.StatusNoContent {
		t.Errorf("unsubscribe: status = %d, want 204", w.Code)
	}
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM push_subscriptions WHERE endpoint=$1`, endpoint).Scan(&n); err != nil {
		t.Fatalf("count subscriptions after unsubscribe: %v", err)
	}
	if n != 0 {
		t.Errorf("subscription rows after unsubscribe = %d, want 0", n)
	}

	// The test deployment has no VAPID keys, so the endpoint says so instead of
	// handing out an empty key the browser cannot use.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/push/vapid-key", nil, u); w.Code != http.StatusBadRequest {
		t.Errorf("vapid key without configuration: status = %d, want 400", w.Code)
	}
}
