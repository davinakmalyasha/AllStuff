//go:build !race

package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/security"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
	"io"
	"net/http"
	"net/http/httptest"
)

// pushToThread was the last piece of the Web Push path that had never executed:
// 34 statements, 0% coverage, in a feature where six separate defects were found
// once the encryption was tested. What it decides is who gets a notification and
// whether a dead subscription is pruned.
//
// The test server receives the deliveries. Without WithHTTPClient the package
// client refuses anything but port 443 and a public address, so this could not be
// observed at all - which is exactly why the function had no test.
func TestPushToThreadFansOutAndPrunesDeadSubscriptions(t *testing.T) {
	s, h, _ := newDBTestServerDeps(t)
	ctx := context.Background()

	// Repos is rebound to the pool here, and it matters: pushToThread mixes a
	// raw query (which follows Repos' connection) with PushRepo.ByUsers, which
	// uses r.pool directly. On the default transaction-bound harness the first
	// would see the fixtures and the second would not, and the fan-out would
	// silently deliver to nobody.
	h.UsePool()
	s.deps.Repos = h.Repos

	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.HasSuffix(r.URL.Path, "/gone") {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	kp, err := security.GenerateVAPIDKeypair()
	if err != nil {
		t.Fatal(err)
	}
	s.deps.Config.VAPID = kp.WithHTTPClient(srv.Client())

	sender := testutil.User(t, h)
	live := testutil.User(t, h)
	gone := testutil.User(t, h)
	optedOut := testutil.User(t, h)
	left := testutil.User(t, h)

	thread := util.NewUUID()
	h.ExecPool(t, `INSERT INTO chat_threads (id, type) VALUES ($1, 'direct')`, thread)
	for _, p := range []struct {
		userID string
		left   bool
	}{
		{sender.ID, false}, {live.ID, false}, {gone.ID, false}, {optedOut.ID, false}, {left.ID, true},
	} {
		var leftAt any
		if p.left {
			leftAt = "2020-01-01T00:00:00Z"
		}
		h.ExecPool(t, `INSERT INTO chat_participants (id, thread_id, user_id, role, left_at) VALUES ($1,$2,$3,'user',$4)`,
			util.NewUUID(), thread, p.userID, leftAt)
	}

	// Real subscription keys, because Send decodes and validates the P-256 point
	// before it builds a request: a placeholder string is rejected locally and the
	// request never leaves, which looks identical to the fan-out not running.
	recv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}
	keys, err := json.Marshal(map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), recv.PublicKey.X, recv.PublicKey.Y)),
		"auth":   base64.RawURLEncoding.EncodeToString(authSecret),
	})
	if err != nil {
		t.Fatal(err)
	}

	subscribe := func(userID, path string) {
		t.Helper()
		h.ExecPool(t, `INSERT INTO push_subscriptions (id, user_id, endpoint, keys) VALUES ($1,$2,$3,$4)`,
			util.NewUUID(), userID, srv.URL+path, keys)
	}
	subscribe(live.ID, "/live")
	subscribe(gone.ID, "/gone")
	subscribe(optedOut.ID, "/opted-out")
	// The sender and a participant who left both hold live subscriptions. Without
	// that, a query that wrongly included them would deliver to nobody and this
	// test could not tell the difference.
	subscribe(sender.ID, "/sender")
	subscribe(left.ID, "/left")

	// Only an explicit boolean false disables push; this is the preference the
	// fan-out reads back as an any from jsonb.
	h.ExecPool(t, `UPDATE users SET notification_prefs = '{"channels":{"push":false}}' WHERE id=$1`, optedOut.ID)

	s.pushToThread(ctx, thread, &domain.ChatMessage{ThreadID: thread, SenderID: sender.ID, Body: bodyPtr("hi")})

	mu.Lock()
	got := append([]string{}, paths...)
	mu.Unlock()

	if len(got) != 2 {
		t.Fatalf("delivered to %d endpoints (%v), want 2: one live, one that answers 410", len(got), got)
	}
	seen := map[string]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if !seen["/live"] || !seen["/gone"] {
		t.Errorf("delivered to %v, want /live and /gone", got)
	}
	if seen["/opted-out"] {
		t.Error("a participant who disabled push was delivered to")
	}
	if seen["/sender"] {
		t.Error("the sender received a push for their own message")
	}
	if seen["/left"] {
		t.Error("a participant who left the thread was delivered to")
	}

	// The 410 prunes the dead subscription, so future fan-outs stop paying for an
	// endpoint that no longer exists. A 2xx must not prune.
	count := func(userID string) int {
		t.Helper()
		var n int
		if err := h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM push_subscriptions WHERE user_id=$1`, userID).Scan(&n); err != nil {
			t.Fatalf("counting subscriptions: %v", err)
		}
		return n
	}
	if n := count(gone.ID); n != 0 {
		t.Errorf("the dead subscription survived (%d rows); a 410 must prune it", n)
	}
	if n := count(live.ID); n != 1 {
		t.Errorf("the live subscription was removed (%d rows); a 2xx must not prune", n)
	}
	if n := count(optedOut.ID); n != 1 {
		t.Errorf("an opted-out participant's subscription was removed (%d rows); opting out is not unsubscribing", n)
	}
	// A participant who left keeps their subscription row - leaving a thread is
	// not unsubscribing from push - but is never dialled for it.
	if n := count(left.ID); n != 1 {
		t.Errorf("the left participant's subscription was removed (%d rows)", n)
	}
}

// With no keypair configured the fan-out must do nothing rather than dial.
func TestPushToThreadIsInertWithoutVAPID(t *testing.T) {
	s, h := newDBTestServer(t)
	ctx := context.Background()

	if s.deps.Config.VAPID != nil {
		t.Fatal("the harness configured VAPID; this test needs it unset")
	}

	u := testutil.User(t, h)
	thread := util.NewUUID()
	h.Exec(t, `INSERT INTO chat_threads (id, type) VALUES ($1, 'direct')`, thread)
	h.Exec(t, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,'user')`,
		util.NewUUID(), thread, u.ID)
	// A subscription pointing anywhere at all: nothing may be dialled.
	h.Exec(t, `INSERT INTO push_subscriptions (id, user_id, endpoint, keys) VALUES ($1,$2,$3,$4)`,
		util.NewUUID(), u.ID, "https://127.0.0.1:1/never", []byte(`{"p256dh":"x","auth":"y"}`))

	// No panic and no hang is the assertion; the package client would refuse this
	// address anyway, so a dial would surface as a timeout rather than a failure.
	s.pushToThread(ctx, thread, &domain.ChatMessage{ThreadID: thread, SenderID: u.ID, Body: bodyPtr("hi")})
}

func bodyPtr(s string) *string { return &s }
