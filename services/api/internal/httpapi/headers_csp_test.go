package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Clickjacking protection, and why this test exists at all.
//
// index.html ships a Content-Security-Policy via <meta>, and that policy listed
// `frame-ancestors 'none'`. The spec IGNORES frame-ancestors when CSP is
// delivered in a <meta> element, so the directive was inert: the page's only
// clickjacking defence did nothing, while reading exactly like a defence. It is
// gone from the meta now, which means the header set here is the only thing
// providing that protection for the web surface.
//
// That makes this the load-bearing assertion for clickjacking, not a duplicate
// of the existing X-Frame-Options check. Both are sent deliberately:
// X-Frame-Options is the legacy mechanism and is still honoured by user agents
// that predate frame-ancestors, while frame-ancestors is the CSP directive that
// supersedes it. Neither alone covers everything.
//
// The handlers that install their own stricter CSP (the admin export and the OG
// image endpoints) call Header.Set, which REPLACES the value the middleware
// wrote. So the override cases are asserted too: without them, adding
// frame-ancestors to those two handlers could silently regress, and a test that
// only checked the default path would keep passing.
func TestSecurityHeadersCarryFrameAncestors(t *testing.T) {
	t.Parallel()

	h := newTestServer(t)

	tests := []struct {
		name string
		path string
	}{
		{"api route", "/api/v1/categories"},
		{"probe route", "/__probe/plain"},
		{"admin export", "/api/v1/admin/export.csv"},
		{"og image", "/api/v1/og/business.svg"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))

			// X-Frame-Options stays as the legacy belt.
			if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options = %q, want DENY", got)
			}

			csp := rec.Header().Get("Content-Security-Policy")
			if !strings.Contains(csp, "frame-ancestors 'none'") {
				t.Errorf("Content-Security-Policy = %q, want it to contain frame-ancestors 'none'", csp)
			}
		})
	}
}

// The meta half of this fix is guarded by apps/web/scripts/check-csp.mjs, which
// reads and parses index.html for real. It is deliberately NOT re-asserted here:
// a Go test that checked a copy of the policy string would only be proving that
// the copy matches itself, and index.html is two directories away and not part of
// this package's inputs. The two halves check different files, so neither is
// redundant.
