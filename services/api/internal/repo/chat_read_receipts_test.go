package repo_test

import (
	"context"
	"testing"

	"bizverse/api/internal/testutil"

	"bizverse/api/internal/util"
)

// `chat_messages.read_count` was declared, scanned, and serialised to every client
// as `read_count` - and never written. Every message reported zero reads forever,
// including to its own sender.
//
// Nothing was missing except the increment: the read marker
// (chat_participants.last_read_message_id), ChatRepo.LastRead, the
// POST /threads/{id}/read handler and its receipt.read broadcast all existed and
// worked. So this looks like a dead column rather than a dead feature, which is
// why nothing about it announced itself.

// thread builds a two-party chat with n messages from `from`, returning the thread
// id, the message ids in order, and both participant ids.
func thread(t *testing.T, h *testutil.H, n int) (threadID string, msgIDs []int64, a, b string) {
	t.Helper()
	owner := testutil.User(t, h)
	customer := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})

	var tid string
	if err := h.QueryRow(t,
		`INSERT INTO chat_threads (id, business_id, type, status) VALUES ($1, $2, 'business', 'open') RETURNING id`,
		util.NewUUID(), biz.ID,
	).Scan(&tid); err != nil {
		t.Fatalf("create thread: %v", err)
	}

	for _, u := range []struct {
		id, role string
	}{{owner.ID, "owner"}, {customer.ID, "user"}} {
		h.Exec(t, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,$4)`,
			util.NewUUID(), tid, u.id, u.role)
	}

	for range n {
		var id int64
		if err := h.QueryRow(t,
			`INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, client_msg_id, body)
			 VALUES ($1,$2,'user','text',$3,$4) RETURNING id`,
			tid, owner.ID, util.NewUUID()[:8]+"-msg", "hello").Scan(&id); err != nil {
			t.Fatalf("insert message: %v", err)
		}
		msgIDs = append(msgIDs, id)
	}
	return tid, msgIDs, owner.ID, customer.ID
}

func readCount(t *testing.T, h *testutil.H, threadID string, msgID int64) int {
	t.Helper()
	var n int
	if err := h.QueryRow(t,
		`SELECT read_count FROM chat_messages WHERE thread_id=$1 AND id=$2`, threadID, msgID).Scan(&n); err != nil {
		t.Fatalf("read_count: %v", err)
	}
	return n
}

// The headline case: the other party reads, and the sender sees it.
func TestReadingAThreadCreditsItsMessages(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	tid, ids, _, customer := thread(t, h, 3)

	for _, id := range ids {
		if got := readCount(t, h, tid, id); got != 0 {
			t.Fatalf("read_count on unread message = %d, want 0", got)
		}
	}

	if err := h.Repos.Chat.SetLastRead(ctx, tid, customer, ids[2]); err != nil {
		t.Fatalf("SetLastRead: %v", err)
	}

	for i, id := range ids {
		if got := readCount(t, h, tid, id); got != 1 {
			t.Errorf("read_count for message %d = %d, want 1", i, got)
		}
	}
}

// GREATEST makes the marker monotonic, so the frontend re-sending the same
// last_read_message_id on every render must not inflate the count. Without the
// `id > prev.old_id` guard this is exactly where read_count would run away.
func TestReReadingTheSameMessageIsNotDoubleCounted(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	tid, ids, _, customer := thread(t, h, 2)

	for range 5 {
		if err := h.Repos.Chat.SetLastRead(ctx, tid, customer, ids[1]); err != nil {
			t.Fatalf("SetLastRead: %v", err)
		}
	}
	if got := readCount(t, h, tid, ids[1]); got != 1 {
		t.Errorf("read_count after five identical calls = %d, want 1", got)
	}
	if got := readCount(t, h, tid, ids[0]); got != 1 {
		t.Errorf("read_count for the earlier message = %d, want 1", got)
	}
}

// Only the span actually crossed may be credited. Scrolling back up must not
// credit messages the reader had already passed.
func TestReadingBackwardsCreditsNothing(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	tid, ids, _, customer := thread(t, h, 3)

	if err := h.Repos.Chat.SetLastRead(ctx, tid, customer, ids[2]); err != nil {
		t.Fatalf("SetLastRead: %v", err)
	}
	if err := h.Repos.Chat.SetLastRead(ctx, tid, customer, ids[0]); err != nil {
		t.Fatalf("SetLastRead backwards: %v", err)
	}

	for i, id := range ids {
		if got := readCount(t, h, tid, id); got != 1 {
			t.Errorf("read_count for message %d = %d, want 1 - reading backwards credits nothing", i, got)
		}
	}
}

// read_count answers "how many OTHER people have seen this". A sender reading
// their own message must not credit themselves, or every message would report 1
// to the person who wrote it.
func TestReadingYourOwnMessageCreditsNothing(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	tid, ids, owner, _ := thread(t, h, 2)

	for _, id := range ids {
		if err := h.Repos.Chat.SetLastRead(ctx, tid, owner, id); err != nil {
			t.Fatalf("SetLastRead: %v", err)
		}
	}
	for i, id := range ids {
		if got := readCount(t, h, tid, id); got != 0 {
			t.Errorf("read_count for own message %d = %d, want 0", i, got)
		}
	}
}

// A system notice has a NULL sender. `sender_id IS DISTINCT FROM $2` is what keeps
// it creditable - a plain `<> $2` would evaluate to NULL, match nothing, and
// silently drop every automated message from the count.
func TestSystemMessageWithNullSenderIsCredited(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	tid, _, owner, customer := thread(t, h, 1)

	var sysID int64
	if err := h.QueryRow(t,
		`INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, client_msg_id, body)
		 VALUES ($1, NULL, 'system', 'system', $2, $3) RETURNING id`, tid, util.NewUUID()[:8]+"-sys", "thread opened").Scan(&sysID); err != nil {
		t.Fatalf("insert system message: %v", err)
	}

	if err := h.Repos.Chat.SetLastRead(ctx, tid, customer, sysID); err != nil {
		t.Fatalf("SetLastRead: %v", err)
	}
	if got := readCount(t, h, tid, sysID); got != 1 {
		t.Errorf("read_count for a system message = %d, want 1; a NULL sender must not exclude it", got)
	}

	// And the same message, read by the other participant, credits independently.
	before := readCount(t, h, tid, sysID)
	if err := h.Repos.Chat.SetLastRead(ctx, tid, owner, sysID); err != nil {
		t.Fatalf("SetLastRead as owner: %v", err)
	}
	if got := readCount(t, h, tid, sysID); got != before+1 {
		t.Errorf("read_count for a system message = %d, want %d - each participant credits once", got, before+1)
	}
}

// The marker itself must still behave: monotone, and readable back.
func TestReadMarkerStaysMonotone(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	tid, ids, _, customer := thread(t, h, 3)

	if err := h.Repos.Chat.SetLastRead(ctx, tid, customer, ids[0]); err != nil {
		t.Fatalf("SetLastRead: %v", err)
	}
	if err := h.Repos.Chat.SetLastRead(ctx, tid, customer, ids[2]); err != nil {
		t.Fatalf("SetLastRead: %v", err)
	}
	if err := h.Repos.Chat.SetLastRead(ctx, tid, customer, ids[1]); err != nil {
		t.Fatalf("SetLastRead backwards: %v", err)
	}

	got, err := h.Repos.Chat.LastRead(ctx, tid, customer)
	if err != nil {
		t.Fatalf("LastRead: %v", err)
	}
	if got != ids[2] {
		t.Errorf("last_read_message_id = %d, want %d (GREATEST must not move backwards)", got, ids[2])
	}
}
