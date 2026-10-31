//go:build !race

package service_test

import (
	"context"
	"errors"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/service"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// newTestEngagement builds the real service over the test's repos.
//
// The notifier is nil because SaveTo does not use it - it writes its analytics event
// through repos.Engagement.InsertEvent directly. Same shape as
// visibility_integration_test.go, which passes nil for the dependency it does not
// exercise.
func newTestEngagement(h *testutil.H) *service.Engagement {
	return service.NewEngagement(h.Repos, nil)
}

// The per-collection item cap from PRD 7.2 was not enforced.
//
// `CreateCollection` refuses the 51st collection, and the line that does it sits a few
// lines above `SaveTo` - which had no equivalent check. So collections were capped at
// 50 while each could grow without bound, and the 500-item limit existed only in the
// spec.
//
// It matters more after the read path gained pagination: capping the page size bounds
// what a client can PULL, not what a user can PUSH. Without the write cap, a
// collection is still unbounded - it is just slower to read.
//
// These use the service, not the repo, because the cap is a business rule rather than
// a storage one.

func collectionWithFiller(t *testing.T, h *testutil.H, ownerID string, filler int) string {
	t.Helper()
	colID := util.NewUUID()
	h.Exec(t, `INSERT INTO collections (id, user_id, name, slug)
	           VALUES ($1::uuid, $2::uuid, 'Capped', $3)`, colID, ownerID, "capped-"+colID[:8])

	// Filler rows go in directly. They need no real target because the count is what
	// the cap reads, and building 499 real businesses would make the test dominant.
	// The target the service actually saves is a real one.
	if filler > 0 {
		h.Exec(t, `INSERT INTO collection_items (id, collection_id, target_type, target_id)
		           SELECT gen_random_uuid(), $1::uuid, 'business', gen_random_uuid()
		             FROM generate_series(1, $2)`, colID, filler)
	}
	return colID
}

func TestSaveRefusesTheItemPastTheCap(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	eng := newTestEngagement(h)

	owner := testutil.User(t, h)
	extra := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	colID := collectionWithFiller(t, h, owner.ID, 500)

	err := eng.SaveTo(ctx, owner.ID, colID, "business", extra.ID, "")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("saving the 501st item: err = %v, want ErrValidation - the PRD's 500-item cap is not enforced", err)
	}
}

// The cap must NOT block an item that is already saved.
//
// AddItem is ON CONFLICT DO NOTHING, so re-saving is a no-op. Checking the count
// without first checking existence would tell a user sitting at exactly 500 that they
// cannot save something they already have - an error with no possible corrective
// action, since removing and re-adding is what they are already doing.
func TestSaveAtTheCapStillAllowsAnAlreadySavedItem(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	eng := newTestEngagement(h)

	owner := testutil.User(t, h)
	existing := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	colID := collectionWithFiller(t, h, owner.ID, 499)

	// Get the collection to exactly 500 with a REAL target.
	if err := eng.SaveTo(ctx, owner.ID, colID, "business", existing.ID, ""); err != nil {
		t.Fatalf("filling to the cap: %v", err)
	}
	var n int
	if err := h.QueryRow(t, `SELECT count(*) FROM collection_items WHERE collection_id = $1::uuid`, colID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 500 {
		t.Fatalf("fixture is wrong: collection holds %d items, want 500", n)
	}

	if err := eng.SaveTo(ctx, owner.ID, colID, "business", existing.ID, ""); err != nil {
		t.Errorf("re-saving an item already in a full collection: err = %v, want nil - it is a no-op, not an addition", err)
	}
}

// Below the cap, saving works normally - the guard must not be an off-by-one that
// refuses the 500th item itself.
func TestSaveAllowsTheFiveHundredthItem(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	eng := newTestEngagement(h)

	owner := testutil.User(t, h)
	last := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	colID := collectionWithFiller(t, h, owner.ID, 499)

	if err := eng.SaveTo(ctx, owner.ID, colID, "business", last.ID, ""); err != nil {
		t.Fatalf("saving the 500th item: err = %v, want nil - the cap is 500, not 499", err)
	}
}
