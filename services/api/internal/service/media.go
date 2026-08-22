package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/util"
)

// Media — upload storage (PRD §5.3.1, §7.4). Local disk in dev;
// S3-compatible object storage swaps in for prod without API changes.
type Media struct {
	repos *repo.Repos
	dir   string // MEDIA_DIR
	base  string // public URL prefix for media
	key   []byte // AES-256-GCM key for verification documents (nil = plain, dev)
	clam  string // ClamAV clamd TCP address ("" = scan disabled)
}

// NewMedia wires the service. encKeyHex (32 raw bytes, hex) enables
// at-rest encryption for verification documents (PRD §9.3); clamAddr enables
// virus scanning (clamd protocol, PRD §9.3).
func NewMedia(repos *repo.Repos, dir, base, encKeyHex, clamAddr string) *Media {
	var key []byte
	if encKeyHex != "" {
		if k, err := hex.DecodeString(encKeyHex); err == nil && len(k) == 32 {
			key = k
		}
	}
	return &Media{repos: repos, dir: dir, base: base, key: key, clam: clamAddr}
}

const maxUploadSize = 10 << 20 // 10MB (PRD §8.2 documents, §5.5 images)

// Per-kind size caps (PRD §5.5.2): images 10MB, files 20MB, voice notes
// 25MB, video clips 200MB.
func maxSizeFor(kind domain.MediaKind) int64 {
	switch kind {
	case domain.MediaChatFile:
		return 20 << 20
	case domain.MediaChatAudio:
		return 25 << 20
	case domain.MediaChatVideo:
		return 200 << 20
	}
	return maxUploadSize
}

var publicKinds = map[domain.MediaKind]bool{
	domain.MediaLogo:      true,
	domain.MediaCover:     true,
	domain.MediaGallery:   true,
	domain.MediaProduct:   true,
	domain.MediaAvatar:    true,
	domain.MediaChatImage: true,
	domain.MediaChatFile:  true,
	domain.MediaChatAudio: true,
	domain.MediaChatVideo: true,
}

// chatKinds: the allowed mime sets per chat media kind (PRD §5.5.2).
var chatKinds = map[domain.MediaKind]map[string]bool{
	domain.MediaChatImage: {"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true},
	domain.MediaChatFile:  {"application/pdf": true, "application/zip": true, "text/plain": true, "application/octet-stream": true},
	domain.MediaChatAudio: {"audio/webm": true, "audio/mpeg": true, "audio/mp4": true, "audio/ogg": true, "audio/wav": true},
	domain.MediaChatVideo: {"video/mp4": true, "video/webm": true, "video/quicktime": true},
}

type UploadInput struct {
	UploaderID string
	Kind       domain.MediaKind
	Data       []byte
	FileName   string
}

var ErrInvalidMedia = errors.New("invalid media")

// Upload validates magic bytes, persists, and returns the media record.
func (m *Media) Upload(ctx context.Context, in UploadInput) (*domain.MediaItem, error) {
	if in.UploaderID == "" {
		return nil, domain.ErrNotAuthenticated
	}
	max := maxSizeFor(in.Kind)
	if len(in.Data) == 0 || int64(len(in.Data)) > max {
		return nil, domain.ErrValidation.WithField("file", "File is empty or exceeds the size limit for this kind.")
	}

	mime, err := sniffMIME(in.Data)
	if err != nil {
		return nil, domain.ErrValidation.WithField("file", err.Error())
	}

	// Chat kinds enforce their own allowlist (PRD §5.5.2).
	if allowed, ok := chatKinds[in.Kind]; ok && !allowed[mime] {
		return nil, domain.ErrValidation.WithField("file", "File type not allowed for this kind.")
	}

	var w, h *int
	if strings.HasPrefix(mime, "image/") {
		img, _, err := image.Decode(bytes.NewReader(in.Data))
		if err == nil {
			b := img.Bounds()
			bw, bh := b.Dx(), b.Dy()
			w, h = &bw, &bh
		}
	}

	name := util.NewUUID()
	ext := extensionFor(mime)
	rel := filepath.Join(string(in.Kind), name+ext)
	full := filepath.Join(m.dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}

	// Verification documents: encrypt at rest when a key is configured (PRD §9.3).
	stored := in.Data
	if in.Kind == domain.MediaDocVerif && m.key != nil {
		enc, err := m.encrypt(in.Data)
		if err != nil {
			return nil, domain.ErrInternal
		}
		stored = enc
		rel += ".enc"
		full += ".enc"
	}
	if err := os.WriteFile(full, stored, 0o600); err != nil {
		return nil, err
	}

	item := &domain.MediaItem{
		ID:           util.NewUUID(),
		UploaderID:   in.UploaderID,
		Kind:         in.Kind,
		OriginalName: filepath.Base(in.FileName),
		Mime:         mime,
		Size:         int64(len(in.Data)),
		Width:        w,
		Height:       h,
		Path:         rel,
		CreatedAt:    time.Now(),
	}
	if err := m.repos.Media.Create(ctx, item); err != nil {
		return nil, err
	}

	// Virus scan (best-effort, clamd protocol): mark clean/infected/error.
	if m.clam != "" {
		switch m.scan(in.Data) {
		case "clean":
			_, _ = m.repos.Exec(ctx, `UPDATE media SET virus_scan_status='clean' WHERE id=$1`, item.ID)
		case "infected":
			_, _ = m.repos.Exec(ctx, `UPDATE media SET virus_scan_status='infected' WHERE id=$1`, item.ID)
			_ = os.Remove(full)
			return nil, domain.ErrValidation.WithField("file", "File failed the virus scan.")
		default:
			_, _ = m.repos.Exec(ctx, `UPDATE media SET virus_scan_status='error' WHERE id=$1`, item.ID)
		}
	}

	item.URL = m.publicURL(item)
	if publicKinds[item.Kind] {
		if thumb := m.makeThumb(full, item); thumb != nil {
			item.ThumbURL = thumb
		}
	}
	return item, nil
}

func (m *Media) publicURL(item *domain.MediaItem) string {
	return m.base + "/" + item.ID + "/file"
}

// ServePath resolves a media record to its on-disk path (nil if missing).
func (m *Media) ServePath(ctx context.Context, id string) (*domain.MediaItem, string, error) {
	item, err := m.repos.Media.GetByID(ctx, id)
	if err != nil || item == nil {
		return nil, "", domain.ErrNotFound
	}
	if !publicKinds[item.Kind] {
		return nil, "", domain.ErrForbidden // documents are not publicly servable (PRD §5.8.1)
	}
	return item, filepath.Join(m.dir, item.Path), nil
}

// ServeThumbPath resolves a thumbnail (generated at upload, if the image was large).
func (m *Media) ServeThumbPath(ctx context.Context, id string) (*domain.MediaItem, string, error) {
	item, _, err := m.ServePath(ctx, id)
	if err != nil {
		return nil, "", err
	}
	thumb := strings.TrimSuffix(item.Path, filepath.Ext(item.Path)) + "_thumb.jpg"
	thumbFull := filepath.Join(m.dir, thumb)
	if _, err := os.Stat(thumbFull); err != nil {
		return nil, "", domain.ErrNotFound
	}
	return item, thumbFull, nil
}

// DocumentPath resolves a verification document file (admin only; access audited upstream).
func (m *Media) DocumentPath(ctx context.Context, mediaID string) (*domain.MediaItem, string, error) {
	item, err := m.repos.Media.GetByID(ctx, mediaID)
	if err != nil || item == nil {
		return nil, "", domain.ErrNotFound
	}
	return item, filepath.Join(m.dir, item.Path), nil
}

// ---- at-rest encryption (PRD §9.3) ----

// encrypt AES-256-GCM: random 12-byte nonce || ciphertext.
func (m *Media) encrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, data, nil), nil
}

func (m *Media) decrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
}

// Decrypt decrypts a blob produced by encrypt() (used by the document viewer).
func (m *Media) Decrypt(data []byte) ([]byte, error) { return m.decrypt(data) }

// ---- virus scanning (PRD §9.3, clamd protocol) ----

// scan streams the payload to clamd INSTREAM; returns clean|infected|error.
func (m *Media) scan(data []byte) string {
	conn, err := net.DialTimeout("tcp", m.clam, 10*time.Second)
	if err != nil {
		return "error"
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return "error"
	}
	const chunk = 1 << 20
	for off := 0; off < len(data); off += chunk {
		end := min(off+chunk, len(data))
		part := data[off:end]
		head := []byte{byte(len(part) >> 24), byte(len(part) >> 16), byte(len(part) >> 8), byte(len(part))}
		if _, err := conn.Write(head); err != nil {
			return "error"
		}
		if _, err := conn.Write(part); err != nil {
			return "error"
		}
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return "error"
	}
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		return "error"
	}
	reply := strings.ToLower(string(buf[:n]))
	if strings.Contains(reply, "found") {
		return "infected"
	}
	return "clean"
}

// makeThumb writes a ≤512px JPEG thumbnail next to the original.
func (m *Media) makeThumb(full string, item *domain.MediaItem) *string {
	if item.Width == nil || item.Height == nil {
		return nil
	}
	f, err := os.Open(full)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	const maxSide = 512
	w, h := *item.Width, *item.Height
	if w <= maxSide && h <= maxSide {
		return nil
	}
	scale := float64(maxSide) / float64(max(w, h))
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	// box-sample downsample (fast, dependency-free)
	for y := 0; y < nh; y++ {
		sy := y * h / nh
		for x := 0; x < nw; x++ {
			sx := x * w / nw
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	thumbRel := strings.TrimSuffix(item.Path, filepath.Ext(item.Path)) + "_thumb.jpg"
	thumbFull := filepath.Join(m.dir, thumbRel)
	out, err := os.Create(thumbFull)
	if err != nil {
		return nil
	}
	defer out.Close()
	if err := jpeg.Encode(out, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil
	}
	u := m.base + "/" + item.ID + "/thumb"
	return &u
}

func sniffMIME(data []byte) (string, error) {
	switch {
	case len(data) >= 3 && bytes.Equal(data[:3], []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg", nil
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", nil
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp", nil
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("%PDF")):
		return "application/pdf", nil
	case len(data) >= 2 && bytes.Equal(data[:2], []byte{0x47, 0x49}):
		return "image/gif", nil
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("PK\x03\x04")):
		return "application/zip", nil
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("OggS")):
		return "audio/ogg", nil
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")):
		return "audio/wav", nil
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}):
		// EBML container → webm (audio or video).
		if len(data) > 32 && containsSegmentType(data, "video") {
			return "video/webm", nil
		}
		return "audio/webm", nil
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		ft := string(data[8:12])
		switch ft {
		case "isom", "mp42", "avc1", "M4V ":
			return "video/mp4", nil
		case "M4A ":
			return "audio/mp4", nil
		}
		return "video/mp4", nil
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("ftyp")):
		return "video/mp4", nil
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("moov")):
		return "video/mp4", nil
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("qt  ")):
		return "video/quicktime", nil
	case len(data) >= 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0:
		return "audio/mpeg", nil
	}
	// Plain text falls back to a safe default only for chat_file.
	for _, b := range data[:min(len(data), 256)] {
		if b < 0x09 || (b > 0x0D && b < 0x20) {
			return "", fmt.Errorf("unsupported file type (jpg, png, webp, gif, pdf, zip, mp4, webm, mp3, ogg, wav, text)")
		}
	}
	return "text/plain", nil
}

// containsSegmentType looks for a "t"rack codec string inside the first KB of
// an EBML stream to distinguish audio-only from video webm.
func containsSegmentType(data []byte, want string) bool {
	hay := string(data[:min(len(data), 2048)])
	return strings.Contains(hay, want+"_") || strings.Contains(hay, want+" ")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func extensionFor(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	case "text/plain":
		return ".txt"
	case "audio/webm":
		return ".webm"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4":
		return ".m4a"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav":
		return ".wav"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	}
	return ".bin"
}
