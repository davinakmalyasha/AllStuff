package db

import (
	"strings"
	"testing"
)

// classify returns the statements Migrate() would run in the transaction and
// the ones it would run on autocommit, without touching a database.
//
// This is the same split Migrate performs, so a test built on it cannot
// disagree with the runner about which statements are transactional.
func classify(body string) (tx, conc []string) {
	for _, stmt := range SplitStatements(body) {
		if isAutocommit(stmt) {
			conc = append(conc, stmt)
		} else {
			tx = append(tx, stmt)
		}
	}
	return
}

func loadOne(t *testing.T, version string) string {
	t.Helper()
	b := loadMigrations(t)
	for _, m := range b {
		if m.Version == version {
			return m.Body
		}
	}
	t.Fatalf("migration %s not found", version)
	return ""
}

// TestSplitStatementsDropsWholeLineComments pins the three splitter behaviours
// that every migration in this repository depends on. If any of these change,
// SplitStatements must be re-read before trusting a migration file, because the
// failure mode is silent: a statement boundary moves and a fragment executes.
func TestSplitStatementsDropsWholeLineComments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "whole-line comments are dropped",
			in:   "-- a comment\nSELECT 1;\n-- another\nSELECT 2;",
			want: []string{"SELECT 1;", "SELECT 2;"},
		},
		{
			name: "semicolon inside a dollar-quoted body does not split",
			in:   "DO $x$ BEGIN PERFORM 1; PERFORM 2; END $x$;\nSELECT 3;",
			want: []string{"DO $x$ BEGIN PERFORM 1; PERFORM 2; END $x$;", "SELECT 3;"},
		},
		{
			name: "statement ends only on a line-final semicolon",
			in:   "SELECT 1;\nSELECT 2",
			want: []string{"SELECT 1;", "SELECT 2"},
		},
		{
			name: "trailing line comment suppresses the boundary",
			// Documented hazard: SplitStatements writes in-line comments into
			// the buffer, so `SELECT 1; -- note` never ends in ';' and the two
			// statements merge into one. This is why every comment in this
			// repository's migrations is on its own line.
			in:   "SELECT 1; -- note\nSELECT 2;",
			want: []string{"SELECT 1; -- note\nSELECT 2;"},
		},
		{
			name: "block comment survives and is valid SQL",
			in:   "/* autocommit */ SELECT 1;",
			want: []string{"/* autocommit */ SELECT 1;"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitStatements(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d statements %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("statement %d:\n got %q\nwant %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestIndexMigrationsRouteConcurrentStatementsToAutocommit checks the routing
// that decides whether a statement runs inside the migration transaction.
//
// A CREATE INDEX CONCURRENTLY inside a transaction is rejected outright by
// Postgres, so misrouting is a hard boot failure rather than a subtle bug —
// but the DROP CONSTRAINT ordering in 0029 is the opposite: misrouting that is
// invisible until a concurrent insert duplicates a slug.
func TestIndexMigrationsRouteConcurrentStatementsToAutocommit(t *testing.T) {
	for _, version := range []string{"0029_integrity.sql", "0031_index_hot.sql"} {
		t.Run(version, func(t *testing.T) {
			body := loadOne(t, version)
			tx, conc := classify(body)

			for _, s := range tx {
				if strings.Contains(s, "INDEX CONCURRENTLY") {
					t.Errorf("transactional statement would be rejected by Postgres:\n%s", firstLine(s))
				}
			}
			hasConcurrent := false
			for _, s := range conc {
				if strings.Contains(s, "INDEX CONCURRENTLY") {
					hasConcurrent = true
				}
			}
			if !hasConcurrent {
				t.Error("no INDEX CONCURRENTLY statement was routed to autocommit")
			}
		})
	}
}

// TestSlugConstraintIsDroppedAfterItsReplacement is the ordering assertion for
// the one window in this schema where a mistake is invisible.
//
// businesses_slug_live (partial unique on live rows) replaces businesses_slug_key
// (unique on all rows). If the DROP runs first, then between the two statements
// `businesses.slug` has NO uniqueness at all, and a single concurrent insert of
// a duplicate in that window wedges the file permanently. A partial unique index
// and a full unique constraint can legally coexist — the partial one is strictly
// weaker — so build-then-drop is safe and drop-then-build is not.
func TestSlugConstraintIsDroppedAfterItsReplacement(t *testing.T) {
	body := loadOne(t, "0029_integrity.sql")
	_, conc := classify(body)

	buildAt, dropAt := -1, -1
	for i, s := range conc {
		if strings.Contains(s, "CREATE UNIQUE INDEX CONCURRENTLY businesses_slug_live") {
			buildAt = i
		}
		if strings.Contains(s, "DROP CONSTRAINT IF EXISTS businesses_slug_key") {
			dropAt = i
		}
	}
	if buildAt < 0 {
		t.Fatal("businesses_slug_live is not built on autocommit; the soft-delete fix is not applied")
	}
	if dropAt < 0 {
		t.Fatal("businesses_slug_key is never dropped; soft-deleted slugs stay reserved forever, " +
			"which is the bug this migration exists to fix")
	}
	if dropAt < buildAt {
		t.Errorf("businesses_slug_key is dropped at autocommit statement %d, before its replacement is "+
			"built at %d. Dropping first opens a window in which businesses.slug has no uniqueness at all",
			dropAt, buildAt)
	}
}

// TestEveryConcurrentIndexBuildIsPrecededByItsDrop is the static form of the
// INVALID-index invariant. TestMigrateReplayIsSafe proves it behaviourally, but
// only in environments that have a database; this runs everywhere.
func TestEveryConcurrentIndexBuildIsPrecededByItsDrop(t *testing.T) {
	for _, m := range loadMigrations(t) {
		if IsBaseline(m.Version) {
			continue
		}
		_, conc := classify(m.Body)
		seen := map[string]bool{}
		for _, s := range conc {
			up := strings.ToUpper(s)
			if strings.Contains(up, "DROP INDEX CONCURRENTLY") {
				seen[indexName(s)] = true
				continue
			}
			if !strings.Contains(up, "CREATE") || !strings.Contains(up, "INDEX CONCURRENTLY") {
				continue
			}
			name := indexName(s)
			if !seen[name] {
				t.Errorf("%s: CREATE INDEX CONCURRENTLY %s is not preceded by a DROP of the same name", m.Version, name)
			}
		}
	}
}

// indexName pulls the identifier out of a CREATE/DROP INDEX statement.
func indexName(stmt string) string {
	fields := strings.Fields(stmt)
	for i, f := range fields {
		up := strings.ToUpper(f)
		if up == "EXISTS" || up == "CONCURRENTLY" || up == "IF" || up == "NOT" {
			continue
		}
		if up == "INDEX" {
			if i+1 < len(fields) {
				return strings.Trim(fields[i+1], `;"`)
			}
			return ""
		}
	}
	return ""
}
