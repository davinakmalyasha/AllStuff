package service

import (
	"context"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
)

// Cities — per-city landing pages (SEO, PRD §5.1.5). Cities are derived from
// verified businesses; slug resolution is case/diacritic-insensitive.
type Cities struct {
	repos *repo.Repos
}

func NewCities(repos *repo.Repos) *Cities { return &Cities{repos: repos} }

type CityPage struct {
	Slug       string           `json:"slug"`
	Name       string           `json:"name"`
	Count      int              `json:"count"`
	Lat        *float64         `json:"lat"`
	Lng        *float64         `json:"lng"`
	Categories []CityCategory   `json:"categories"`
	Top        []*domain.TrendEntry `json:"top"`
	TopRated   []*domain.Business  `json:"top_rated"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

type CityCategory struct {
	CategoryID   string `json:"category_id"`
	CategoryName string `json:"category_name"`
	CategorySlug string `json:"category_slug"`
	Count        int    `json:"count"`
}

// BySlug resolves a city slug to its page payload.
func (c *Cities) BySlug(ctx context.Context, slug string) (*CityPage, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	rows, err := c.repos.Query(ctx, `
		SELECT city, count(*) FROM businesses
		WHERE status = 'verified' AND deleted_at IS NULL AND city <> ''
		GROUP BY city`)
	if err != nil {
		return nil, err
	}
	var cities []struct{ name string; count int }
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			rows.Close()
			return nil, err
		}
		cities = append(cities, struct{ name string; count int }{name, count})
	}
	rows.Close()

	var chosen *struct{ name string; count int }
	for i := range cities {
		if slugify(cities[i].name) == slug {
			chosen = &cities[i]
			break
		}
	}
	if chosen == nil {
		return nil, domain.ErrNotFound
	}

	page := &CityPage{Slug: slug, Name: chosen.name, Count: chosen.count}

	// Map center: average of business coordinates.
	_ = c.repos.QueryRow(ctx, `
		SELECT avg(lat), avg(lng) FROM businesses
		WHERE status = 'verified' AND deleted_at IS NULL AND city = $1`, chosen.name).
		Scan(&page.Lat, &page.Lng)

	// Category breakdown.
	catRows, err := c.repos.Query(ctx, `
		SELECT b.category_id, cat.name, cat.slug, count(*)
		FROM businesses b JOIN categories cat ON cat.id = b.category_id
		WHERE b.status = 'verified' AND b.deleted_at IS NULL AND b.city = $1
		GROUP BY b.category_id, cat.name, cat.slug ORDER BY count(*) DESC LIMIT 12`, chosen.name)
	if err == nil {
		for catRows.Next() {
			var cc CityCategory
			if err := catRows.Scan(&cc.CategoryID, &cc.CategoryName, &cc.CategorySlug, &cc.Count); err != nil {
				break
			}
			page.Categories = append(page.Categories, cc)
		}
		catRows.Close()
	}

	// Trending leaders in the city (latest 24h snapshot).
	var takenAt time.Time
	_ = c.repos.QueryRow(ctx,
		`SELECT max(taken_at) FROM trend_snapshots WHERE period = '24h'`).Scan(&takenAt)
	page.UpdatedAt = takenAt
	if !takenAt.IsZero() {
		topRows, err := c.repos.Query(ctx, `
			SELECT b.id, b.name, b.slug, b.logo_url, b.city, cat.name, s.score, s.velocity,
				s.is_booming, s.is_rising, b.verification_level, s.rank_city
			FROM trend_snapshots s
			JOIN businesses b ON b.id = s.business_id
			LEFT JOIN categories cat ON cat.id = b.category_id
			WHERE s.period = '24h' AND s.taken_at = $1 AND b.city = $2
			ORDER BY s.rank_city NULLS LAST, s.score DESC LIMIT 8`, takenAt, chosen.name)
		if err == nil {
			for topRows.Next() {
				var e domain.TrendEntry
				var rank int
				if err := topRows.Scan(&e.ID, &e.Name, &e.Slug, &e.LogoURL, &e.City, &e.Category,
					&e.Score, &e.Velocity, &e.IsBooming, &e.IsRising, &e.VerificationLevel, &rank); err != nil {
					break
				}
				e.RankCity = rank
				page.Top = append(page.Top, &e)
			}
			topRows.Close()
		}
	}

	// Top-rated fallback list.
	rated, err := c.repos.Businesses.Search(ctx, `
		SELECT `+repo.BusinessCols+repo.BusinessCounts+`, NULL::float8 AS distance_km, 0 AS ts_rank
		FROM businesses b JOIN categories cat ON cat.id = b.category_id
		WHERE b.status = 'verified' AND b.deleted_at IS NULL AND b.city = $1
		ORDER BY rating_avg DESC NULLS LAST, b.created_at DESC LIMIT 6`, []any{chosen.name})
	if err == nil {
		page.TopRated = rated
	}
	return page, nil
}

// All returns every city with a count (sitemap + /cities index).
func (c *Cities) All(ctx context.Context) ([]map[string]any, error) {
	rows, err := c.repos.Query(ctx, `
		SELECT city, count(*) FROM businesses
		WHERE status = 'verified' AND deleted_at IS NULL AND city <> ''
		GROUP BY city ORDER BY count(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"name": name, "slug": slugify(name), "count": count})
	}
	return out, rows.Err()
}
