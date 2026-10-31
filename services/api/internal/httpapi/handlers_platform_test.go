//go:build !race

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// handlers_platform.go had no HTTP coverage: public profiles, support contact,
// appeals, co-owner invitations, user search, pinned messages, and the admin
// appeal/anomaly/KPI routes. Several of these sit on authorization boundaries
// (an invite token is access to someone else's business), so the wiring is what
// these tests are for.
//
// The pool harness is required throughout because chat sends and subscription
// fixtures must see the same connection as the handlers.

func platformJSON(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if w.Code < 200 || w.Code >= 300 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("not JSON: %v: %s", err, w.Body.String())
	}
}

func platformAnonymous(t *testing.T, hdl http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

// growthPlan puts a business on a plan with more than one team seat. Every
// business starts on free (team_seats = 1), which the owner already occupies,
// so invitation creation is refused until the fixture upgrades.
func growthPlan(t *testing.T, h *testutil.H, businessID string) {
	t.Helper()
	h.ExecPool(t, `INSERT INTO subscriptions
		(id, business_id, plan_id, price_cents, currency, stripe_subscription_id, status)
		VALUES ($1, $2, 'growth', 1900, 'USD', $3, 'active')`,
		util.NewUUID(), businessID, "sub_"+util.NewUUID())
}

func TestPlatformInvites(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	stranger := testutil.User(t, h)
	invitee := testutil.User(t, h)
	viewer := testutil.User(t, h)
	expired := testutil.User(t, h)

	cat := testutil.Category(t, h, "Invite Category")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Invite Bistro", Category: cat, Owner: owner,
		Status: domain.BusinessVerified, City: "Jakarta", Country: "ID",
	})
	otherBiz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Other Bistro", Category: cat, Owner: owner,
		Status: domain.BusinessVerified, City: "Jakarta", Country: "ID",
	})
	countInvites := func() int {
		t.Helper()
		var n int
		if err := h.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM business_invites WHERE business_id = $1`, biz.ID).Scan(&n); err != nil {
			t.Fatalf("count invites: %v", err)
		}
		return n
	}
	invitePath := "/api/v1/businesses/" + biz.ID + "/invites"

	// The free plan has one seat and the owner fills it. The cap is enforced at
	// send time so the owner sees the upgrade prompt while looking at the team
	// page, rather than the invitee seeing an error they cannot act on.
	w := dash(t, td, hdl, http.MethodPost, invitePath,
		map[string]any{"email": invitee.Email, "role": "co_owner"}, owner)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "team seat") {
		t.Fatalf("invite on the free plan: status = %d: %s", w.Code, w.Body.String())
	}
	if n := countInvites(); n != 0 {
		t.Fatalf("a refused invitation wrote %d row(s)", n)
	}

	growthPlan(t, h, biz.ID)

	// An unauthenticated visitor cannot even list the team.
	if w := platformAnonymous(t, hdl, http.MethodGet, invitePath); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous list: status = %d, want 401", w.Code)
	}
	// A stranger cannot read or write the team.
	if w := dash(t, td, hdl, http.MethodGet, invitePath, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger list: status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, invitePath,
		map[string]any{"email": "x@example.test", "role": "viewer"}, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger create: status = %d, want 403: %s", w.Code, w.Body.String())
	}

	// An unknown role must be rejected, not upgraded to co-owner. The old code
	// defaulted everything that was not exactly "viewer" to the highest role, so
	// a typo silently granted management access.
	for _, bad := range []string{"viewr", "admin", "owner"} {
		w := dash(t, td, hdl, http.MethodPost, invitePath,
			map[string]any{"email": invitee.Email, "role": bad}, owner)
		if w.Code != http.StatusBadRequest {
			t.Errorf("role %q: status = %d, want 400: %s", bad, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "co_owner or viewer") {
			t.Errorf("role %q: body does not name the valid roles: %s", bad, w.Body.String())
		}
	}
	if n := countInvites(); n != 0 {
		t.Fatalf("a rejected role wrote %d row(s)", n)
	}

	// An omitted role keeps the historical default: the dashboard's form always
	// sends one, and an older client that does not gets the same role it always
	// did.
	w = dash(t, td, hdl, http.MethodPost, invitePath,
		map[string]any{"email": invitee.Email}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("invite with the default role: status = %d: %s", w.Code, w.Body.String())
	}
	var defaultRole string
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT role FROM business_invites WHERE business_id=$1 AND email=$2`, biz.ID, invitee.Email).Scan(&defaultRole); err != nil {
		t.Fatalf("read default role: %v", err)
	}
	if defaultRole != "co_owner" {
		t.Errorf("default role = %q, want co_owner", defaultRole)
	}

	// A malformed business id is a client mistake, not a 500 from Postgres.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/businesses/not-a-uuid/invites", nil, owner); w.Code != http.StatusNotFound {
		t.Errorf("malformed business id: status = %d, want 404: %s", w.Code, w.Body.String())
	}

	// The list never carries the token: possession is the capability, and the
	// dashboard only needs the id/role/email.
	var listed struct {
		Invites []struct {
			ID         string     `json:"id"`
			Email      string     `json:"email"`
			Role       string     `json:"role"`
			AcceptedAt *time.Time `json:"accepted_at"`
			RevokedAt  *time.Time `json:"revoked_at"`
		} `json:"invites"`
	}
	w = dash(t, td, hdl, http.MethodGet, invitePath, nil, owner)
	platformJSON(t, w, &listed)
	if len(listed.Invites) != 1 {
		t.Fatalf("invites = %d, want 1", len(listed.Invites))
	}
	if strings.Contains(w.Body.String(), `"token"`) {
		t.Error("the invite list exposes the token")
	}
	inviteID := listed.Invites[0].ID

	tokenFor := func(t *testing.T, email string) string {
		t.Helper()
		var token string
		if err := h.Pool.QueryRow(context.Background(),
			`SELECT token FROM business_invites WHERE business_id=$1 AND email=$2`,
			biz.ID, strings.ToLower(email)).Scan(&token); err != nil {
			t.Fatalf("read token: %v", err)
		}
		return token
	}
	token := tokenFor(t, invitee.Email)

	// Preview is public, and it must not confirm a token after it is spent.
	var preview struct {
		BusinessName string `json:"business_name"`
		InviterName  string `json:"inviter_name"`
		Role         string `json:"role"`
		Email        string `json:"email"`
	}
	w = platformAnonymous(t, hdl, http.MethodGet, "/api/v1/invites/"+token)
	platformJSON(t, w, &preview)
	if preview.BusinessName != biz.Name || preview.Role != "co_owner" || preview.Email != invitee.Email {
		t.Errorf("preview = %+v", preview)
	}
	if preview.InviterName != owner.Name {
		t.Errorf("inviter = %q, want %q", preview.InviterName, owner.Name)
	}
	if w := platformAnonymous(t, hdl, http.MethodGet, "/api/v1/invites/unknown-token"); w.Code != http.StatusNotFound {
		t.Errorf("unknown preview: status = %d, want 404", w.Code)
	}

	// The invite is addressed to one account. Someone else holding the link must
	// not be able to accept it.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/invites/"+token+"/accept", nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("wrong account accept: status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/invites/"+token+"/accept", nil, invitee); w.Code != http.StatusOK {
		t.Fatalf("accept: status = %d: %s", w.Code, w.Body.String())
	}
	if can, err := h.Repos.Businesses.CanManageBusiness(context.Background(), invitee.ID, biz.ID); err != nil || !can {
		t.Fatalf("accepted co-owner cannot manage: can=%v err=%v", can, err)
	}
	// Single use.
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/invites/"+token+"/accept", nil, invitee); w.Code != http.StatusBadRequest {
		t.Errorf("second accept: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := platformAnonymous(t, hdl, http.MethodGet, "/api/v1/invites/"+token); w.Code != http.StatusNotFound {
		t.Errorf("preview after accept: status = %d, want 404", w.Code)
	}

	// Revoking an ACCEPTED invite cannot withdraw the grant (there is no member
	// removal endpoint). It used to answer 204 while the co-owner kept access;
	// the honest answer is that no revocable row matched.
	w = dash(t, td, hdl, http.MethodDelete, invitePath+"/"+inviteID, nil, owner)
	if w.Code != http.StatusNotFound {
		t.Errorf("revoke accepted: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if can, _ := h.Repos.Businesses.CanManageBusiness(context.Background(), invitee.ID, biz.ID); !can {
		t.Error("a 404 revoke still removed the co-owner's access")
	}

	// A pending viewer invitation can be revoked, and the token dies with it.
	w = dash(t, td, hdl, http.MethodPost, invitePath,
		map[string]any{"email": viewer.Email, "role": "viewer"}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("viewer invite: status = %d: %s", w.Code, w.Body.String())
	}
	viewerToken := tokenFor(t, viewer.Email)
	if w := platformAnonymous(t, hdl, http.MethodGet, "/api/v1/invites/"+viewerToken); w.Code != http.StatusOK {
		t.Fatalf("viewer preview: status = %d: %s", w.Code, w.Body.String())
	}
	var viewerID string
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT id FROM business_invites WHERE business_id=$1 AND email=$2`, biz.ID, viewer.Email).Scan(&viewerID); err != nil {
		t.Fatalf("viewer invite id: %v", err)
	}
	if w := dash(t, td, hdl, http.MethodDelete, invitePath+"/"+viewerID, nil, owner); w.Code != http.StatusNoContent {
		t.Fatalf("revoke pending: status = %d: %s", w.Code, w.Body.String())
	}
	if w := platformAnonymous(t, hdl, http.MethodGet, "/api/v1/invites/"+viewerToken); w.Code != http.StatusNotFound {
		t.Errorf("preview after revoke: status = %d, want 404", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/invites/"+viewerToken+"/accept", nil, viewer); w.Code != http.StatusBadRequest {
		t.Errorf("accept after revoke: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// An invitation that has run out is invisible and unusable.
	w = dash(t, td, hdl, http.MethodPost, invitePath,
		map[string]any{"email": expired.Email, "role": "viewer"}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("expiring invite: status = %d: %s", w.Code, w.Body.String())
	}
	expiredToken := tokenFor(t, expired.Email)
	h.ExecPool(t, `UPDATE business_invites SET expires_at = now() - interval '1 hour'
		WHERE business_id=$1 AND email=$2`, biz.ID, expired.Email)
	if w := platformAnonymous(t, hdl, http.MethodGet, "/api/v1/invites/"+expiredToken); w.Code != http.StatusNotFound {
		t.Errorf("expired preview: status = %d, want 404", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, "/api/v1/invites/"+expiredToken+"/accept", nil, expired); w.Code != http.StatusBadRequest {
		t.Errorf("expired accept: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// The business id in the path, not the invite's own id, scopes the revoke:
	// pointing business B at business A's invite must not touch it.
	if w := dash(t, td, hdl, http.MethodDelete,
		"/api/v1/businesses/"+otherBiz.ID+"/invites/"+inviteID, nil, owner); w.Code != http.StatusNotFound {
		t.Errorf("cross-business revoke: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodDelete, invitePath+"/not-a-uuid", nil, owner); w.Code != http.StatusNotFound {
		t.Errorf("malformed invite id: status = %d, want 404: %s", w.Code, w.Body.String())
	}
}

func TestPlatformProfileSupportAndAppeals(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	user := testutil.User(t, h)
	stranger := testutil.User(t, h)
	suspended := testutil.UserOpts_(t, h, testutil.UserOpts{Suspended: true})
	banned := testutil.UserOpts_(t, h, testutil.UserOpts{Banned: true})

	cat := testutil.Category(t, h, "Profile Category")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Profile Cafe", Category: cat, Owner: user,
		Status: domain.BusinessVerified, City: "Jakarta", Country: "ID",
	})

	// ---- public profile ----
	var profile struct {
		Username    string `json:"username"`
		Reviews     []any  `json:"reviews"`
		Comments    []any  `json:"comments"`
		Collections []any  `json:"collections"`
		Businesses  []struct {
			ID string `json:"id"`
		} `json:"businesses"`
	}
	w := platformAnonymous(t, hdl, http.MethodGet, "/api/v1/u/"+user.Username)
	platformJSON(t, w, &profile)
	if profile.Username != user.Username {
		t.Errorf("profile username = %q, want %q", profile.Username, user.Username)
	}
	for name, list := range map[string][]any{
		"reviews": profile.Reviews, "comments": profile.Comments, "collections": profile.Collections,
	} {
		if list == nil {
			t.Errorf("profile.%s is null, want an array", name)
		}
	}
	if len(profile.Businesses) != 1 || profile.Businesses[0].ID != biz.ID {
		t.Errorf("profile businesses = %+v", profile.Businesses)
	}
	if w := platformAnonymous(t, hdl, http.MethodGet, "/api/v1/u/does-not-exist"); w.Code != http.StatusNotFound {
		t.Errorf("unknown profile: status = %d, want 404", w.Code)
	}

	// ---- support contact ----
	contactPath := "/api/v1/support/contact"
	anonymous := httptest.NewRecorder()
	hdl.ServeHTTP(anonymous, authReq(t, http.MethodPost, contactPath,
		jsonBody(t, map[string]any{"subject": "Help", "message": "A long enough message."})))
	if anonymous.Code != http.StatusUnauthorized {
		t.Errorf("anonymous contact: status = %d, want 401: %s", anonymous.Code, anonymous.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, contactPath,
		map[string]any{"subject": "   ", "message": "A long enough message."}, user); w.Code != http.StatusBadRequest {
		t.Errorf("blank subject: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, contactPath,
		map[string]any{"subject": "Help", "message": "too short"}, user); w.Code != http.StatusBadRequest {
		t.Errorf("short message: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, contactPath,
		map[string]any{"subject": strings.Repeat("s", 121), "message": "A long enough message."}, user); w.Code != http.StatusBadRequest {
		t.Errorf("long subject: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	w = dash(t, td, hdl, http.MethodPost, contactPath,
		map[string]any{"subject": "  Refund  ", "message": "  I need help with my order.  "}, user)
	if w.Code != http.StatusCreated {
		t.Fatalf("contact: status = %d: %s", w.Code, w.Body.String())
	}
	var reason, targetType, reporter string
	if err := h.Pool.QueryRow(context.Background(), `
		SELECT reason, target_type, reporter_id::text FROM reports
		WHERE target_type = 'support' ORDER BY created_at DESC LIMIT 1`).
		Scan(&reason, &targetType, &reporter); err != nil {
		t.Fatalf("support report: %v", err)
	}
	if reason != "Refund\n\nI need help with my order." {
		t.Errorf("support reason = %q", reason)
	}
	if targetType != "support" || reporter != user.ID {
		t.Errorf("support report = type %q reporter %q", targetType, reporter)
	}

	// ---- appeals ----
	appealPath := "/api/v1/me/appeal"
	// A moderated account is still allowed to reach the two self-service
	// endpoints. Without the exemption the middleware dropped its identity and
	// answered 401 to the only users appeals exist for.
	if w := dash(t, td, hdl, http.MethodPost, appealPath,
		map[string]any{"reason": "short"}, suspended); w.Code != http.StatusBadRequest {
		t.Errorf("suspended short appeal: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	w = dash(t, td, hdl, http.MethodPost, appealPath,
		map[string]any{"reason": "I believe this suspension is a mistake."}, suspended)
	if w.Code != http.StatusCreated {
		t.Fatalf("suspended appeal: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost, appealPath,
		map[string]any{"reason": "A second appeal with different wording."}, suspended); w.Code != http.StatusBadRequest {
		t.Errorf("duplicate appeal: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	// The exemption is exactly two paths; ordinary access stays blocked.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me", nil, suspended); w.Code != http.StatusUnauthorized {
		t.Errorf("suspended /me: status = %d, want 401", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPost, contactPath,
		map[string]any{"subject": "Suspended", "message": "I need help with my account."}, suspended); w.Code != http.StatusCreated {
		t.Errorf("suspended contact: status = %d: %s", w.Code, w.Body.String())
	}

	if w := dash(t, td, hdl, http.MethodPost, appealPath,
		map[string]any{"reason": "This ban is a mistake and I can explain."}, banned); w.Code != http.StatusCreated {
		t.Fatalf("banned appeal: status = %d: %s", w.Code, w.Body.String())
	}

	// ---- admin appeals ----
	admin := testutil.Admin(t, h)
	h.ExecPool(t, `INSERT INTO user_2fa (id, user_id, totp_secret_encrypted, recovery_codes_hash, enabled_at)
		VALUES ($1,$2,'x','[]'::jsonb, now())`, util.NewUUID(), admin.ID)

	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/admin/appeals", nil, user); w.Code != http.StatusForbidden {
		t.Errorf("non-admin appeals: status = %d, want 403", w.Code)
	}
	type appealRow struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
		Status string `json:"status"`
	}
	var appealList struct {
		Appeals []appealRow `json:"appeals"`
	}
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/admin/appeals", nil, admin)
	platformJSON(t, w, &appealList)
	if len(appealList.Appeals) != 2 {
		t.Fatalf("open appeals = %d, want 2", len(appealList.Appeals))
	}
	byUser := map[string]string{}
	for _, a := range appealList.Appeals {
		byUser[a.UserID] = a.ID
	}
	if byUser[suspended.ID] == "" || byUser[banned.ID] == "" {
		t.Fatalf("appeals do not name their users: %+v", appealList.Appeals)
	}
	// A decision that is neither approve nor reject is a client error.
	if w := dash(t, td, hdl, http.MethodPost,
		"/api/v1/admin/appeals/"+byUser[suspended.ID]+"/decide",
		map[string]any{"decision": "maybe"}, admin); w.Code != http.StatusBadRequest {
		t.Errorf("invalid decision: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost,
		"/api/v1/admin/appeals/"+util.NewUUID()+"/decide",
		map[string]any{"decision": "approve"}, admin); w.Code != http.StatusNotFound {
		t.Errorf("unknown appeal: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost,
		"/api/v1/admin/appeals/"+byUser[suspended.ID]+"/decide",
		map[string]any{"decision": "approve"}, admin); w.Code != http.StatusOK {
		t.Fatalf("approve: status = %d: %s", w.Code, w.Body.String())
	}
	restored, err := h.Repos.Users.GetByID(context.Background(), suspended.ID)
	if err != nil || restored == nil || restored.Status != domain.UserStatusActive {
		t.Fatalf("approved user status = %+v (%v)", restored, err)
	}
	// The same access token now works because the account is active again.
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/me", nil, suspended); w.Code != http.StatusOK {
		t.Errorf("restored /me: status = %d: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPost,
		"/api/v1/admin/appeals/"+byUser[banned.ID]+"/decide",
		map[string]any{"decision": "reject"}, admin); w.Code != http.StatusOK {
		t.Fatalf("reject: status = %d: %s", w.Code, w.Body.String())
	}
	still, _ := h.Repos.Users.GetByID(context.Background(), banned.ID)
	if still == nil || still.Status != domain.UserStatusBanned {
		t.Errorf("a rejected appeal changed the ban: %+v", still)
	}

	// ---- KPIs and anomalies ----
	var kpis map[string]any
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/admin/kpis", nil, admin)
	platformJSON(t, w, &kpis)
	for _, key := range []string{"users", "businesses", "verified_businesses", "open_reports"} {
		if _, ok := kpis[key]; !ok {
			t.Errorf("KPIs have no %q: %v", key, kpis)
		}
	}
	if w := dash(t, td, hdl, http.MethodGet, "/api/v1/admin/kpis", nil, user); w.Code != http.StatusForbidden {
		t.Errorf("non-admin KPIs: status = %d, want 403", w.Code)
	}

	var eventID int64
	if err := h.Pool.QueryRow(context.Background(), `
		INSERT INTO engagement_events (user_id, target_type, target_id, signal, weight, dedupe_key, flagged)
		VALUES ($1, 'business', $2, 'view', 9, $3, true) RETURNING id`,
		stranger.ID, biz.ID, "anomaly-"+util.NewUUID()).Scan(&eventID); err != nil {
		t.Fatalf("seed anomaly: %v", err)
	}
	// engagement_events.id is a bigint, but Profiles.Anomalies scans it into a
	// string for the JSON map, so the wire value is quoted.
	var anomalies struct {
		Anomalies []struct {
			ID           string `json:"id"`
			BusinessName string `json:"business_name"`
			Signal       string `json:"signal"`
		} `json:"anomalies"`
	}
	w = dash(t, td, hdl, http.MethodGet, "/api/v1/admin/anomalies", nil, admin)
	platformJSON(t, w, &anomalies)
	found := false
	for _, a := range anomalies.Anomalies {
		if a.ID == util.Itoa(int(eventID)) {
			found = true
			if a.BusinessName != biz.Name || a.Signal != "view" {
				t.Errorf("anomaly row = %+v", a)
			}
		}
	}
	if !found {
		t.Fatalf("flagged event %d is not in the anomaly list", eventID)
	}
	if w := dash(t, td, hdl, http.MethodPost,
		"/api/v1/admin/anomalies/"+util.Itoa(int(eventID))+"/resolve", nil, admin); w.Code != http.StatusOK {
		t.Fatalf("resolve anomaly: status = %d: %s", w.Code, w.Body.String())
	}
	var flagged bool
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT flagged FROM engagement_events WHERE id = $1`, eventID).Scan(&flagged); err != nil {
		t.Fatalf("read anomaly: %v", err)
	}
	if flagged {
		t.Error("resolve left the event flagged")
	}
}

func TestPlatformUserSearch(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	searcher := testutil.User(t, h)

	// Underscore and percent are LIKE metacharacters. The handler escapes them,
	// so "like_marker" must not match "likeXmarker", and "100%" must not match
	// "100 cotton" - otherwise a search returns unrelated people.
	underscore := testutil.User(t, h)
	h.ExecPool(t, `UPDATE users SET username = 'like_marker', name = 'Alice Underscore' WHERE id = $1`, underscore.ID)
	lookalike := testutil.User(t, h)
	// lowercase: usernames are CHECKed to [a-z0-9_].
	h.ExecPool(t, `UPDATE users SET username = 'likexmarker', name = 'Bob Lookalike' WHERE id = $1`, lookalike.ID)
	percent := testutil.User(t, h)
	h.ExecPool(t, `UPDATE users SET username = 'percent_real', name = '100% cotton' WHERE id = $1`, percent.ID)
	plain := testutil.User(t, h)
	h.ExecPool(t, `UPDATE users SET username = 'percent_plain', name = '100 cotton' WHERE id = $1`, plain.ID)
	suspended := testutil.UserOpts_(t, h, testutil.UserOpts{Suspended: true})
	h.ExecPool(t, `UPDATE users SET username = 'searchsusp' WHERE id = $1`, suspended.ID)
	deleted := testutil.User(t, h)
	h.ExecPool(t, `UPDATE users SET username = 'searchdel', deleted_at = now() WHERE id = $1`, deleted.ID)

	search := func(t *testing.T, q string) []string {
		t.Helper()
		w := dash(t, td, hdl, http.MethodGet, "/api/v1/users/search?q="+url.QueryEscape(q), nil, searcher)
		if w.Code != http.StatusOK {
			t.Fatalf("search %q: status = %d: %s", q, w.Code, w.Body.String())
		}
		var res struct {
			Users []struct {
				Username string `json:"username"`
				Name     string `json:"name"`
			} `json:"users"`
		}
		platformJSON(t, w, &res)
		if res.Users == nil {
			t.Fatalf("search %q returned null, want an array", q)
		}
		out := make([]string, 0, len(res.Users))
		for _, u := range res.Users {
			out = append(out, u.Username)
		}
		return out
	}

	if w := platformAnonymous(t, hdl, http.MethodGet, "/api/v1/users/search?q=alice"); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous search: status = %d, want 401", w.Code)
	}
	if got := search(t, ""); len(got) != 0 {
		t.Errorf("empty query = %v, want no users", got)
	}
	got := search(t, "like_marker")
	if len(got) != 1 || got[0] != "like_marker" {
		t.Errorf("underscore search = %v, want only like_marker", got)
	}
	got = search(t, "100%")
	if len(got) != 1 || got[0] != "percent_real" {
		t.Errorf("percent search = %v, want only percent_real", got)
	}
	// Whitespace around the term is not part of the query.
	if got := search(t, "  like_marker  "); len(got) != 1 || got[0] != "like_marker" {
		t.Errorf("padded search = %v", got)
	}
	// Suspended and deleted accounts are not mentionable.
	if got := search(t, "searchsusp"); len(got) != 0 {
		t.Errorf("suspended account in results: %v", got)
	}
	if got := search(t, "searchdel"); len(got) != 0 {
		t.Errorf("deleted account in results: %v", got)
	}
	// Name matching is contained, not prefix-only.
	if got := search(t, "Underscore"); len(got) != 1 || got[0] != "like_marker" {
		t.Errorf("name search = %v, want like_marker", got)
	}
}

func TestPlatformPinnedMessages(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	alice := testutil.User(t, h)
	bob := testutil.User(t, h)
	carol := testutil.User(t, h)
	mallory := testutil.User(t, h)

	thread := func(other *domain.User) string {
		t.Helper()
		w := dash(t, td, hdl, http.MethodPost, "/api/v1/threads", map[string]any{"user_id": other.ID}, alice)
		if w.Code != http.StatusCreated {
			t.Fatalf("create thread: status = %d: %s", w.Code, w.Body.String())
		}
		var env threadEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("thread JSON: %v", err)
		}
		return env.Thread.ID
	}
	thread1 := thread(bob)
	thread2 := thread(carol)
	code, firstID := chatSend(t, td, hdl, alice, thread1, "pin me")
	if code != http.StatusCreated || firstID == 0 {
		t.Fatalf("send: status = %d, id = %d", code, firstID)
	}
	_, otherThreadMsg := chatSend(t, td, hdl, alice, thread2, "not this thread")

	pinnedList := func(t *testing.T, u *domain.User, threadID string) (int, []int64) {
		t.Helper()
		w := dash(t, td, hdl, http.MethodGet, "/api/v1/threads/"+threadID+"/pinned", nil, u)
		if w.Code != http.StatusOK {
			return w.Code, nil
		}
		var res struct {
			IDs []int64 `json:"pinned_message_ids"`
		}
		platformJSON(t, w, &res)
		if res.IDs == nil {
			t.Fatal("pinned list is null, want an array")
		}
		return w.Code, res.IDs
	}

	if w := platformAnonymous(t, hdl, http.MethodGet, "/api/v1/threads/"+thread1+"/pinned"); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous pinned list: status = %d, want 401", w.Code)
	}
	if w := dash(t, td, hdl, http.MethodPut,
		"/api/v1/threads/"+thread1+"/pin/"+util.Itoa(int(firstID)), nil, mallory); w.Code != http.StatusForbidden {
		t.Errorf("stranger pin: status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPut,
		"/api/v1/threads/"+thread1+"/pin/"+util.Itoa(int(firstID)), nil, alice); w.Code != http.StatusOK {
		t.Fatalf("pin: status = %d: %s", w.Code, w.Body.String())
	}
	// Pinning again is idempotent rather than a duplicate entry.
	if w := dash(t, td, hdl, http.MethodPut,
		"/api/v1/threads/"+thread1+"/pin/"+util.Itoa(int(firstID)), nil, alice); w.Code != http.StatusOK {
		t.Errorf("second pin: status = %d: %s", w.Code, w.Body.String())
	}
	code, ids := pinnedList(t, alice, thread1)
	if code != http.StatusOK || len(ids) != 1 || ids[0] != firstID {
		t.Errorf("pinned = %v (status %d), want [%d]", ids, code, firstID)
	}
	if code, _ := pinnedList(t, mallory, thread1); code != http.StatusForbidden {
		t.Errorf("stranger pinned list: status = %d, want 403", code)
	}
	// A message id from another thread is not pinnable through this one, and an
	// id that names nothing is a 404 rather than a 500.
	if w := dash(t, td, hdl, http.MethodPut,
		"/api/v1/threads/"+thread1+"/pin/"+util.Itoa(int(otherThreadMsg)), nil, alice); w.Code != http.StatusNotFound {
		t.Errorf("cross-thread pin: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPut,
		"/api/v1/threads/"+thread1+"/pin/999999999", nil, alice); w.Code != http.StatusNotFound {
		t.Errorf("unknown message pin: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodPut,
		"/api/v1/threads/"+thread1+"/pin/not-a-number", nil, alice); w.Code != http.StatusNotFound {
		t.Errorf("malformed message id: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodDelete,
		"/api/v1/threads/"+thread1+"/pin/"+util.Itoa(int(firstID)), nil, alice); w.Code != http.StatusOK {
		t.Fatalf("unpin: status = %d: %s", w.Code, w.Body.String())
	}
	if _, ids := pinnedList(t, alice, thread1); len(ids) != 0 {
		t.Errorf("pinned after unpin = %v", ids)
	}

	// The cap is five. Insert the messages directly: Send allows one message per
	// second per thread, which would add five seconds for no extra coverage.
	var extra []int64
	for i := 0; i < 6; i++ {
		var id int64
		if err := h.Pool.QueryRow(context.Background(), `
			INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, body, client_msg_id)
			VALUES ($1, $2, 'user', 'text', $3, $4) RETURNING id`,
			thread1, alice.ID, "extra "+util.Itoa(i), util.NewUUID()).Scan(&id); err != nil {
			t.Fatalf("insert message: %v", err)
		}
		extra = append(extra, id)
	}
	for i := 0; i < 5; i++ {
		if w := dash(t, td, hdl, http.MethodPut,
			"/api/v1/threads/"+thread1+"/pin/"+util.Itoa(int(extra[i])), nil, alice); w.Code != http.StatusOK {
			t.Fatalf("pin %d: status = %d: %s", i, w.Code, w.Body.String())
		}
	}
	if w := dash(t, td, hdl, http.MethodPut,
		"/api/v1/threads/"+thread1+"/pin/"+util.Itoa(int(extra[5])), nil, alice); w.Code != http.StatusBadRequest {
		t.Errorf("sixth pin: status = %d, want 400: %s", w.Code, w.Body.String())
	}
}
