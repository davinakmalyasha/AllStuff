//go:build !race

package service

import (
	"context"
	"errors"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
)

// cities.BySlug had no coverage - 48 statements - and it is the public city
// landing page, so what it counts and what it refuses are both user-visible.

func TestCitiesBySlug(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	cat := testutil.Category(t, h, "Cafe")
	owner := testutil.User(t, h)

	biz := func(name, city string, status domain.BusinessStatus) *domain.Business {
		return testutil.Business(t, h, testutil.BusinessOpts{
			Name: name, Category: cat, Owner: owner, Status: status,
			City: city, Country: "ID", Lat: -6.2, Lng: 106.8,
		})
	}

	biz("Jakarta One", "Jakarta", domain.BusinessVerified)
	biz("Jakarta Two", "Jakarta", domain.BusinessVerified)
	biz("Bandung One", "Bandung", domain.BusinessVerified)
	// Neither of these may be counted: the page is a public listing.
	biz("Jakarta Draft", "Jakarta", domain.BusinessDraft)
	gone := biz("Jakarta Deleted", "Jakarta", domain.BusinessVerified)
	h.Exec(t, `UPDATE businesses SET deleted_at = now() WHERE id = $1`, gone.ID)

	c := NewCities(h.Repos)

	t.Run("counts only verified, non-deleted businesses in the city", func(t *testing.T) {
		page, err := c.BySlug(ctx, "jakarta")
		if err != nil {
			t.Fatalf("BySlug: %v", err)
		}
		if page.Name != "Jakarta" {
			t.Errorf("Name = %q, want Jakarta", page.Name)
		}
		if page.Count != 2 {
			t.Errorf("Count = %d, want 2 (draft and deleted excluded)", page.Count)
		}
		if page.Slug != "jakarta" {
			t.Errorf("Slug = %q", page.Slug)
		}
		// The map centre is the average of the counted businesses' coordinates.
		if page.Lat == nil || page.Lng == nil || *page.Lat == 0 || *page.Lng == 0 {
			t.Errorf("centre = %v,%v; want the average of the city's businesses", page.Lat, page.Lng)
		}
	})

	t.Run("the slug is normalised", func(t *testing.T) {
		// Case and surrounding whitespace come from the URL and must not create a
		// 404 for a city that exists.
		for _, in := range []string{"Jakarta", "JAKARTA", "  jakarta  ", "\tJAKARTA\n"} {
			if _, err := c.BySlug(ctx, in); err != nil {
				t.Errorf("BySlug(%q): %v", in, err)
			}
		}
	})

	t.Run("categories are broken down by count", func(t *testing.T) {
		page, err := c.BySlug(ctx, "jakarta")
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Categories) == 0 {
			t.Fatal("no category breakdown")
		}
		if page.Categories[0].CategoryName != "Cafe" || page.Categories[0].Count != 2 {
			t.Errorf("first category = %+v, want Cafe with 2", page.Categories[0])
		}
	})

	t.Run("the top-rated list carries the city's businesses", func(t *testing.T) {
		page, err := c.BySlug(ctx, "jakarta")
		if err != nil {
			t.Fatal(err)
		}
		if len(page.TopRated) != 2 {
			t.Errorf("TopRated has %d businesses, want the 2 verified ones", len(page.TopRated))
		}
		for _, b := range page.TopRated {
			if b.City != "Jakarta" {
				t.Errorf("TopRated contains a business from %q", b.City)
			}
		}
	})

	t.Run("a city with no verified businesses is not found", func(t *testing.T) {
		// The cities list is built from verified rows only, so a name that exists
		// solely as a draft is not a city page yet.
		biz("Surabaya Draft", "Surabaya", domain.BusinessDraft)
		if _, err := c.BySlug(ctx, "surabaya"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("an unknown slug is not found", func(t *testing.T) {
		for _, in := range []string{"nowhere", "", "   ", "%20"} {
			if _, err := c.BySlug(ctx, in); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("BySlug(%q): err = %v, want ErrNotFound", in, err)
			}
		}
	})
}
