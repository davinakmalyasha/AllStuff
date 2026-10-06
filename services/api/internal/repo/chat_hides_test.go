package repo_test

import (
	"context"
	"testing"

	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// "Delete for me" hid the message from EVERYONE.
//
// chat_messages.deleted_for is one column on the shared row, and 'me' is not
// 'everyone' - so every read path, which filters on `deleted_for <> 'everyone'`,
// kept serving it. The author removing their own message therefore erased it for
// the other party while continuing to see it themselves. 0048 moves the
// per-participant scope into its own table.
//
// These assert the behaviour the feature is named for, which is why each test
// reads the thread from BOTH sides. A test that only checks the actor's view
// passes against the old broken behaviour.

// convo builds a two-party thread, writes n messages from the CUSTOMER, and
// returns the thread id, the message ids in order, and both participant ids.
//
// Messages come from the customer rather than a caller-supplied sender id
// specifically so the call sites read `convo(t, h, 1)` instead of passing a
// variable that does not exist yet - the first draft took `senders ...string` and
// every call site referenced the very `user` it was trying to receive.
func convo(t *testing.T, h *testutil.H, n int) (threadID string, msgIDs []int64, owner, user string) {
	t.Helper()
	ownerU := testutil.User(t, h)
	userU := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: ownerU})

	if err := h.QueryRow(t,
		`INSERT INTO chat_threads (id, business_id, type, status)
		 VALUES ($1,$2,'business','open') RETURNING id`,
		util.NewUUID(), biz.ID,
	).Scan(&threadID); err != nil {
		t.Fatalf("create thread: %v", err)
	}

	for _, p := range []struct{ id, role string }{{ownerU.ID, "owner"}, {userU.ID, "user"}} {
		h.Exec(t, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,$4)`,
			util.NewUUID(), threadID, p.id, p.role)
	}

	for range n {
		var id int64
		if err := h.QueryRow(t,
			`INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, client_msg_id, body)
			 VALUES ($1,$2,'user','text',$3,$4) RETURNING id`,
			threadID, userU.ID, util.NewUUID()[:8]+"-m", "the message body").Scan(&id); err != nil {
			t.Fatalf("insert message: %v", err)
		}
		msgIDs = append(msgIDs, id)
	}
	return threadID, msgIDs, ownerU.ID, userU.ID
}

func bodies(t *testing.T, h *testutil.H, threadID, viewer string) []string {
	t.Helper()
	msgs, err := h.Repos.Chat.MessagesByThread(context.Background(), threadID, viewer, 0, 100)
	if err != nil {
		t.Fatalf("MessagesByThread(%s): %v", viewer, err)
	}
	out := []string{}
	for _, m := range msgs {
		if m.Body != nil {
			out = append(out, *m.Body)
		}
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The headline bug: one participant hides a message and the other still sees it.
func TestHidingForMeLeavesTheMessageVisibleToTheOtherParty(t *testing.T) {
	h := testutil.New(t)
	tid, ids, owner, user := convo(t, h, 1)

	if err := h.Repos.Chat.DeleteMessage(context.Background(), ids[0], "me", user); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	if got := bodies(t, h, tid, user); has(got, "the message body") {
		t.Error("the message is still visible to the participant who hid it")
	}
	if got := bodies(t, h, tid, owner); !has(got, "the message body") {
		t.Error("the message vanished for the OTHER participant; \"delete for me\" deleted it for everyone")
	}
}

// Hiding must survive a refetch. Before 0048 nothing in the read path consulted a
// per-participant hide, so the message simply reappeared on reload.
func TestHidingSurvivesARefetch(t *testing.T) {
	h := testutil.New(t)
	tid, ids, _, user := convo(t, h, 1)

	if err := h.Repos.Chat.DeleteMessage(context.Background(), ids[0], "me", user); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	for range 3 {
		if got := bodies(t, h, tid, user); has(got, "the message body") {
			t.Fatal("the hidden message came back on refetch")
		}
	}
}

// 'everyone' is genuinely global and must stay on the shared row, where every
// reader path already honours it.
func TestDeleteForEveryoneRemovesItForBothParties(t *testing.T) {
	h := testutil.New(t)
	tid, ids, owner, user := convo(t, h, 1)

	if err := h.Repos.Chat.DeleteMessage(context.Background(), ids[0], "everyone", user); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	for _, viewer := range []struct{ id, who string }{{user, "actor"}, {owner, "other"}} {
		if got := bodies(t, h, tid, viewer.id); has(got, "the message body") {
			t.Errorf("the message is still visible to the %s after delete-for-everyone", viewer.who)
		}
	}
}

// Hiding one message must not hide the rest of the thread.
func TestHidingOneMessageLeavesTheThreadIntact(t *testing.T) {
	h := testutil.New(t)
	tid, ids, _, user := convo(t, h, 3)

	if err := h.Repos.Chat.DeleteMessage(context.Background(), ids[1], "me", user); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	got := bodies(t, h, tid, user)
	if len(got) != 2 {
		t.Errorf("viewer sees %d messages, want 2 (only the middle one is hidden)", len(got))
	}
}

// Repeated deletes must not raise. The hide is keyed (message_id, user_id), and a
// second delete is a no-op rather than a unique violation.
func TestHidingTheSameMessageTwiceIsSafe(t *testing.T) {
	h := testutil.New(t)
	tid, ids, owner, user := convo(t, h, 1)

	for range 3 {
		if err := h.Repos.Chat.DeleteMessage(context.Background(), ids[0], "me", user); err != nil {
			t.Fatalf("DeleteMessage (repeat): %v", err)
		}
	}
	if got := bodies(t, h, tid, owner); !has(got, "the message body") {
		t.Error("the other participant lost visibility after repeated hides")
	}
}

// Search and the media gallery are reader paths too. If they skip the hides table
// then a hidden message is one search away from being visible again.
func TestSearchRespectsPerParticipantHides(t *testing.T) {
	h := testutil.New(t)
	tid, ids, owner, user := convo(t, h, 1)

	// Distinctive token so the search matches only this message.
	//
	// h.Exec, NOT h.Pool.Exec. Repos is bound to a transaction that only rolls back
	// at cleanup, so the fixture row is uncommitted and invisible to the separate
	// Pool connection. An UPDATE sent there matches nothing, exits 0, and reads as a
	// successful edit that never happened - which is exactly what the first run of
	// this test did, reporting zero search hits both before and after hiding.
	h.Exec(t, `UPDATE chat_messages SET body='zebra unique token' WHERE id=$1`, ids[0])

	actorHits, err := h.Repos.Chat.SearchMessages(context.Background(), tid, user, "zebra", 50)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(actorHits) != 1 {
		t.Errorf("actor search returned %d hits, want 1 before hiding", len(actorHits))
	}

	if err := h.Repos.Chat.DeleteMessage(context.Background(), ids[0], "me", user); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	actorHits, err = h.Repos.Chat.SearchMessages(context.Background(), tid, user, "zebra", 50)
	if err != nil {
		t.Fatalf("SearchMessages after hide: %v", err)
	}
	if len(actorHits) != 0 {
		t.Errorf("a hidden message was returned by search; the hides table is not consulted")
	}

	otherHits, err := h.Repos.Chat.SearchMessages(context.Background(), tid, owner, "zebra", 50)
	if err != nil {
		t.Fatalf("SearchMessages as other party: %v", err)
	}
	if len(otherHits) != 1 {
		t.Errorf("the other participant's search returned %d hits, want 1", len(otherHits))
	}
}
