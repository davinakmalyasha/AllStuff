package service_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A test file in this directory that uses another test file's unexported helpers
// must carry the same `//go:build !race` tag, because the helpers live in a tagged
// file.
//
// This is not hypothetical bookkeeping. auth_events_integration_test.go depends on
// fixtureCtx / newTestAuth / userWithPassword / testPassword from
// auth_2fa_integration_test.go, which is tagged `!race`. Without the same tag,
// `go test -race ./...` excludes the helper file, compiles the dependent file
// alone, and fails with "undefined: fixtureCtx" followed by a cascade of "too
// many errors" that buries the real cause. CI reported the cascade and nothing
// else, which cost a full push-and-wait cycle to diagnose.
//
// The tag exists for a good reason - these tests clone a database per test and
// re-migrate each clone, far too slow to run under the race detector as well as
// without it - so it cannot simply be dropped. Remembering it is the only
// alternative, and this makes forgetting loud instead of mysterious.
//
// Scanned as text rather than compiled, because the failure mode is precisely
// that it does not compile.
func TestFilesUsingSharedHelpersCarryTheRaceTag(t *testing.T) {
	t.Parallel()

	// Helpers that only exist inside a `!race`-tagged file. Adding a helper to a
	// tagged file without adding it here would make this lint incomplete, which is
	// stated in the comment below rather than pretended away.
	shared := regexp.MustCompile(`\b(fixtureCtx|newTestAuth|userWithPassword|testPassword|newTestAuth\b)\b`)

	entries, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}

	var offenders []string
	for _, name := range entries {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !shared.Match(src) {
			continue
		}
		if strings.Contains(string(src), "//go:build !race") {
			continue
		}
		offenders = append(offenders, name)
	}

	for _, name := range offenders {
		t.Errorf("%s uses helpers from a `//go:build !race` file but does not carry the tag itself. "+
			"`go test -race ./...` will fail to build with 'undefined: fixtureCtx'. "+
			"Add `//go:build !race` above the package clause.", name)
	}

	// A helper added to a tagged file and not listed here simply escapes the lint;
	// it does not produce a false failure. The trade is deliberate: a conservative
	// version that treated every tagged file's identifiers as shared would fail on
	// harmless files.
	t.Logf("checked %d test files against the shared-helper list", len(entries))
}
