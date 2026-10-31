package service

import (
	"testing"

	"allstuff/api/internal/domain"
)

func comment(id string, parent *string) *domain.Comment {
	return &domain.Comment{ID: id, ParentID: parent}
}

func ptr(s string) *string { return &s }

// buildCommentTree assembles the flat comment list returned by the repository
// into the nested shape the API emits, and it runs in two passes so that a
// reply appearing before its parent in the list still attaches correctly. It had
// no coverage at all in the last measured profile.
func TestBuildCommentTree(t *testing.T) {
	t.Run("empty in, empty out", func(t *testing.T) {
		if got := buildCommentTree(nil); len(got) != 0 {
			t.Errorf("got %d roots, want 0", len(got))
		}
	})

	t.Run("a lone comment is a root", func(t *testing.T) {
		got := buildCommentTree([]*domain.Comment{comment("a", nil)})
		if len(got) != 1 || got[0].ID != "a" {
			t.Fatalf("got %d roots, want just a", len(got))
		}
		if got[0].Children == nil {
			t.Error("Children must be initialised, not left nil; a reply appended later depends on it")
		}
		if len(got[0].Children) != 0 {
			t.Errorf("a root with no replies has %d children, want 0", len(got[0].Children))
		}
	})

	t.Run("a reply attaches to its parent and is not a root", func(t *testing.T) {
		got := buildCommentTree([]*domain.Comment{
			comment("parent", nil),
			comment("child", ptr("parent")),
		})
		if len(got) != 1 {
			t.Fatalf("got %d roots, want 1 (the reply must not also be a root)", len(got))
		}
		if len(got[0].Children) != 1 || got[0].Children[0].ID != "child" {
			t.Fatalf("reply not attached: %#v", got[0].Children)
		}
	})

	t.Run("a reply listed before its parent still attaches", func(t *testing.T) {
		// This is what the two-pass structure is for. Ordering by created_at
		// with a reply that shares a timestamp, or any ordering the repository
		// chooses, must not detach a reply from its parent.
		got := buildCommentTree([]*domain.Comment{
			comment("child", ptr("parent")),
			comment("parent", nil),
		})
		if len(got) != 1 || got[0].ID != "parent" {
			t.Fatalf("got %d roots (%v), want just the parent", len(got), ids(got))
		}
		if len(got[0].Children) != 1 || got[0].Children[0].ID != "child" {
			t.Fatalf("reply not attached when it precedes its parent: %#v", got[0].Children)
		}
	})

	t.Run("a reply whose parent is absent becomes a root", func(t *testing.T) {
		// The parent may have been deleted or hidden. Dropping the reply would
		// lose a comment that still exists, so it is surfaced instead.
		got := buildCommentTree([]*domain.Comment{comment("orphan", ptr("missing"))})
		if len(got) != 1 || got[0].ID != "orphan" {
			t.Fatalf("got %v, want the orphan surfaced as a root", ids(got))
		}
	})

	t.Run("nesting is deep and ordered", func(t *testing.T) {
		got := buildCommentTree([]*domain.Comment{
			comment("r", nil),
			comment("c1", ptr("r")),
			comment("c2", ptr("r")),
			comment("c1a", ptr("c1")),
			comment("c1b", ptr("c1")),
			comment("c1a1", ptr("c1a")),
			comment("r2", nil),
		})
		if len(got) != 2 || got[0].ID != "r" || got[1].ID != "r2" {
			t.Fatalf("roots = %v, want [r r2] in input order", ids(got))
		}
		r := got[0]
		if len(r.Children) != 2 || r.Children[0].ID != "c1" || r.Children[1].ID != "c2" {
			t.Fatalf("r children = %v, want [c1 c2]", ids(r.Children))
		}
		c1 := r.Children[0]
		if len(c1.Children) != 2 || c1.Children[0].ID != "c1a" || c1.Children[1].ID != "c1b" {
			t.Fatalf("c1 children = %v, want [c1a c1b]", ids(c1.Children))
		}
		if len(c1.Children[0].Children) != 1 || c1.Children[0].Children[0].ID != "c1a1" {
			t.Fatalf("c1a children = %v, want [c1a1]", ids(c1.Children[0].Children))
		}
	})

	t.Run("every comment appears exactly once", func(t *testing.T) {
		// A comment reachable twice would be rendered twice; one reachable
		// nowhere would vanish. Both are silent in the API response.
		in := []*domain.Comment{
			comment("r", nil), comment("a", ptr("r")), comment("b", ptr("a")),
			comment("orphan", ptr("gone")), comment("r2", nil),
		}
		seen := map[string]int{}
		var walk func([]*domain.Comment)
		walk = func(cs []*domain.Comment) {
			for _, c := range cs {
				seen[c.ID]++
				walk(c.Children)
			}
		}
		walk(buildCommentTree(in))

		if len(seen) != len(in) {
			t.Errorf("reached %d distinct comments, want %d (%v)", len(seen), len(in), seen)
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("%s reached %d times, want exactly 1", id, n)
			}
		}
	})
}

func ids(cs []*domain.Comment) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}
