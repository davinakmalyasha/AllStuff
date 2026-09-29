//go:build !race

package repo_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"bizverse/api/internal/testutil"
)

// Migration 0033 exists because 0031 wrote the rule down and then did not apply
// it. 0031's own comment says:
//
//	"Every paginated list needs a total order, or a row can appear on two pages
//	 and another on none."
//
// and it built idx_reviews_business_recent as (business_id, created_at DESC,
// id DESC). ListReviews still ordered by `r.created_at DESC`, so the tiebreaker
// sat at the end of an index the query never asked to use. A total order the
// query does not request is not a total order.
//
// WHY A TEST CANNOT SIMPLY ASSERT THE ORDER
// -----------------------------------------
// The tempting test is "read the SQL and confirm id is in the ORDER BY". That
// passes and means nothing: it is a grep, and greps do not catch the next
// developer adding a list. The failure mode is behavioural — Postgres returns
// rows that tie on the sort key in whatever order the plan produces, and the
// plan changes with LIMIT/OFFSET — so the test has to page and look for the
// behavioural signature.
//
// THE SIGNATURE
// -------------
// With a non-total order, paging N rows in pages of k produces a union that is
// NOT the full set: some row appears on two pages, and a different row is
// silently never returned. Each individual page is a perfectly valid result for
// its own query, which is why nothing anywhere reports an error.
//
// The fixtures pin created_at to one fixed instant for every row, which is the
// strictest form of the tie and is what real data produces: scripts/seed.sql
// inserts rows with generate_series in a single statement, so `now()` is
// constant across all of them, and so is it for any bulk import or backfill.

// pageAll walks every page with LIMIT/OFFSET and returns the ids in the order
// they were served, plus any id served more than once.
func pageAll(t *testing.T, page func(limit, offset int) ([]string, error), n, pageSize int) (order, dupes []string) {
	t.Helper()
	seen := map[string]bool{}
	for offset := 0; offset < n; offset += pageSize {
		ids, err := page(pageSize, offset)
		if err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		for _, id := range ids {
			if seen[id] {
				dupes = append(dupes, id)
			}
			seen[id] = true
			order = append(order, id)
		}
	}
	return order, dupes
}

// assertTotalOrder is the shared assertion, so every list is held to the same
// standard and the failure message names the list.
func assertTotalOrder(t *testing.T, name string, order, dupes []string, n int) {
	t.Helper()
	if len(dupes) > 0 {
		t.Errorf("%s: %d row(s) were served on more than one page, e.g. %v. A different row was "+
			"therefore skipped: the ORDER BY has no final tiebreaker, so rows tying on the sort key "+
			"come back in an arbitrary order per page.", name, len(dupes), dupes)
	}
	if len(order) != n {
		t.Errorf("%s: paging surfaced %d of %d rows; %d were never returned", name, len(order), n, n-len(order))
	}
}

const tiedInstant = `UPDATE %s SET created_at = '2026-01-01 00:00:00+00' WHERE id = $1`

// TestListReviewsHasATotalOrder covers ListReviews across all three sorts.
//
// The default sort is the interesting one: idx_reviews_business_recent already
// had `id DESC`, so before 0033's query change the index and the query disagreed
// and the read was index-ordered without being total. The rating and helpful
// sorts never had a supporting index and already sorted, so the tiebreaker there
// is free — the test just confirms none of the three can skip a row.
func TestListReviewsHasATotalOrder(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Tied Reviews Cafe", Owner: owner, Category: cat, Status: "verified",
	})

	const n, pageSize = 9, 3
	for i := 0; i < n; i++ {
		// Distinct authors, so the reviews_unique_business partial index (one
		// review per business+user for product-less reviews) is satisfied.
		author := testutil.User(t, h)
		testutil.Review(t, h, biz.ID, author.ID, 4) // identical rating: ties on `highest` too
		h.Exec(t, fmt.Sprintf(tiedInstant, "reviews"), lastReviewID(t, h, biz.ID, author.ID))
	}

	for _, sort := range []string{"", "highest", "helpful"} {
		t.Run("sort="+sort, func(t *testing.T) {
			order, dupes := pageAll(t, func(limit, offset int) ([]string, error) {
				rows, err := h.Repos.Engagement.ListReviews(ctx, biz.ID, nil, sort, limit, offset, nil)
				if err != nil {
					return nil, err
				}
				ids := make([]string, 0, len(rows))
				for _, r := range rows {
					ids = append(ids, r.ID)
				}
				return ids, nil
			}, n, pageSize)
			assertTotalOrder(t, "ListReviews sort="+sort, order, dupes, n)
		})
	}
}

// TestListCommentsHasATotalOrder covers the thread on a business page.
//
// ListComments orders ASC and is served by a BACKWARD scan of
// idx_comments_recent, so the tiebreaker has to be `id ASC` to compose with the
// index — `id DESC` here would have silently cost the index-ordered plan while
// fixing the correctness, which is the trap 0033 exists to avoid.
func TestListCommentsHasATotalOrder(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Bakery")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Tied Comments Bakery", Owner: owner, Category: cat, Status: "verified",
	})

	const n, pageSize = 9, 3
	for i := 0; i < n; i++ {
		c, err := h.Repos.Engagement.CreateComment(ctx, biz.ID, owner.ID, "", fmt.Sprintf("Comment %d", i))
		if err != nil {
			t.Fatalf("comment %d: %v", i, err)
		}
		h.Exec(t, fmt.Sprintf(tiedInstant, "comments"), c.ID)
	}

	order, dupes := pageAll(t, func(limit, offset int) ([]string, error) {
		rows, err := h.Repos.Engagement.ListComments(ctx, biz.ID, limit, offset)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(rows))
		for _, c := range rows {
			ids = append(ids, c.ID)
		}
		return ids, nil
	}, n, pageSize)
	assertTotalOrder(t, "ListComments", order, dupes, n)
}

// TestListQuestionsHasATotalOrder covers the Q&A list, which is the one users
// most obviously page through by hand.
func TestListQuestionsHasATotalOrder(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Books")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Tied Questions Bookshop", Owner: owner, Category: cat, Status: "verified",
	})

	const n, pageSize = 9, 3
	for i := 0; i < n; i++ {
		q, err := h.Repos.Community.CreateQuestion(ctx, biz.ID, owner.ID, fmt.Sprintf("Question %d?", i))
		if err != nil {
			t.Fatalf("question %d: %v", i, err)
		}
		h.Exec(t, fmt.Sprintf(tiedInstant, "questions"), q.ID)
	}

	order, dupes := pageAll(t, func(limit, offset int) ([]string, error) {
		rows, err := h.Repos.Community.ListQuestions(ctx, biz.ID, limit, offset)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(rows))
		for _, q := range rows {
			ids = append(ids, q.ID)
		}
		return ids, nil
	}, n, pageSize)
	assertTotalOrder(t, "ListQuestions", order, dupes, n)
}

// TestListNotificationsHasATotalOrder covers the notification list, which is the
// highest-traffic paginated read in the product.
//
// Notifications tie more readily than anything else here: one user action can
// generate several notifications, and the weekly digest and saved-search alert
// jobs insert in batches.
func TestListNotificationsHasATotalOrder(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	user := testutil.User(t, h)

	const n, pageSize = 9, 3
	for i := 0; i < n; i++ {
		// product_review is one of the values the notifications_type_check CHECK
		// allows. The constraint is NOT VALID, which means it is not scanned but
		// IS enforced for new rows — so an invented type fails the insert and
		// aborts the test transaction with 23514, not with anything that names the
		// real problem.
		if _, err := h.Repos.Engagement.CreateNotification(ctx, user.ID, "product_review", map[string]any{"n": i}); err != nil {
			t.Fatalf("notification %d: %v", i, err)
		}
	}
	h.Exec(t, `UPDATE notifications SET created_at = '2026-01-01 00:00:00+00' WHERE user_id = $1`, user.ID)

	order, dupes := pageAll(t, func(limit, offset int) ([]string, error) {
		rows, err := h.Repos.Engagement.ListNotifications(ctx, user.ID, "product_review", limit, offset)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(rows))
		for _, x := range rows {
			ids = append(ids, x.ID)
		}
		return ids, nil
	}, n, pageSize)
	assertTotalOrder(t, "ListNotifications", order, dupes, n)
}

// TestMyReviewsHasATotalOrder covers "my reviews". This one filters only
// user_id, for which the sole index is idx_reviews_user (user_id), so the query
// already sorts and the tiebreaker is free — asserted here so a future index
// change cannot quietly reintroduce the skip.
func TestMyReviewsHasATotalOrder(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	author := testutil.User(t, h)
	cat := testutil.Category(t, h, "Deli")

	const n, pageSize = 9, 3
	// Several businesses, because one author may leave only one product-less
	// review per business.
	for i := 0; i < n; i++ {
		owner := testutil.User(t, h)
		biz := testutil.Business(t, h, testutil.BusinessOpts{
			Name: fmt.Sprintf("Deli %d", i), Owner: owner, Category: cat, Status: "verified",
		})
		testutil.Review(t, h, biz.ID, author.ID, 4)
	}
	h.Exec(t, `UPDATE reviews SET created_at = '2026-01-01 00:00:00+00' WHERE user_id = $1`, author.ID)

	order, dupes := pageAll(t, func(limit, offset int) ([]string, error) {
		rows, err := h.Repos.Engagement.MyReviews(ctx, author.ID, limit, offset)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids, nil
	}, n, pageSize)
	assertTotalOrder(t, "MyReviews", order, dupes, n)
}

// TestByStatusAdminQueueHasATotalOrder covers the admin verification queue.
//
// No index was added for this in 0033, because `status = ANY($1)` cannot yield a
// total order across a multi-value comparison — the query sorts either way, so an
// index would be storage never used for ordering. The test therefore also
// documents that this list is correct WITHOUT index support, which is the case a
// future optimiser might otherwise try to "fix" by adding one.
func TestByStatusAdminQueueHasATotalOrder(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")

	const n, pageSize = 9, 3
	for i := 0; i < n; i++ {
		b := testutil.Business(t, h, testutil.BusinessOpts{
			Name: fmt.Sprintf("Pending %d", i), Owner: owner, Category: cat, Status: "pending_review",
		})
		h.Exec(t, `UPDATE businesses SET updated_at = '2026-01-01 00:00:00+00' WHERE id = $1`, b.ID)
	}

	order, dupes := pageAll(t, func(limit, offset int) ([]string, error) {
		rows, err := h.Repos.Businesses.ByStatus(ctx, []string{"pending_review"}, limit, offset)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(rows))
		for _, b := range rows {
			ids = append(ids, b.ID)
		}
		return ids, nil
	}, n, pageSize)
	assertTotalOrder(t, "ByStatus (admin queue)", order, dupes, n)
}

// TestEveryLimitedQueryHasATotalOrder is the DETERMINISTIC guard for this class,
// and it exists because the behavioural tests above turned out to be insufficient.
//
// Those tests were written first, and when the tiebreakers were stripped back out
// to check them, ALL SEVEN STILL PASSED. That is worth recording plainly, because
// a test that passes on the broken code is worse than no test: it reports
// confidence it has not earned.
//
// The reason is that the bug is an UNSPECIFIED behaviour, not an incorrect one.
// For a simple `WHERE business_id = $1 ORDER BY created_at DESC LIMIT 3 OFFSET n`
// the planner picks one index scan and returns the same order every time, so
// paging is de-facto stable — it is just not guaranteed, and it stops being
// stable the moment the plan changes: a different page size, different
// selectivity, a rebuilt index, a new Postgres version. The search suite did
// reproduce it, but only because that query joins a trend table and the plan
// genuinely varies per page.
//
// So this test asserts the guarantee rather than the observed behaviour: every
// ORDER BY that feeds a LIMIT or an OFFSET in this package must end in a unique
// column. It is a lint, and it is not a substitute for reading the diff — it is a
// substitute for remembering, which is the part that actually fails. A developer
// adding a new paginated list gets a red build instead of a latent bug.
//
// It reads the package's own source because these queries build SQL inline, so
// there is nothing to unit test. A line that genuinely cannot be checked
// statically opts out with an explicit `lint:allow` and a reason, so the opt-outs
// are reviewable rather than hidden.
func TestEveryLimitedQueryHasATotalOrder(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// The unique key every total order must terminate in. `id` is a uuid
	// primary key everywhere in this schema, so it is always available and
	// always total.
	finalTerm := regexp.MustCompile(`(?i)\bid\b[^,]*\b(asc|desc)?\b`)
	// `LIMIT 1` exactly, not `LIMIT 100`.
	limitOne := regexp.MustCompile(`LIMIT\s+1\b`)

	var offenders []string
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		// Blank out comments, preserving every byte offset so a match position in
		// the cleaned text still maps to the right line in the original. The
		// alternative — scanning line by line — was the first attempt and it was
		// WRONG in a way that mattered: most queries here put ORDER BY and
		// OFFSET on different lines, so a line-based scan saw the ORDER BY with
		// no OFFSET beside it, decided there was nothing to check, and silently
		// passed. It caught 2 of the 4 violations in the experiment below, which
		// is worse than catching none, because two of the four were fixed.
		cleaned := blankComments(string(src))

		// `rest` walks the file forward, so `consumed` is the absolute byte offset
		// of rest within cleaned. Without it the reported line number restarts at 1
		// for every match after the first, which for a lint is worse than useless:
		// it sends the reader to the wrong place and teaches them to ignore it.
		rest := cleaned
		consumed := 0
		for {
			at := indexFold(rest, "ORDER BY")
			if at < 0 {
				break
			}
			tail := rest[at+len("ORDER BY"):]
			line := 1 + strings.Count(cleaned[:consumed+at], "\n")

			// Bound the scan to the ENCLOSING raw string. Getting this wrong was
			// the second bug in this lint: searching the whole remaining file for
			// OFFSET made every ORDER BY in the file inherit the OFFSET of some
			// later query, so a query with no LIMIT at all was reported. The
			// backtick is the terminator, since every one of these statements is a
			// Go raw string.
			stmtEnd := len(tail)
			if p := strings.IndexByte(tail, '`'); p >= 0 {
				stmtEnd = p
			}
			stmt := tail[:stmtEnd]

			// The order expression runs to whichever comes first of LIMIT, OFFSET,
			// a backtick, a quote, or a concatenation `+`. Whitespace is collapsed
			// FIRST so a multi-line ORDER BY reads as one expression and, more
			// importantly, so the cut offset and the slice afterwards are taken
			// against the same string.
			stmt = strings.Join(strings.Fields(stmt), " ")
			expr := stmt
			cut := len(expr)
			for _, stop := range []string{"LIMIT", "OFFSET", "`", `"`, "+"} {
				if p := indexFold(expr, stop); p >= 0 && p < cut {
					cut = p
				}
			}
			clause := expr[cut:]
			expr = strings.TrimSpace(expr[:cut])

			// The rule, and why it is exactly this rule:
			//
			//   * OFFSET  — rows are SKIPPED and REPEATED across pages unless the
			//     order is total. This is the bug.
			//   * LIMIT 1 — returns an arbitrary row out of a tied set where the
			//     caller means one specific row. GetLatestUnread is the case.
			//   * LIMIT N with no OFFSET — picks N rows out of a tied set, and
			//     every one is equally valid. "The 50 newest sessions" returning a
			//     different arbitrary 50 on Tuesday is not a correctness problem,
			//     and demanding a total order there would mean seven index rebuilds
			//     for no user-visible gain.
			//
			// So the lint fires on OFFSET and on LIMIT 1 and stays quiet on the
			// rest: narrow on purpose, so everything it reports is real. The \b in
			// the LIMIT pattern matters — a plain substring test for "LIMIT 1" also
			// matches "LIMIT 100", which is exactly the noise that would have made
			// this lint ignorable.
			upperClause := strings.ToUpper(clause)
			needsTotal := strings.Contains(upperClause, "OFFSET") || limitOne.MatchString(upperClause)
			// A dynamic expression (`+order+`) cannot be checked statically, and
			// ListReviews' three variants are covered behaviourally instead.
			dynamic := strings.Contains(strings.ToLower(expr), "order")
			// A `lint:allow` marker anywhere in the statement opts out, with a
			// reason, so the opt-outs stay reviewable instead of hidden.
			optOut := strings.Contains(stmt, "lint:allow")

			if needsTotal && !dynamic && !optOut && expr != "" {
				last := expr
				if p := strings.LastIndex(expr, ","); p >= 0 {
					last = expr[p+1:]
				}
				if !finalTerm.MatchString(strings.TrimSpace(last)) {
					offenders = append(offenders, fmt.Sprintf(
						"%s:%d  ORDER BY %s  — the final term is not a unique column, so this "+
							"LIMIT/OFFSET query has no total order: a tied row can be served on two "+
							"pages while another is never served at all",
						name, line, expr))
				}
			}

			consumed += at + len("ORDER BY")
			rest = tail
		}
	}
	for _, o := range offenders {
		t.Error(o)
	}
}

// blankComments replaces comment text with spaces, keeping byte offsets intact.
// Whole-line `//` comments and SQL `--` comments are both handled; a `//` inside
// a string literal is not, which is the safe direction to err in — the worst case
// is a missed check, and a false report would be obvious immediately.
func blankComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			lines[i] = strings.Repeat(" ", len(line))
			continue
		}
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx] + strings.Repeat(" ", len(line)-idx)
		}
	}
	return strings.Join(lines, "\n")
}

// indexFold is strings.Index with case folding, for SQL keywords.
func indexFold(s, sub string) int { return strings.Index(strings.ToUpper(s), strings.ToUpper(sub)) }

// TestReviewWithoutPhotosIsAccepted is a regression test for a 500 on the most
// common engagement action in the product.
//
// `reviews.image_ids` is `NOT NULL DEFAULT '{}'`, and it is easy to assume that
// makes the default apply when no photos are given. It does not: an explicit bind
// of a Go nil slice is sent as SQL NULL, and a column default is only consulted
// when the column is omitted from the statement. The repository always lists
// image_ids, so it always sent an explicit NULL.
//
// The path from a real request: `{"rating":4,"text":"..."}` with no `image_ids`
// key — encoding/json leaves the field nil — through the service (which only
// checks the upper bound, `len(imageIDs) > 6`, so nil sails through) into a 23502
// that surfaced as an internal error. So EVERY review written without a photo
// failed. Reviews with photos worked, which is why it could sit unnoticed: it
// looks like a flaky feature rather than a total failure of one variant.
//
// Exercised at the repository layer rather than through service.Engagement,
// because that is where the fix lives and where the bind happens, and because
// service.CreateReview dereferences its Notifier on the success path — passing nil
// for it panics, which would obscure the assertion this test exists to make. The
// service adds only the `len > 6` bound check, which nil passes.
func TestReviewWithoutPhotosIsAccepted(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	author := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Photo Optional Cafe", Owner: owner, Category: cat, Status: "verified",
	})

	const body = "a review body long enough to clear the minimum length"

	// nil: what an absent "image_ids" key decodes to. This is the case that
	// raised 23502.
	if err := h.Repos.Engagement.CreateReview(ctx, biz.ID, nil, author.ID, 4, body, nil); err != nil {
		t.Fatalf("review with a nil image_ids slice was rejected: %v. This is the bug: nil binds as "+
			"SQL NULL, and a NOT NULL column with a DEFAULT still rejects it.", err)
	}
	got, err := h.Repos.Engagement.GetReviewByUser(ctx, biz.ID, nil, author.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("the review was not stored")
	}
	if len(got.ImageIDs) != 0 {
		t.Errorf("image_ids = %v, want empty", got.ImageIDs)
	}

	// A second reviewer, so the edit path has its own row and the
	// one-product-less-review-per-user partial unique index is not violated.
	editor := testutil.User(t, h)
	if err := h.Repos.Engagement.CreateReview(ctx, biz.ID, nil, editor.ID, 5, body, []string{}); err != nil {
		t.Fatalf("review with an empty image_ids slice was rejected: %v", err)
	}

	// Editing photos OFF is an ordinary action and must not be a constraint
	// violation either: the same nil binds on the UPDATE.
	if err := h.Repos.Engagement.UpdateReview(ctx, editor.ID, lastReviewID(t, h, biz.ID, editor.ID),
		4, body, nil); err != nil {
		t.Errorf("stripping the photos off a review was rejected: %v", err)
	}
}

// lastReviewID finds the review just written, so the created_at pin can be
// applied to it. The Review fixture does not return the id, and re-selecting by
// author is unambiguous because a user has at most one product-less review per
// business.
func lastReviewID(t *testing.T, h *testutil.H, businessID, userID string) string {
	t.Helper()
	var id string
	if err := h.QueryRow(t,
		`SELECT id FROM reviews WHERE business_id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		businessID, userID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
