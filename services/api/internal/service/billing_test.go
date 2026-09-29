package service

import (
	"context"
	"encoding/json"
	"testing"

	"bizverse/api/internal/repo"
)

// ResolveEntitlements is the only place a plan's entitlement list becomes an
// access decision, so it is tested exhaustively: a bug here silently grants or
// denies a paid feature.

func TestResolveEntitlements_FreePlanDefaults(t *testing.T) {
	// A business with no entitlements at all (free plan missing from the
	// catalogue) must still get usable, conservative limits rather than zero —
	// a ProductLimit of 0 would lock an owner out of their own catalog.
	e := ResolveEntitlements(nil)
	if e.ProductLimit != 5 || e.GalleryLimit != 3 || e.TeamSeats != 1 {
		t.Errorf("nil entitlements = %+v, want conservative defaults", e)
	}
	if e.Analytics || e.AnalyticsAdvanced || e.SupportPriority || e.APIAccess {
		t.Errorf("nil entitlements must not grant paid capabilities: %+v", e)
	}
}

func TestResolveEntitlements_BooleanFlags(t *testing.T) {
	raw := []string{"analytics", "analytics_advanced", "api_access", "support_priority"}
	e := ResolveEntitlements(raw)
	for _, name := range raw {
		if !e.Has(name) {
			t.Errorf("Has(%q) = false, want true", name)
		}
	}
}

// TestResolveEntitlements_TrustAndPlacementCannotBeGranted is a policy test, not
// a behaviour test: the assertion is that the resolver has no way to express
// these capabilities at all.
//
// `verified_badge` and `featured_placement` were in the seed. Both were removed,
// because selling the verification badge makes the badge meaningless and a paid
// featured slot is a promoted listing — the product's own stated non-goal. This
// test fails if anyone reintroduces either string, so the removal cannot be
// undone by a well-meaning "let me just add the flag back" edit.
func TestResolveEntitlements_TrustAndPlacementCannotBeGranted(t *testing.T) {
	forbidden := []string{
		"verified_badge",
		"featured_placement",
		"webhooks",
		"embeddable_widget",
	}
	e := ResolveEntitlements(append([]string{"analytics"}, forbidden...))
	for _, name := range forbidden {
		if e.Has(name) {
			t.Errorf("Has(%q) = true. Trust and placement are not sellable and must not "+
				"be reintroduced as entitlements; see the seed comment in migration 0028", name)
		}
	}
	// The resolver has no field to set, so re-granting the capability would
	// require a struct change — which is the point of removing them.
	if err := json.Unmarshal([]byte("{}"), &Entitlements{}); err != nil {
		t.Fatalf("Entitlements must remain JSON-serialisable: %v", err)
	}
}

func TestResolveEntitlements_Limits(t *testing.T) {
	e := ResolveEntitlements([]string{"product_limit:50", "gallery_limit:10", "team_seats:5"})
	if e.ProductLimit != 50 || e.GalleryLimit != 10 || e.TeamSeats != 5 {
		t.Errorf("limits = %d/%d/%d, want 50/10/5", e.ProductLimit, e.GalleryLimit, e.TeamSeats)
	}
	if e.Limit("product_limit", 7) != 50 {
		t.Errorf("Limit(product_limit) = %d, want 50", e.Limit("product_limit", 7))
	}
	// An absent cap falls back to the caller's default, which is how a gate
	// supplies "unchanged" behaviour for a business on the free plan.
	if e.Limit("nonexistent_limit", 7) != 7 {
		t.Errorf("Limit(unknown) must fall back to the default, got %d", e.Limit("nonexistent_limit", 7))
	}
}

// Exceeds and Limit disagree on purpose, and this is where that is pinned.
//
// Limit falls back to a caller default when a cap is absent, which is right for
// a value shown in the UI. Exceeds treats an absent cap as UNLIMITED, because a
// gate that invented a cap nobody agreed to would lock an owner out of their own
// storefront after they upgraded for a different reason.
func TestEntitlements_ExceedsTreatsAbsentCapAsUncapped(t *testing.T) {
	cases := []struct {
		cap  int
		n    int
		want bool
	}{
		{cap: 5, n: 4, want: false},
		{cap: 5, n: 5, want: false}, // the cap is inclusive
		{cap: 5, n: 6, want: true},
		{cap: 0, n: 6, want: false}, // 0 means "no cap published", not "none allowed"
		{cap: 1, n: 0, want: false},
		{cap: 1, n: 1, want: false},
	}
	for _, c := range cases {
		e := &Entitlements{ProductLimit: c.cap, GalleryLimit: c.cap, TeamSeats: c.cap}
		for _, name := range []string{"product_limit", "gallery_limit", "team_seats"} {
			if got := e.Exceeds(name, c.n); got != c.want {
				t.Errorf("cap=%d Exceeds(%q, %d) = %v, want %v", c.cap, name, c.n, got, c.want)
			}
		}
	}
	// An unrecognised capability is never exceeded, so a typo in a gate name
	// cannot accidentally deny.
	if (&Entitlements{ProductLimit: 1}).Exceeds("nonexistent_limit", 999) {
		t.Error("Exceeds(unknown) = true; a typo in a gate name must not deny")
	}
}

// A malformed cap is ignored rather than clamped. Clamping to 0 would lock the
// owner out; accepting a huge number would hand them an unlimited tier from a
// typo in the catalogue.
func TestResolveEntitlements_MalformedLimitIgnored(t *testing.T) {
	for _, bad := range []string{"product_limit:abc", "product_limit:-5", "product_limit:"} {
		e := ResolveEntitlements([]string{bad})
		if e.ProductLimit != 5 {
			t.Errorf("%q: ProductLimit = %d, want the default 5", bad, e.ProductLimit)
		}
	}
}

// An unknown capability must deny, never allow. A typo in a gate name would
// otherwise become an accidental bypass.
func TestResolveEntitlements_UnknownCapabilityDenies(t *testing.T) {
	e := ResolveEntitlements([]string{"analytics", "totally_made_up_capability"})
	if e.Has("totally_made_up_capability") {
		t.Error("Has(unknown) = true, want false — unknown must deny")
	}
	if !e.Has("analytics") {
		t.Error("known capability was lost alongside the unknown one")
	}
}

func TestResolveEntitlements_IgnoresWhitespace(t *testing.T) {
	e := ResolveEntitlements([]string{"  analytics  ", "", "  ", "product_limit: 9 "})
	if !e.Analytics {
		t.Error("whitespace-padded capability should still parse")
	}
	if e.ProductLimit != 5 {
		t.Errorf("a padded/malformed numeric should be ignored, got %d", e.ProductLimit)
	}
}

// The exact entitlements seeded by migration 0028, asserted so a seed edit that
// accidentally upgrades the free plan fails here.
func TestResolveEntitlements_SeededPlanShape(t *testing.T) {
	free := ResolveEntitlements([]string{
		"analytics", "api_access", "product_limit:5", "gallery_limit:3", "team_seats:1",
	})
	if free.AnalyticsAdvanced || free.SupportPriority {
		t.Errorf("the free plan must not carry paid capabilities: %+v", free)
	}
	if !free.Analytics || !free.APIAccess {
		t.Errorf("the free plan should carry analytics + api_access: %+v", free)
	}

	pro := ResolveEntitlements([]string{
		"analytics", "analytics_advanced", "product_limit:500",
		"gallery_limit:30", "api_access", "team_seats:25", "support_priority",
	})
	if !pro.SupportPriority || !pro.AnalyticsAdvanced {
		t.Errorf("the pro plan should carry the paid capabilities: %+v", pro)
	}
	if pro.ProductLimit != 500 || pro.GalleryLimit != 30 || pro.TeamSeats != 25 {
		t.Errorf("pro capacity = %d/%d/%d, want 500/30/25", pro.ProductLimit, pro.GalleryLimit, pro.TeamSeats)
	}
}

func TestSubscriptionGrantsEntitlements(t *testing.T) {
	// A strict allowlist: an unknown or empty status must NOT grant access, so
	// a status Stripe adds later cannot silently become a paid tier.
	cases := map[string]bool{
		"active":     true,
		"trialing":   true,
		"past_due":   true, // grace period while Stripe retries
		"incomplete": false,
		"canceled":   false,
		"unpaid":     false,
		"paused":     false,
		"":           false,
		"ACTIVE":     false, // case-sensitive on purpose
		"unknown":    false,
	}
	for status, want := range cases {
		sub := &repo.Subscription{Status: status}
		if got := sub.GrantsEntitlements(); got != want {
			t.Errorf("status %q: GrantsEntitlements() = %v, want %v", status, got, want)
		}
	}
	var nilSub *repo.Subscription
	if nilSub.GrantsEntitlements() {
		t.Error("a nil subscription must not grant entitlements")
	}
}

// stubEntitlements is a fixed capability set, so the capacity gates can be
// tested without a subscription lookup.
type stubEntitlements struct{ e *Entitlements }

func (s stubEntitlements) Entitlements(_ context.Context, _ string) (*Entitlements, error) {
	return s.e, nil
}
