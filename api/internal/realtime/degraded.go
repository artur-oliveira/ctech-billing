package realtime

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"gopkg.aoctech.app/api-commons/ws"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

// healthKey is the fan-out key the probe uses; it carries no change and no
// connection of a client is ever registered under it.
const healthKeyPrefix = "realtime:health:"

// FanoutHealth answers one question for the gateway: does a change published on
// another instance still reach the connections of this one? It publishes a probe
// through the same registry every interval and waits to hear it come back through
// the subscription. The Redis registry's Broadcast falls back to local delivery
// when Valkey is down, which would hide the outage from a self-check, so the
// probe uses Publish, which never falls back: only a probe that made the round
// trip counts.
//
// When it has not (a publish error, or no echo for three intervals) the gateway
// tells its clients to poll as well (Hint), and the changes that were lost in the
// meantime are found again by the log.
type FanoutHealth struct {
	reg      ws.Registry
	pub      ws.Publisher
	key      string
	interval time.Duration
	metrics  *Metrics
	now      func() time.Time

	started  time.Time
	lastEcho atomic.Int64 // unix nanoseconds
	lastErr  atomic.Bool
}

// NewFanoutHealth builds the monitor. A registry that cannot Publish (a test
// double) is never reported degraded: there is nothing to measure.
func NewFanoutHealth(reg ws.Registry, interval time.Duration, m *Metrics) *FanoutHealth {
	h := &FanoutHealth{reg: reg, key: healthKeyPrefix + id.New(), interval: interval, metrics: m, now: time.Now}
	h.started = h.now()
	if p, ok := reg.(ws.Publisher); ok {
		h.pub = p
	}
	return h
}

// WithClock replaces the clock. Tests only.
func (h *FanoutHealth) WithClock(now func() time.Time) *FanoutHealth {
	h.now = now
	h.started = now()
	return h
}

// probeConn is what the registry delivers the probe to.
type probeConn struct{ h *FanoutHealth }

func (p probeConn) WriteMessage(int, []byte) error {
	p.h.lastEcho.Store(p.h.now().UnixNano())
	return nil
}

// Degraded is true when the round trip is not working.
func (h *FanoutHealth) Degraded() bool {
	if h == nil || h.pub == nil {
		return false
	}
	if h.lastErr.Load() {
		return true
	}
	last := time.Unix(0, h.lastEcho.Load())
	if h.lastEcho.Load() == 0 {
		last = h.started
	}
	return h.now().Sub(last) > 3*h.interval
}

// Run probes until ctx ends.
func (h *FanoutHealth) Run(ctx context.Context) {
	if h == nil || h.pub == nil {
		return
	}
	h.reg.Register(h.key, "probe", probeConn{h})
	defer h.reg.Unregister(h.key, "probe")
	h.probe(ctx)
	t := time.NewTicker(h.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.probe(ctx)
		}
	}
}

func (h *FanoutHealth) probe(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, h.interval)
	defer cancel()
	was := h.Degraded()
	if err := h.pub.Publish(pctx, h.key, []byte("probe")); err != nil {
		h.lastErr.Store(true)
		h.metrics.publishError()
		if !was {
			slog.WarnContext(ctx, "realtime: fan-out publish failing, clients are asked to poll", "error", err)
		}
		return
	}
	h.lastErr.Store(false)
	if was && !h.Degraded() {
		slog.InfoContext(ctx, "realtime: fan-out recovered")
	}
}
