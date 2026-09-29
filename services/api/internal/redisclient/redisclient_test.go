package redisclient

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		addr    string
		pass    string
		db      int
		useTLS  bool
		wantErr bool
	}{
		{name: "plain default port", in: "redis://cache.internal", addr: "cache.internal:6379"},
		{name: "explicit port", in: "redis://localhost:6380", addr: "localhost:6380"},
		{name: "password", in: "redis://:s3cret@cache:6379", addr: "cache:6379", pass: "s3cret"},
		{name: "user and password", in: "redis://default:s3cret@cache:6379", addr: "cache:6379", pass: "s3cret"},
		{name: "tls", in: "rediss://cache:6380", addr: "cache:6380", useTLS: true},
		{name: "db index", in: "redis://cache:6379/3", addr: "cache:6379", db: 3},
		{name: "db zero", in: "redis://cache:6379/0", addr: "cache:6379", db: 0},
		{name: "http rejected", in: "http://cache:6379", wantErr: true},
		{name: "no host", in: "redis://", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseURL(%q) = %+v, want error", tc.in, cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseURL(%q): %v", tc.in, err)
			}
			if cfg.Addr != tc.addr {
				t.Errorf("Addr = %q, want %q", cfg.Addr, tc.addr)
			}
			if cfg.Password != tc.pass {
				t.Errorf("Password = %q, want %q", cfg.Password, tc.pass)
			}
			if cfg.DB != tc.db {
				t.Errorf("DB = %d, want %d", cfg.DB, tc.db)
			}
			if cfg.UseTLS != tc.useTLS {
				t.Errorf("UseTLS = %v, want %v", cfg.UseTLS, tc.useTLS)
			}
		})
	}
}

func TestEncodeRESPArray(t *testing.T) {
	got := string(Encode([][]byte{[]byte("INCR"), []byte("k")}))
	want := "*2\r\n$4\r\nINCR\r\n$1\r\nk\r\n"
	if got != want {
		t.Fatalf("Encode = %q, want %q", got, want)
	}
}

// A bulk payload containing CRLF must be length-framed, not scanned for
// delimiters: without the length header an injected "\r\n$5\r\nEVILS" would let
// a caller splice extra RESP commands onto the wire.
func TestEncodeFramingSurvivesCRLFInPayload(t *testing.T) {
	evil := "x\r\n$5\r\nEVILS\r\n"
	enc := string(Encode([][]byte{[]byte("ECHO"), []byte(evil)}))
	if !strings.Contains(enc, "$14\r\nx\r\n$5\r\nEVILS\r\n") {
		t.Fatalf("payload not length-framed: %q", enc)
	}
}

func TestReadReply(t *testing.T) {
	tests := []struct {
		name    string
		wire    string
		want    any
		wantErr bool
	}{
		{name: "simple string", wire: "+OK\r\n", want: "OK"},
		{name: "integer", wire: ":42\r\n", want: 42},
		{name: "bulk", wire: "$5\r\nhello\r\n", want: "hello"},
		{name: "empty bulk", wire: "$0\r\n\r\n", want: ""},
		{name: "null bulk", wire: "$-1\r\n", want: nil},
		{name: "null array", wire: "*-1\r\n", want: nil},
		{name: "int array", wire: "*2\r\n:3\r\n:12000\r\n", want: []any{3, 12000}},
		{name: "nested array", wire: "*2\r\n:1\r\n$2\r\nok\r\n", want: []any{1, "ok"}},
		{name: "server error", wire: "-NOSCRIPT No matching script\r\n", wantErr: true},
		{name: "bad integer", wire: ":abc\r\n", wantErr: true},
		{name: "bad bulk length", wire: "$xyz\r\n", wantErr: true},
		{name: "bad array length", wire: "*xyz\r\n", wantErr: true},
		{name: "empty line", wire: "\r\n", wantErr: true},
		{name: "unknown prefix", wire: "@nope\r\n", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadReply(bufio.NewReader(strings.NewReader(tc.wire)))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ReadReply(%q) = %#v, want error", tc.wire, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadReply(%q): %v", tc.wire, err)
			}
			switch want := tc.want.(type) {
			case []any:
				arr, ok := got.([]any)
				if !ok || len(arr) != len(want) {
					t.Fatalf("ReadReply(%q) = %#v, want %#v", tc.wire, got, want)
				}
				for i := range want {
					if arr[i] != want[i] {
						t.Errorf("ReadReply(%q)[%d] = %#v, want %#v", tc.wire, i, arr[i], want[i])
					}
				}
			default:
				if got != tc.want {
					t.Fatalf("ReadReply(%q) = %#v, want %#v", tc.wire, got, tc.want)
				}
			}
		})
	}
}

// Truncated bulk bodies must error rather than return a short string, or a
// half-read reply would be parsed as valid.
func TestReadReplyRejectsTruncatedBulk(t *testing.T) {
	if got, err := ReadReply(bufio.NewReader(strings.NewReader("$10\r\nshort\r\n"))); err == nil {
		t.Fatalf("ReadReply = %#v, want error for truncated bulk", got)
	}
}
