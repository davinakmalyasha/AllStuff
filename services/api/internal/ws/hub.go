package ws

import (
	"context"
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

	mu    sync.RWMutex
	conns map[string]*conn // userID -> conn (single per user per device; last wins)

	upgrader websocket.Upgrader

	// pubsub fans frames out to other instances (nil = single-instance dev).
	pubsub *RedisPubSub
}

type conn struct {
	userID string
	ws     *websocket.Conn
	send   chan []byte
	done   chan struct{} // closed once by shutdown(); send is NEVER closed
	once   sync.Once
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

// NewHubWithRedis wires optional Redis fan-out.
func NewHubWithRedis(logger *slog.Logger, allowedOrigins []string, pubsub *RedisPubSub) *Hub {
	allowed := map[string]bool{}
	for _, o := range allowedOrigins {
		if o = strings.TrimSpace(o); o != "" {
			allowed[o] = true
		}
	}
	return &Hub{
		logger: logger,
		conns:  map[string]*conn{},
		pubsub: pubsub,
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
// frames published by other instances (ARCHITECTURE §3, M5 fan-out).
func (h *Hub) Run(ctx context.Context) {
	if h.pubsub != nil {
		go h.pubsub.Subscribe(ctx, func(payload string) {
			var f Frame
			if err := json.Unmarshal([]byte(payload), &f); err != nil {
				return
			}
			// Envelope: {user_id, frame}
			var env struct {
				UserID string `json:"user_id"`
				Frame  Frame  `json:"frame"`
			}
			if err := json.Unmarshal([]byte(payload), &env); err != nil || env.UserID == "" {
				return
			}
			h.SendToUser(env.UserID, env.Frame)
		})
	}
	<-ctx.Done()
	h.mu.Lock()
	for id, c := range h.conns {
		c.shutdown()
		delete(h.conns, id)
	}
	h.mu.Unlock()
}

// Serve upgrades and registers the connection.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, userID string) {
	wsConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("ws upgrade", "err", err)
		return
	}
	c := &conn{userID: userID, ws: wsConn, send: make(chan []byte, 64), done: make(chan struct{})}
	h.mu.Lock()
	if old, ok := h.conns[userID]; ok {
		old.shutdown() // last wins (single-conn-per-user model)
	}
	h.conns[userID] = c
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
			// thread subscriptions land with M5; frame contract reserved.
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
	if h.conns[c.userID] == c {
		delete(h.conns, c.userID)
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

// SendToUser delivers a frame to a user if connected (idempotent, non-blocking).
// With Redis configured it also publishes for other instances.
func (h *Hub) SendToUser(userID string, f Frame) {
	h.mu.RLock()
	c, ok := h.conns[userID]
	h.mu.RUnlock()
	if ok {
		h.sendTo(c, f)
	}
	if h.pubsub != nil {
		env, err := json.Marshal(map[string]any{"user_id": userID, "frame": f})
		if err == nil {
			h.pubsub.Publish(context.Background(), string(env))
		}
	}
}

// Count returns the number of live connections (health/observability).
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}
