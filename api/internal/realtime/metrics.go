package realtime

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
)

// Metrics counts what the realtime path does (docs/specs/2026-10-14-realtime.md
// § 8). The counters live in the process and are reported as ONE structured log
// line a minute (see Report), not as CloudWatch custom metrics: every distinct
// series would be billed ($0.30 a month each), and the Poker service switched its
// EMF emission off for exactly that reason. The line is searchable in Logs
// Insights; the few alarms the rollout needs are metric filters on it.
//
// Every label comes from a bounded set (a principal kind, a reason): never an
// id.
type Metrics struct {
	mu       sync.Mutex
	counters map[string]int64 // "name" or "name|label"
	gauges   map[string]int64
	latency  histogram
}

// latencyBounds are the upper edges, in milliseconds, of the publish-to-write
// latency histogram; the last bucket is everything above.
var latencyBounds = []int64{50, 100, 250, 500, 1000, 2500, 5000}

type histogram struct {
	buckets [8]int64
	count   int64
	sum     int64
	max     int64
}

func (h *histogram) observe(ms int64) {
	i := 0
	for i < len(latencyBounds) && ms > latencyBounds[i] {
		i++
	}
	h.buckets[i]++
	h.count++
	h.sum += ms
	h.max = max(h.max, ms)
}

func (m *Metrics) add(name, label string, n int64) {
	if m == nil {
		return
	}
	key := name
	if label != "" {
		key += "|" + label
	}
	m.mu.Lock()
	if m.counters == nil {
		m.counters = map[string]int64{}
	}
	m.counters[key] += n
	m.mu.Unlock()
}

func (m *Metrics) gauge(name, label string, delta int64) {
	if m == nil {
		return
	}
	key := name + "|" + label
	m.mu.Lock()
	if m.gauges == nil {
		m.gauges = map[string]int64{}
	}
	m.gauges[key] += delta
	m.mu.Unlock()
}

// Count is a counter's value, for a name and an optional label.
func (m *Metrics) Count(name, label string) int64 {
	if m == nil {
		return 0
	}
	key := name
	if label != "" {
		key += "|" + label
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[key]
}

func (m *Metrics) connected(kind string)     { m.gauge("connections", kind, 1) }
func (m *Metrics) disconnected(kind string)  { m.gauge("connections", kind, -1) }
func (m *Metrics) authFailure(reason string) { m.add("auth_failures", reason, 1) }
func (m *Metrics) bye(reason string)         { m.add("bye", reason, 1) }
func (m *Metrics) gap(reason string)         { m.add("gap", reason, 1) }
func (m *Metrics) slowClose()                { m.add("slow_consumer_closes", "", 1) }
func (m *Metrics) publishError()             { m.add("publish_errors", "", 1) }
func (m *Metrics) fallbackPoll()             { m.add("fallback_polls", "", 1) }
func (m *Metrics) frameSent()                { m.add("frames_sent", "", 1) }
func (m *Metrics) replay(changes int) {
	m.add("replays", "", 1)
	m.add("replayed_changes", "", int64(changes))
}

// observeWrite records the time from a change happening to its frame reaching
// the socket, when the frame is a Change. Cheap: one small unmarshal on the
// connection's own writer goroutine, off the publisher's path.
func (m *Metrics) observeWrite(frame []byte, now time.Time) {
	if m == nil {
		return
	}
	var f wire.ServerFrame
	if proto.Unmarshal(frame, &f) != nil || f.GetChange() == nil || f.GetChange().GetOccurredAtMs() == 0 {
		return
	}
	ms := now.UnixMilli() - int64(f.GetChange().GetOccurredAtMs())
	if ms < 0 {
		ms = 0 // two clocks that disagree by a few ms is not a negative delay
	}
	m.mu.Lock()
	m.latency.observe(ms)
	m.mu.Unlock()
}

// Active is the connections currently registered, across kinds.
func (m *Metrics) Active() int64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, v := range m.gauges {
		n += v
	}
	return n
}

// SlowCloses, AuthFailures and PublishErrors are the headline counters.
func (m *Metrics) SlowCloses() int64    { return m.Count("slow_consumer_closes", "") }
func (m *Metrics) PublishErrors() int64 { return m.Count("publish_errors", "") }

// AuthFailures is the sum of every reason.
func (m *Metrics) AuthFailures() int64 {
	return m.Count("auth_failures", "unauthorized") + m.Count("auth_failures", "unavailable")
}

// Latency is the publish-to-write histogram so far: how many writes, their mean
// and maximum in milliseconds, and the share above each bound.
func (m *Metrics) Latency() (count, meanMs, maxMs int64) {
	if m == nil {
		return 0, 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latency.count == 0 {
		return 0, 0, 0
	}
	return m.latency.count, m.latency.sum / m.latency.count, m.latency.max
}

// line renders the counters since the last report as one stable key=value
// string, or "" when nothing happened.
func (m *Metrics) line() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.counters) == 0 && m.latency.count == 0 {
		return "", false
	}
	parts := make([]string, 0, len(m.counters)+len(m.gauges)+4)
	for k, v := range m.counters {
		parts = append(parts, strings.ReplaceAll(k, "|", ".")+"="+fmt.Sprint(v))
	}
	for k, v := range m.gauges {
		parts = append(parts, "connections_"+strings.TrimPrefix(k, "connections|")+"="+fmt.Sprint(v))
	}
	if m.latency.count > 0 {
		parts = append(parts,
			"write_latency_count="+fmt.Sprint(m.latency.count),
			"write_latency_mean_ms="+fmt.Sprint(m.latency.sum/m.latency.count),
			"write_latency_max_ms="+fmt.Sprint(m.latency.max))
		for i, n := range m.latency.buckets {
			edge := "inf"
			if i < len(latencyBounds) {
				edge = fmt.Sprint(latencyBounds[i])
			}
			if n > 0 {
				parts = append(parts, "write_latency_le_"+edge+"="+fmt.Sprint(n))
			}
		}
	}
	// Counters and the histogram are per interval; gauges are not reset.
	m.counters = map[string]int64{}
	m.latency = histogram{}
	sort.Strings(parts)
	return strings.Join(parts, " "), true
}

// Report logs one line every interval with what happened in it, until ctx ends.
// It logs nothing for a quiet minute.
func (m *Metrics) Report(ctx context.Context, interval time.Duration) {
	if m == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if line, ok := m.line(); ok {
				slog.InfoContext(ctx, "realtime stats", "stats", line)
			}
		}
	}
}
