package service

import (
	"bytes"
	"testing"

	"bizverse/api/internal/domain"
)

func TestSniffMIME(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}, "image/jpeg"},
		{"png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, "image/png"},
		{"webp", append([]byte("RIFF"), append([]byte{0, 0, 0, 0}, []byte("WEBP")...)...), "image/webp"},
		{"gif", []byte{0x47, 0x49, 0x46, 0x38}, "image/gif"},
		{"pdf", []byte("%PDF-1.7"), "application/pdf"},
		{"zip", []byte("PK\x03\x04rest"), "application/zip"},
		{"ogg", []byte("OggS\x00\x02"), "audio/ogg"},
		{"wav", append([]byte("RIFF"), append(make([]byte, 4), []byte("WAVE")...)...), "audio/wav"},
		{"mp4", append([]byte{0, 0, 0, 0x18}, []byte("ftypisom")...), "video/mp4"},
		{"mp3", []byte{0xFF, 0xFB, 0x90, 0x00}, "audio/mpeg"},
		{"text", []byte("hello plain text file\n"), "text/plain"},
		{"unknown", []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}, ""},
	}
	for _, c := range cases {
		got, err := sniffMIME(c.data)
		if c.want == "" {
			if err == nil {
				t.Errorf("%s: expected error, got %q", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMaxSizeFor(t *testing.T) {
	if got := maxSizeFor(domain.MediaChatVideo); got != 200<<20 {
		t.Errorf("video cap = %d, want 200MB", got)
	}
	if got := maxSizeFor(domain.MediaChatAudio); got != 25<<20 {
		t.Errorf("audio cap = %d, want 25MB", got)
	}
	if got := maxSizeFor(domain.MediaChatFile); got != 20<<20 {
		t.Errorf("file cap = %d, want 20MB", got)
	}
	if got := maxSizeFor(domain.MediaGallery); got != 10<<20 {
		t.Errorf("gallery cap = %d, want 10MB", got)
	}
}

func TestChatKindAllowlists(t *testing.T) {
	if !chatKinds[domain.MediaChatImage]["image/png"] {
		t.Error("chat_image should allow png")
	}
	if chatKinds[domain.MediaChatImage]["application/pdf"] {
		t.Error("chat_image must not allow pdf")
	}
	if !chatKinds[domain.MediaChatAudio]["audio/webm"] {
		t.Error("chat_audio should allow webm")
	}
	if !chatKinds[domain.MediaChatVideo]["video/mp4"] {
		t.Error("chat_video should allow mp4")
	}
}

func TestExtensionFor(t *testing.T) {
	cases := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "video/mp4": ".mp4",
		"audio/webm": ".webm", "application/pdf": ".pdf", "application/zip": ".zip",
		"text/plain": ".txt", "audio/mpeg": ".mp3",
	}
	for mime, want := range cases {
		if got := extensionFor(mime); got != want {
			t.Errorf("extensionFor(%q) = %q, want %q", mime, got, want)
		}
	}
}

var _ = bytes.MinRead
