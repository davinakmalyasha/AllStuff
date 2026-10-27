//go:build !race

package httpapi

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Thumbnails are served by a second media route that had never run. It shares
// ServePath with the full-size route, so every visibility rule that applies
// there (hidden item, private chat kind, document refusal) has to apply here
// too - a thumbnail is a copy of the same private bytes.
//
// pngHeader is only a magic-byte stub and decodes to nothing, which is why it
// can stand in for an image that has no thumbnail. bigJPEG is a real image so
// Media.Upload can measure it and makeThumb can downsample it.

func bigJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 251), G: uint8(y % 241), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestMediaThumb(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	sender := testutil.User(t, h)
	participant := testutil.User(t, h)
	outsider := testutil.User(t, h)

	// The thumbnail path is derived from the stored path, so reading the row is
	// how the test knows which file the handler must have found.
	thumbFull := func(t *testing.T, id string) string {
		t.Helper()
		var stored string
		if err := h.QueryRow(t, `SELECT path FROM media WHERE id = $1`, id).Scan(&stored); err != nil {
			t.Fatalf("read media path: %v", err)
		}
		rel := strings.TrimSuffix(stored, filepath.Ext(stored)) + "_thumb.jpg"
		return filepath.Join(td.Config.MediaDir, rel)
	}
	get := func(t *testing.T, path string, u *domain.User) *httptest.ResponseRecorder {
		t.Helper()
		var req *http.Request
		if u == nil {
			req = httptest.NewRequest(http.MethodGet, path, nil)
		} else {
			req = td.authed(t, http.MethodGet, path, nil, u)
		}
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, req)
		return w
	}

	// ---- large public image ----
	code, id := uploadMedia(t, td, hdl, sender, string(domain.MediaLogo), "logo.jpg", bigJPEG(t, 800, 600))
	if code != http.StatusCreated || id == "" {
		t.Fatalf("upload: status = %d, id = %q", code, id)
	}
	if _, err := os.Stat(thumbFull(t, id)); err != nil {
		t.Fatalf("upload did not write a thumbnail: %v", err)
	}
	w := get(t, "/api/v1/media/"+id+"/thumb", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("public thumb: status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}
	// The id is immutable, so a public thumbnail is cacheable forever.
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "public") || !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want public, immutable", cc)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("served bytes are not a JPEG: %v", err)
	}
	if cfg.Width > 512 || cfg.Height > 512 || cfg.Width <= 0 || cfg.Height <= 0 {
		t.Errorf("thumbnail dimensions = %dx%d, want within 512", cfg.Width, cfg.Height)
	}

	// ---- small image: no thumbnail is generated ----
	code, smallID := uploadMedia(t, td, hdl, sender, string(domain.MediaLogo), "small.jpg", bigJPEG(t, 120, 90))
	if code != http.StatusCreated {
		t.Fatalf("small upload: status = %d", code)
	}
	if _, err := os.Stat(thumbFull(t, smallID)); !os.IsNotExist(err) {
		t.Errorf("a 120x90 image produced a thumbnail (stat err = %v)", err)
	}
	if w := get(t, "/api/v1/media/"+smallID+"/thumb", nil); w.Code != http.StatusNotFound {
		t.Errorf("small image thumb: status = %d, want 404: %s", w.Code, w.Body.String())
	} else if !strings.Contains(w.Body.String(), "not_found") {
		// The 404 must come from ServeThumbPath's stat, not from http.ServeFile
		// failing to open a missing path later. The latter still answers 404,
		// but with plain text and a bogus image/jpeg content type.
		t.Errorf("small image thumb 404 is not the media error: %s", w.Body.String())
	}
	// The original is still served; the absence of a thumbnail is not an error
	// for the full-size route.
	if w := get(t, "/api/v1/media/"+smallID+"/file", nil); w.Code != http.StatusOK {
		t.Errorf("small image file: status = %d", w.Code)
	}

	// A magic-byte-only upload has no dimensions, so it has no thumbnail either.
	code, stubID := uploadMedia(t, td, hdl, sender, string(domain.MediaLogo), "stub.png", pngHeader)
	if code != http.StatusCreated {
		t.Fatalf("stub upload: status = %d", code)
	}
	if w := get(t, "/api/v1/media/"+stubID+"/thumb", nil); w.Code != http.StatusNotFound {
		t.Errorf("stub thumb: status = %d, want 404", w.Code)
	}

	// ---- unknown and hidden ----
	if w := get(t, "/api/v1/media/"+util.NewUUID()+"/thumb", nil); w.Code != http.StatusNotFound {
		t.Errorf("unknown thumb: status = %d, want 404", w.Code)
	}
	h.Exec(t, `UPDATE media SET deleted_at = now() WHERE id = $1`, id)
	if w := get(t, "/api/v1/media/"+id+"/thumb", nil); w.Code != http.StatusNotFound {
		t.Errorf("hidden thumb: status = %d, want 404", w.Code)
	}

	// ---- private chat image ----
	code, chatID := uploadMedia(t, td, hdl, sender, string(domain.MediaChatImage), "photo.jpg", bigJPEG(t, 700, 500))
	if code != http.StatusCreated {
		t.Fatalf("chat upload: status = %d", code)
	}
	thread := util.NewUUID()
	h.Exec(t, `INSERT INTO chat_threads (id, type) VALUES ($1,'direct')`, thread)
	h.Exec(t, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,'user')`,
		util.NewUUID(), thread, participant.ID)
	h.Exec(t, `INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, media_id, client_msg_id)
		VALUES ($1,$2,'user','text',$3,$4)`, thread, sender.ID, chatID, util.NewUUID())

	thumbPath := "/api/v1/media/" + chatID + "/thumb"
	if w := get(t, thumbPath, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous chat thumb: status = %d, want 401", w.Code)
	}
	if w := get(t, thumbPath, outsider); w.Code != http.StatusForbidden {
		t.Errorf("outsider chat thumb: status = %d, want 403", w.Code)
	}
	w = get(t, thumbPath, participant)
	if w.Code != http.StatusOK {
		t.Fatalf("participant chat thumb: status = %d: %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") || !strings.Contains(cc, "private") {
		t.Errorf("chat thumb Cache-Control = %q, want private, no-store", cc)
	}
	if !strings.Contains(w.Header().Get("Vary"), "Cookie") {
		t.Errorf("chat thumb Vary = %q, want Cookie", w.Header().Get("Vary"))
	}

	// A participant who left the thread loses the attachment, and a message
	// deleted for everyone withdraws it - the same rules as the full-size route.
	leaver := testutil.User(t, h)
	h.Exec(t, `INSERT INTO chat_participants (id, thread_id, user_id, role, left_at)
		VALUES ($1,$2,$3,'user',now())`, util.NewUUID(), thread, leaver.ID)
	if w := get(t, thumbPath, leaver); w.Code != http.StatusForbidden {
		t.Errorf("left participant: status = %d, want 403: %s", w.Code, w.Body.String())
	}
	h.Exec(t, `UPDATE chat_messages SET deleted_for = 'everyone' WHERE media_id = $1`, chatID)
	if w := get(t, thumbPath, participant); w.Code != http.StatusForbidden {
		t.Errorf("thumb after delete-for-everyone: status = %d, want 403: %s", w.Code, w.Body.String())
	}

	// ---- verification documents are never servable ----
	code, docID := uploadMedia(t, td, hdl, sender, string(domain.MediaDocVerif), "id.pdf",
		[]byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n"))
	if code != http.StatusCreated {
		t.Fatalf("doc upload: status = %d", code)
	}
	if w := get(t, "/api/v1/media/"+docID+"/thumb", nil); w.Code != http.StatusForbidden {
		t.Errorf("document thumb: status = %d, want 403: %s", w.Code, w.Body.String())
	}
}
