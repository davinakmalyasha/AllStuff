//go:build !race

package service

import (
	"context"
	"errors"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// analytics.ForBusiness had no coverage - 51 statements. It is the dashboard the
// owner reads, and it carries a three-way authorisation rule (owner, co-owner,
// read-only viewer) that is easy to get wrong.

func newTestAnalytics(t *testing.T) (*Analytics, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	return NewAnalytics(h.Repos), h
}

func ownerBusiness(t *testing.T, h *testutil.H) (*domain.Business, *domain.User) {
	t.Helper()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Analytics Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	return b, owner
}

func addView(t *testing.T, h *testutil.H, userID, businessID string) {
	t.Helper()
	h.Exec(t, `INSERT INTO engagement_events (user_id, target_type, target_id, signal, weight, dedupe_key)
		VALUES ($1, 'business', $2, 'view', 1, $3)`, userID, businessID, util.NewUUID())
}

func addBusinessLike(t *testing.T, h *testutil.H, userID, businessID string) {
	t.Helper()
	h.Exec(t, `INSERT INTO likes (id, user_id, target_type, target_id)
		VALUES ($1, $2, 'business', $3)`, util.NewUUID(), userID, businessID)
}

func addProductLike(t *testing.T, h *testutil.H, userID, productID string) {
	t.Helper()
	h.Exec(t, `INSERT INTO likes (id, user_id, target_type, target_id)
		VALUES ($1, $2, 'product', $3)`, util.NewUUID(), userID, productID)
}

func addProductView(t *testing.T, h *testutil.H, userID, productID string) {
	t.Helper()
	h.Exec(t, `INSERT INTO engagement_events (user_id, target_type, target_id, signal, weight, dedupe_key)
		VALUES ($1, 'product', $2, 'view', 1, $3)`, userID, productID, util.NewUUID())
}

func inviteAccepted(t *testing.T, h *testutil.H, businessID, inviterID, inviteeID, role string) {
	t.Helper()
	h.Exec(t, `INSERT INTO business_invites (id, business_id, email, role, token, invited_by, accepted_at, accepted_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7)`,
		util.NewUUID(), businessID, inviteeID+"@example.test", role, util.NewUUID(), inviterID, inviteeID)
}

func TestAnalyticsForBusinessRequiresAccess(t *testing.T) {
	svc, h := newTestAnalytics(t)
	ctx := context.Background()
	biz, owner := ownerBusiness(t, h)

	// The owner sees their own numbers.
	if _, err := svc.ForBusiness(ctx, owner.ID, biz.ID, "7d"); err != nil {
		t.Fatalf("owner: %v", err)
	}

	// A stranger does not - this is the gate the whole dashboard relies on.
	stranger := testutil.User(t, h)
	if _, err := svc.ForBusiness(ctx, stranger.ID, biz.ID, "7d"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger: err = %v, want ErrForbidden", err)
	}

	// An accepted viewer is allowed read-only access (PRD §5.9.3).
	viewer := testutil.User(t, h)
	inviteAccepted(t, h, biz.ID, owner.ID, viewer.ID, "viewer")
	if _, err := svc.ForBusiness(ctx, viewer.ID, biz.ID, "7d"); err != nil {
		t.Errorf("viewer: %v", err)
	}

	// So is an accepted co-owner.
	co := testutil.User(t, h)
	inviteAccepted(t, h, biz.ID, owner.ID, co.ID, "co_owner")
	if _, err := svc.ForBusiness(ctx, co.ID, biz.ID, "7d"); err != nil {
		t.Errorf("co-owner: %v", err)
	}

	// A REVOKED co-owner is not.
	revoked := testutil.User(t, h)
	inviteAccepted(t, h, biz.ID, owner.ID, revoked.ID, "co_owner")
	h.Exec(t, `UPDATE business_invites SET revoked_at=now() WHERE accepted_user_id=$1`, revoked.ID)
	if _, err := svc.ForBusiness(ctx, revoked.ID, biz.ID, "7d"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("revoked co-owner: err = %v, want ErrForbidden", err)
	}

	if _, err := svc.ForBusiness(ctx, owner.ID, util.NewUUID(), "7d"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown business: err = %v, want ErrNotFound", err)
	}
}

func TestAnalyticsForBusinessCountsAndPeriod(t *testing.T) {
	svc, h := newTestAnalytics(t)
	ctx := context.Background()
	biz, owner := ownerBusiness(t, h)

	rater := testutil.User(t, h)
	addView(t, h, rater.ID, biz.ID)
	addView(t, h, owner.ID, biz.ID)
	addBusinessLike(t, h, rater.ID, biz.ID)
	testutil.Review(t, h, biz.ID, rater.ID, 4)

	got, err := svc.ForBusiness(ctx, owner.ID, biz.ID, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Period != "7d" {
		t.Errorf("Period = %q, want it echoed", got.Period)
	}
	if got.Views != 2 {
		t.Errorf("Views = %d, want 2", got.Views)
	}
	if got.Likes != 1 {
		t.Errorf("Likes = %d, want 1", got.Likes)
	}
	if got.Reviews != 1 {
		t.Errorf("Reviews = %d, want 1", got.Reviews)
	}
	if got.RatingAvg == nil || *got.RatingAvg != 4 {
		t.Errorf("RatingAvg = %v, want 4", got.RatingAvg)
	}
	// The series is filled to the period's length even when only one day has
	// data, so the chart never renders a short axis.
	if len(got.Series) != 7 {
		t.Errorf("7d series length = %d, want 7", len(got.Series))
	}

	// The default period is 30 days and "all" is a year.
	for period, want := range map[string]int{"": 30, "30d": 30, "all": 365} {
		got, err := svc.ForBusiness(ctx, owner.ID, biz.ID, period)
		if err != nil {
			t.Fatalf("period %q: %v", period, err)
		}
		if len(got.Series) != want {
			t.Errorf("period %q: series length = %d, want %d", period, len(got.Series), want)
		}
	}
}

func TestAnalyticsForBusinessTopProducts(t *testing.T) {
	svc, h := newTestAnalytics(t)
	ctx := context.Background()
	biz, owner := ownerBusiness(t, h)
	rater := testutil.User(t, h)

	// Six products, deliberately ranked by engagement rather than insertion
	// order, because the function sorts a slice in place.
	names := []string{"p1", "p2", "p3", "p4", "p5", "p6"}
	products := make([]*domain.Product, len(names))
	for i, n := range names {
		products[i] = testutil.Product(t, h, biz.ID, n)
	}
	addProductView(t, h, rater.ID, products[3].ID) // 1 point
	addProductView(t, h, rater.ID, products[4].ID)
	addProductView(t, h, rater.ID, products[4].ID)
	addProductLike(t, h, rater.ID, products[5].ID) // 3 points (likes weigh 3)
	addProductView(t, h, rater.ID, products[5].ID) // + 1

	got, err := svc.ForBusiness(ctx, owner.ID, biz.ID, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TopProducts) != 5 {
		t.Fatalf("TopProducts = %d, want a cap of 5", len(got.TopProducts))
	}
	// p6 scored 4, p5 scored 2, p4 scored 1; everything else scored zero.
	if got.TopProducts[0].ProductID != products[5].ID {
		t.Errorf("first = %s, want the most engaged product", got.TopProducts[0].Name)
	}
	if got.TopProducts[0].Name != "p6" {
		t.Errorf("names are not carried through: %q", got.TopProducts[0].Name)
	}
	if got.TopProducts[1].ProductID != products[4].ID {
		t.Errorf("second = %q, want p5", got.TopProducts[1].Name)
	}
	// Every product appears, including the ones with no engagement, so the
	// dashboard can show a full list rather than only the winners.
	seen := map[string]bool{}
	for _, p := range got.TopProducts {
		seen[p.ProductID] = true
	}
	if len(seen) != 5 {
		t.Errorf("distinct products = %d, want 5", len(seen))
	}
}

func TestAnalyticsForBusinessLeaderboard(t *testing.T) {
	svc, h := newTestAnalytics(t)
	ctx := context.Background()
	biz, owner := ownerBusiness(t, h)

	got, err := svc.ForBusiness(ctx, owner.ID, biz.ID, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Leaderboard != nil {
		t.Error("a leaderboard was reported before any snapshot exists")
	}

	h.Exec(t, `INSERT INTO trend_snapshots (id, period, business_id, score, rank_global, rank_category)
		VALUES ($1, '7d', $2, 10, 3, NULL)`, util.NewUUID(), biz.ID)
	h.Exec(t, `INSERT INTO trend_snapshots (id, period, business_id, score, rank_global, rank_category)
		VALUES ($1, '24h', $2, 10, NULL, 2)`, util.NewUUID(), biz.ID)

	got, err = svc.ForBusiness(ctx, owner.ID, biz.ID, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Leaderboard == nil {
		t.Fatal("no leaderboard despite snapshots")
	}
	if got.Leaderboard.Global == nil || *got.Leaderboard.Global != 3 {
		t.Errorf("global rank = %v, want 3", got.Leaderboard.Global)
	}
	if got.Leaderboard.Category == nil || *got.Leaderboard.Category != 2 {
		t.Errorf("category rank = %v, want 2", got.Leaderboard.Category)
	}
}
