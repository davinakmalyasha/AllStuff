//go:build !race

package main

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"allstuff/api/internal/config"
)

// newHTTPServer is the only place the listener limits live, so a silent edit
// here (or a silent stdlib default change) moves the header-flood bound with
// no test turning red. This pins the values AND proves them against a live
// socket: a >256KB header must never reach the handler.
func TestServerHeaderLimits(t *testing.T) {
	cfg := config.Config{Port: "0"}
	srv := newHTTPServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler reached with a header flood; the limit did not fire")
	}))
	if srv.MaxHeaderBytes != 256<<10 {
		t.Errorf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, 256<<10)
	}
	if srv.MaxHeaderValueCount != http.DefaultMaxHeaderValueCount {
		t.Errorf("MaxHeaderValueCount = %d, want stdlib default %d",
			srv.MaxHeaderValueCount, http.DefaultMaxHeaderValueCount)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// Raw socket: net/http client refuses to send some oversized requests
	// itself, which would test the client instead of the server.
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	flood := strings.Repeat("a", 300<<10)
	_, _ = io.WriteString(conn, "GET /api/v1/health HTTP/1.1\r\nHost: x\r\nX-Flood: "+flood+"\r\n\r\n")
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	got := string(buf[:n])
	if strings.Contains(got, " 200 ") {
		t.Errorf("header flood got a 200; want rejection (431 or closed connection), got %q", got)
	}
}
