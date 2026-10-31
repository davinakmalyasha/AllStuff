//go:build !race

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"allstuff/api/internal/config"
	"allstuff/api/internal/domain"
	"allstuff/api/internal/email"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// invites.Create's seat cap and trending.Leaderboard were the last uncovered
// service functions. The cap is a plan boundary, and the leaderboard is the
// public page the trending job exists to feed.

func TestInvitesCreateEnforcesSeatCapacity(t *testing.T) {
	h := testutil.New(t)
	cfg := config.Config{AppEnv: "test", PublicURL: "https://allstuff.test"}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test"})
	ctx := context.Background()

	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Seat Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})

	// The OWNER occupies a seat, so a one-seat plan has nothing left for an
	// invitation. That is the correct reading of "team_seats", and the reason
	// this test starts by proving the cap is not off by one.
	one := NewInvites(h.Repos, cfg, sender, stubEntitlements{e: &Entitlements{TeamSeats: 1}})
	if err := one.Create(ctx, owner.ID, biz.ID, "first@example.test", "co_owner"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("invite on a one-seat plan: err = %v, want ErrValidation", err)
	}

	// Two seats: the owner plus one invitation.
	two := NewInvites(h.Repos, cfg, sender, stubEntitlements{e: &Entitlements{TeamSeats: 2}})
	if err := two.Create(ctx, owner.ID, biz.ID, "first@example.test", "co_owner"); err != nil {
		t.Fatalf("first invite with two seats: %v", err)
	}
	if err := two.Create(ctx, owner.ID, biz.ID, "second@example.test", "co_owner"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("second invite with two seats: err = %v, want ErrValidation", err)
	}
	// Accepting the outstanding invitation fills the second seat, and the cap
	// still holds.
	invitee := testutil.User(t, h)
	h.Exec(t, `UPDATE users SET email='first@example.test' WHERE id=$1`, invitee.ID)
	var token string
	if err := h.QueryRow(t, `SELECT token FROM business_invites WHERE email='first@example.test'`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if err := two.Accept(ctx, invitee.ID, token); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := two.Create(ctx, owner.ID, biz.ID, "third@example.test", "co_owner"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("invite over an accepted seat: err = %v, want ErrValidation", err)
	}

	// A plan with more seats allows more invitations.
	five := NewInvites(h.Repos, cfg, sender, stubEntitlements{e: &Entitlements{TeamSeats: 5}})
	if err := five.Create(ctx, owner.ID, biz.ID, "fourth@example.test", "viewer"); err != nil {
		t.Errorf("an invite within the allowance was refused: %v", err)
	}
	// A revoked or expired invitation frees its seat: the reservation is of live
	// invitations only.
	mkBiz := func(name string) *domain.Business {
		return testutil.Business(t, h, testutil.BusinessOpts{
			Name: name, Category: cat, Owner: owner,
			City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		})
	}
	two2 := NewInvites(h.Repos, cfg, sender, stubEntitlements{e: &Entitlements{TeamSeats: 2}})

	revokeBiz := mkBiz("Revoke Seat Cafe")
	if err := two2.Create(ctx, owner.ID, revokeBiz.ID, "a@example.test", "co_owner"); err != nil {
		t.Fatalf("create before revoke: %v", err)
	}
	h.Exec(t, `UPDATE business_invites SET revoked_at=now() WHERE business_id=$1`, revokeBiz.ID)
	if err := two2.Create(ctx, owner.ID, revokeBiz.ID, "b@example.test", "co_owner"); err != nil {
		t.Errorf("a revoked invitation still held its seat: %v", err)
	}

	expireBiz := mkBiz("Expire Seat Cafe")
	if err := two2.Create(ctx, owner.ID, expireBiz.ID, "c@example.test", "co_owner"); err != nil {
		t.Fatalf("create before expiry: %v", err)
	}
	h.Exec(t, `UPDATE business_invites SET expires_at=now() - interval '1 minute' WHERE business_id=$1`, expireBiz.ID)
	if err := two2.Create(ctx, owner.ID, expireBiz.ID, "d@example.test", "co_owner"); err != nil {
		t.Errorf("an expired invitation still held its seat: %v", err)
	}

	// A zero allowance means unlimited, not zero.
	unlimited := NewInvites(h.Repos, cfg, sender, stubEntitlements{e: &Entitlements{TeamSeats: 0}})
	if err := unlimited.Create(ctx, owner.ID, biz.ID, "fifth@example.test", "co_owner"); err != nil {
		t.Errorf("a zero seat allowance was treated as a cap: %v", err)
	}
}

func TestTrendingLeaderboard(t *testing.T) {
	svc, h := newTestTrending(t)
	ctx := context.Background()

	// No snapshots at all: empty, not an error.
	got, err := svc.Leaderboard(ctx, "24h", "", 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty leaderboard: %d entries, err = %v", len(got), err)
	}

	cat := testutil.Category(t, h, "Cafe")
	otherCat := testutil.Category(t, h, "Bookshop")
	owner := testutil.User(t, h)
	mk := func(name, city string, c *domain.Category) *domain.Business {
		return testutil.Business(t, h, testutil.BusinessOpts{
			Name: name, Category: c, Owner: owner, Status: domain.BusinessVerified,
			City: city, Country: "ID", Lat: -6.2, Lng: 106.8,
		})
	}
	hot := mk("Hot Cafe", "Jakarta", cat)
	boom := mk("Boom Cafe", "Bandung", cat)
	rise := mk("Rise Cafe", "Jakarta", cat)
	book := mk("Book Shop", "Jakarta", otherCat)

	// The 24h batch is the one the page reads. Two rows at different instants
	// prove it uses the LATEST, not an arbitrary one.
	old := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	now := time.Now().UTC().Truncate(time.Second)
	snap := func(b *domain.Business, score, velocity float64, booming, rising bool, at time.Time) {
		h.Exec(t, `INSERT INTO trend_snapshots (id, period, business_id, score, velocity, is_booming, is_rising, taken_at)
			VALUES ($1,'24h',$2,$3,$4,$5,$6,$7)`,
			util.NewUUID(), b.ID, score, velocity, booming, rising, at)
	}
	snap(hot, 10, 1, false, false, old) // superseded
	snap(hot, 50, 1, false, false, now)
	snap(boom, 40, 9, true, false, now)
	snap(rise, 30, 7, false, true, now)
	snap(book, 20, 1, false, false, now)
	// A 7d snapshot must not leak into the 24h board.
	h.Exec(t, `INSERT INTO trend_snapshots (id, period, business_id, score, taken_at)
		VALUES ($1,'7d',$2,999,now())`, util.NewUUID(), book.ID)

	// Default scope: newest batch, score descending.
	got, err = svc.Leaderboard(ctx, "24h", "", 10)
	if err != nil {
		t.Fatalf("Leaderboard: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("entries = %d, want the four 24h listings", len(got))
	}
	if got[0].ID != hot.ID || got[0].Score != 50 {
		t.Errorf("first = %s (score %v), want the latest hot snapshot", got[0].Name, got[0].Score)
	}

	// Scope filters.
	names := func(scope string) []string {
		entries, err := svc.Leaderboard(ctx, "24h", scope, 10)
		if err != nil {
			t.Fatalf("%s: %v", scope, err)
		}
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, e.Name)
		}
		return out
	}
	if got := names("booming"); len(got) != 1 || got[0] != "Boom Cafe" {
		t.Errorf("booming = %v, want only Boom Cafe", got)
	}
	if got := names("rising"); len(got) != 1 || got[0] != "Rise Cafe" {
		t.Errorf("rising = %v, want only Rise Cafe", got)
	}
	if got := names("category:" + otherCat.ID); len(got) != 1 || got[0] != "Book Shop" {
		t.Errorf("category scope = %v, want only Book Shop", got)
	}
	if got := names("city:Bandung"); len(got) != 1 || got[0] != "Boom Cafe" {
		t.Errorf("city scope = %v, want only the Bandung listing", got)
	}
	if got := names("city:Jakarta"); len(got) != 3 {
		t.Errorf("Jakarta = %v, want the three Jakarta listings", got)
	}
}
