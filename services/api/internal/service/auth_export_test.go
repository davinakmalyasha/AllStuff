//go:build !race

package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// ExportData is the account export. It had no coverage at all in the last
// measured profile - 90 statements - and it is the one function in this package
// where a mistake is not a bug but a disclosure: the failure that matters is not
// a missing field, it is another account's data appearing in the payload.
//
// The strongest assertion available is therefore on the serialized output, not on
// the individual fields: whatever shape the maps take, the bytes that leave the
// server must not contain anything belonging to someone else.
func TestExportDataContainsOnlyTheRequestersData(t *testing.T) {
	h := testutil.New(t)
	a := newTestAuth(t, h)
	ctx := fixtureCtx(t)

	owner := testutil.User(t, h)
	// A distinctive hash, because testutil.User stores the one-character string
	// "x" - which appears all over any JSON payload and would make a substring
	// check for the hash pass or fail for the wrong reason.
	const passwordHash = "DISTINCTIVE-PASSWORD-HASH-VALUE"
	h.Exec(t, `UPDATE users SET password_hash = $2 WHERE id = $1`, owner.ID, passwordHash)
	other := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")

	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Owner Co", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	prod := testutil.Product(t, h, biz.ID, "Kopi Susu")

	// The owner's own review, and one that has been soft-deleted.
	ownReview := testutil.Review(t, h, biz.ID, owner.ID, 5)
	h.Exec(t, `UPDATE reviews SET text = $2 WHERE id = $1`, ownReview.ID, "OWN-REVIEW-TEXT")
	biz2 := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Owner Two", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	stale := testutil.Review(t, h, biz2.ID, owner.ID, 3)
	h.Exec(t, `UPDATE reviews SET deleted_at = now(), text = $2 WHERE id = $1`, stale.ID, "DELETED-REVIEW-TEXT")

	// Another account's review and comment, with text that is unmistakable in a
	// payload search.
	foreignReview := testutil.Review(t, h, biz.ID, other.ID, 1)
	h.Exec(t, `UPDATE reviews SET text = $2 WHERE id = $1`, foreignReview.ID, "FOREIGN-REVIEW-TEXT")

	h.Exec(t, `INSERT INTO comments (id, business_id, user_id, text) VALUES ($1,$2,$3,$4)`,
		util.NewUUID(), biz.ID, owner.ID, "OWN-COMMENT-TEXT")
	h.Exec(t, `INSERT INTO comments (id, business_id, user_id, text) VALUES ($1,$2,$3,$4)`,
		util.NewUUID(), biz.ID, other.ID, "FOREIGN-COMMENT-TEXT")

	h.Exec(t, `INSERT INTO collections (id, user_id, name, slug) VALUES ($1,$2,$3,$4)`,
		util.NewUUID(), owner.ID, "OWN-COLLECTION", "own-collection")
	h.Exec(t, `INSERT INTO collections (id, user_id, name, slug) VALUES ($1,$2,$3,$4)`,
		util.NewUUID(), other.ID, "FOREIGN-COLLECTION", "foreign-collection")

	// A thread the owner is in, with a message from each side and one deleted for
	// everyone.
	thread := util.NewUUID()
	h.Exec(t, `INSERT INTO chat_threads (id, type) VALUES ($1, 'direct')`, thread)
	for _, u := range []string{owner.ID, other.ID} {
		h.Exec(t, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,'user')`,
			util.NewUUID(), thread, u)
	}
	h.Exec(t, `INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, body, client_msg_id) VALUES ($1,$2,'user','text',$3,$4)`,
		thread, owner.ID, "OWN-MESSAGE", util.NewUUID())
	h.Exec(t, `INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, body, client_msg_id) VALUES ($1,$2,'user','text',$3,$4)`,
		thread, other.ID, "PEER-MESSAGE", util.NewUUID())
	h.Exec(t, `INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, body, client_msg_id, deleted_for) VALUES ($1,$2,'user','text',$3,$4,'everyone')`,
		thread, owner.ID, "DELETED-FOR-EVERYONE-ONLY", util.NewUUID())

	// A thread the owner is NOT in, with recognisable content.
	foreignThread := util.NewUUID()
	h.Exec(t, `INSERT INTO chat_threads (id, type) VALUES ($1, 'direct')`, foreignThread)
	h.Exec(t, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,'user')`,
		util.NewUUID(), foreignThread, other.ID)
	h.Exec(t, `INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, body, client_msg_id) VALUES ($1,$2,'user','text',$3,$4)`,
		foreignThread, other.ID, "FOREIGN-THREAD-MESSAGE", util.NewUUID())

	out, err := a.ExportData(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ExportData: %v", err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("the export does not marshal: %v", err)
	}
	payload := string(raw)

	t.Run("nothing from another account appears", func(t *testing.T) {
		for _, forbidden := range []string{
			"FOREIGN-REVIEW-TEXT",
			"FOREIGN-COMMENT-TEXT",
			"FOREIGN-COLLECTION",
			"FOREIGN-THREAD-MESSAGE",
			other.Email,
			other.Username,
		} {
			if forbidden == "" {
				continue
			}
			if strings.Contains(payload, forbidden) {
				t.Errorf("the export contains %q, which belongs to another account", forbidden)
			}
		}
	})

	t.Run("the requesters own data is present", func(t *testing.T) {
		for _, want := range []string{
			"OWN-REVIEW-TEXT",
			"OWN-COMMENT-TEXT",
			"OWN-COLLECTION",
			"OWN-MESSAGE",
			"Owner Co",
			prod.Name,
			owner.Email,
		} {
			if !strings.Contains(payload, want) {
				t.Errorf("the export is missing %q", want)
			}
		}
	})

	t.Run("soft-deleted content is excluded", func(t *testing.T) {
		for _, gone := range []string{"DELETED-REVIEW-TEXT", "DELETED-FOR-EVERYONE-ONLY"} {
			if strings.Contains(payload, gone) {
				t.Errorf("the export contains %q, which was deleted", gone)
			}
		}
	})

	// A conversation export necessarily contains the other party's messages: the
	// thread is the record, and omitting half of it would make the export
	// misleading. Pinned as a deliberate choice rather than an oversight.
	t.Run("a conversation includes the peer's messages", func(t *testing.T) {
		if !strings.Contains(payload, "PEER-MESSAGE") {
			t.Error("the peer's message is absent from a thread export")
		}
	})

	t.Run("messages are oldest first", func(t *testing.T) {
		msgs, ok := out["messages"].([]map[string]any)
		if !ok {
			t.Fatalf("messages is %T", out["messages"])
		}
		// The query reads newest-first and the function reverses the slice. Only
		// the two non-deleted messages survive.
		if len(msgs) != 2 {
			t.Fatalf("got %d messages, want 2", len(msgs))
		}
		// body is a *string (the column is nullable), so compare positions in the
		// serialized payload rather than the map values - fmt prints a pointer and
		// an interface comparison against a string never matches.
		iOwn, iPeer := strings.Index(payload, "OWN-MESSAGE"), strings.Index(payload, "PEER-MESSAGE")
		if iOwn < 0 || iPeer < 0 {
			t.Fatalf("messages missing from the payload: own=%d peer=%d", iOwn, iPeer)
		}
		if iOwn > iPeer {
			t.Error("messages are newest-first; the export should read oldest-first")
		}
	})

	t.Run("no credential material is exported", func(t *testing.T) {
		profile, ok := out["profile"].(map[string]any)
		if !ok {
			t.Fatalf("profile is %T", out["profile"])
		}
		for _, k := range []string{"password_hash", "password", "hash", "refresh_token", "totp_secret"} {
			if _, present := profile[k]; present {
				t.Errorf("the profile export carries %q", k)
			}
		}
		if strings.Contains(payload, passwordHash) {
			t.Error("the password hash appears in the export")
		}
	})
}

// The shape matters as much as the content: an export that returns null where a
// client expects a list is a broken export, and a data-subject request is not the
// place to be debugging one.
func TestExportDataForAnEmptyAccountReturnsEmptyCollections(t *testing.T) {
	h := testutil.New(t)
	a := newTestAuth(t, h)
	u := testutil.User(t, h)

	out, err := a.ExportData(fixtureCtx(t), u.ID)
	if err != nil {
		t.Fatalf("ExportData: %v", err)
	}

	for _, key := range []string{"businesses", "products", "collections", "reviews", "comments", "threads", "messages"} {
		if _, ok := out[key]; !ok {
			t.Errorf("the export has no %q key", key)
			continue
		}
		raw, err := json.Marshal(out[key])
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if string(raw) == "null" {
			t.Errorf("%s is null; an empty account should export an empty list", key)
		}
	}
}

func TestExportDataForAnUnknownUser(t *testing.T) {
	h := testutil.New(t)
	a := newTestAuth(t, h)

	_, err := a.ExportData(fixtureCtx(t), util.NewUUID())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
