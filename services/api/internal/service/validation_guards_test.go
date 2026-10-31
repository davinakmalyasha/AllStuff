package service

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"allstuff/api/internal/domain"
)

// These three functions had no coverage at all in the last measured CI profile
// (123 pure functions are in that state, 853 statements between them). They are
// grouped here because none of them needs a database, so unlike the
// //go:build !race suites they can be run and mutation-checked locally rather
// than only on a runner.

// The address-safety tests that were here have MOVED to
// internal/security/push_test.go. isPublicIP turned out to exist twice - once
// here, untested, and once in security, tested and hardened to reject nil and
// to block CGNAT and the reserved ranges. This file's copy was the weaker of
// the two and faced the more exposed caller. The duplicate is gone and both
// callers now use security.IsPublicIP, so the tests belong with it.

// validateHours normalises whatever the client sent into the shape the rest of
// the service and the SQL open-now function expect. Two things matter: a
// malformed time must be rejected rather than stored, and anything the function
// does not understand must be dropped rather than passed through, because
// unknown keys reaching jsonb are read back by biz_is_open_now.
func TestValidateHoursNormalisesAndRejects(t *testing.T) {
	t.Run("rejects a non-object", func(t *testing.T) {
		for _, v := range []any{"09:00", 42, []any{}, nil} {
			if _, err := validateHours(v); !errors.Is(err, domain.ErrValidation) {
				t.Errorf("validateHours(%#v) err = %v, want ErrValidation", v, err)
			}
		}
	})

	t.Run("keeps a closed day as closed only", func(t *testing.T) {
		out, err := validateHours(map[string]any{
			"mon": map[string]any{"closed": true, "open": "09:00", "close": "17:00"},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		day, _ := out["mon"].(map[string]any)
		if day["closed"] != true {
			t.Errorf("closed = %v, want true", day["closed"])
		}
		if _, ok := day["open"]; ok {
			t.Error("a closed day must not carry an open time; the SQL reads whichever key is present")
		}
	})

	t.Run("normalises an open day", func(t *testing.T) {
		out, err := validateHours(map[string]any{
			"mon": map[string]any{"open": "07:30", "close": "18:00"},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		day, _ := out["mon"].(map[string]any)
		if day["open"] != "07:30" || day["close"] != "18:00" || day["closed"] != false {
			t.Errorf("got %#v, want open 07:30 close 18:00 closed false", day)
		}
	})

	t.Run("rejects malformed and out-of-range times", func(t *testing.T) {
		bad := []string{"9:00", "09:0", "0900", "09-00", "24:00", "23:60", "", "ab:cd", "09:00:00"}
		for _, tm := range bad {
			_, err := validateHours(map[string]any{
				"mon": map[string]any{"open": tm, "close": "17:00"},
			})
			if !errors.Is(err, domain.ErrValidation) {
				t.Errorf("open=%q: err = %v, want ErrValidation", tm, err)
			}
		}
	})

	t.Run("accepts the boundaries of valid times", func(t *testing.T) {
		for _, tm := range []string{"00:00", "23:59"} {
			if _, err := validateHours(map[string]any{
				"mon": map[string]any{"open": tm, "close": tm},
			}); err != nil {
				t.Errorf("%s should be valid: %v", tm, err)
			}
		}
	})

	t.Run("drops anything it does not recognise", func(t *testing.T) {
		out, err := validateHours(map[string]any{
			"mon":      map[string]any{"open": "09:00", "close": "17:00"},
			"Mon":      map[string]any{"open": "09:00", "close": "17:00"}, // wrong case
			"funday":   map[string]any{"open": "09:00", "close": "17:00"}, // not a day
			"tue":      "09:00-17:00",                                     // not an object
			"whatever": map[string]any{"junk": true},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("got %d keys (%v), want only mon", len(out), out)
		}
		if _, ok := out["mon"]; !ok {
			t.Error("mon was dropped")
		}
	})

	t.Run("an empty object is valid and empty", func(t *testing.T) {
		out, err := validateHours(map[string]any{})
		if err != nil || len(out) != 0 {
			t.Errorf("got %v, %v; want empty and no error", out, err)
		}
	})
}

// validStorefrontTheme validates the owner-supplied theme. The theme is stored
// as jsonb and handed to the storefront renderer, so the checks that matter are
// that arbitrary strings cannot reach the renderer as a font or template name
// (the regexes are what restrict them to known values) and that a colour cannot
// carry something other than a hex value.
func TestValidStorefrontTheme(t *testing.T) {
	valid := []struct {
		name  string
		theme map[string]any
	}{
		{"empty", map[string]any{}},
		{"font", map[string]any{"font": "inter"}},
		{"font with separators", map[string]any{"font": "inter-tight"}},
		{"template", map[string]any{"template": "classic_2-col"}},
		{"icon shape", map[string]any{"icon_shape": "circle"}},
		{"3-digit hex", map[string]any{"colors": map[string]any{"accent": "#fff"}}},
		{"6-digit hex", map[string]any{"colors": map[string]any{"accent": "#1A2b3C"}}},
		{"8-digit hex", map[string]any{"colors": map[string]any{"accent": "#1a2b3cff"}}},
		{"unknown top-level key is ignored", map[string]any{"nonsense": true}},
	}
	for _, tc := range valid {
		if err := validStorefrontTheme(tc.theme); err != nil {
			t.Errorf("%s: want nil, got %v", tc.name, err)
		}
	}

	invalid := []struct {
		name  string
		theme map[string]any
	}{
		{"font with a space", map[string]any{"font": "comic sans"}},
		{"font with a quote", map[string]any{"font": `" onload="x`}},
		{"font uppercase", map[string]any{"font": "Inter"}},
		{"font too long", map[string]any{"font": strings.Repeat("a", 21)}},
		{"font not a string", map[string]any{"font": 12}},
		{"template with a slash", map[string]any{"template": "../etc/passwd"}},
		{"template uppercase", map[string]any{"template": "Classic"}},
		{"template too long", map[string]any{"template": strings.Repeat("a", 21)}},
		{"icon_shape too long", map[string]any{"icon_shape": strings.Repeat("a", 21)}},
		{"colour without a hash", map[string]any{"colors": map[string]any{"accent": "red"}}},
		{"colour with non-hex digits", map[string]any{"colors": map[string]any{"accent": "#xyzxyz"}}},
		{"colour with css", map[string]any{"colors": map[string]any{"accent": "red; background:url(x)"}}},
		{"colour too long", map[string]any{"colors": map[string]any{"accent": "#" + strings.Repeat("a", 9)}}},
		{"colour not a string", map[string]any{"colors": map[string]any{"accent": 0}}},
	}
	for _, tc := range invalid {
		err := validStorefrontTheme(tc.theme)
		if !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", tc.name, err)
		}
	}
}

// sanitizeUsername turns a display name from an OAuth provider into something
// usable as a handle. It must always produce a legal handle - the caller stores
// it without a second check - so the invariants are pinned rather than specific
// outputs, because the padding is deliberately random.
func TestSanitizeUsernameAlwaysProducesALegalHandle(t *testing.T) {
	handleRe := regexp.MustCompile(`^[a-z0-9_]+$`)

	inputs := []string{
		"Alice", "a.b-c@d!e", "  spaced  ", "café", "Ünïcödé", "!!!", "",
		"a", "ab", "abc", strings.Repeat("z", 100), "user_name", "42",
		"DROP TABLE users;--", "<script>alert(1)</script>", "日本",
	}
	for _, in := range inputs {
		got := sanitizeUsername(in)
		if len(got) < 3 || len(got) > 30 {
			t.Errorf("sanitizeUsername(%q) = %q, length %d, want 3..30", in, got, len(got))
			continue
		}
		if !handleRe.MatchString(got) {
			t.Errorf("sanitizeUsername(%q) = %q, which is not a legal handle", in, got)
		}
	}

	// Deterministic cases, where no padding is involved.
	t.Run("lowercases and drops everything else", func(t *testing.T) {
		for in, want := range map[string]string{
			"Alice":                     "alice",
			"a.b-c@d!e":                 "abcde",
			"café":                      "caf",
			"DROP TABLE users;--":       "droptableusers",
			"<script>alert(1)</script>": "scriptalert1script",
		} {
			if got := sanitizeUsername(in); got != want {
				t.Errorf("sanitizeUsername(%q) = %q, want %q", in, got, want)
			}
		}
	})

	t.Run("truncates to 30", func(t *testing.T) {
		if got := sanitizeUsername(strings.Repeat("z", 100)); got != strings.Repeat("z", 30) {
			t.Errorf("got %q (len %d), want 30 z's", got, len(got))
		}
	})

	t.Run("pads a short handle to the minimum length", func(t *testing.T) {
		// The padding is random, so assert what is actually guaranteed: the
		// characters that survived sanitising are kept as a prefix and the total
		// length reaches the minimum. Asserting the exact output would only be
		// asserting the random source.
		for in, prefix := range map[string]string{"": "", "a": "a", "ab": "ab"} {
			got := sanitizeUsername(in)
			if len(got) != 3 {
				t.Errorf("sanitizeUsername(%q) = %q, length %d, want exactly 3", in, got, len(got))
				continue
			}
			if !strings.HasPrefix(got, prefix) {
				t.Errorf("sanitizeUsername(%q) = %q, want it to start with %q", in, got, prefix)
			}
		}
	})

	// The padding path is the one with a random source and a fallback, so run it
	// enough times that an occasionally-illegal character would show up.
	t.Run("padding is always legal, repeatedly", func(t *testing.T) {
		for i := 0; i < 200; i++ {
			got := sanitizeUsername("x")
			if len(got) != 3 || !handleRe.MatchString(got) {
				t.Fatalf("iteration %d: sanitizeUsername(\"x\") = %q, want a 3-character legal handle", i, got)
			}
		}
	})
}
