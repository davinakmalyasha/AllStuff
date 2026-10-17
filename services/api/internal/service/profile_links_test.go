package service

import (
	"strings"
	"testing"
)

// validProfileLinks vets the links a user attaches to their profile. They are
// rendered as anchors on a public page, so the checks that matter are that the
// scheme is pinned to https or mailto (which is what stops javascript: and
// data: from becoming hrefs) and that the key is one the renderer knows.
//
// It had no coverage at all in the last measured profile.
func TestValidProfileLinks(t *testing.T) {
	valid := []struct {
		name  string
		links map[string]any
	}{
		{"empty", map[string]any{}},
		{"https", map[string]any{"website": "https://example.com"}},
		{"https with path and query", map[string]any{"github": "https://github.com/u?tab=repos"}},
		{"every known key", map[string]any{
			"website": "https://a.example", "instagram": "https://b.example",
			"twitter": "https://c.example", "x": "https://d.example",
			"facebook": "https://e.example", "linkedin": "https://f.example",
			"tiktok": "https://g.example", "youtube": "https://h.example",
			"github": "https://i.example", "whatsapp": "https://j.example",
		}},
		{"email as a bare address", map[string]any{"email": "me@example.com"}},
		{"email with the mailto scheme", map[string]any{"email": "mailto:me@example.com"}},
		{"mailto is allowed for non-email keys too", map[string]any{"whatsapp": "mailto:me@example.com"}},
		{"300 characters exactly", map[string]any{"website": "https://e.co/" + strings.Repeat("a", 300-len("https://e.co/"))}},
	}
	for _, tc := range valid {
		if !validProfileLinks(tc.links) {
			t.Errorf("%s: want valid, got invalid", tc.name)
		}
	}

	invalid := []struct {
		name  string
		links map[string]any
	}{
		{"http is not https", map[string]any{"website": "http://example.com"}},
		{"javascript scheme", map[string]any{"website": "javascript:alert(1)"}},
		{"data scheme", map[string]any{"website": "data:text/html,<script>1</script>"}},
		{"no scheme", map[string]any{"website": "example.com"}},
		{"relative path", map[string]any{"website": "/settings"}},
		{"https with no host", map[string]any{"website": "https://"}},
		{"unknown key", map[string]any{"myspace": "https://myspace.example"}},
		{"key is not a known key, even with https", map[string]any{"href": "https://x.example"}},
		{"value is not a string", map[string]any{"website": 42}},
		{"value is null", map[string]any{"website": nil}},
		{"value is an object", map[string]any{"website": map[string]any{"url": "https://x.example"}}},
		{"301 characters", map[string]any{"website": "https://e.co/" + strings.Repeat("a", 301-len("https://e.co/"))}},
		{"email with neither @ nor mailto", map[string]any{"email": "notanaddress"}},
	}
	for _, tc := range invalid {
		if validProfileLinks(tc.links) {
			t.Errorf("%s: want invalid, got valid", tc.name)
		}
	}

	// One bad link rejects the whole set, rather than storing the good ones. The
	// caller treats false as "reject the update", so a partial accept would
	// silently drop links the user believed they had saved.
	t.Run("one bad link fails the whole set", func(t *testing.T) {
		links := map[string]any{
			"website": "https://good.example",
			"github":  "https://also-good.example",
			"twitter": "javascript:alert(1)",
		}
		if validProfileLinks(links) {
			t.Error("want the set rejected when it contains one bad link")
		}
	})
}
