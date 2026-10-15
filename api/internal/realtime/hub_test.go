package realtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/api-commons/ws"

	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
)

// fakeSocket records what it is given and can be made slow. It fails the test if
// two writes ever overlap, which is the one thing fasthttp/websocket forbids.
type fakeSocket struct {
	t        *testing.T
	delay    time.Duration // how long each write takes
	inflight atomic.Int32

	mu       sync.Mutex
	frames   []received
	closed   bool
	deadline time.Time
}

type received struct {
	kind int
	data []byte
	at   time.Time
}

func (f *fakeSocket) SetWriteDeadline(t time.Time) error {
	f.mu.Lock()
	f.deadline = t
	f.mu.Unlock()
	return nil
}

func (f *fakeSocket) WriteMessage(kind int, data []byte) error {
	if f.inflight.Add(1) != 1 {
		f.t.Error("two concurrent writes on one socket")
	}
	defer f.inflight.Add(-1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, received{kind: kind, data: append([]byte(nil), data...), at: time.Now()})
	return nil
}

func (f *fakeSocket) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeSocket) snapshot() []received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]received(nil), f.frames...)
}

func (f *fakeSocket) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeSocket) dataFrames() [][]byte {
	var out [][]byte
	for _, r := range f.snapshot() {
		if r.kind == msgBinary {
			out = append(out, r.data)
		}
	}
	return out
}

func testHub(lim Limits) (*Hub, *Metrics) {
	m := &Metrics{}
	return NewHub(ws.NewMemoryRegistry(), lim, m), m
}

func principal(kind, sub string, scopes ...string) *Principal {
	return &Principal{Kind: kind, Sub: sub, Scopes: scopes}
}

func newTestConn(t *testing.T, h *Hub, id, sub, ip string, sock *fakeSocket, scopes ...string) *Conn {
	t.Helper()
	c := h.NewConn(id, principal(KindPortal, sub, scopes...), ip, sock)
	if err := h.Add(c, scopes); err != nil {
		t.Fatalf("add %s: %v", id, err)
	}
	t.Cleanup(func() { h.Remove(c); c.Finish(nil, CloseNormal, ""); <-c.Done() })
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestABroadcastReachesExactlyTheConnectionsUnderItsScope(t *testing.T) {
	h, _ := testHub(DefaultLimits())
	a, b, c := &fakeSocket{t: t}, &fakeSocket{t: t}, &fakeSocket{t: t}
	newTestConn(t, h, "c1", "u1", "1.1.1.1", a, "user:u1")
	newTestConn(t, h, "c2", "u1", "1.1.1.2", b, "user:u1", "tenant:o:live")
	newTestConn(t, h, "c3", "u2", "1.1.1.3", c, "user:u2")

	h.reg.Broadcast(context.Background(), "user:u1", []byte("for-u1"))
	h.reg.Broadcast(context.Background(), "tenant:o:live", []byte("for-tenant"))

	waitFor(t, "u1's two connections", func() bool { return len(a.dataFrames()) == 1 && len(b.dataFrames()) == 2 })
	time.Sleep(30 * time.Millisecond)
	if len(c.dataFrames()) != 0 {
		t.Fatalf("a connection under another scope received %d frames", len(c.dataFrames()))
	}
	if string(a.dataFrames()[0]) != "for-u1" || string(b.dataFrames()[1]) != "for-tenant" {
		t.Fatalf("frames: a=%q b=%q", a.dataFrames(), b.dataFrames())
	}
}

func TestASlowConsumerIsClosedWithAGapAndNeverHoldsUpAnother(t *testing.T) {
	lim := DefaultLimits()
	lim.QueueSize = 8
	h, m := testHub(lim)
	slow := &fakeSocket{t: t, delay: 40 * time.Millisecond}
	fast := &fakeSocket{t: t}
	sc := newTestConn(t, h, "slow", "u1", "1.1.1.1", slow, "tenant:o:live")
	newTestConn(t, h, "fast", "u2", "1.1.1.2", fast, "tenant:o:live")

	const frames = 200
	started := time.Now()
	for i := 0; i < frames; i++ {
		h.reg.Broadcast(context.Background(), "tenant:o:live", []byte(fmt.Sprintf("f%03d", i)))
		time.Sleep(time.Millisecond) // a steady producer: the fast reader keeps up, the slow one cannot
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("publishing %d frames took %s: a slow reader held the publisher", frames, took)
	}

	waitFor(t, "the fast connection to receive everything", func() bool { return len(fast.dataFrames()) == frames })
	waitFor(t, "the slow connection to be closed", func() bool { return slow.isClosed() })

	select {
	case <-sc.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the slow connection never finished")
	}
	last := slow.dataFrames()[len(slow.dataFrames())-1]
	var f wire.ServerFrame
	if err := proto.Unmarshal(last, &f); err != nil || f.GetGap().GetReason() != GapSlowConsumer {
		t.Fatalf("the slow connection's last frame = %+v (%v), want Gap slow_consumer", f.GetGap(), err)
	}
	if m.SlowCloses() != 1 {
		t.Fatalf("slow closes = %d, want 1", m.SlowCloses())
	}
	if len(slow.dataFrames()) >= frames {
		t.Fatalf("a slow reader was sent everything (%d frames): the queue was not bounded", len(slow.dataFrames()))
	}
}

func TestWritesToOneConnectionAreNeverConcurrent(t *testing.T) {
	h, _ := testHub(DefaultLimits())
	sock := &fakeSocket{t: t, delay: time.Millisecond}
	c := newTestConn(t, h, "c", "u", "1.1.1.1", sock, "user:u")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				c.Send([]byte("x"))
			}
		}()
	}
	wg.Wait()
	waitFor(t, "the writes", func() bool { return len(sock.dataFrames()) > 0 })
	// The fake socket fails the test itself if two writes overlapped.
}

func TestTheSixthConnectionOfOnePrincipalAndThe31stOfOneIPAreRefused(t *testing.T) {
	h, _ := testHub(DefaultLimits())
	for i := 0; i < 5; i++ {
		newTestConn(t, h, fmt.Sprintf("p%d", i), "same-user", fmt.Sprintf("2.2.2.%d", i), &fakeSocket{t: t}, "user:same-user")
	}
	sixth := h.NewConn("p5", principal(KindPortal, "same-user", "user:same-user"), "2.2.2.9", &fakeSocket{t: t})
	defer func() { sixth.Finish(nil, CloseNormal, ""); <-sixth.Done() }()
	if err := h.Add(sixth, []string{"user:same-user"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("sixth of one principal: %v, want ErrLimit", err)
	}

	for i := 0; i < 30; i++ {
		newTestConn(t, h, fmt.Sprintf("ip%d", i), fmt.Sprintf("user-%d", i), "9.9.9.9", &fakeSocket{t: t}, fmt.Sprintf("user:user-%d", i))
	}
	extra := h.NewConn("ip30", principal(KindPortal, "someone-else", "user:someone-else"), "9.9.9.9", &fakeSocket{t: t})
	defer func() { extra.Finish(nil, CloseNormal, ""); <-extra.Done() }()
	if err := h.Add(extra, []string{"user:someone-else"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("31st of one IP: %v, want ErrLimit", err)
	}
}

func TestARemovedConnectionFreesItsCapAndStopsReceiving(t *testing.T) {
	h, m := testHub(Limits{PerPrincipal: 1, PerIP: 5, QueueSize: 8, WriteWait: time.Second})
	sock := &fakeSocket{t: t}
	c := h.NewConn("c1", principal(KindPortal, "u", "user:u"), "1.1.1.1", sock)
	if err := h.Add(c, []string{"user:u"}); err != nil {
		t.Fatal(err)
	}
	h.Remove(c)
	h.Remove(c) // harmless twice
	c.Finish(nil, CloseNormal, "")
	<-c.Done()
	if h.Count() != 0 || m.Active() != 0 {
		t.Fatalf("count=%d active=%d after remove", h.Count(), m.Active())
	}
	h.reg.Broadcast(context.Background(), "user:u", []byte("late"))
	time.Sleep(20 * time.Millisecond)
	if len(sock.dataFrames()) != 0 {
		t.Fatal("a removed connection still received a broadcast")
	}
	again := h.NewConn("c2", principal(KindPortal, "u", "user:u"), "1.1.1.1", &fakeSocket{t: t})
	defer func() { again.Finish(nil, CloseNormal, ""); <-again.Done() }()
	if err := h.Add(again, []string{"user:u"}); err != nil {
		t.Fatalf("the cap was not released: %v", err)
	}
}

func TestDrainSaysByeThenClosesEveryConnection(t *testing.T) {
	h, _ := testHub(DefaultLimits())
	a, b := &fakeSocket{t: t}, &fakeSocket{t: t}
	ca := newTestConn(t, h, "a", "u1", "1.1.1.1", a, "user:u1")
	cb := newTestConn(t, h, "b", "u2", "1.1.1.2", b, "user:u2")

	h.Drain(3 * time.Second)
	<-ca.Done()
	<-cb.Done()
	for name, s := range map[string]*fakeSocket{"a": a, "b": b} {
		frames := s.dataFrames()
		var f wire.ServerFrame
		if len(frames) == 0 || proto.Unmarshal(frames[len(frames)-1], &f) != nil || f.GetBye().GetReason() != ByeDraining || f.GetBye().GetRetryAfterMs() != 3000 {
			t.Errorf("%s: last frame = %+v, want Bye draining retry 3000", name, f.GetBye())
		}
		if !s.isClosed() {
			t.Errorf("%s was not closed", name)
		}
		all := s.snapshot()
		if closeFrame := all[len(all)-1]; closeFrame.kind != msgClose || closeFrame.data[0] != 0x03 || closeFrame.data[1] != 0xE9 { // 1001
			t.Errorf("%s: close frame = %v, want code 1001", name, closeFrame)
		}
	}
}
