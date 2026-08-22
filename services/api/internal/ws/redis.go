package ws

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RedisPubSub — minimal RESP pub/sub client for multi-instance WS fan-out
// (ARCHITECTURE §3, M5). Dependency-free: speaks the wire protocol directly.
// Supports redis:// and rediss:// (TLS, required by most managed providers).
// Without a configured REDIS_URL the hub stays single-instance (dev default).
type RedisPubSub struct {
	addr     string
	password string
	db       int
	useTLS   bool
	channel  string
	logger   *slog.Logger

	mu      sync.Mutex
	pubConn net.Conn
	pubBr   *bufio.Reader
}

func NewRedisPubSub(redisURL, channel string, logger *slog.Logger) (*RedisPubSub, error) {
	u, err := url.Parse(redisURL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if u.Port() == "" {
		host += ":6379"
	}
	pass := ""
	if u.User != nil {
		pass, _ = u.User.Password()
	}
	db := 0
	if len(u.Path) > 1 {
		db, _ = strconv.Atoi(strings.TrimPrefix(u.Path, "/"))
	}
	return &RedisPubSub{
		addr: host, password: pass, db: db,
		useTLS:  strings.EqualFold(u.Scheme, "rediss"),
		channel: channel, logger: logger,
	}, nil
}

// dial opens a plain or TLS connection depending on the URL scheme.
func (r *RedisPubSub) dial() (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", r.addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if r.useTLS {
		host, _, _ := net.SplitHostPort(r.addr)
		conn = tls.Client(conn, &tls.Config{ServerName: host})
	}
	return conn, nil
}

// Publish sends a raw frame payload to the channel (fire-and-forget) over a
// persistent connection; failed publishes drop the conn so the next call
// redials.
func (r *RedisPubSub) Publish(_ context.Context, payload string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pubConn == nil {
		conn, err := r.dial()
		if err != nil {
			r.logger.Warn("redis publish dial", "err", err)
			return
		}
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		br := bufio.NewReader(conn)
		if !r.handshake(conn, br) {
			conn.Close()
			return
		}
		r.pubConn, r.pubBr = conn, br
	}
	args := [][]byte{[]byte("PUBLISH"), []byte(r.channel), []byte(payload)}
	_ = r.pubConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := r.pubConn.Write(respArray(args)); err != nil {
		r.dropPubLocked()
		return
	}
	// Consume the integer reply so buffered data never accumulates.
	_ = r.pubConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := readRESP(r.pubBr); err != nil {
		r.dropPubLocked()
	}
}

func (r *RedisPubSub) dropPubLocked() {
	if r.pubConn != nil {
		r.pubConn.Close()
		r.pubConn, r.pubBr = nil, nil
	}
}

// Subscribe blocks reading the channel; every message is handed to onMsg.
// A PING keepalive prevents idle pub/sub connections from being dropped by
// intermediary timeouts (frames published during a reconnect gap are lost —
// clients resync via REST).
func (r *RedisPubSub) Subscribe(ctx context.Context, onMsg func(string)) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := r.dial()
		if err != nil {
			r.logger.Warn("redis subscribe dial", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		br := bufio.NewReader(conn)
		if !r.handshake(conn, br) {
			conn.Close()
			continue
		}
		args := [][]byte{[]byte("SUBSCRIBE"), []byte(r.channel)}
		if _, err := conn.Write(respArray(args)); err != nil {
			conn.Close()
			continue
		}
		// First reply is the subscription confirmation.
		_, _ = readRESP(br)

		done := make(chan struct{})
		go keepalive(ctx, conn, done)

		for {
			// Long deadline only reaps half-dead peers; the keepalive PING
			// keeps healthy connections alive well past it.
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Minute))
			msg, err := readRESP(br)
			if err != nil {
				break // reconnect
			}
			if arr, ok := msg.([]any); ok && len(arr) == 3 {
				if kind, _ := arr[0].(string); kind == "message" {
					if payload, ok := arr[2].(string); ok {
						onMsg(payload)
					}
				}
			}
		}
		close(done)
		conn.Close()
	}
}

func keepalive(ctx context.Context, conn net.Conn, done <-chan struct{}) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	var wmu sync.Mutex
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			wmu.Lock()
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, err := conn.Write(respArray([][]byte{[]byte("PING")}))
			wmu.Unlock()
			if err != nil {
				conn.Close() // unblocks the reader → reconnect
				return
			}
		}
	}
}

// handshake: optional AUTH + SELECT; returns false on failure.
func (r *RedisPubSub) handshake(conn net.Conn, br *bufio.Reader) bool {
	if r.password != "" {
		args := [][]byte{[]byte("AUTH"), []byte(r.password)}
		if _, err := conn.Write(respArray(args)); err != nil {
			return false
		}
		rep, err := readRESP(br)
		if err != nil {
			return false
		}
		if s, ok := rep.(string); !ok || !strings.EqualFold(s, "OK") {
			return false
		}
	}
	if r.db > 0 {
		args := [][]byte{[]byte("SELECT"), []byte(strconv.Itoa(r.db))}
		if _, err := conn.Write(respArray(args)); err != nil {
			return false
		}
		rep, err := readRESP(br)
		if err != nil {
			return false
		}
		if s, ok := rep.(string); !ok || !strings.EqualFold(s, "OK") {
			return false
		}
	}
	return true
}

// respArray encodes args as a RESP array of bulk strings.
func respArray(args [][]byte) []byte {
	var b strings.Builder
	b.WriteString("*" + strconv.Itoa(len(args)) + "\r\n")
	for _, a := range args {
		b.WriteString("$" + strconv.Itoa(len(a)) + "\r\n")
		b.Write(a)
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// readRESP parses one RESP reply: int, bulk string, array, or error.
func readRESP(br *bufio.Reader) (any, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(line, "\r\n")
	if line == "" {
		return nil, fmt.Errorf("empty resp line")
	}
	switch line[0] {
	case ':':
		n, _ := strconv.Atoi(line[1:])
		return n, nil
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad bulk length %q", line)
		}
		buf := make([]byte, n+2)
		if _, err := readFull(br, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			v, err := readRESP(br)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case '-':
		return nil, fmt.Errorf("redis error: %s", line[1:])
	default:
		return nil, fmt.Errorf("unexpected resp prefix %q", line)
	}
}

func readFull(br *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := br.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
