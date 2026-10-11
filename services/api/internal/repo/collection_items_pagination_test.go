package repo_test

import (
	"context"
	"testing"

	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Collection items were read with no LIMIT and no OFFSET.
//
// A collection is user-growable without bound - users save businesses and products
// into them - so `GET /me/collections/{id}/items` read, serialised and rendered every
// item a user had ever saved, and the PUBLIC reader did the same with no
// authentication and no cap at all. Opening a collection cost more the more had been
// saved into it.

// collectionWithItems builds a collection holding n business items, inserted in a
// SINGLE statement.
//
// That detail is the point of most of these tests. `created_at` defaults to now(),
// which in PostgreSQL is the TRANSACTION timestamp, so every row inserted by one
// statement shares it exactly. Combined with the shared sort_order default that makes
// the pre-existing `ORDER BY sort_order, created_at DESC` a total tie, which is the
// worst case an OFFSET page can face and the reason the `id` tiebreaker is not
// decoration.
func collectionWithItems(t *testing.T, h *testutil.H, n int) (collectionID string, itemCount int) {
	t.Helper()
	owner := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})

	colID := util.NewUUID()
	h.Exec(t, `INSERT INTO collections (id, user_id, name, slug)
	           VALUES ($1::uuid, $2::uuid, 'Saved', $3)`, colID, owner.ID, "saved-"+colID[:8])

	h.Exec(t, `INSERT INTO collection_items (id, collection_id, target_type, target_id)
	           SELECT gen_random_uuid(), $1::uuid, 'business', $2::uuid
	             FROM generate_series(1, $3)`, colID, biz.ID, n)

	return colID, n
}

func pageIDs(t *testing.T, h *testutil.H, colID string, limit, offset int) []string {
	t.Helper()
	items, err := h.Repos.Engagement.ListItems(context.Background(), colID, limit, offset)
	if err != nil {
		t.Fatalf("ListItems(limit=%d, offset=%d): %v", limit, offset, err)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestCollectionItemsRespectsTheLimit(t *testing.T) {
	h := testutil.New(t)
	colID, total := collectionWithItems(t, h, 12)

	got := pageIDs(t, h, colID, 5, 0)
	if len(got) != 5 {
		t.Errorf("limit 5 returned %d rows, want 5", len(got))
	}

	// A limit larger than the collection must simply return what exists, not pad and
	// not error.
	all := pageIDs(t, h, colID, 100, 0)
	if len(all) != total {
		t.Errorf("limit 100 over %d items returned %d rows", total, len(all))
	}
}

// THE PROPERTY THAT MATTERS
// Paging through with OFFSET must visit every item exactly once. Without a unique
// final ORDER BY term, tied rows can be served on two different pages while another
// is never served at all - and every individual page looks correct.
func TestCollectionItemPagesDoNotOverlapOrSkip(t *testing.T) {
	h := testutil.New(t)
	colID, total := collectionWithItems(t, h, 10)

	seen := map[string]int{}
	for offset := 0; offset < total; offset += 3 {
		for _, id := range pageIDs(t, h, colID, 3, offset) {
			seen[id]++
		}
	}

	if len(seen) != total {
		t.Errorf("paging saw %d distinct items, want %d - a row is unreachable across pages", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("item %s appeared on %d pages, want exactly 1 - the ORDER BY is not a total order", id, n)
		}
	}
}

// The unbounded reader is kept separate so the export path is the only thing that can
// reach it. It must still return everything.
func TestListItemsAllReturnsEverything(t *testing.T) {
	h := testutil.New(t)
	colID, total := collectionWithItems(t, h, 7)

	items, err := h.Repos.Engagement.ListItemsAll(context.Background(), colID)
	if err != nil {
		t.Fatalf("ListItemsAll: %v", err)
	}
	if len(items) != total {
		t.Errorf("ListItemsAll returned %d items, want %d", len(items), total)
	}
}

// An empty collection must return no rows rather than an error, and the first page of
// an over-large offset must be empty rather than repeating the last page.
func TestCollectionItemPagingEdges(t *testing.T) {
	h := testutil.New(t)
	colID, total := collectionWithItems(t, h, 3)

	if got := pageIDs(t, h, colID, 10, 0); len(got) != total {
		t.Errorf("first page returned %d rows, want %d", len(got), total)
	}
	if got := pageIDs(t, h, colID, 10, 100); len(got) != 0 {
		t.Errorf("an offset past the end returned %d rows, want 0", len(got))
	}
}
