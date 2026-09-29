//go:build !race

package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EXCLUDED FROM -race ON PURPOSE
// -------------------------------
// Go defines the `race` build constraint when -race is enabled, and this file
// opts out of it. The reason is measured, not theoretical: this suite clones a
// database ~20 times and re-migrates each clone, which takes roughly 90 seconds
// and about 820 seconds under the race detector. That is past the default
// 10-minute `go test` timeout, so `go test -race ./...` failed with a bare
// "panic: test timed out" — which reads as a hung test and is actually a slow
// one, in a suite where the hang is the signal you care about.
//
// Nothing here tests concurrency. It tests whether Postgres accepts a sequence
// of statements and whether the resulting schema is unchanged; the race detector
// has nothing to observe. Meanwhile the tests that DO test concurrency — the
// TOTP replay guard, the WS hub, the goroutine panic isolator, the rate-limit
// DoS regression — are small, fast, and meaningless without -race.
//
// So the two concerns are separated by construction:
//
//	go test -race ./...                                 # concurrency, fast
//	TEST_DATABASE_URL=... go test ./internal/db/ ...    # migrations, slow
//
// The static half of the contract (TestMigrationsDeclareTheirIdempotencyContract
// and the routing tests) lives in files with no constraint, so it is checked on
// every run, race or not.

// knownNonReplayable is the ratchet.
//
// Every entry was verified against a real PostgreSQL instance by running this
// test, and each carries the reason it is tolerated rather than fixed. Editing
// the baseline is deliberately out of scope: those files are frozen schema
// history, and 0001 in particular cannot be made replayable without rewriting
// the entire bootstrap.
//
// The point of listing them is that the list is CLOSED. A new migration that
// cannot be replayed fails this test, because it will not be in this map. That
// is the difference between documenting debt and hiding it — a reviewer can
// check every entry against the file it names.
var knownNonReplayable = map[string]string{
	"0001_initial.sql": "bootstrap: 35 CREATE TABLE, 6 CREATE TYPE and 7 CREATE TRIGGER statements " +
		"have no IF NOT EXISTS form. A crash inside 0001 rolls the whole transaction back, so it is never " +
		"partially applied and never needs replaying.",
	"0006_business_timezone.sql":  "ADD COLUMN businesses.timezone without IF NOT EXISTS.",
	"0007_site_config_digest.sql": "CREATE TABLE site_config and ADD COLUMN users.digest_opt_in, neither guarded.",
	"0008_notification_prefs.sql": "ADD COLUMN users.notification_prefs without IF NOT EXISTS.",
	"0009_review_photos_qa_follows.sql": "CREATE TABLE for questions/answers/follows/business_updates, plus an " +
		"unguarded ADD COLUMN and index.",
	"0010_resubmit_appeals.sql":      "CREATE TABLE appeals, plus an unguarded ADD COLUMN and index.",
	"0012_amenities_searches.sql":    "CREATE TABLE saved_searches, plus an unguarded ADD COLUMN and index.",
	"0013_notification_channels.sql": "unguarded CREATE INDEX idx_notifications_type.",
	"0015_perf_indexes.sql": "THE INTERESTING ONE. All six statements are correctly written as " +
		"`CREATE INDEX IF NOT EXISTS`, so the static linter passes it and it fails no check. It still " +
		"cannot be replayed, because 0031 deliberately DROPPED three of the indexes it creates " +
		"(idx_reviews_business_rating, idx_engagement_events_target and idx_notifications_user_type, the " +
		"last of which is a byte-identical duplicate on the highest-write table in the schema). " +
		"Re-running 0015 finds those three names missing and recreates them, while 0031's own marker is " +
		"already recorded so its drops never re-run. The database is left permanently carrying exactly " +
		"the redundant write amplification 0031 existed to remove, and nothing reports an error. " +
		"This is the failure mode the replay test was written to find and the only one that is silent " +
		"rather than loud. Not fixed here because 0015 is frozen; do not edit 0015 to 'fix' it, because " +
		"every already-migrated environment records the file as applied and would never see the edit.",
	"0017_search_alerts.sql": "unguarded ADD COLUMN on saved_searches.",
	"0025_audit_fixes.sql": "CREATE TABLE job_runs/consumed_tokens, an ADD CONSTRAINT with no DROP, and an " +
		"unguarded CREATE INDEX.",
	"0026_system_chat_messages.sql": "two ADD CONSTRAINT statements with no preceding DROP CONSTRAINT IF " +
		"EXISTS, so a replay aborts with 42710.",
}

// TestMigrateReplayIsSafe is the behavioural half of the migration contract.
//
// TestMigrationsDeclareTheirIdempotencyContract checks the SHAPE of a migration
// from a fixed rule table. That catches the mistakes this repository has
// actually made, but it is a heuristic and it cannot see inside a DO block. This
// test needs no heuristic at all — it reproduces the exact crash window and
// observes what Postgres does:
//
//  1. migrate the whole chain into a template database
//  2. for each migration, clone the template, DELETE that migration's version
//     marker, and re-run the chain
//  3. require that the re-run succeeds and that the schema fingerprint is
//     unchanged
//
// Deleting the marker is the precise simulation of "the process died between
// COMMIT and the marker write", which is the only window that matters: before
// COMMIT nothing happened, and after the marker the file is skipped entirely.
//
// Requires TEST_DATABASE_URL pointing at a database this test may CREATE and
// DROP siblings of. It is destructive to that server's sibling databases and
// must never be pointed at a database holding data.

// replayAdminDSN reads the DSN for the privileged connection.
func replayAdminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping the behavioural migration replay test")
	}
	return dsn
}

// schemaFingerprint is a deterministic digest of everything a migration is
// supposed to be able to change: tables and their columns, indexes, and
// constraints including whether each has been VALIDATEd.
//
// Comparing a digest rather than "the re-run did not error" is what makes the
// assertion meaningful. A migration that is technically replayable but quietly
// drops an index on the way through passes a no-error check and fails this one.
func schemaFingerprint(ctx context.Context, t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	const q = `
	SELECT md5(string_agg(src, E'\n' ORDER BY src)) FROM (
	    SELECT 'IDX ' || indexname || ' | ' || indexdef AS src
	      FROM pg_indexes WHERE schemaname = 'public'
	    UNION ALL
	    SELECT 'CON ' || conname || ' | ' || pg_get_constraintdef(c.oid) || ' | validated=' || convalidated
	      FROM pg_constraint c
	      JOIN pg_class r     ON r.oid = c.conrelid
	      JOIN pg_namespace n ON n.oid = r.relnamespace
	     WHERE n.nspname = 'public'
	    UNION ALL
	    SELECT 'COL ' || table_name || '.' || column_name || ' | ' || data_type
	           || ' | nullable=' || is_nullable || ' | default=' || coalesce(column_default, '')
	      FROM information_schema.columns WHERE table_schema = 'public'
	    UNION ALL
	    SELECT 'TYP ' || t.typname
	      FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
	     WHERE n.nspname = 'public' AND t.typtype = 'e'
	) s`
	var fp string
	if err := p.QueryRow(ctx, q).Scan(&fp); err != nil {
		t.Fatalf("schema fingerprint: %v", err)
	}
	return fp
}

func replayPool(t *testing.T, dsn, dbname string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	cfg, err := pgxpool.ParseConfig(dsn + sep + "dbname=" + dbname)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(ctx); err != nil {
		t.Fatalf("connect to %s: %v", dbname, err)
	}
	return p
}

// dropIfExists issues a DROP DATABASE, tolerating the case where the database
// does not exist. Uses FORCE so a lingering connection from a previous failed
// run cannot block teardown.
func dropIfExists(ctx context.Context, t *testing.T, admin *pgxpool.Pool, name string) {
	t.Helper()
	_, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("drop %s: %v", name, err)
	}
}

// TestKnownNonReplayableEntriesStillExist keeps the ratchet honest in both
// directions.
//
// A stale key would leave a file exempted from the replay test that no longer
// exists, which reads as a permanent excuse. A file that becomes replayable
// should have its entry removed, because the map is the documented debt list
// and a fixed defect still sitting in it overstates how much is broken.
func TestKnownNonReplayableEntriesStillExist(t *testing.T) {
	present := map[string]bool{}
	for _, m := range loadMigrations(t) {
		present[m.Version] = true
	}
	for version := range knownNonReplayable {
		if !present[version] {
			t.Errorf("knownNonReplayable lists %q, which is not a migration file. Remove the stale entry.",
				version)
		}
	}
	if len(knownNonReplayable) == 0 {
		t.Error("knownNonReplayable is empty; if the baseline was fixed, delete this test " +
			"rather than leaving a ratchet that checks nothing")
	}
}

func TestMigrateReplayIsSafe(t *testing.T) {
	dsn := replayAdminDSN(t)
	ctx := context.Background()
	dir := migrationsDir(t)

	admin := replayPool(t, dsn, "postgres")

	const tmpl = "bizverse_replay_tpl"
	dropIfExists(ctx, t, admin, tmpl)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	defer func() {
		dropIfExists(ctx, t, admin, tmpl)
		admin.Close()
	}()

	// Build the template once: this is the state a healthy, fully-migrated
	// database is in.
	tplPool := replayPool(t, dsn, tmpl)
	if err := Migrate(ctx, tplPool, dir); err != nil {
		t.Fatalf("baseline migrate: %v", err)
	}
	want := schemaFingerprint(ctx, t, tplPool)

	// Every recorded migration, in the order the runner applied them.
	rows, err := tplPool.Query(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	rows.Close()
	if len(versions) == 0 {
		t.Fatal("no migrations were recorded")
	}
	sort.Strings(versions)

	// The pool must be closed before the clone loop: CREATE DATABASE ... TEMPLATE
	// refuses to run while any session is connected to the source, and
	// "source database is being accessed by other users" would otherwise fail
	// every subtest.
	tplPool.Close()

	for _, version := range versions {
		reason, tolerated := knownNonReplayable[version]
		t.Run(version, func(t *testing.T) {
			if tolerated {
				t.Skipf("known baseline defect, documented in knownNonReplayable: %s", reason)
			}
			scratch := "bizverse_replay_" + strings.NewReplacer(".", "_", "-", "_", "/", "_").Replace(version)
			if len(scratch) > 60 {
				scratch = scratch[:60]
			}
			dropIfExists(ctx, t, admin, scratch)
			defer dropIfExists(ctx, t, admin, scratch)

			// Clone the migrated state. Postgres 15+ allows TEMPLATE to name a
			// database, so no pg_dump/restore round trip and no dependency on
			// the filesystem.
			if _, err := admin.Exec(ctx, "CREATE DATABASE "+scratch+" TEMPLATE "+tmpl); err != nil {
				t.Fatalf("clone: %v", err)
			}
			p := replayPool(t, dsn, scratch)
			defer p.Close()

			// Reproduce the crash window exactly: the file's effects are
			// committed and its marker is not.
			if _, err := p.Exec(ctx, "DELETE FROM schema_migrations WHERE version = $1", version); err != nil {
				t.Fatalf("clear marker: %v", err)
			}

			if err := Migrate(ctx, p, dir); err != nil {
				t.Fatalf("replay of %s failed, so the migration is NOT idempotent.\n"+
					"Re-running it after a crash between COMMIT and the version-marker write would "+
					"wedge startup permanently:\n%v", version, err)
			}
			if got := schemaFingerprint(ctx, t, p); got != want {
				t.Errorf("replay of %s changed the schema.\n"+
					"  before: %s\n  after:  %s\n"+
					"The statements are individually tolerated by Postgres but they are not a no-op, "+
					"so something the file intended to be permanent is not (or vice versa)",
					version, want, got)
			}

			// The marker must come back, or the next boot replays the file
			// forever.
			var n int
			if err := p.QueryRow(ctx,
				"SELECT count(*) FROM schema_migrations WHERE version = $1", version).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Errorf("replay of %s did not re-record its version marker", version)
			}
		})
	}
}

// TestMigrateFailsOnInvalidIndex proves that the INVALID-index state — the one
// that `CREATE INDEX CONCURRENTLY IF NOT EXISTS` silently skipped past — is now
// a loud boot failure instead of a quiet permanent gap.
func TestMigrateFailsOnInvalidIndex(t *testing.T) {
	dsn := replayAdminDSN(t)
	ctx := context.Background()
	dir := migrationsDir(t)

	admin := replayPool(t, dsn, "postgres")
	defer admin.Close()

	const db = "bizverse_replay_invalid"
	dropIfExists(ctx, t, admin, db)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
	defer dropIfExists(ctx, t, admin, db)

	p := replayPool(t, dsn, db)
	defer p.Close()
	if err := Migrate(ctx, p, dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Manufacture the exact state an interrupted concurrent build leaves:
	// a row in pg_class with indisvalid = false.
	//
	// Two separate Exec calls on purpose. pgx sends a multi-statement string
	// through the simple protocol, which wraps it in an implicit transaction,
	// and CREATE INDEX CONCURRENTLY is rejected inside one — the same rule
	// db/migrate.go's splitter exists to work around.
	if _, err := p.Exec(ctx,
		"CREATE INDEX CONCURRENTLY bizverse_fake_invalid ON categories (upper(name))"); err != nil {
		t.Fatalf("stage invalid index: %v", err)
	}
	if _, err := p.Exec(ctx,
		"UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'bizverse_fake_invalid'::regclass"); err != nil {
		t.Fatalf("mark index invalid: %v", err)
	}

	// Migrate() is a no-op for an already-fully-migrated database, so the only
	// thing it can do here is run the validity check — which must fail.
	err := Migrate(ctx, p, dir)
	if err == nil {
		t.Fatal("Migrate() returned nil with an INVALID index present; the index would be " +
			"silently skipped by every future IF NOT EXISTS replay")
	}
	if !strings.Contains(err.Error(), "bizverse_fake_invalid") {
		t.Errorf("error should name the offending index so an operator knows what to drop, got: %v", err)
	}
}

// TestMigrateAppliesAnEmptyDirectory is a guard on the guard: Migrate must be a
// clean no-op when there is nothing to do, or the replay test above would be
// testing a function that always errors.
func TestMigrateAppliesAnEmptyDirectory(t *testing.T) {
	dsn := replayAdminDSN(t)
	ctx := context.Background()
	admin := replayPool(t, dsn, "postgres")
	defer admin.Close()

	const db = "bizverse_replay_empty"
	dropIfExists(ctx, t, admin, db)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
	defer dropIfExists(ctx, t, admin, db)

	p := replayPool(t, dsn, db)
	defer p.Close()

	empty := t.TempDir()
	if err := Migrate(ctx, p, empty); err != nil {
		t.Fatalf("Migrate on an empty directory: %v", err)
	}
	if _, err := filepath.Glob(filepath.Join(empty, "*.sql")); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := p.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("recorded %d versions for an empty directory, want 0", n)
	}
	fmt.Fprintln(os.Stderr, "empty-directory no-op verified")
}
