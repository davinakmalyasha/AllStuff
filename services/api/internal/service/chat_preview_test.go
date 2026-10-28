//go:build !race

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"bizverse/api/internal/testutil"
)

// fetchLinkPreview had no coverage: the parsers, the fetch-failure branches,
// and the dial-level SSRF guard had never run. This is the one place the
// server fetches a user-supplied URL, so every refusal branch is asserted with
// the message that distinguishes it, and every rejection proves no outbound
// request was attempted.

type previewRoundTripper func(*http.Request) (*http.Response, error)

func (f previewRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func previewResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"text/html"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// stubPreview swaps the link-preview client for one test. Package tests run
// sequentially, so the swap is safe as long as it is restored (the same seam
// the OAuth tests use for their transport).
func stubPreview(t *testing.T, fn previewRoundTripper) {
	t.Helper()
	old := previewClient
	previewClient = &http.Client{Transport: fn}
	t.Cleanup(func() { previewClient = old })
}

func fetchOK(t *testing.T, text string) map[string]any {
	t.Helper()
	preview, ok := fetchLinkPreview(context.Background(), text)
	if !ok || preview == nil {
		t.Fatalf("fetchLinkPreview(%q) = %v, %v; want a preview", text, preview, ok)
	}
	return preview
}

func fetchRefused(t *testing.T, text string) {
	t.Helper()
	called := false
	stubPreview(t, func(r *http.Request) (*http.Response, error) {
		called = true
		return previewResponse(200, `<meta property="og:title" content="x">`), nil
	})
	if preview, ok := fetchLinkPreview(context.Background(), text); ok || preview != nil {
		t.Errorf("fetchLinkPreview(%q) = %v, %v; want refusal", text, preview, ok)
	}
	if called {
		t.Errorf("fetchLinkPreview(%q) made an outbound request for a refused URL", text)
	}
}

func TestFetchLinkPreviewRefusesWithoutNetwork(t *testing.T) {
	// None of these may reach the transport: the guard answers before any
	// dial, and the stub fails the test if one is attempted.
	for _, text := range []string{
		"no url here at all",
		"ftp://example.com/file", // the extractor only matches http(s)
		"visit http://127.0.0.1:8080/admin for the console",
		"visit http://10.0.0.5/share",
		"visit http://192.168.1.1/router",
		"visit http://169.254.169.254/latest/meta-data/",
		"visit http://[::1]/loopback",
		"visit http://localhost:3000/app",
		"visit http://intranet/docs",
		"see http://[::1 for the bracket bug",
	} {
		t.Run(text, func(t *testing.T) { fetchRefused(t, text) })
	}
}

func TestPreviewDialContextRejectsBeforeDialing(t *testing.T) {
	// Each guard has a distinct message, and the tests assert it: without that,
	// removing one guard still fails with a later guard's error and the
	// mutation looks caught while the layer it removed is gone.
	cases := []struct {
		name string
		addr string
		want string
	}{
		{"a non-80/443 port", "93.184.216.1:8080", "port 8080 not allowed"},
		{"ssh port", "93.184.216.1:22", "port 22 not allowed"},
		{"a bare hostname", "localhost:80", "bare hostname rejected"},
		{"a single-label name", "intranet:443", "bare hostname rejected"},
		{"loopback v4", "127.0.0.1:80", "private address blocked"},
		{"rfc1918", "10.0.0.5:443", "private address blocked"},
		{"link-local", "169.254.169.254:80", "private address blocked"},
		{"loopback v6", "[::1]:80", "private address blocked"},
		{"no port at all", "example.com", "missing port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A short timeout bounds the test if a guard regresses into a real
			// dial: the failure is a wrong error, not a hang.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := previewDialContext(ctx, "tcp", tc.addr)
			if err == nil {
				t.Fatalf("dial %q succeeded; want refusal", tc.addr)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("dial %q error = %q, want it to mention %q", tc.addr, err, tc.want)
			}
		})
	}
}

const previewHTML = `<html><head>
<meta property="og:title" content="The Article &amp; More">
<meta property="og:description" content="A description.">
<meta property="og:image" content="https://cdn.example.test/img.png">
<title>Fallback Title</title>
</head></html>`

// A failed lookup returns before dialing. LookupIPAddr with a canceled
// context fails immediately without touching the network, so this is
// deterministic: without the error check the code falls through to
// d.DialContext, whose own error carries the "dial" prefix.
func TestPreviewDialLookupFailureReturnsBeforeDialing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := previewDialContext(ctx, "tcp", "example.com:80")
	if err == nil {
		t.Fatal("expected a lookup error")
	}
	if strings.HasPrefix(err.Error(), "dial") {
		t.Errorf("error = %q; a lookup failure must not reach the dialer", err)
	}
}

func TestFetchLinkPreviewSuccess(t *testing.T) {
	var gotUA string
	stubPreview(t, func(r *http.Request) (*http.Response, error) {
		gotUA = r.Header.Get("User-Agent")
		return previewResponse(200, previewHTML), nil
	})
	preview := fetchOK(t, "read this https://example.test/article today")
	if preview["url"] != "https://example.test/article" {
		t.Errorf("url = %v", preview["url"])
	}
	if preview["title"] != "The Article & More" {
		t.Errorf("title = %q, want the unescaped og:title", preview["title"])
	}
	if preview["description"] != "A description." {
		t.Errorf("description = %q", preview["description"])
	}
	if preview["image"] != "https://cdn.example.test/img.png" {
		t.Errorf("image = %q", preview["image"])
	}
	if gotUA != "BizVerseBot/1.0 (+https://bizverse.app)" {
		t.Errorf("User-Agent = %q", gotUA)
	}
}

func TestFetchLinkPreviewFetchFailures(t *testing.T) {
	stubErr := errors.New("connection reset")
	cases := []struct {
		name string
		fn   previewRoundTripper
	}{
		{"transport error", func(*http.Request) (*http.Response, error) { return nil, stubErr }},
		{"400", func(r *http.Request) (*http.Response, error) { return previewResponse(400, previewHTML), nil }},
		{"404", func(r *http.Request) (*http.Response, error) { return previewResponse(404, "missing"), nil }},
		{"500", func(r *http.Request) (*http.Response, error) { return previewResponse(500, "boom"), nil }},
		{"no metadata at all", func(r *http.Request) (*http.Response, error) {
			return previewResponse(200, "<html><body>just text</body></html>"), nil
		}},
		{"unreadable body", func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(errorReader{})}, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubPreview(t, tc.fn)
			if preview, ok := fetchLinkPreview(context.Background(), "see https://example.test/x"); ok || preview != nil {
				t.Errorf("fetch = %v, %v; want refusal", preview, ok)
			}
		})
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestFetchLinkPreviewTitleFallback(t *testing.T) {
	stubPreview(t, func(*http.Request) (*http.Response, error) {
		return previewResponse(200, `<html><head><title>Only A Title</title></head></html>`), nil
	})
	if preview := fetchOK(t, "see https://example.test/x"); preview["title"] != "Only A Title" {
		t.Errorf("title = %q, want the <title> fallback", preview["title"])
	}
}

func TestFetchLinkPreviewImagePolicy(t *testing.T) {
	// Only absolute http(s) image URLs survive: data:, file: and relative
	// references are cleared, but the preview itself still returns when the
	// title is present.
	for _, tc := range []struct {
		name  string
		image string
		want  string
	}{
		{"absolute https kept", "https://cdn.example.test/a.png", "https://cdn.example.test/a.png"},
		{"data url cleared", "data:image/png;base64,AAAA", ""},
		{"relative cleared", "/img/a.png", ""},
		{"javascript cleared", "javascript:alert(1)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubPreview(t, func(*http.Request) (*http.Response, error) {
				return previewResponse(200,
					`<meta property="og:title" content="T"><meta property="og:image" content="`+tc.image+`">`), nil
			})
			if preview := fetchOK(t, "see https://example.test/x"); preview["image"] != tc.want {
				t.Errorf("image = %q, want %q", preview["image"], tc.want)
			}
		})
	}
}

// The basic forms (property=/name=, quotes, case, key order, entities) are
// covered in chat_meta_test.go. These are the cases only this file pins: the
// twitter: fallback in the call order fetchLinkPreview itself uses, an empty
// content attribute, and the forward-only search.
func TestFirstMetaMatching(t *testing.T) {
	html := `<head>
<meta name="twitter:title" content="Twitter Title">
<meta property="og:title" content="OG Title">
</head>`
	// Keys are tried in order: og: wins over twitter: when both are present.
	if got := firstMeta([]byte(html), "og:title", "twitter:title", "title"); got != "OG Title" {
		t.Errorf("og:title = %q", got)
	}
	// twitter: is the fallback when og: is absent.
	if got := firstMeta([]byte(html), "og:image", "twitter:title"); got != "Twitter Title" {
		t.Errorf("twitter fallback = %q", got)
	}
	// An empty content attribute is not a value: matching continues.
	if got := firstMeta([]byte(`<meta property="og:title" content=""><title>T</title>`), "og:title"); got != "T" {
		t.Errorf("empty content = %q, want the title fallback", got)
	}
	// content-before-name is not matched; the parser only looks forward from
	// the name. Pinned, not fixed: real pages put the name first.
	if got := firstMeta([]byte(`<meta content="Backwards" property="og:title">`)); got != "" {
		t.Errorf("backwards meta = %q, want no match", got)
	}
}

func TestPreviewValueBounds(t *testing.T) {
	if got := previewValue("Fish &amp; Chips"); got != "Fish & Chips" {
		t.Errorf("unescaped = %q", got)
	}
	long := strings.Repeat("a", 299) + strings.Repeat("é", 10)
	got := previewValue(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("long value does not end with an ellipsis: %q", got)
	}
	// The cut must land on a rune boundary, not split a multibyte character.
	if !utf8.ValidString(got) {
		t.Errorf("truncated value is not valid UTF-8: %q", got)
	}
	if len(got) > 304 {
		t.Errorf("truncated length = %d, want at most 300 bytes plus the ellipsis", len(got))
	}
	if previewValue(strings.Repeat("b", 300)) != strings.Repeat("b", 300) {
		t.Error("a 300-byte value must pass through untouched")
	}
}

func TestAsciiLowerPreservesByteLength(t *testing.T) {
	// "İ" (U+0130) is two bytes and lowers to "i" (one byte) under
	// strings.ToLower, which would shift every offset found after it. Only
	// ASCII folds here, so offsets into the original stay valid.
	in := "ABCİDEF"
	if got := asciiLower(in); got != "abcİdef" || len(got) != len(in) {
		t.Errorf("asciiLower(%q) = %q, want the ASCII-only fold abcİdef", in, got)
	}
	doc := "İİİİ<meta property=\"og:title\" content=\"Correct\">"
	if got := firstMeta([]byte(doc), "og:title"); got != "Correct" {
		t.Errorf("extraction after multibyte run = %q, want Correct", got)
	}
}

func TestChatSendEnrichesLinkPreview(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	member := testutil.User(t, h)
	other := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: member.ID}, participant{userID: other.ID})

	stubPreview(t, func(*http.Request) (*http.Response, error) {
		return previewResponse(200,
			`<meta property="og:title" content="Linked Page"><meta property="og:description" content="Desc">`), nil
	})

	sent, err := sendText(t, c, ctx, member.ID, thread, "read this https://example.test/article")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// The enrichment is a goroutine, so poll with a deadline rather than
	// sleeping a fixed span: fast locally, tolerant under load.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var title *string
		if err := h.Repos.QueryRow(ctx,
			`SELECT link_preview->>'title' FROM chat_messages WHERE id = $1`, sent.ID).Scan(&title); err != nil {
			t.Fatalf("read link_preview: %v", err)
		}
		if title != nil {
			if *title != "Linked Page" {
				t.Fatalf("preview title = %q, want Linked Page", *title)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the link preview was never written")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestChatSendSkipsPreviewWithoutURL(t *testing.T) {
	c, h := newTestChat(t)
	ctx := fixtureCtx(t)
	member := testutil.User(t, h)
	other := testutil.User(t, h)
	thread := makeThread(t, h, "direct", nil, "", participant{userID: member.ID}, participant{userID: other.ID})

	called := false
	stubPreview(t, func(r *http.Request) (*http.Response, error) {
		called = true
		return previewResponse(200, previewHTML), nil
	})

	sent, err := sendText(t, c, ctx, member.ID, thread, "plain text, no links")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var hasPreview bool
	if err := h.Repos.QueryRow(ctx,
		`SELECT link_preview IS NOT NULL FROM chat_messages WHERE id = $1`, sent.ID).Scan(&hasPreview); err != nil {
		t.Fatalf("read link_preview: %v", err)
	}
	if hasPreview || called {
		t.Error("a message without a URL triggered the preview fetch")
	}
}
