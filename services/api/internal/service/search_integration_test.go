//go:build !race

package service_test

import (
	"context"
	"fmt"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/service"
	"bizverse/api/internal/testutil"
)

// service.Search had no tests, and it is the heaviest read path in the product:
// every directory page, the compare view, the leaderboards, the SSR pages and
// the nightly saved-search alert all go through Businesses.
//
// The two-phase rewrite and the N+1 fix were both real performance defects, and
// both are invisible to review after the fact. A test asserting "returns the
// right businesses" passes whether the code issues two queries or two hundred,
// and passes again if someone reintroduces per-row hydration in a refactor.
// These tests are mostly about the things a result-only assertion cannot see.
//
// Search needs a POOL-bound Repos: its queries go through Repos.pool, which
// NewForTx leaves nil. So these tests do not get the transaction rollback, and
// rely on the harness dropping the whole per-test database instead. Isolation is
// unaffected.

// searchFixture creates n verified businesses that all share the SAME created_at,
// in one category and city.
//
// Identical timestamps are not a contrived edge case: scripts/seed.sql inserts
// rows with generate_series inside a single statement, so `now()` is constant
// across all of them, and any bulk import or backfill behaves the same way. So
// the ties these tests create are ties a real database genuinely produces.
func searchFixture(t *testing.T, h *testutil.H, n int, cat *domain.Category) []string {
	t.Helper()
	owner := testutil.User(t, h)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		b := testutil.Business(t, h, testutil.BusinessOpts{
			Name: fmt.Sprintf("Fixture %02d", i), Owner: owner, Category: cat,
			Status: "verified", City: "Jakarta", Lat: -6.2, Lng: 106.8,
		})
		// One fixed instant for every row is the strictest form of the tie.
		h.ExecPool(t, `UPDATE businesses SET created_at = '2026-01-01 00:00:00+00' WHERE id = $1`, b.ID)
		ids = append(ids, b.ID)
	}
	return ids
}

// TestSearchPaginationIsDeterministicWhenSortKeysTie is the regression test for
// a total-order bug that affected every sort mode.
//
// No sort emitted a final tiebreaker. `newest` was literally
// `ORDER BY b.created_at DESC`; `rating` was `avg(rating) DESC NULLS LAST,
// b.created_at DESC`. When the sort key AND created_at both tie — which
// generated seed data and any bulk insert produces, because `now()` is constant
// within a statement — Postgres may return those rows in any order, and it
// chooses differently per execution because the plan differs with LIMIT/OFFSET.
//
// The consequence is not cosmetic. Paging 12 rows in pages of 4, a row can
// appear on page 1 and again on page 2 while another is never shown at all. A
// user scrolling a directory sees a business twice and assumes something else is
// missing.
//
// The test walks every page and requires the union to be exactly the full set
// with no duplicates, then repeats the whole walk: if ordering is plan-dependent,
// two runs over unchanged data disagree, and a test that only compared sets would
// not notice.
func TestSearchPaginationIsDeterministicWhenSortKeysTie(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	ctx := context.Background()
	cat := testutil.Category(t, h, "Bakery")
	search := service.NewSearch(h.Repos)

	const n, pageSize = 12, 4
	want := map[string]bool{}
	for _, id := range searchFixture(t, h, n, cat) {
		want[id] = true
	}

	for _, sort := range []string{"newest", "rating", "trending", "relevance"} {
		t.Run(sort, func(t *testing.T) {
			collect := func() (order []string, dupes []string) {
				for offset := 0; offset < n; offset += pageSize {
					page, _, err := search.Businesses(ctx, service.SearchParams{
						Sort: sort, Limit: pageSize, Offset: offset,
						Q: "Fixture", // so `relevance` takes a real code path
					})
					if err != nil {
						t.Fatalf("offset %d: %v", offset, err)
					}
					for _, b := range page {
						for _, s := range order {
							if s == b.ID {
								dupes = append(dupes, b.ID)
							}
						}
						order = append(order, b.ID)
					}
				}
				return order, dupes
			}

			first, dupes := collect()
			if len(dupes) > 0 {
				t.Errorf("paging returned %d duplicate row(s), e.g. %v. A row from an earlier page "+
					"reappeared, which means a different row was skipped: the ORDER BY has no final "+
					"tiebreaker, so rows tying on the sort key AND created_at come back in an arbitrary "+
					"order per page.", len(dupes), dupes)
			}
			if len(first) != n {
				t.Errorf("paging surfaced %d of %d businesses; %d were skipped entirely",
					len(first), n, n-len(first))
			}
			for _, id := range first {
				if !want[id] {
					t.Errorf("paging returned an unknown business %s", id)
				}
			}

			second, _ := collect()
			if len(first) == len(second) {
				for i := range first {
					if first[i] != second[i] {
						t.Errorf("two identical queries returned different orders (position %d: %s vs %s); "+
							"pagination is not stable", i, first[i], second[i])
						break
					}
				}
			}
		})
	}
}

// TestSearchHydratesThePageInTwoQueries guards the two-phase rewrite against
// regressing into an N+1.
//
// The rewrite exists because Phase 1 used to evaluate the like/save/recommend/
// review count subqueries per candidate row. The fix selects ids first and
// hydrates only the page. Nothing about the RESULT changes when that regresses —
// same businesses, same order, same counts — so a result-only suite cannot
// detect it.
//
// The count comes from a pgx tracer on the test's connection, so it measures
// what actually reached the server. Page size is varied, because the point is
// that the query count does not GROW with it: that growth is the N+1 signature.
func TestSearchHydratesThePageInTwoQueries(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	ctx := context.Background()
	cat := testutil.Category(t, h, "Cafe")
	search := service.NewSearch(h.Repos)
	searchFixture(t, h, 20, cat)

	for _, limit := range []int{5, 10, 20} {
		h.ResetQueryCount()
		page, _, err := search.Businesses(ctx, service.SearchParams{Sort: "newest", Limit: limit})
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if len(page) != limit {
			t.Fatalf("limit %d: got %d rows, want %d", limit, len(page), limit)
		}
		// Two = the id query plus the hydration query. The total-count query is
		// deliberately NOT requested (WithTotal is off by default), so a third
		// query here would mean the expensive count regressed onto every page.
		if got := h.QueryCount(); got != 2 {
			t.Errorf("limit %d: %d queries for %d rows. The two-phase shape is one id query plus one "+
				"hydration query, INDEPENDENT of page size; a count that grows with the page size is an "+
				"N+1 and no result assertion would catch it.", limit, got, limit)
		}
	}
}

// TestSearchTotalCountRespectsEveryFilter catches the classic two-phase bug.
//
// Phase 1 filters and pages; the count is a separate statement built from the
// same predicate. The failure mode is a count describing a WIDER result set than
// the page — "1-24 of 1,203" when 40 match. It is the kind of wrong that
// survives review because the number is plausible, and it is invisible to any
// test that only checks the returned rows.
func TestSearchTotalCountRespectsEveryFilter(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	ctx := context.Background()
	owner := testutil.User(t, h)
	cafe := testutil.Category(t, h, "Cafe")
	bakery := testutil.Category(t, h, "Bakery")
	search := service.NewSearch(h.Repos)

	// 4 Jakarta cafes, 3 Jakarta bakeries, 2 Bandung cafes.
	add := func(prefix string, cat *domain.Category, city string, lat, lng float64, n int) {
		for i := 0; i < n; i++ {
			testutil.Business(t, h, testutil.BusinessOpts{
				Name: fmt.Sprintf("%s %d", prefix, i), Owner: owner, Category: cat,
				Status: "verified", City: city, Lat: lat, Lng: lng,
			})
		}
	}
	add("Cafe", cafe, "Jakarta", -6.2, 106.8, 4)
	add("Bakery", bakery, "Jakarta", -6.2, 106.8, 3)
	add("Bandung", cafe, "Bandung", -6.9, 107.6, 2)

	cases := []struct {
		name string
		p    service.SearchParams
		want int
		why  string
	}{
		{"no filters", service.SearchParams{Sort: "newest", Limit: 50, WithTotal: true}, 9,
			"every verified, live business"},
		{"city", service.SearchParams{Sort: "newest", Limit: 50, WithTotal: true, City: "Jakarta"}, 7,
			"the city filter must reach the count, not just the page"},
		{"category", service.SearchParams{Sort: "newest", Limit: 50, WithTotal: true,
			CategoryIDs: []string{bakery.ID}}, 3,
			"a category filter must reach the count"},
		{"city AND category", service.SearchParams{Sort: "newest", Limit: 50, WithTotal: true,
			City: "Jakarta", CategoryIDs: []string{cafe.ID}}, 4,
			"combined filters must intersect in the count too"},
		{"total above one page", service.SearchParams{Sort: "newest", Limit: 2, WithTotal: true,
			City: "Jakarta", CategoryIDs: []string{bakery.ID}}, 3,
			"3 matches over a page of 2: the total must be 3, not the page size"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page, total, err := search.Businesses(ctx, c.p)
			if err != nil {
				t.Fatal(err)
			}
			if total != c.want {
				t.Errorf("total = %d, want %d (%s)", total, c.want, c.why)
			}
			if len(page) > c.p.Limit {
				t.Errorf("returned %d rows for Limit %d", len(page), c.p.Limit)
			}
		})
	}
}

// TestSearchTotalIsNotComputedUnlessAskedFor guards a deliberate performance
// decision that is easy to undo by accident.
//
// WithTotal re-runs the ENTIRE candidate predicate — the 5-way FTS OR, the
// per-row products EXISTS, a plpgsql open-now call per row, the avg(rating)
// subquery and the trend join — which costs more than fetching the page it
// counts. It used to run on every page except the last, i.e. essentially always,
// and once more per saved search in the nightly alert job.
//
// A negative total means "not computed" and must be rendered as such rather than
// as zero, so the test pins the negative value specifically: a change returning 0
// would make every pager read "0 results".
func TestSearchTotalIsNotComputedUnlessAskedFor(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	ctx := context.Background()
	cat := testutil.Category(t, h, "Cafe")
	search := service.NewSearch(h.Repos)
	searchFixture(t, h, 12, cat)

	// Not the last page, and no WithTotal.
	_, total, err := search.Businesses(ctx, service.SearchParams{Sort: "newest", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if total >= 0 {
		t.Errorf("total = %d; without WithTotal this must be negative ('not computed'), not 0 and not a "+
			"real count", total)
	}

	// The last page can compute the total for free, so it is exact.
	_, total, err = search.Businesses(ctx, service.SearchParams{Sort: "newest", Limit: 5, Offset: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 12 {
		t.Errorf("last-page total = %d, want 12; offset + rows returned is exact and costs nothing", total)
	}
}
