package service

import (
	"context"
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
	HasChat           bool // businesses that engage in chat (PRD §5.1.2)
	Sort              string // trending|rating|newest|nearest|relevance
	Limit             int
	Offset            int
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
		// ILIKE patterns get escaped so user input matches literally.
		qLike := util.EscapeLike(q)
		where = append(where, `(
			to_tsvector('simple', coalesce(b.name,'') || ' ' || coalesce(b.tagline,'') || ' ' ||
				coalesce(b.description,'') || ' ' || coalesce(b.city,'') || ' ' ||
				array_to_string(b.tags,' ')) @@ plainto_tsquery('simple', `+arg(q)+`)
			OR b.name ILIKE '%' || `+arg(qLike)+` || '%'
			OR b.name % `+arg(q)+`
			OR cat.name ILIKE '%' || `+arg(qLike)+` || '%'
			OR EXISTS (SELECT 1 FROM products p WHERE p.business_id = b.id AND p.is_published = true
				AND p.deleted_at IS NULL AND to_tsvector('simple', coalesce(p.name,'')) @@ plainto_tsquery('simple', `+arg(q)+`)))`)
	}
	if len(p.CategoryIDs) > 0 {
		where = append(where, "b.category_id = ANY("+arg(p.CategoryIDs)+")")
	}
	if p.City != "" {
		where = append(where, "b.city = "+arg(p.City))
	}
	if p.Lat != nil && p.Lng != nil && p.RadiusKM > 0 {
		where = append(where, `earth_distance(ll_to_earth(b.lat, b.lng),
			ll_to_earth(`+arg(*p.Lat)+`, `+arg(*p.Lng)+`)) <= `+arg(p.RadiusKM*1000))
	}
	if p.MinLng != nil && p.MinLat != nil && p.MaxLng != nil && p.MaxLat != nil {
		where = append(where, `b.lat BETWEEN `+arg(*p.MinLat)+` AND `+arg(*p.MaxLat)+`
			AND b.lng BETWEEN `+arg(*p.MinLng)+` AND `+arg(*p.MaxLng))
	}
	if len(p.PriceLevels) > 0 {
		where = append(where, "b.price_level = ANY("+arg(p.PriceLevels)+")")
	}
	if p.MinRating > 0 {
		where = append(where, `(SELECT coalesce(avg(r.rating), 0) FROM reviews r
			WHERE r.business_id = b.id AND r.deleted_at IS NULL) >= `+arg(p.MinRating))
	}
	if p.OpenNow {
		// SQL-side filter (migration 0014): keeps LIMIT/OFFSET pagination exact.
		where = append(where, "biz_is_open_now(b.hours, b.timezone)")
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

	// True-total args: only the WHERE clause placeholders (dist/rank/limit are
	// SELECT extras and must not leak into the COUNT query).
	countArgs := append([]any{}, args...)

	dist := "NULL::float8 AS distance_km"
	if p.Lat != nil && p.Lng != nil {
		dist = "earth_distance(ll_to_earth(b.lat, b.lng), ll_to_earth(" + arg(*p.Lat) + ", " + arg(*p.Lng) + ")) / 1000.0 AS distance_km"
	}

	rank := "0 AS ts_rank"
	if q != "" {
		rank = "ts_rank(to_tsvector('simple', coalesce(b.name,'') || ' ' || coalesce(b.tagline,'') || ' ' || coalesce(b.description,'') || ' ' || coalesce(b.city,'')), plainto_tsquery('simple', " + arg(q) + ")) AS ts_rank"
	}

	// Trend join: latest 24h snapshot for the default (trending) sort (PRD §5.1.2).
	trendJoin := ""
	if p.Sort == "" || p.Sort == "trending" {
		trendJoin = `
			LEFT JOIN trend_snapshots ts ON ts.business_id = b.id AND ts.period='24h'
				AND ts.taken_at = (SELECT max(taken_at) FROM trend_snapshots WHERE period='24h')`
	}

	// Sort expressions are self-contained (own bind params via arg()): they
	// run in BOTH phases below, after countArgs was snapshotted, so they
	// never leak into the COUNT query.
	order := "b.created_at DESC"
	switch p.Sort {
	case "rating":
		order = `(SELECT coalesce(avg(r.rating), 0) FROM reviews r
			WHERE r.business_id = b.id AND r.deleted_at IS NULL) DESC NULLS LAST, b.created_at DESC`
	case "nearest":
		if p.Lat != nil && p.Lng != nil {
			order = "earth_distance(ll_to_earth(b.lat, b.lng), ll_to_earth(" +
				arg(*p.Lat) + ", " + arg(*p.Lng) + ")) / 1000.0 ASC NULLS LAST, b.created_at DESC"
		}
	case "relevance":
		if q != "" {
			order = "ts_rank(to_tsvector('simple', coalesce(b.name,'') || ' ' || coalesce(b.tagline,'') || ' ' || coalesce(b.description,'') || ' ' || coalesce(b.city,'')), plainto_tsquery('simple', " + arg(q) + ")) DESC, b.created_at DESC"
		}
	default: // trending: engagement velocity, then score (PRD §5.6.3)
		order = "coalesce(ts.velocity, 0) DESC, coalesce(ts.score, 0) DESC, b.verified_at DESC NULLS LAST, b.created_at DESC"
	}

	// Phase 1: select ONLY the page's business ids. This keeps the expensive
	// per-row hydration subqueries (like/save/recommend/review counts) out of
	// the candidate scan entirely; sorting still evaluates its own aggregate,
	// but once per candidate instead of five extra subqueries.
	idSQL := `
		SELECT b.id FROM businesses b
		JOIN categories cat ON cat.id = b.category_id` + trendJoin + `
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + order + `
		LIMIT ` + arg(p.Limit+1) + ` OFFSET ` + arg(p.Offset)
	idRows, err := s.repos.Query(ctx, idSQL, args...)
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
	sql := `
		SELECT ` + repo.BusinessCols + repo.BusinessCounts + `, ` + dist + `, ` + rank + `
		FROM businesses b
		JOIN categories cat ON cat.id = b.category_id` + trendJoin + `
		WHERE b.id = ANY(` + arg(ids) + `)
		ORDER BY ` + order

	rows, err := s.repos.Businesses.Search(ctx, sql, args)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	out := make([]*domain.Business, 0, len(rows))
	for _, b := range rows {
		open := isOpenNow(b.Hours, now, b.Timezone)
		b.IsOpenNow = &open
		out = append(out, b)
	}
	if !hasMore {
		// Last page: total is offset + actual rows returned.
		return out, p.Offset + len(out), nil
	}
	// True total (the row limit above is a page cap; PRD §5.1.2 result counts).
	var total int
	countSQL := `
		SELECT count(*) FROM businesses b
		JOIN categories cat ON cat.id = b.category_id` + trendJoin + `
		WHERE ` + strings.Join(where, " AND ")
	if err := s.repos.QueryRow(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return out, len(out), nil // best-effort: fall back to page size
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
		  AND (b.name ILIKE '%' || $1 || '%' OR b.tagline ILIKE '%' || $1 || '%')
		ORDER BY (b.name ILIKE $1 || '%') DESC, b.created_at DESC
		LIMIT $2`, []any{qLike, limit})
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
func isOpenNow(hours map[string]any, at time.Time, timezone string) bool {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	local := at.In(loc)
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	cur := local.Hour()*60 + local.Minute()

	check := func(day string) (bool, bool) {
		raw, ok := hours[day]
		if !ok {
			return false, false
		}
		entry, ok := raw.(map[string]any)
		if !ok {
			return false, false
		}
		if closed, _ := entry["closed"].(bool); closed {
			return false, false
		}
		open, _ := entry["open"].(string)
		closeT, _ := entry["close"].(string)
		oh, ok1 := parseHHMM(open)
		ch, ok2 := parseHHMM(closeT)
		if !ok1 || !ok2 {
			return false, false
		}
		if ch <= oh { // overnight window
			return cur >= oh || cur < ch, true
		}
		return cur >= oh && cur < ch, true
	}

	if open, defined := check(days[int(local.Weekday())]); defined {
		if open {
			return true
		}
	}
	// Previous day's OVERNIGHT window may cover early today. The overnight
	// guard (close <= open) mirrors the SQL twin biz_is_open_now (migration
	// 0014): without it, yesterday's 09:00–17:00 window would wrongly match
	// today's daytime minutes.
	prev := local.AddDate(0, 0, -1)
	if open, defined := check(days[int(prev.Weekday())]); defined && open {
		oh := parseOpenOf(prev, hours, days)
		ch := parseCloseOf(prev, hours, days)
		if ch <= oh && cur < ch {
			return true
		}
	}
	return false
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
