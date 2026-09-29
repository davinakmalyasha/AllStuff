// Package redisclient is a minimal, dependency-free RESP client shared by the
// WS fan-out and the distributed rate limiter.
//
// It exists so the wire protocol is implemented once. The alternative — a
// second hand-rolled encoder inside the rate limiter — would mean two
// implementations of bulk-length framing, CRLF termination and reply parsing to
// keep in sync, and a bug in either is a Redis-corruption or auth-bypass class
// failure rather than a compile error.
//
// Scope is deliberately narrow: AUTH/SELECT, EVALSHA, PING and connection
// health. No pipelining, no MULTI/EXEC, no cluster or sentinel support, and no
// client-side caching. It speaks redis:// and rediss:// (TLS, which every
// managed provider requires and which config.Validate enforces in production).
package redisclient

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config is the subset of a redis:// URL this client honours.
type Config struct {
	Addr     string
	Password string
	DB       int
	UseTLS   bool
}

// ParseURL extracts the connection settings from a redis:// or rediss:// URL.
func ParseURL(redisURL string) (Config, error) {
	u, err := url.Parse(redisURL)
	if err != nil {
		return Config{}, err
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return Config{}, fmt.Errorf("redisclient: unsupported scheme %q (want redis:// or rediss://)", u.Scheme)
	}
	cfg := Config{Addr: u.Host, UseTLS: strings.EqualFold(u.Scheme, "rediss")}
	if u.Port() == "" {
		cfg.Addr += ":6379"
	}
	if u.User != nil {
		cfg.Password, _ = u.User.Password()
	}
	if len(u.Path) > 1 {
		cfg.DB, _ = strconv.Atoi(strings.TrimPrefix(u.Path, "/"))
	}
	if cfg.Addr == "" || cfg.Addr == ":6379" {
		return Config{}, errors.New("redisclient: missing host in redis URL")
	}
	return cfg, nil
}

// Conn is a single authenticated connection. It is not safe for concurrent
// use; callers serialise access (the rate limiter pools one Conn per worker).
type Conn struct {
	netConn net.Conn
	br      *bufio.Reader
	cfg     Config
}

// Dial opens and authenticates a connection, applying cfg's TLS mode.
func Dial(cfg Config) (*Conn, error) {
	raw, err := net.DialTimeout("tcp", cfg.Addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	// Half-close the dial on a failed TLS/auth handshake: otherwise a
	// misconfigured URL leaks one socket per attempt, and the rate limiter
	// dials on every miss.
	if cfg.UseTLS {
		host, _, _ := net.SplitHostPort(cfg.Addr)
		raw = tls.Client(raw, &tls.Config{ServerName: host})
	}
	c := &Conn{netConn: raw, br: bufio.NewReader(raw), cfg: cfg}
	if err := c.handshake(); err != nil {
		raw.Close()
		return nil, err
	}
	return c, nil
}

// Close releases the connection.
func (c *Conn) Close() error { return c.netConn.Close() }

// handshake runs AUTH and SELECT. Both are best-effort in the sense that an
// empty password/DB 0 means the command is skipped entirely, but a command that
// is sent and rejected is a hard failure — a wrong password must not leave a
// connection that silently fails every later command.
func (c *Conn) handshake() error {
	if c.cfg.Password != "" {
		if _, err := c.do(5*time.Second, [][]byte{[]byte("AUTH"), []byte(c.cfg.Password)}); err != nil {
			return err
		}
	}
	if c.cfg.DB > 0 {
		if _, err := c.do(5*time.Second, [][]byte{[]byte("SELECT"), []byte(strconv.Itoa(c.cfg.DB))}); err != nil {
			return err
		}
	}
	return nil
}

// Do sends one command and returns its decoded reply.
func (c *Conn) Do(deadline time.Duration, args ...[]byte) (any, error) {
	return c.do(deadline, args)
}

func (c *Conn) do(deadline time.Duration, args [][]byte) (any, error) {
	if err := c.netConn.SetDeadline(time.Now().Add(deadline)); err != nil {
		return nil, err
	}
	if _, err := c.netConn.Write(Encode(args)); err != nil {
		return nil, err
	}
	return ReadReply(c.br)
}

// Ping verifies the connection is still usable. Used by the pool to detect a
// peer that dropped a half-open socket: a pooled connection idle beyond a
// provider's timeout looks writable until the first real write fails.
func (c *Conn) Ping() error {
	_, err := c.do(3*time.Second, [][]byte{[]byte("PING")})
	return err
}

// Encode renders args as a RESP array of bulk strings.
func Encode(args [][]byte) []byte {
	var b strings.Builder
	b.WriteString("*" + strconv.Itoa(len(args)) + "\r\n")
	for _, a := range args {
		b.WriteString("$" + strconv.Itoa(len(a)) + "\r\n")
		b.Write(a)
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// ReadReply parses one RESP reply: simple string, error, integer, bulk string,
// null, or array. Nil maps to the '-ERR' and null cases; use ReadIsNull to
// distinguish them where it matters.
func ReadReply(br *bufio.Reader) (any, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(line, "\r\n")
	if line == "" {
		return nil, errors.New("redisclient: empty resp line")
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case ':':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, fmt.Errorf("redisclient: bad integer %q", line)
		}
		return n, nil
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, fmt.Errorf("redisclient: bad bulk length %q", line)
		}
		if n < 0 {
			return nil, nil // null bulk string
		}
		buf := make([]byte, n+2)
		if _, err := readFull(br, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, fmt.Errorf("redisclient: bad array length %q", line)
		}
		if n < 0 {
			return nil, nil // null array
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			v, err := ReadReply(br)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case '-':
		return nil, fmt.Errorf("redis: %s", line[1:])
	default:
		return nil, fmt.Errorf("redisclient: unexpected resp prefix %q", line)
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
