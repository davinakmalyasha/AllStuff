package db

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Migration metadata contract.
//
// Every migration from `baselineMigration` onward must carry a header block:
//
//	-- migrate:idempotent  yes
//	-- migrate:concurrent  false
//	-- migrate:seed        none
//	-- migrate:risk        DML
//	-- migrate:note        one line: what this file assumes and why it is safe to replay
//
// The block is read from the RAW file bytes, not from SplitStatements output,
// because SplitStatements drops whole-line `--` comments before a statement is
// ever assembled. The header is metadata for humans and for the linter; it is
// not SQL.
//
// Why the header exists at all: a hand-written .sql file has no parser in
// front of it, so "is this migration replay-safe?" was previously a matter of
// opinion. `migrate:idempotent yes` is an explicit claim that a human made, and
// TestMigrationsAreReplaySafe then PROVES it behaviourally by deleting the
// version marker and re-running. A file that says `no` has to say why.

// BaselineMigration is the last migration of the frozen pre-audit schema.
//
// Files up to and including it are applied once, in order, to an empty
// database and are never replayed in practice, so the replay contract is not
// enforced on them. They are EXEMPT, not ignored: their headers are still
// required, and TestMigrationHeadersArePresent reports any that are missing, so
// the exemption is always visible rather than implicit.
//
// Three defects in this baseline are documented in
// infra/postgres/migrations/REGISTRY.md and fixed by later files:
//   - 0002's seed has no ON CONFLICT and can wedge startup permanently
//   - 0013/0015 created two byte-identical indexes on `notifications`
//   - 0015's `CREATE INDEX IF NOT EXISTS` matched an existing NAME with a
//     different column list, so the intended index was never built
const BaselineMigration = "0027_perf_indexes2.sql"

// MigrationHeader is the parsed `-- migrate:` block.
type MigrationHeader struct {
	// Idempotent is "yes" or "no". Anything else is a parse error — a
	// typo'd header that silently disabled the linter would be worse than
	// no header at all.
	Idempotent string
	// Concurrent is true when the file contains INDEX CONCURRENTLY or an
	// /* autocommit */ statement.
	Concurrent bool
	// Seed names the table a file seeds, or "none". Seeds follow a stricter
	// contract than other DML (see REGISTRY.md rule 7).
	Seed string
	// Risk is DDL, DML, or MIXED.
	Risk string
	// Note is free text. Required when Idempotent is "no".
	Note string
	// OpaqueDOBlocks counts `DO $tag$ ... $tag$` bodies. The linter cannot see
	// inside plpgsql, so it reports this count rather than pretending to have
	// checked. A DO block is a documented escape hatch.
	OpaqueDOBlocks int
}

const headerPrefix = "-- migrate:"

// ParseHeader reads the `-- migrate:` block from the first 40 lines of a
// migration file. Leading prose comments before the block are allowed, so a file
// can open with an explanation of what it fixes.
func ParseHeader(body string) (MigrationHeader, error) {
	var h MigrationHeader
	seen := false
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for i := 0; i < 40 && sc.Scan(); i++ {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, headerPrefix) {
			continue
		}
		seen = true
		key, val, ok := strings.Cut(strings.TrimPrefix(line, headerPrefix), " ")
		if !ok {
			return h, fmt.Errorf("malformed header line %q: expected `-- migrate:<key> <value>`", line)
		}
		val = strings.TrimSpace(val)
		switch key {
		case "idempotent":
			if val != "yes" && val != "no" {
				return h, fmt.Errorf("migrate:idempotent must be yes or no, got %q", val)
			}
			h.Idempotent = val
		case "concurrent":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return h, fmt.Errorf("migrate:concurrent must be a bool, got %q", val)
			}
			h.Concurrent = b
		case "seed":
			h.Seed = val
		case "risk":
			h.Risk = val
		case "note":
			h.Note = val
		default:
			return h, fmt.Errorf("unknown header key %q", key)
		}
	}
	if !seen {
		return h, fmt.Errorf("missing `-- migrate:` header block (see REGISTRY.md rules 2 and 6-9)")
	}
	h.OpaqueDOBlocks = countDOBodies(body)
	return h, nil
}

// IsBaseline reports whether a filename is at or before the frozen baseline.
func IsBaseline(version string) bool {
	// Lexical comparison is the same ordering the runner applies, and the
	// numbers are zero-padded, so string order IS version order.
	return version <= BaselineMigration
}

// countDOBodies counts `DO $tag$ ... $tag$` blocks. Each opens with
// "DO" and a dollar-quote open and closes with the matching close, so a simple
// state machine over the dollar-quote toggles is enough.
func countDOBodies(body string) int {
	n := 0
	lines := strings.Split(body, "\n")
	inDollar := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inDollar && (strings.HasPrefix(trimmed, "DO $$") || strings.HasPrefix(trimmed, "DO $")) {
			n++
		}
		if toggleDollarQuotes(line, &inDollar) {
			continue
		}
	}
	return n
}

// toggleDollarQuotes advances the in-dollar-body flag across a line. It mirrors
// the dollar-quote handling in SplitStatements so the two cannot disagree about
// where a body ends.
func toggleDollarQuotes(line string, inDollar *bool) bool {
	for i := 0; i < len(line); i++ {
		if line[i] != '$' {
			continue
		}
		if i+1 < len(line) && line[i+1] == '$' {
			*inDollar = !*inDollar
			i++
			continue
		}
		if i+1 < len(line) && isQuoteChar(line[i+1]) {
			end := i + 2
			for end < len(line) && line[end] != '$' {
				end++
			}
			if end < len(line) {
				*inDollar = !*inDollar
				i = end
				continue
			}
		}
	}
	return false
}
