package v1

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// The web app opens a space with several parallel GETs. Before the fix each one
// ran the space-creation transaction and they collided with each other; the
// work must run once per key however many requests arrive together.
func TestEnsureOnceRunsOncePerKeyUnderConcurrency(t *testing.T) {
	var e ensureOnce
	var calls atomic.Int32
	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.do("space#1", func() error { calls.Add(1); return nil })
		}()
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1", got)
	}
	for _, err := range errs {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

// A failure is not remembered: the next request must try again.
func TestEnsureOnceRetriesAfterAFailure(t *testing.T) {
	var e ensureOnce
	boom := errors.New("boom")
	if err := e.do("k", func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}
	var calls int
	if err := e.do("k", func() error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatalf("retry: err=%v calls=%d", err, calls)
	}
	if err := e.do("k", func() error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatalf("done keys must not run again: err=%v calls=%d", err, calls)
	}
}
