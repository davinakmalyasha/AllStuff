package util

import "testing"

func TestEscapeLike(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"100%", `100\%`},
		{"under_score", `under\_score`},
		{"back\\slash", `back\\slash`},
		{"a_b%c\\d", `a\_b\%c\\d`},
	}
	for _, c := range cases {
		if got := EscapeLike(c.in); got != c.want {
			t.Errorf("EscapeLike(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
