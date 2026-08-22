package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/service"
)

// ---- owner business wizard (PRD §5.4.1) ----

func (s *Server) handleBusinessList(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	list, err := s.deps.Businesses.GetOwned(r.Context(), user.ID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"businesses": list})
}

func (s *Server) handleBusinessCreate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if !user.EmailVerified() {
		fail(w, domain.ErrEmailNotVerified)
		return
	}
	b, err := s.deps.Businesses.Create(r.Context(), user.ID)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"business": b})
}

func (s *Server) handleBusinessGet(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.GetOwnedOne(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

func (s *Server) handleBusinessUpdate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in service.BusinessInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		fail(w, domain.ErrValidation.WithField("_", "Request body is invalid."))
		return
	}
	b, err := s.deps.Businesses.Update(r.Context(), user.ID, r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

func (s *Server) handleBusinessSubmit(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.Submit(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

func (s *Server) handleBusinessResubmit(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.Resubmit(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

// ---- verification documents (owner side) ----

func (s *Server) handleDocumentsList(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.GetOwnedOne(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	docs, err := s.deps.Businesses.ListDocuments(r.Context(), b.ID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"documents": docs})
}

func (s *Server) handleDocumentAdd(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Kind    string `json:"kind"`
		MediaID string `json:"media_id"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	doc, err := s.deps.Businesses.AddDocument(r.Context(), user.ID, r.PathValue("id"), in.Kind, in.MediaID)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"document": doc})
}

func (s *Server) handleDocumentRemove(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	err := s.deps.Businesses.RemoveDocument(r.Context(), user.ID, r.PathValue("id"), r.PathValue("docId"))
	if err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// ---- media upload (PRD §7.4) ----

func (s *Server) handleMediaUpload(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	// Hard-cap the whole upload at 211MB (200MB chat-video limit + margin);
	// only 8MB is held in RAM, the rest spills to temp files.
	r.Body = http.MaxBytesReader(w, r.Body, 211<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		fail(w, domain.ErrValidation.WithField("file", "Upload too large or malformed."))
		return
	}
	kind := domain.MediaKind(r.FormValue("kind"))
	validKinds := map[domain.MediaKind]bool{
		domain.MediaLogo: true, domain.MediaCover: true, domain.MediaGallery: true,
		domain.MediaProduct: true, domain.MediaAvatar: true, domain.MediaDocVerif: true,
		domain.MediaChatImage: true, domain.MediaChatFile: true,
		domain.MediaChatAudio: true, domain.MediaChatVideo: true,
	}
	if !validKinds[kind] {
		fail(w, domain.ErrValidation.WithField("kind", "Invalid media kind."))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, domain.ErrValidation.WithField("file", "File field is required."))
		return
	}
	defer file.Close()

	// Read exactly what was sent (bounded): avoids allocating a fixed 210MB
	// buffer per request and never truncates on short reads.
	const maxUpload = 210 << 20
	data, readErr := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if len(data) == 0 || (readErr != nil && readErr != io.EOF) {
		fail(w, domain.ErrValidation.WithField("file", "Could not read upload."))
		return
	}

	item, err := s.deps.Media.Upload(r.Context(), service.UploadInput{
		UploaderID: user.ID,
		Kind:       kind,
		Data:       data,
		FileName:   header.Filename,
	})
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"media": item})
}

// Serve public media by id (kind-whitelisted; documents are admin-only).
func (s *Server) handleMediaServe(w http.ResponseWriter, r *http.Request) {
	item, path, err := s.deps.Media.ServePath(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", item.Mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

func (s *Server) handleMediaThumb(w http.ResponseWriter, r *http.Request) {
	_, path, err := s.deps.Media.ServeThumbPath(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

var _ = strconv.Itoa
