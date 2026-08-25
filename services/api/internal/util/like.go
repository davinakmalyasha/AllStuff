package util

import "strings"

// EscapeLike neutralizes LIKE/ILIKE metacharacters so user input can only
// match literally. Without it, a "%" query forces leading-wildcard scans and
// "_" acts as a single-char wildcard. Postgres' default LIKE escape char is
// the backslash.
func EscapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
