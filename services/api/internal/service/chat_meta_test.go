package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// firstMeta extracts open-graph metadata from a fetched page for the link
// preview. It had no coverage at all in the last measured profile, and writing
// these tests found two defects in it - see TestFirstMetaHandlesNonASCIIBeforeTheTag
// and TestFirstMetaTruncatesOnARuneBoundary.
func TestFirstMeta(t *testing.T) {
	t.Run("property form", func(t *testing.T) {
		html := []byte(`<head><meta property="og:title" content="Hello"></head>`)
		if got := firstMeta(html, "og:title"); got != "Hello" {
			t.Errorf("got %q, want Hello", got)
		}
	})

	t.Run("name form", func(t *testing.T) {
		html := []byte(`<head><meta name="description" content="A shop"></head>`)
		if got := firstMeta(html, "description"); got != "A shop" {
			t.Errorf("got %q, want A shop", got)
		}
	})

	t.Run("single quotes", func(t *testing.T) {
		html := []byte(`<meta property='og:title' content='Single'>`)
		if got := firstMeta(html, "og:title"); got != "Single" {
			t.Errorf("got %q, want Single", got)
		}
	})

	t.Run("attribute case is ignored", func(t *testing.T) {
		html := []byte(`<META PROPERTY="OG:TITLE" CONTENT="Shouty">`)
		if got := firstMeta(html, "og:title"); got != "Shouty" {
			t.Errorf("got %q, want Shouty", got)
		}
	})

	t.Run("the value keeps its original case", func(t *testing.T) {
		// The match is found in a lowered copy but the value must be sliced out
		// of the original, or previews come back in lower case.
		html := []byte(`<meta property="og:title" content="MixedCase Title">`)
		if got := firstMeta(html, "og:title"); got != "MixedCase Title" {
			t.Errorf("got %q, want MixedCase Title", got)
		}
	})

	t.Run("earlier keys win", func(t *testing.T) {
		html := []byte(`<meta property="og:title" content="First">` +
			`<meta name="title" content="Second">`)
		if got := firstMeta(html, "og:title", "title"); got != "First" {
			t.Errorf("got %q, want First", got)
		}
		if got := firstMeta(html, "twitter:title", "title"); got != "Second" {
			t.Errorf("fallback to the second key: got %q, want Second", got)
		}
	})

	t.Run("falls back to the title element", func(t *testing.T) {
		html := []byte(`<head><title>Just A Title</title></head>`)
		if got := firstMeta(html, "og:title"); got != "Just A Title" {
			t.Errorf("got %q, want Just A Title", got)
		}
	})

	t.Run("entities are unescaped", func(t *testing.T) {
		html := []byte(`<meta property="og:title" content="Coffee &amp; Co &#39;Best&#39;">`)
		if got := firstMeta(html, "og:title"); got != "Coffee & Co 'Best'" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("nothing found returns empty", func(t *testing.T) {
		for _, in := range []string{"", "<html><body>plain</body></html>", "<meta name=\"x\">"} {
			if got := firstMeta([]byte(in), "og:title", "title"); got != "" {
				t.Errorf("firstMeta(%q) = %q, want empty", in, got)
			}
		}
	})

	t.Run("a content attribute before the tag is not picked up", func(t *testing.T) {
		// content= is searched for after the match, so an earlier element's
		// content must not be attributed to this key.
		html := []byte(`<meta property="og:image" content="wrong">` +
			`<meta property="og:title" content="right">`)
		if got := firstMeta(html, "og:title"); got != "right" {
			t.Errorf("got %q, want right", got)
		}
	})
}

// Non-ASCII before the tag used to corrupt the extraction.
//
// The function lowers the whole document with strings.ToLower and then uses
// those byte offsets to slice the ORIGINAL. That is only valid if lowering
// preserves byte length, and it does not for every input: "İ" (U+0130) is two
// bytes and lowers to "i", one byte. A single such character anywhere before the
// tag shifts every later offset by one, so the slice starts and ends a byte off
// and returns a fragment of the surrounding markup rather than the content.
//
// The fix lowers only ASCII, which is all the matching needs - the attribute
// names it looks for are ASCII - and is length-preserving by construction.
func TestFirstMetaHandlesNonASCIIBeforeTheTag(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{
		{
			"dotted capital I in the body",
			"<p>İstanbul</p><meta property=\"og:title\" content=\"Real Title\">",
			"Real Title",
		},
		{
			"several, and in the title element path",
			"<p>İİİ</p><title>Fallback Title</title>",
			"Fallback Title",
		},
		{
			"inside the value itself",
			"<meta property=\"og:title\" content=\"İstanbul Guide\">",
			"İstanbul Guide",
		},
		{
			"accented text",
			"<p>café ☕</p><meta property=\"og:title\" content=\"Accented\">",
			"Accented",
		},
	}
	for _, tc := range cases {
		got := firstMeta([]byte(tc.html), "og:title")
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Truncation cut at a byte offset, which can split a multi-byte character and
// put invalid UTF-8 into a JSON response. Asserted as a property, because the
// boundary depends on the content.
func TestFirstMetaTruncatesOnARuneBoundary(t *testing.T) {
	// 299 ASCII bytes then a 3-byte character, so a cut at 300 lands inside it.
	long := strings.Repeat("a", 299) + "☕" + strings.Repeat("b", 50)
	html := []byte(`<meta property="og:title" content="` + long + `">`)

	got := firstMeta(html, "og:title")
	if got == "" {
		t.Fatal("expected a truncated value, got empty")
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 299)) {
		t.Error("the 299 ASCII characters before the boundary should be preserved")
	}

	// The same property for a value made only of multi-byte characters, where
	// many cut points are interior.
	for _, n := range []int{100, 150, 300, 301, 400} {
		many := strings.Repeat("日", n)
		html := []byte(`<meta property="og:title" content="` + many + `">`)
		got := firstMeta(html, "og:title")
		if !utf8.ValidString(got) {
			t.Errorf("%d multi-byte characters: produced invalid UTF-8 (%d bytes)", n, len(got))
		}
		if got == "" {
			t.Errorf("%d multi-byte characters: got empty", n)
		}
	}
}
