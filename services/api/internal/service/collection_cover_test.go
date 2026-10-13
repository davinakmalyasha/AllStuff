//go:build !race

package service_test

import (
	"context"
	"errors"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// `collections.cover_item_id` has been in the schema since 0001 and appears nowhere
// else in the Go source - it is specified in the PRD ("collections - ... cover_item_id
// ...") and was never implemented, so the column was dead.
//
// Unlike the audit columns fixed earlier, this one is not a missing write on an
// existing path: there was no path. It is an unimplemented feature, so these tests
// define the behaviour rather than pin an existing one.

// collection builds an empty collection owned by a fresh user.
func collection(t *testing.T, h *testutil.H, ownerID string) string {
	t.Helper()
	colID := util.NewUUID()
	h.Exec(t, `INSERT INTO collections (id, user_id, name, slug)
	           VALUES ($1::uuid, $2::uuid, 'Covered', $3)`, colID, ownerID, "cov-"+colID[:8])
	return colID
}

func itemID(t *testing.T, h *testutil.H, colID string) string {
	t.Helper()
	id := util.NewUUID()
	h.Exec(t, `INSERT INTO collection_items (id, collection_id, target_type, target_id)
	           VALUES ($1::uuid, $2::uuid, 'business', gen_random_uuid())`, id, colID)
	return id
}

func TestCollectionCoverIsStoredAndReturned(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	eng := newTestEngagement(h)

	owner := testutil.User(t, h)
	colID := collection(t, h, owner.ID)
	item := itemID(t, h, colID)

	got, err := eng.UpdateCollection(ctx, owner.ID, colID, nil, nil, &item)
	if err != nil {
		t.Fatalf("UpdateCollection: %v", err)
	}
	if got == nil || got.CoverItemID == nil {
		t.Fatal("cover_item_id came back nil after being set")
	}
	if *got.CoverItemID != item {
		t.Errorf("cover_item_id = %q, want %q", *got.CoverItemID, item)
	}

	// And it survives a re-read through the list path, which is a different query.
	list, err := h.Repos.Engagement.ListCollections(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	for _, c := range list {
		if c.ID != colID {
			continue
		}
		if c.CoverItemID == nil || *c.CoverItemID != item {
			t.Errorf("ListCollections cover_item_id = %v, want %q", c.CoverItemID, item)
		}
		return
	}
	t.Fatal("the collection is missing from ListCollections")
}

// THE VALIDATION THAT MATTERS
// A cover is a reference INTO the collection's items. Without a membership check a
// user could point it at any item id in the system, including another user's, and the
// stored value would mean nothing.
func TestCollectionCoverMustBeAnItemInThatCollection(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	eng := newTestEngagement(h)

	owner := testutil.User(t, h)
	mine := collection(t, h, owner.ID)
	theirs := collection(t, h, owner.ID)
	foreignItem := itemID(t, h, theirs)

	_, err := eng.UpdateCollection(ctx, owner.ID, mine, nil, nil, &foreignItem)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("setting a cover to an item from another collection: err = %v, want ErrValidation", err)
	}

	// Nothing must have been written by the rejected call.
	list, err := h.Repos.Engagement.ListCollections(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	for _, c := range list {
		if c.ID == mine && c.CoverItemID != nil {
			t.Errorf("the rejected update still wrote a cover: %v", *c.CoverItemID)
		}
	}
}

// A cover id that exists nowhere must be rejected rather than stored as a dangling
// reference.
func TestCollectionCoverRejectsAnUnknownItem(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	eng := newTestEngagement(h)

	owner := testutil.User(t, h)
	colID := collection(t, h, owner.ID)
	ghost := util.NewUUID()

	_, err := eng.UpdateCollection(ctx, owner.ID, colID, nil, nil, &ghost)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("setting a cover to a non-existent item: err = %v, want ErrValidation", err)
	}
}

// An empty string CLEARS the cover. A pointer is used so absent and clear are
// distinguishable: omitting the key must leave the cover alone.
func TestCollectionCoverCanBeCleared(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	eng := newTestEngagement(h)

	owner := testutil.User(t, h)
	colID := collection(t, h, owner.ID)
	item := itemID(t, h, colID)

	if _, err := eng.UpdateCollection(ctx, owner.ID, colID, nil, nil, &item); err != nil {
		t.Fatalf("set cover: %v", err)
	}

	// Absent: leave it alone. Another field changes alongside it, which is both the
	// realistic case - renaming a collection must not wipe its cover - and a
	// requirement of this service: an update with nothing to change is rejected with
	// "Nothing to update", existing behaviour that this test is not about. The first
	// version passed three nils and failed on that guard rather than on the cover.
	name := "Renamed"
	got, err := eng.UpdateCollection(ctx, owner.ID, colID, &name, nil, nil)
	if err != nil {
		t.Fatalf("update without a cover: %v", err)
	}
	if got.CoverItemID == nil {
		t.Error("an update that omitted cover_item_id cleared it; absent must mean untouched")
	}
	if got.Name != name {
		t.Errorf("name = %q, want %q - the unrelated field did not apply", got.Name, name)
	}

	// Explicit empty: clear it.
	empty := ""
	got, err = eng.UpdateCollection(ctx, owner.ID, colID, nil, nil, &empty)
	if err != nil {
		t.Fatalf("clear cover: %v", err)
	}
	if got.CoverItemID != nil {
		t.Errorf("cover_item_id = %v after an explicit clear, want nil", *got.CoverItemID)
	}
}
