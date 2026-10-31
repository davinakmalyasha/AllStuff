//go:build !race

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/security"
	"allstuff/api/internal/service"
	"allstuff/api/internal/testutil"
)

// get performs a request through the server's real handler chain, middleware
// included, which is the point of the harness: these tests are about what the
// whole stack answers, not what one function returns.
func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// handleVapidKey is the endpoint a browser calls before subscribing. With no
// keypair configured it must say so rather than hand back an empty key, which
// the client would then use to build a subscription that can never be delivered.
func TestVapidKeyEndpointRefusesWhenPushIsNotConfigured(t *testing.T) {
	s, _ := newDBTestServer(t)

	rec := get(t, s, "/api/v1/push/vapid-key")
	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d with no VAPID key configured; body %s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "public_key") {
		t.Errorf("the response carries a public_key it does not have: %s", rec.Body.String())
	}
}

// A public kind is servable; a verification document is not, and the answer must
// not confirm that the document exists. ServePath returns ErrForbidden for it,
// which is deliberately different from the ErrNotFound returned for a hidden or
// unknown id - this pins which one reaches the client.
func TestMediaServeDistinguishesPublicFromPrivateKinds(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	u := testutil.User(t, h)
	// The harness's own Media, so the file lands in the directory the server
	// reads from. A separate NewMedia with its own t.TempDir() writes the row
	// and the bytes to different places, and the symptom is a 404 that looks
	// like a routing problem.
	m := td.Deps.Media

	logo := uploadFor(t, h, m, u.ID, domain.MediaLogo, "logo.png",
		append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 64)...))
	doc := uploadFor(t, h, m, u.ID, domain.MediaDocVerif, "id.pdf",
		append([]byte("%PDF-1.7\n"), []byte("secret contents")...))

	t.Run("a public kind is served", func(t *testing.T) {
		rec := get(t, s, "/api/v1/media/"+logo.ID+"/file")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
		}
		if !bytes.HasPrefix(rec.Body.Bytes(), []byte{0x89, 0x50, 0x4E, 0x47}) {
			t.Error("the served bytes are not the uploaded file")
		}
	})

	t.Run("a verification document is refused", func(t *testing.T) {
		rec := get(t, s, "/api/v1/media/"+doc.ID+"/file")
		if rec.Code == http.StatusOK {
			t.Fatal("a verification document was served over the public route")
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("secret contents")) {
			t.Error("the document's contents leaked into the response body")
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("an unknown id is a 404", func(t *testing.T) {
		rec := get(t, s, "/api/v1/media/00000000-0000-0000-0000-000000000000/file")
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}

func uploadFor(t *testing.T, h *testutil.H, m *service.Media, uploaderID string, kind domain.MediaKind, name string, body []byte) *domain.MediaItem {
	t.Helper()
	item, err := m.Upload(context.Background(), service.UploadInput{
		UploaderID: uploaderID, Kind: kind, FileName: name, Reader: bytes.NewReader(body),
	})
	if err != nil {
		t.Fatalf("uploading %s: %v", name, err)
	}
	return item
}

// The route table is large and mostly guarded; this asserts the shape of the
// answer an anonymous caller gets, which is the one an attacker sees first.
func TestAnonymousCallersAreRefusedOnAuthenticatedRoutes(t *testing.T) {
	s, _ := newDBTestServer(t)

	// GET routes only. A POST-only route answers 405 to a GET, which is correct
	// and is asserted separately by the method-specific tests.
	paths := []string{
		"/api/v1/me",
		"/api/v1/me/export",
		"/api/v1/notifications",
		"/api/v1/threads",
		"/api/v1/me/collections",
		"/api/v1/me/api-keys",
		"/api/v1/admin/reports",
	}
	for _, p := range paths {
		rec := get(t, s, p)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401 for an anonymous caller", p, rec.Code)
		}
		// A redirect to a login page would be wrong here: this is an API, and a
		// client that follows it would report success on an HTML page.
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("GET %s redirected to %q instead of refusing", p, loc)
		}
		// The refusal must not describe what it is protecting.
		if body := rec.Body.String(); strings.Contains(body, "sql") || strings.Contains(body, "panic") {
			t.Errorf("GET %s leaked internals: %s", p, body)
		}
	}
}

func TestAnonymousCallersGetAJSONError(t *testing.T) {
	s, _ := newDBTestServer(t)
	rec := get(t, s, "/api/v1/me")

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the error body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["error"] == nil {
		t.Errorf("no error field in %s", rec.Body.String())
	}
}

func post(t *testing.T, s *Server, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// A state-changing request carrying a session but no CSRF pair must be refused.
// The access cookie is HttpOnly and the CSRF cookie is readable precisely so a
// cross-site form post cannot produce the matching header.
func TestStateChangingRequestsRequireTheCSRFPair(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	u := testutil.User(t, h)

	// An IP literal, so ValidPushEndpoint does not need DNS. Everything else
	// about this request is well formed, so the only reason to refuse it is the
	// missing pair.
	body := []byte(`{"endpoint":"https://8.8.8.8/push","keys":{"p256dh":"a","auth":"b"}}`)

	without := post(t, s, td.authedNoCSRF(t, http.MethodPost, "/api/v1/push/subscribe", body, u))
	if without.Code != http.StatusForbidden {
		t.Errorf("POST with no CSRF pair = %d, want 403; body %s", without.Code, without.Body.String())
	}
	if !strings.Contains(strings.ToLower(without.Body.String()), "csrf") {
		t.Errorf("the refusal does not mention CSRF: %s", without.Body.String())
	}

	with := post(t, s, td.authed(t, http.MethodPost, "/api/v1/push/subscribe", body, u))
	if with.Code == http.StatusForbidden {
		t.Errorf("POST with the CSRF pair was refused: %s", with.Body.String())
	}
	if with.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201; body %s", with.Code, with.Body.String())
	}

	// Shapes that must also be refused. An attacker who can set a cookie on the
	// domain but cannot read one - a subdomain, a sibling app - would try an
	// empty value, because "" == "" passes a naive equality check.
	//
	// The middleware's own empty-header check is redundant with CSRFValid, which
	// refuses an empty value on either side, so these cases are asserting the
	// pair works end to end rather than which of the two refuses it. That the
	// middleware check cannot be observed by a test is a fact about the code, not
	// a gap in the test: removing it is an equivalent mutation.
	mismatches := []struct {
		name   string
		cookie string
		header string
	}{
		{"empty cookie and no header", "", ""},
		{"a cookie with no header", "some-value", ""},
		{"a header with no cookie", "", "some-value"},
		{"a cookie that does not match the header", "cookie-value", "header-value"},
	}
	for _, tc := range mismatches {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/push/subscribe", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		token, _, err := security.IssueToken(td.Config.JWTSecret, security.TokenAccess, u, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "bv_access", Value: token})
		if tc.cookie != "" {
			req.AddCookie(&http.Cookie{Name: "bv_csrf", Value: tc.cookie, Path: "/"})
		}
		if tc.header != "" {
			req.Header.Set(security.CSRFHeader, tc.header)
		}

		rec := post(t, s, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", tc.name, rec.Code)
		}
	}
}

// The endpoint is user-supplied and is later dialled by the fan-out, so this
// handler is the first of two SSRF gates. It must refuse anything that is not a
// public https origin, and refuse it before storing a row.
func TestPushSubscribeRefusesEndpointsThatAreNotPublicHTTPS(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	u := testutil.User(t, h)

	rejected := []struct {
		name     string
		endpoint string
	}{
		{"http rather than https", "http://8.8.8.8/push"},
		{"loopback", "https://127.0.0.1/push"},
		{"loopback by name", "https://localhost/push"},
		{"ipv6 loopback", "https://[::1]/push"},
		{"ipv4-mapped loopback", "https://[::ffff:127.0.0.1]/push"},
		{"rfc1918", "https://10.0.0.1/push"},
		{"rfc1918 172.16", "https://172.16.0.1/push"},
		{"cloud metadata", "https://169.254.169.254/latest/meta-data/"},
		{"cgnat", "https://100.64.0.1/push"},
		{"unspecified", "https://0.0.0.0/push"},
		{"relative", "/push"},
		{"javascript", "javascript:alert(1)"},
		{"empty", ""},
	}
	for _, tc := range rejected {
		body, err := json.Marshal(map[string]any{
			"endpoint": tc.endpoint,
			"keys":     map[string]string{"p256dh": "a", "auth": "b"},
		})
		if err != nil {
			t.Fatal(err)
		}
		rec := post(t, s, td.authed(t, http.MethodPost, "/api/v1/push/subscribe", body, u))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s (%s): status = %d, want 400; body %s", tc.name, tc.endpoint, rec.Code, rec.Body.String())
		}
	}

	var n int
	if err := h.QueryRow(t, `SELECT count(*) FROM push_subscriptions WHERE user_id=$1`, u.ID).Scan(&n); err != nil {
		t.Fatalf("counting subscriptions: %v", err)
	}
	if n != 0 {
		t.Errorf("%d subscription(s) were stored for endpoints that were refused", n)
	}
}

func TestPushSubscribeRequiresTheKeys(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	u := testutil.User(t, h)

	bad := []string{
		`{}`,
		`{"endpoint":"https://8.8.8.8/push"}`,
		`{"endpoint":"https://8.8.8.8/push","keys":{"p256dh":"a"}}`,
		`{"endpoint":"https://8.8.8.8/push","keys":{"auth":"b"}}`,
		`{"keys":{"p256dh":"a","auth":"b"}}`,
	}
	for _, body := range bad {
		rec := post(t, s, td.authed(t, http.MethodPost, "/api/v1/push/subscribe", []byte(body), u))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

// A successful subscribe stores what the fan-out will read back, including the
// keys, which are what the encryption needs.
func TestPushSubscribeStoresTheSubscription(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	u := testutil.User(t, h)

	body := []byte(`{"endpoint":"https://8.8.8.8/push/abc","keys":{"p256dh":"cHVibGlj","auth":"YXV0aA"}}`)
	rec := post(t, s, td.authed(t, http.MethodPost, "/api/v1/push/subscribe", body, u))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}

	var endpoint string
	var keys []byte
	if err := h.QueryRow(t, `SELECT endpoint, keys FROM push_subscriptions WHERE user_id=$1`, u.ID).
		Scan(&endpoint, &keys); err != nil {
		t.Fatalf("the subscription was not stored: %v", err)
	}
	if endpoint != "https://8.8.8.8/push/abc" {
		t.Errorf("endpoint = %q", endpoint)
	}
	if !bytes.Contains(keys, []byte("cHVibGlj")) || !bytes.Contains(keys, []byte("YXV0aA")) {
		t.Errorf("the keys stored are not the keys sent: %s", keys)
	}

	// Re-subscribing with the same endpoint must not accumulate rows, or the
	// fan-out sends the same notification twice.
	if rec := post(t, s, td.authed(t, http.MethodPost, "/api/v1/push/subscribe", body, u)); rec.Code != http.StatusCreated {
		t.Errorf("re-subscribe status = %d, want 201", rec.Code)
	}
	var n int
	if err := h.QueryRow(t, `SELECT count(*) FROM push_subscriptions WHERE user_id=$1`, u.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows after subscribing twice to the same endpoint, want 1", n)
	}
}
