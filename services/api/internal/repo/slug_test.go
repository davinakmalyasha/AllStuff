package repo

import "testing"

// slugFromName builds the URL segment for a collection. It had no coverage at
// all in the last measured profile.
//
// The case that matters is the fallback: a name made entirely of characters the
// slug allows nothing from would otherwise produce an empty segment, and an
// empty segment is a collection URL of ".../collections//items" - which either
// 404s or, worse, resolves to the listing itself.
func TestSlugFromName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"My Favorites", "my-favorites"},
		{"Already-Slugged", "already-slugged"},
		{"Café", "caf"},
		{"123", "123"},
		{"a.b", "a-b"},
		{"test_underscore.dot-dash", "test-underscore-dot-dash"},

		// Runs of separators collapse rather than accumulating.
		{"Multi   Spaces", "multi-spaces"},
		{"a---b", "a-b"},
		{"a - b", "a-b"},
		{"a___b", "a-b"},
		{"a . - _ b", "a-b"},

		// Leading and trailing separators are trimmed.
		{"--leading", "leading"},
		{"trailing--", "trailing"},
		{"--both--", "both"},
		{"_._mixed.-_", "mixed"},

		// Everything the slug drops leaves nothing behind, so the fallback runs.
		{"", "collection"},
		{"!!!", "collection"},
		{"日本語", "collection"},
		{"   ", "collection"},
		{"---", "collection"},
		{"...", "collection"},
		{"-.-_-", "collection"},
		{"🎉🎉", "collection"},
	}
	for _, tc := range cases {
		if got := slugFromName(tc.in); got != tc.want {
			t.Errorf("slugFromName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	t.Run("never returns an empty segment", func(t *testing.T) {
		// The fallback is what makes this true. Asserted as a property as well
		// as by example, because an empty slug is the failure that matters.
		for _, in := range []string{"", "!", " ", "日本語", "🎉", "-", ".", "_", "\t", "\n"} {
			if got := slugFromName(in); got == "" {
				t.Errorf("slugFromName(%q) returned an empty slug", in)
			}
		}
	})

	t.Run("output only ever contains slug characters", func(t *testing.T) {
		for _, in := range []string{
			"Normal Name", "Ünïcödé", "a<b>c", "a/b", "a?b=1", "a#b", "100%",
			"quote\"x", "semi;colon", "back\\slash", "ünïcödé 名前",
		} {
			for _, r := range slugFromName(in) {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
					t.Errorf("slugFromName(%q) = %q contains %q, which is not a slug character",
						in, slugFromName(in), string(r))
					break
				}
			}
		}
	})
}
