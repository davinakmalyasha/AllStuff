package ws

import (
	"bufio"
	"strings"
	"testing"
)

func readRESPFrom(t *testing.T, raw string) (any, error) {
	t.Helper()
	return readRESP(bufio.NewReader(strings.NewReader(raw)))
}

// readRESP decodes the subset of RESP the Redis pub/sub bridge needs. It had no
// coverage at all in the last measured profile.
func TestReadRESP(t *testing.T) {
	t.Run("integer", func(t *testing.T) {
		for raw, want := range map[string]int{":0\r\n": 0, ":42\r\n": 42, ":-1\r\n": -1} {
			got, err := readRESPFrom(t, raw)
			if err != nil {
				t.Errorf("%q: %v", raw, err)
				continue
			}
			if got != want {
				t.Errorf("%q = %v, want %d", raw, got, want)
			}
		}
	})

	t.Run("bulk string", func(t *testing.T) {
		got, err := readRESPFrom(t, "$5\r\nhello\r\n")
		if err != nil || got != "hello" {
			t.Errorf("got %#v, %v; want hello", got, err)
		}
	})

	t.Run("empty bulk string", func(t *testing.T) {
		got, err := readRESPFrom(t, "$0\r\n\r\n")
		if err != nil || got != "" {
			t.Errorf("got %#v, %v; want an empty string and no error", got, err)
		}
	})

	t.Run("bulk string containing CRLF", func(t *testing.T) {
		// Length-prefixed, so embedded newlines must survive intact. Splitting on
		// lines here would corrupt every payload that contains one.
		got, err := readRESPFrom(t, "$4\r\na\r\nb\r\n")
		if err != nil || got != "a\r\nb" {
			t.Errorf("got %#v, %v; want %q", got, err, "a\r\nb")
		}
	})

	t.Run("array", func(t *testing.T) {
		got, err := readRESPFrom(t, "*2\r\n$3\r\nfoo\r\n:7\r\n")
		if err != nil {
			t.Fatal(err)
		}
		arr, ok := got.([]any)
		if !ok || len(arr) != 2 || arr[0] != "foo" || arr[1] != 7 {
			t.Errorf("got %#v, want [foo 7]", got)
		}
	})

	t.Run("empty array", func(t *testing.T) {
		got, err := readRESPFrom(t, "*0\r\n")
		if err != nil {
			t.Fatal(err)
		}
		if arr, ok := got.([]any); !ok || len(arr) != 0 {
			t.Errorf("got %#v, want an empty array", got)
		}
	})

	t.Run("nested array", func(t *testing.T) {
		got, err := readRESPFrom(t, "*2\r\n*2\r\n:1\r\n:2\r\n:3\r\n")
		if err != nil {
			t.Fatal(err)
		}
		arr, _ := got.([]any)
		if len(arr) != 2 {
			t.Fatalf("got %#v, want 2 elements", got)
		}
		inner, ok := arr[0].([]any)
		if !ok || len(inner) != 2 || inner[0] != 1 || inner[1] != 2 {
			t.Errorf("inner = %#v, want [1 2]", arr[0])
		}
		if arr[1] != 3 {
			t.Errorf("second = %#v, want 3", arr[1])
		}
	})

	t.Run("error reply is an error carrying the message", func(t *testing.T) {
		_, err := readRESPFrom(t, "-ERR unknown command\r\n")
		if err == nil {
			t.Fatal("want an error for a - line")
		}
		if !strings.Contains(err.Error(), "ERR unknown command") {
			t.Errorf("error %q should carry the server's message", err)
		}
	})

	t.Run("malformed input is rejected", func(t *testing.T) {
		bad := []struct {
			raw string
			why string
		}{
			{"", "empty stream"},
			{"\r\n", "blank line"},
			{"+OK\r\n", "simple strings are not handled by this reader"},
			{"$abc\r\n", "bulk length is not a number"},
			{"$-1\r\n", "negative bulk length"},
			{"$3\r\nab", "bulk shorter than its declared length"},
			{":abc\r\n", "integer line is not a number"},
			{"*abc\r\n", "array length is not a number"},
		}
		for _, tc := range bad {
			if got, err := readRESPFrom(t, tc.raw); err == nil {
				t.Errorf("%q (%s) returned %#v and no error", tc.raw, tc.why, got)
			}
		}
	})

	t.Run("an array truncated mid-element fails", func(t *testing.T) {
		if got, err := readRESPFrom(t, "*2\r\n$3\r\nfoo\r\n"); err == nil {
			t.Errorf("got %#v and no error; a short array must not look complete", got)
		}
	})
}
