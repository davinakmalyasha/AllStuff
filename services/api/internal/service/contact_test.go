package service

import (
	"errors"
	"reflect"
	"testing"

	"bizverse/api/internal/domain"
)

// validateContact normalises the contact block to a known set of keys with
// trimmed values. It had no coverage at all in the last measured profile.
//
// Note what it does NOT do: it does not check that a website is https, or that
// an email is an address. That is deliberate rather than missed - both renderers
// already refuse to turn an unsafe value into a link (ssrContactHTML rejects
// non-https schemes, and the storefront's ContactRow passes the value through
// safeExternalUrl). Adding a scheme check here would also mean existing rows with
// an http:// website could no longer be saved, which is a data decision rather
// than a validation one. The tests below pin the normalisation and say so.
func TestValidateContact(t *testing.T) {
	t.Run("rejects a non-object", func(t *testing.T) {
		for _, v := range []any{"phone", 42, []any{}, nil} {
			_, err := validateContact(v)
			if !errors.Is(err, domain.ErrValidation) {
				t.Errorf("validateContact(%#v) err = %v, want ErrValidation", v, err)
			}
		}
	})

	t.Run("keeps the known keys and trims them", func(t *testing.T) {
		in := map[string]any{
			"phone": "  +62 812 3456  ", "email": "\tme@example.com\n",
			"website": " https://example.com ",
		}
		got, err := validateContact(in)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		want := map[string]any{
			"phone": "+62 812 3456", "email": "me@example.com", "website": "https://example.com",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	})

	t.Run("keeps every social key", func(t *testing.T) {
		socials := []string{"whatsapp", "instagram", "tiktok", "facebook", "x", "youtube", "line", "telegram"}
		in := map[string]any{}
		for _, k := range socials {
			in[k] = "@" + k
		}
		got, err := validateContact(in)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != len(socials) {
			t.Fatalf("kept %d of %d socials: %#v", len(got), len(socials), got)
		}
		for _, k := range socials {
			if got[k] != "@"+k {
				t.Errorf("%s = %#v, want %q", k, got[k], "@"+k)
			}
		}
	})

	t.Run("drops keys it does not know", func(t *testing.T) {
		// The column is jsonb, so anything could be in it. An unknown key
		// surviving here would be stored and then read back by the renderer.
		in := map[string]any{
			"phone": "123", "myspace": "@tom", "href": "https://evil.example",
			"__proto__": "x", "constructor": "y",
		}
		got, err := validateContact(in)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(got, map[string]any{"phone": "123"}) {
			t.Errorf("got %#v, want only the phone key", got)
		}
	})

	t.Run("drops empty and whitespace-only values", func(t *testing.T) {
		// An empty string stored here renders as an empty link, which is worse
		// than no link at all.
		in := map[string]any{
			"phone": "", "email": "   ", "website": "\t\n", "whatsapp": "@real",
		}
		got, err := validateContact(in)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(got, map[string]any{"whatsapp": "@real"}) {
			t.Errorf("got %#v, want only whatsapp", got)
		}
	})

	t.Run("drops values that are not strings", func(t *testing.T) {
		in := map[string]any{
			"phone": 62812345678, "email": nil, "website": []any{"https://example.com"},
			"x": map[string]any{"handle": "me"}, "youtube": "@ok",
		}
		got, err := validateContact(in)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(got, map[string]any{"youtube": "@ok"}) {
			t.Errorf("got %#v, want only youtube", got)
		}
	})

	t.Run("an empty object is valid and empty", func(t *testing.T) {
		got, err := validateContact(map[string]any{})
		if err != nil {
			t.Errorf("err = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %#v, want empty", got)
		}
	})

	t.Run("values are not scheme-checked here", func(t *testing.T) {
		// Pinned so the division of responsibility is explicit rather than
		// assumed: this function accepts whatever string it is given, and the
		// renderers are what refuse to link it. If a scheme check is ever added
		// here, this test should be changed deliberately.
		got, err := validateContact(map[string]any{
			"website": "javascript:alert(1)", "email": "not-an-address",
		})
		if err != nil {
			t.Fatalf("err = %v; storage accepts these, the renderers reject them", err)
		}
		if got["website"] != "javascript:alert(1)" {
			t.Errorf("website = %#v", got["website"])
		}
		if got["email"] != "not-an-address" {
			t.Errorf("email = %#v", got["email"])
		}
	})

	t.Run("the input map is not modified", func(t *testing.T) {
		in := map[string]any{"phone": "  123  ", "unknown": "kept?"}
		if _, err := validateContact(in); err != nil {
			t.Fatalf("err = %v", err)
		}
		if in["phone"] != "  123  " {
			t.Errorf("the caller's map was rewritten: %#v", in)
		}
		if _, ok := in["unknown"]; !ok {
			t.Error("the caller's map lost a key")
		}
	})
}
