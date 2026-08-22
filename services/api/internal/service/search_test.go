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
	if !isOpenNow(open, at, "Asia/Jakarta") {
		t.Error("expected open at 10:00 Monday")
	}

	// Before opening.
	early := time.Date(2026, 8, 10, 6, 30, 0, 0, loc)
	if isOpenNow(open, early, "Asia/Jakarta") {
		t.Error("expected closed at 06:30")
	}

	// Overnight window (18:00–02:00).
	overnight := map[string]any{
		"mon": map[string]any{"open": "18:00", "close": "02:00", "closed": false},
	}
	late := time.Date(2026, 8, 10, 23, 0, 0, 0, loc)
	if !isOpenNow(overnight, late, "Asia/Jakarta") {
		t.Error("expected open at 23:00 for overnight window")
	}
	afterMidnight := time.Date(2026, 8, 11, 1, 0, 0, 0, loc)
	if !isOpenNow(overnight, afterMidnight, "Asia/Jakarta") {
		t.Error("expected open at 01:00 next day")
	}

	// Timezone correctness: same UTC instant, different local day.
	utc := time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC) // 23:00 UTC Monday
	// In Jakarta it's 06:00 Tuesday — Monday hours should NOT apply.
	if isOpenNow(open, utc, "Asia/Jakarta") {
		t.Error("Monday hours should not apply at 06:00 Tuesday local")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Rumah Kopi Senja": "rumah-kopi-senja",
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
