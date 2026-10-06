package repo_test

import (
	"context"
	"testing"

	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Query-count assertions for the two fan-outs removed this session.
//
// A result assertion cannot catch an N+1: every page still returns the right rows,
// just slowly, and slowly is invisible until it is load-bearing. So these count
// statements directly, and vary the input size - because the defect is not "many
// queries", it is "the count GROWS with the data".
//
// Both use h.UsePool() because the query counter instruments Pool. Repos is
// normally transaction-bound in tests, where nothing would be counted.

// A direct thread costs three round trips per send: ParticipantIDs, then one
// IsBlocked per participant. A group thread costs one more per member, so the cost
// of sending a message grew with the size of the conversation.
//
// The batched form is two queries regardless of participant count.
func TestBlockCheckOnSendIsOneQueryRegardlessOfThreadSize(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	ctx := context.Background()

	owner := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	sender := testutil.User(t, h)

	var threadID string
	h.ExecPool(t, `INSERT INTO chat_threads (id, business_id, type, status)
	               VALUES ($1,$2,'business','open')`, util.NewUUID(), biz.ID)
	if err := h.QueryRow(t,
		`SELECT id FROM chat_threads WHERE business_id = $1 ORDER BY created_at DESC LIMIT 1`,
		biz.ID).Scan(&threadID); err != nil {
		t.Fatalf("create thread: %v", err)
	}

	// Peers are drawn from a pool that does not overlap the thread, so no iteration
	// can collide with a participant inserted by an earlier one.
	pool := make([]string, 0, 16)
	for range cap(pool) {
		pool = append(pool, testutil.User(t, h).ID)
	}
	for i := range pool[:15] {
		h.ExecPool(t, `INSERT INTO chat_participants (id, thread_id, user_id, role)
		            SELECT gen_random_uuid(), $1, u.id, 'user' FROM users u
		             WHERE u.id = $2`, threadID, pool[i])
	}

	// Sizes 2, 5 and 10 participants. The sender is included in every set so the
	// self-exclusion path is exercised.
	for i, n := range []int{2, 5, 10} {
		h.ExecPool(t, `DELETE FROM chat_participants WHERE thread_id = $1`, threadID)
		for j := range i + 1 {
			h.ExecPool(t, `INSERT INTO chat_participants (id, thread_id, user_id, role)
			            VALUES (gen_random_uuid(), $1, $2, 'user')`, threadID, pool[j])
		}
		h.ExecPool(t, `INSERT INTO chat_participants (id, thread_id, user_id, role)
		            VALUES (gen_random_uuid(), $1, $2, 'user')`, threadID, sender.ID)

		peers, err := h.Repos.Chat.ParticipantIDs(ctx, threadID)
		if err != nil {
			t.Fatalf("ParticipantIDs: %v", err)
		}

		h.ResetQueryCount()
		blocked, err := h.Repos.Chat.AnyBlocked(ctx, sender.ID, peers)
		if err != nil {
			t.Fatalf("AnyBlocked: %v", err)
		}
		if blocked {
			t.Fatalf("%d participants: reported blocked with no blocks present", n)
		}

		// One, and the same one for every size. Growing with the participant count is
		// the N+1 signature this replaces.
		if got := h.QueryCount(); got != 1 {
			t.Errorf("%d participants: %d queries, want exactly 1. A count that grows with the thread size "+
				"means the per-participant block check is back.", n, got)
		}
	}
}

// The batching must preserve the SEMANTICS of the loop it replaced, which checked
// both directions: "I blocked them" and "they blocked me".
//
// Fresh users per subtest.
//
// The first version of this table shared one `a`, `b` and `c` across every case, so
// the blocks inserted by "a blocked b" were still present when "sender self-excluded"
// ran - and that case asserts FALSE. It failed with "AnyBlocked = true, want false",
// which reads like a broken query and is actually a leaked fixture.
//
// Isolation matters more here than it looks: this is a table of about a boolean
// about set membership, so a shared fixture silently changes the expected answers of
// every case after the first that writes a row.
func TestAnyBlockedMatchesThePerPeerLoopItReplaces(t *testing.T) {
	// Explicit uuid casts on every parameter. Postgres cannot infer a type for an
	// untyped parameter in an INSERT, and reports it only when that statement
	// happens to run - so the early cases would pass and a later one would abort
	// the shared transaction, failing every case after it.
	block := func(t *testing.T, h *testutil.H, x, y string) {
		t.Helper()
		h.Exec(t, `INSERT INTO blocks (id, blocker_id, blocked_id)
		           VALUES (gen_random_uuid(), $1::uuid, $2::uuid)`, x, y)
	}

	cases := []struct {
		name  string
		setup func(t *testing.T, h *testutil.H, a, b, c, stranger string)
		peers func(a, b, c, stranger string) []string
		want  bool
	}{
		{
			name:  "no blocks",
			setup: func(*testing.T, *testutil.H, string, string, string, string) {},
			peers: func(_, b, c, _ string) []string { return []string{b, c} },
			want:  false,
		},
		{
			name:  "a blocked b",
			setup: func(t *testing.T, h *testutil.H, a, b, _, _ string) { block(t, h, a, b) },
			peers: func(_, b, c, _ string) []string { return []string{b, c} },
			want:  true,
		},
		{
			name:  "b blocked a (reverse direction)",
			setup: func(t *testing.T, h *testutil.H, a, b, _, _ string) { block(t, h, b, a) },
			peers: func(_, b, c, _ string) []string { return []string{b, c} },
			want:  true,
		},
		{
			// The regression this whole refactor risks: batching a loop into one
			// query can quietly widen the match. b blocking c must not implicate a.
			name:  "only an unrelated peer is blocked",
			setup: func(t *testing.T, h *testutil.H, _, b, c, _ string) { block(t, h, b, c) },
			peers: func(a, _, _, stranger string) []string { return []string{a, stranger} },
			want:  false,
		},
		{
			// The sender is in their own peer list; the loop skipped them inline and
			// the batched form must not start matching a self-block.
			name:  "sender self-excluded from their own list",
			setup: func(*testing.T, *testutil.H, string, string, string, string) {},
			peers: func(a, b, _, _ string) []string { return []string{a, b} },
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := testutil.New(t)
			ctx := context.Background()
			a := testutil.User(t, h)
			b := testutil.User(t, h)
			c := testutil.User(t, h)
			stranger := testutil.User(t, h)

			tc.setup(t, h, a.ID, b.ID, c.ID, stranger.ID)

			got, err := h.Repos.Chat.AnyBlocked(ctx, a.ID, tc.peers(a.ID, b.ID, c.ID, stranger.ID))
			if err != nil {
				t.Fatalf("AnyBlocked: %v", err)
			}
			if got != tc.want {
				t.Errorf("AnyBlocked = %v, want %v", got, tc.want)
			}
		})
	}
}

// Empty and self-only inputs must not reach the database: an empty IN list is a
// syntax error, and "no peers" is trivially unblocked.
func TestAnyBlockedShortCircuitsOnEmptyInput(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	ctx := context.Background()
	a := testutil.User(t, h)

	for _, peers := range [][]string{nil, {}, {a.ID}} {
		h.ResetQueryCount()
		blocked, err := h.Repos.Chat.AnyBlocked(ctx, a.ID, peers)
		if err != nil {
			t.Fatalf("AnyBlocked(%v): %v", peers, err)
		}
		if blocked {
			t.Errorf("AnyBlocked(%v) = true, want false", peers)
		}
		if got := h.QueryCount(); got != 0 {
			t.Errorf("AnyBlocked(%v) issued %d queries, want 0", peers, got)
		}
	}
}

// The export path called ListOptions and ListVariants once per product. These pin
// the batched form to a constant two queries however many products there are.
func TestExportProductHydrationIsTwoQueries(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	ctx := context.Background()
	owner := testutil.User(t, h)

	for _, n := range []int{3, 8} {
		h.ResetQueryCount()
		biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
		ids := make([]string, 0, n)
		for range n {
			p := testutil.Product(t, h, biz.ID, "p"+util.NewUUID()[:6])
			ids = append(ids, p.ID)
		}

		h.ResetQueryCount()
		opts, err := h.Repos.Products.OptionsByProducts(ctx, ids)
		if err != nil {
			t.Fatalf("OptionsByProducts: %v", err)
		}
		if got := h.QueryCount(); got != 1 {
			t.Errorf("%d products: options took %d queries, want 1", n, got)
		}
		// Every requested id is keyed, including products with no options, so callers
		// never need a presence check.
		for _, id := range ids {
			if _, ok := opts[id]; !ok {
				t.Errorf("%d products: id %s missing from the result map", n, id)
			}
		}

		h.ResetQueryCount()
		if _, err := h.Repos.Products.VariantsByProducts(ctx, ids); err != nil {
			t.Fatalf("VariantsByProducts: %v", err)
		}
		if got := h.QueryCount(); got != 1 {
			t.Errorf("%d products: variants took %d queries, want 1", n, got)
		}
	}
}

// Empty input must not reach the database, for the same reason as AnyBlocked.
func TestExportHydrationShortCircuitsOnEmptyInput(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	ctx := context.Background()

	h.ResetQueryCount()
	o, err := h.Repos.Products.OptionsByProducts(ctx, nil)
	if err != nil {
		t.Fatalf("OptionsByProducts(nil): %v", err)
	}
	v, err := h.Repos.Products.VariantsByProducts(ctx, []string{})
	if err != nil {
		t.Fatalf("VariantsByProducts(empty): %v", err)
	}
	if len(o) != 0 || len(v) != 0 {
		t.Error("empty input must return empty maps, not rows")
	}
	if got := h.QueryCount(); got != 0 {
		t.Errorf("empty input issued %d queries, want 0", got)
	}
}
