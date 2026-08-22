package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func dial(t *testing.T, srv *httptest.Server, origin string) (*websocket.Conn, error) {
	t.Helper()
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if resp != nil {
		resp.Body.Close()
	}
	return conn, err
}

func TestHubOriginCheck(t *testing.T) {
	h := NewHub(nil, []string{"http://app.example.com"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Serve(w, r, "user-1")
	}))
	defer srv.Close()

	// Same-origin upgrade allowed.
	host := strings.TrimPrefix(srv.URL, "http://")
	c, err := dial(t, srv, "http://"+host)
	if err != nil {
		t.Fatalf("same-origin should connect: %v", err)
	}
	c.Close()

	// Allowlisted origin allowed.
	c, err = dial(t, srv, "http://app.example.com")
	if err != nil {
		t.Fatalf("allowlisted origin should connect: %v", err)
	}
	c.Close()

	// Unknown cross-origin denied.
	if _, err := dial(t, srv, "http://evil.example.com"); err == nil {
		t.Fatal("unknown cross-origin must be rejected (CSWSH)")
	}
}

func TestHubSendToUserAndCount(t *testing.T) {
	h := NewHub(nil, []string{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Serve(w, r, "user-1")
	}))
	defer srv.Close()

	c, err := dial(t, srv, "")
	if err != nil {
		t.Fatalf("no-origin (native client) should connect: %v", err)
	}
	defer c.Close()

	// Welcome frame first.
	var welcome Frame
	if err := c.ReadJSON(&welcome); err != nil {
		t.Fatalf("welcome frame: %v", err)
	}

	h.SendToUser("user-1", Frame{Type: "notification.new", Payload: map[string]any{"unread": 3}})
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var f Frame
	if err := c.ReadJSON(&f); err != nil {
		t.Fatalf("frame delivery: %v", err)
	}
	if f.Type != "notification.new" {
		t.Fatalf("got frame %q, want notification.new", f.Type)
	}
	if f.Payload.(map[string]any)["unread"].(float64) != 3 {
		t.Fatal("payload mismatch")
	}

	if h.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", h.Count())
	}
	// Unknown user: no crash.
	h.SendToUser("nobody", Frame{Type: "ping"})

	// Frame round-trips through JSON (server-side marshaling matches client).
	b, err := json.Marshal(Frame{Type: "notification.new", Payload: map[string]any{"unread": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"notification.new"`) {
		t.Fatal("frame serialization mismatch")
	}
}
