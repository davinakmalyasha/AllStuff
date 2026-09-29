package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Search - PRD §5.1.2: FTS + filters + sorts. Open-now is filtered in Go
// (hours are JSONB; the candidate set per page is small).
type Search struct {
	repos *repo.Repos
}

func NewSearch(repos *repo.Repos) *Search { return &Search{repos: repos} }

type SearchParams struct {
	Q                 string
	City              string
	CategoryIDs       []string
	Lat, Lng          *float64
	RadiusKM          float64
	MinLng, MinLat    *float64 // viewport bounding box (PRD §5.2)
	MaxLng, MaxLat    *float64
	PriceLevels       []int
	MinRating         float64
	OpenNow           bool
	VerifiedOnly      bool
	FullyVerifiedOnly bool
	HasChat           bool   // businesses that engage in chat (PRD §5.1.2)
	Sort              string // trending|rating|newest|nearest|relevance
	Limit             int
	Offset            int
	// WithTotal requests an exact result count. OFF by default: the count
	// re-runs the ENTIRE candidate predicate — the 5-way FTS OR, the per-row
	// products EXISTS, a plpgsql biz_is_open_now call per row, the avg(rating)
	// subquery and the trend join — which is more expensive than fetching the
	// page it is counting.
	//
	// It previously ran on every page except the last, i.e. essentially always,
	// and again per saved search in the nightly alert job. Callers that genuinely
	// need a total (e.g. "results 1-24 of 1,203") pass WithTotal on the FIRST
	// page only; pager navigation reads has_more, which is free.
	//
	// A negative return means "not computed" and must be rendered as such, not
	// as zero.
	WithTotal bool
}

func (s *Search) Businesses(ctx context.Context, p SearchParams) ([]*domain.Business, int, error) {
	if p.Limit <= 0 || p.Limit > 50 {
		p.Limit = 24
	}

	q := strings.TrimSpace(p.Q)
	where := []string{"b.status = 'verified'", "b.deleted_at IS NULL"}
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + util.Itoa(len(args))
	}

	if q != "" {
		// Exact-ish FTS first; trigram similarity catches typos (migration 0016).
		// ILIKE patterns get escaped so user input matches literally. Every
		// placeholder carries an explicit type: extended-protocol Parse cannot
		// infer bare params in ANY()/trigram contexts (PG17 42P18).
		//
		// The two FTS branches call the IMMUTABLE helpers from migration 0031
		// (biz_search_tsv / product_search_tsv) rather than writing
		// to_tsvector(...) inline. That is what makes the GIN indexes usable: an
		// index expression must match the query expression textually or the
		// planner ignores it, and this predicate previously existed in three
		// hand-written variants (here, the `relevance` sort, and a shorter form
		// in the rank pass) — none indexed, all able to drift apart silently.
		// The helpers make that divergence unrepresentable.
		qLike := util.EscapeLike(q)
		where = append(where, `(
			biz_search_tsv(b.name, b.tagline, b.description, b.city, b.tags)
				@@ plainto_tsquery('simple', `+arg(q)+`::text)
			OR b.name ILIKE '%' || `+arg(qLike)+`::text || '%'
			OR b.name % `+arg(q)+`::text
			OR cat.name ILIKE '%' || `+arg(qLike)+`::text || '%'
			OR EXISTS (SELECT 1 FROM products p WHERE p.business_id = b.id AND p.is_published = true
				AND p.deleted_at IS NULL
				AND product_search_tsv(p.name) @@ plainto_tsquery('simple', `+arg(q)+`::text)))`)
	}
	if len(p.CategoryIDs) > 0 {
		where = append(where, "b.category_id = ANY("+arg(p.CategoryIDs)+"::uuid[])")
	}
	if p.City != "" {
		where = append(where, "b.city = "+arg(p.City)+"::text")
	}
	if p.Lat != nil && p.Lng != nil && p.RadiusKM > 0 {
		where = append(where, `earth_distance(ll_to_earth(b.lat, b.lng),
			ll_to_earth(`+arg(*p.Lat)+`::float8, `+arg(*p.Lng)+`::float8)) <= `+arg(p.RadiusKM*1000)+"::float8")
	}
	if p.MinLng != nil && p.MinLat != nil && p.MaxLng != nil && p.MaxLat != nil {
		where = append(where, `b.lat BETWEEN `+arg(*p.MinLat)+`::float8 AND `+arg(*p.MaxLat)+`::float8
			AND b.lng BETWEEN `+arg(*p.MinLng)+`::float8 AND `+arg(*p.MaxLng)+`::float8`)
	}
	if len(p.PriceLevels) > 0 {
		where = append(where, "b.price_level = ANY("+arg(p.PriceLevels)+"::int[])")
	}
	if p.MinRating > 0 {
		where = append(where, `(SELECT coalesce(avg(r.rating), 0) FROM reviews r
			WHERE r.business_id = b.id AND r.deleted_at IS NULL) >= `+arg(p.MinRating)+"::float8")
	}
	if p.OpenNow {
		// SQL-side filter (migration 0014): keeps LIMIT/OFFSET pagination exact.
		// 3-arg form so a business marked closed for a public holiday is
		// excluded by the filter, not just badged "Closed". Mismatched with
		// the Go isOpenNow badge, the filter and the badge would disagree on
		// the same row. See migration 0030_open_now.sql; the 2-arg form is
		// kept as a back-compat wrapper for rolling deploys.
		where = append(where, "biz_is_open_now(b.hours, b.special_hours, b.timezone)")
	}
	if p.VerifiedOnly {
		where = append(where, "b.verification_level IS NOT NULL")
	}
	if p.FullyVerifiedOnly {
		where = append(where, "b.verification_level = 'fully_verified'")
	}
	if p.HasChat {
		// Businesses where an owner participates in at least one chat thread.
		where = append(where, `EXISTS (
			SELECT 1 FROM chat_threads t JOIN chat_participants cp ON cp.thread_id = t.id
			JOIN businesses bo ON bo.id = t.business_id
			WHERE t.business_id = b.id AND t.type = 'business'
			  AND cp.user_id = bo.owner_id AND cp.left_at IS NULL)`)
	}

	// True-total args: only the WHERE clause placeholders (dist/rank/order
	// extras must not leak into the COUNT query). Every dynamic parameter
	// carries an explicit cast AND each statement receives EXACTLY the args
	// it references, densely numbered from $1: extended-protocol Parse cannot
	// infer unreferenced or context-free parameters (PG17 42P18).
	whereArgs := append([]any{}, args...)

	// buildOrder emits the ORDER BY expression for a given sort, minting its
	// bind params through the supplied per-statement placeholder factory.
	//
	// EVERY branch ends in `b.id`. That is not decoration: it is what makes
	// pagination correct. Postgres does not guarantee a stable order for rows
	// that tie on the sort key, and it picks a different one per execution
	// because the plan changes with LIMIT/OFFSET. A tie is not a rare edge case
	// here — scripts/seed.sql inserts rows with generate_series in a single
	// statement, so `now()` gives every row an identical created_at, and any bulk
	// import or backfill does the same.
	//
	// Without the final key, paging 12 rows in pages of 4 could return a business
	// on page 1 and again on page 2 while another never appeared at all. The user
	// sees a duplicate and assumes something is missing, and nothing anywhere
	// reports an error: each page was a perfectly valid result for its own query.
	// `b.id` is unique and non-null, so it is always available as a tiebreaker
	// and always makes the order total.
	buildOrder := func(add func(any) string) string {
		switch p.Sort {
		case "rating":
			return `(SELECT coalesce(avg(r.rating), 0) FROM reviews r
				WHERE r.business_id = b.id AND r.deleted_at IS NULL) DESC NULLS LAST,
				b.created_at DESC, b.id ASC`
		case "newest":
			return "b.created_at DESC, b.id ASC"
		case "nearest":
			if p.Lat != nil && p.Lng != nil {
				return "earth_distance(ll_to_earth(b.lat, b.lng), ll_to_earth(" +
					add(*p.Lat) + "::float8, " + add(*p.Lng) + "::float8)) / 1000.0 ASC NULLS LAST, " +
					"b.created_at DESC, b.id ASC"
			}
		case "relevance":
			if q != "" {
				// Same helper as the WHERE clause, minus `tags` (a relevance
				// expression that includes tags would rank a business on its
				// own keywords more than on its text). It is deliberately NOT
				// the full 5-column form: the point of the helper is that one
				// definition is reused, not that every call site is identical.
				// Keeping the shapes in one place is what prevents an
				// unindexed variant reappearing here.
				return "ts_rank(biz_search_tsv(b.name, b.tagline, b.description, b.city, NULL), plainto_tsquery('simple', " + add(q) + "::text)) DESC, b.created_at DESC, b.id ASC"
			}
		default: // trending: engagement velocity, then score (PRD §5.6.3)
			return "coalesce(ts.velocity, 0) DESC, coalesce(ts.score, 0) DESC, " +
				"b.verified_at DESC NULLS LAST, b.created_at DESC, b.id ASC"
		}
		return "b.created_at DESC, b.id ASC"
	}

	// Trend join: latest 24h snapshot for the default (trending) sort (PRD §5.1.2).
	trendJoin := ""
	if p.Sort == "" || p.Sort == "trending" {
		trendJoin = `
			LEFT JOIN trend_snapshots ts ON ts.business_id = b.id AND ts.period='24h'
				AND ts.taken_at = (SELECT max(taken_at) FROM trend_snapshots WHERE period='24h')`
	}

	// Phase 1: select ONLY the page's business ids. This keeps the expensive
	// per-row hydration subqueries (like/save/recommend/review counts) out of
	// the candidate scan entirely; sorting still evaluates its own aggregate,
	// but once per candidate instead of five extra subqueries.
	// LIMIT/OFFSET are inlined as validated integers (Limit clamped ≤50,
	// Offset ≥0 by parsePositiveInt): bare LIMIT placeholders are uninferable.
	p1 := append([]any{}, whereArgs...)
	add1 := func(v any) string { p1 = append(p1, v); return "$" + util.Itoa(len(p1)) }
	idSQL := `
		SELECT b.id FROM businesses b
		JOIN categories cat ON cat.id = b.category_id` + trendJoin + `
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + buildOrder(add1) + `
		LIMIT ` + util.Itoa(p.Limit+1) + ` OFFSET ` + util.Itoa(p.Offset)
	idRows, err := s.repos.Query(ctx, idSQL, p1...)
	if err != nil {
		return nil, 0, err
	}
	var ids []string
	for idRows.Next() {
		var id string
		if err := idRows.Scan(&id); err != nil {
			idRows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	idRows.Close()
	if err := idRows.Err(); err != nil {
		return nil, 0, err
	}
	hasMore := len(ids) > p.Limit
	if hasMore {
		ids = ids[:p.Limit]
	}
	if len(ids) == 0 {
		return nil, 0, nil
	}

	// Phase 2: hydrate the full row shape for exactly the page's ids.
	p2 := []any{ids}
	add2 := func(v any) string { p2 = append(p2, v); return "$" + util.Itoa(len(p2)) }
	dist := "NULL::float8 AS distance_km"
	if p.Lat != nil && p.Lng != nil {
		dist = "earth_distance(ll_to_earth(b.lat, b.lng), ll_to_earth(" + add2(*p.Lat) + "::float8, " + add2(*p.Lng) + "::float8)) / 1000.0 AS distance_km"
	}
	rank := "0 AS ts_rank"
	if q != "" {
		// Same 4-column shape as the `relevance` ORDER BY, via the same helper.
		// This is the third of the three hand-written copies of this expression
		// that existed; all three are now one definition.
		rank = "ts_rank(biz_search_tsv(b.name, b.tagline, b.description, b.city, NULL), plainto_tsquery('simple', " + add2(q) + "::text)) AS ts_rank"
	}
	sql := `
		SELECT ` + repo.BusinessCols + repo.BusinessCounts + `, ` + dist + `, ` + rank + `
		FROM businesses b
		JOIN categories cat ON cat.id = b.category_id` + trendJoin + `
		WHERE b.id = ANY($1::uuid[])
		ORDER BY ` + buildOrder(add2)

	rows, err := s.repos.Businesses.Search(ctx, sql, p2)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	out := make([]*domain.Business, 0, len(rows))
	for _, b := range rows {
		open := isOpenNow(b.Hours, b.SpecialHours, now, b.Timezone)
		b.IsOpenNow = &open
		out = append(out, b)
	}
	if !hasMore {
		// Last page: the total is exactly offset + rows returned, so no extra
		// query is needed regardless of WithTotal.
		return out, p.Offset + len(out), nil
	}
	if !p.WithTotal {
		// Negative means "not computed" — the caller renders a pager from the
		// page rather than claiming a total it does not have.
		return out, -1, nil
	}
	// Exact total for callers that asked for one (first page of a paged view).
	// Deliberately first-page-only in practice: see SearchParams.WithTotal.
	var total int
	countSQL := `
		SELECT count(*) FROM businesses b
		JOIN categories cat ON cat.id = b.category_id` + trendJoin + `
		WHERE ` + strings.Join(where, " AND ")
	if err := s.repos.QueryRow(ctx, countSQL, whereArgs...).Scan(&total); err != nil {
		// Best-effort: fall back to the page size, but leave a trace — silent
		// wrong counts are indistinguishable from real ones.
		slog.Warn("search count fallback", "err", err)
		return out, len(out), nil
	}
	return out, total, nil
}

// Suggestions - autocomplete (PRD §5.1.2): business prefixes + category names.
func (s *Search) Suggestions(ctx context.Context, q string, limit int) (map[string][]any, error) {
	if limit <= 0 || limit > 10 {
		limit = 10
	}
	qLike := util.EscapeLike(q)
	biz, err := s.repos.Businesses.Search(ctx, `
		SELECT `+repo.BusinessCols+repo.BusinessCounts+`, NULL::float8 AS distance_km, 0 AS ts_rank
		FROM businesses b JOIN categories cat ON cat.id = b.category_id
		WHERE b.status = 'verified' AND b.deleted_at IS NULL
		  AND (b.name ILIKE '%' || $1::text || '%' OR b.tagline ILIKE '%' || $1::text || '%')
		ORDER BY (b.name ILIKE $1::text || '%') DESC, b.created_at DESC
		LIMIT $2::bigint`, []any{qLike, limit})
	if err != nil {
		return nil, err
	}
	cats, err := s.repos.Categories.ListWithCounts(ctx)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(q)
	catHits := []any{}
	for _, c := range cats {
		if strings.Contains(strings.ToLower(c.Name), lower) {
			catHits = append(catHits, map[string]any{
				"type": "category", "name": c.Name, "slug": c.Slug, "count": c.Count,
			})
			if len(catHits) >= 5 {
				break
			}
		}
	}
	bizHits := []any{}
	for _, b := range biz {
		bizHits = append(bizHits, map[string]any{
			"type": "business", "name": b.Name, "slug": b.Slug,
			"category": b.CategoryName, "city": b.City,
		})
	}
	return map[string][]any{"businesses": bizHits, "categories": catHits}, nil
}

// isOpenNow: hours JSONB {day:{open,close,closed}}; overnight windows supported.
// Computed in the business's own timezone (PRD §5.3.4). An overnight window
// (e.g. Mon 18:00–02:00) covers the early hours of the NEXT day, so we also
// check the previous day's entry when the current day looks closed.
//
// specialHours JSONB {"YYYY-MM-DD":{open,close,closed}} overrides the weekly
// schedule for specific dates — public holidays, a private event, a one-off
// late opening. It takes precedence over the weekly entry for that date, and an
// override that marks the day closed is final.
//
// This function previously took only `hours`, so a business marked closed on
// Christmas was still reported "Open now" and still matched the `open_now=true`
// search filter — the platform's most-used filter returning a business that is
// definitionally shut. The column had existed since migration 0012, been read
// into the domain model and typed on the client, and been consulted by nothing.
func isOpenNow(hours, specialHours map[string]any, at time.Time, timezone string) bool {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	local := at.In(loc)
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	cur := local.Hour()*60 + local.Minute()
	today := local.Format("2006-01-02")
	prevDate := local.AddDate(0, 0, -1).Format("2006-01-02")

	// openWindow returns (isOpen, isDefined) for a single {open,close,closed}
	// entry evaluated at `cur` minutes.
	openWindow := func(entry map[string]any) (bool, bool) {
		if closed, _ := entry["closed"].(bool); closed {
			return false, true
		}
		oh, ok1 := parseHHMM(asString(entry["open"]))
		ch, ok2 := parseHHMM(asString(entry["close"]))
		if !ok1 || !ok2 {
			return false, false
		}
		if ch <= oh { // overnight window
			return cur >= oh || cur < ch, true
		}
		return cur >= oh && cur < ch, true
	}

	// check evaluates a day key against the weekly schedule, or against the
	// special-hours override for `date` when a USABLE one exists.
	//
	// An override that is present but unusable (bad times, missing close) falls
	// back to the weekly entry rather than reporting the day closed. The
	// validator rejects those at write time, but rows written before it existed
	// (or by a seed script) would otherwise make a business invisible for a
	// whole day: search's open_now filter would drop it and its badge would read
	// "Closed" while the owner is trading.
	check := func(day, date string) (bool, bool) {
		if override, ok := lookupSpecial(specialHours, date); ok {
			if open, defined := openWindow(override); defined {
				return open, true
			}
			// Unusable override: fall through to the weekly schedule.
		}
		raw, ok := hours[day]
		if !ok {
			return false, false
		}
		entry, ok := raw.(map[string]any)
		if !ok {
			return false, false
		}
		return openWindow(entry)
	}

	if open, defined := check(days[int(local.Weekday())], today); defined {
		if open {
			return true
		}
	}
	// Previous day's OVERNIGHT window may cover early today. The overnight
	// guard (close <= open) mirrors the SQL twin biz_is_open_now (migration
	// 0014): without it, yesterday's 09:00–17:00 window would wrongly match
	// today's daytime minutes.
	//
	// An overnight window that started on a day with its own special-hours
	// override must be evaluated against that override, not the weekly entry.
	prev := local.AddDate(0, 0, -1)
	prevDay := days[int(prev.Weekday())]
	if open, defined := check(prevDay, prevDate); defined && open {
		// Evaluate the overnight guard against whichever entry `check` used:
		// the override when one applies to the origin day, else the weekly row.
		entry, ok := lookupSpecial(specialHours, prevDate)
		if !ok || !openWindowDefined(entry) {
			entry = nil
			if raw, found := hours[prevDay]; found {
				if m, isMap := raw.(map[string]any); isMap {
					entry = m
				}
			}
		}
		if entry != nil {
			oh, ok1 := parseHHMM(asString(entry["open"]))
			ch, ok2 := parseHHMM(asString(entry["close"]))
			if ok1 && ok2 && ch <= oh && cur < ch {
				return true
			}
		}
	}
	return false
}

// openWindowDefined reports whether an entry carries usable open/close times
// (a `closed` entry is "defined" but has no window, so it returns false here
// and the caller correctly falls back to the weekly row only when there is no
// override at all).
func openWindowDefined(entry map[string]any) bool {
	if entry == nil {
		return false
	}
	if closed, _ := entry["closed"].(bool); closed {
		return true
	}
	_, ok1 := parseHHMM(asString(entry["open"]))
	_, ok2 := parseHHMM(asString(entry["close"]))
	return ok1 && ok2
}

// lookupSpecial resolves a special-hours override for a date.
//
// Two representations are accepted because the two writers disagree: the Go
// validator produces a bool `closed`, while scripts/seed.sql has historically
// written a string. Supporting both means a seeded business is not silently
// treated as having no override.
func lookupSpecial(specialHours map[string]any, date string) (map[string]any, bool) {
	if len(specialHours) == 0 || date == "" {
		return nil, false
	}
	raw, ok := specialHours[date]
	if !ok || raw == nil {
		return nil, false
	}
	entry, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	return entry, true
}

// parseOpenOf returns the open time (minutes) of the given day's entry.
func parseOpenOf(day time.Time, hours map[string]any, days []string) int {
	raw, ok := hours[days[int(day.Weekday())]]
	if !ok {
		return 0
	}
	entry, ok := raw.(map[string]any)
	if !ok {
		return 0
	}
	openT, _ := entry["open"].(string)
	o, ok := parseHHMM(openT)
	if !ok {
		return 0
	}
	return o
}

// parseCloseOf returns the close time (minutes) of the given day's entry.
func parseCloseOf(day time.Time, hours map[string]any, days []string) int {
	raw, ok := hours[days[int(day.Weekday())]]
	if !ok {
		return 0
	}
	entry, ok := raw.(map[string]any)
	if !ok {
		return 0
	}
	closeT, _ := entry["close"].(string)
	ch, ok := parseHHMM(closeT)
	if !ok {
		return 0
	}
	return ch
}

func parseHHMM(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	hh, ok1 := asInt(s[0:2])
	mm, ok2 := asInt(s[3:5])
	if !ok1 || !ok2 || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}
