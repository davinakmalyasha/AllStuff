package repo_test

import (
	"context"
	"testing"

	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// Two audit columns that were declared, documented in the PRD, and never written.
//
// These are the same defect as auth_events.ip, sessions.last_seen_at and
// chat_messages.read_count: the schema has the column, the read path may even select
// it, and nothing ever populates it. The failure is silent by construction - an empty
// audit column looks exactly like an audit column with nothing to report.

// pendingDoc inserts a verification document in the state a review acts on.
func pendingDoc(t *testing.T, h *testutil.H, bizID, uploaderID string) string {
	t.Helper()
	// media has seven NOT NULL columns and no fixture helper, so it is built here.
	mediaID := util.NewUUID()
	h.Exec(t, `INSERT INTO media (id, uploader_id, kind, original_name, mime, size, path)
	           VALUES ($1::uuid, $2::uuid, 'document_verification', 'license.pdf',
	                   'application/pdf', 1024, $3)`,
		mediaID, uploaderID, "media/"+mediaID+".pdf")

	docID := util.NewUUID()
	h.Exec(t, `INSERT INTO verification_documents (id, business_id, kind, media_id, status)
	           VALUES ($1::uuid, $2::uuid, 'license', $3::uuid, 'pending')`,
		docID, bizID, mediaID)
	return docID
}

type docRow struct {
	Status     string
	ReviewerID *string
	ReviewedAt *string
}

func readDoc(t *testing.T, h *testutil.H, docID string) docRow {
	t.Helper()
	var r docRow
	if err := h.QueryRow(t,
		`SELECT status, reviewer_id::text, reviewed_at::text
		   FROM verification_documents WHERE id = $1::uuid`, docID,
	).Scan(&r.Status, &r.ReviewerID, &r.ReviewedAt); err != nil {
		t.Fatalf("read verification_documents: %v", err)
	}
	return r
}

// THE HEADLINE CASE
// A verification decision grants a trust badge. Recording when it happened but not
// who decided leaves the accountability chain one step short - and
// verification_document_views already records who LOOKED at the document.
func TestDocumentReviewRecordsTheReviewer(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	owner := testutil.User(t, h)
	admin := testutil.Admin(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	doc := pendingDoc(t, h, biz.ID, owner.ID)

	before := readDoc(t, h, doc)
	if before.ReviewerID != nil {
		t.Fatal("a pending document already has a reviewer; the fixture is wrong")
	}

	if err := h.Repos.Businesses.SetDocumentStatuses(ctx, admin.ID, biz.ID, "approved"); err != nil {
		t.Fatalf("SetDocumentStatuses: %v", err)
	}

	after := readDoc(t, h, doc)
	if after.Status != "approved" {
		t.Errorf("status = %q, want approved", after.Status)
	}
	if after.ReviewerID == nil {
		t.Fatal("reviewer_id is NULL after a decision; the column is declared but never written")
	}
	if *after.ReviewerID != admin.ID {
		t.Errorf("reviewer_id = %q, want the deciding admin %q", *after.ReviewerID, admin.ID)
	}
	if after.ReviewedAt == nil {
		t.Error("reviewed_at is NULL; the timestamp regression is back too")
	}
}

// Rejection is the decision most likely to be questioned later, so it must name the
// reviewer as clearly as approval does.
func TestDocumentRejectionRecordsTheReviewer(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	owner := testutil.User(t, h)
	admin := testutil.Admin(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	doc := pendingDoc(t, h, biz.ID, owner.ID)

	if err := h.Repos.Businesses.SetDocumentStatuses(ctx, admin.ID, biz.ID, "rejected"); err != nil {
		t.Fatalf("SetDocumentStatuses: %v", err)
	}
	after := readDoc(t, h, doc)
	if after.ReviewerID == nil || *after.ReviewerID != admin.ID {
		t.Errorf("reviewer_id = %v, want %v", after.ReviewerID, admin.ID)
	}
}

// Only PENDING documents are touched. A decision must not re-stamp a document that
// was already reviewed, which would move reviewed_at and overwrite the reviewer of
// the original decision.
func TestDocumentReviewDoesNotRestampAnAlreadyDecidedDocument(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	owner := testutil.User(t, h)
	first := testutil.Admin(t, h)
	second := testutil.Admin(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	doc := pendingDoc(t, h, biz.ID, owner.ID)

	if err := h.Repos.Businesses.SetDocumentStatuses(ctx, first.ID, biz.ID, "approved"); err != nil {
		t.Fatalf("first SetDocumentStatuses: %v", err)
	}
	if err := h.Repos.Businesses.SetDocumentStatuses(ctx, second.ID, biz.ID, "rejected"); err != nil {
		t.Fatalf("second SetDocumentStatuses: %v", err)
	}

	after := readDoc(t, h, doc)
	if after.Status != "approved" {
		t.Errorf("status = %q, want the original approved", after.Status)
	}
	if after.ReviewerID == nil || *after.ReviewerID != first.ID {
		t.Errorf("reviewer_id = %v, want the FIRST reviewer %v", after.ReviewerID, first.ID)
	}
}

// ---------------------------------------------------------------------------
// chat_threads.closed_at / closed_by
// ---------------------------------------------------------------------------

type threadRow struct {
	Status   string
	ClosedAt *string
	ClosedBy *string
}

func readThread(t *testing.T, h *testutil.H, threadID string) threadRow {
	t.Helper()
	var r threadRow
	if err := h.QueryRow(t,
		`SELECT status, closed_at::text, closed_by::text FROM chat_threads WHERE id = $1::uuid`, threadID,
	).Scan(&r.Status, &r.ClosedAt, &r.ClosedBy); err != nil {
		t.Fatalf("read chat_threads: %v", err)
	}
	return r
}

func openThread(t *testing.T, h *testutil.H, bizID string) string {
	t.Helper()
	tid := util.NewUUID()
	h.Exec(t, `INSERT INTO chat_threads (id, business_id, type, status)
	           VALUES ($1::uuid, $2::uuid, 'business', 'open')`, tid, bizID)
	return tid
}

// Closing a thread between a customer and a business owner must record who ended it
// and when. Neither column appeared anywhere in the Go source before this.
func TestClosingAThreadRecordsWhoAndWhen(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	owner := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	tid := openThread(t, h, biz.ID)

	before := readThread(t, h, tid)
	if before.ClosedAt != nil || before.ClosedBy != nil {
		t.Fatal("a new thread already carries closed_*; the fixture is wrong")
	}

	if err := h.Repos.Chat.SetThreadStatus(ctx, tid, "closed", owner.ID); err != nil {
		t.Fatalf("SetThreadStatus: %v", err)
	}

	after := readThread(t, h, tid)
	if after.Status != "closed" {
		t.Errorf("status = %q, want closed", after.Status)
	}
	if after.ClosedAt == nil {
		t.Error("closed_at is NULL after closing; the column is declared but never written")
	}
	if after.ClosedBy == nil {
		t.Fatal("closed_by is NULL after closing; nobody is accountable for ending the thread")
	}
	if *after.ClosedBy != owner.ID {
		t.Errorf("closed_by = %q, want %q", *after.ClosedBy, owner.ID)
	}
}

// A customer replying to a closed thread reopens it. The reopen must CLEAR both
// columns: leaving them would make the row claim it was closed by someone at a time
// when it is open, which is worse than an empty column because it reads as a fact.
func TestReopeningAThreadClearsTheClosedColumns(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	owner := testutil.User(t, h)
	customer := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	tid := openThread(t, h, biz.ID)

	if err := h.Repos.Chat.SetThreadStatus(ctx, tid, "closed", owner.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := h.Repos.Chat.SetThreadStatus(ctx, tid, "open", customer.ID); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	after := readThread(t, h, tid)
	if after.Status != "open" {
		t.Errorf("status = %q, want open", after.Status)
	}
	if after.ClosedAt != nil {
		t.Errorf("closed_at = %q after reopen, want NULL - a stale timestamp misreports the state", *after.ClosedAt)
	}
	if after.ClosedBy != nil {
		t.Errorf("closed_by = %q after reopen, want NULL", *after.ClosedBy)
	}
}

// An unowned listing can have no `closed_by`, and a NULL actor must not fail the
// statement. 0029 made owner_id nullable, so this is a reachable state rather than a
// hypothetical one.
func TestClosingWithNoActorIsStillRecorded(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	owner := testutil.User(t, h)
	biz := testutil.Business(t, h, testutil.BusinessOpts{Owner: owner})
	tid := openThread(t, h, biz.ID)

	if err := h.Repos.Chat.SetThreadStatus(ctx, tid, "closed", ""); err != nil {
		t.Fatalf("SetThreadStatus with no actor: %v", err)
	}
	after := readThread(t, h, tid)
	if after.Status != "closed" {
		t.Errorf("status = %q, want closed", after.Status)
	}
	if after.ClosedAt == nil {
		t.Error("closed_at is NULL; an unknown actor must not suppress the timestamp")
	}
	if after.ClosedBy != nil {
		t.Errorf("closed_by = %q, want NULL for an unknown actor", *after.ClosedBy)
	}
}
