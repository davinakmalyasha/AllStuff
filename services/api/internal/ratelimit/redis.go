package ratelimit

import (
	"crypto/sha1" // #nosec G505 -- Redis script cache key, not a security primitive
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bizverse/api/internal/redisclient"
)

// redisLimiter is a fixed-window limiter whose counters live in Redis, so every
// replica observes the same count. This is the difference between "3 replicas
// each allow 10 logins per 15 minutes" (30 effective) and an actual 10.
//
// Implementation notes that matter for correctness:
//
//   - The increment and the TTL are set in one Lua script. A bare INCR followed
//     by EXPIRE is not atomic: a crash between the two leaves a key with no
//     expiry, which permanently pins that key at count=1 and silently disables
//     the limit for that client for the life of the database.
//   - The script uses PEXPIRE with the window in milliseconds so a 15-minute
//     window is exact rather than rounded to whole seconds.
//   - Remaining/retryAfter come from the script's own return value, not from a
//     second round trip. A separate TTL read races with the next concurrent
//     request and reports a stale retry time to the client.
//   - On any Redis error the limiter fails *open* (allows the request) and
//     falls back to the in-memory limiter. Failing closed would turn a Redis
//     blip into a total site outage, and every caller here is a request-path
//     guard whose absence is survivable. The fallback also keeps the per-
//     instance bound meaningful during an incident.
type redisLimiter struct {
	pool     *connPool
	fallback RateLimiter
	logger   *slog.Logger

	// failOpen counts degraded decisions for /metrics and tests.
	failOpen atomic.Uint64
}

const rateLimitScript = `
local c = redis.call('INCR', KEYS[1])
if c == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
return {c, ttl}
`

// scriptSHA is the SHA1 of rateLimitScript. EVALSHA sends only this digest
// rather than the script body on every request. Computed with sha1 because
// Redis identifies scripts by SHA1 — this is a cache key, not a security
// boundary, and the digest is never used to authenticate anything.
var scriptSHA = sha1.Sum([]byte(rateLimitScript))

func scriptDigest() string { return hex.EncodeToString(scriptSHA[:]) }

// NewRedis returns a limiter backed by Redis, degrading to the in-memory
// limiter whenever Redis is unavailable. Passing a failing Redis URL therefore
// cannot take the API down: callers get per-instance limiting and a log line.
func NewRedis(redisURL string, logger *slog.Logger) (RateLimiter, error) {
	cfg, err := redisclient.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	pool := newConnPool(cfg, 8, logger)
	// Prime the script so the first real request does not pay NOSCRIPT.
	if err := pool.loadScript(rateLimitScript); err != nil {
		pool.Close()
		return nil, err
	}
	if logger != nil {
		logger.Info("rate limiting via redis", "addr", cfg.Addr, "tls", cfg.UseTLS)
	}
	return &redisLimiter{pool: pool, fallback: NewInMemory(), logger: logger}, nil
}

func (r *redisLimiter) Allow(key string, limit int, window time.Duration) (int, time.Duration, bool) {
	if limit <= 0 || window <= 0 {
		// A zero/negative limit or window is a misconfiguration, not a request
		// to enforce. Denying everything here would turn a bad config value
		// into a 429 for all traffic.
		return 0, 0, true
	}

	ms := window.Milliseconds()
	if ms <= 0 {
		ms = 1
	}

	conn, err := r.pool.get()
	if err != nil {
		r.degrade("acquire", err)
		return r.fallback.Allow(key, limit, window)
	}
	defer r.pool.put(conn)

	// The per-call budget is the deadline passed to Do; the limiter interface
	// takes no context, so a cancelled request still costs at most 500ms.
	reply, err := conn.Do(500*time.Millisecond,
		[]byte("EVALSHA"), []byte(scriptDigest()), []byte("1"),
		[]byte(redisKey(key, window)), []byte(strconv.FormatInt(ms, 10)))
	if err != nil {
		// NOSCRIPT means the script was flushed (SCRIPT FLUSH, or a failover to
		// a replica that had not cached it). Reload once and retry before
		// declaring the limiter degraded.
		if isNoScript(err) {
			if lerr := r.pool.loadScript(rateLimitScript); lerr == nil {
				if conn2, cerr := r.pool.get(); cerr == nil {
					defer r.pool.put(conn2)
					reply, err = conn2.Do(500*time.Millisecond,
						[]byte("EVALSHA"), []byte(scriptDigest()), []byte("1"),
						[]byte(redisKey(key, window)), []byte(strconv.FormatInt(ms, 10)))
					if err == nil {
						return parseCount(reply, limit, window)
					}
				}
			}
		}
		// A protocol or transport error leaves the connection's read buffer at
		// an unknown offset, so the connection must not be reused.
		r.pool.discard(conn)
		r.degrade("eval", err)
		return r.fallback.Allow(key, limit, window)
	}
	return parseCount(reply, limit, window)
}

func (r *redisLimiter) degrade(stage string, err error) {
	r.failOpen.Add(1)
	if r.logger != nil {
		r.logger.Warn("redis rate limiter degraded; using in-memory", "stage", stage, "err", err)
	}
}

// parseCount maps the script's {count, pttl_ms} reply onto the limiter
// contract. A count within the limit consumes one unit of remaining budget; a
// count past it reports the time left in the window.
func parseCount(reply any, limit int, window time.Duration) (int, time.Duration, bool) {
	arr, ok := reply.([]any)
	if !ok || len(arr) != 2 {
		// An unexpected reply shape is a protocol mismatch, not a denial.
		return 0, 0, true
	}
	count, ok1 := arr[0].(int)
	pttl, ok2 := arr[1].(int)
	if !ok1 || !ok2 {
		return 0, 0, true
	}
	if count > limit {
		retry := time.Duration(pttl) * time.Millisecond
		if retry <= 0 {
			// PTTL returned -1 (no expiry set) or -2 (key already gone). The
			// latter means the window elapsed between INCR and PTTL, so the
			// caller is not actually over budget; the former is a bug we must
			// not turn into a retry hint of 0 (which reads as "retry now").
			return limit, window, true
		}
		return 0, retry, false
	}
	return limit - count, 0, true
}

// isNoScript reports whether err is Redis's NOSCRIPT reply, which means the
// script cache lost our EVALSHA. redisclient surfaces server errors as
// fmt.Errorf("redis: %s", ...), so this matches on the prefix rather than on
// a sentinel that would require redisclient to know about this caller.
func isNoScript(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOSCRIPT")
}

// redisKey namespaces and digests the key. Hashing keeps user-controlled parts
// (an email address, an IP) from being visible in Redis keyspace listings or
// slow-log output, and bounds key length so a long composite key cannot blow
// past Redis's key size limit.
func redisKey(key string, window time.Duration) string {
	sum := sha256.Sum256([]byte(key))
	// Bucket by window so two tiers over the same logical key never share a
	// counter.
	return "bv:rl:" + hex.EncodeToString(sum[:16]) + ":" + strconv.FormatInt(int64(window/time.Second), 10)
}

// connPool is a fixed-size pool of Redis connections. A pool rather than one
// shared connection because Allow sits on the request path: serialising every
// request behind one socket would make Redis latency the API's p99.
type connPool struct {
	cfg      redisclient.Config
	size     int
	logger   *slog.Logger
	mu       sync.Mutex
	idle     []*redisclient.Conn
	closed   bool
	sem      chan struct{}
	initOnce sync.Once
}

func newConnPool(cfg redisclient.Config, size int, logger *slog.Logger) *connPool {
	return &connPool{cfg: cfg, size: size, logger: logger, sem: make(chan struct{}, size)}
}

func (p *connPool) acquireSem() bool {
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *connPool) get() (*redisclient.Conn, error) {
	if !p.acquireSem() {
		return nil, errors.New("ratelimit: all redis connections busy")
	}
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			p.releaseSem()
			return nil, errors.New("ratelimit: pool closed")
		}
		if n := len(p.idle); n > 0 {
			c := p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.mu.Unlock()
			return c, nil
		}
		p.mu.Unlock()
		c, err := redisclient.Dial(p.cfg)
		if err != nil {
			p.releaseSem()
			return nil, err
		}
		return c, nil
	}
}

func (p *connPool) put(c *redisclient.Conn) {
	if c == nil {
		p.releaseSem()
		return
	}
	p.mu.Lock()
	if p.closed || len(p.idle) >= p.size {
		p.mu.Unlock()
		c.Close()
		p.releaseSem()
		return
	}
	p.idle = append(p.idle, c)
	p.mu.Unlock()
	p.releaseSem()
}

// discard drops a connection whose stream position is no longer trustworthy.
func (p *connPool) discard(c *redisclient.Conn) {
	if c == nil {
		p.releaseSem()
		return
	}
	c.Close()
	p.releaseSem()
}

func (p *connPool) releaseSem() {
	select {
	case <-p.sem:
	default:
	}
}

func (p *connPool) loadScript(script string) error {
	conn, err := p.get()
	if err != nil {
		return err
	}
	defer p.put(conn)
	// SCRIPT LOAD returns the SHA1 as a bulk string.
	reply, err := conn.Do(5*time.Second, []byte("SCRIPT"), []byte("LOAD"), []byte(script))
	if err != nil {
		return err
	}
	if s, ok := reply.(string); ok && s != scriptDigest() {
		// The digest we precomputed disagrees with the server's. Falling back to
		// EVAL would be correct but doubles the payload; failing loudly is
		// better than silently shipping a broken sha1 forever.
		return errors.New("ratelimit: script digest mismatch")
	}
	return nil
}

func (p *connPool) Close() {
	p.mu.Lock()
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, c := range idle {
		c.Close()
	}
}
