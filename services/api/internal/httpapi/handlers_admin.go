package httpapi

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/service"
)

// ---- admin: categories (PRD §5.8.3) ----

func (s *Server) handleAdminCategoriesCreate(w http.ResponseWriter, r *http.Request) {
	var in service.CategoryInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	cat, err := s.deps.Categories.Create(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"category": cat})
}

func (s *Server) handleAdminCategoriesUpdate(w http.ResponseWriter, r *http.Request) {
	var in service.CategoryInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	cat, err := s.deps.Categories.Update(r.Context(), r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"category": cat})
}

func (s *Server) handleAdminCategoriesDelete(w http.ResponseWriter, r *http.Request) {
	var moveTo *string
	if v := r.URL.Query().Get("move_to"); v != "" {
		moveTo = &v
	}
	if err := s.deps.Categories.Delete(r.Context(), r.PathValue("id"), moveTo); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// Category merge (PRD §5.8.3): businesses + children move to the target,
// then the source disappears.
func (s *Server) handleAdminCategoriesMerge(w http.ResponseWriter, r *http.Request) {
	cat, err := s.deps.Categories.Merge(r.Context(), r.PathValue("id"), r.PathValue("intoId"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"category": cat})
}

func (s *Server) handleAdminDocReRequest(w http.ResponseWriter, r *http.Request) {
	admin, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Kind string `json:"kind"`
		Note string `json:"note"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Admin.RequestDocument(r.Context(), admin.ID, r.PathValue("id"), in.Kind, strings.TrimSpace(in.Note)); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// ---- admin: verification queue (PRD §5.8.1) ----

func (s *Server) handleAdminVerifyQueue(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	statuses := []string{"pending_review"}
	if status == "rejected" {
		statuses = []string{"rejected"}
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 50)
	offset := parsePositiveInt(r.URL.Query().Get("offset"), 0)
	queue, err := s.deps.Admin.VerifyQueue(r.Context(), statuses, limit, offset)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"queue": queue})
}

func (s *Server) handleAdminVerifyDetail(w http.ResponseWriter, r *http.Request) {
	b, err := s.deps.Repos.Businesses.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if b == nil {
		fail(w, domain.ErrNotFound)
		return
	}
	docs, err := s.deps.Businesses.ListDocuments(r.Context(), b.ID)
	if err != nil {
		fail(w, err)
		return
	}
	owner, _ := s.deps.Repos.Users.GetByID(r.Context(), b.OwnerID)
	ownerInfo := map[string]any{"name": "", "email": "", "username": ""}
	if owner != nil {
		ownerInfo["name"] = owner.Name
		ownerInfo["email"] = owner.Email
		ownerInfo["username"] = owner.Username
	}
	ok(w, map[string]any{
		"business":  b,
		"documents": docs,
		"owner":     ownerInfo,
	})
}

func (s *Server) handleAdminVerifyDecide(w http.ResponseWriter, r *http.Request) {
	admin, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in service.DecideInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	b, err := s.deps.Admin.Decide(r.Context(), admin.ID, r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

// Secure document file serving for admins; every view is audited (PRD §9.3).
func (s *Server) handleAdminDocFile(w http.ResponseWriter, r *http.Request) {
	admin, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	docID := r.PathValue("docId")
	docs, err := s.deps.Businesses.ListDocuments(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	var target *domain.VerificationDocument
	for _, d := range docs {
		if d.ID == docID {
			target = d
			break
		}
	}
	if target == nil {
		fail(w, domain.ErrNotFound)
		return
	}
	// Audit: who viewed which document when.
	_ = s.deps.Admin.LogDocumentView(r.Context(), admin.ID, target.ID)

	item, path, err := s.deps.Media.DocumentPath(r.Context(), target.MediaID)
	if err != nil {
		fail(w, err)
		return
	}
	// Decrypt at rest (PRD §9.3): .enc files are AES-GCM encrypted documents.
	blob, err := os.ReadFile(path)
	if err != nil {
		fail(w, domain.ErrNotFound)
		return
	}
	if strings.HasSuffix(path, ".enc") {
		blob, err = s.deps.Media.Decrypt(blob)
		if err != nil {
			fail(w, domain.ErrInternal)
			return
		}
	}
	// Hardened serving of untrusted user bytes to privileged viewers:
	// `sandbox` CSP gives the document an opaque origin so no script inside
	// it can reach this API's origin/cookies even if a future sniffer change
	// or browser bug makes the declared MIME executable; nosniff pins the
	// type; the filename is sanitized for Content-Disposition.
	w.Header().Set("Content-Type", item.Mime)
	w.Header().Set("Content-Disposition", "inline; filename=\""+sanitizeCDName(target.FileName)+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(blob)
}

var _ = strconv.Itoa
