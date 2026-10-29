package util

import (
	"strings"
	"uuid"
)

// Itoa converts a non-negative int to its decimal string.
func Itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// NewUUID returns a random (v4) UUID string. Deliberately v4, not v7:
// ARCHITECTURE documents v4 as the choice, and the version nibble is pinned
// by TestNewUUID_IsValidVersion4.
func NewUUID() string {
	return uuid.NewV4().String()
}

// IsUUID reports whether s is a well-formed UUID. Handlers use it before a
// string reaches a uuid column: an arbitrary literal otherwise fails inside
// Postgres (SQLSTATE 22P02) and surfaces as a 500 for what is a client error.
//
// uuid.Parse alone is too lenient for this job: it also accepts braces,
// urn: prefixes and dash-less forms, none of which the previous
// implementation accepted. The lowercase-canonical comparison re-narrows it
// to exactly the old set — canonical 8-4-4-4-12 hex in either case, pinned
// by TestIsUUID_*. The length pre-check is a fast path only: dropping it
// changes nothing observable (the comparison already implies length 36),
// which mutation testing confirmed — the compare flip is killable, the
// pre-check is not.
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return false
	}
	return u.String() == strings.ToLower(s)
}
