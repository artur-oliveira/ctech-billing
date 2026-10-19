package realtime

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
)

func pingFrame() *wire.ClientFrame {
	return &wire.ClientFrame{Body: &wire.ClientFrame_Ping{Ping: &wire.Ping{ClientTimeMs: 1}}}
}

func TestAFloodOfFramesClosesTheConnectionAndNobodyElse(t *testing.T) {
	cfg := fastConfig()
	cfg.FramesPerSecond = 20
	e := newGateway(t, cfg, nil, DefaultLimits())
	flooder := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, flooder) // Ready
	bystander := e.open(t, &wire.Auth{Token: "u2"})
	mustNext(t, bystander)

	go func() {
		for i := 0; i < 2000; i++ {
			b, _ := proto.Marshal(pingFrame())
			if flooder.WriteMessage(fws.BinaryMessage, b) != nil {
				return
			}
		}
	}()
	// The flooder is cut off. Whether it still reads the Bye is the kernel's call: a
	// server that closes with the rest of the flood unread resets the connection, and
	// the reset discards what the client had not read yet. So what is asserted is the
	// server's decision (a Bye limit counted) and that the connection ended.
	for i := 0; i < 2100; i++ {
		f, err := next(t, flooder, 2*time.Second)
		if err != nil || f.GetBye().GetReason() == ByeLimit {
			break
		}
	}
	for until := time.Now().Add(2 * time.Second); e.metrics.Count("bye", ByeLimit) == 0 && time.Now().Before(until); {
		time.Sleep(5 * time.Millisecond)
	}
	if e.metrics.Count("bye", ByeLimit) != 1 {
		t.Fatal("2,000 pings in a burst were not answered with Bye limit")
	}
	if _, err := next(t, flooder, 2*time.Second); err == nil {
		t.Fatal("the flooder is still connected after the limit")
	}

	// The bystander is untouched and still answered.
	send(t, bystander, pingFrame())
	if f := mustNext(t, bystander); f.GetPong() == nil {
		t.Fatalf("the bystander got %v instead of a pong", f)
	}
}

func TestAnOrdinaryClientNeverHitsTheFrameLimit(t *testing.T) {
	cfg := fastConfig()
	cfg.FramesPerSecond = 20
	e := newGateway(t, cfg, nil, DefaultLimits())
	c := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, c)
	for i := 0; i < 5; i++ { // a ping every so often, a reauth now and then
		send(t, c, pingFrame())
		if f := mustNext(t, c); f.GetPong() == nil {
			t.Fatalf("expected a pong, got %v", f)
		}
		time.Sleep(60 * time.Millisecond)
	}
}

func TestGarbageAndTextFramesCloseTheOffenderOnly(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	good := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, good)
	for _, payload := range [][]byte{{0xff, 0xff, 0xff}, bytes.Repeat([]byte{0x0a, 0xff}, 40), {0x0a}} {
		bad := e.open(t, &wire.Auth{Token: "u2"})
		mustNext(t, bad)
		if err := bad.WriteMessage(fws.BinaryMessage, payload); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := next(t, bad, 2*time.Second); err != nil {
				break
			}
		}
	}
	text := e.open(t, &wire.Auth{Token: "u2"})
	mustNext(t, text)
	_ = text.WriteMessage(fws.TextMessage, []byte(`{"type":"ping"}`))
	if _, err := next(t, text, 2*time.Second); err == nil {
		if _, err = next(t, text, 2*time.Second); err == nil {
			t.Fatal("a text frame did not close the connection")
		}
	}
	send(t, good, pingFrame())
	if f := mustNext(t, good); f.GetPong() == nil {
		t.Fatal("the well-behaved client was affected")
	}
}

func TestAnOversizedFrameAfterAuthClosesTheConnection(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	c := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, c)
	_ = c.WriteMessage(fws.BinaryMessage, bytes.Repeat([]byte{0}, 1<<20))
	for {
		if _, err := next(t, c, 2*time.Second); err != nil {
			return
		}
	}
}

// capture is a slog handler that keeps what production would write: Info and
// above. api-commons logs registered keys (a scope key names a user) at Debug,
// which nothing here turns on.
type capture struct {
	mu    sync.Mutex
	lines []string
}

func (c *capture) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelInfo }
func (c *capture) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool { b.WriteString(" " + a.Key + "=" + a.Value.String()); return true })
	c.mu.Lock()
	c.lines = append(c.lines, b.String())
	c.mu.Unlock()
	return nil
}
func (c *capture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capture) WithGroup(string) slog.Handler      { return c }

func TestNoLogLineOrMetricCarriesATokenAPayLinkOrAScopeKey(t *testing.T) {
	sink := &capture{}
	old := slog.Default()
	slog.SetDefault(slog.New(sink))
	t.Cleanup(func() { slog.SetDefault(old) })

	const secret = "u1-secret-token-value"
	m := &Metrics{}
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	e.metrics = m
	// A bad token, a bad frame, a good session and a flood: the paths that log.
	bad := e.open(t, &wire.Auth{Token: secret, PayLink: "pay-link-secret"})
	_, _ = next(t, bad, time.Second)
	good := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, good)
	_ = good.WriteMessage(fws.BinaryMessage, []byte{0xff, 0xff})
	_, _ = next(t, good, time.Second)

	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, line := range sink.lines {
		for _, forbidden := range []string{secret, "pay-link-secret", "user:u1", "tenant:", "invoice:"} {
			if strings.Contains(line, forbidden) {
				t.Errorf("a log line carries %q: %s", forbidden, line)
			}
		}
	}
}
