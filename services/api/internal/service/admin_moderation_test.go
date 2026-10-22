//go:build !race

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// The moderation-configuration half of Admin - the banned-word list, its
// allowlist, and the audit trail that every other decision writes into.

func TestAdminAddBannedWord(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()

	if err := svc.AddBannedWord(ctx, "   "); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("empty word: err = %v, want ErrValidation", err)
	}
	if err := svc.AddBannedWord(ctx, strings.Repeat("a", 41)); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("41 runes: err = %v, want ErrValidation", err)
	}

	// Trimmed and lowercased before it is stored, so "SPAM" and " spam " are one
	// entry rather than three.
	if err := svc.AddBannedWord(ctx, "  SPAM  "); err != nil {
		t.Fatalf("AddBannedWord: %v", err)
	}
	var word string
	if err := h.QueryRow(t, `SELECT word FROM banned_words`).Scan(&word); err != nil {
		t.Fatal(err)
	}
	if word != "spam" {
		t.Errorf("stored word = %q, want spam", word)
	}

	// Adding it again is a no-op, not an error (ON CONFLICT DO NOTHING).
	if err := svc.AddBannedWord(ctx, "spam"); err != nil {
		t.Errorf("duplicate: %v", err)
	}
	var n int
	if err := h.QueryRow(t, `SELECT count(*) FROM banned_words`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
}

func TestAdminAllowlist(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()

	// No configuration row is an empty allowlist, not an error.
	got, err := svc.Allowlist(ctx)
	if err != nil {
		t.Fatalf("Allowlist: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("allowlist = %v, want empty", got)
	}

	h.Exec(t, `INSERT INTO site_config (id, key, value) VALUES ($1, 'banned_words_allowlist', $2::jsonb)`,
		util.NewUUID(), `{"words":["scunthorpe","essex"]}`)

	got, err = svc.Allowlist(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "scunthorpe" || got[1] != "essex" {
		t.Errorf("allowlist = %v, want the two configured words", got)
	}
}

func TestAdminAuditTrail(t *testing.T) {
	svc, h := newTestAdmin(t)
	ctx := context.Background()
	admin := testutil.Admin(t, h)

	base := time.Now().UTC().Add(-time.Hour)
	for i, action := range []string{"hide", "warn", "suspend"} {
		h.Exec(t, `INSERT INTO moderation_actions (id, admin_id, action, target_type, target_id, reason, created_at)
			VALUES ($1,$2,$3,'user',$4,$5,$6)`,
			util.NewUUID(), admin.ID, action, util.NewUUID(), "reason "+action, base.Add(time.Duration(i)*time.Minute))
	}

	got, err := svc.AuditTrail(ctx, 10, 0)
	if err != nil {
		t.Fatalf("AuditTrail: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3", len(got))
	}
	// Newest first.
	if got[0]["action"] != "suspend" || got[2]["action"] != "hide" {
		t.Errorf("order = %v, %v, %v; want newest first", got[0]["action"], got[1]["action"], got[2]["action"])
	}
	// The admin's name is joined in, because an id is not an audit trail.
	if got[0]["admin_name"] != admin.Name {
		t.Errorf("admin_name = %v, want %q", got[0]["admin_name"], admin.Name)
	}
	if got[0]["reason"] != "reason suspend" {
		t.Errorf("reason = %v", got[0]["reason"])
	}

	// Paging.
	page, err := svc.AuditTrail(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0]["action"] != "warn" {
		t.Errorf("limit=1 offset=1 = %v, want the middle entry", page)
	}
}
