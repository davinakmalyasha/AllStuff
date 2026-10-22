//go:build !race

package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
)

// Block, BlockList, CheckAccess and AddQuickReply had no coverage. Blocking is
// a safety feature, so what matters is that it is actually enforced on the send
// path, not that the row exists.

func TestChatBlockRefusesSelf(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	me := testutil.User(t, h)

	if err := c.Block(ctx, me.ID, me.ID, true); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("blocking yourself: err = %v, want ErrValidation", err)
	}
	list, err := c.BlockList(ctx, me.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("a refused self-block was recorded: %v", list)
	}
}

func TestChatBlockListTracksBlocks(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	me := testutil.User(t, h)
	peer := testutil.User(t, h)
	other := testutil.User(t, h)

	list, err := c.BlockList(ctx, me.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("a new user has a block list: %v", list)
	}

	if err := c.Block(ctx, me.ID, peer.ID, true); err != nil {
		t.Fatalf("Block: %v", err)
	}
	list, err = c.BlockList(ctx, me.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0] != peer.ID {
		t.Errorf("block list = %v, want [%s]", list, peer.ID)
	}
	// The block is one-directional in storage: the other user's list is empty.
	if other, err := c.BlockList(ctx, peer.ID); err != nil {
		t.Fatal(err)
	} else if len(other) != 0 {
		t.Errorf("the blocked user has a block list: %v", other)
	}

	// Unblocking removes it.
	if err := c.Block(ctx, me.ID, peer.ID, false); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if list, err = c.BlockList(ctx, me.ID); err != nil {
		t.Fatal(err)
	} else if len(list) != 0 {
		t.Errorf("the block list survived an unblock: %v", list)
	}
	_ = other
}

// A block is enforced on the send path in BOTH directions and in any thread
// type (PRD §5.5.4). The blocker cannot write either, so the block cannot be
// worked around by the person who set it.
func TestChatSendRefusesWhenBlocked(t *testing.T) {
	c, h := newTestChatNoLimit(t)
	ctx := fixtureCtx(t)
	alice := testutil.User(t, h)
	bob := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "",
		participant{userID: alice.ID}, participant{userID: bob.ID})

	if _, err := sendText(t, c, ctx, alice.ID, thread, "hello"); err != nil {
		t.Fatalf("before the block: %v", err)
	}

	if err := c.Block(ctx, alice.ID, bob.ID, true); err != nil {
		t.Fatalf("Block: %v", err)
	}

	if _, err := sendText(t, c, ctx, bob.ID, thread, "let me in"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("the blocked user: err = %v, want ErrValidation", err)
	}
	if _, err := sendText(t, c, ctx, alice.ID, thread, "let me in too"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("the blocker: err = %v, want ErrValidation", err)
	}

	// Nothing was written by either refused send.
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE thread_id=$1`, thread).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d messages in the thread, want only the one sent before the block", n)
	}

	// Unblocking restores the conversation.
	if err := c.Block(ctx, alice.ID, bob.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := sendText(t, c, ctx, bob.ID, thread, "back again"); err != nil {
		t.Errorf("after unblocking: %v", err)
	}
}

// A block applies to the whole thread, not just to the blocked pair: anyone
// blocked by a participant can no longer write to that participant, in any
// thread that contains them.
func TestChatBlockAppliesAcrossAGroup(t *testing.T) {
	c, h := newTestChatNoLimit(t)
	ctx := fixtureCtx(t)
	alice := testutil.User(t, h)
	bob := testutil.User(t, h)
	carol := testutil.User(t, h)
	// A business thread is the multi-participant case: the thread_type enum has
	// only 'direct' and 'business'.
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Group Cafe", Category: cat, Owner: alice,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	group := makeThread(t, h, "business", &biz.ID, "",
		participant{userID: alice.ID}, participant{userID: bob.ID}, participant{userID: carol.ID})

	if err := c.Block(ctx, alice.ID, bob.ID, true); err != nil {
		t.Fatalf("Block: %v", err)
	}
	// Alice can no longer write to the group, because Bob is in it.
	if _, err := sendText(t, c, ctx, alice.ID, group, "hi everyone"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("the blocker in a group: err = %v, want ErrValidation", err)
	}
	// Carol is not blocked, so Carol can still write.
	if _, err := sendText(t, c, ctx, carol.ID, group, "hi everyone"); err != nil {
		t.Errorf("an unrelated participant: %v", err)
	}
}

func TestChatCheckAccess(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	member := testutil.User(t, h)
	stranger := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: member.ID})

	if err := c.CheckAccess(ctx, member.ID, thread); err != nil {
		t.Errorf("a participant: %v", err)
	}
	if err := c.CheckAccess(ctx, stranger.ID, thread); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a stranger: err = %v, want ErrForbidden", err)
	}
	if err := c.CheckAccess(ctx, member.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("an unknown thread: err = %v, want ErrNotFound", err)
	}
}

func TestChatAddQuickReply(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	owner := testutil.User(t, h)
	stranger := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Quick Reply Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})

	// Only someone who manages the business may set its replies.
	if err := c.AddQuickReply(ctx, stranger.ID, biz.ID, "hello"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a stranger: err = %v, want ErrForbidden", err)
	}
	if err := c.AddQuickReply(ctx, owner.ID, biz.ID, "   "); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("empty text: err = %v, want ErrValidation", err)
	}
	if err := c.AddQuickReply(ctx, owner.ID, biz.ID, strings.Repeat("x", 501)); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("501 runes: err = %v, want ErrValidation", err)
	}

	if err := c.AddQuickReply(ctx, owner.ID, biz.ID, "  Thanks for reaching out!  "); err != nil {
		t.Fatalf("AddQuickReply: %v", err)
	}
	var text string
	if err := h.Pool.QueryRow(ctx, `SELECT text FROM quick_replies WHERE business_id=$1`, biz.ID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "Thanks for reaching out!" {
		t.Errorf("stored text = %q, want it trimmed", text)
	}

	// The list is capped at 20 (PRD §5.5.2).
	for i := 0; i < 19; i++ {
		if err := c.AddQuickReply(ctx, owner.ID, biz.ID, fmt.Sprintf("reply %d", i)); err != nil {
			t.Fatalf("reply %d: %v", i, err)
		}
	}
	if err := c.AddQuickReply(ctx, owner.ID, biz.ID, "one too many"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("the 21st reply: err = %v, want ErrValidation", err)
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM quick_replies WHERE business_id=$1`, biz.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 20 {
		t.Errorf("%d quick replies stored, want 20", n)
	}
}
