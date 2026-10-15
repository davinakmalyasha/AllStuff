package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// The README's opening block states three counts, and its whole framing is "here are
// claims, and here is how to check each one". A reviewer who spot-checks the easiest
// claims should not find them wrong.
//
// They were wrong. It advertised 31 migrations when there were 38 and 27 services when
// there were 25, because the block was written when the migration set ended at 0031 and
// nothing connected the text to the code. The route count was correct by luck.
//
// This is the same class of gate as the encoding check, the CSP check and the
// pagination lint: a claim in a file that no compiler reads, kept true by a test rather
// than by memory. It fails on any change that moves one of the numbers, which is the
// point - adding a migration or a service means updating one line of prose, and that is
// cheaper than a stale README.
//
// Reads files only. No database, no environment, so it cannot be skipped.

// repoRoot walks up from the package directory to the directory holding README.md.
//
// `go test` runs with the working directory set to the package, so the root is a fixed
// number of hops up - but hardcoding "four levels" would break silently the day this
// test moves. Walking until the marker is found fails loudly instead.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("walked to the filesystem root without finding README.md")
		}
		dir = parent
	}
}

func countLinesInFile(t *testing.T, path string, re *regexp.Regexp) int {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return len(re.FindAll(src, -1))
}

// countRoutes counts registrations across the package's non-test files.
//
// Counting the package rather than server.go alone, so moving the route table into
// another file does not silently drop to zero - a gate that reads nothing passes
// everything, which is the failure mode this whole file exists to avoid.
func countRoutes(t *testing.T) int {
	t.Helper()
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`mux\.HandleFunc\(`)
	total := 0
	for _, name := range entries {
		if filepath.Ext(name) != ".go" || len(name) > 8 && name[len(name)-8:] == "_test.go" {
			continue
		}
		total += countLinesInFile(t, name, re)
	}
	if total == 0 {
		t.Fatal("counted zero routes; the pattern no longer matches how routes are registered")
	}
	return total
}

func countServices(t *testing.T, root string) int {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(root, "services", "api", "internal", "service", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	// A service is a type with a constructor. Counting `type X struct` would also count
	// the input/result DTOs that live beside them - there are 53 of those - which is not
	// what the README means.
	re := regexp.MustCompile(`(?m)^func New[A-Za-z]+\(`)
	total := 0
	for _, path := range entries {
		if filepath.Ext(path) != ".go" || len(path) > 8 && path[len(path)-8:] == "_test.go" {
			continue
		}
		total += countLinesInFile(t, path, re)
	}
	if total == 0 {
		t.Fatal("counted zero services; the constructor naming convention has changed")
	}
	return total
}

func countMigrations(t *testing.T, root string) int {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(root, "infra", "postgres", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("found no migration files")
	}
	return len(entries)
}

func TestReadmeCountsMatchTheCode(t *testing.T) {
	root := repoRoot(t)
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("README.md: %v", err)
	}

	cases := []struct {
		what  string
		claim *regexp.Regexp
		truth int
	}{
		{"routes", regexp.MustCompile(`(\d+) routes`), countRoutes(t)},
		{"services", regexp.MustCompile(`(\d+) services`), countServices(t, root)},
		{"migrations", regexp.MustCompile(`(\d+) versioned SQL migrations`), countMigrations(t, root)},
	}

	for _, tc := range cases {
		m := tc.claim.FindSubmatch(readme)
		if m == nil {
			t.Errorf("README no longer states a %s count in the form this test matches (%s); "+
				"either restore the phrase or update this test, but do not leave the claim unchecked",
				tc.what, tc.claim)
			continue
		}
		claimed, err := strconv.Atoi(string(m[1]))
		if err != nil {
			t.Errorf("%s: %v", tc.what, err)
			continue
		}
		if claimed != tc.truth {
			t.Errorf("README claims %d %s; the code has %d. Update the README's opening block.",
				claimed, tc.what, tc.truth)
		}
	}
}
