package httpapi

import (
	"testing"

	"bizverse/api/internal/domain"
)

// ssrOpeningHours builds the schema.org OpeningHoursSpecification list that goes
// into the storefront's JSON-LD. It had no coverage at all in the last measured
// profile.
//
// It is not the same function as ssrHours even though both read b.Hours: this one
// emits structured data for a crawler and therefore omits a day rather than
// describing it, while ssrHours renders a visible row and says "Closed". The
// difference is deliberate and pinned below.
func TestSSROpeningHours(t *testing.T) {
	open := map[string]any{"open": "09:00", "close": "17:00", "closed": false}

	t.Run("emits the schema.org shape", func(t *testing.T) {
		got := ssrOpeningHours(&domain.Business{Hours: map[string]any{"mon": open}})
		if len(got) != 1 {
			t.Fatalf("got %d entries, want 1", len(got))
		}
		e := got[0]
		if e["@type"] != "OpeningHoursSpecification" {
			t.Errorf("@type = %#v", e["@type"])
		}
		if e["dayOfWeek"] != "https://schema.org/Monday" {
			t.Errorf("dayOfWeek = %#v", e["dayOfWeek"])
		}
		if e["opens"] != "09:00" || e["closes"] != "17:00" {
			t.Errorf("opens/closes = %#v/%#v", e["opens"], e["closes"])
		}
	})

	t.Run("a closed day is omitted, not described", func(t *testing.T) {
		// Omitting is the correct encoding: schema.org has no way to say "closed
		// on Monday" in this shape, and emitting an entry with no opens/closes
		// would tell a crawler something false.
		got := ssrOpeningHours(&domain.Business{Hours: map[string]any{
			"mon": open, "sun": map[string]any{"closed": true},
		}})
		if len(got) != 1 {
			t.Fatalf("got %d entries, want only Monday", len(got))
		}
		if got[0]["dayOfWeek"] != "https://schema.org/Monday" {
			t.Errorf("got %#v", got[0])
		}
	})

	t.Run("a closed day carrying times is still omitted", func(t *testing.T) {
		// The case the closed check actually decides. A day with closed:true AND
		// both times passes the opens/closes guard below, so without the closed
		// check it would be published to crawlers as open - the exact opposite of
		// what the row says.
		//
		// validateHours cannot produce this shape: it stores {"closed": true} and
		// nothing else for a closed day. But the column is jsonb, so a hand edit
		// or an older row can, and "unreachable through the current writer" is not
		// the same as "cannot exist".
		got := ssrOpeningHours(&domain.Business{Hours: map[string]any{
			"sun": map[string]any{"closed": true, "open": "09:00", "close": "17:00"},
		}})
		if len(got) != 0 {
			t.Errorf("a day marked closed was published as open: %#v", got)
		}
	})

	t.Run("a day missing either time is omitted", func(t *testing.T) {
		got := ssrOpeningHours(&domain.Business{Hours: map[string]any{
			"mon": map[string]any{"open": "09:00"},
			"tue": map[string]any{"close": "17:00"},
			"wed": open,
		}})
		if len(got) != 1 || got[0]["dayOfWeek"] != "https://schema.org/Wednesday" {
			t.Errorf("got %#v, want only Wednesday", got)
		}
	})

	t.Run("malformed entries are skipped", func(t *testing.T) {
		got := ssrOpeningHours(&domain.Business{Hours: map[string]any{
			"mon": nil,
			"tue": "09:00-17:00",
			"wed": []any{"09:00", "17:00"},
			"xyz": open,
			"thu": open,
		}})
		if len(got) != 1 || got[0]["dayOfWeek"] != "https://schema.org/Thursday" {
			t.Errorf("got %#v, want only Thursday", got)
		}
	})

	t.Run("no hours yields nothing", func(t *testing.T) {
		for _, h := range []map[string]any{nil, {}} {
			if got := ssrOpeningHours(&domain.Business{Hours: h}); len(got) != 0 {
				t.Errorf("got %#v, want nothing", got)
			}
		}
	})

	t.Run("entries follow week order", func(t *testing.T) {
		// The rows come from a map, so without ssrDayOrder the structured data
		// would be reordered on every render.
		hours := map[string]any{}
		for _, d := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
			hours[d] = open
		}
		got := ssrOpeningHours(&domain.Business{Hours: hours})
		want := []string{
			"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday",
		}
		if len(got) != len(want) {
			t.Fatalf("got %d entries, want %d", len(got), len(want))
		}
		for i, day := range want {
			if got[i]["dayOfWeek"] != "https://schema.org/"+day {
				t.Errorf("entry %d = %#v, want %s", i, got[i]["dayOfWeek"], day)
			}
		}
	})

	t.Run("the two hours renderers disagree on a closed day, on purpose", func(t *testing.T) {
		// ssrHours shows "Closed"; ssrOpeningHours omits the day. Asserted side by
		// side because consolidating them would look like a cleanup and would
		// break one of the two outputs.
		b := &domain.Business{Hours: map[string]any{"sun": map[string]any{"closed": true}}}
		if n := len(ssrOpeningHours(b)); n != 0 {
			t.Errorf("ssrOpeningHours emitted %d entries for a closed day, want 0", n)
		}
		if html := ssrHours(b); html == "" {
			t.Error("ssrHours should still render the day as Closed")
		}
	})
}
