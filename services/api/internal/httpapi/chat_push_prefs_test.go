package httpapi

import (
	"testing"
	"time"
)

// pushOn reads the channel preference that decides whether a chat message
// triggers a web-push notification. It had no coverage at all in the last
// measured profile, as did the rest of the push delivery path.
func TestPushOn(t *testing.T) {
	enabled := []struct {
		name  string
		prefs map[string]any
	}{
		{"nil prefs", nil},
		{"empty prefs", map[string]any{}},
		{"no channels key at all", map[string]any{"quiet_hours": map[string]any{"enabled": true}}},
		{"channels is not an object", map[string]any{"channels": "all"}},
		{"channels is null", map[string]any{"channels": nil}},
		{"channels is empty", map[string]any{"channels": map[string]any{}}},
		{"push explicitly true", map[string]any{"channels": map[string]any{"push": true}}},
		{"a different channel is off", map[string]any{"channels": map[string]any{"email": false}}},

		// Only an explicit boolean false disables. Anything else fails OPEN, which
		// is the documented intent - "absent = enabled" - extended to malformed
		// input. Pinned deliberately: the alternative reading, that anything
		// falsy disables, would silently stop notifying every user whose
		// preference was written as a string or a number.
		{"push is the string \"false\"", map[string]any{"channels": map[string]any{"push": "false"}}},
		{"push is zero", map[string]any{"channels": map[string]any{"push": 0}}},
		{"push is null", map[string]any{"channels": map[string]any{"push": nil}}},
		{"push is an empty object", map[string]any{"channels": map[string]any{"push": map[string]any{}}}},
	}
	for _, tc := range enabled {
		if !pushOn(tc.prefs) {
			t.Errorf("%s: want push enabled", tc.name)
		}
	}

	if pushOn(map[string]any{"channels": map[string]any{"push": false}}) {
		t.Error("push explicitly false: want push disabled")
	}
}

// quietHoursNow suppresses push between 22:00 and 08:00 in the RECIPIENT's
// timezone. The comment above it records that an earlier version used the
// server's hour, which pushed at 3 PM - or silenced people at midday - for
// anyone more than a few hours from the server clock.
//
// The zones below are chosen for detection power, not for realism alone. A
// server-clock implementation only fails a case when the recipient's hour and
// the server's fall on opposite sides of the 22:00-08:00 boundary, so a single
// zone would leave part of the day where the test cannot tell the difference.
// These two - and the wider set they came from - disagree with UTC at every
// hour of the day under any DST state, which was checked rather than assumed.
func TestQuietHoursUseTheRecipientsClock(t *testing.T) {
	zones := []string{"America/Los_Angeles", "Asia/Jakarta", "America/New_York", "Australia/Sydney", "Pacific/Auckland"}

	for _, tz := range zones {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			t.Fatalf("tzdata unavailable for %s: %v", tz, err)
		}
		prefs := map[string]any{"_tz": tz}

		// Read the clock either side of the call. A test that reads an hour and
		// then asserts on a function that reads it again can straddle the
		// boundary between the two, which is a real flake and an intermittent
		// one - it would only ever fire just before the hour.
		before := time.Now().In(loc).Hour()
		got := quietHoursNow(prefs)
		after := time.Now().In(loc).Hour()
		if before != after {
			continue
		}

		want := before >= 22 || before < 8
		if got != want {
			t.Errorf("%s: at local hour %d quietHoursNow = %v, want %v (the server clock would say %v)",
				tz, before, got, want, func() bool {
					h := time.Now().UTC().Hour()
					return h >= 22 || h < 8
				}())
		}
	}

	// Guard: if the zone list is ever trimmed to one that happens to agree with
	// the server at this hour, the test above would pass while checking nothing.
	// At least one zone must disagree with UTC right now.
	now := time.Now()
	utcQuiet := now.UTC().Hour() >= 22 || now.UTC().Hour() < 8
	disagree := false
	for _, tz := range zones {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			t.Fatal(err)
		}
		h := now.In(loc).Hour()
		if (h >= 22 || h < 8) != utcQuiet {
			disagree = true
			break
		}
	}
	if !disagree {
		t.Error("no zone in the list disagrees with the server clock at this hour; the test above cannot detect a server-clock regression right now")
	}
}

// A missing, non-string, or unrecognised timezone falls back to UTC.
//
// This is a real choice with a user-visible consequence: a preference carrying a
// typo like "Asia/Jakrta" gets quiet hours at UTC rather than at the user's
// midnight. Pinned so that changing it is deliberate, and so the fallback is not
// mistaken for "no quiet hours".
func TestQuietHoursTimezoneFallback(t *testing.T) {
	utcQuiet := func() bool {
		h := time.Now().UTC().Hour()
		return h >= 22 || h < 8
	}

	for _, prefs := range []map[string]any{
		{},
		{"_tz": nil},
		{"_tz": 7},
		{"_tz": "Asia/Jakrta"}, // typo
		{"_tz": "Not/AZone"},   // never valid
		{"_tz": ""},            // explicit empty
	} {
		if got, want := quietHoursNow(prefs), utcQuiet(); got != want {
			t.Errorf("prefs %#v: quietHoursNow = %v, want the UTC answer %v", prefs, got, want)
		}
	}
}

func TestLocByName(t *testing.T) {
	t.Run("empty name is UTC", func(t *testing.T) {
		if got := locByName(""); got != time.UTC {
			t.Errorf("got %v, want UTC", got)
		}
	})

	t.Run("a named zone resolves", func(t *testing.T) {
		got := locByName("Asia/Jakarta")
		want, err := time.LoadLocation("Asia/Jakarta")
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != want.String() {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("an unknown zone falls back to UTC rather than failing", func(t *testing.T) {
		for _, name := range []string{"Not/AZone", "Mars/Olympus", "../../etc/passwd", "UTC\x00"} {
			if got := locByName(name); got != time.UTC {
				t.Errorf("locByName(%q) = %v, want UTC", name, got)
			}
		}
	})

	t.Run("results are cached", func(t *testing.T) {
		// The fan-out calls this once per recipient, so an uncached
		// LoadLocation per push would read the tz database per message.
		first := locByName("Pacific/Auckland")
		second := locByName("Pacific/Auckland")
		if first != second {
			t.Error("the same zone returned different pointers; the cache is not being used")
		}
	})
}
