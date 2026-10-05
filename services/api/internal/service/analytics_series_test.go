package service

import (
	"strings"
	"testing"
	"time"
)

// TestFillSeriesUsesUTC pins the half of the day-bucketing contract that does
// not need a database.
//
// The owner's daily view series is assembled from two halves that must agree on
// the calendar: SQL buckets rows with
//
//	date_trunc('day', occurred_at AT TIME ZONE 'UTC')
//
// and Go fills the gaps with day keys derived from the clock. Those were two
// different days whenever the Postgres session zone and the API process zone
// differed - and the failure mode is total silence, not a wrong number.
//
// With Postgres at UTC and the API host at Asia/Jakarta, every SQL key was one
// day behind every Go key. `fillSeries` looked up day strings that never appeared
// in the result set, found nothing, and rendered thirty zeros while
// AnalyticsResult.Views on the same page showed the correct total. Nothing
// errored and nothing logged.
//
// `now` is a parameter precisely so this is testable: a test can hand in an
// instant whose LOCAL calendar date differs from its UTC one and assert which
// one the keys come from. Reading the clock inside the function made the zone
// impossible to pin down from a test at all.
func TestFillSeriesUsesUTC(t *testing.T) {
	// 2026-10-05T20:00Z is 2026-10-06 in any zone at or east of UTC+4. So a
	// LOCAL-zone implementation and a UTC one produce different first keys for
	// this input, and the test tells them apart.
	now := time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC)
	utcKey := now.UTC().Format("2006-01-02")
	if local := now.Local().Format("2006-01-02"); local == utcKey {
		t.Skipf("this machine's local zone (%s) agrees with UTC for the chosen instant, "+
			"so this test cannot distinguish the two implementations; pick an instant near "+
			"midnight UTC", time.Local)
	}

	out := fillSeries(nil, 3, now)

	if len(out) != 3 {
		t.Fatalf("got %d days, want 3", len(out))
	}
	want := []string{
		now.UTC().AddDate(0, 0, -2).Format("2006-01-02"),
		now.UTC().AddDate(0, 0, -1).Format("2006-01-02"),
		now.UTC().Format("2006-01-02"),
	}
	for i, w := range want {
		if out[i].Day != w {
			t.Errorf("day %d = %q, want %q (series is keyed in local time, not UTC)", i, out[i].Day, w)
		}
	}
	if out[len(out)-1].Day == utcKey && !strings.HasPrefix(utcKey, out[len(out)-1].Day) {
		t.Error("last key does not match the UTC calendar date")
	}
}

// TestFillSeriesZeroFillsAndCounts is the behaviour fillSeries exists for, and
// the part that was silently broken: it must produce a dense run of days and
// carry the real counts through onto the right keys.
func TestFillSeriesZeroFillsAndCounts(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	series := []DailyCount{
		{Day: now.UTC().Format("2006-01-02"), Count: 7},
		{Day: now.UTC().AddDate(0, 0, -2).Format("2006-01-02"), Count: 3},
	}
	out := fillSeries(series, 4, now)

	if len(out) != 4 {
		t.Fatalf("got %d days, want 4 - the series must be dense", len(out))
	}
	// Reverse order: fillSeries walks oldest to newest.
	want := []struct {
		offset int
		count  int
	}{
		{-3, 0}, // gap before the earliest data point
		{-2, 3}, // the sparse entry
		{-1, 0}, // gap
		{0, 7},  // today
	}
	for i, w := range want {
		if got := out[i].Count; got != w.count {
			t.Errorf("day %d (%s, offset %d) = %d, want %d", i, out[i].Day, w.offset, got, w.count)
		}
	}
	// And the keys must be strictly increasing, or the chart's x-axis is wrong.
	for i := 1; i < len(out); i++ {
		if out[i].Day <= out[i-1].Day {
			t.Errorf("day keys are not ascending at %d: %q then %q", i, out[i-1].Day, out[i].Day)
		}
	}
}

// TestSinceForIsUTC pins the other half of the contract: the query cutoff has to
// be computed on the same clock the day keys are, or the window and the buckets
// disagree.
func TestSinceForIsUTC(t *testing.T) {
	for _, period := range []string{"7d", "30d", "all"} {
		since, days := sinceFor(period)
		if since.Location() != time.UTC {
			t.Errorf("sinceFor(%q) returned a cutoff in %v, want UTC - the day buckets are keyed in UTC, "+
				"so the window has to be too or the two halves count different spans",
				period, since.Location())
		}
		want := map[string]int{"7d": 7, "30d": 30, "all": 365}[period]
		if days != want {
			t.Errorf("sinceFor(%q) days = %d, want %d", period, days, want)
		}
	}
	// An unknown period must not be treated as "all time".
	if _, days := sinceFor("nonsense"); days != 30 {
		t.Errorf("sinceFor(\"nonsense\") days = %d, want the 30-day default", days)
	}
}

// TestDayBucketExprIsExplicitlyUTC is a static check, and it is here because the
// SQL half of this contract cannot be exercised without a database.
//
// The failure it guards against is a well-meaning edit: `AT TIME ZONE 'UTC'` is
// redundant-looking on a UTC cluster, so it is exactly the sort of clause someone
// removes as noise. On a cluster configured for anything else - or behind a
// replica or connection pooler that sets the session zone - removing it
// reintroduces the silent all-zeros chart.
func TestDayBucketExprIsExplicitlyUTC(t *testing.T) {
	if !strings.Contains(dayBucketExpr, "AT TIME ZONE 'UTC'") {
		t.Errorf("dayBucketExpr = %q\nIt must carry an explicit AT TIME ZONE 'UTC'. The clause looks "+
			"redundant on a UTC cluster, which is why it is worth a test: remove it and the owner's "+
			"daily view chart renders thirty zeros on any deployment whose Postgres session zone "+
			"differs from the API host's, with no error anywhere.", dayBucketExpr)
	}
	if !strings.Contains(dayBucketExpr, "date_trunc('day'") {
		t.Errorf("dayBucketExpr = %q no longer buckets by day; fillSeries will not find the keys it generates",
			dayBucketExpr)
	}
}
