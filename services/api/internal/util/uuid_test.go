package util

import (
	"strings"
	"testing"
)

// IsUUID is the malformed-id guard at the HTTP boundary: handlers call it
// before a path parameter reaches a uuid column, so a non-UUID becomes a 404
// instead of a Postgres 22P02 500. These cases pin the exact acceptance set —
// canonical 8-4-4-4-12 hex in either case, nothing else — so a refactor of the
// implementation (stdlib uuid.Parse accepts braces, urn: prefixes and
// dash-less forms) cannot silently widen what reaches the database.
func TestIsUUID_AcceptsCanonicalForms(t *testing.T) {
	for _, s := range []string{
		"f81d4fae-7dec-4d0a-a765-00a0c91e6bf6",
		"F81D4FAE-7DEC-4D0A-A765-00A0C91E6BF6",
		"00000000-0000-4000-8000-000000000000",
	} {
		if !IsUUID(s) {
			t.Errorf("IsUUID(%q) = false, want true", s)
		}
	}
}

func TestIsUUID_RejectsNonCanonicalForms(t *testing.T) {
	for _, s := range []string{
		"",
		"not-a-uuid",
		"f81d4fae-7dec-4d0a-a765-00a0c91e6bf6 ",
		" f81d4fae-7dec-4d0a-a765-00a0c91e6bf6",
		"f81d4fae7dec4d0aa76500a0c91e6bf6",                           // no dashes
		"{f81d4fae-7dec-4d0a-a765-00a0c91e6bf6}",                     // braces
		"urn:uuid:f81d4fae-7dec-4d0a-a765-00a0c91e6bf6",              // urn prefix
		"f81d4fae-7dec-4d0a-a765-00a0c91e6bf",                        // truncated
		"f81d4fae-7dec-4d0a-a765-00a0c91e6bf60",                      // extended
		"g81d4fae-7dec-4d0a-a765-00a0c91e6bf6",                       // non-hex
		"f81d4fae-7dec-4d0a-a765-00a0c91e6bf6\n",                     // newline
		"f81d4fae-7dec-4d0a-a765-00a0c91e6bf6'; DROP TABLE users;--", // injection
	} {
		if IsUUID(s) {
			t.Errorf("IsUUID(%q) = true, want false", s)
		}
	}
}

// NewUUID must stay a v4 UUID string: ARCHITECTURE documents v4 (not v7) as
// the deliberate choice, and 264 call sites take the string form.
func TestNewUUID_IsValidVersion4(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		id := NewUUID()
		if !IsUUID(id) {
			t.Fatalf("NewUUID() = %q, which IsUUID rejects", id)
		}
		if id != strings.ToLower(id) {
			t.Fatalf("NewUUID() = %q, want lowercase", id)
		}
		// Version nibble is the first char of the third group.
		if id[14] != '4' {
			t.Fatalf("NewUUID() = %q, want version nibble 4", id)
		}
		if seen[id] {
			t.Fatalf("NewUUID() repeated %q after %d draws", id, i)
		}
		seen[id] = true
	}
}
