//go:build !race

package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// Media upload and serving had no coverage. The serving side is a security
// boundary: it returns user-supplied bytes from the API origin, and for chat
// kinds it is the only thing standing between a private attachment and anyone
// who guesses the UUID.

// pngHeader is enough for the sniffer: it matches on the magic bytes.
var pngHeader = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte{0}, 64)...)

// uploadMedia posts a multipart body and returns the new media id.
func uploadMedia(t *testing.T, td *testDeps, hdl http.Handler, u *domain.User, kind, filename string, content []byte) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("kind", kind)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(content)
	_ = mw.Close()

	req := td.authed(t, http.MethodPost, "/api/v1/media", buf.Bytes(), u)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, req)

	var out struct {
		Media struct {
			ID string `json:"id"`
		} `json:"media"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.Media.ID
}

func TestMediaUploadRejects(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := testutil.User(t, h)

	// Anonymous uploads are refused by CSRF first (403), because CSRF runs ahead
	// of auth for every state-changing route.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	hdl.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("anonymous upload: status = %d, want 403", w.Code)
	}

	// The kind is an allowlist.
	if code, _ := uploadMedia(t, td, hdl, user, "not_a_kind", "x.png", pngHeader); code != http.StatusBadRequest {
		t.Errorf("invalid kind: status = %d, want 400", code)
	}
	// A chat kind only accepts its own MIME set: a PNG is not an audio file.
	if code, _ := uploadMedia(t, td, hdl, user, string(domain.MediaChatAudio), "x.png", pngHeader); code != http.StatusBadRequest {
		t.Errorf("png as audio: status = %d, want 400", code)
	}
	// Content is sniffed, so an extension cannot smuggle a type past the list.
	if code, _ := uploadMedia(t, td, hdl, user, string(domain.MediaChatImage), "x.png", []byte("this is not an image at all")); code != http.StatusBadRequest {
		t.Errorf("non-image as chat image: status = %d, want 400", code)
	}
}

func TestMediaServePublic(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	user := testutil.User(t, h)

	code, id := uploadMedia(t, td, hdl, user, string(domain.MediaLogo), "logo.png", pngHeader)
	if code != http.StatusCreated || id == "" {
		t.Fatalf("upload: status = %d, id = %q", code, id)
	}

	// Public kinds are served to anyone, and are cacheable forever because the
	// id is immutable.
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+id+"/file", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("serve: status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "public") {
		t.Errorf("Cache-Control = %q, want a public cache policy", cc)
	}
	// Served from the API origin, so sniffing must be off.
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}

	// An unknown id is a 404, not a path error.
	w = httptest.NewRecorder()
	hdl.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+util.NewUUID()+"/file", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown media: status = %d, want 404", w.Code)
	}
}

// A chat attachment is readable only by an active participant of a thread that
// contains it, and must not be cacheable by any shared cache.
func TestMediaServeChatIsPrivate(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	sender := testutil.User(t, h)
	participant := testutil.User(t, h)
	outsider := testutil.User(t, h)

	code, id := uploadMedia(t, td, hdl, sender, string(domain.MediaChatImage), "photo.png", pngHeader)
	if code != http.StatusCreated || id == "" {
		t.Fatalf("upload: status = %d", code)
	}
	path := "/api/v1/media/" + id + "/file"

	// Anonymous: unauthenticated.
	w := httptest.NewRecorder()
	hdl.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", w.Code)
	}
	// Authenticated but not in the thread: forbidden.
	w = dash(t, td, hdl, http.MethodGet, path, nil, outsider)
	if w.Code != http.StatusForbidden {
		t.Errorf("outsider: status = %d, want 403", w.Code)
	}

	// Put it in a thread the participant belongs to.
	thread := util.NewUUID()
	h.Exec(t, `INSERT INTO chat_threads (id, type) VALUES ($1,'direct')`, thread)
	h.Exec(t, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,'user')`,
		util.NewUUID(), thread, participant.ID)
	h.Exec(t, `INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, media_id, client_msg_id)
		VALUES ($1,$2,'user','text',$3,$4)`, thread, sender.ID, id, util.NewUUID())

	w = dash(t, td, hdl, http.MethodGet, path, nil, participant)
	if w.Code != http.StatusOK {
		t.Fatalf("participant: status = %d: %s", w.Code, w.Body.String())
	}
	// The authorization is per request, so the response must not be cacheable by
	// a shared cache that would re-serve it to someone else.
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") || !strings.Contains(cc, "private") {
		t.Errorf("Cache-Control = %q, want private, no-store", cc)
	}
	if !strings.Contains(w.Header().Get("Vary"), "Cookie") {
		t.Errorf("Vary = %q, want Cookie", w.Header().Get("Vary"))
	}

	// A message deleted for everyone takes the attachment out of reach.
	h.Exec(t, `UPDATE chat_messages SET deleted_for='everyone' WHERE media_id=$1`, id)
	if w := dash(t, td, hdl, http.MethodGet, path, nil, participant); w.Code != http.StatusForbidden {
		t.Errorf("after delete-for-everyone: status = %d, want 403", w.Code)
	}
}

// Anything that is not image/audio/video is served as a download, so active
// content cannot be rendered inline from the API origin.
func TestMediaServeNonMediaIsAnAttachment(t *testing.T) {
	s, h, td := newDBTestServerDeps(t)
	hdl := s.Handler()
	sender := testutil.User(t, h)
	participant := testutil.User(t, h)

	// No backslash in this name on purpose: sanitizeCDName normalises "\" to "/"
	// and then takes the base, so a backslash never reaches the quote-stripping
	// step. A quote does, and it is what must not break the header.
	code, id := uploadMedia(t, td, hdl, sender, string(domain.MediaChatFile), `weird"name.txt`, []byte("plain text payload, long enough to sniff"))
	if code != http.StatusCreated {
		t.Fatalf("upload: status = %d", code)
	}
	thread := util.NewUUID()
	h.Exec(t, `INSERT INTO chat_threads (id, type) VALUES ($1,'direct')`, thread)
	h.Exec(t, `INSERT INTO chat_participants (id, thread_id, user_id, role) VALUES ($1,$2,$3,'user')`,
		util.NewUUID(), thread, participant.ID)
	h.Exec(t, `INSERT INTO chat_messages (thread_id, sender_id, sender_role, type, media_id, client_msg_id)
		VALUES ($1,$2,'user','text',$3,$4)`, thread, sender.ID, id, util.NewUUID())

	w := dash(t, td, hdl, http.MethodGet, "/api/v1/media/"+id+"/file", nil, participant)
	if w.Code != http.StatusOK {
		t.Fatalf("serve: status = %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="weirdname.txt"`) {
		t.Errorf("Content-Disposition = %q, want the quote stripped from the filename", cd)
	}
}

func TestSanitizeCDName(t *testing.T) {
	cases := map[string]string{
		`report.pdf`:       `report.pdf`,
		`a"b.txt`:          `ab.txt`,
		`a\b.txt`:          `b.txt`,
		"a\x00b.txt":       `ab.txt`,
		`../../etc/passwd`: `passwd`,
		``:                 "download",
		`.`:                "download",
		`invoice "1".pdf`:  `invoice 1.pdf`,
		`..\..\evil.exe`:   `evil.exe`,
	}
	for in, want := range cases {
		if got := sanitizeCDName(in); got != want {
			t.Errorf("sanitizeCDName(%q) = %q, want %q", in, got, want)
		}
	}
}
