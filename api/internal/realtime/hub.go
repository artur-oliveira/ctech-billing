package realtime

import (
	"errors"
	"sync"
	"time"

	"gopkg.aoctech.app/api-commons/ws"
)

// Limits are the caps and sizes of a hub (spec § 4.4).
type Limits struct {
	// PerPrincipal is how many connections one person (or one pay link) may hold.
	PerPrincipal int
	// PerIP is how many one address may hold across principals.
	PerIP int
	// QueueSize is the frames a connection may have waiting before it is closed
	// as a slow consumer.
	QueueSize int
	// WriteWait bounds one socket write.
	WriteWait time.Duration
}

// DefaultLimits are the production values.
func DefaultLimits() Limits {
	return Limits{PerPrincipal: 5, PerIP: 30, QueueSize: 256, WriteWait: 5 * time.Second}
}

// ErrLimit is a connection refused because its principal or its address holds as
// many as it may.
var ErrLimit = errors.New("realtime: connection limit reached")

// Hub keeps the live connections of this instance and registers each under the
// scopes its principal owns. A published change reaches them through the
// registry (ws.Registry, Valkey across instances), one broadcast per scope.
type Hub struct {
	reg     ws.Registry
	limits  Limits
	metrics *Metrics

	mu          sync.Mutex
	conns       map[string]*Conn
	scopesOf    map[string][]string
	byPrincipal map[string]int
	byIP        map[string]int
}

func NewHub(reg ws.Registry, lim Limits, m *Metrics) *Hub {
	return &Hub{
		reg: reg, limits: lim, metrics: m,
		conns: map[string]*Conn{}, scopesOf: map[string][]string{},
		byPrincipal: map[string]int{}, byIP: map[string]int{},
	}
}

// NewConn builds a connection with this hub's sizes.
func (h *Hub) NewConn(id string, p *Principal, ip string, sock Socket) *Conn {
	return NewConn(id, p, ip, sock, h.limits.QueueSize, h.limits.WriteWait, h.metrics)
}

// principalKey is what the per-principal cap counts. A checkout link has no
// subject, so it counts by the invoice it opens.
func principalKey(p *Principal) string {
	if p.Sub != "" {
		return p.Kind + ":" + p.Sub
	}
	if len(p.Scopes) > 0 {
		return p.Kind + ":" + p.Scopes[0]
	}
	return p.Kind
}

// Add registers the connection under every scope. ErrLimit when its principal or
// its address is at the cap; nothing is registered then.
func (h *Hub) Add(c *Conn, scopes []string) error {
	key := principalKey(c.Principal)
	h.mu.Lock()
	if h.limits.PerPrincipal > 0 && h.byPrincipal[key] >= h.limits.PerPrincipal {
		h.mu.Unlock()
		return ErrLimit
	}
	if h.limits.PerIP > 0 && c.IP != "" && h.byIP[c.IP] >= h.limits.PerIP {
		h.mu.Unlock()
		return ErrLimit
	}
	h.conns[c.ID] = c
	h.scopesOf[c.ID] = append([]string(nil), scopes...)
	h.byPrincipal[key]++
	if c.IP != "" {
		h.byIP[c.IP]++
	}
	h.mu.Unlock()

	for _, s := range scopes {
		h.reg.Register(s, c.ID, c)
	}
	h.metrics.connected(c.Principal.Kind)
	return nil
}

// Remove unregisters the connection from every scope and releases its caps.
// Calling it twice is harmless.
func (h *Hub) Remove(c *Conn) {
	h.mu.Lock()
	scopes, ok := h.scopesOf[c.ID]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.conns, c.ID)
	delete(h.scopesOf, c.ID)
	key := principalKey(c.Principal)
	if h.byPrincipal[key]--; h.byPrincipal[key] <= 0 {
		delete(h.byPrincipal, key)
	}
	if c.IP != "" {
		if h.byIP[c.IP]--; h.byIP[c.IP] <= 0 {
			delete(h.byIP, c.IP)
		}
	}
	h.mu.Unlock()

	for _, s := range scopes {
		h.reg.Unregister(s, c.ID)
	}
	h.metrics.disconnected(c.Principal.Kind)
}

// Count is the connections this instance holds.
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// Drain tells every connection the server is going away, then lets each close.
// retryAfter is the delay a client should wait before it reconnects.
func (h *Hub) Drain(retryAfter time.Duration) {
	h.mu.Lock()
	all := make([]*Conn, 0, len(h.conns))
	for _, c := range h.conns {
		all = append(all, c)
	}
	h.mu.Unlock()
	bye := ByeFrame(ByeDraining, retryAfter)
	for _, c := range all {
		c.Finish(bye, CloseGoingAway, ByeDraining)
	}
}
