//go:build !race

package service

import (
	"context"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Cities.All backs the sitemap and the /cities index, so it must agree with
// every other public reader about what a visible city is.

func TestCitiesAllCountsOnlyPublicListings(t *testing.T) {
	h := testutil.New(t)
	svc := NewCities(h.Repos)
	ctx := context.Background()
	cat := testutil.Category(t, h, "Cafe")
	owner := testutil.User(t, h)

	mk := func(name, city string, status domain.BusinessStatus) *domain.Business {
		return testutil.Business(t, h, testutil.BusinessOpts{
			Name: name, Category: cat, Owner: owner, Status: status,
			City: city, Country: "ID", Lat: -6.2, Lng: 106.8,
		})
	}
	mk("Bandung One", "Bandung", domain.BusinessVerified)
	mk("Bandung Two", "Bandung", domain.BusinessVerified)
	mk("Jakarta One", "Jakarta", domain.BusinessVerified)
	// Not public: draft, retired, and a blank city.
	mk("Draft", "Bandung", domain.BusinessDraft)
	retired := mk("Retired", "Bandung", domain.BusinessVerified)
	h.Exec(t, `UPDATE businesses SET deleted_at=now() WHERE id=$1`, retired.ID)
	// The fixture defaults an empty city to Jakarta, so the blank-city case is
	// inserted directly. The query excludes those: a listing with no city has
	// nothing to count.
	h.Exec(t, `INSERT INTO businesses (id, owner_id, name, slug, description, category_id, status, address, lat, lng, city, country)
		VALUES ($1,$2,'Nowhere','nowhere-listing',
			'A listing that has no city at all, which the cities index must skip.',
			$3,'verified','Somewhere',-6.2,106.8,'','ID')`, util.NewUUID(), owner.ID, cat.ID)

	got, err := svc.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("cities = %d, want 2: %v", len(got), got)
	}
	// Ordered by count descending, so the busier city leads.
	if got[0]["name"] != "Bandung" {
		t.Errorf("first city = %v, want Bandung", got[0]["name"])
	}
	if got[0]["count"] != 2 {
		t.Errorf("Bandung count = %v, want 2 (draft and retired must not count)", got[0]["count"])
	}
	if got[0]["slug"] != "bandung" {
		t.Errorf("slug = %v, want bandung", got[0]["slug"])
	}
	if got[1]["name"] != "Jakarta" || got[1]["count"] != 1 {
		t.Errorf("second city = %v, want Jakarta x1", got[1])
	}
}
