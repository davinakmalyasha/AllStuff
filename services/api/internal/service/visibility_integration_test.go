//go:build !race

package service_test

import (
	"context"
	"errors"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/service"
	"bizverse/api/internal/testutil"
)

// Public visibility is enforced ONE LAYER ABOVE the repository, and that is
// worth a test in its own right.
//
// The repository's GetBySlug filters only `deleted_at IS NULL`. It does NOT
// filter status, so on its own it will happily return a suspended or closed
// listing — which is correct for an internal accessor, because the owner
// dashboard and the admin tools need to read exactly those rows.
//
// The whitelist lives in service.Businesses.GetPublic, and the comment there
// records why it exists:
//
//	"Public pages whitelist visible statuses. Hiding only closed/draft left
//	 suspended/pending/rejected listings fully readable by direct URL, which
//	 defeats admin suspension (moderation bypass)."
//
// So this test lives at the service layer on purpose. An earlier version of it
// sat in the repo package and failed, which was the test being wrong about
// where the rule lives rather than the code being wrong about it — worth
// recording, because "the test failed" and "the layer is wrong" look identical
// until you find the whitelist.
func TestPublicBusinessVisibilityWhitelist(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")

	svc := service.NewBusinesses(h.Repos, nil)

	cases := []struct {
		status      domain.BusinessStatus
		wantVisible bool
		why         string
	}{
		{domain.BusinessVerified, true,
			"a verified listing is the product; it must be publicly readable"},
		{domain.BusinessPaused, true,
			"a paused listing stays reachable and renders 'temporarily closed', which is the whole point of pausing rather than closing"},
		{domain.BusinessSuspended, false,
			"a suspended listing must not be readable by direct URL, or admin suspension is bypassable by anyone with the link"},
		{domain.BusinessClosed, false,
			"a closed listing is permanently gone; the PRD specifies a 410"},
		{domain.BusinessDraft, false,
			"a draft is an owner's unfinished work and was never public"},
		{domain.BusinessPending, false,
			"a listing awaiting review must not be indexable or linkable before an admin has seen its documents"},
		{domain.BusinessRejected, false,
			"a rejected listing must not be readable while the owner may still be resubmitting or appealing"},
	}

	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			b := testutil.Business(t, h, testutil.BusinessOpts{
				Name: "Biz " + string(c.status), Owner: owner, Category: cat, Status: c.status,
			})

			// The repository, deliberately, does not filter. Asserting that
			// here documents the layering: the rule is in the service, and
			// moving it down would break the owner dashboard and admin views,
			// which need to read suspended and closed rows.
			raw, err := h.Repos.Businesses.GetBySlug(ctx, b.Slug)
			if err != nil {
				t.Fatalf("repo GetBySlug: %v", err)
			}
			if raw == nil {
				t.Fatalf("repo GetBySlug returned nothing for a %s listing; the raw accessor must read every live row", c.status)
			}

			got, err := svc.GetPublic(ctx, b.Slug)
			switch {
			case c.wantVisible && err != nil:
				t.Errorf("%s: service GetPublic returned %v, want the listing visible", c.why, err)
			case c.wantVisible && got == nil:
				t.Errorf("%s: service GetPublic returned nothing, want the listing visible", c.why)
			case c.wantVisible && got.ID != b.ID:
				t.Errorf("%s: service GetPublic returned %s, want %s", c.why, got.ID, b.ID)
			case !c.wantVisible && err == nil:
				t.Errorf("%s: service GetPublic returned %s, want not-found. %s",
					c.why, got.ID, c.why)
			case !c.wantVisible && !errors.Is(err, domain.ErrNotFound):
				t.Errorf("%s: want domain.ErrNotFound so the handler renders a 404, got %v", c.why, err)
			}
		})
	}
}

// TestOwnerCanStillReadTheirOwnHiddenListing is the other half of the layering
// decision, and the reason the whitelist lives in the service rather than the
// repository.
//
// Suspending or closing a business removes it from the PUBLIC directory. It must
// not remove the owner from their own listing, or "your business is suspended
// and here is why, resubmit here" becomes an unreachable page — which is the
// exact state an owner in trouble is least able to work around.
func TestOwnerCanStillReadTheirOwnHiddenListing(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	stranger := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	svc := service.NewBusinesses(h.Repos, nil)

	for _, status := range []domain.BusinessStatus{domain.BusinessSuspended, domain.BusinessClosed} {
		b := testutil.Business(t, h, testutil.BusinessOpts{
			Name: "Hidden " + string(status), Owner: owner, Category: cat, Status: status,
		})

		got, err := h.Repos.Businesses.GetByID(ctx, b.ID)
		if err != nil {
			t.Fatalf("status %s: the owner must still be able to read their own row by id: %v", status, err)
		}
		if got.ID != b.ID {
			t.Fatalf("status %s: got %s want %s", status, got.ID, b.ID)
		}

		if _, err := svc.GetPublic(ctx, b.Slug); err == nil {
			t.Errorf("status %s: the public accessor must not expose a %s listing", status, status)
		}

		can, err := h.Repos.Businesses.CanManageBusiness(ctx, stranger.ID, b.ID)
		if err != nil {
			t.Fatalf("CanManageBusiness: %v", err)
		}
		if can {
			t.Errorf("status %s: an unrelated user may manage a %s listing", status, status)
		}
	}
}

// TestSlugRotationIsAllowedExactlyOnce covers the one-time rename budget.
//
// PRD §8.2: the slug is immutable after creation, and the owner may request ONE
// change. ChangeSlugOnce reports false when the budget is already spent, which is
// how a concurrent double-submit loses the race deterministically instead of
// both succeeding.
func TestSlugRotationIsAllowedExactlyOnce(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	b := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner, Category: cat})

	ok, err := h.Repos.Businesses.ChangeSlugOnce(ctx, b.ID, "a-brand-new-slug")
	if err != nil {
		t.Fatalf("first rotation: %v", err)
	}
	if !ok {
		t.Fatal("the first slug change must be allowed")
	}

	ok, err = h.Repos.Businesses.ChangeSlugOnce(ctx, b.ID, "and-another-one")
	if err != nil {
		t.Fatalf("second rotation: %v", err)
	}
	if ok {
		t.Error("a second slug change was allowed; the one-time rename budget is not enforced")
	}

	got, err := h.Repos.Businesses.GetBySlug(ctx, "a-brand-new-slug")
	if err != nil {
		t.Fatalf("GetBySlug after rotation: %v", err)
	}
	if got == nil || got.ID != b.ID {
		t.Errorf("the rotated slug does not resolve to the business")
	}
	if taken, err := h.Repos.Businesses.SlugTaken(ctx, "and-another-one", ""); err != nil {
		t.Fatal(err)
	} else if taken {
		t.Error("the rejected slug is reported as taken; nothing claimed it")
	}
}

// TestCategoryCountsDoNotContradictTheTree pins a bug the service layer already
// fixed, kept here because it is the same shape as the visibility rule: two
// readers of the same data disagreeing, with neither looking wrong.
//
// Categories.GetBySlug did not select the verified-business count while
// ListWithCounts did, so a category page rendered "0 verified businesses" under
// a heading while the sidebar said 1 for the same category. The fix prefers the
// counted copy from the tree.
func TestCategoryCountsDoNotContradictTheTree(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Bakery")
	testutil.Business(t, h, testutil.BusinessOpts{Owner: owner, Category: cat})
	// A draft in the same category must not be counted, or the page
	// over-reports.
	testutil.Business(t, h, testutil.BusinessOpts{
		Owner: owner, Category: cat, Status: domain.BusinessDraft, Lat: -6.7, Lng: 107.3,
	})

	flat, err := h.Repos.Categories.ListWithCounts(ctx)
	if err != nil {
		t.Fatalf("ListWithCounts: %v", err)
	}
	var treeCount int
	for _, c := range flat {
		if c.ID == cat.ID {
			treeCount = c.Count
		}
	}
	if treeCount != 1 {
		t.Errorf("tree count = %d, want 1 (the verified listing only; a draft is not a public business)", treeCount)
	}
}

// TestCloseRetiresTheListingAndReleasesItsSlug is the service-level regression
// test for migration 0032.
//
// It goes through service.Businesses.Close rather than the repository because
// the bug was a missing write in exactly this function: Close set status and
// cleared the published snapshot, but never set `deleted_at`, so the slug stayed
// reserved for the life of the row. Testing the repository would have passed
// against the unfixed code, because the repository never decided when a listing
// retires — the service does.
//
// It asserts the whole user-visible outcome rather than one field, because the
// plausible partial fixes differ:
//
//   - setting `deleted_at` but leaving GetByID filtering it: the slug is
//     released and the owner's own listing vanishes from their dashboard,
//     unmanageable, with no indication it existed.
//   - setting `deleted_at` but leaving GetBySlug unfiltered: the retired row
//     shadows whichever business later claims the freed slug.
func TestCloseRetiresTheListingAndReleasesItsSlug(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	svc := service.NewBusinesses(h.Repos, nil)

	const slug = "senja-roasters"
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Senja Roasters", Owner: owner, Category: cat, Slug: slug,
		Status: domain.BusinessVerified,
	})

	closed, err := svc.Close(ctx, owner.ID, b.ID)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.Status != domain.BusinessClosed {
		t.Errorf("status = %s, want closed", closed.Status)
	}

	// The slug is free to the API and free to the database. Asserting only one
	// of those is how the 0029 bug survived: SlugTaken said "taken" and the
	// index agreed by accident, which masked the defect.
	taken, err := h.Repos.Businesses.SlugTaken(ctx, slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if taken {
		t.Error("SlugTaken still reports a closed listing's slug as taken; Close must write deleted_at")
	}

	// And the retired listing is not publicly readable, by either accessor.
	if _, err := svc.GetPublic(ctx, slug); err == nil {
		t.Error("a retired listing is still publicly readable by slug")
	}
	// GetByID is the one accessor that must still see it, so the owner keeps
	// control of the record.
	if _, err := h.Repos.Businesses.GetByID(ctx, b.ID); err != nil {
		t.Errorf("the owner lost read access to the listing they just closed: %v", err)
	}
	can, err := h.Repos.Businesses.CanManageBusiness(ctx, owner.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !can {
		t.Error("the owner lost management of a listing they closed; closed is terminal, so this row " +
			"would be permanently unadministrable")
	}

	// The owner's dashboard still lists it. Dropping it here would make closing a
	// business an unrecoverable disappearance rather than a visible action.
	owned, err := svc.GetOwned(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range owned {
		if o.ID == b.ID {
			found = true
		}
	}
	if !found {
		t.Error("a closed listing vanished from its owner's dashboard; ListByOwner must include retired rows")
	}
}

// TestCreateCapsLiveBusinessesPerOwner covers the abuse control added with 0032.
//
// The cap exists because releasing a slug on close is not, on its own, enough:
// an attacker simply never closes anything. Each draft holds its slug
// indefinitely, `ChangeSlugOnce` then allows one rotation onto a name worth
// having, and `POST /businesses` had no per-user limit. The unique index stopped
// honest users from colliding while letting an attacker take exclusive ownership
// of any name they registered first.
//
// The second half is the one worth asserting: closing a listing must FREE the
// slot. A cap that counts every listing ever created would punish exactly the
// behaviour 0032 introduced, and owners would have no reason to ever retire a
// shop.
func TestCreateCapsLiveBusinessesPerOwner(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	svc := service.NewBusinesses(h.Repos, nil)

	for i := 0; i < service.MaxLiveBusinessesPerOwner; i++ {
		if _, err := svc.Create(ctx, owner.ID); err != nil {
			t.Fatalf("creating listing %d of %d must succeed: %v", i+1, service.MaxLiveBusinessesPerOwner, err)
		}
	}

	_, err := svc.Create(ctx, owner.ID)
	if err == nil {
		t.Fatalf("a %dth live listing was allowed; the cap does not bound slug squatting",
			service.MaxLiveBusinessesPerOwner+1)
	}
	// The refusal must carry the validation code, so the handler maps it to 400
	// and the user is told which limit they hit. A raw constraint error would be
	// a 500.
	//
	// Compared via FromError rather than errors.Is: WithField returns a CLONE
	// carrying a per-request field map, so it is a different pointer from the
	// ErrValidation sentinel and errors.Is (pointer equality) never matches.
	// FromError is what the HTTP layer itself uses to turn this into a response.
	if de := domain.FromError(err); de.Code != domain.ErrValidation.Code {
		t.Errorf("over-cap creation returned %q, want %q so the API can present it",
			de.Code, domain.ErrValidation.Code)
	}

	// A second user is unaffected: the cap is per user, not global.
	other := testutil.User(t, h)
	if _, err := svc.Create(ctx, other.ID); err != nil {
		t.Errorf("the cap leaked across users: %v", err)
	}

	// Closing one frees exactly one slot.
	owned, err := svc.GetOwned(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) == 0 {
		t.Fatal("no listings to close")
	}
	if _, err := svc.Close(ctx, owner.ID, owned[0].ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := svc.Create(ctx, owner.ID); err != nil {
		t.Errorf("closing a listing must free a slot, otherwise owners have no reason to ever close one: %v", err)
	}
	// And only one: the cap still holds after the free.
	if _, err := svc.Create(ctx, owner.ID); err == nil {
		t.Error("closing one listing freed more than one slot")
	}
}

// TestCategoryDeleteGuardSeesRetiredListings is the regression test for a bug
// that migration 0032 introduced into an unrelated feature.
//
// Categories.Delete is guarded by BusinessCount: if a category has any
// businesses attached, the caller must pass a target to reassign them to, rather
// than orphaning the rows. That guard's predicate was
// `WHERE category_id = $1 AND deleted_at IS NULL`.
//
// The filter was inert while nothing wrote `businesses.deleted_at`, so the guard
// worked by accident. 0032 made Close write it, and from that moment a category
// whose only listings were CLOSED looked empty. The guard passed, MoveBusinesses
// was never called, and the DELETE hit `businesses_category_id_fkey`, which is
// NO ACTION — so the operator got a raw 23503 instead of "This category has
// businesses. Pass move_to to reassign them". A 500 where a helpful validation
// error belongs, on the one path an admin uses to tidy up.
//
// The test exercises the service rather than either repository method, because
// the bug is not in one query: it is the pairing. Each query still filtered
// retired rows on its own, and the sequence broke between them.
func TestCategoryDeleteGuardSeesRetiredListings(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Poppy Seed Bakery")
	target := testutil.Category(t, h, "Cafe")
	svc := service.NewCategories(h.Repos)

	// One verified listing, which the owner then retires.
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Poppy Seed", Owner: owner, Category: cat, Status: domain.BusinessVerified,
	})
	if _, err := service.NewBusinesses(h.Repos, nil).Close(ctx, owner.ID, b.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The listing really is retired, so this cannot pass for the wrong reason: if
	// Close stopped setting deleted_at the guard would be correct again and the
	// assertions below would prove nothing.
	var deletedAt *string
	if err := h.QueryRow(t, `SELECT deleted_at::text FROM businesses WHERE id = $1`, b.ID).Scan(&deletedAt); err != nil {
		t.Fatal(err)
	}
	if deletedAt == nil {
		t.Fatal("the listing is not retired, so this test would pass for the wrong reason")
	}

	// A retired listing still holds category_id, so it must still block deletion.
	err := svc.Delete(ctx, cat.ID, nil)
	if err == nil {
		t.Fatal("a category holding only a retired listing was deleted; the listing is now orphaned")
	}
	if de := domain.FromError(err); de.Code != domain.ErrValidation.Code {
		t.Errorf("got %q (%v), want a validation error. A 23503 here means the guard let the call through "+
			"to the foreign key, which is the bug this test exists for.", de.Code, err)
	}

	// A forced move must carry the retired row with it, or the delete that
	// follows still fails on the foreign key.
	into := target.ID
	if err := svc.Delete(ctx, cat.ID, &into); err != nil {
		t.Fatalf("forced delete with a move target must succeed: %v", err)
	}
	moved, err := h.Repos.Businesses.GetByID(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved == nil {
		t.Fatal("the retired listing vanished")
	}
	if moved.CategoryID != target.ID {
		t.Errorf("retired listing category = %s, want %s; MoveBusinesses skipped it, so the category it "+
			"referenced is gone and the row points at nothing", moved.CategoryID, target.ID)
	}
}
