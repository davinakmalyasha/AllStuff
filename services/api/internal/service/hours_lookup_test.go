package service

import (
	"fmt"
	"testing"
	"time"
)

// parseOpenOf and parseCloseOf index the days slice with time.Weekday(), which is
// 0=Sunday, so the slice must be Sunday-first. That coupling is invisible at the
// call site: reordering it to Monday-first - the more natural reading order, and
// the order ssrDayOrder uses elsewhere in the codebase - would shift every lookup
// by one day, with no compile error and no failure anywhere the day is missing.
func TestParseOpenCloseMapsWeekdayToTheRightDay(t *testing.T) {
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

	// Give every day a distinct open time so a one-day shift cannot pass: with
	// identical hours the mapping would be untestable.
	hours := map[string]any{}
	for i, day := range days {
		hours[day] = map[string]any{
			"open":  fmt.Sprintf("%02d:00", i+1), // 01:00 .. 07:00
			"close": fmt.Sprintf("%02d:30", i+1), // 01:30 .. 07:30
		}
	}

	// 2026-10-05 is a Monday. Walk a full week from it, so Sunday is covered too.
	monday := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		d := monday.AddDate(0, 0, i)
		idx := int(d.Weekday())
		wantOpen := (idx + 1) * 60
		wantClose := wantOpen + 30

		if got := parseOpenOf(d, hours, days); got != wantOpen {
			t.Errorf("%s (Weekday %d = %q): parseOpenOf = %d, want %d",
				d.Format("Mon"), idx, days[idx], got, wantOpen)
		}
		if got := parseCloseOf(d, hours, days); got != wantClose {
			t.Errorf("%s (Weekday %d = %q): parseCloseOf = %d, want %d",
				d.Format("Mon"), idx, days[idx], got, wantClose)
		}
	}

	// And the front of the slice really is Sunday, so the check above is not
	// passing for some other reason.
	sunday := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if sunday.Weekday() != time.Sunday {
		t.Fatalf("the fixture date is wrong: %s is %s", sunday.Format("2006-01-02"), sunday.Weekday())
	}
}

// A day the function cannot read means "no hours known", which the caller treats
// as closed. The two accessors are independent - parseOpenOf reads only "open",
// parseCloseOf only "close" - so a day missing one still yields a value for the
// other. That is pinned per-column below, because asserting both return zero
// together would be asserting they are the same function.
func TestParseOpenCloseReturnsZeroWhenUnknown(t *testing.T) {
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	monday := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	const nine, seventeen = 9 * 60, 17 * 60

	cases := []struct {
		name      string
		hours     map[string]any
		wantOpen  int
		wantClose int
	}{
		{"no hours at all", nil, 0, 0},
		{"the day is absent", map[string]any{"tue": map[string]any{"open": "09:00", "close": "17:00"}}, 0, 0},
		{"the entry is null", map[string]any{"mon": nil}, 0, 0},
		{"the entry is a string", map[string]any{"mon": "09:00-17:00"}, 0, 0},
		{"the entry is a list", map[string]any{"mon": []any{"09:00", "17:00"}}, 0, 0},
		{"a closed day carries no times", map[string]any{"mon": map[string]any{"closed": true}}, 0, 0},

		// One column unreadable, the other still usable.
		{"open is missing", map[string]any{"mon": map[string]any{"close": "17:00"}}, 0, seventeen},
		{"close is missing", map[string]any{"mon": map[string]any{"open": "09:00"}}, nine, 0},
		{"open is not a string", map[string]any{"mon": map[string]any{"open": 900, "close": "17:00"}}, 0, seventeen},
		{"close is not a string", map[string]any{"mon": map[string]any{"open": "09:00", "close": 1700}}, nine, 0},
		{"open is malformed", map[string]any{"mon": map[string]any{"open": "9:00", "close": "17:00"}}, 0, seventeen},
		{"close is out of range", map[string]any{"mon": map[string]any{"open": "09:00", "close": "24:00"}}, nine, 0},
	}
	for _, tc := range cases {
		if got := parseOpenOf(monday, tc.hours, days); got != tc.wantOpen {
			t.Errorf("%s: parseOpenOf = %d, want %d", tc.name, got, tc.wantOpen)
		}
		if got := parseCloseOf(monday, tc.hours, days); got != tc.wantClose {
			t.Errorf("%s: parseCloseOf = %d, want %d", tc.name, got, tc.wantClose)
		}
	}
}

// The boundaries of a valid HH:MM, since the range check is what stops a 25:00
// becoming a time that sorts past a legitimate close.
func TestParseOpenCloseAcceptsOnlyRealTimes(t *testing.T) {
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	monday := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	valid := map[string]int{"00:00": 0, "00:01": 1, "23:59": 23*60 + 59, "12:30": 12*60 + 30}
	for tm, want := range valid {
		hours := map[string]any{"mon": map[string]any{"open": tm, "close": "23:59"}}
		if got := parseOpenOf(monday, hours, days); got != want {
			t.Errorf("open %s = %d, want %d", tm, got, want)
		}
	}

	invalid := []string{"", "9:00", "09:0", "0900", "09-00", "09:60", "24:00", "-1:00", "ab:cd", "09:00 "}
	for _, tm := range invalid {
		hours := map[string]any{"mon": map[string]any{"open": tm, "close": "17:00"}}
		if got := parseOpenOf(monday, hours, days); got != 0 {
			t.Errorf("open %q = %d, want 0 for a malformed time", tm, got)
		}
	}
}
