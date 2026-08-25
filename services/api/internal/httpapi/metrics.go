package httpapi

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"bizverse/api/internal/util"
)

// Metrics — minimal Prometheus text-format registry (PRD §5.8.6 ops depth).
type Metrics struct {
	requestsTotal atomic.Uint64
	byPath        syncMap // path -> status -> count
	errorsTotal   atomic.Uint64
	rateLimited   atomic.Uint64
}

type syncMap struct {
	mu    sync.Mutex
	items map[string]*atomic.Uint64
}

// maxSeries bounds metric cardinality: unauthenticated traffic previously
// minted a permanent label per arbitrary slug/404 path — slow memory DoS.
const maxSeries = 512

func (m *syncMap) get(key string) *atomic.Uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[string]*atomic.Uint64{}
	}
	c, ok := m.items[key]
	if !ok {
		if len(m.items) >= maxSeries {
			if ov, exists := m.items["|overflow"]; exists {
				return ov
			}
			c = &atomic.Uint64{}
			m.items["|overflow"] = c
			return c
		}
		c = &atomic.Uint64{}
		m.items[key] = c
	}
	return c
}

func (m *syncMap) snapshot() map[string]uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]uint64{}
	for k, v := range m.items {
		out[k] = v.Load()
	}
	return out
}

func (m *Metrics) Observe(method, path string, status int) {
	m.requestsTotal.Add(1)
	key := fmt.Sprintf("%s %s", method, normalizePath(path))
	m.byPath.get(key + "|" + util.Itoa(status)).Add(1)
	if status >= 500 {
		m.errorsTotal.Add(1)
	}
}

// normalizePath collapses numeric and UUID-like segments so every request to
// the same route shape shares one metric series.
func normalizePath(p string) string {
	segs := strings.Split(p, "/")
	changed := false
	for i, seg := range segs {
		if seg == "" || len(seg) > 64 {
			continue
		}
		if isNumericOrID(seg) {
			segs[i] = ":id"
			changed = true
		}
	}
	if !changed {
		return p
	}
	return strings.Join(segs, "/")
}

func isNumericOrID(s string) bool {
	numeric := true
	hexish := strings.Count(s, "-") == 4 && len(s) == 36
	for _, r := range s {
		if r < '0' || r > '9' {
			numeric = false
			break
		}
	}
	if numeric {
		return true
	}
	if hexish {
		for _, r := range s {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r == '-') {
				return false
			}
		}
		return true
	}
	return false
}

func (m *Metrics) RateLimited() { m.rateLimited.Add(1) }

// Render produces Prometheus text format.
func (m *Metrics) Render(wsConns int, lastTrend string) string {
	var b strings.Builder
	b.WriteString("# HELP bizverse_requests_total Total HTTP requests.\n")
	b.WriteString("# TYPE bizverse_requests_total counter\n")
	b.WriteString(fmt.Sprintf("bizverse_requests_total %d\n", m.requestsTotal.Load()))
	b.WriteString("# HELP bizverse_http_requests_by_path Requests by method+path+status.\n")
	b.WriteString("# TYPE bizverse_http_requests_by_path counter\n")
	items := m.byPath.snapshot()
	keys := make([]string, 0, len(items))
	for k := range items {
		if k == "|overflow" {
			continue // rendered separately below
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.SplitN(k, "|", 2)
		if len(parts) != 2 {
			continue
		}
		label := parts[0]
		b.WriteString(fmt.Sprintf("bizverse_http_requests_by_path{route=%q,status=%s} %d\n",
			label, parts[1], items[k]))
	}
	if ov, ok := items["|overflow"]; ok {
		b.WriteString(fmt.Sprintf("bizverse_http_requests_by_path{route=\"overflow\",status=\"\"} %d\n", ov))
	}
	b.WriteString("# HELP bizverse_errors_total 5xx responses.\n")
	b.WriteString("# TYPE bizverse_errors_total counter\n")
	b.WriteString(fmt.Sprintf("bizverse_errors_total %d\n", m.errorsTotal.Load()))
	b.WriteString("# HELP bizverse_rate_limited_total Rate-limited requests.\n")
	b.WriteString("# TYPE bizverse_rate_limited_total counter\n")
	b.WriteString(fmt.Sprintf("bizverse_rate_limited_total %d\n", m.rateLimited.Load()))
	b.WriteString("# HELP bizverse_ws_connections Live WebSocket connections.\n")
	b.WriteString("# TYPE bizverse_ws_connections gauge\n")
	b.WriteString(fmt.Sprintf("bizverse_ws_connections %d\n", wsConns))
	b.WriteString("# HELP bizverse_goroutines Current goroutines.\n")
	b.WriteString("# TYPE bizverse_goroutines gauge\n")
	b.WriteString(fmt.Sprintf("bizverse_goroutines %d\n", runtime.NumGoroutine()))
	if lastTrend != "" {
		b.WriteString("# HELP bizverse_last_trend_run Last trending recompute.\n")
		b.WriteString("# TYPE bizverse_last_trend_run gauge\n")
		b.WriteString(fmt.Sprintf("bizverse_last_trend_run %s\n", lastTrend))
	}
	return b.String()
}
