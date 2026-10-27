//go:build !race

package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bizverse/api/internal/service"
	"bizverse/api/internal/testutil"
)

// The X-API-Key boundary had one covered branch: the empty header. Everything
// that decides whether a key is accepted - the hash lookup, revocation, scopes,
// the owner's moderation state, and the per-key rate limit - had never run.
// That is the whole authentication story for /api/v2, so every branch is
// exercised here rather than inferred from the management handlers.

const apiV2Categories = "/api/v2/categories"

func apiKeyGET(t *testing.T, hdl http.Handler, raw string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, apiV2Categories, nil)
	if raw != "" {
		req.Header.Set("X-API-Key", raw)
	}
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, req)
	return w
}

// newTestAPIKey issues a key through the service, which is the only way to get
// the raw value: the database stores the hash.
func newTestAPIKey(t *testing.T, td *testDeps, userID, name string) (id, raw string) {
	t.Helper()
	key, raw, err := td.Deps.APIKeys.Create(t.Context(), userID, name)
	if err != nil {
		t.Fatalf("create API key: %v", err)
	}
	return key.ID, raw
}

func TestAPIKeyAuthRejectsMissingUnknownAndRevokedKeys(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := testutil.User(t, h)

	if w := apiKeyGET(t, hdl, ""); w.Code != http.StatusUnauthorized || errorCode(t, w) != "not_authenticated" {
		t.Errorf("no key: status = %d, code = %q; want 401 not_authenticated", w.Code, errorCode(t, w))
	}

	// A syntactically plausible key with no matching row. The value is never
	// compared as text; only its hash is looked up.
	unknown := "bv_" + strings.Repeat("0", 48)
	if w := apiKeyGET(t, hdl, unknown); w.Code != http.StatusUnauthorized || errorCode(t, w) != "not_authenticated" {
		t.Errorf("unknown key: status = %d, code = %q; want 401 not_authenticated", w.Code, errorCode(t, w))
	}

	keyID, raw := newTestAPIKey(t, td, user.ID, "live")
	if w := apiKeyGET(t, hdl, raw); w.Code != http.StatusOK {
		t.Fatalf("valid key: status = %d: %s", w.Code, w.Body.String())
	}

	// One character changed must not authenticate.
	last := "0"
	if strings.HasSuffix(raw, "0") {
		last = "1"
	}
	changed := raw[:len(raw)-1] + last
	if w := apiKeyGET(t, hdl, changed); w.Code != http.StatusUnauthorized {
		t.Errorf("a key with one character changed: status = %d, want 401", w.Code)
	}

	if err := td.Deps.APIKeys.Revoke(t.Context(), user.ID, keyID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if w := apiKeyGET(t, hdl, raw); w.Code != http.StatusUnauthorized || errorCode(t, w) != "not_authenticated" {
		t.Errorf("revoked key: status = %d, code = %q; want 401 not_authenticated", w.Code, errorCode(t, w))
	}
}

func TestAPIKeyAuthEnforcesReadScope(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := testutil.User(t, h)
	keyID, raw := newTestAPIKey(t, td, user.ID, "scoped")

	cases := []struct {
		name   string
		scopes string
		status int
		code   string
	}{
		// Rows created before scopes existed carry an empty array. Treating
		// that as no access would break every legacy key, so empty means the
		// historical read grant and nothing more.
		{"legacy empty scopes", `[]`, http.StatusOK, ""},
		{"read", `["read"]`, http.StatusOK, ""},
		{"read among others", `["write","read"]`, http.StatusOK, ""},
		{"write only", `["write"]`, http.StatusForbidden, "forbidden"},
		// Scope names are lower-case; a case-insensitive match would widen the
		// grant silently.
		{"upper-case read", `["READ"]`, http.StatusForbidden, "forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h.Exec(t, `UPDATE api_keys SET scopes = $2::jsonb WHERE id = $1`, keyID, tc.scopes)
			w := apiKeyGET(t, hdl, raw)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if tc.code != "" {
				if got := errorCode(t, w); got != tc.code {
					t.Errorf("code = %q, want %q", got, tc.code)
				}
			}
		})
	}
}

func TestAPIKeyAuthRefusesModeratedOwners(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := testutil.User(t, h)
	_, raw := newTestAPIKey(t, td, user.ID, "moderated")

	// API keys do not expire like access tokens, so a missing moderation check
	// would keep a suspended account's public API access alive indefinitely.
	// This is the same defect class the per-request session check fixed.
	cases := []struct {
		name   string
		sql    string
		status int
		code   string
	}{
		{"active", `UPDATE users SET status='active', suspended_until=NULL WHERE id=$1`, http.StatusOK, ""},
		{"banned", `UPDATE users SET status='banned', suspended_until=NULL WHERE id=$1`, http.StatusForbidden, "account_banned"},
		// NULL suspended_until is an indefinite suspension, not an expired one.
		{"suspended indefinitely", `UPDATE users SET status='suspended', suspended_until=NULL WHERE id=$1`, http.StatusForbidden, "account_suspended"},
		{"suspended with a future expiry", `UPDATE users SET status='suspended', suspended_until=now()+interval '1 hour' WHERE id=$1`, http.StatusForbidden, "account_suspended"},
		{"suspension has lapsed", `UPDATE users SET status='suspended', suspended_until=now()-interval '1 hour' WHERE id=$1`, http.StatusOK, ""},
		// Soft deletion hides the owner from GetByID, so the key cannot resolve
		// a principal even though the row still exists. Run last: it is
		// terminal for this user.
		{"soft-deleted owner", `UPDATE users SET status='active', suspended_until=NULL, deleted_at=now() WHERE id=$1`, http.StatusUnauthorized, "not_authenticated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h.Exec(t, tc.sql, user.ID)
			w := apiKeyGET(t, hdl, raw)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if tc.code != "" {
				if got := errorCode(t, w); got != tc.code {
					t.Errorf("code = %q, want %q", got, tc.code)
				}
			}
		})
	}
}

func TestAPIKeyAuthRateLimitsPerKey(t *testing.T) {
	cfg := testConfig(t)
	// The IP tier defaults to 120/min and would answer first; the key tier is
	// the one under test.
	cfg.RateLimitGlobal = 100000
	s, h, td := newDBTestServerWith(t, cfg)
	hdl := s.Handler()
	user := testutil.User(t, h)
	keyID, raw := newTestAPIKey(t, td, user.ID, "limited")
	_, otherRaw := newTestAPIKey(t, td, user.ID, "other")

	// Prime the same bucket the middleware uses. 300 requests over HTTP would
	// answer the same question through 600 database round-trips; the limiter is
	// the component under test, not the query path.
	for i := 0; i < 300; i++ {
		if _, _, ok := td.Deps.RateLimiter.Allow("apikey:"+keyID, 300, time.Minute); !ok {
			t.Fatalf("request %d was refused before the limit", i+1)
		}
	}

	w := apiKeyGET(t, hdl, raw)
	if w.Code != http.StatusTooManyRequests || errorCode(t, w) != "rate_limited" {
		t.Fatalf("over the limit: status = %d, code = %q; want 429 rate_limited", w.Code, errorCode(t, w))
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("a rate-limited API-key response has no Retry-After header")
	}
	// The bucket is keyed by key id, so one busy integration cannot lock out
	// another key belonging to the same account.
	if w := apiKeyGET(t, hdl, otherRaw); w.Code != http.StatusOK {
		t.Errorf("a second key after the first was limited: status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

func TestAPIKeyAuthStoresOnlyAHashAndRecordsUsage(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := testutil.User(t, h)
	keyID, raw := newTestAPIKey(t, td, user.ID, "hashed")

	var hash, prefix string
	var lastUsed *time.Time
	if err := h.QueryRow(t,
		`SELECT key_hash, prefix, last_used_at FROM api_keys WHERE id = $1`, keyID).
		Scan(&hash, &prefix, &lastUsed); err != nil {
		t.Fatalf("read the key row: %v", err)
	}
	sum := sha256.Sum256([]byte(raw))
	if want := hex.EncodeToString(sum[:]); hash != want {
		t.Errorf("stored key_hash = %q, want the sha256 of the raw key", hash)
	}
	if hash == raw || strings.Contains(hash, raw) {
		t.Error("the raw key is recoverable from the stored hash")
	}
	if lastUsed != nil {
		t.Errorf("last_used_at = %v before first use, want NULL", lastUsed)
	}
	if !strings.HasPrefix(raw, "bv_") || len(prefix) != 10 || raw[:len(prefix)] != prefix {
		t.Errorf("prefix = %q, raw = %q; want the first 10 characters of the raw key", prefix, raw)
	}

	if w := apiKeyGET(t, hdl, raw); w.Code != http.StatusOK {
		t.Fatalf("use: status = %d: %s", w.Code, w.Body.String())
	}
	var recorded bool
	if err := h.QueryRow(t, `SELECT last_used_at IS NOT NULL FROM api_keys WHERE id = $1`, keyID).
		Scan(&recorded); err != nil {
		t.Fatalf("read last_used_at: %v", err)
	}
	if !recorded {
		t.Error("a successful API-key request did not record last_used_at")
	}
}

// HasScope has one caller, and it only ever asks for "read". The empty-array
// rule therefore cannot be proven through the middleware: a key with no scopes
// is allowed to read either way. The method's contract is stated directly so
// "legacy rows get read and nothing more" cannot silently become "legacy rows
// get everything".
func TestAPIKeyHasScopeContract(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
		read   bool
		write  bool
	}{
		{"legacy empty scopes", nil, true, false},
		{"read", []string{"read"}, true, false},
		{"write only", []string{"write"}, false, true},
		{"both", []string{"write", "read"}, true, true},
		{"scope names are case-sensitive", []string{"READ"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := &service.APIKey{Scopes: tc.scopes}
			if got := k.HasScope("read"); got != tc.read {
				t.Errorf("HasScope(read) = %v, want %v", got, tc.read)
			}
			if got := k.HasScope("write"); got != tc.write {
				t.Errorf("HasScope(write) = %v, want %v", got, tc.write)
			}
		})
	}
}
