package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Currency — read-time conversion (PRD D5). Rates synced hourly from a free
// feed into currency_rates; conversions are display-only (originals stored).
type Currency struct {
	repos *repo.Repos
}

func NewCurrency(repos *repo.Repos) *Currency { return &Currency{repos: repos} }

// Sync fetches USD-based rates from open.er-api.com (free, no key).
func (c *Currency) Sync(ctx context.Context) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://open.er-api.com/v6/latest/USD")
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
	for code, rate := range payload.Rates {
		_, err := c.repos.Exec(ctx, `
			INSERT INTO currency_rates (id, code, rate_usd, fetched_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (code) DO UPDATE SET rate_usd = EXCLUDED.rate_usd, fetched_at = now()`,
			util.NewUUID(), code, rate)
		if err != nil {
			return err
		}
	}
	return nil
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
