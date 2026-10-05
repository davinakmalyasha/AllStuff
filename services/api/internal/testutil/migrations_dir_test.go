// `package testutil`, not `testutil_test`, because resolveMigrationsDir is
// deliberately unexported: it is harness plumbing, and exporting it would add
// public surface to a package that exists only to be imported by _test files.
package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrationsDirIsNotSilentlyWrong guards the harness against a path that
// resolves to nothing.
//
// Migrate() treats an empty migrations directory as a valid input -
// TestMigrateAppliesAnEmptyDirectory asserts exactly that - so a
// MIGRATIONS_DIR pointing at a non-existent directory produced a pristine but
// EMPTY template database and a green-looking migration replay. The first
// symptom was four thousand lines away in a different file:
//
//	stage invalid index: relation "categories" does not exist
//
// Nothing in the harness noticed that the directory it had been handed did not
// exist. This test makes the harness notice.
func TestMigrationsDirIsNotSilentlyWrong(t *testing.T) {
	dir := MigrationsDir(t)
	if dir == "" {
		t.Fatal("MigrationsDir returned an empty path")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("MigrationsDir %q is not readable: %v", dir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("MigrationsDir %q is empty", dir)
	}
}

// TestResolveMigrationsDirRejectsANonExistentDirectory is the negative case,
// and it is why the resolution is a plain function returning an error rather than
// a helper that calls t.Fatalf directly.
//
// Without this, resolve() could be simplified back to a bare os.Getenv and the
// whole class of bug would return with no test noticing.
func TestResolveMigrationsDirRejectsANonExistentDirectory(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "no-such-dir")
	t.Setenv("MIGRATIONS_DIR", bad)

	_, err := resolveMigrationsDir()
	if err == nil {
		t.Fatalf("resolveMigrationsDir(%q) returned no error; the guard is not wired up", bad)
	}
	// The message has to name the path AND say which working directory it was
	// resolved from, because that is the whole diagnosis and it is not obvious
	// from the path alone.
	//
	// filepath.Base rather than the whole path: the message formats the path with
	// %q, so on Windows the separators come out doubled and a substring match on
	// the raw path fails for a reason that has nothing to do with the message.
	for _, want := range []string{filepath.Base(bad), "working directory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q; an operator reading only this line cannot tell "+
				"which of the two working directories the path was resolved from", err, want)
		}
	}
}

// TestResolveMigrationsDirPrefersTheOverride documents the precedence, so a
// future refactor cannot quietly make the env var decorative.
func TestResolveMigrationsDirPrefersTheOverride(t *testing.T) {
	real, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("the default migrations dir does not resolve: %v", err)
	}
	t.Setenv("MIGRATIONS_DIR", real)
	overridden, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("the override did not resolve: %v", err)
	}
	if overridden != real {
		t.Errorf("MIGRATIONS_DIR was ignored: got %q, want %q", overridden, real)
	}
}
