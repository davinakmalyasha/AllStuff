package httpapi

import (
	"strings"
	"testing"

	"bizverse/api/internal/domain"
)

// ssrHours renders the opening-hours block into the server-rendered storefront.
// It had no coverage at all in the last measured profile, despite being part of
// the HTML a crawler reads.
func TestSSRHours(t *testing.T) {
	business := func(hours map[string]any) *domain.Business {
		return &domain.Business{Hours: hours}
	}
	open := map[string]any{"open": "09:00", "close": "17:00", "closed": false}

	t.Run("an open day renders both times", func(t *testing.T) {
		got := ssrHours(business(map[string]any{"mon": open}))
		if !strings.Contains(got, "<dt>Monday</dt>") {
			t.Errorf("missing the day name: %s", got)
		}
		if !strings.Contains(got, "09:00") || !strings.Contains(got, "17:00") {
			t.Errorf("missing the times: %s", got)
		}
	})

	t.Run("a closed day says so", func(t *testing.T) {
		got := ssrHours(business(map[string]any{"sun": map[string]any{"closed": true}}))
		if !strings.Contains(got, "<dt>Sunday</dt><dd>Closed</dd>") {
			t.Errorf("got %s", got)
		}
	})

	t.Run("days are emitted in week order, not map order", func(t *testing.T) {
		// Map iteration is random in Go, so without ssrDayOrder the block would
		// be reordered on every render.
		hours := map[string]any{}
		for _, d := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
			hours[d] = open
		}
		got := ssrHours(business(hours))
		want := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
		at := -1
		for _, day := range want {
			i := strings.Index(got, "<dt>"+day+"</dt>")
			if i < 0 {
				t.Fatalf("%s missing from %s", day, got)
			}
			if i < at {
				t.Errorf("%s appears out of order (at %d, previous at %d)", day, i, at)
			}
			at = i
		}
	})

	t.Run("one row per present day, and only present days", func(t *testing.T) {
		got := ssrHours(business(map[string]any{"mon": open, "fri": open}))
		if n := strings.Count(got, "<div>"); n != 2 {
			t.Errorf("got %d rows, want 2: %s", n, got)
		}
		for _, absent := range []string{"Tuesday", "Sunday", "Thursday"} {
			if strings.Contains(got, absent) {
				t.Errorf("%s should not appear: %s", absent, got)
			}
		}
	})

	t.Run("no hours renders nothing", func(t *testing.T) {
		for _, h := range []map[string]any{nil, {}} {
			if got := ssrHours(business(h)); got != "" {
				t.Errorf("got %q, want empty", got)
			}
		}
	})

	t.Run("malformed entries are skipped rather than rendered as blank rows", func(t *testing.T) {
		got := ssrHours(business(map[string]any{
			"mon": open,
			"tue": nil,                             // present but null
			"wed": "09:00-17:00",                   // a string, not an object
			"thu": []any{"09:00", "17:00"},         // a list, not an object
			"xyz": open,                            // not a day
			"fri": map[string]any{"closed": false}, // open with no times
		}))
		if n := strings.Count(got, "<div>"); n != 2 {
			t.Errorf("got %d rows, want 2 (Monday and Friday): %s", n, got)
		}
		if strings.Contains(got, "xyz") || strings.Contains(got, "Wednesday") || strings.Contains(got, "Thursday") {
			t.Errorf("a malformed or unknown entry was rendered: %s", got)
		}
	})

	t.Run("a day marked open with no times falls back to Closed", func(t *testing.T) {
		// This looks like a defect and is pinned deliberately, because it is not
		// reachable. `when` starts as "Closed" and is only overwritten when a
		// time is present, so an open day carrying neither time renders "Closed".
		//
		// No stored row can be in that shape: validateHours is the only writer of
		// this column, and for a day that is not closed it always stores both
		// times, dropping the day outright if they are missing or malformed. The
		// seed stores the same shape, and no migration inserts or updates the
		// column.
		//
		// Left unchanged rather than "fixed": choosing different output for an
		// unreachable state would be a behaviour change resting on nothing. If a
		// future writer can produce this shape, the right answer is to make that
		// writer validate, not to guess here.
		got := ssrHours(business(map[string]any{"fri": map[string]any{"closed": false}}))
		if !strings.Contains(got, "<dt>Friday</dt>") {
			t.Fatalf("missing Friday: %s", got)
		}
		if !strings.Contains(got, "<dd>Closed</dd>") {
			t.Errorf("the fallback changed; if that was deliberate, update this test and the note above. got: %s", got)
		}
	})

	t.Run("values are escaped", func(t *testing.T) {
		// Hours are validated on write, so this is defence in depth: whatever is
		// in the column reaches the page, and a quote must not close the tag.
		got := ssrHours(business(map[string]any{
			"mon": map[string]any{"open": `09:00"><script>alert(1)</script>`, "close": "17:00"},
		}))
		if strings.Contains(got, "<script>") {
			t.Errorf("markup from the hours column reached the page unescaped: %s", got)
		}
		if !strings.Contains(got, "&lt;script&gt;") {
			t.Errorf("expected the value to be escaped, got: %s", got)
		}
	})

	t.Run("every row is a dt/dd pair inside a div", func(t *testing.T) {
		business := &domain.Business{}
		business.Hours = map[string]any{"mon": open, "sun": map[string]any{"closed": true}}
		got := ssrHours(business)
		if n := strings.Count(got, "<dt>"); n != strings.Count(got, "</dt>") {
			t.Errorf("unbalanced dt tags: %s", got)
		}
		if n := strings.Count(got, "<dd>"); n != strings.Count(got, "</dd>") {
			t.Errorf("unbalanced dd tags: %s", got)
		}
		if n := strings.Count(got, "<div>"); n != strings.Count(got, "</div>") {
			t.Errorf("unbalanced div tags: %s", got)
		}
	})
}
