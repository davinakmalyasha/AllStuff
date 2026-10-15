package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every API route must be documented. This is the check behind README's claim that
// docs/API.md is "the endpoint reference", and until this test existed nothing backed
// that claim - it was true only as long as nobody forgot.
//
// Two routes were already undocumented when it was written: the four /admin/categories
// endpoints (create, update, delete, merge) and GET /auth/csrf, the cookie bootstrap a
// signed-out visitor's first mutation depends on. Neither was reported by anything.
//
// # WHY THIS IS COARSE ON PURPOSE
//
// It checks the first TWO path segments, not the whole path. A stricter check was tried
// and produced 33 findings of which 2 were real: API.md writes `{id}` where the router
// writes `{productId}`, and it writes endpoint families as `POST /threads/{id}/read|
// typing|close|leave`, which is a notation no path comparison can read without a parser
// for it.
//
// A gate that reports 31 false positives per run gets switched off, which is worse than
// no gate - so this trades precision for a signal that is always worth acting on. It
// catches "a whole area of the API is missing from the reference", which is the drift
// that actually happened twice, and it will NOT catch a single endpoint omitted from a
// documented family. That limitation is stated rather than implied.
//
// Brace parameter names are normalised on both sides before comparing, because the
// router's `{productId}` and the document's `{id}` mean the same thing and neither name
// is part of the contract.
//
// Scoped to /api/ routes: docs/API.md opens with "Base URL: /api/v1" and covers those.
// Non-API surfaces (/metrics, the /ssr/* SEO shell) have their own sections and are a
// different question.
func TestEveryAPIRouteIsDocumented(t *testing.T) {
	root := repoRoot(t)

	doc, err := os.ReadFile(filepath.Join(root, "docs", "API.md"))
	if err != nil {
		t.Fatalf("docs/API.md: %v", err)
	}

	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	routeRe := regexp.MustCompile(`mux\.HandleFunc\("(?:GET|POST|PUT|PATCH|DELETE) (/api/[^"]+)"`)

	normalisedDoc := normaliseParams(string(doc))

	type route struct {
		path   string
		prefix string
	}
	var routes []route
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, m := range routeRe.FindAllSubmatch(src, -1) {
			p := strings.Replace(string(m[1]), "/api/v1", "", 1)
			np := normaliseParams(p)
			segs := strings.Split(strings.Trim(np, "/"), "/")
			if len(segs) == 0 {
				continue
			}
			prefix := "/" + strings.Join(segs[:min(2, len(segs))], "/")
			routes = append(routes, route{path: p, prefix: prefix})
		}
	}
	if len(routes) == 0 {
		t.Fatal("found no routes; the registration pattern has changed and this test is now checking nothing")
	}

	missing := map[string]string{}
	for _, r := range routes {
		if !strings.Contains(normalisedDoc, r.prefix) {
			missing[r.prefix] = r.path
		}
	}
	for prefix, example := range missing {
		t.Errorf("docs/API.md does not mention %s (registered as %s). "+
			"Add it to the reference, or this API surface is undocumented.",
			prefix, example)
	}
}

// normaliseParams rewrites every `{...}` path parameter to `{}`.
//
// The router names parameters for its handlers and the document names them for a reader;
// `{productId}` and `{id}` are the same contract. Comparing the names would report a
// route as undocumented for the crime of being described with a shorter word.
func normaliseParams(s string) string {
	return regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(s, "{}")
}
