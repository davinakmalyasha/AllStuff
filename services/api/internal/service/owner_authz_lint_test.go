package service

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Owner-only authorization is a recurring defect here, and it has been found FIVE
// times.
//
// products.own was fixed once, with a comment explaining that a co-owner could edit
// the storefront and then got a 403 on the product catalog. That comment stated the
// rule - use CanManageBusiness, not the owner_id column - and the same bug was still
// live in four other places: engagement.isOwner, chat message attribution, review
// deletion, and the follow / message-your-own-business guards. None of them would be
// found by reading the place that had already been fixed.
//
// So the rule is enforced rather than remembered. Comparing an owner_id column is not
// wrong everywhere - two notification-dedup checks genuinely ask about the owner - but
// it is wrong often enough, and silently enough, that every occurrence must justify
// itself in the source.
//
// An exempt line carries `lint:allow` and a reason. That is the whole mechanism: not
// "compare owner_id freely" and not "never compare owner_id", but "say why this one
// is about the owner rather than about authorisation".
//
// Scanned as text with comments stripped, because a comment-blind lint is unusable
// here: the fix comments in this package QUOTE the old code - "this compared
// `b.OwnerID == userID`" - so a naive scan flags the sentences that document the
// defect. blankComments preserves byte offsets so reported line numbers still point
// at the right place in the original file.

var ownerCompare = regexp.MustCompile(`(?i)\b\w+\.OwnerID\s*(==|!=)`)
var lintAllow = regexp.MustCompile(`lint:allow`)

func TestAuthorizationUsesTheCoOwnerPredicateNotTheOwnerColumn(t *testing.T) {
	t.Parallel()

	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	var offenders []string
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cleaned := blankComments(string(src))

		// The exemption is read from the ORIGINAL line, not the blanked one: the
		// reason is written in a trailing comment, which blanking removes. Checking
		// the whole file instead would let one justified line exempt every other.
		original := strings.Split(string(src), "\n")

		for i, line := range strings.Split(cleaned, "\n") {
			if !ownerCompare.MatchString(line) {
				continue
			}
			var orig string
			if i < len(original) {
				orig = original[i]
			}
			if lintAllow.MatchString(orig) {
				continue
			}
			offenders = append(offenders, sprintfLine(name, i+1, strings.TrimSpace(orig)))
		}
	}

	for _, o := range offenders {
		t.Error(o)
	}
}

func sprintfLine(file string, line int, text string) string {
	return file + ":" + strconv.Itoa(line) +
		": compares a business owner_id column. Business-scoped AUTHORIZATION must go through " +
		"CanManageBusiness so accepted co-owners are included (PRD 5.9.3). If this really is about the " +
		"owner rather than about who may act - a notification recipient, say - append " +
		"`lint:allow: <reason>` to this line and say why.\n    > " + text
}

// blankComments replaces comment content with spaces, preserving byte offsets so a
// match position in the cleaned text still maps to the right line of the original.
// Scanning line by line was the first attempt and it was WRONG in a way that mattered:
// it also mis-reports the original line, because stripping by index shifts nothing but
// blind matching does not know which line it came from.
func blankComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			lines[i] = strings.Repeat(" ", len(line))
			continue
		}
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx] + strings.Repeat(" ", len(line)-idx)
		}
	}
	return strings.Join(lines, "\n")
}
