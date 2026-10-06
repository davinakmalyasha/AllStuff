//go:build !race

package repo_test

import (
	"context"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// These tests exist because migrations 0028-0031 changed the invariants this
// layer depends on, and because that layer had NO tests at all. Every assertion
// below corresponds to an invariant the SQL now enforces; where the two could
// disagree — and one of them already did — the test names which side is wrong.
// TestSlugUniquenessReflectsHowBusinessesAreActuallyRetired
//
// This test started life as a description of a bug. Its first version asserted
// the behaviour migration 0029 claimed to provide and failed on its first run,
// because the behaviour did not exist:
//
//	0029 replaced the table's `businesses_slug_key` UNIQUE constraint with
//	`CREATE UNIQUE INDEX businesses_slug_live ON businesses (slug)
//	 WHERE deleted_at IS NULL`
//
// on the premise that retiring a business sets `deleted_at`. Nothing ever wrote
// that column. `businesses` was the only table shaped like a soft delete that
// nothing soft-deleted — products, media, users and reviews all had real
// `deleted_at = now()` paths — so the partial predicate was always true and the
// index was a plain unique index wearing a predicate that protected nothing.
// A slug was reserved for the lifetime of the row.
//
// 0032 fixed it: Close now writes `deleted_at`, and closed rows are backfilled.
// This test is the regression test for that, and it deliberately asserts BOTH
// halves of the contract, because each half alone is a plausible but wrong
// implementation:
//
//   - Retiring releases the slug, so a new business can claim it. This is the
//     user-visible half and the one a reviewer will test for.
//   - A retired listing is invisible to the public. If a fix only did the first
//     half, the closed shop's page would still resolve and two businesses would
//     serve the same URL.
//
// It also pins the asymmetry that makes releasing safe: a retired row KEEPS its
// old slug string, so once a new business claims that slug two rows carry it,
// and only the live one may resolve. Asserting the retired row is not returned
// by GetBySlug is what stops the old shop shadowing the new one.
func TestSlugUniquenessReflectsHowBusinessesAreActuallyRetired(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")

	const slug = "shared-slug"
	first := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "First", Owner: owner, Category: cat, Slug: slug,
	})

	// Two LIVE businesses may never share a slug. This is the invariant the
	// index exists for, and it must survive the change to a real predicate.
	second := &domain.Business{
		ID: util.NewUUID(), Name: "Second", Slug: slug,
		Description: "a description long enough to clear any minimum length a listing must satisfy",
		City:        "Jakarta", Country: "Indonesia", Address: "2 Test Street",
		Lat: -6.3, Lng: 106.9, Timezone: "Asia/Jakarta", Currency: "USD",
		Hours: testutil.DefaultHours(), Status: domain.BusinessVerified,
		Tags: []string{}, Gallery: []string{}, Amenities: []string{},
		Contact: map[string]any{}, OwnerID: owner.ID,
	}
	err := h.ExpectError(t, "inserting a second live business with the same slug", func() error {
		return h.Repos.Businesses.Create(ctx, second, &cat.ID)
	})
	// Assert the SPECIFIC violation, not "some error": a bare non-nil check also
	// passes when the insert fails for an unrelated reason — a missing NOT
	// NULL, a dropped column, a typo in the fixture — and those are exactly the
	// failures that are hard to diagnose later.
	if !isUniqueViolation(err) {
		t.Errorf("expected a unique-violation (23505), got %v", err)
	}

	// Retire the first the way the product retires a business: status plus the
	// soft delete that 0032 introduced.
	if err := h.Repos.Businesses.SetStatus(ctx, first.ID, domain.BusinessClosed, map[string]any{
		"published_snapshot": nil, "last_published_at": nil, "deleted_at": nowUTC(),
	}); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The slug is now free, and the API agrees with the database. This is the
	// disagreement 0029 set out to remove, now removed for the right reason:
	// the closed row genuinely released the name.
	taken, err := h.Repos.Businesses.SlugTaken(ctx, slug, "")
	if err != nil {
		t.Fatalf("SlugTaken: %v", err)
	}
	if taken {
		t.Error("SlugTaken reports a retired business's slug as taken; Close must write deleted_at so the " +
			"partial index stops reserving it")
	}

	// And the insert genuinely succeeds now — the other half of the agreement.
	// An API that says "free" while the index refuses is the 500-becomes-409 bug.
	third := &domain.Business{
		ID: util.NewUUID(), Name: "Third", Slug: slug,
		Description: "a description long enough to clear any minimum length a listing must satisfy",
		City:        "Jakarta", Country: "Indonesia", Address: "3 Test Street",
		Lat: -6.5, Lng: 107.1, Timezone: "Asia/Jakarta", Currency: "USD",
		Hours: testutil.DefaultHours(), Status: domain.BusinessVerified,
		Tags: []string{}, Gallery: []string{}, Amenities: []string{},
		Contact: map[string]any{}, OwnerID: owner.ID,
	}
	if err := h.Repos.Businesses.Create(ctx, third, &cat.ID); err != nil {
		t.Fatalf("a slug released by retirement must be claimable: %v", err)
	}

	// The retired row still carries the slug string, so two rows now hold it.
	// GetBySlug must return the LIVE one, never the retired one — otherwise the
	// closed shop's page sits in front of the new shop's URL.
	bySlug, err := h.Repos.Businesses.GetBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if bySlug == nil {
		t.Fatal("the new owner of the released slug does not resolve")
	}
	if bySlug.ID != third.ID {
		t.Errorf("GetBySlug returned the retired listing %s, want the live listing %s", bySlug.ID, third.ID)
	}

	// GetByID deliberately still resolves the retired row, so the owner who
	// closed it can see what they did. This is the asymmetry that stops a close
	// from becoming an unmanageable black hole.
	retired, err := h.Repos.Businesses.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if retired == nil {
		t.Error("a retired listing must still be resolvable by id; the owner's dashboard would otherwise " +
			"lose it with no indication it existed")
	}

	// A retired listing is out of every public feed, even though GetByID can
	// still see it. status = 'closed' is on no public whitelist, so the status
	// gate holds independently of deleted_at as well.
	var stillPublished int
	if err := h.QueryRow(t, `
		SELECT count(*) FROM businesses
		WHERE id = $1 AND status = 'verified' AND deleted_at IS NULL`, first.ID).Scan(&stillPublished); err != nil {
		t.Fatal(err)
	}
	if stillPublished != 0 {
		t.Error("a retired listing is still selectable as a live verified listing")
	}
}

// TestUnownedBusinessIsRepresentable covers the invariant that made
// claim-an-existing-listing structurally unreachable, and the second half of
// that fix — which is the half 0029's comment does not mention.
//
// owner_id was `uuid NOT NULL`. service/claims.go detects an unowned listing by
// checking `if b.OwnerID != ""`, and a NOT NULL uuid column can never scan into
// "" — so 100% of existing-listing claims were rejected with "This listing
// already has an owner" before any other logic ran.
//
// 0029 made the column nullable. That was necessary and NOT sufficient: Create
// bound OwnerID directly, so an empty string asked Postgres to cast ” to uuid
// and it raised 22P02. The schema permitted a state the write path could not
// produce. This test is the regression guard for the write-path half; the
// schema half is covered by the migration replay test.
func TestUnownedBusinessIsRepresentable(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	cat := testutil.Category(t, h, "Cafe")

	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Unclaimed Listing", Category: cat, Owner: nil,
	})

	got, err := h.Repos.Businesses.GetByID(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.OwnerID != "" {
		t.Errorf("OwnerID = %q, want empty so claims.go can detect the listing as unowned", got.OwnerID)
	}

	// The column must be genuinely NULL, not '' — a string that round-trips as
	// empty would make the Go-side check pass while every SQL comparison against
	// owner_id behaves as a type error.
	var isNull bool
	if err := h.QueryRow(t,
		`SELECT owner_id IS NULL FROM businesses WHERE id = $1`, b.ID).Scan(&isNull); err != nil {
		t.Fatalf("read owner_id nullness: %v", err)
	}
	if !isNull {
		t.Error("owner_id is not SQL NULL; an empty-string value would satisfy the Go check while breaking every SQL comparison against the column")
	}

	// Nobody may manage an unowned business. CanManageBusiness is a POSITIVE
	// check, so an unowned listing is simply not manageable by anyone, which is
	// the correct outcome — but it is worth pinning, because making it a
	// negative check later would silently let any authenticated user edit any
	// unclaimed listing.
	owner := testutil.User(t, h)
	can, err := h.Repos.Businesses.CanManageBusiness(ctx, owner.ID, b.ID)
	if err != nil {
		t.Fatalf("CanManageBusiness: %v", err)
	}
	if can {
		t.Error("an arbitrary user may manage an unowned business; ownership must be a positive check")
	}

	// An owned business is unaffected by the NULL translation.
	owned := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Owned", Category: cat, Owner: owner,
	})
	if got, err := h.Repos.Businesses.GetByID(ctx, owned.ID); err != nil {
		t.Fatal(err)
	} else if got.OwnerID != owner.ID {
		t.Errorf("an owned business lost its owner: got %q want %q", got.OwnerID, owner.ID)
	}
}

// TestOwnerAndCoOwnerCanManage covers the co-owner permission model, which is
// the entire mechanism behind "one user can own several businesses with
// several people".
func TestOwnerAndCoOwnerCanManage(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	b := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner, Category: cat})

	coOwner := testutil.User(t, h)
	viewer := testutil.User(t, h)
	stranger := testutil.User(t, h)

	if err := h.Repos.Businesses.CreateInvite(ctx, owner.ID, b.ID, coOwner.Email, "co_owner", util.NewUUID()); err != nil {
		t.Fatalf("create co_owner invite: %v", err)
	}
	if err := h.Repos.Businesses.CreateInvite(ctx, owner.ID, b.ID, viewer.Email, "viewer", util.NewUUID()); err != nil {
		t.Fatalf("create viewer invite: %v", err)
	}
	// userID is passed because AcceptInvite binds the grant to the account; see the
	// repo method and migration 0049.
	if err := h.Repos.Businesses.AcceptInvite(ctx, mustInviteID(t, h, b.ID, coOwner.Email), coOwner.ID); err != nil {
		t.Fatalf("accept co_owner invite: %v", err)
	}
	if err := h.Repos.Businesses.AcceptInvite(ctx, mustInviteID(t, h, b.ID, viewer.Email), viewer.ID); err != nil {
		t.Fatalf("accept viewer invite: %v", err)
	}

	cases := []struct {
		user string
		id   string
		want bool
		why  string
	}{
		{owner.ID, owner.ID, true, "the owner can always manage"},
		{coOwner.ID, coOwner.ID, true, "an accepted co_owner has full manage rights"},
		{viewer.ID, viewer.ID, false, "a viewer is read-only and must NOT gain manage rights"},
		{stranger.ID, stranger.ID, false, "an unrelated user cannot manage"},
	}
	for _, c := range cases {
		got, err := h.Repos.Businesses.CanManageBusiness(ctx, c.user, b.ID)
		if err != nil {
			t.Fatalf("CanManageBusiness(%s): %v", c.why, err)
		}
		if got != c.want {
			t.Errorf("%s: CanManageBusiness = %v, want %v", c.why, got, c.want)
		}
	}

	// The viewer distinction is the one worth being strict about: if `viewer`
	// were ever accepted as `co_owner`, every read-only collaborator would
	// silently become a full editor, and no test would notice.
	viewable, err := h.Repos.Businesses.IsBusinessViewer(ctx, viewer.ID, b.ID)
	if err != nil {
		t.Fatalf("IsBusinessViewer: %v", err)
	}
	if !viewable {
		t.Error("a viewer must be able to see analytics; IsBusinessViewer returned false")
	}
	if _, err := h.Repos.Businesses.IsBusinessViewer(ctx, stranger.ID, b.ID); err != nil {
		t.Fatalf("IsBusinessViewer(stranger): %v", err)
	}
}

// TestSlugTakenExcludeID covers the exclude branch, which the update path relies
// on: saving a business without changing its slug must not report its own slug
// as taken.
func TestSlugTakenExcludeID(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	b := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner, Category: cat})

	if taken, err := h.Repos.Businesses.SlugTaken(ctx, b.Slug, ""); err != nil {
		t.Fatal(err)
	} else if !taken {
		t.Error("a live business's own slug must be reported as taken")
	}
	if taken, err := h.Repos.Businesses.SlugTaken(ctx, b.Slug, b.ID); err != nil {
		t.Fatal(err)
	} else if taken {
		t.Error("SlugTaken must ignore the business's own id, or every save without a slug change is rejected as a conflict")
	}
	// The empty-string exclude is the OTHER trap, and it is the one that already
	// bit this repository: `id <> $2::uuid` with $2 = '' raises 22P02 rather
	// than being false. The NULLIF in the query is what prevents it, so the
	// empty case above is not redundant with this one.
	if _, err := h.Repos.Businesses.SlugTaken(ctx, "never-used-slug", ""); err != nil {
		t.Fatalf("SlugTaken with an empty exclude must not error: %v", err)
	}
}

func mustInviteID(t *testing.T, h *testutil.H, businessID, email string) string {
	t.Helper()
	invites, err := h.Repos.Businesses.ListInvites(context.Background(), businessID)
	if err != nil {
		t.Fatalf("ListInvites: %v", err)
	}
	for _, i := range invites {
		if i.Email == email {
			return i.ID
		}
	}
	t.Fatalf("no invite found for %s", email)
	return ""
}
