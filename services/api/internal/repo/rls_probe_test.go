package repo_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"allstuff/api/internal/repo"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// Row-level security (migrations 0041+0053) is the backstop behind every
// tenant boundary in this repository: a missed WHERE in a repo method must
// surface as an empty result, never as another user's row. These tests prove
// the policies from the reader's side, through the same hooked pool the HTTP
// server uses (repo.ConfigureRLS + SET ROLE app + app.user_id per
// acquisition).
//
// The fixtures go through h.UsePool() FIRST, so they commit immediately and
// are visible to the second pool on the same database. Without that, the
// tx-bound fixtures would be invisible and every assertion would read "0 of
// N" for the wrong reason (see testutil.UsePool).

func rlsHookedPool(t *testing.T, h *testutil.H) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping the integration test")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	dbname := h.Pool.Config().ConnConfig.Database
	if dbname == "" {
		t.Fatal("test pool has no database name")
	}
	u.Path = "/" + dbname
	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatalf("parse per-test DSN: %v", err)
	}
	repo.ConfigureRLS(cfg)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect hooked pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func rlsCtx(userID string) context.Context {
	return repo.WithUserID(context.Background(), userID)
}

func rlsCount(t *testing.T, pool *pgxpool.Pool, ctx context.Context, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

// The plumbing proof: acquisitions run as the app role with the requesting
// user stamped, and the identity does not leak into the next holder.
func TestRLSHooksSetRoleAndUser(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	pool := rlsHookedPool(t, h)
	a := testutil.User(t, h)
	b := testutil.User(t, h)

	var role string
	var uid sql.NullString
	if err := pool.QueryRow(rlsCtx(a.ID), `SELECT current_user, current_setting('allstuff_app.user_id', true)`).Scan(&role, &uid); err != nil {
		t.Fatalf("acquire as A: %v", err)
	}
	if role != "allstuff_app" {
		t.Errorf("session role = %q, want allstuff_app (least privilege)", role)
	}
	if !uid.Valid || uid.String != a.ID {
		t.Errorf("app.user_id = %q, want %q", uid.String, a.ID)
	}

	// Anonymous contexts run as app with no user: public reads work, tenant
	// reads match nothing.
	var anonRole string
	var anonUID sql.NullString
	if err := pool.QueryRow(context.Background(), `SELECT current_user, current_setting('allstuff_app.user_id', true)`).Scan(&anonRole, &anonUID); err != nil {
		t.Fatalf("acquire anonymous: %v", err)
	}
	if anonRole != "allstuff_app" || (anonUID.Valid && anonUID.String != "") {
		t.Errorf("anonymous = (%q, %q), want (allstuff_app, empty)", anonRole, anonUID.String)
	}

	// No leak across checkouts: B must see B, and a failed acquisition must
	// not serve as the owner.
	var uidB sql.NullString
	if err := pool.QueryRow(rlsCtx(b.ID), `SELECT current_setting('allstuff_app.user_id', true)`).Scan(&uidB); err != nil {
		t.Fatalf("acquire as B: %v", err)
	}
	if !uidB.Valid || uidB.String != b.ID {
		t.Errorf("second holder sees %q, want %q (identity leaked across release)", uidB.String, b.ID)
	}
}

func TestRLSCollectionsIsolateUsers(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	pool := rlsHookedPool(t, h)
	a := testutil.User(t, h)
	b := testutil.User(t, h)

	ctx := context.Background()
	mk := func(owner, name string, pub bool) string {
		id := util.NewUUID()
		_, err := h.Pool.Exec(ctx,
			`INSERT INTO collections (id, user_id, name, slug, is_public) VALUES ($1,$2,$3,$4,$5)`,
			id, owner, name, "c-"+id[:8], pub)
		if err != nil {
			t.Fatalf("seed collection: %v", err)
		}
		return id
	}
	mk(a.ID, "A private", false)
	mk(a.ID, "A public", true)

	q := `SELECT count(*) FROM collections`
	if n := rlsCount(t, pool, rlsCtx(a.ID), q); n != 2 {
		t.Errorf("owner sees %d collections, want 2", n)
	}
	if n := rlsCount(t, pool, rlsCtx(b.ID), q); n != 1 {
		t.Errorf("other user sees %d collections, want 1 (public only)", n)
	}
	if n := rlsCount(t, pool, ctx, q); n != 1 {
		t.Errorf("anonymous sees %d collections, want 1 (public only)", n)
	}

	// Items follow the parent: the public link works logged out, the private
	// one does not.
	var pubID string
	if err := h.Pool.QueryRow(ctx, `SELECT id FROM collections WHERE is_public`).Scan(&pubID); err != nil {
		t.Fatal(err)
	}
	itemID := util.NewUUID()
	if _, err := h.Pool.Exec(ctx, `INSERT INTO collection_items (id, collection_id, target_type, target_id) VALUES ($1,$2,'business',$3)`,
		itemID, pubID, util.NewUUID()); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	iq := `SELECT count(*) FROM collection_items WHERE collection_id = $1`
	if n := rlsCount(t, pool, ctx, iq, pubID); n != 1 {
		t.Errorf("anonymous sees %d public items, want 1", n)
	}
}

func TestRLSChatIsolation(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	pool := rlsHookedPool(t, h)
	a := testutil.User(t, h)
	b := testutil.User(t, h)
	outsider := testutil.User(t, h)
	ctx := context.Background()

	tid := util.NewUUID()
	if _, err := h.Pool.Exec(ctx, `INSERT INTO chat_threads (id, type) VALUES ($1,'direct')`, tid); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{a.ID, b.ID} {
		if _, err := h.Pool.Exec(ctx, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,'user')`,
			util.NewUUID(), tid, u); err != nil {
			t.Fatal(err)
		}
	}
	msgID := util.NewUUID()
	if _, err := h.Pool.Exec(ctx, `INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, body, client_msg_id) VALUES ($1,$2,'user','text','hello',$3)`,
		tid, a.ID, msgID); err != nil {
		t.Fatal(err)
	}

	mq := `SELECT count(*) FROM chat_messages WHERE thread_id = $1`
	if n := rlsCount(t, pool, rlsCtx(b.ID), mq, tid); n != 1 {
		t.Errorf("participant sees %d messages, want 1", n)
	}
	if n := rlsCount(t, pool, rlsCtx(outsider.ID), mq, tid); n != 0 {
		t.Errorf("outsider sees %d messages, want 0", n)
	}
	tq := `SELECT count(*) FROM chat_threads WHERE id = $1`
	if n := rlsCount(t, pool, rlsCtx(outsider.ID), tq, tid); n != 0 {
		t.Errorf("outsider sees %d threads, want 0", n)
	}
	pq := `SELECT count(*) FROM chat_participants WHERE thread_id = $1`
	if n := rlsCount(t, pool, rlsCtx(a.ID), pq, tid); n != 2 {
		t.Errorf("participant sees %d participants, want 2 (self + fellow)", n)
	}
	if n := rlsCount(t, pool, rlsCtx(outsider.ID), pq, tid); n != 0 {
		t.Errorf("outsider sees %d participants, want 0", n)
	}
}

func TestRLSSavedSearchesAPIKeysAndClaims(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	pool := rlsHookedPool(t, h)
	a := testutil.User(t, h)
	b := testutil.User(t, h)
	admin := testutil.Admin(t, h)
	ctx := context.Background()

	if _, err := h.Pool.Exec(ctx, `INSERT INTO saved_searches (id, user_id, name, query) VALUES ($1,$2,'kopi','{}')`,
		util.NewUUID(), a.ID); err != nil {
		t.Fatal(err)
	}
	keyID := util.NewUUID()
	if _, err := h.Pool.Exec(ctx, `INSERT INTO api_keys (id, user_id, name, key_hash, prefix) VALUES ($1,$2,'k','hash-`+keyID+`','p')`,
		keyID, a.ID); err != nil {
		t.Fatal(err)
	}
	claimID := util.NewUUID()
	if _, err := h.Pool.Exec(ctx, `INSERT INTO business_claims (id, user_id, name, address, city) VALUES ($1,$2,'Shop','Jl 1','Jakarta')`,
		claimID, a.ID); err != nil {
		t.Fatal(err)
	}

	ss := `SELECT count(*) FROM saved_searches`
	if n := rlsCount(t, pool, rlsCtx(a.ID), ss); n != 1 {
		t.Errorf("owner sees %d searches, want 1", n)
	}
	if n := rlsCount(t, pool, rlsCtx(b.ID), ss); n != 0 {
		t.Errorf("other user sees %d searches, want 0", n)
	}

	// The API-key login runs unauthenticated, so NULL may look keys up (by
	// hash in the auth path); another authenticated user may not.
	ak := `SELECT count(*) FROM api_keys WHERE id = $1`
	if n := rlsCount(t, pool, ctx, ak, keyID); n != 1 {
		t.Errorf("anonymous key lookup sees %d, want 1 (login path)", n)
	}
	if n := rlsCount(t, pool, rlsCtx(b.ID), ak, keyID); n != 0 {
		t.Errorf("other user sees %d keys, want 0", n)
	}

	cq := `SELECT count(*) FROM business_claims`
	if n := rlsCount(t, pool, rlsCtx(a.ID), cq); n != 1 {
		t.Errorf("claimant sees %d claims, want 1", n)
	}
	if n := rlsCount(t, pool, rlsCtx(b.ID), cq); n != 0 {
		t.Errorf("stranger sees %d claims, want 0", n)
	}
	if n := rlsCount(t, pool, rlsCtx(admin.ID), cq); n != 1 {
		t.Errorf("admin sees %d claims, want 1 (moderation queue)", n)
	}
}

func TestRLSMediaKinds(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	pool := rlsHookedPool(t, h)
	a := testutil.User(t, h)
	b := testutil.User(t, h)
	admin := testutil.Admin(t, h)
	ctx := context.Background()

	mk := func(uploader, kind string) string {
		id := util.NewUUID()
		_, err := h.Pool.Exec(ctx, `INSERT INTO media (id, uploader_id, kind, original_name, mime, size, path) VALUES ($1,$2,$3,'f','image/jpeg',10,$4)`,
			id, uploader, kind, "m/"+id)
		if err != nil {
			t.Fatalf("seed media: %v", err)
		}
		return id
	}
	logo := mk(a.ID, "logo")
	chatImg := mk(a.ID, "chat_image")

	// Wire the chat image into a thread A belongs to.
	tid := util.NewUUID()
	if _, err := h.Pool.Exec(ctx, `INSERT INTO chat_threads (id, type) VALUES ($1,'direct')`, tid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,'user')`,
		util.NewUUID(), tid, a.ID); err != nil {
		t.Fatal(err)
	}
	var msgID int64
	if err := h.Pool.QueryRow(ctx, `INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, body, media_id, client_msg_id) VALUES ($1,$2,'user','image','pic',$3,$4) RETURNING id`,
		tid, a.ID, chatImg, util.NewUUID()).Scan(&msgID); err != nil {
		t.Fatal(err)
	}
	_ = msgID

	mq := `SELECT count(*) FROM media WHERE id = $1`
	if n := rlsCount(t, pool, ctx, mq, logo); n != 1 {
		t.Errorf("anonymous sees %d public logos, want 1", n)
	}
	if n := rlsCount(t, pool, ctx, mq, chatImg); n != 0 {
		t.Errorf("anonymous sees %d chat images, want 0", n)
	}
	if n := rlsCount(t, pool, rlsCtx(b.ID), mq, chatImg); n != 0 {
		t.Errorf("non-participant sees %d chat images, want 0", n)
	}
	if n := rlsCount(t, pool, rlsCtx(a.ID), mq, chatImg); n != 1 {
		t.Errorf("uploader sees %d chat images, want 1", n)
	}
	if n := rlsCount(t, pool, rlsCtx(admin.ID), mq, chatImg); n != 1 {
		t.Errorf("admin sees %d chat images, want 1 (attachment moderation + doc viewer)", n)
	}

	// Verification documents: the owning manager and admins, nobody else.
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Shop " + a.ID[:8], Category: cat, Owner: a,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	docMedia := mk(a.ID, "document_verification")
	docID := util.NewUUID()
	if _, err := h.Pool.Exec(ctx, `INSERT INTO verification_documents (id, business_id, kind, media_id) VALUES ($1,$2,'registration',$3)`,
		docID, biz.ID, docMedia); err != nil {
		t.Fatal(err)
	}
	dq := `SELECT count(*) FROM verification_documents WHERE id = $1`
	if n := rlsCount(t, pool, rlsCtx(a.ID), dq, docID); n != 1 {
		t.Errorf("manager sees %d documents, want 1", n)
	}
	if n := rlsCount(t, pool, rlsCtx(b.ID), dq, docID); n != 0 {
		t.Errorf("stranger sees %d documents, want 0", n)
	}
	if n := rlsCount(t, pool, rlsCtx(admin.ID), dq, docID); n != 1 {
		t.Errorf("admin sees %d documents, want 1", n)
	}
	vq := `SELECT count(*) FROM verification_document_views`
	if _, err := pool.Exec(rlsCtx(admin.ID), `INSERT INTO verification_document_views (id, document_id, admin_id) VALUES ($1,$2,$3)`,
		util.NewUUID(), docID, admin.ID); err != nil {
		t.Errorf("admin audit write: %v", err)
	}
	if n := rlsCount(t, pool, rlsCtx(b.ID), vq); n != 0 {
		t.Errorf("stranger sees %d audit rows, want 0", n)
	}
}

func TestRLSBillingLedgerNullOnly(t *testing.T) {
	h := testutil.New(t)
	h.UsePool()
	pool := rlsHookedPool(t, h)
	a := testutil.User(t, h)
	admin := testutil.Admin(t, h)
	ctx := context.Background()

	ev := "evt_" + util.NewUUID()[:8]
	if _, err := h.Pool.Exec(ctx, `INSERT INTO billing_webhook_events (provider_event_id, type, object_id) VALUES ($1,'customer.subscription.created','sub_x')`, ev); err != nil {
		t.Fatal(err)
	}
	wq := `SELECT count(*) FROM billing_webhook_events WHERE provider_event_id = $1`
	// The signed webhook runs with no user: it must read and write.
	if n := rlsCount(t, pool, ctx, wq, ev); n != 1 {
		t.Errorf("webhook context sees %d events, want 1", n)
	}
	// Authenticated sessions see nothing: the ledger is not per-user data.
	if n := rlsCount(t, pool, rlsCtx(a.ID), wq, ev); n != 0 {
		t.Errorf("user session sees %d ledger rows, want 0", n)
	}
	if n := rlsCount(t, pool, rlsCtx(admin.ID), wq, ev); n != 1 {
		t.Errorf("admin sees %d ledger rows, want 1 (forensics)", n)
	}
}
