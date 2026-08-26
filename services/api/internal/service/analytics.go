package service

import (
	"context"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
)

// Analytics — owner insights (PRD §5.4.5). Engagement events feed the series
// once the M3 pipeline lands; counters are live today.
type Analytics struct {
	repos *repo.Repos
}

func NewAnalytics(repos *repo.Repos) *Analytics { return &Analytics{repos: repos} }

type AnalyticsResult struct {
	Period       string          `json:"period"`
	Views        int             `json:"views"`
	Likes        int             `json:"likes"`
	Recommends   int             `json:"recommends"`
	Comments     int             `json:"comments"`
	Reviews      int             `json:"reviews"`
	Saves        int             `json:"saves"`
	ChatMessages int             `json:"chat_messages"`
	RatingAvg    *float64        `json:"rating_avg"`
	TopProducts  []ProductCount  `json:"top_products"`
	Series       []DailyCount    `json:"views_series"`
	Leaderboard  *LeaderboardPos `json:"leaderboard,omitempty"`
}

type ProductCount struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	Likes     int    `json:"likes"`
	Views     int    `json:"views"`
}

type DailyCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type LeaderboardPos struct {
	Global   *int `json:"global"`
	Category *int `json:"category"`
}

func (a *Analytics) ForBusiness(ctx context.Context, userID, businessID, period string) (*AnalyticsResult, error) {
	b, err := a.repos.Businesses.GetByID(ctx, businessID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, domain.ErrNotFound
	}
	// Read-only analytics access: owner OR co-owner OR accepted viewer
	// (PRD §5.9.3). Viewers get nothing beyond this read.
	if b.OwnerID != userID {
		can, err := a.repos.Businesses.CanManageBusiness(ctx, userID, businessID)
		if err != nil {
			return nil, err
		}
		if !can {
			viewer, err := a.repos.Businesses.IsBusinessViewer(ctx, userID, businessID)
			if err != nil {
				return nil, err
			}
			if !viewer {
				return nil, domain.ErrForbidden
			}
		}
	}

	since, days := sinceFor(period)

	out := &AnalyticsResult{Period: period}

	_ = a.repos.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engagement_events e WHERE e.target_type='business' AND e.target_id=$1 AND e.signal='view' AND e.occurred_at >= $2),
			(SELECT count(*) FROM likes l WHERE l.target_type='business' AND l.target_id=$1),
			(SELECT count(*) FROM recommends rc WHERE rc.business_id=$1),
			(SELECT count(*) FROM comments c WHERE c.business_id=$1 AND c.status='visible'),
			(SELECT count(*) FROM reviews r WHERE r.business_id=$1 AND r.deleted_at IS NULL),
			(SELECT count(*) FROM collection_items ci WHERE ci.target_type='business' AND ci.target_id=$1),
			(SELECT count(*) FROM chat_messages m JOIN chat_threads t ON t.id=m.thread_id WHERE t.business_id=$1 AND m.created_at >= $2),
			(SELECT avg(r.rating)::float8 FROM reviews r WHERE r.business_id=$1 AND r.deleted_at IS NULL)`,
		businessID, since).Scan(&out.Views, &out.Likes, &out.Recommends, &out.Comments,
		&out.Reviews, &out.Saves, &out.ChatMessages, &out.RatingAvg)

	// Daily view series (fills in as M3 events land).
	rows, err := a.repos.Query(ctx, `
		SELECT to_char(date_trunc('day', occurred_at), 'YYYY-MM-DD') AS day, count(*)
		FROM engagement_events
		WHERE target_type='business' AND target_id=$1 AND signal='view' AND occurred_at >= $2
		GROUP BY 1 ORDER BY 1`, businessID, since)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d DailyCount
			if err := rows.Scan(&d.Day, &d.Count); err == nil {
				out.Series = append(out.Series, d)
			}
		}
	}
	if len(out.Series) < days {
		out.Series = fillSeries(out.Series, days)
	}

	// Top products by engagement — scoped to THIS business's products.
	// The old query aggregated likes/events across the entire platform
	// (every analytics page view scanned the global tables).
	if prods, err := a.repos.Products.ListByBusiness(ctx, businessID); err == nil && len(prods) > 0 {
		rows, qerr := a.repos.Query(ctx, `
			SELECT p.id,
				(SELECT count(*) FROM likes l2 WHERE l2.target_type='product' AND l2.target_id = p.id),
				(SELECT count(*) FROM engagement_events e WHERE e.target_type='product' AND e.target_id = p.id AND e.signal='view')
			FROM products p WHERE p.business_id = $1`, businessID)
		counts := map[string]ProductCount{}
		if qerr == nil {
			for rows.Next() {
				var pid string
				var likes, views int
				if err := rows.Scan(&pid, &likes, &views); err != nil {
					break
				}
				counts[pid] = ProductCount{ProductID: pid, Likes: likes, Views: views}
			}
			rows.Close()
		}
		for _, p := range prods {
			c := counts[p.ID]
			c.ProductID, c.Name = p.ID, p.Name
			out.TopProducts = append(out.TopProducts, c)
		}
		// rank by views+likes desc, top 5
		for i := 1; i < len(out.TopProducts); i++ {
			for j := i; j > 0 && rank(out.TopProducts[j]) > rank(out.TopProducts[j-1]); j-- {
				out.TopProducts[j], out.TopProducts[j-1] = out.TopProducts[j-1], out.TopProducts[j]
			}
		}
		if len(out.TopProducts) > 5 {
			out.TopProducts = out.TopProducts[:5]
		}
	}

	// Leaderboard position: global rank from the 7d snapshot, category rank
	// from the 24h snapshot (populated by the trending job, PRD §5.6.3).
	// Scan into scalars — the previous single-destination Scan for two
	// columns errored (silently discarded) and left Leaderboard nil forever.
	var gRank, cRank *int
	if err := a.repos.QueryRow(ctx, `
		SELECT
			(SELECT rank_global FROM trend_snapshots WHERE business_id = $1 AND period='7d'
			 ORDER BY taken_at DESC LIMIT 1),
			(SELECT rank_category FROM trend_snapshots WHERE business_id = $1 AND period='24h'
			 ORDER BY taken_at DESC LIMIT 1)`, businessID).
		Scan(&gRank, &cRank); err == nil && (gRank != nil || cRank != nil) {
		out.Leaderboard = &LeaderboardPos{Global: gRank, Category: cRank}
	}

	return out, nil
}

func sinceFor(period string) (time.Time, int) {
	days := 30
	switch period {
	case "7d":
		days = 7
	case "all":
		days = 365
	}
	return time.Now().AddDate(0, 0, -days), days
}

func rank(p ProductCount) int { return p.Views + p.Likes*3 }

func fillSeries(series []DailyCount, days int) []DailyCount {
	byDay := map[string]int{}
	for _, d := range series {
		byDay[d.Day] = d.Count
	}
	out := make([]DailyCount, 0, days)
	start := time.Now().AddDate(0, 0, -days+1)
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i).Format("2006-01-02")
		out = append(out, DailyCount{Day: day, Count: byDay[day]})
	}
	return out
}
