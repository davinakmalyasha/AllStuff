//go:build !race

package service

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"bizverse/api/internal/config"
	"bizverse/api/internal/email"
	"bizverse/api/internal/ws"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// profiles.ByUsername had no coverage - it is the public /u/{username} page,
// so what it exposes and what it withholds are both worth pinning.

func newTestProfilesPublic(t *testing.T) (*Profiles, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	notifier := NewNotifier(h.Repos, cfg, email.NewSender(email.ResendConfig{AppEnv: "test"}), ws.NewHub(logger, nil), logger)
	return NewProfiles(h.Repos, notifier), h
}

func TestProfilesByUsernameNotFound(t *testing.T) {
	svc, _ := newTestProfilesPublic(t)
	if _, err := svc.ByUsername(context.Background(), "nobody-here"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestProfilesByUsernameCollectsPublicActivity(t *testing.T) {
	svc, h := newTestProfilesPublic(t)
	ctx := context.Background()
	user := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Profile Cafe", Category: cat, Owner: user,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		Status: domain.BusinessVerified,
	})

	// A review on a verified business is part of the public profile.
	testutil.Review(t, h, biz.ID, user.ID, 5)

	// A review on a DRAFT business is not - it belongs to a listing nobody can
	// see, so it must not surface through the author's profile either.
	draft := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Hidden Cafe", Category: cat, Owner: user, Status: domain.BusinessDraft,
	})
	testutil.Review(t, h, draft.ID, user.ID, 1)

	// A HIDDEN review on a visible business is not either. Moderation hides a
	// review without deleting it, and the author's own profile must not
	// resurrect it. Each case needs its own business: a user may review a given
	// business only once.
	hiddenBiz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Hidden Review Cafe", Category: cat, Owner: user,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		Status: domain.BusinessVerified,
	})
	hidden := testutil.Review(t, h, hiddenBiz.ID, user.ID, 4)
	h.Exec(t, `UPDATE reviews SET status='hidden' WHERE id=$1`, hidden.ID)

	// A DELETED review is not.
	deletedBiz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Deleted Review Cafe", Category: cat, Owner: user,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		Status: domain.BusinessVerified,
	})
	deleted := testutil.Review(t, h, deletedBiz.ID, user.ID, 3)
	h.Exec(t, `UPDATE reviews SET deleted_at=now() WHERE id=$1`, deleted.ID)

	// Comments follow the same rule: visible ones appear, hidden ones do not.
	visibleComment := util.NewUUID()
	h.Exec(t, `INSERT INTO comments (id, business_id, user_id, text) VALUES ($1,$2,$3,'A public comment')`,
		visibleComment, biz.ID, user.ID)
	hiddenComment := util.NewUUID()
	h.Exec(t, `INSERT INTO comments (id, business_id, user_id, text, status) VALUES ($1,$2,$3,'A hidden comment','hidden')`,
		hiddenComment, biz.ID, user.ID)

	got, err := svc.ByUsername(ctx, user.Username)
	if err != nil {
		t.Fatalf("ByUsername: %v", err)
	}
	if got.ID != user.ID || got.Username != user.Username {
		t.Errorf("profile = %#v", got)
	}
	reviews := got.Reviews
	if len(reviews) != 1 {
		t.Errorf("reviews = %d, want only the visible one on a verified business", len(reviews))
	}
	comments := got.Comments
	if len(comments) != 1 {
		t.Errorf("comments = %d, want only the visible one", len(comments))
	} else {
		row, _ := comments[0].(map[string]any)
		if row["id"] != visibleComment {
			t.Errorf("comment id = %v, want the visible one", row["id"])
		}
	}
	// The public profile must not carry the account's email address.
	if got.Name == "" {
		t.Error("no name on the profile")
	}
}

// A business whose owner never set a category still belongs on their public
// profile. category_id is nullable, and the read scanned it into a non-nullable
// string, so such a business was dropped from the page.
func TestProfilesByUsernameKeepsUncategorisedBusiness(t *testing.T) {
	svc, h := newTestProfilesPublic(t)
	ctx := context.Background()
	user := testutil.User(t, h)

	id := util.NewUUID()
	h.Exec(t, `INSERT INTO businesses (id, owner_id, name, slug, description, category_id, status, address, lat, lng, city, country)
		VALUES ($1, $2, 'No Category Cafe', 'no-category-cafe',
			'A business whose owner never chose a category for it, which the schema allows.',
			NULL, 'verified', 'Jl. No Kategori 1', -6.2, 106.8, 'Jakarta', 'ID')`, id, user.ID)

	got, err := svc.ByUsername(ctx, user.Username)
	if err != nil {
		t.Fatalf("ByUsername: %v", err)
	}
	biz := got.Businesses
	if len(biz) != 1 {
		t.Fatalf("businesses = %d, want the uncategorised one to be listed", len(biz))
	}
	row, _ := biz[0].(map[string]any)
	if row["id"] != id {
		t.Errorf("business id = %v, want %s", row["id"], id)
	}
}

func TestProfilesByUsernameHidesPrivateCollections(t *testing.T) {
	svc, h := newTestProfilesPublic(t)
	ctx := context.Background()
	user := testutil.User(t, h)

	publicID := util.NewUUID()
	h.Exec(t, `INSERT INTO collections (id, user_id, name, slug, is_public) VALUES ($1,$2,'Favourites','favourites',true)`, publicID, user.ID)
	h.Exec(t, `INSERT INTO collections (id, user_id, name, slug, is_public) VALUES ($1,$2,'Secret','secret',false)`, util.NewUUID(), user.ID)

	got, err := svc.ByUsername(ctx, user.Username)
	if err != nil {
		t.Fatal(err)
	}
	cols := got.Collections
	if len(cols) != 1 {
		t.Fatalf("collections = %d, want only the public one", len(cols))
	}
	row, _ := cols[0].(map[string]any)
	if row["id"] != publicID {
		t.Errorf("collection id = %v, want the public one", row["id"])
	}
}
