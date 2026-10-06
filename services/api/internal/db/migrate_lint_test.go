package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// migrationsDir locates the migration directory from this package.
func migrationsDir(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("MIGRATIONS_DIR"); d != "" {
		return d
	}
	// internal/db -> internal -> api -> services -> repo root
	return filepath.Join("..", "..", "..", "..", "infra", "postgres", "migrations")
}

func loadMigrations(t *testing.T) []struct {
	Version string
	Body    string
	Header  MigrationHeader
} {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(migrationsDir(t), "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no migrations found in %s", migrationsDir(t))
	}
	var out []struct {
		Version string
		Body    string
		Header  MigrationHeader
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		version := filepath.Base(f)
		h, err := ParseHeader(string(b))
		if err != nil {
			// Reported by TestMigrationHeadersArePresent; keep loading so that
			// test sees every offender instead of only the first.
			h = MigrationHeader{}
		}
		out = append(out, struct {
			Version string
			Body    string
			Header  MigrationHeader
		}{version, string(b), h})
	}
	return out
}

// TestMigrationHeadersArePresent requires the `-- migrate:` block on every
// migration at or after the frozen baseline.
//
// The baseline (0001-0027) is exempt. Those files applied once, in order, to an
// empty database; rewriting them to add metadata would be churn on frozen
// history, and several of them carry documented defects that must NOT be
// retroactively "fixed" in place (see REGISTRY.md). Exempt-but-visible: they
// are logged so the exemption is never mistaken for an oversight.
func TestMigrationHeadersArePresent(t *testing.T) {
	var baseline, missing int
	for _, m := range loadMigrations(t) {
		if _, err := ParseHeader(m.Body); err != nil {
			if IsBaseline(m.Version) {
				baseline++
				continue
			}
			missing++
			t.Errorf("%s: %v", m.Version, err)
		}
	}
	if baseline > 0 {
		t.Logf("%d pre-baseline migration(s) carry no header — expected; %s is the frozen baseline",
			baseline, BaselineMigration)
	}
	_ = missing
}

func TestNonIdempotentMigrationsExplainThemselves(t *testing.T) {
	for _, m := range loadMigrations(t) {
		if m.Header.Idempotent == "no" && strings.TrimSpace(m.Header.Note) == "" {
			t.Errorf("%s: `migrate:idempotent no` requires a `migrate:note` explaining why", m.Version)
		}
		if m.Header.Idempotent == "" && !IsBaseline(m.Version) {
			// Missing header is TestMigrationHeadersArePresent's job; do not
			// double-report.
			continue
		}
	}
}

// Go's regexp is RE2 and has no negative lookahead, so each rule captures the
// guard as an OPTIONAL GROUP and the test checks whether that group matched.
// Capturing is more robust than string surgery: an earlier version matched
// `ADD COLUMN\s+([A-Za-z0-9_]+)` and then looked for "IF NOT EXISTS" after the
// match, which reported `ADD COLUMN IF NOT EXISTS` as an unguarded ADD COLUMN
// because the column-name group had swallowed the `IF`.
//
// Group layout is documented per rule. Index: 1=UNIQUE 2=CONCURRENTLY
// 3=IF NOT EXISTS 4=name.
var (
	reCreateTable = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+(IF\s+NOT\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
	// Drop-then-build is the recoverable shape for ANY index, not only concurrent
	// ones: a plain CREATE INDEX can also be interrupted (lock timeout, disk full,
	// a cancelled statement) and leave an INVALID index, which every later replay
	// silently skips because IF NOT EXISTS compares names only and never checks
	// pg_index.indisvalid.
	//
	// The concurrent-only form of reDropIndex is why 0048's index was reported as
	// "not replay-safe" even though it had the preceding DROP the rule asks for -
	// the drop-scan did not match a non-CONCURRENTLY DROP, so `dropped[name]` stayed
	// false and the rule fell through to its unguarded case. The fix belongs in the
	// pattern rather than in the migration: 0048's DROP INDEX is correct, and
	// weakening the migration to IF NOT EXISTS would have made it permanently
	// unrecoverable from an interrupted build.
	reDropIndex     = regexp.MustCompile(`(?is)\bDROP\s+INDEX\s+(CONCURRENTLY\s+)?(IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
	reCreateIndex   = regexp.MustCompile(`(?is)^\s*CREATE\s+(UNIQUE\s+)?INDEX\s+(CONCURRENTLY\s+)?(IF\s+NOT\s+EXISTS\s+)?([A-Za-z0-9_]+)`)
	reCreateType    = regexp.MustCompile(`(?is)^\s*CREATE\s+TYPE\s+([A-Za-z0-9_."]+)`)
	reCreateTrigger = regexp.MustCompile(`(?is)^\s*CREATE\s+TRIGGER\s+([A-Za-z0-9_]+)`)
	reAddColumn     = regexp.MustCompile(`(?is)\bALTER\s+TABLE\s+([A-Za-z0-9_."]+)\s+ADD\s+COLUMN\s+(IF\s+NOT\s+EXISTS\s+)?([A-Za-z0-9_]+)`)
	reAddConstraint = regexp.MustCompile(`(?is)\bADD\s+CONSTRAINT\s+([A-Za-z0-9_]+)`)
	reInsert        = regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+([A-Za-z0-9_."]+)`)
	// INDEX is deliberately absent: a plain DROP INDEX is covered by
	// reDropIndex below, which understands the CONCURRENTLY variant too.
	reUnguardedDrop  = regexp.MustCompile(`(?is)^\s*DROP\s+(TABLE|TRIGGER|TYPE|VIEW)\s+(IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
	reDropConstraint = regexp.MustCompile(`(?is)\bDROP\s+CONSTRAINT\s+IF\s+EXISTS\s+([A-Za-z0-9_]+)`)
)

// TestMigrationsDeclareTheirIdempotencyContract is the static half of the
// replay-safety contract. It cannot prove a migration is safe to re-run — only
// TestMigrateReplayIsSafe can, behaviourally — but it catches the specific
// shapes that have actually broken this repository:
//
//   - a bare INSERT seed (0002) that wedges startup when the version marker is
//     lost between COMMIT and the marker write
//   - ADD CONSTRAINT with no DROP CONSTRAINT IF EXISTS (0029 had twelve, and
//     they made a failed CONCURRENTLY statement unretryable)
//   - CREATE INDEX CONCURRENTLY IF NOT EXISTS (0029 had five, 0031 has 26), which
//     silently skips a rebuild of an INVALID index forever
//
// concurrentWord keeps the two index rules' messages in step, so a message cannot
// say CONCURRENTLY about a statement that is not.
func concurrentWord(concurrent bool) string {
	if concurrent {
		return " CONCURRENTLY"
	}
	return ""
}

// It is prefix matching, not SQL parsing, and it says so: DO blocks are
// reported as opaque rather than silently assumed clean.
func TestMigrationsDeclareTheirIdempotencyContract(t *testing.T) {
	for _, m := range loadMigrations(t) {
		if m.Header.Idempotent != "yes" {
			continue // "no" is a human claim, covered by the note requirement
		}
		if IsBaseline(m.Version) {
			continue // frozen baseline; exempt, not ignored
		}
		if m.Header.OpaqueDOBlocks > 0 {
			t.Logf("%s: %d DO block(s) not inspected by this linter (plpgsql is opaque to it)",
				m.Version, m.Header.OpaqueDOBlocks)
		}

		// Every index this file creates must be preceded by a DROP of the same name,
		// or a failed build is skipped forever. Group 1 is the optional CONCURRENTLY
		// and group 2 the optional IF EXISTS, so the NAME is group 3 - the concurrent-
		// only pattern this originally used put the name in group 2 and silently
		// recorded the wrong key for every non-concurrent drop.
		dropped := map[string]bool{}
		for _, d := range reDropIndex.FindAllStringSubmatch(m.Body, -1) {
			dropped[strings.Trim(d[3], `"`)] = true
		}
		// Constraints this file adds must have their drop in the same file.
		dropsConstraint := map[string]bool{}
		for _, d := range reDropConstraint.FindAllStringSubmatch(m.Body, -1) {
			dropsConstraint[d[1]] = true
		}

		for i, stmt := range SplitStatements(m.Body) {
			where := func(rule string) {
				t.Errorf("%s: statement %d: %s\n    > %s", m.Version, i+1, rule, firstLine(stmt))
			}

			if mm := reCreateTable.FindStringSubmatch(stmt); mm != nil && mm[1] == "" {
				where("CREATE TABLE " + mm[2] + " without IF NOT EXISTS is not replay-safe")
			}
			if mm := reCreateIndex.FindStringSubmatch(stmt); mm != nil {
				concurrent := strings.TrimSpace(mm[2]) != ""
				guarded := strings.TrimSpace(mm[3]) != ""
				name := mm[4]
				switch {
				case dropped[name]:
					// Drop-then-build is the correct shape for a concurrent index AND
					// for a plain one: in both cases a failed build is removed on the
					// next replay and rebuilt from clean. The old condition required
					// `concurrent &&`, so a non-concurrent index that DID have the
					// correct preceding DROP was reported as unsafe — which teaches
					// authors to "fix" a correct migration by swapping the DROP for
					// IF NOT EXISTS, the one change that makes an interrupted build
					// permanently unrecoverable.
					// IF NOT EXISTS here is not merely redundant — it is
					// actively misleading, because it reads as "this is safe to
					// skip" when the DROP above is what makes it safe.
					if guarded {
						where("CREATE INDEX" + concurrentWord(concurrent) + " " + name + " has both a preceding " +
							"DROP INDEX" + concurrentWord(concurrent) + " IF EXISTS and its own IF NOT EXISTS. " +
							"Keep the DROP (it is what makes a failed build recoverable) and drop the " +
							"IF NOT EXISTS, which reads as though skipping were the intended behaviour")
					}
				case concurrent:
					where("CREATE INDEX CONCURRENTLY " + name + " has no preceding " +
						"DROP INDEX CONCURRENTLY IF EXISTS " + name + ". IF NOT EXISTS matches on NAME and " +
						"never checks pg_index.indisvalid, so a failed build leaves an INVALID index that " +
						"every later replay silently skips")
				case !guarded:
					where("CREATE INDEX " + name + " without IF NOT EXISTS is not replay-safe")
				}
			}
			if mm := reCreateType.FindStringSubmatch(stmt); mm != nil {
				where("CREATE TYPE " + mm[1] + " has no IF NOT EXISTS form; guard it in a DO block " +
					"and declare `migrate:idempotent no`")
			}
			if mm := reCreateTrigger.FindStringSubmatch(stmt); mm != nil {
				where("CREATE TRIGGER " + mm[1] + " needs a preceding `DROP TRIGGER IF EXISTS` in the same file")
			}
			if mm := reAddColumn.FindStringSubmatch(stmt); mm != nil && mm[2] == "" {
				where("ALTER TABLE " + mm[1] + " ADD COLUMN " + mm[3] +
					" without IF NOT EXISTS is not replay-safe")
			}
			if mm := reUnguardedDrop.FindStringSubmatch(stmt); mm != nil && mm[2] == "" {
				where("DROP " + strings.ToUpper(mm[1]) + " " + mm[3] + " without IF EXISTS is not replay-safe")
			}
			// Groups: 1=CONCURRENTLY 2=IF EXISTS 3=name
			if mm := reDropIndex.FindStringSubmatch(stmt); mm != nil && mm[2] == "" {
				where("DROP INDEX " + strings.TrimSpace(mm[1]) + mm[3] +
					" without IF EXISTS is not replay-safe: on a fresh database the index does not exist yet, " +
					"and a bare DROP aborts the whole file with 42704")
			}
			if mm := reInsert.FindStringSubmatch(stmt); mm != nil && !strings.Contains(strings.ToUpper(stmt), "ON CONFLICT") {
				where("INSERT into " + mm[1] + " without ON CONFLICT is not replay-safe: a crash between " +
					"COMMIT and the version-marker write replays it (see REGISTRY.md rule 8)")
			}
			if mm := reAddConstraint.FindStringSubmatch(stmt); mm != nil && !dropsConstraint[mm[1]] {
				where("ADD CONSTRAINT " + mm[1] + " has no `ALTER TABLE ... DROP CONSTRAINT IF EXISTS " + mm[1] +
					"` in the same file, so a replay aborts with 42710 duplicate_object and the rest of the " +
					"file never runs")
			}
		}
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 110 {
		s = s[:110] + "..."
	}
	return s
}
