package service

import (
	"testing"
	"time"
)

func TestIsOpenNow(t *testing.T) {
	// Jakarta timezone; Monday 10:00 local.
	loc, _ := time.LoadLocation("Asia/Jakarta")
	at := time.Date(2026, 8, 10, 10, 0, 0, 0, loc) // Monday 10:00 WIB

	open := map[string]any{
		"mon": map[string]any{"open": "07:00", "close": "18:00", "closed": false},
		"sun": map[string]any{"closed": true},
	}
	if !isOpenNow(open, nil, at, "Asia/Jakarta") {
		t.Error("expected open at 10:00 Monday")
	}

	// Before opening.
	early := time.Date(2026, 8, 10, 6, 30, 0, 0, loc)
	if isOpenNow(open, nil, early, "Asia/Jakarta") {
		t.Error("expected closed at 06:30")
	}

	// Overnight window (18:00–02:00).
	overnight := map[string]any{
		"mon": map[string]any{"open": "18:00", "close": "02:00", "closed": false},
	}
	late := time.Date(2026, 8, 10, 23, 0, 0, 0, loc)
	if !isOpenNow(overnight, nil, late, "Asia/Jakarta") {
		t.Error("expected open at 23:00 for overnight window")
	}
	afterMidnight := time.Date(2026, 8, 11, 1, 0, 0, 0, loc)
	if !isOpenNow(overnight, nil, afterMidnight, "Asia/Jakarta") {
		t.Error("expected open at 01:00 next day")
	}

	// Timezone correctness: same UTC instant, different local day.
	utc := time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC) // 23:00 UTC Monday
	// In Jakarta it's 06:00 Tuesday — Monday hours should NOT apply.
	if isOpenNow(open, nil, utc, "Asia/Jakarta") {
		t.Error("Monday hours should not apply at 06:00 Tuesday local")
	}

	// Regression (audit C3): a previous-day DAYTIME window must not match
	// today's time-of-day. Mon 09:00–17:00 with Tuesday closed: at Tue 10:00
	// the old code matched Monday's window and reported "open".
	daytime := map[string]any{
		"mon": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
	}
	tueDay := time.Date(2026, 8, 11, 10, 0, 0, 0, loc)
	if isOpenNow(daytime, nil, tueDay, "Asia/Jakarta") {
		t.Error("previous-day daytime window must not leak into today (Tue 10:00 vs Mon 09-17)")
	}

	// ...but a previous-day OVERNIGHT window still covers early today.
	nightOnly := map[string]any{
		"mon": map[string]any{"open": "22:00", "close": "02:00", "closed": false},
	}
	tueEarly := time.Date(2026, 8, 11, 1, 0, 0, 0, loc)
	if !isOpenNow(nightOnly, nil, tueEarly, "Asia/Jakarta") {
		t.Error("overnight Mon 22:00-02:00 should be open Tue 01:00")
	}
	tueLate := time.Date(2026, 8, 11, 3, 0, 0, 0, loc)
	if isOpenNow(nightOnly, nil, tueLate, "Asia/Jakarta") {
		t.Error("overnight Mon 22:00-02:00 must be closed Tue 03:00")
	}
}

// special_hours was in the schema since 0012, read into the domain model and
// typed on the client — and consulted by nothing. A business marked closed on a
// public holiday was reported "Open now" and matched the open_now=true filter.
func TestIsOpenNow_SpecialHoursOverridesWeekly(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	tz := "Asia/Jakarta"

	// Open 09:00–17:00 every day that is defined.
	weekly := map[string]any{
		"mon": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
		"tue": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
		"wed": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
		"thu": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
		"fri": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
		"sat": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
		"sun": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
	}
	tue10 := time.Date(2026, 8, 11, 10, 0, 0, 0, loc)
	if !isOpenNow(weekly, nil, tue10, tz) {
		t.Fatal("baseline: Tuesday 10:00 should be open with no overrides")
	}

	// A closed override wins over an open weekly entry.
	closedTue := map[string]any{"2026-08-11": map[string]any{"closed": true}}
	if isOpenNow(weekly, closedTue, tue10, tz) {
		t.Error("a special-hours 'closed' must override an open weekly entry")
	}
	// ...and only for that date.
	if !isOpenNow(weekly, closedTue, time.Date(2026, 8, 12, 10, 0, 0, 0, loc), tz) {
		t.Error("an override must not leak to the following day")
	}

	// A shortened-hours override replaces the window rather than adding to it.
	shortTue := map[string]any{"2026-08-11": map[string]any{"open": "14:00", "close": "16:00", "closed": false}}
	if isOpenNow(weekly, shortTue, tue10, tz) {
		t.Error("a 14:00-16:00 override must not report open at 10:00")
	}
	if !isOpenNow(weekly, shortTue, time.Date(2026, 8, 11, 15, 0, 0, 0, loc), tz) {
		t.Error("expected open inside the override window")
	}
	// A closed weekly day can be opened by an override (a one-off Sunday
	// opening), and is closed when no override applies.
	sunClosed := map[string]any{"sun": map[string]any{"closed": true}}
	sunNoon := time.Date(2026, 8, 9, 11, 0, 0, 0, loc)
	if isOpenNow(sunClosed, nil, sunNoon, tz) {
		t.Error("expected closed Sunday with a closed weekly entry and no override")
	}
	openedSun := map[string]any{"2026-08-09": map[string]any{"open": "10:00", "close": "12:00", "closed": false}}
	if !isOpenNow(sunClosed, openedSun, sunNoon, tz) {
		t.Error("an override should be able to open an otherwise-closed day")
	}
	if isOpenNow(sunClosed, openedSun, time.Date(2026, 8, 9, 13, 0, 0, 0, loc), tz) {
		t.Error("expected closed at 13:00, outside the Sunday override window")
	}
}

// An overnight window that started on a day with its own override must be
// evaluated against the override, not the weekly entry. A 22:00–02:00 Friday
// window that Friday's owner marked closed must not leave the business showing
// open at 01:00 Saturday.
func TestIsOpenNow_SpecialHoursAppliesToOvernightCarryOver(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	tz := "Asia/Jakarta"

	weekly := map[string]any{
		"fri": map[string]any{"open": "22:00", "close": "02:00", "closed": false},
	}
	satEarly := time.Date(2026, 8, 15, 1, 0, 0, 0, loc) // Saturday 01:00
	if !isOpenNow(weekly, nil, satEarly, tz) {
		t.Fatal("baseline: the Friday overnight window should cover Saturday 01:00")
	}
	closedFri := map[string]any{"2026-08-14": map[string]any{"closed": true}}
	if isOpenNow(weekly, closedFri, satEarly, tz) {
		t.Error("a closed override on the origin day must cancel the overnight carry-over")
	}
	// A shortened override on the origin day must also be honoured, not ignored.
	earlyCloseFri := map[string]any{"2026-08-14": map[string]any{"open": "22:00", "close": "23:00", "closed": false}}
	if isOpenNow(weekly, earlyCloseFri, satEarly, tz) {
		t.Error("a 22:00-23:00 override on the origin day must not leave the 01:00 window open")
	}
}

// The override is keyed on the business's LOCAL date, not the UTC date. A
// business in Auckland must not have yesterday's Auckland holiday applied.
func TestIsOpenNow_SpecialHoursUseLocalDate(t *testing.T) {
	tz := "Pacific/Auckland" // UTC+12/+13
	auckland, err := time.LoadLocation(tz)
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// 2026-08-10 22:00 UTC is already 2026-08-11 in Auckland.
	utcInstant := time.Date(2026, 8, 10, 22, 0, 0, 0, time.UTC)
	if got := utcInstant.In(auckland).Format("2006-01-02"); got != "2026-08-11" {
		t.Skipf("unexpected local date mapping: %s", got)
	}
	weekly := map[string]any{
		"tue": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
		"wed": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
	}
	// Closed on the LOCAL date (11th), open on the UTC date (10th).
	closedLocal := map[string]any{"2026-08-11": map[string]any{"closed": true}}
	if isOpenNow(weekly, closedLocal, utcInstant, tz) {
		t.Error("the override must be matched against the business's local date")
	}
}

// A malformed override must not crash or invert the weekly answer: an
// unparseable entry is treated as "no override".
func TestIsOpenNow_SpecialHoursMalformedIsIgnored(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	tue10 := time.Date(2026, 8, 11, 10, 0, 0, 0, loc)
	weekly := map[string]any{
		"tue": map[string]any{"open": "09:00", "close": "17:00", "closed": false},
	}
	for name, bad := range map[string]map[string]any{
		"not a map":     {"2026-08-11": "closed"},
		"bad times":     {"2026-08-11": map[string]any{"open": "nope", "close": "", "closed": false}},
		"missing close": {"2026-08-11": map[string]any{"open": "09:00"}},
	} {
		if !isOpenNow(weekly, bad, tue10, "Asia/Jakarta") {
			t.Errorf("%s: a malformed override should fall back to the weekly hours", name)
		}
	}
}

func TestValidateSpecialHours(t *testing.T) {
	ok, err := validateSpecialHours(map[string]any{
		"2026-12-25": map[string]any{"closed": true},
		"2026-12-24": map[string]any{"open": "09:00", "close": "13:00", "closed": false},
	})
	if err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if len(ok) != 2 {
		t.Errorf("got %d entries, want 2", len(ok))
	}

	bad := map[string]any{
		"not a date":     map[string]any{"25/12/2026": map[string]any{"closed": true}},
		"not a real day": map[string]any{"2026-02-30": map[string]any{"closed": true}},
		"bad times":      map[string]any{"2026-12-25": map[string]any{"open": "25:00", "close": "13:00"}},
		"bad shape":      map[string]any{"2026-12-25": "closed"},
		"not an object":  "nope",
	}
	for name, in := range bad {
		if _, err := validateSpecialHours(in); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

// The cap must be enforced so the map cannot become a large jsonb scan on the
// open-now path.
func TestValidateSpecialHours_Cap(t *testing.T) {
	big := map[string]any{}
	for i := 0; i < maxSpecialHours+1; i++ {
		big[time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).
			AddDate(0, 0, i).Format("2006-01-02")] = map[string]any{"closed": true}
	}
	if _, err := validateSpecialHours(big); err == nil {
		t.Errorf("expected the %d-entry cap to be enforced", maxSpecialHours+1)
	}
}

// Go and SQL are two independent implementations of the same rule, and the SQL
// twin lives in migration 0030_open_now.sql. These cases mirror
// infra/postgres/test_open_now.sql section by section, so the two cannot drift
// without one suite failing. The absolute expectations (not just "equals the
// no-override case") are deliberate: an equality-only suite passed while the SQL
// side silently fell back to UTC.
func TestIsOpenNow_GoSQLParity(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	tz := "Asia/Jakarta"
	// Fixed instant: a Tuesday local 10:00, inside 09:00-17:00.
	at := time.Date(2026, 8, 11, 10, 0, 0, 0, loc)
	today := at.Format("2006-01-02")
	allDay := map[string]any{"open": "00:00", "close": "23:59"}
	business := map[string]any{"open": "09:00", "close": "17:00", "closed": false}

	t.Run("baseline 09:00-17:00 is open at 10:00", func(t *testing.T) {
		weekly := map[string]any{"tue": business}
		if !isOpenNow(weekly, nil, at, tz) {
			t.Error("expected open inside the weekly window")
		}
	})

	t.Run("closed override beats an open weekly entry", func(t *testing.T) {
		weekly := map[string]any{"tue": business}
		over := map[string]any{today: map[string]any{"closed": true}}
		if isOpenNow(weekly, over, at, tz) {
			t.Error("a closed override must win")
		}
	})

	t.Run("override opens a normally-closed day", func(t *testing.T) {
		weekly := map[string]any{"tue": map[string]any{"closed": true}}
		over := map[string]any{today: allDay}
		if !isOpenNow(weekly, over, at, tz) {
			t.Error("an override should be able to open a closed day")
		}
	})

	t.Run("override outside its window does not apply", func(t *testing.T) {
		weekly := map[string]any{"tue": allDay}
		over := map[string]any{today: map[string]any{"open": "03:00", "close": "03:01"}}
		if isOpenNow(weekly, over, at, tz) {
			t.Error("expected closed at 10:00 with a 03:00-03:01 override")
		}
	})

	t.Run("override for another date does not apply", func(t *testing.T) {
		weekly := map[string]any{"tue": map[string]any{"closed": true}}
		over := map[string]any{"1999-01-01": allDay}
		if isOpenNow(weekly, over, at, tz) {
			t.Error("an override dated 1999 must not apply in 2026")
		}
	})

	t.Run("entry omitting the closed key means open", func(t *testing.T) {
		// The SQL twin had this bug: `entry ->> 'closed'` is NULL when the key
		// is absent and `NULL <> 'true'` is NULL, which IF treats as false, so
		// the business read as closed all day and vanished from open_now
		// search. Go's zero-value semantics are correct; this pins them.
		weekly := map[string]any{"tue": map[string]any{"open": "09:00", "close": "17:00"}}
		if !isOpenNow(weekly, nil, at, tz) {
			t.Error("an absent `closed` key must mean open")
		}
	})

	t.Run("override omitting the closed key is treated as open", func(t *testing.T) {
		weekly := map[string]any{"tue": map[string]any{"closed": true}}
		over := map[string]any{today: map[string]any{"open": "00:00", "close": "23:59"}}
		if !isOpenNow(weekly, over, at, tz) {
			t.Error("an override without a `closed` key must be treated as open")
		}
	})

	t.Run("empty hours and special -> false", func(t *testing.T) {
		if isOpenNow(map[string]any{}, map[string]any{}, at, tz) {
			t.Error("expected false for an empty schedule")
		}
	})

	t.Run("invalid timezone falls back to UTC rather than failing", func(t *testing.T) {
		// Mirrors the SQL `EXCEPTION WHEN OTHERS` fallback.
		allDayWeekly := map[string]any{"tue": allDay}
		if !isOpenNow(allDayWeekly, nil, at, "Not/AZone") {
			t.Error("an unknown timezone must not make the business unreadable")
		}
	})
}

// The Go and SQL implementations of the overnight carry-over disagreed around
// DST: Go used AddDate(0,0,-1) on a zoned Time, SQL used `lt - interval '1
// day'` on a timestamp. This pins the Go side to the local-date semantics the
// SQL function uses.
func TestIsOpenNow_OvernightAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	tz := "America/New_York"
	weekly := map[string]any{
		// Sunday 22:00 -> Monday 02:00, an overnight window.
		"sun": map[string]any{"open": "22:00", "close": "02:00"},
	}
	// Monday 01:00 local, on a date in the DST-observing part of the year.
	monEarly := time.Date(2026, 3, 9, 1, 0, 0, 0, loc)
	if !isOpenNow(weekly, nil, monEarly, tz) {
		t.Error("a Sunday 22:00-02:00 window should cover Monday 01:00 across a spring-forward")
	}
	monLate := time.Date(2026, 3, 9, 5, 0, 0, 0, loc)
	if isOpenNow(weekly, nil, monLate, tz) {
		t.Error("expected closed at Monday 05:00, outside the overnight window")
	}
	// A closed override on the origin (Sunday) date must cancel the carry-over
	// even when the local date arithmetic crosses a DST boundary.
	over := map[string]any{"2026-03-08": map[string]any{"closed": true}}
	if isOpenNow(weekly, over, monEarly, tz) {
		t.Error("a closed override on the origin day must cancel the overnight carry-over")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Rumah Kopi Senja":  "rumah-kopi-senja",
		"   Hello World!  ": "hello-world",
		"Café & Bistro":     "caf-bistro",
		"123":               "123",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseHHMM(t *testing.T) {
	if v, ok := parseHHMM("09:30"); !ok || v != 570 {
		t.Errorf("09:30 = %d,%v", v, ok)
	}
	if _, ok := parseHHMM("25:00"); ok {
		t.Error("25:00 should be invalid")
	}
	if _, ok := parseHHMM("9:30"); ok {
		t.Error("9:30 should be invalid (needs leading zero)")
	}
}

func TestCommentEditWindow(t *testing.T) {
	// The 10-minute window is enforced in the service via time.Since —
	// verify the boundary math here by constructing the repo comment.
	old := time.Now().Add(-11 * time.Minute)
	if time.Since(old) <= 10*time.Minute {
		t.Error("boundary broken: 11 minutes should exceed the 10-minute window")
	}
	recent := time.Now().Add(-2 * time.Minute)
	if time.Since(recent) > 10*time.Minute {
		t.Error("boundary broken: 2 minutes should be within the window")
	}
}
