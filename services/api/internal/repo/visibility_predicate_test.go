package repo

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// TestPubliclyVisibleBusinessMatchesTheServiceWhitelist keeps the SQL predicate
// and the Go switch in agreement.
//
// repo.PubliclyVisibleBusiness and the switch in service.Businesses.GetPublic
// express the same rule - which listings the public may see - and they live in
// different layers so they cannot share code or a compiler check. They drifted
// before this test existed: the SQL side did not exist, so ListReviews,
// ListComments, the Q&A readers and every leaderboard served rows the Go side
// considered hidden. A hidden review stayed fully readable AND kept moving the
// star average on every listing card.
//
// The reference is transcribed here rather than imported on purpose. This test
// states the CONTRACT. If someone changes the predicate, this fails and names
// the statuses to reconcile; if they change GetPublic, the failure points here.
func TestPubliclyVisibleBusinessMatchesTheServiceWhitelist(t *testing.T) {
	// The statuses the SQL predicate admits, read out of the constant rather than
	// restated, so this test cannot pass because both copies drifted together.
	open := statusesFromPredicate(t)

	want := map[string]bool{
		"verified": true, // live and trustworthy
		"paused":   true, // temporarily closed, page stays reachable
		// Everything below must be REFUSED:
		"draft":          false, // never published
		"pending_review": false, // in the verification queue
		"rejected":       false, // failed verification
		"suspended":      false, // under moderation
		"closed":         false, // permanently retired
	}
	for status, expected := range want {
		if got := open[status]; got != expected {
			t.Errorf("status %q: predicate admits it = %v, want %v", status, got, expected)
		}
	}

	// The admitted set must be EXACTLY the two intended statuses.
	//
	// Checking each contract status individually is not enough on its own: it
	// would still pass if the predicate also admitted something nobody thought
	// to list. So assert the whole set, which catches an extra status (someone
	// adding `suspended`, or a wildcard) as well as a missing one.
	admitted := make([]string, 0, len(open))
	for status, yes := range open {
		if yes {
			admitted = append(admitted, status)
		}
	}
	sort.Strings(admitted)
	if len(admitted) != 2 || admitted[0] != "paused" || admitted[1] != "verified" {
		t.Errorf("the predicate admits %v; it must admit exactly [paused verified]", admitted)
	}
}

// statusesFromPredicate extracts the admitted statuses from the constant's
// source text. It reads the constant's own definition, so a change to the
// predicate shows up here as a changed set rather than as a stale duplicate.
func statusesFromPredicate(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile("businesses.go")
	if err != nil {
		t.Fatalf("read businesses.go: %v", err)
	}
	const marker = "const PubliclyVisibleBusiness = `"
	i := strings.Index(string(src), marker)
	if i < 0 {
		t.Fatal("PubliclyVisibleBusiness is not declared with a single-line raw string; " +
			"this test parses that exact form and needs updating if the shape changed")
	}
	rest := string(src)[i+len(marker):]
	j := strings.Index(rest, "`")
	if j < 0 {
		t.Fatal("PubliclyVisibleBusiness raw string is unterminated")
	}
	body := rest[:j]

	const inClause = "b.status IN ("
	k := strings.Index(body, inClause)
	if k < 0 {
		t.Fatalf("the predicate does not filter on b.status IN (...), so it does not enumerate "+
			"statuses at all: %q", body)
	}
	list := body[k+len(inClause):]
	list = list[:strings.Index(list, ")")]

	out := map[string]bool{}
	for _, s := range strings.Split(list, ",") {
		s = strings.Trim(strings.TrimSpace(s), "'")
		if s != "" {
			out[s] = true
		}
	}
	return out
}
