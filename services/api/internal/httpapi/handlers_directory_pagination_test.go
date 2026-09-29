package httpapi

import "testing"

func TestHasMoreResults(t *testing.T) {
	const limit = 24
	tests := []struct {
		name     string
		total    int
		offset   int
		returned int
		limit    int
		want     bool
	}{
		// Exact total available: the answer is exact.
		{name: "middle page with total", total: 100, offset: 24, returned: 24, limit: limit, want: true},
		{name: "last partial page with total", total: 30, offset: 24, returned: 6, limit: limit, want: false},
		{name: "single page with total", total: 10, offset: 0, returned: 10, limit: limit, want: false},
		{name: "empty page with total", total: 0, offset: 0, returned: 0, limit: limit, want: false},
		{
			// The regression: a final page that is exactly full. Total is a
			// multiple of the limit, so the page-length heuristic says "more"
			// and the pager offers an empty next page.
			name: "last page exactly full with total", total: 48, offset: 24, returned: 24, limit: limit, want: false,
		},
		{
			name: "page before a full final page", total: 48, offset: 0, returned: 24, limit: limit, want: true,
		},

		// Total not computed (negative): fall back to the page-length signal.
		{name: "full page without total assumes more", total: -1, offset: 0, returned: 24, limit: limit, want: true},
		{name: "short page without total is the end", total: -1, offset: 0, returned: 7, limit: limit, want: false},
		{name: "empty page without total is the end", total: -1, offset: 48, returned: 0, limit: limit, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasMoreResults(tc.total, tc.offset, tc.returned, tc.limit); got != tc.want {
				t.Errorf("hasMoreResults(total=%d, offset=%d, returned=%d, limit=%d) = %v, want %v",
					tc.total, tc.offset, tc.returned, tc.limit, got, tc.want)
			}
		})
	}
}
