package realtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/api-commons/ws"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// flakyRegistry is a registry whose Publish can fail, or succeed without the
// subscription ever delivering (the shape of a Valkey subscription that died).
type flakyRegistry struct {
	*ws.MemoryRegistry
	failing atomic.Bool
	silent  atomic.Bool
}

func (r *flakyRegistry) Publish(ctx context.Context, key string, payload []byte) error {
	switch {
	case r.failing.Load():
		return errors.New("valkey down")
	case r.silent.Load():
		return nil // accepted, never delivered
	}
	return r.MemoryRegistry.Publish(ctx, key, payload)
}

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *manualClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestAHealthyFanoutIsNotDegraded(t *testing.T) {
	reg := &flakyRegistry{MemoryRegistry: ws.NewMemoryRegistry()}
	h := NewFanoutHealth(reg, 20*time.Millisecond, &Metrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)
	for deadline := time.Now().Add(250 * time.Millisecond); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if h.Degraded() {
			t.Fatal("a fan-out that echoes its probe every interval is reported degraded")
		}
	}
}

func TestAFanoutThatFailsToPublishIsDegradedAndRecovers(t *testing.T) {
	clock := &manualClock{t: testNow}
	reg := &flakyRegistry{MemoryRegistry: ws.NewMemoryRegistry()}
	m := &Metrics{}
	h := NewFanoutHealth(reg, 20*time.Millisecond, m).WithClock(clock.now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	reg.failing.Store(true)
	waitFor(t, "degraded after a publish error", h.Degraded)
	if m.PublishErrors() == 0 {
		t.Fatal("a failed probe publish was not counted")
	}
	reg.failing.Store(false)
	waitFor(t, "recovery", func() bool { return !h.Degraded() })
}

func TestAFanoutThatPublishesButNeverEchoesIsDegradedAfterThreeIntervals(t *testing.T) {
	clock := &manualClock{t: testNow}
	reg := &flakyRegistry{MemoryRegistry: ws.NewMemoryRegistry()}
	reg.silent.Store(true)
	h := NewFanoutHealth(reg, time.Second, &Metrics{}).WithClock(clock.now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	clock.advance(2 * time.Second)
	if h.Degraded() {
		t.Fatal("degraded before three intervals of silence")
	}
	clock.advance(2 * time.Second)
	if !h.Degraded() {
		t.Fatal("not degraded after four seconds without an echo")
	}
}

func TestARegistryThatCannotPublishIsNeverReportedDegraded(t *testing.T) {
	h := NewFanoutHealth(noPublish{}, time.Millisecond, nil)
	if h.Degraded() {
		t.Fatal("nothing to measure, so nothing to report")
	}
}

type noPublish struct{}

func (noPublish) Start(context.Context) error               { return nil }
func (noPublish) Stop(context.Context) error                { return nil }
func (noPublish) Register(string, string, ws.Conn)          {}
func (noPublish) Unregister(string, string)                 {}
func (noPublish) Broadcast(context.Context, string, []byte) {}

type toggle struct{ on atomic.Bool }

func (t *toggle) Degraded() bool { return t.on.Load() }

// pump reads a connection to its end on its own goroutine, so a test can wait on
// the frames with a timeout without ever reading again after a read deadline (a
// failed read is permanent on a WebSocket connection).
func pump(conn *fws.Conn) <-chan *wire.ServerFrame {
	out := make(chan *wire.ServerFrame, 1024)
	go func() {
		defer close(out)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var f wire.ServerFrame
			if proto.Unmarshal(raw, &f) == nil {
				out <- &f
			}
		}
	}()
	return out
}

func within(frames <-chan *wire.ServerFrame, d time.Duration) (*wire.ServerFrame, bool) {
	select {
	case f, ok := <-frames:
		return f, ok && f != nil
	case <-time.After(d):
		return nil, false
	}
}

func TestTheGatewayAsksClientsToPollWhileTheFanoutIsDownAndStopsWhenItRecovers(t *testing.T) {
	cfg := fastConfig()
	cfg.TickInterval = 60 * time.Millisecond
	cfg.DegradedPoll = 10 * time.Second
	m := &Metrics{}
	e := newGatewayWithHealth(t, cfg, &toggle{}, m)
	health := e.health.(*toggle)
	health.on.Store(true)

	frames := pump(e.open(t, &wire.Auth{Token: "u1"}))
	if f, ok := within(frames, 2*time.Second); !ok || f.GetReady() == nil {
		t.Fatalf("first frame = %+v, want Ready", f)
	}
	if f, ok := within(frames, 2*time.Second); !ok || f.GetHint().GetPollIntervalMs() != 10_000 {
		t.Fatalf("frame after Ready = %+v, want Hint poll 10000 ms", f)
	}
	hints := 0
	for deadline := time.Now().Add(400 * time.Millisecond); time.Now().Before(deadline); {
		if f, ok := within(frames, 50*time.Millisecond); ok && f.GetHint() != nil {
			hints++
		}
	}
	if hints < 2 {
		t.Fatalf("hints on the ticks = %d, want one on each", hints)
	}

	health.on.Store(false)
	// Hints already on their way (queued before the recovery) still arrive; read
	// past them, then none may follow.
	for deadline := time.Now().Add(250 * time.Millisecond); time.Now().Before(deadline); {
		within(frames, 20*time.Millisecond)
	}
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); {
		if f, ok := within(frames, 50*time.Millisecond); ok && f.GetHint() != nil {
			t.Fatal("a hint after the fan-out recovered")
		}
	}
}

func TestMetricsCountConnectionsByKindAndRenderOneLine(t *testing.T) {
	m := &Metrics{}
	m.connected(KindConsole)
	m.connected(KindPortal)
	m.connected(KindPortal)
	m.disconnected(KindPortal)
	m.authFailure("unauthorized")
	m.bye(ByeDraining)
	m.gap(GapCursorTooOld)
	m.replay(3)
	m.fallbackPoll()

	if m.Active() != 2 {
		t.Fatalf("active = %d, want 2", m.Active())
	}
	line, ok := m.line()
	if !ok {
		t.Fatal("no line")
	}
	for _, want := range []string{"connections_console=1", "connections_portal=1", "auth_failures.unauthorized=1", "bye.draining=1", "gap.cursor_too_old=1", "replays=1", "replayed_changes=3", "fallback_polls=1"} {
		if !contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	// A quiet interval logs nothing: the counters were reset by the report.
	if again, ok := m.line(); ok {
		t.Fatalf("counters were not reset after a report: %q", again)
	}
	if m.Active() != 2 {
		t.Fatal("the connection gauge is not reset by a report")
	}
}

func TestThePublishToWriteLatencyIsBucketed(t *testing.T) {
	m := &Metrics{}
	at := testNow
	frame := func(ms int64) []byte {
		b, _ := FrameOf(changeAt(at.Add(-time.Duration(ms) * time.Millisecond)))
		return b
	}
	m.observeWrite(frame(30), at)
	m.observeWrite(frame(400), at)
	m.observeWrite(frame(9000), at)
	count, mean, maxMs := m.Latency()
	if count != 3 || maxMs != 9000 || mean != (30+400+9000)/3 {
		t.Fatalf("count=%d mean=%d max=%d", count, mean, maxMs)
	}
	line, _ := m.line()
	for _, want := range []string{"write_latency_le_50=1", "write_latency_le_500=1", "write_latency_le_inf=1"} {
		if !contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	// A frame that is not a change is not measured.
	m.observeWrite(gapFrame(GapCursorTooOld), at)
	if c, _, _ := m.Latency(); c != 0 {
		t.Fatal("a Gap was counted as a write latency (the histogram was reset by the report)")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func changeAt(at time.Time) *repositories.Change {
	return &repositories.Change{ID: id.NewAt(at), Type: "invoice.paid", Entity: "invoice", EntityID: "in_1", OccurredAt: at}
}
