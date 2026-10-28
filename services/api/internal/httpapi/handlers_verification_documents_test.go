//go:build !race

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Verification documents are identity documents. Three properties matter and
// none had a test: only the caller's own document upload may be attached to
// their listing, the admin viewer must decrypt and audit every access, and a
// malformed path id must not become a 500.

var pdfBytes = []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")

func TestDocumentAddRequiresTheCallersOwnVerificationMedia(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	ownerA := testutil.User(t, h)
	ownerB := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	bizA := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Documents A", Category: cat, Owner: ownerA, Status: domain.BusinessDraft,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	bizB := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Documents B", Category: cat, Owner: ownerB, Status: domain.BusinessDraft,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})

	code, docMedia := uploadMedia(t, td, hdl, ownerA, string(domain.MediaDocVerif), "identity.pdf", pdfBytes)
	if code != http.StatusCreated || docMedia == "" {
		t.Fatalf("upload a verification document: status = %d, id = %q", code, docMedia)
	}
	code, logoMedia := uploadMedia(t, td, hdl, ownerA, string(domain.MediaLogo), "logo.png", pngHeader)
	if code != http.StatusCreated || logoMedia == "" {
		t.Fatalf("upload a logo: status = %d, id = %q", code, logoMedia)
	}

	// The owner's own document upload is accepted.
	w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+bizA.ID+"/documents",
		map[string]any{"kind": "identity", "media_id": docMedia}, ownerA)
	if w.Code != http.StatusCreated {
		t.Fatalf("own document: status = %d: %s", w.Code, w.Body.String())
	}
	var createdDoc struct {
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createdDoc); err != nil || createdDoc.Document.ID == "" {
		t.Fatalf("decode the created document: %v: %s", err, w.Body.String())
	}

	// Another owner attaching A's document to B's listing would show A's
	// private identity document to the reviewer of B's application.
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+bizB.ID+"/documents",
		map[string]any{"kind": "identity", "media_id": docMedia}, ownerB)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "media_id") {
		t.Errorf("another owner's document: status = %d, body = %s; want the media_id 400", w.Code, w.Body.String())
	}

	// A public image is not a verification document, even when it is A's own.
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+bizA.ID+"/documents",
		map[string]any{"kind": "identity", "media_id": logoMedia}, ownerA)
	if w.Code != http.StatusBadRequest {
		t.Errorf("a logo as a document: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// A soft-deleted upload is no longer evidence of anything.
	code, deletedMedia := uploadMedia(t, td, hdl, ownerA, string(domain.MediaDocVerif), "old.pdf", pdfBytes)
	if code != http.StatusCreated {
		t.Fatalf("upload a second document: status = %d", code)
	}
	h.Exec(t, `UPDATE media SET deleted_at = now() WHERE id = $1`, deletedMedia)
	w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+bizA.ID+"/documents",
		map[string]any{"kind": "identity", "media_id": deletedMedia}, ownerA)
	if w.Code != http.StatusBadRequest {
		t.Errorf("a deleted document upload: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	for _, mediaID := range []string{
		"00000000-0000-4000-8000-000000000001", // well-formed, nonexistent
		"not-a-uuid",                           // would be SQLSTATE 22P02
	} {
		w = dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+bizA.ID+"/documents",
			map[string]any{"kind": "identity", "media_id": mediaID}, ownerA)
		if w.Code != http.StatusBadRequest {
			t.Errorf("media_id %q: status = %d, want 400: %s", mediaID, w.Code, w.Body.String())
		}
	}

	var docsB, docsA int
	if err := h.QueryRow(t, `SELECT count(*) FROM verification_documents WHERE business_id = $1`, bizB.ID).Scan(&docsB); err != nil {
		t.Fatal(err)
	}
	if err := h.QueryRow(t, `SELECT count(*) FROM verification_documents WHERE business_id = $1`, bizA.ID).Scan(&docsA); err != nil {
		t.Fatal(err)
	}
	if docsB != 0 || docsA != 1 {
		t.Errorf("documents = %d for B and %d for A; want 0 and 1", docsB, docsA)
	}

	// Removal has the same malformed-id problem in the opposite direction: the
	// id goes straight into a uuid comparison.
	w = dash(t, td, hdl, http.MethodDelete,
		"/api/v1/businesses/"+bizA.ID+"/documents/not-a-uuid", nil, ownerA)
	if w.Code != http.StatusNotFound {
		t.Errorf("remove with a malformed id: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := dash(t, td, hdl, http.MethodDelete,
		"/api/v1/businesses/"+bizA.ID+"/documents/"+createdDoc.Document.ID, nil, ownerA); w.Code != http.StatusNoContent {
		t.Errorf("remove the real document: status = %d, want 204: %s", w.Code, w.Body.String())
	}
}

func TestAdminDocumentFile(t *testing.T) {
	// Pool-bound: the audit-failure case raises a database error, and a failed
	// statement inside the shared test transaction would abort the rest.
	cfg := testConfig(t)
	cfg.MediaEncryptionKey = strings.Repeat("ab", 32)
	cfg.RateLimitGlobal = 100000
	s, h, td := newDBTestServerOpts(t, cfg, true)
	hdl := s.Handler()

	admin := testutil.Admin(t, h)
	h.ExecPool(t, `INSERT INTO user_2fa (id, user_id, totp_secret_encrypted, recovery_codes_hash, enabled_at)
		VALUES ($1,$2,'encrypted-secret','[]'::jsonb,now())`, util.NewUUID(), admin.ID)
	adminTFAEnrollment.Delete(admin.ID)

	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Admin Document Cafe", Category: cat, Owner: owner, Status: domain.BusinessDraft,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	other := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Other Draft", Category: cat, Owner: owner, Status: domain.BusinessDraft,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})

	code, mediaID := uploadMedia(t, td, hdl, owner, string(domain.MediaDocVerif), "identity proof.pdf", pdfBytes)
	if code != http.StatusCreated || mediaID == "" {
		t.Fatalf("upload: status = %d, id = %q", code, mediaID)
	}
	// Documents are the one kind encrypted at rest.
	var rel string
	if err := h.Repos.QueryRow(t.Context(), `SELECT path FROM media WHERE id = $1`, mediaID).Scan(&rel); err != nil {
		t.Fatalf("read the media path: %v", err)
	}
	if !strings.HasSuffix(rel, ".enc") {
		t.Fatalf("stored path = %q, want a .enc document", rel)
	}
	onDisk, err := os.ReadFile(filepath.Join(cfg.MediaDir, rel))
	if err != nil {
		t.Fatalf("read the stored file: %v", err)
	}
	if bytes.HasPrefix(onDisk, []byte("%PDF")) {
		t.Error("the verification document was stored in plaintext")
	}

	w := dash(t, td, hdl, http.MethodPost, "/api/v1/businesses/"+biz.ID+"/documents",
		map[string]any{"kind": "identity", "media_id": mediaID}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("add document: status = %d: %s", w.Code, w.Body.String())
	}
	var createdDoc struct {
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createdDoc); err != nil || createdDoc.Document.ID == "" {
		t.Fatalf("decode the created document: %v: %s", err, w.Body.String())
	}
	fileURL := "/api/v1/admin/verify/" + biz.ID + "/documents/" + createdDoc.Document.ID + "/file"

	w = dash(t, td, hdl, http.MethodGet, fileURL, nil, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("admin file: status = %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), pdfBytes) {
		t.Errorf("served %d bytes that do not match the uploaded document", w.Body.Len())
	}
	if got := w.Header().Get("Content-Type"); got != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", got)
	}
	if got := w.Header().Get("Content-Disposition"); got != `inline; filename="identity proof.pdf"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Errorf("CSP = %q, want the sandbox directive", got)
	}

	var views int
	if err := h.Repos.QueryRow(t.Context(),
		`SELECT count(*) FROM verification_document_views WHERE document_id = $1 AND admin_id = $2`,
		createdDoc.Document.ID, admin.ID).Scan(&views); err != nil {
		t.Fatalf("read the audit: %v", err)
	}
	if views != 1 {
		t.Errorf("audit rows = %d after one view, want 1", views)
	}

	if w := dash(t, td, hdl, http.MethodGet, fileURL, nil, owner); w.Code != http.StatusForbidden {
		t.Errorf("non-admin file: status = %d, want 403", w.Code)
	}
	// A document id belonging to another draft is not reachable through this
	// business's path.
	if w := dash(t, td, hdl, http.MethodGet,
		"/api/v1/admin/verify/"+other.ID+"/documents/"+createdDoc.Document.ID+"/file", nil, admin); w.Code != http.StatusNotFound {
		t.Errorf("document under another business: status = %d, want 404: %s", w.Code, w.Body.String())
	}
	// Malformed path segments are 404s, not Postgres 22P02 behind a 500.
	for _, target := range []string{
		"/api/v1/admin/verify/not-a-uuid/documents/" + createdDoc.Document.ID + "/file",
		"/api/v1/admin/verify/" + biz.ID + "/documents/not-a-uuid/file",
	} {
		if w := dash(t, td, hdl, http.MethodGet, target, nil, admin); w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404: %s", target, w.Code, w.Body.String())
		}
	}

	// If the audit cannot be written, the document must not be served: the
	// audit is the only record that a privileged user opened an identity file.
	if _, err := h.Pool.Exec(t.Context(), `
		CREATE FUNCTION document_view_fail() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'audit unavailable';
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := h.Pool.Exec(t.Context(), `
		CREATE TRIGGER document_view_fail BEFORE INSERT ON verification_document_views
		FOR EACH ROW EXECUTE FUNCTION document_view_fail()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	w = dash(t, td, hdl, http.MethodGet, fileURL, nil, admin)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("audit failure: status = %d, want 500: %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("%PDF")) {
		t.Error("the document was served even though its access could not be audited")
	}
	if _, err := h.Pool.Exec(t.Context(), `DROP TRIGGER document_view_fail ON verification_document_views`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}

	// Corrupt ciphertext is an internal error, never raw bytes or a panic.
	if err := os.WriteFile(filepath.Join(cfg.MediaDir, rel), []byte("not ciphertext"), 0o600); err != nil {
		t.Fatalf("corrupt the stored document: %v", err)
	}
	w = dash(t, td, hdl, http.MethodGet, fileURL, nil, admin)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("corrupt ciphertext: status = %d, want 500: %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("not ciphertext")) {
		t.Error("the corrupt ciphertext was echoed to the client")
	}
}
