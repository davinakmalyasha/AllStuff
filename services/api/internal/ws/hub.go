package ws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Hub — per-instance connection manager. Multi-instance fan-out via Redis
// pub/sub lands in M5 (ARCHITECTURE §3); the wire protocol is final.
type Hub struct {
	logger *slog.Logger

	// instanceID tags frames this process publishes so the Redis subscriber
	// can skip its own echoes (Redis delivers publishes back to the
	// publishing connection too — without this every frame amplifies).
	instanceID string

	mu sync.RWMutex
	// userID → live connections (multi-device: every tab/device gets frames).
	conns map[string]map[*conn]struct{}

	upgrader websocket.Upgrader

	// pubsub fans frames out to other instances (nil = single-instance dev).
	pubsub *RedisPubSub

	// authorizeSubscribe, when set, filters thread IDs a connection may
	// subscribe to (membership check). Nil = allow all (tests only).
	authorizeSubscribe func(userID string, threadIDs []string) []string
}

type conn struct {
	userID string
	ws     *websocket.Conn
	send   chan []byte
	done   chan struct{} // closed once by shutdown(); send is NEVER closed
	once   sync.Once

	mu   sync.Mutex
	subs map[string]bool // thread ids this connection opted into via `subscribe`
}

// subscribeThreads records the threads this connection wants signals for.
func (c *conn) subscribeThreads(ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subs == nil {
		c.subs = map[string]bool{}
	}
	for _, id := range ids {
		c.subs[id] = true
	}
}

func (c *conn) wants(threadID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subs[threadID]
}

// shutdown tears the connection down exactly once. c.send must not be
// closed: producers race with disconnect and a send on a closed channel
// panics; draining via done lets the garbage collector reclaim the buffer.
func (c *conn) shutdown() {
	c.once.Do(func() {
		close(c.done)
		_ = c.ws.Close()
	})
}

type Frame struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// NewHub builds the connection manager. allowedOrigins is the WS origin
// allowlist (same-origin host + CORS origins); an empty set denies all
// cross-origin upgrades (PRD §9.3 CSWSH protection). Pass a non-nil pubsub
// to enable multi-instance fan-out.
func NewHub(logger *slog.Logger, allowedOrigins []string) *Hub {
	return NewHubWithRedis(logger, allowedOrigins, nil)
}

// SetAuthorizeSubscribe installs the thread-membership filter used for
// `subscribe` frames (must be called before Serve traffic).
func (h *Hub) SetAuthorizeSubscribe(fn func(userID string, threadIDs []string) []string) {
	h.authorizeSubscribe = fn
}

// NewHubWithRedis wires optional Redis fan-out.
func NewHubWithRedis(logger *slog.Logger, allowedOrigins []string, pubsub *RedisPubSub) *Hub {
	allowed := map[string]bool{}
	for _, o := range allowedOrigins {
		if o = strings.TrimSpace(o); o != "" {
			allowed[o] = true
		}
	}
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	return &Hub{
		logger:     logger,
		instanceID: hex.EncodeToString(id),
		conns:      map[string]map[*conn]struct{}{},
		pubsub:     pubsub,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					// Non-browser clients (tests, native) send no Origin.
					return true
				}
				// Same-origin upgrades: host matches the Origin.
				if u, err := url.Parse(origin); err == nil {
					if u.Host == r.Host {
						return true
					}
				}
				return allowed[origin]
			},
		},
	}
}

// Run starts the hub. With Redis configured, a subscriber goroutine relays
// frames published by OTHER instances (ARCHITECTURE §3, M5 fan-out). Frames
// carrying this instance's own tag are dropped — Redis redelivers publishes
// to the publishing connection, which would otherwise loop exponentially.
func (h *Hub) Run(ctx context.Context) {
	if h.pubsub != nil {
		go h.pubsub.Subscribe(ctx, func(payload string) {
			// Envelope: {from, user_id, frame}
			var env struct {
				From   string `json:"from"`
				UserID string `json:"user_id"`
				Frame  Frame  `json:"frame"`
			}
			if err := json.Unmarshal([]byte(payload), &env); err != nil || env.UserID == "" {
				return
			}
			if env.From == h.instanceID {
				return // our own publish; locals were already delivered
			}
			h.deliverLocal(env.UserID, env.Frame)
		})
	}
	<-ctx.Done()
	h.mu.Lock()
	for id, set := range h.conns {
		for c := range set {
			c.shutdown()
		}
		delete(h.conns, id)
	}
	h.mu.Unlock()
}

// Serve upgrades and registers the connection. Multiple simultaneous
// connections per user are supported (tabs, phone, desktop).
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, userID string) {
	wsConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("ws upgrade", "err", err)
		return
	}
	c := &conn{userID: userID, ws: wsConn, send: make(chan []byte, 64), done: make(chan struct{})}
	h.mu.Lock()
	set := h.conns[userID]
	if set == nil {
		set = map[*conn]struct{}{}
		h.conns[userID] = set
	}
	set[c] = struct{}{}
	h.mu.Unlock()

	h.sendTo(c, Frame{Type: "welcome", Payload: map[string]any{"user_id": userID}})

	go h.writePump(c)
	go h.readPump(c)
}

func (h *Hub) readPump(c *conn) {
	defer h.disconnect(c)
	c.ws.SetReadLimit(4096)
	_ = c.ws.SetReadDeadline(time.Now().Add(70 * time.Second))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(70 * time.Second))
	})
	for {
		var f Frame
		if err := c.ws.ReadJSON(&f); err != nil {
			return
		}
		switch f.Type {
		case "ping":
			h.sendTo(c, Frame{ID: f.ID, Type: "pong"})
		case "subscribe":
			// Opt-in thread signals (typing) for this connection. IDs are
			// filtered through the membership check: without it any
			// authenticated user could subscribe to arbitrary threads and
			// receive presence signals for conversations they're not in.
			var p struct {
				ThreadIDs []string `json:"thread_ids"`
			}
			if f.Payload != nil {
				if raw, err := json.Marshal(f.Payload); err == nil {
					if json.Unmarshal(raw, &p) == nil && len(p.ThreadIDs) > 0 {
						if len(p.ThreadIDs) > 50 {
							p.ThreadIDs = p.ThreadIDs[:50]
						}
						if h.authorizeSubscribe != nil {
							p.ThreadIDs = h.authorizeSubscribe(c.userID, p.ThreadIDs)
						}
						if len(p.ThreadIDs) > 0 {
							c.subscribeThreads(p.ThreadIDs)
						}
					}
				}
			}
		}
	}
}

func (h *Hub) writePump(c *conn) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.shutdown()
	}()
	for {
		select {
		case msg := <-c.send:
			_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

func (h *Hub) disconnect(c *conn) {
	h.mu.Lock()
	if set, ok := h.conns[c.userID]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.conns, c.userID)
		}
	}
	h.mu.Unlock()
	c.shutdown()
}

func (h *Hub) sendTo(c *conn, f Frame) {
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default: // slow consumer — drop frame; client resyncs via REST
	}
}

// deliverLocal sends a frame to this instance's connections only.
func (h *Hub) deliverLocal(userID string, f Frame) {
	h.mu.RLock()
	conns := make([]*conn, 0, len(h.conns[userID]))
	for c := range h.conns[userID] {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		h.sendTo(c, f)
	}
}

// SendToUser delivers a frame to every live connection of the user
// (multi-device). With Redis configured it also publishes for other
// instances; the envelope carries this instance's tag so the publisher's own
// subscriber ignores the echo.
func (h *Hub) SendToUser(userID string, f Frame) {
	h.deliverLocal(userID, f)
	if h.pubsub != nil {
		env, err := json.Marshal(map[string]any{"from": h.instanceID, "user_id": userID, "frame": f})
		if err == nil {
			h.pubsub.Publish(context.Background(), string(env))
		}
	}
}

// DeliverTypingToThread sends a high-frequency signal only to connections
// that opted in via `subscribe` — no DB lookup, no cross-tab noise.
func (h *Hub) DeliverTypingToThread(threadID string, f Frame) int {
	h.mu.RLock()
	var targets []*conn
	for _, set := range h.conns {
		for c := range set {
			if c.wants(threadID) {
				targets = append(targets, c)
			}
		}
	}
	h.mu.RUnlock()
	for _, c := range targets {
		h.sendTo(c, f)
	}
	return len(targets)
}

// Count returns the number of live connections (health/observability).
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, set := range h.conns {
		n += len(set)
	}
	return n
}
