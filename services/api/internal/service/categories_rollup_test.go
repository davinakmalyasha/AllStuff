package service_test

import (
	"context"
	"testing"

	"bizverse/api/internal/testutil"
)

// Category counts used to be measured per category in isolation, which made every
// parent category report zero - not occasionally, but structurally. Businesses
// are only ever assigned to leaf categories ("Café", never "Food & Dining"), so a
// parent's own count was pinned at 0 by the shape of the data rather than by
// anything about the business.
//
// The visible symptom was the landing page rendering all six group tiles as
// "0 businesses" directly underneath its own "2 Verified businesses" banner, and
// every group tile being a dead end that searched for the literal text
// "Food & Dining" - a string no business carries.
//
// These tests pin the rolled-up behaviour at three levels, because a fix that only
// handles two would still be wrong the day a third is introduced.
func TestCategoryCountsRollUpOverTheSubtree(t *testing.T) {
	ctx := context.Background()
	h := testutil.New(t)

	parent := testutil.Category(t, h, "Parent "+t.Name())
	child := testutil.ChildCategory(t, h, parent, "Child "+t.Name())
	grandchild := testutil.ChildCategory(t, h, child, "Grandchild "+t.Name())

	// Both businesses hang off the GRANDCHILD, three levels below the parent.
	// Nothing is assigned to the parent or the child directly.
	for range 2 {
		testutil.Business(t, h, testutil.BusinessOpts{Category: grandchild})
	}

	counts := map[string]int{}
	flat, err := h.Repos.Categories.ListWithCounts(ctx)
	if err != nil {
		t.Fatalf("ListWithCounts: %v", err)
	}
	for _, c := range flat {
		counts[c.Name] = c.Count
	}

	// Every ancestor of the grandchild must report the same total. A per-category
	// count gives 0/0/2 here and satisfies nothing a user would call correct.
	for _, name := range []string{parent.Name, child.Name, grandchild.Name} {
		if got := counts[name]; got != 2 {
			t.Errorf("count for %q = %d, want 2 (its whole subtree)", name, got)
		}
	}
}

func TestCategoryCountExcludesUnverifiedAndDeleted(t *testing.T) {
	ctx := context.Background()
	h := testutil.New(t)

	cat := testutil.Category(t, h, "Counted "+t.Name())

	testutil.Business(t, h, testutil.BusinessOpts{Category: cat})
	// A draft in the same category must not be counted: the tile says "verified
	// businesses", so counting a draft is a straight lie to the user.
	testutil.Business(t, h, testutil.BusinessOpts{Category: cat, Status: "draft"})

	deleted := testutil.Business(t, h, testutil.BusinessOpts{Category: cat})
	// h.Exec, NOT h.Pool.Exec. Repos is bound to an open transaction that only
	// ROLLBACKs at cleanup, so the fixture rows are uncommitted and invisible to
	// the separate Pool connection - an UPDATE sent there affects nothing and
	// still exits 0, which reads as a successful soft delete that never happened.
	h.Exec(t, `UPDATE businesses SET deleted_at = now() WHERE id = $1`, deleted.ID)

	flat, err := h.Repos.Categories.ListWithCounts(ctx)
	if err != nil {
		t.Fatalf("ListWithCounts: %v", err)
	}
	for _, c := range flat {
		if c.Name == cat.Name {
			if c.Count != 1 {
				t.Errorf("count = %d, want 1 - drafts and soft-deleted rows must be excluded", c.Count)
			}
			return
		}
	}
	t.Fatalf("category %q missing from ListWithCounts", cat.Name)
}

// A category with no businesses must report exactly 0, not NULL. The tile renders
// `${count} business${count === 1 ? ” : 'es'}`, so a NULL would render as
// "null businesses" in the UI instead of "0 businesses".
func TestEmptyCategoryCountsZeroNotNull(t *testing.T) {
	ctx := context.Background()
	h := testutil.New(t)

	cat := testutil.Category(t, h, "Empty "+t.Name())

	flat, err := h.Repos.Categories.ListWithCounts(ctx)
	if err != nil {
		t.Fatalf("ListWithCounts: %v", err)
	}
	for _, c := range flat {
		if c.Name == cat.Name {
			if c.Count != 0 {
				t.Errorf("count = %d, want 0 for a category with no businesses", c.Count)
			}
			return
		}
	}
	t.Fatalf("category %q missing from ListWithCounts", cat.Name)
}
