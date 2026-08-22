package ws

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RedisPubSub — minimal RESP pub/sub client for multi-instance WS fan-out
// (ARCHITECTURE §3, M5). Dependency-free: speaks the wire protocol directly.
// Without a configured REDIS_URL the hub stays single-instance (dev default).
type RedisPubSub struct {
	addr     string
	password string
	db       int
	channel  string
	logger   *slog.Logger
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
	return &RedisPubSub{addr: host, password: pass, db: db, channel: channel, logger: logger}, nil
}

// Publish sends a raw frame payload to the channel (fire-and-forget).
func (r *RedisPubSub) Publish(ctx context.Context, payload string) {
	conn, err := net.DialTimeout("tcp", r.addr, 5*time.Second)
	if err != nil {
		r.logger.Warn("redis publish dial", "err", err)
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(conn)
	if !r.handshake(conn, br) {
		return
	}
	args := [][]byte{[]byte("PUBLISH"), []byte(r.channel), []byte(payload)}
	if _, err := conn.Write(respArray(args)); err != nil {
		return
	}
	// Consume the integer reply so the connection closes cleanly.
	_, _ = br.ReadString('\n')
}

// Subscribe blocks reading the channel; every message is handed to onMsg.
func (r *RedisPubSub) Subscribe(ctx context.Context, onMsg func(string)) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := net.DialTimeout("tcp", r.addr, 5*time.Second)
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
		for {
			if ctx.Err() != nil {
				conn.Close()
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
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
		conn.Close()
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
