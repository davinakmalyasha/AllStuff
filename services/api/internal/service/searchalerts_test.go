package service

import (
	"reflect"
	"testing"
)

// paramsFromQuery converts a stored saved-search query back into SearchParams
// for the alert job. It had no coverage at all in the last measured profile.
//
// Two things are worth pinning here. The defaults, because a zero Limit would
// make the alert job page through nothing and a missing Sort would change
// ordering silently. And the tolerances, because the input is jsonb that has
// round-tripped through the database: every value arrives as an any, and a type
// it does not expect must be ignored rather than panic or half-apply.
func TestParamsFromQuery(t *testing.T) {
	t.Run("an empty query gets the documented defaults", func(t *testing.T) {
		got := paramsFromQuery(map[string]any{})
		if got.Limit != 20 {
			t.Errorf("Limit = %d, want 20", got.Limit)
		}
		if got.Sort != "newest" {
			t.Errorf("Sort = %q, want newest", got.Sort)
		}
		if got.Q != "" || got.City != "" || len(got.CategoryIDs) != 0 || len(got.PriceLevels) != 0 {
			t.Errorf("expected empty filters, got %#v", got)
		}
		if got.OpenNow || got.VerifiedOnly || got.HasChat || got.MinRating != 0 {
			t.Errorf("expected false/zero filters, got %#v", got)
		}
	})

	t.Run("strings", func(t *testing.T) {
		got := paramsFromQuery(map[string]any{"q": "kopi", "city": "Jakarta"})
		if got.Q != "kopi" || got.City != "Jakarta" {
			t.Errorf("Q = %q, City = %q", got.Q, got.City)
		}
	})

	t.Run("booleans", func(t *testing.T) {
		got := paramsFromQuery(map[string]any{
			"open_now": true, "verified_only": true, "has_chat": false,
		})
		if !got.OpenNow || !got.VerifiedOnly || got.HasChat {
			t.Errorf("got %#v", got)
		}
	})

	t.Run("min_rating", func(t *testing.T) {
		if got := paramsFromQuery(map[string]any{"min_rating": 4.5}); got.MinRating != 4.5 {
			t.Errorf("MinRating = %v, want 4.5", got.MinRating)
		}
	})

	t.Run("category list", func(t *testing.T) {
		got := paramsFromQuery(map[string]any{"category": []any{"food", "drink"}})
		if !reflect.DeepEqual(got.CategoryIDs, []string{"food", "drink"}) {
			t.Errorf("CategoryIDs = %v", got.CategoryIDs)
		}
	})

	t.Run("non-string categories are skipped, not fatal", func(t *testing.T) {
		// jsonb can hold whatever was written; a number in the array must not
		// abort the parse or shift the remaining entries.
		got := paramsFromQuery(map[string]any{"category": []any{"food", 42, nil, "drink"}})
		if !reflect.DeepEqual(got.CategoryIDs, []string{"food", "drink"}) {
			t.Errorf("CategoryIDs = %v, want [food drink]", got.CategoryIDs)
		}
	})

	t.Run("price levels are filtered to 1..4", func(t *testing.T) {
		got := paramsFromQuery(map[string]any{"price_level": []any{
			float64(1), float64(2), float64(4),
			float64(0), float64(5), float64(-1), // out of range
		}})
		if !reflect.DeepEqual(got.PriceLevels, []int{1, 2, 4}) {
			t.Errorf("PriceLevels = %v, want [1 2 4]", got.PriceLevels)
		}
	})

	t.Run("a fractional price level is truncated, not dropped", func(t *testing.T) {
		// JSON numbers decode to float64, so a stored 2.0 arrives as 2.0 and a
		// stored 2.7 arrives as 2.7. Both fall inside the range check and become
		// int(2). Pinned so a change here is deliberate.
		got := paramsFromQuery(map[string]any{"price_level": []any{2.7}})
		if !reflect.DeepEqual(got.PriceLevels, []int{2}) {
			t.Errorf("PriceLevels = %v, want [2]", got.PriceLevels)
		}
	})

	t.Run("wrong types are ignored, leaving the default in place", func(t *testing.T) {
		got := paramsFromQuery(map[string]any{
			"q":             42,
			"city":          []any{"Jakarta"},
			"min_rating":    "4",
			"open_now":      "true",
			"verified_only": 1,
			"has_chat":      nil,
			"category":      "food",       // a bare string, not a list
			"price_level":   []any{"2"},   // strings, not numbers
			"sort":          "trending",   // not read by this function
			"limit":         float64(999), // not read by this function
		})
		if got.Q != "" || got.City != "" || got.MinRating != 0 {
			t.Errorf("string fields were set from the wrong types: %#v", got)
		}
		if got.OpenNow || got.VerifiedOnly || got.HasChat {
			t.Errorf("bool fields were set from the wrong types: %#v", got)
		}
		if len(got.CategoryIDs) != 0 {
			t.Errorf("CategoryIDs = %v, want empty; a bare string is not a list", got.CategoryIDs)
		}
		if len(got.PriceLevels) != 0 {
			t.Errorf("PriceLevels = %v, want empty; strings are not numbers", got.PriceLevels)
		}
		// sort and limit are not part of this mapping. If that ever changes, the
		// alert job starts honouring a stored sort/limit it does not expect.
		if got.Sort != "newest" {
			t.Errorf("Sort = %q; this function does not read a sort from the query", got.Sort)
		}
		if got.Limit != 20 {
			t.Errorf("Limit = %d; this function does not read a limit from the query", got.Limit)
		}
	})
}
