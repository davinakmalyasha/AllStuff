//go:build !race

package service

import (
	"context"
	"errors"
	"testing"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/email"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// invites.Accept had no coverage. It is the point where an emailed token becomes
// access to someone else's business, so every refusal in it matters.

func newTestInvites(t *testing.T) (*Invites, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	cfg := config.Config{AppEnv: "test", PublicURL: "https://bizverse.test"}
	sender := email.NewSender(email.ResendConfig{AppEnv: "test", PublicURL: cfg.PublicURL})
	// A nil EntitlementSource skips seat accounting, which is Create's concern.
	return NewInvites(h.Repos, cfg, sender, nil), h
}

// inviteToken creates an invitation through the service and returns its token,
// so the test exercises the real writing path rather than a hand-built row.
func inviteToken(t *testing.T, svc *Invites, h *testutil.H, ownerID, businessID, emailAddr string) string {
	t.Helper()
	if err := svc.Create(context.Background(), ownerID, businessID, emailAddr, "co_owner"); err != nil {
		t.Fatalf("Create invite: %v", err)
	}
	// created_at defaults to now(), which is the TRANSACTION timestamp, so two
	// invites written in one test share it and ORDER BY created_at is a tie. A
	// pending invite is identified by its state instead: not revoked, not used.
	var token string
	if err := h.QueryRow(t,
		`SELECT token FROM business_invites
		 WHERE business_id=$1 AND email=$2 AND revoked_at IS NULL AND accepted_at IS NULL
		 ORDER BY token DESC LIMIT 1`,
		businessID, emailAddr).Scan(&token); err != nil {
		t.Fatalf("read invite token: %v", err)
	}
	return token
}

func ownedBusiness(t *testing.T, h *testutil.H, name string) (*domain.Business, *domain.User) {
	t.Helper()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: name, Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		Status: domain.BusinessVerified,
	})
	return b, owner
}

func TestInvitesAcceptRefusesBadTokens(t *testing.T) {
	svc, h := newTestInvites(t)
	ctx := context.Background()
	biz, owner := ownedBusiness(t, h, "Invite Cafe")
	invitee := testutil.User(t, h)

	// Unknown token.
	if err := svc.Accept(ctx, invitee.ID, util.NewUUID()); !errors.Is(err, domain.ErrTokenInvalid) {
		t.Errorf("unknown token: err = %v, want ErrTokenInvalid", err)
	}

	// Revoked.
	revoked := inviteToken(t, svc, h, owner.ID, biz.ID, invitee.Email)
	h.Exec(t, `UPDATE business_invites SET revoked_at=now() WHERE token=$1`, revoked)
	if err := svc.Accept(ctx, invitee.ID, revoked); !errors.Is(err, domain.ErrTokenInvalid) {
		t.Errorf("revoked token: err = %v, want ErrTokenInvalid", err)
	}

	// Already used.
	used := inviteToken(t, svc, h, owner.ID, biz.ID, invitee.Email)
	if err := svc.Accept(ctx, invitee.ID, used); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if err := svc.Accept(ctx, invitee.ID, used); !errors.Is(err, domain.ErrTokenInvalid) {
		t.Errorf("reused token: err = %v, want ErrTokenInvalid", err)
	}

	// Expired.
	expired := inviteToken(t, svc, h, owner.ID, biz.ID, invitee.Email)
	h.Exec(t, `UPDATE business_invites SET expires_at=now() - interval '1 minute' WHERE token=$1`, expired)
	if err := svc.Accept(ctx, invitee.ID, expired); !errors.Is(err, domain.ErrTokenInvalid) {
		t.Errorf("expired token: err = %v, want ErrTokenInvalid", err)
	}
}

func TestInvitesAcceptRequiresTheAddressedUser(t *testing.T) {
	svc, h := newTestInvites(t)
	ctx := context.Background()
	biz, owner := ownedBusiness(t, h, "Addressed Cafe")
	invitee := testutil.User(t, h)
	someoneElse := testutil.User(t, h)

	token := inviteToken(t, svc, h, owner.ID, biz.ID, invitee.Email)

	// The token is a bearer credential in an email; holding it is not enough.
	if err := svc.Accept(ctx, someoneElse.ID, token); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a different account: err = %v, want ErrForbidden", err)
	}
	// The token is not consumed by the refusal.
	if err := svc.Accept(ctx, invitee.ID, token); err != nil {
		t.Errorf("the addressed user was refused after another account failed: %v", err)
	}
	// An unknown account cannot accept either.
	if err := svc.Accept(ctx, util.NewUUID(), inviteToken(t, svc, h, owner.ID, biz.ID, "ghost@example.test")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user: err = %v, want ErrNotFound", err)
	}
}

func TestInvitesAcceptRefusesUnavailableBusiness(t *testing.T) {
	svc, h := newTestInvites(t)
	ctx := context.Background()

	suspendedBiz, owner := ownedBusiness(t, h, "Suspended Cafe")
	invitee := testutil.User(t, h)
	token := inviteToken(t, svc, h, owner.ID, suspendedBiz.ID, invitee.Email)
	h.Exec(t, `UPDATE businesses SET status='suspended' WHERE id=$1`, suspendedBiz.ID)
	if err := svc.Accept(ctx, invitee.ID, token); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("suspended business: err = %v, want ErrValidation", err)
	}

	// A deleted business is not resolvable at all, so the invitation is refused
	// rather than silently granting access to a listing nobody can see.
	deletedBiz, owner2 := ownedBusiness(t, h, "Deleted Cafe")
	invitee2 := testutil.User(t, h)
	token2 := inviteToken(t, svc, h, owner2.ID, deletedBiz.ID, invitee2.Email)
	h.Exec(t, `UPDATE businesses SET deleted_at=now() WHERE id=$1`, deletedBiz.ID)
	if err := svc.Accept(ctx, invitee2.ID, token2); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("deleted business: err = %v, want ErrValidation", err)
	}
}

func TestInvitesAcceptGrantsAccess(t *testing.T) {
	svc, h := newTestInvites(t)
	ctx := context.Background()
	biz, owner := ownedBusiness(t, h, "Grant Cafe")
	invitee := testutil.User(t, h)

	if can, _ := h.Repos.Businesses.CanManageBusiness(ctx, invitee.ID, biz.ID); can {
		t.Fatal("the invitee already manages the business")
	}

	token := inviteToken(t, svc, h, owner.ID, biz.ID, invitee.Email)
	if err := svc.Accept(ctx, invitee.ID, token); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if can, err := h.Repos.Businesses.CanManageBusiness(ctx, invitee.ID, biz.ID); err != nil || !can {
		t.Errorf("after accepting, CanManageBusiness = %v, %v; want true", can, err)
	}
	var acceptedUser *string
	if err := h.QueryRow(t, `SELECT accepted_user_id::text FROM business_invites WHERE token=$1`, token).Scan(&acceptedUser); err != nil {
		t.Fatal(err)
	}
	if acceptedUser == nil || *acceptedUser != invitee.ID {
		t.Errorf("accepted_user_id = %v, want the accepting account %s", acceptedUser, invitee.ID)
	}

	// The grant is bound to the ACCOUNT, not to the address: changing your email
	// afterwards must not cost you the access you already accepted.
	h.Exec(t, `UPDATE users SET email='changed-address@example.test' WHERE id=$1`, invitee.ID)
	if can, err := h.Repos.Businesses.CanManageBusiness(ctx, invitee.ID, biz.ID); err != nil || !can {
		t.Errorf("after changing the email, CanManageBusiness = %v, %v; want true", can, err)
	}
	// And the old address no longer identifies the grant.
	if can, _ := h.Repos.Businesses.CanManageBusiness(ctx, util.NewUUID(), biz.ID); can {
		t.Error("an unrelated account can manage the business")
	}
}

func TestInvitesCreateRefusesNonManagers(t *testing.T) {
	svc, h := newTestInvites(t)
	ctx := context.Background()
	biz, _ := ownedBusiness(t, h, "Create Gate Cafe")
	stranger := testutil.User(t, h)

	if err := svc.Create(ctx, stranger.ID, biz.ID, "friend@example.test", "co_owner"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a stranger: err = %v, want ErrForbidden", err)
	}
	if err := svc.Create(ctx, stranger.ID, biz.ID, "not-an-email", "co_owner"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a stranger with a bad address: err = %v, want ErrForbidden", err)
	}
}
