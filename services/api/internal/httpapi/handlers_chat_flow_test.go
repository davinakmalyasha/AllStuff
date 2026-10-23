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

// The chat handlers had no coverage. The service underneath is well tested, so
// what matters here is the HTTP wiring: status codes, the pagination cursor that
// once made history beyond the first 50 messages unreachable, and the
// participant checks on every entry point.

type threadEnvelope struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type messageEnvelope struct {
	Message struct {
		ID         int64  `json:"id"`
		Body       string `json:"body"`
		SenderID   string `json:"sender_id"`
		DeletedFor string `json:"deleted_for"`
	} `json:"message"`
}

// chatSend posts one message. Send allows one message per second per
// (user, thread), so the caller spaces them.
func chatSend(t *testing.T, td *testDeps, hdl http.Handler, u *domain.User, threadID, body string) (int, int64) {
	t.Helper()
	w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads/"+threadID+"/messages", map[string]any{"body": body}, u)
	var env messageEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env.Message.ID
}

func TestChatFlow(t *testing.T) {
	// Pool-bound: Chat.Send opens its own transaction, and Pool() is nil inside
	// the default tx-bound harness.
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	alice := testutil.User(t, h)
	bob := testutil.User(t, h)
	mallory := testutil.User(t, h)

	// Create a direct thread. Asking again returns the SAME thread rather than
	// creating a duplicate conversation.
	w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads", map[string]any{"user_id": bob.ID}, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create thread: status = %d: %s", w.Code, w.Body.String())
	}
	var created threadEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	threadID := created.Thread.ID
	if threadID == "" {
		t.Fatal("no thread id")
	}
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/threads", map[string]any{"user_id": bob.ID}, alice)
	var again threadEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &again)
	if again.Thread.ID != threadID {
		t.Errorf("a second create made a second thread: %s vs %s", again.Thread.ID, threadID)
	}

	// Neither a missing target nor a participant check is optional.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads", map[string]any{}, alice); w.Code != http.StatusBadRequest {
		t.Errorf("no target: status = %d, want 400", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID, nil, mallory); w.Code != http.StatusForbidden {
		t.Errorf("non-participant detail: status = %d, want 403", w.Code)
	}

	// Three messages, spaced past the one-per-second limiter.
	var ids []int64
	for i, body := range []string{"first", "second", "third"} {
		code, id := chatSend(t, td, hdl, alice, threadID, body)
		if code != http.StatusCreated || id == 0 {
			t.Fatalf("send %d: status = %d, id = %d: %s", i, code, id, body)
		}
		ids = append(ids, id)
		if i < 2 {
			time.Sleep(1100 * time.Millisecond)
		}
	}

	// Pagination: limit=2 returns the newest two and says there is more, and the
	// cursor reaches the rest. This is the path that used to be missing entirely,
	// making history beyond the first page unreachable.
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"?limit=2", nil, alice)
	if w.Code != http.StatusOK {
		t.Fatalf("page 1: status = %d", w.Code)
	}
	var page1 struct {
		Messages []struct {
			ID int64 `json:"id"`
		} `json:"messages"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatal(err)
	}
	if len(page1.Messages) != 2 || !page1.HasMore {
		t.Fatalf("page 1 = %d messages, has_more=%v; want 2 and true", len(page1.Messages), page1.HasMore)
	}
	oldest := page1.Messages[len(page1.Messages)-1].ID
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"?limit=2&before="+strconv.FormatInt(oldest, 10), nil, alice)
	var page2 struct {
		Messages []struct {
			ID int64 `json:"id"`
		} `json:"messages"`
		HasMore bool `json:"has_more"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page2)
	if len(page2.Messages) != 1 || page2.HasMore {
		t.Errorf("page 2 = %d messages, has_more=%v; want 1 and false", len(page2.Messages), page2.HasMore)
	}
	if len(page2.Messages) == 1 && page2.Messages[0].ID != ids[0] {
		t.Errorf("page 2 returned message %d, want the oldest %d", page2.Messages[0].ID, ids[0])
	}
	// A malformed cursor is a validation error, not a silent default.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"?before=abc", nil, alice); w.Code != http.StatusBadRequest {
		t.Errorf("bad cursor: status = %d, want 400", w.Code)
	}

	// Only the author may edit.
	if w := dash(t, td, hdl, http.MethodPatch, "/api/v1/messages/"+strconv.FormatInt(ids[0], 10), map[string]any{"body": "edited"}, alice); w.Code != http.StatusOK {
		t.Errorf("author edit: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPatch, "/api/v1/messages/"+strconv.FormatInt(ids[0], 10), map[string]any{"body": "hijacked"}, bob); w.Code == http.StatusOK {
		t.Error("a participant who is not the author edited the message")
	}

	// Delete for me: no body, and the message disappears for the deleter only.
	// The UI offers this only on your own messages, and the service enforces the
	// same rule, so the author is the one hiding it.
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/messages/"+strconv.FormatInt(ids[1], 10)+"?scope=me", nil, bob); w.Code != http.StatusForbidden {
		t.Errorf("delete for me by a non-author: status = %d, want 403", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/messages/"+strconv.FormatInt(ids[1], 10)+"?scope=me", nil, alice); w.Code != http.StatusNoContent {
		t.Errorf("delete for me: status = %d, want 204", w.Code)
	}
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID, nil, alice)
	if strings.Contains(w.Body.String(), `"second"`) {
		t.Error("a message deleted for me is still in my history")
	}
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID, nil, bob)
	if !strings.Contains(w.Body.String(), `"second"`) {
		t.Error("delete-for-me removed the message for the other participant too")
	}

	// Delete for everyone: the author's own message, echoed back as deleted.
	w = dash(t, td, hdl, http.MethodDelete, "/api/v1/messages/"+strconv.FormatInt(ids[2], 10)+"?scope=everyone", nil, alice)
	if w.Code != http.StatusOK {
		t.Fatalf("delete for everyone: status = %d: %s", w.Code, w.Body.String())
	}
	var del messageEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &del)
	if del.Message.DeletedFor != "everyone" {
		t.Errorf("deleted_for = %q, want everyone", del.Message.DeletedFor)
	}

	// Reactions are available to any participant, and are removable.
	if w := dash(t, td, hdl, http.MethodPut, "/api/v1/messages/"+strconv.FormatInt(ids[0], 10)+"/reaction", map[string]any{"emoji": "\U0001F44D"}, bob); w.Code != http.StatusOK {
		t.Errorf("react: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodDelete, "/api/v1/messages/"+strconv.FormatInt(ids[0], 10)+"/reaction", nil, bob); w.Code != http.StatusOK {
		t.Errorf("unreact: status = %d", w.Code)
	}
	// An outsider cannot react to a message in a thread they are not in.
	if w := dash(t, td, hdl, http.MethodPut, "/api/v1/messages/"+strconv.FormatInt(ids[0], 10)+"/reaction", map[string]any{"emoji": "\U0001F44D"}, mallory); w.Code == http.StatusOK {
		t.Error("an outsider reacted to a private message")
	}
}
