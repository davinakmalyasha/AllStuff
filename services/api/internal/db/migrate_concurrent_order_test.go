package db

import (
	"regexp"
	"strings"
	"testing"
)

// TestNoConcurrentObjectIsReferencedByATransactionalStatement is the static form
// of the execution-order invariant in Migrate().
//
// Migrate() partitions each migration's statements into two buckets and runs
// them in a fixed order: EVERY transactional statement first, then every
// autocommit one (migrate.go:111-134). That order is forced - CREATE INDEX
// CONCURRENTLY is rejected inside a transaction - but it has a consequence
// nothing checked:
//
//	a statement that CREATES an object CONCURRENTLY cannot be followed, in the
//	same file, by a TRANSACTIONAL statement that references that object.
//
// The reference runs first and fails. This is not hypothetical: 0035 shipped a
// COMMENT ON INDEX immediately after its CREATE UNIQUE INDEX CONCURRENTLY, and
// the first applied run failed with
//
//	relation "subscriptions_business_uniq" does not exist (SQLSTATE 42P01)
//
// on a repository whose migration linters were otherwise all green.
//
// The fix was to delete the statement and document the rule in the migration
// header, where it is read by the audience that needs it. This test stops the
// next one trying the same thing.
func TestNoConcurrentObjectIsReferencedByATransactionalStatement(t *testing.T) {
	// Objects a CONCURRENTLY statement can create.
	creates := []*regexp.Regexp{
		regexp.MustCompile(`(?i)CREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`),
	}
	// Transactional statements that can reference a created object.
	references := []struct {
		what string
		re   *regexp.Regexp
	}{
		{"COMMENT ON INDEX", regexp.MustCompile(`(?i)COMMENT\s+ON\s+INDEX\s+([a-z_][a-z0-9_]*)`)},
		{"ALTER TABLE ... ADD CONSTRAINT", regexp.MustCompile(`(?i)ALTER\s+TABLE\s+[a-z_][a-z0-9_]*\s+ADD\s+(?:CONSTRAINT\s+)?[a-z_ ]*` +
			`(?:USING\s+INDEX\s+([a-z_][a-z0-9_]*))`)},
	}

	for _, m := range loadMigrations(t) {
		tx, conc := classify(m.Body)

		built := map[string]bool{}
		for _, stmt := range conc {
			for _, re := range creates {
				for _, match := range re.FindAllStringSubmatch(stmt, -1) {
					built[match[1]] = true
				}
			}
		}
		if len(built) == 0 {
			continue
		}

		for _, stmt := range tx {
			for _, ref := range references {
				for _, match := range ref.re.FindAllStringSubmatch(stmt, -1) {
					name := strings.ToLower(match[1])
					if built[name] {
						t.Errorf("%s: %s references index %q, but that index is built with CONCURRENTLY "+
							"and therefore runs AFTER every transactional statement in this file.\n"+
							"Migrate executes txStmts before concStmts because CONCURRENTLY cannot run inside "+
							"a transaction, so this reference always fails at apply time with SQLSTATE 42P01.\n"+
							"Move the reference into the migration header comment, or make it a "+
							"`/* autocommit */` statement, or move it to a later migration.",
							m.Version, ref.what, name)
					}
				}
			}
		}
	}
}
