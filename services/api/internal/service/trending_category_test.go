//go:build !race

package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
	"bizverse/api/internal/ws"
)

// CategoryLeaderboard had no coverage. It had already shipped with one defect -
// max(taken_at) is NULL before the first snapshot exists, and scanning that into
// a time.Time made every category page a 500 - so the empty case is the one
// worth protecting first.

func newTestTrending(t *testing.T) (*Trending, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	notifier := NewNotifier(h.Repos, cfg, email.NewSender(email.ResendConfig{AppEnv: "test"}), ws.NewHub(logger, nil), logger)
	return NewTrending(h.Repos, notifier), h
}

func TestCategoryLeaderboardWithoutSnapshots(t *testing.T) {
	svc, h := newTestTrending(t)
	cat := testutil.Category(t, h, "Cafe")

	got, takenAt, err := svc.CategoryLeaderboard(context.Background(), cat.ID, 10)
	if err != nil {
		t.Fatalf("a leaderboard with no snapshots must be empty, not an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("entries = %d, want 0", len(got))
	}
	if !takenAt.IsZero() {
		t.Errorf("takenAt = %v, want the zero time", takenAt)
	}
}

func TestCategoryLeaderboardRanksAndMergesRising(t *testing.T) {
	svc, h := newTestTrending(t)
	ctx := context.Background()
	cat := testutil.Category(t, h, "Cafe")
	otherCat := testutil.Category(t, h, "Bookshop")
	owner := testutil.User(t, h)

	mk := func(name string, c *domain.Category) *domain.Business {
		return testutil.Business(t, h, testutil.BusinessOpts{
			Name: name, Category: c, Owner: owner, Status: domain.BusinessVerified,
			City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		})
	}
	top := mk("Top Cafe", cat)
	second := mk("Second Cafe", cat)
	risingBiz := mk("Rising Cafe", cat)
	outsider := mk("Other Bookshop", otherCat)

	takenAt := time.Now().UTC().Truncate(time.Second)
	snap := func(b *domain.Business, score, velocity float64, rising bool, rank any) {
		h.Exec(t, `INSERT INTO trend_snapshots (id, period, business_id, score, velocity, is_rising, rank_category, taken_at)
			VALUES ($1,'24h',$2,$3,$4,$5,$6,$7)`,
			util.NewUUID(), b.ID, score, velocity, rising, rank, takenAt)
	}
	// The top entry is ALSO rising, so it appears in both queries and the dedupe
	// is what keeps it from being listed twice.
	snap(top, 30, 10, true, 1)
	snap(second, 20, 0, false, 2)
	// Third by score, but rising - it is merged in from the second query even
	// though a limit of 2 would otherwise exclude it.
	snap(risingBiz, 10, 5, true, 3)
	snap(outsider, 99, 0, false, 1)

	got, gotTakenAt, err := svc.CategoryLeaderboard(ctx, cat.ID, 2)
	if err != nil {
		t.Fatalf("CategoryLeaderboard: %v", err)
	}
	if !gotTakenAt.Equal(takenAt) {
		t.Errorf("takenAt = %v, want %v", gotTakenAt, takenAt)
	}
	// The other category's higher-scoring business must not appear.
	for _, e := range got {
		if e.ID == outsider.ID {
			t.Error("an entry from another category was included")
		}
	}
	if len(got) != 3 {
		t.Fatalf("entries = %d, want the two top plus the rising one", len(got))
	}
	if got[0].ID != top.ID || got[1].ID != second.ID {
		t.Errorf("score order = %s, %s; want top then second", got[0].Name, got[1].Name)
	}
	if got[2].ID != risingBiz.ID {
		t.Errorf("third = %s, want the rising entry appended", got[2].Name)
	}
	if got[0].RankCategory != 1 {
		t.Errorf("rank = %d, want the stored rank", got[0].RankCategory)
	}
	// Deduped: the rising entry appears once even though both queries match it.
	seen := map[string]int{}
	for _, e := range got {
		seen[e.ID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("entry %s appears %d times", id, n)
		}
	}
}

// rank_category is nullable, and the trending job leaves it NULL between the
// INSERT and the later UPDATE that ranks the batch. Scanning that into an int
// failed the whole page, so it is coalesced.
func TestCategoryLeaderboardToleratesNullRank(t *testing.T) {
	svc, h := newTestTrending(t)
	ctx := context.Background()
	cat := testutil.Category(t, h, "Cafe")
	owner := testutil.User(t, h)
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Unranked Cafe", Category: cat, Owner: owner, Status: domain.BusinessVerified,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	h.Exec(t, `INSERT INTO trend_snapshots (id, period, business_id, score, taken_at)
		VALUES ($1,'24h',$2,5,now())`, util.NewUUID(), b.ID)

	got, _, err := svc.CategoryLeaderboard(ctx, cat.ID, 10)
	if err != nil {
		t.Fatalf("a snapshot with no rank broke the page: %v", err)
	}
	if len(got) != 1 || got[0].RankCategory != 0 {
		t.Errorf("entries = %v, want one with rank 0", got)
	}
}
