package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/repo"
)

// SearchAlerts — daily "new matches" emails for saved searches (PRD §5.1.2).
// Runs every 24h: re-runs each alerted query, diffs against the last result
// set, and emails only the genuinely new businesses (cap 10 per email).
type SearchAlerts struct {
	repos  *repo.Repos
	search *Search
	email  email.Sender
	cfg    config.Config
}

func NewSearchAlerts(repos *repo.Repos, sender email.Sender, cfg config.Config) *SearchAlerts {
	return &SearchAlerts{repos: repos, search: NewSearch(repos), email: sender, cfg: cfg}
}

type alertRow struct {
	id, userID, name string
	query            map[string]any
	lastIDs          []string
	email, username  string
}

// SendDaily processes every alerted saved search. Idempotent per row.
func (a *SearchAlerts) SendDaily(ctx context.Context) error {
	rows, err := a.repos.Query(ctx, `
		SELECT ss.id, ss.user_id, ss.name, ss.query, ss.last_result_ids, u.email, u.username
		FROM saved_searches ss JOIN users u ON u.id = ss.user_id
		WHERE ss.notify_daily = true AND u.email_verified_at IS NOT NULL
		ORDER BY ss.id`)
	if err != nil {
		return err
	}
	var alerts []alertRow
	for rows.Next() {
		var r alertRow
		var ids []string
		if err := rows.Scan(&r.id, &r.userID, &r.name, &r.query, &ids, &r.email, &r.username); err != nil {
			rows.Close()
			return err
		}
		r.lastIDs = ids
		alerts = append(alerts, r)
	}
	rows.Close()

	for _, al := range alerts {
		if err := a.sendOne(ctx, al); err != nil {
			// One failing alert never blocks the rest — but log it so
			// silent data loss is at least visible.
			slog.Warn("search alert send failed", "alert", al.id, "err", err)
			continue
		}
	}
	return nil
}

func (a *SearchAlerts) sendOne(ctx context.Context, al alertRow) error {
	results, _, err := a.search.Businesses(ctx, paramsFromQuery(al.query))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, id := range al.lastIDs {
		seen[id] = true
	}
	var fresh []*domain.Business
	for _, b := range results {
		if !seen[b.ID] {
			fresh = append(fresh, b)
		}
		if len(fresh) >= 10 {
			break
		}
	}

	if len(fresh) == 0 {
		// Nothing new: still advance the seen-set so stale entries drop.
		nowIDs := make([]string, 0, len(results))
		for _, b := range results {
			nowIDs = append(nowIDs, b.ID)
		}
		_, _ = a.repos.Exec(ctx, `
			UPDATE saved_searches SET last_result_ids = $2, last_sent_at = now(), updated_at = now() WHERE id = $1`,
			al.id, nowIDs)
		return nil
	}

	var sb strings.Builder
	sb.WriteString(`<p>New businesses matched your saved search <b>` + escapeHTML(al.name) + `</b>:</p><ul>`)
	for _, b := range fresh {
		link := a.cfg.PublicURL + "/b/" + b.Slug
		sb.WriteString(`<li><a href="` + link + `">` + escapeHTML(b.Name) + `</a>`)
		if b.City != "" {
			sb.WriteString(` — ` + escapeHTML(b.City))
		}
		sb.WriteString(`</li>`)
	}
	sb.WriteString(`</ul><p style="color:#999;font-size:12px"><a href="` + a.cfg.PublicURL + `/me" style="color:#999">Manage alerts</a></p>`)

	title := fmt.Sprintf("New matches for %q", al.name)
	if err := a.email.SendBulk(ctx, al.email, title, email.WrapHTML(a.cfg.PublicURL, title, sb.String())); err != nil {
		return err
	}
	// Persist the diff-state ONLY after a successful send — previously the
	// marker was written first, so a failed email meant those matches were
	// marked seen and never delivered.
	nowIDs := make([]string, 0, len(results))
	for _, b := range results {
		nowIDs = append(nowIDs, b.ID)
	}
	_, _ = a.repos.Exec(ctx, `
		UPDATE saved_searches SET last_result_ids = $2, last_sent_at = now(), updated_at = now() WHERE id = $1`,
		al.id, nowIDs)
	return nil
}

// paramsFromQuery converts a saved-search query JSON to SearchParams.
func paramsFromQuery(q map[string]any) SearchParams {
	p := SearchParams{Limit: 20, Sort: "newest"}
	if v, ok := q["q"].(string); ok {
		p.Q = v
	}
	if v, ok := q["city"].(string); ok {
		p.City = v
	}
	if v, ok := q["category"].([]any); ok {
		for _, c := range v {
			if s, ok := c.(string); ok {
				p.CategoryIDs = append(p.CategoryIDs, s)
			}
		}
	}
	if v, ok := q["min_rating"].(float64); ok {
		p.MinRating = v
	}
	if v, ok := q["open_now"].(bool); ok {
		p.OpenNow = v
	}
	if v, ok := q["verified_only"].(bool); ok {
		p.VerifiedOnly = v
	}
	if v, ok := q["has_chat"].(bool); ok {
		p.HasChat = v
	}
	if v, ok := q["price_level"].([]any); ok {
		for _, pl := range v {
			if n, ok := pl.(float64); ok && n >= 1 && n <= 4 {
				p.PriceLevels = append(p.PriceLevels, int(n))
			}
		}
	}
	return p
}

// escapeHTML escapes for an HTML text node and for single-quoted attributes.
//
// `'` is included even though the alert template currently only interpolates
// into element text. The three escapers in this codebase disagreed on this —
// auth.go's htmlEscape includes it, this one did not — and the next template
// edit that moved a value into an attribute would have reopened a stored-XSS
// vector in transactional email. One consistent set, no exceptions.
func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}
