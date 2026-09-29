package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"bizverse/api/internal/redisclient"
)

// errTOTPPoolBusy means every pooled connection is checked out. Callers treat
// it like any other store failure: log and allow, so a burst of concurrent
// logins cannot deny access.
var errTOTPPoolBusy = errors.New("totp guard: all redis connections busy")

// hashKey keeps user ids out of Redis keyspace, which is readable by anyone
// with cache access and appears in Redis slow logs.
func hashKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// totpReplayGuard enforces single-use TOTP codes. A 6-digit code stays valid
// for its whole ±1 step window (30-90s depending on the provider), so without a
// replay guard a phished code can be submitted repeatedly inside that window and
// each attempt mints a fresh session.
//
// Two implementations exist because the deployment shapes differ: a single
// instance needs no extra infrastructure, while 2-3 replicas need the decision
// to be shared or a captured code works once per replica.
type totpReplayGuard interface {
	// claim records the (user, timestep) pair and reports whether this was the
	// first use. It must be atomic.
	claim(ctx context.Context, userID string, step int64) (bool, error)
}

// localTOTPGuard is the process-local implementation (sync.Map with TTL
// pruning). Correct for a single instance only.
type localTOTPGuard struct {
	mu   sync.Mutex
	used map[string]time.Time
}

func newLocalTOTPGuard() *localTOTPGuard {
	return &localTOTPGuard{used: make(map[string]time.Time)}
}

func (g *localTOTPGuard) claim(_ context.Context, userID string, step int64) (bool, error) {
	key := userID + ":" + strconv.FormatInt(step, 10)
	now := time.Now()

	g.mu.Lock()
	defer g.mu.Unlock()

	// Prune first: expired steps can never be replayed again, so dropping them
	// cannot admit a replay. Bounded by only visiting entries older than the
	// cutoff, so this stays cheap under normal load.
	cutoff := now.Add(-usedTOTPStepTTL)
	for k, seen := range g.used {
		if seen.Before(cutoff) {
			delete(g.used, k)
		}
	}
	if _, dup := g.used[key]; dup {
		return false, nil
	}
	g.used[key] = now
	return true, nil
}

// redisTOTPGuard shares the claim across replicas via SET key 1 NX EX ttl. The
// TTL is deliberately the full step window rather than a fixed guess: a step
// that expires early re-admits its own code, and a TTL longer than the window
// only costs a slightly later reclaim.
type redisTOTPGuard struct {
	pool   *totpRedisPool
	logger *slog.Logger
}

func newRedisTOTPGuard(redisURL string, logger *slog.Logger) (*redisTOTPGuard, error) {
	cfg, err := redisclient.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	pool := newTOTPRedisPool(cfg, 4)
	// Prove the cache works now rather than at the first login, where a failure
	// would surface as a confusing auth error instead of a boot warning.
	if err := pool.verify(); err != nil {
		pool.Close()
		return nil, err
	}
	if logger != nil {
		logger.Info("totp replay guard via redis", "addr", cfg.Addr, "tls", cfg.UseTLS)
	}
	return &redisTOTPGuard{pool: pool, logger: logger}, nil
}

func (g *redisTOTPGuard) claim(_ context.Context, userID string, step int64) (bool, error) {
	// The key carries the user id, so this must be hashed for the same reason as
	// the rate limiter keys: Redis keyspace is readable by anyone with cache
	// access, and user ids are not a secret.
	key := "bv:totp:" + hashKey(userID) + ":" + strconv.FormatInt(step, 10)
	conn, err := g.pool.get()
	if err != nil {
		return false, err
	}
	defer g.pool.put(conn)

	// SET key 1 NX EX ttl replies nil when the key already existed, which is
	// exactly the replay case.
	reply, err := conn.Do(2*time.Second, []byte("SET"), []byte(key), []byte("1"),
		[]byte("NX"), []byte("EX"), []byte(strconv.FormatInt(int64(usedTOTPStepTTL/time.Second), 10)))
	if err != nil {
		g.pool.discard(conn)
		return false, err
	}
	// A nil reply means NX was not satisfied: the step was already consumed.
	return reply != nil, nil
}

// totpRedisPool is a small connection pool specialised for the auth path. It is
// separate from the rate limiter's pool so a burst of throttled login attempts
// cannot exhaust the connections 2FA verification needs.
type totpRedisPool struct {
	cfg    redisclient.Config
	mu     sync.Mutex
	idle   []*redisclient.Conn
	closed bool
	sem    chan struct{}
}

func newTOTPRedisPool(cfg redisclient.Config, size int) *totpRedisPool {
	return &totpRedisPool{cfg: cfg, sem: make(chan struct{}, size)}
}

func (p *totpRedisPool) verify() error {
	c, err := p.get()
	if err != nil {
		return err
	}
	defer p.put(c)
	return c.Ping()
}

func (p *totpRedisPool) get() (*redisclient.Conn, error) {
	select {
	case p.sem <- struct{}{}:
	default:
		return nil, errTOTPPoolBusy
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.release()
		return nil, errTOTPPoolBusy
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
		p.release()
		return nil, err
	}
	return c, nil
}

func (p *totpRedisPool) put(c *redisclient.Conn) {
	if c == nil {
		p.release()
		return
	}
	p.mu.Lock()
	if p.closed || len(p.idle) >= cap(p.sem) {
		p.mu.Unlock()
		c.Close()
		p.release()
		return
	}
	p.idle = append(p.idle, c)
	p.mu.Unlock()
	p.release()
}

func (p *totpRedisPool) discard(c *redisclient.Conn) {
	if c != nil {
		c.Close()
	}
	p.release()
}

func (p *totpRedisPool) release() {
	select {
	case <-p.sem:
	default:
	}
}

func (p *totpRedisPool) Close() {
	p.mu.Lock()
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, c := range idle {
		c.Close()
	}
}
