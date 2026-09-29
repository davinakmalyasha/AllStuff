package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

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

// Slug change: one-time owner request (PRD §8.2 slug immutability).
func (s *Server) handleSlugChange(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.RequestSlugChange(r.Context(), user.ID, r.PathValue("id"))
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

	// The stream is spooled to disk inside Media.Upload with a per-kind size
	// cap — nothing beyond a 4 KB sniff head is held in RAM.
	item, err := s.deps.Media.Upload(r.Context(), service.UploadInput{
		UploaderID: user.ID,
		Kind:       kind,
		FileName:   header.Filename,
		Reader:     file,
	})
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"media": item})
}

// Serve public media by id. Chat uploads are PRIVATE: they require an
// authenticated user who participates in a thread containing the media.
// Public storefront/logo/product/avatar kinds stay open.
func (s *Server) handleMediaServe(w http.ResponseWriter, r *http.Request) {
	item, path, err := s.deps.Media.ServePath(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.authorizeChatMedia(r, item.Kind, item.ID); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", item.Mime)
	// Cache policy depends on visibility, not just on the path being immutable.
	//
	// The immutable-UUID year-long cache is correct for PUBLIC media. It is
	// wrong for chat attachments: authorizeChatMedia gates them per request, but
	// a `public` response authorises any shared cache (CDN, corporate proxy,
	// nginx proxy_cache) to store the body and re-serve it to an
	// UNAUTHENTICATED third party indefinitely. The authorization is per-request;
	// the cache key is not. `Vary: Cookie` is added so a shared cache that
	// ignores no-store still cannot cross users.
	if isPrivateMediaKind(item.Kind) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Cookie")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	// User-controlled bytes served from the API origin: forbid MIME sniffing
	// (text/plain → HTML XSS) and force download for anything that is not an
	// image/audio/video (files can carry active content).
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !strings.HasPrefix(item.Mime, "image/") && !strings.HasPrefix(item.Mime, "audio/") && !strings.HasPrefix(item.Mime, "video/") {
		w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeCDName(item.OriginalName)+`"`)
	}
	http.ServeFile(w, r, path)
}

// isPrivateMediaKind reports whether a kind is gated behind thread membership
// rather than being public. The two handlers that serve bytes must agree on
// this, so it lives in one place.
func isPrivateMediaKind(kind domain.MediaKind) bool {
	switch kind {
	case domain.MediaChatImage, domain.MediaChatFile, domain.MediaChatAudio, domain.MediaChatVideo,
		domain.MediaDocVerif:
		return true
	default:
		return false
	}
}

func sanitizeCDName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < 0x20 {
			return -1
		}
		return r
	}, filepath.Base(name))
	if name == "" || name == "." || name == "/" {
		return "download"
	}
	return name
}

func (s *Server) handleMediaThumb(w http.ResponseWriter, r *http.Request) {
	item, path, err := s.deps.Media.ServeThumbPath(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.authorizeChatMedia(r, item.Kind, item.ID); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	// Thumbnails share the immutable media UUID, but chat thumbnails inherit the
	// same per-thread authorization as the original, so they must not be
	// publicly cacheable either.
	if isPrivateMediaKind(item.Kind) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Cookie")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

// authorizeChatMedia enforces thread-membership on private conversation
// uploads. Without it anyone with the media UUID could fetch private chat
// images/files/audio/video unauthenticated — including after the message was
// deleted for everyone.
func (s *Server) authorizeChatMedia(r *http.Request, kind domain.MediaKind, mediaID string) error {
	switch kind {
	case domain.MediaChatImage, domain.MediaChatFile, domain.MediaChatAudio, domain.MediaChatVideo:
	default:
		return nil
	}
	user, found := currentUser(r)
	if !found {
		return domain.ErrNotAuthenticated
	}
	var n int
	err := s.deps.Repos.QueryRow(r.Context(), `
		SELECT count(*) FROM chat_messages m
		JOIN chat_participants p ON p.thread_id = m.thread_id AND p.user_id = $2 AND p.left_at IS NULL
		WHERE m.media_id = $1 AND m.deleted_for <> 'everyone'`,
		mediaID, user.ID).Scan(&n)
	if err != nil || n == 0 {
		return domain.ErrForbidden
	}
	return nil
}
