//go:build !race

// The tag matches the rest of the database-backed suite in this package: these
// tests clone a database per case, which is far too slow to also run under the
// race detector. media.Upload has no concurrency of its own, so the detector
// adds nothing here that the non-race run does not already cover.

// This file uses fixtureCtx from a tagged file, which is what the lint in
// buildtag_lint_test.go enforces - the tag is required, not optional.
package service

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
)

// media.Upload had no coverage at all in the last measured profile - 102
// statements, the largest uncovered function in the service package after chat
// send. The pure helpers beside it (sniffMIME, maxSizeFor, chatKinds,
// extensionFor) were already tested; what follows is the I/O path they feed.

var (
	pngMagic  = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	jpegMagic = []byte{0xFF, 0xD8, 0xFF, 0xE0}
	pdfMagic  = []byte("%PDF-1.7\n")
	zipMagic  = []byte("PK\x03\x04")
	oggMagic  = []byte("OggS")
	// An executable header: not a recognised type, and not printable, so the
	// text fallback must not claim it either.
	exeMagic = []byte{0x4D, 0x5A, 0x00, 0x01}
)

func pngOfSize(n int) []byte {
	b := make([]byte, n)
	copy(b, pngMagic)
	return b
}

// fillerReader produces count zero bytes without allocating them, so the
// size-limit test can cross a 10 MB cap without a 10 MB fixture.
type fillerReader struct{ remaining int64 }

func (f *fillerReader) Read(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > f.remaining {
		n = int(f.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 0
	}
	f.remaining -= int64(n)
	return n, nil
}

// newMediaFor returns the harness as well as the service.
//
// Calling testutil.New twice inside one test is a trap worth naming: the
// per-test database is named after the test, so the second call runs
// `DROP DATABASE ... WITH (FORCE)` against the same name and terminates the
// first harness's connections mid-test. The symptom is not a clear error - it is
// "conn closed" and "wsasend: connection forcibly closed" from arbitrary
// statements later on.
func newMediaFor(t *testing.T) (*Media, *testutil.H, string) {
	t.Helper()
	dir := t.TempDir()
	h := testutil.New(t)
	return NewMedia(h.Repos, dir, "/media", "", ""), h, dir
}

func TestMediaUploadRequiresAnUploader(t *testing.T) {
	m, _, dir := newMediaFor(t)

	_, err := m.Upload(fixtureCtx(t), UploadInput{
		Kind: domain.MediaLogo, FileName: "x.png", Reader: bytes.NewReader(pngOfSize(64)),
	})
	if !errors.Is(err, domain.ErrNotAuthenticated) {
		t.Errorf("err = %v, want ErrNotAuthenticated", err)
	}
	assertNoStoredFiles(t, dir)
}

func TestMediaUploadRejectsUnusableInput(t *testing.T) {
	m, h, dir := newMediaFor(t)
	u := testutil.User(t, h)

	// The specific reason lives in the error's field, which Error() does not
	// render, so these assert the class rather than the message. What matters
	// behaviourally is that each is a validation failure and not a panic, a
	// 500, or a stored row.
	cases := []struct {
		name  string
		input UploadInput
	}{
		{"no reader", UploadInput{UploaderID: u.ID, Kind: domain.MediaLogo, FileName: "x.png"}},
		{"empty file", UploadInput{UploaderID: u.ID, Kind: domain.MediaLogo, FileName: "x.png", Reader: bytes.NewReader(nil)}},
		{"a type that cannot be sniffed", UploadInput{UploaderID: u.ID, Kind: domain.MediaLogo, FileName: "x.exe", Reader: bytes.NewReader(exeMagic)}},
		{"a reader that fails", UploadInput{UploaderID: u.ID, Kind: domain.MediaLogo, FileName: "x.png", Reader: io.MultiReader(bytes.NewReader(pngMagic), errReader{})}},
	}
	for _, tc := range cases {
		_, err := m.Upload(fixtureCtx(t), tc.input)
		if !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", tc.name, err)
		}
	}
	assertNoStoredFiles(t, dir)
	assertNoMediaRows(t, h)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// The chat kinds carry their own allowlist on top of the sniffer, which is the
// part that stops an image being posted as a chat file or a document being
// posted as an image.
func TestMediaUploadHonoursTheChatKindAllowlist(t *testing.T) {
	m, h, dir := newMediaFor(t)
	u := testutil.User(t, h)

	rejected := []struct {
		kind domain.MediaKind
		body []byte
		why  string
	}{
		{domain.MediaChatFile, pngOfSize(64), "an image as a chat file"},
		{domain.MediaChatFile, jpegMagic, "a jpeg as a chat file"},
		{domain.MediaChatImage, pdfMagic, "a pdf as a chat image"},
		{domain.MediaChatImage, append(append([]byte{}, zipMagic...), make([]byte, 32)...), "a zip as a chat image"},
		{domain.MediaChatAudio, pngOfSize(64), "an image as a voice note"},
		{domain.MediaChatVideo, pdfMagic, "a pdf as a video clip"},
	}
	for _, tc := range rejected {
		_, err := m.Upload(fixtureCtx(t), UploadInput{
			UploaderID: u.ID, Kind: tc.kind, FileName: "f", Reader: bytes.NewReader(tc.body),
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", tc.why, err)
		}
	}
	assertNoStoredFiles(t, dir)

	accepted := []struct {
		kind domain.MediaKind
		body []byte
	}{
		{domain.MediaChatFile, pdfMagic},
		{domain.MediaChatFile, append(append([]byte{}, zipMagic...), make([]byte, 32)...)},
		{domain.MediaChatImage, pngOfSize(64)},
		{domain.MediaChatImage, jpegMagic},
		{domain.MediaChatAudio, append(append([]byte{}, oggMagic...), make([]byte, 32)...)},
		{domain.MediaChatVideo, append(append([]byte{0, 0, 0, 0x20}, []byte("ftypisom")...), make([]byte, 32)...)},
	}
	for _, tc := range accepted {
		if _, err := m.Upload(fixtureCtx(t), UploadInput{
			UploaderID: u.ID, Kind: tc.kind, FileName: "f", Reader: bytes.NewReader(tc.body),
		}); err != nil {
			t.Errorf("%s with %d bytes: %v", tc.kind, len(tc.body), err)
		}
	}
}

func TestMediaUploadEnforcesTheSizeLimit(t *testing.T) {
	m, h, dir := newMediaFor(t)
	u := testutil.User(t, h)

	// The sniff head plus a filler, so the cap is crossed without allocating the
	// payload: the stored file is never written, only spooled and removed.
	body := io.MultiReader(bytes.NewReader(pngMagic), &fillerReader{remaining: maxUploadSize})

	_, err := m.Upload(fixtureCtx(t), UploadInput{
		UploaderID: u.ID, Kind: domain.MediaLogo, FileName: "big.png", Reader: body,
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	// No spool file and no stored file. The kind directory itself is left
	// behind — os.MkdirAll runs before the size check — so only files are
	// asserted here.
	assertNoStoredFiles(t, dir)
	assertNoMediaRows(t, h)
}

func TestMediaUploadStoresTheFileAndTheRow(t *testing.T) {
	m, h, dir := newMediaFor(t)
	u := testutil.User(t, h)

	// Larger than the 4 KB sniff head, so the head has to be counted as well as
	// the bytes io.Copy reports. io.Copy replaces any prior count rather than
	// adding to it, which is the bug the size accounting exists to avoid.
	body := pngOfSize(5000)
	item, err := m.Upload(fixtureCtx(t), UploadInput{
		UploaderID: u.ID, Kind: domain.MediaLogo, FileName: "../../etc/passwd.png", Reader: bytes.NewReader(body),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if item.Mime != "image/png" {
		t.Errorf("Mime = %q, want image/png", item.Mime)
	}
	if item.Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d (the sniff head must be included)", item.Size, len(body))
	}
	if item.Kind != domain.MediaLogo {
		t.Errorf("Kind = %q", item.Kind)
	}
	// The caller-supplied name is a display name only. filepath.Base is what
	// stops a path being echoed back out through the API.
	if item.OriginalName != "passwd.png" {
		t.Errorf("OriginalName = %q, want the base name only", item.OriginalName)
	}
	if !strings.HasPrefix(item.Path, "logo/") || !strings.HasSuffix(item.Path, ".png") {
		t.Errorf("Path = %q, want logo/<uuid>.png", item.Path)
	}
	// The stored form has to be platform-independent, because it is data rather
	// than an OS path: a backslash here means a Linux server cannot find the
	// file after a restore.
	//
	// This assertion only bites on Windows - on Linux filepath.Join produces the
	// same string as path.Join, so a regression passes CI and corrupts only the
	// paths written by a Windows developer. Said plainly rather than papered
	// over: the guard is real exactly where the bug is.
	if strings.Contains(item.Path, `\`) {
		t.Errorf("Path = %q contains a backslash; the stored form must be platform-independent", item.Path)
	}
	if strings.Contains(item.Path, "..") {
		t.Errorf("Path = %q contains a traversal segment", item.Path)
	}
	abs := filepath.Join(dir, item.Path)
	if rel, err := filepath.Rel(dir, abs); err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("the stored file escapes the media directory: %s", abs)
	}

	on, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("the file is not on disk: %v", err)
	}
	if !bytes.Equal(on, body) {
		t.Errorf("the stored bytes differ from what was uploaded (%d vs %d)", len(on), len(body))
	}

	// And the row matches what the item claims. h.Repos and h.QueryRow share the
	// test transaction, so an uncommitted insert is visible here.
	var kind, mime, path, name string
	var size int64
	if err := h.QueryRow(t, `SELECT kind, mime, size, path, original_name FROM media WHERE id=$1`, item.ID).
		Scan(&kind, &mime, &size, &path, &name); err != nil {
		t.Fatalf("the media row was not written: %v", err)
	}
	if kind != "logo" || mime != "image/png" || size != int64(len(body)) || path != item.Path || name != "passwd.png" {
		t.Errorf("row = %s/%s/%d/%s/%s, item = %+v", kind, mime, size, path, name, item)
	}

	if item.URL == "" {
		t.Error("a public kind should carry a URL")
	}
}

// Verification documents are encrypted at rest when a key is configured, and the
// plaintext must not survive next to the ciphertext.
func TestMediaUploadEncryptsVerificationDocuments(t *testing.T) {
	dir := t.TempDir()
	h := testutil.New(t)
	u := testutil.User(t, h)
	// 32 bytes, hex-encoded: the key NewMedia accepts and ignores otherwise.
	key := strings.Repeat("ab", 32)
	m := NewMedia(h.Repos, dir, "/media", key, "")

	body := append(append([]byte{}, pdfMagic...), bytes.Repeat([]byte{0x20}, 200)...)
	item, err := m.Upload(fixtureCtx(t), UploadInput{
		UploaderID: u.ID, Kind: domain.MediaDocVerif, FileName: "id.pdf", Reader: bytes.NewReader(body),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if !strings.HasSuffix(item.Path, ".enc") {
		t.Errorf("Path = %q, want it to end in .enc", item.Path)
	}
	if item.ThumbURL != nil {
		t.Errorf("ThumbURL = %v, want nil; documents get no thumbnail", *item.ThumbURL)
	}

	enc, err := os.ReadFile(filepath.Join(dir, item.Path))
	if err != nil {
		t.Fatalf("the encrypted file is missing: %v", err)
	}
	if bytes.Contains(enc, pdfMagic) {
		t.Error("the plaintext is readable in the encrypted file")
	}
	if bytes.Equal(enc, body) {
		t.Error("the file was stored unencrypted")
	}
	if len(enc) <= len(body) {
		t.Errorf("encrypted size %d should exceed the plaintext %d by a nonce and a tag", len(enc), len(body))
	}

	// The plaintext must be gone, not merely unreferenced.
	plain := strings.TrimSuffix(filepath.Join(dir, item.Path), ".enc")
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Errorf("the unencrypted file is still on disk at %s", plain)
	}
}

// A verification document is NOT publicly servable, and ServePath enforces that
// with ErrForbidden. The item still carries a URL, because URL is assigned
// before the publicKinds check - so this pins the fact that the guard is at
// serve time rather than at upload time. A client that renders the URL gets a
// 403 rather than a document, which is the behaviour that matters.
func TestMediaUploadGivesDocumentsAURLThatServePathRefuses(t *testing.T) {
	dir := t.TempDir()
	h := testutil.New(t)
	u := testutil.User(t, h)
	m := NewMedia(h.Repos, dir, "/media", "", "")

	item, err := m.Upload(fixtureCtx(t), UploadInput{
		UploaderID: u.ID, Kind: domain.MediaDocVerif, FileName: "id.pdf", Reader: bytes.NewReader(pdfMagic),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if item.URL == "" {
		t.Fatal("expected the item to carry a URL; if that changed, this test and its comment are stale")
	}

	_, _, err = m.ServePath(fixtureCtx(t), item.ID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ServePath on a verification document: err = %v, want ErrForbidden", err)
	}
}

func assertNoMediaRows(t *testing.T, h *testutil.H) {
	t.Helper()
	var n int
	if err := h.QueryRow(t, `SELECT count(*) FROM media`).Scan(&n); err != nil {
		t.Fatalf("counting media rows: %v", err)
	}
	if n != 0 {
		t.Errorf("%d media row(s) were written by a rejected upload", n)
	}
}

// assertNoStoredFiles fails if a rejected or failed upload left a file behind.
//
// Directories are ignored deliberately: os.MkdirAll runs before the size check,
// so a rejected upload can leave an empty kind directory with nothing in it, and
// that is the code as written rather than a leak of content.
func assertNoStoredFiles(t *testing.T, dir string) {
	t.Helper()
	var leftovers []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path != dir && !info.IsDir() {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	if len(leftovers) > 0 {
		t.Errorf("a failed upload left files behind: %v", leftovers)
	}
}
