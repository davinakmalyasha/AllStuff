package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"bizverse/api/internal/repo"
)

// Currency — read-time conversion (PRD D5). Rates synced hourly from a free
// feed into currency_rates; conversions are display-only (originals stored).
type Currency struct {
	repos *repo.Repos
}

func NewCurrency(repos *repo.Repos) *Currency { return &Currency{repos: repos} }

// Sync fetches USD-based rates from open.er-api.com (free, no key).
// Single batched upsert (was ~160 individual Execs per hour) with a
// ctx-bound request so shutdown can cancel the fetch, and sanity-checked
// rates (≤0 would poison downstream division/inversion math).
func (c *Currency) Sync(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://open.er-api.com/v6/latest/USD", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("rates feed: %s", resp.Status)
	}
	var payload struct {
		Result string             `json:"result"`
		Rates  map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	if payload.Result != "success" || len(payload.Rates) == 0 {
		return fmt.Errorf("rates feed: unexpected payload")
	}

	codes := make([]string, 0, len(payload.Rates))
	vals := make([]float64, 0, len(payload.Rates))
	for code, rate := range payload.Rates {
		if rate <= 0 || len(code) != 3 || code != strings.ToUpper(code) {
			continue
		}
		codes = append(codes, code)
		vals = append(vals, rate)
	}
	if len(codes) == 0 {
		return fmt.Errorf("rates feed: no valid rates")
	}
	_, err = c.repos.Exec(ctx, `
		INSERT INTO currency_rates (id, code, rate_usd, fetched_at)
		SELECT gen_random_uuid(), t.code, t.rate, now()
		FROM unnest($1::char(3)[], $2::float8[]) AS t(code, rate)
		ON CONFLICT (code) DO UPDATE SET rate_usd = EXCLUDED.rate_usd, fetched_at = now()`,
		codes, vals)
	return err
}

// Rates returns the full rate map + freshness.
func (c *Currency) Rates(ctx context.Context) (map[string]float64, time.Time, error) {
	rows, err := c.repos.Query(ctx, `SELECT code, rate_usd, fetched_at FROM currency_rates`)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	rates := map[string]float64{"USD": 1}
	updated := time.Time{}
	for rows.Next() {
		var code string
		var rate float64
		var at time.Time
		if err := rows.Scan(&code, &rate, &at); err != nil {
			return nil, time.Time{}, err
		}
		rates[code] = rate
		if at.After(updated) {
			updated = at
		}
	}
	return rates, updated, rows.Err()
}
