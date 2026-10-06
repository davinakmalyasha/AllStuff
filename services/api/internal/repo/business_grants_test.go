package repo_test

import (
	"context"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// A co-owner grant was resolved through an EMAIL ADDRESS.
//
// Both authorisation predicates joined business_invites back to users on
// u.email = i.email, and POST /api/v1/me/email lets a holder change theirs. So
// changing your address silently revoked every co-owner and viewer grant you held,
// while the owner still saw you listed as an accepted collaborator with the old one.
//
// Verified against a database before this test existed: a user with an accepted
// co_owner invite went from able to manage the listing to unable to, purely by
// updating users.email. Nothing deleted the grant row.
//
// Migration 0049 binds the grant at acceptance time.

// grant issues an accepted co_owner (or viewer) invite for user on bizID.
func grant(t *testing.T, h *testutil.H, ownerID, bizID string, role string, u *domain.User) {
	t.Helper()
	if err := h.Repos.Businesses.CreateInvite(context.Background(),
		ownerID, bizID, u.Email, role, util.NewUUID()); err != nil {
		t.Fatalf("create %s invite: %v", role, err)
	}
	invites, err := h.Repos.Businesses.ListInvites(context.Background(), bizID)
	if err != nil {
		t.Fatalf("ListInvites: %v", err)
	}
	for _, inv := range invites {
		if inv.Email == u.Email && inv.Role == role && inv.AcceptedAt == nil {
			if err := h.Repos.Businesses.AcceptInvite(context.Background(), inv.ID, u.ID); err != nil {
				t.Fatalf("accept %s invite: %v", role, err)
			}
			return
		}
	}
	t.Fatalf("no pending %s invite for %s", role, u.Email)
}

func canManage(t *testing.T, h *testutil.H, userID, bizID string) bool {
	t.Helper()
	can, err := h.Repos.Businesses.CanManageBusiness(context.Background(), userID, bizID)
	if err != nil {
		t.Fatalf("CanManageBusiness: %v", err)
	}
	return can
}

func isViewer(t *testing.T, h *testutil.H, userID, bizID string) bool {
	t.Helper()
	yes, err := h.Repos.Businesses.IsBusinessViewer(context.Background(), userID, bizID)
	if err != nil {
		t.Fatalf("IsBusinessViewer: %v", err)
	}
	return yes
}

// THE HEADLINE CASE
// ----------------
// A collaborator's access must survive them changing their own email address.
func TestCoOwnerKeepsAccessAfterChangingTheirEmail(t *testing.T) {
	h := testutil.New(t)

	owner := testutil.User(t, h)
	collab := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})

	grant(t, h, owner.ID, biz.ID, "co_owner", collab)

	if !canManage(t, h, collab.ID, biz.ID) {
		t.Fatal("the accepted co-owner cannot manage the listing before the email change")
	}

	h.Exec(t, `UPDATE users SET email = 'renamed-elsewhere@test.dev' WHERE id = $1`, collab.ID)

	if !canManage(t, h, collab.ID, biz.ID) {
		t.Error("changing their own email revoked the co-owner grant; the grant is keyed on the address, not the account")
	}
}

// The same for a read-only viewer invite, which resolves through the identical join.
func TestViewerKeepsAccessAfterChangingTheirEmail(t *testing.T) {
	h := testutil.New(t)
	owner := testutil.User(t, h)
	viewer := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})

	grant(t, h, owner.ID, biz.ID, "viewer", viewer)
	if !isViewer(t, h, viewer.ID, biz.ID) {
		t.Fatal("the accepted viewer has no dashboard access before the email change")
	}

	h.Exec(t, `UPDATE users SET email = 'renamed-elsewhere@test.dev' WHERE id = $1`, viewer.ID)

	if !isViewer(t, h, viewer.ID, biz.ID) {
		t.Error("changing their own email revoked the viewer grant")
	}
}

// An email must not confer access. This is the flip side of the bug: if a grant is
// keyed on an address, then whoever later acquires that address inherits it. With
// the user deleted and the address recycled, nothing should grant management.
func TestAnEmailAddressAloneConfersNothing(t *testing.T) {
	h := testutil.New(t)

	owner := testutil.User(t, h)
	collab := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})

	grant(t, h, owner.ID, biz.ID, "co_owner", collab)
	h.Exec(t, `DELETE FROM users WHERE id = $1`, collab.ID)

	// A different person now holds the address the invite was sent to.
	newcomer := testutil.User(t, h)
	h.Exec(t, `UPDATE users SET email = $1 WHERE id = $2`, collab.Email, newcomer.ID)

	if canManage(t, h, newcomer.ID, biz.ID) {
		t.Error("a user who merely holds the invited email address can manage the listing")
	}
}

// The bind must be atomic with the acceptance. A row accepted without a holder
// confers nothing while the owner sees it as accepted, so this asserts both halves.
func TestAcceptingAnInviteBindsTheHolder(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	owner := testutil.User(t, h)
	collab := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})

	if err := h.Repos.Businesses.CreateInvite(ctx, owner.ID, biz.ID, collab.Email, "co_owner", util.NewUUID()); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	invites, err := h.Repos.Businesses.ListInvites(ctx, biz.ID)
	if err != nil || len(invites) != 1 {
		t.Fatalf("ListInvites: err=%v n=%d", err, len(invites))
	}
	inv := invites[0]

	if inv.AcceptedUserID != nil {
		t.Fatal("a pending invite already has accepted_user_id; it is bound before acceptance")
	}

	if err := h.Repos.Businesses.AcceptInvite(ctx, inv.ID, collab.ID); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	invites, err = h.Repos.Businesses.ListInvites(ctx, biz.ID)
	if err != nil || len(invites) != 1 {
		t.Fatalf("ListInvites after accept: err=%v n=%d", err, len(invites))
	}
	got := invites[0]
	if got.AcceptedUserID == nil {
		t.Fatal("accepted_at is set but accepted_user_id is NULL; the grant would confer nothing")
	}
	if *got.AcceptedUserID != collab.ID {
		t.Errorf("accepted_user_id = %v, want %v", *got.AcceptedUserID, collab.ID)
	}
	// The invited ADDRESS is deliberately preserved as a record of what was sent.
	if got.Email != collab.Email {
		t.Errorf("email = %q, want the originally invited %q", got.Email, collab.Email)
	}
}

// Accepting twice must not overwrite the holder with whoever tried second.
func TestAnInviteCannotBeReAcceptedBySomeoneElse(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	owner := testutil.User(t, h)
	collab := testutil.User(t, h)
	other := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})

	if err := h.Repos.Businesses.CreateInvite(ctx, owner.ID, biz.ID, collab.Email, "co_owner", util.NewUUID()); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	invites, _ := h.Repos.Businesses.ListInvites(ctx, biz.ID)
	inv := invites[0]

	if err := h.Repos.Businesses.AcceptInvite(ctx, inv.ID, collab.ID); err != nil {
		t.Fatalf("first AcceptInvite: %v", err)
	}
	// The second call matches no rows (accepted_at IS NO LONGER NULL) and must not
	// rebind the grant.
	_ = h.Repos.Businesses.AcceptInvite(ctx, inv.ID, other.ID)

	after, _ := h.Repos.Businesses.ListInvites(ctx, biz.ID)
	if after[0].AcceptedUserID == nil || *after[0].AcceptedUserID != collab.ID {
		t.Errorf("holder = %v, want the first accepter %v", after[0].AcceptedUserID, collab.ID)
	}
	if canManage(t, h, other.ID, biz.ID) {
		t.Error("a second acceptance handed the grant to someone else")
	}
}

// Revoking still works, and revocation is what the owner actually reaches for.
func TestRevokedGrantStopsGrantingAccess(t *testing.T) {
	h := testutil.New(t)
	owner := testutil.User(t, h)
	collab := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})

	grant(t, h, owner.ID, biz.ID, "co_owner", collab)
	if !canManage(t, h, collab.ID, biz.ID) {
		t.Fatal("no access before revocation")
	}

	h.Exec(t, `UPDATE business_invites SET revoked_at = now() WHERE business_id = $1`, biz.ID)

	if canManage(t, h, collab.ID, biz.ID) {
		t.Error("a revoked co_owner grant still confers management access")
	}
}
