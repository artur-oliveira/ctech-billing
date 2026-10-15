package services

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakePasses struct {
	mu      sync.Mutex
	calls   []string
	block   chan struct{}
	started chan struct{}
}

func (f *fakePasses) FanOut(_ context.Context, live bool, _ int, _ time.Time) PassResult {
	f.record("fanout", live)
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.block != nil {
		<-f.block
	}
	return PassResult{}
}

func (f *fakePasses) Deliver(_ context.Context, live bool, _ int, _ time.Time) PassResult {
	f.record("deliver", live)
	return PassResult{}
}

func (f *fakePasses) record(what string, live bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if live {
		what += ":live"
	} else {
		what += ":test"
	}
	f.calls = append(f.calls, what)
}

func (f *fakePasses) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestAKickFansOutThenDeliversForThatModeOnly(t *testing.T) {
	f := &fakePasses{}
	NewDeliveryKick(f).Kick(true)
	waitFor(t, func() bool { return len(f.snapshot()) == 2 })
	got := f.snapshot()
	if got[0] != "fanout:live" || got[1] != "deliver:live" {
		t.Fatalf("passes %v", got)
	}
}

func TestKicksWhileOneRunsCoalesceIntoOneMorePass(t *testing.T) {
	f := &fakePasses{block: make(chan struct{}), started: make(chan struct{}, 1)}
	k := NewDeliveryKick(f)
	k.Kick(true)
	<-f.started
	for i := 0; i < 20; i++ {
		k.Kick(true) // a burst of payments while a pass is in flight
	}
	close(f.block)
	waitFor(t, func() bool { return len(f.snapshot()) >= 4 })
	time.Sleep(50 * time.Millisecond)
	if got := f.snapshot(); len(got) != 4 {
		t.Fatalf("twenty kicks during one pass cost exactly one more pass, got %v", got)
	}
}
