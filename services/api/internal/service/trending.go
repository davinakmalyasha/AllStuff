package service

import (
	"context"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Trending — the engagement-driven ranking engine (PRD §5.6.3, §3 model).
//
//	score(t) = Σ weight × e^(−λ·age_hours)   per window (24h/7d/30d)
//	velocity  = current 24h score − previous 24h score
//	Booming   = top N by 24h velocity
//	Rising    = top N by velocity ÷ baseline(age + trailing score) — hidden gems
//
// Anti-gaming (§8.6): owner self-actions excluded at write time, dedupe keys,
// and spike exclusion when 24h score > 10× the 30d average. Detected spikes
// flag the responsible events and queue a trend anomaly for admins.
type Trending struct {
	repos    *repo.Repos
	notifier *Notifier
}

func NewTrending(repos *repo.Repos, notifier *Notifier) *Trending {
	return &Trending{repos: repos, notifier: notifier}
}

// TrendingConfig holds the admin-tunable engine knobs (PRD §5.8.5
// leaderboard config); stored in site_config under key "trending".
type TrendingConfig struct {
	Lambda24h    float64
	Lambda7d     float64
	Lambda30d    float64
	BoomingN     int
	RisingN      int
	RisingPaused bool // M18: freeze the Rising strip instead of rotating it
}

func defaultTrendingConfig() TrendingConfig {
	return TrendingConfig{Lambda24h: 0.03, Lambda7d: 0.006, Lambda30d: 0.002, BoomingN: 25, RisingN: 20}
}

func (t *Trending) loadConfig(ctx context.Context) TrendingConfig {
	cfg := defaultTrendingConfig()
	var raw map[string]any
	if err := t.repos.QueryRow(ctx,
		`SELECT value FROM site_config WHERE key = 'trending'`).Scan(&raw); err != nil || raw == nil {
		return cfg
	}
	num := func(k string, def float64) float64 {
		if v, ok := raw[k].(float64); ok && v > 0 {
			return v
		}
		return def
	}
	intOf := func(k string, def int) int {
		if v, ok := raw[k].(float64); ok && v >= 1 {
			return int(v)
		}
		return def
	}
	cfg.Lambda24h = num("lambda_24h", cfg.Lambda24h)
	cfg.Lambda7d = num("lambda_7d", cfg.Lambda7d)
	cfg.Lambda30d = num("lambda_30d", cfg.Lambda30d)
	cfg.BoomingN = intOf("booming_n", cfg.BoomingN)
	cfg.RisingN = intOf("rising_n", cfg.RisingN)
	if v, ok := raw["rising_paused"].(bool); ok {
		cfg.RisingPaused = v
	}
	return cfg
}

// Compute runs the full recompute: scores → snapshots → Booming/Rising flags.
func (t *Trending) Compute(ctx context.Context) error {
	cfg := t.loadConfig(ctx)
	windows := []struct {
		period string
		lambda float64
		hours  float64
	}{
		{"24h", cfg.Lambda24h, 24},
		{"7d", cfg.Lambda7d, 168},
		{"30d", cfg.Lambda30d, 720},
	}
	for _, w := range windows {
		// Score per business (verified only, un-flagged events).
		if _, err := t.repos.Exec(ctx, `
			INSERT INTO trend_snapshots (id, period, business_id, score, taken_at)
			SELECT gen_random_uuid(), $1, b.id,
				coalesce(SUM(e.weight * exp(-$2::float8 * EXTRACT(EPOCH FROM (now() - e.occurred_at)) / 3600.0)), 0),
				now()
			FROM businesses b
			LEFT JOIN engagement_events e ON e.target_type='business' AND e.target_id = b.id
				AND e.occurred_at > now() - ($3::float8 * interval '1 hour') AND e.flagged = false
			WHERE b.status = 'verified' AND b.deleted_at IS NULL
			GROUP BY b.id`,
			w.period, w.lambda, w.hours); err != nil {
			return err
		}
	}

	// Velocity + ranks + Booming for the 24h window (vs the previous snapshot).
	// rank_category/rank_city power category pages and owner analytics (§5.6.3).
	_, err := t.repos.Exec(ctx, `
		WITH maxes AS (
			SELECT max(taken_at) AS latest,
				max(taken_at) FILTER (WHERE taken_at < (SELECT max(taken_at) FROM trend_snapshots WHERE period='24h')) AS prev
			FROM trend_snapshots WHERE period='24h'
		),
		prev AS (
			SELECT s.business_id, s.score AS prev_score
			FROM trend_snapshots s JOIN maxes m ON true
			WHERE s.period='24h' AND s.taken_at = m.prev
		),
		ranked AS (
			SELECT s.business_id, s.score, p.prev_score,
				row_number() OVER (ORDER BY (s.score - coalesce(p.prev_score, 0)) DESC) AS rank_global,
				row_number() OVER (PARTITION BY b.category_id ORDER BY s.score DESC) AS rank_category,
				row_number() OVER (PARTITION BY b.city ORDER BY s.score DESC) AS rank_city
			FROM trend_snapshots s JOIN maxes m ON true
			LEFT JOIN prev p ON p.business_id = s.business_id
			JOIN businesses b ON b.id = s.business_id
			WHERE s.period='24h' AND s.taken_at = m.latest
		)
		UPDATE trend_snapshots s
		SET velocity = r.score - coalesce(r.prev_score, 0),
			rank_global = r.rank_global,
			rank_category = r.rank_category,
			rank_city = r.rank_city,
			is_booming = r.rank_global <= $1
		FROM ranked r
		WHERE s.business_id = r.business_id AND s.period='24h'
			AND s.taken_at = (SELECT max(taken_at) FROM trend_snapshots WHERE period='24h')`, cfg.BoomingN)
	if err != nil {
		return err
	}
	if err := t.markRising(ctx, cfg); err != nil {
		return err
	}
	return t.flagSpikes(ctx)
}

// flagSpikes implements the anti-gaming rule (PRD §8.6, ARCHITECTURE §4):
// when a business's 24h score exceeds 10× its trailing 30d baseline (and an
// absolute floor), its last-24h engagement events are flagged — excluding
// them from the next recompute — and admins get a trend_anomaly notification.
func (t *Trending) flagSpikes(ctx context.Context) error {
	rows, err := t.repos.Query(ctx, `
		WITH latest AS (
			SELECT business_id, score FROM trend_snapshots
			WHERE period='24h' AND taken_at = (SELECT max(taken_at) FROM trend_snapshots WHERE period='24h')
		),
		base AS (
			SELECT business_id, score AS score30 FROM trend_snapshots
			WHERE period='30d' AND taken_at = (SELECT max(taken_at) FROM trend_snapshots WHERE period='30d')
		)
		SELECT l.business_id FROM latest l LEFT JOIN base b ON b.business_id = l.business_id
		WHERE l.score > 25 AND l.score > 10 * coalesce(b.score30, 0)`)
	if err != nil {
		return err
	}
	var spiked []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			spiked = append(spiked, id)
		}
	}
	rows.Close()
	if len(spiked) == 0 {
		return nil
	}
	if _, err := t.repos.Exec(ctx, `
		UPDATE engagement_events SET flagged = true
		WHERE target_type='business' AND target_id = ANY($1)
		  AND occurred_at > now() - interval '24 hours' AND flagged = false`, spiked); err != nil {
		return err
	}
	admins, err := t.repos.Query(ctx,
		`SELECT id FROM users WHERE role='admin' AND status='active' AND deleted_at IS NULL`)
	if err != nil {
		return nil // best-effort notify
	}
	defer admins.Close()
	for admins.Next() {
		var adminID string
		if err := admins.Scan(&adminID); err != nil {
			continue
		}
		if t.notifier != nil {
			t.notifier.Create(ctx, adminID, "trend_anomaly", map[string]any{
				"business_ids": spiked, "reason": "Engagement spike detected; events flagged for review.",
			})
		}
	}
	return nil
}

// markRising: hidden gems — velocity normalized by age + trailing score.
// §8.6 rules enforced here:
//   - a business cannot hold Booming and Rising simultaneously (M23)
//   - eligibility resets weekly: businesses rising in last week's final
//     snapshot sit out this week so the badge rotates (M24)
//   - when RisingPaused is set the strip freezes as-is (M18)
func (t *Trending) markRising(ctx context.Context, cfg TrendingConfig) error {
	if cfg.RisingPaused {
		return nil
	}
	_, err := t.repos.Exec(ctx, `
		UPDATE trend_snapshots SET is_rising = false WHERE period='24h'
			AND taken_at = (SELECT max(taken_at) FROM trend_snapshots WHERE period='24h')`)
	if err != nil {
		return err
	}
	_, err = t.repos.Exec(ctx, `
		WITH latest AS (
			SELECT s.business_id, s.velocity, s.is_booming,
				b.created_at,
				(SELECT score FROM trend_snapshots WHERE period='30d' AND business_id = s.business_id
				 ORDER BY taken_at DESC LIMIT 1) AS score30
			FROM trend_snapshots s JOIN businesses b ON b.id = s.business_id
			WHERE s.period='24h' AND s.taken_at = (SELECT max(taken_at) FROM trend_snapshots WHERE period='24h')
		),
		ranked AS (
			SELECT business_id, velocity,
				velocity / (5.0 + coalesce(score30,0)*0.2 + extract(epoch from (now() - created_at))/86400.0*0.05) AS normalized,
				row_number() OVER (ORDER BY velocity / (5.0 + coalesce(score30,0)*0.2 + extract(epoch from (now() - created_at))/86400.0*0.05) DESC) AS rn
			FROM latest
			WHERE velocity > 0 AND NOT coalesce(is_booming, false) -- M23
			  AND business_id NOT IN ( -- M24 weekly rotation
				SELECT business_id FROM trend_snapshots old
				WHERE old.period='24h' AND old.is_rising = true
				  AND old.taken_at = (
					SELECT max(taken_at) FROM trend_snapshots
					WHERE period='24h'
					  AND taken_at >= date_trunc('week', now()) - interval '7 days'
					  AND taken_at < date_trunc('week', now())
				  )
			  )
		)
		UPDATE trend_snapshots s SET is_rising = true
		FROM ranked r
		WHERE s.period='24h' AND s.business_id = r.business_id AND r.rn <= $1
		  AND s.taken_at = (SELECT max(taken_at) FROM trend_snapshots WHERE period='24h')`, cfg.RisingN)
	return err
}

// Leaderboard returns the ranked list for a window/scope (PRD §5.1.5).
func (t *Trending) Leaderboard(ctx context.Context, period, scope string, limit int) ([]*domain.TrendEntry, error) {	if limit <= 0 || limit > 100 {
		limit = 50
	}
	base := `
		SELECT b.id, b.name, b.slug, b.logo_url, b.city, cat.name AS category,
			s.score, s.velocity, s.is_booming, s.is_rising,
			b.verification_level
		FROM trend_snapshots s
		JOIN businesses b ON b.id = s.business_id
		LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE s.period = $1 AND s.taken_at = (SELECT max(taken_at) FROM trend_snapshots WHERE period = $1)`
	var args []any
	args = append(args, period)
	order := "s.score DESC"
	switch {
	case scope == "booming":
		order = "s.velocity DESC"
	case scope == "rising":
		base += " AND s.is_rising = true"
		order = "s.velocity DESC"
	case len(scope) > 9 && scope[:9] == "category:":
		base += " AND b.category_id = $2"
		args = append(args, scope[9:])
	case len(scope) > 5 && scope[:5] == "city:":
		base += " AND b.city = $2"
		args = append(args, scope[5:])
	}
	args = append(args, limit)
	base += " ORDER BY " + order + " LIMIT $" + util.Itoa(len(args))

	rows, err := t.repos.Query(ctx, base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.TrendEntry
	for rows.Next() {
		var e domain.TrendEntry
		if err := rows.Scan(&e.ID, &e.Name, &e.Slug, &e.LogoURL, &e.City, &e.Category,
			&e.Score, &e.Velocity, &e.IsBooming, &e.IsRising, &e.VerificationLevel); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// CategoryLeaderboard: score-ranked top N within a category plus guaranteed
// Rising placements (PRD §5.6.3: "Rising top-3 guaranteed"). Returns the
// snapshot time so UIs can show "updated X min ago".
func (t *Trending) CategoryLeaderboard(ctx context.Context, categoryID string, limit int) ([]*domain.TrendEntry, time.Time, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	var takenAt time.Time
	if err := t.repos.QueryRow(ctx, `
		SELECT max(taken_at) FROM trend_snapshots WHERE period='24h'`).Scan(&takenAt); err != nil {
		return nil, time.Time{}, err
	}

	// 1. Score-ranked top N in the category.
	score, err := t.categoryEntries(ctx, categoryID, takenAt, limit, false)
	if err != nil {
		return nil, time.Time{}, err
	}
	// 2. Rising (velocity > 0) entries in the category, any score rank.
	rising, err := t.categoryEntries(ctx, categoryID, takenAt, 3, true)
	if err != nil {
		return nil, time.Time{}, err
	}
	seen := map[string]bool{}
	var out []*domain.TrendEntry
	for _, e := range append(score, rising...) {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	if len(out) > limit+3 {
		out = out[:limit+3]
	}
	return out, takenAt, nil
}

func (t *Trending) categoryEntries(ctx context.Context, categoryID string, takenAt time.Time, limit int, risingOnly bool) ([]*domain.TrendEntry, error) {
	base := `
		SELECT b.id, b.name, b.slug, b.logo_url, b.city, cat.name AS category,
			s.score, s.velocity, s.is_booming, s.is_rising, b.verification_level, s.rank_category
		FROM trend_snapshots s
		JOIN businesses b ON b.id = s.business_id
		LEFT JOIN categories cat ON cat.id = b.category_id
		WHERE s.period='24h' AND s.taken_at = $1 AND b.category_id = $2`
	var order string
	if risingOnly {
		base += " AND s.is_rising = true AND s.velocity > 0"
		order = "s.velocity DESC"
	} else {
		order = "s.score DESC"
	}
	base += " ORDER BY " + order + " LIMIT $3"
	rows, err := t.repos.Query(ctx, base, takenAt, categoryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.TrendEntry
	for rows.Next() {
		var e domain.TrendEntry
		var rank int
		if err := rows.Scan(&e.ID, &e.Name, &e.Slug, &e.LogoURL, &e.City, &e.Category,
			&e.Score, &e.Velocity, &e.IsBooming, &e.IsRising, &e.VerificationLevel, &rank); err != nil {
			return nil, err
		}
		e.RankCategory = rank
		out = append(out, &e)
	}
	return out, rows.Err()
}

var _ = repo.Repos{}
