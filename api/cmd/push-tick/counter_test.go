package main

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/pendingpush"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type fakePending struct {
	n        int
	err      error
	gotOwner string
	personal bool
	live     bool
	limit    int
}

func (f *fakePending) ListPendingOccurrences(_ context.Context, sp space.ResolvedSpace, limit int) ([]repositories.PendingOccurrence, error) {
	f.gotOwner, f.personal, f.live, f.limit = sp.Owner(), sp.Personal(), sp.Livemode(), limit
	return make([]repositories.PendingOccurrence, f.n), f.err
}

func TestCounterReadsThePersonalLiveSpaceOnly(t *testing.T) {
	f := &fakePending{n: 3}
	n, err := newCounter(f).Pending(context.Background(), "u1")
	if err != nil || n != 3 {
		t.Fatalf("Pending = %d, %v", n, err)
	}
	if f.gotOwner != "USER#u1" || !f.personal || !f.live {
		t.Errorf("space = %q personal=%v live=%v, want the person's own live space", f.gotOwner, f.personal, f.live)
	}
}

func TestCounterBoundsTheReadOneOverTheCap(t *testing.T) {
	f := &fakePending{}
	_, _ = newCounter(f).Pending(context.Background(), "u1")
	if f.limit != pendingpush.PendingCap+1 {
		t.Errorf("limit = %d, want %d (enough to say 'more than 99')", f.limit, pendingpush.PendingCap+1)
	}
}

func TestCounterRefusesASubjectThatCouldNameAnotherSpace(t *testing.T) {
	f := &fakePending{n: 3}
	if _, err := newCounter(f).Pending(context.Background(), "u1#evil"); err == nil {
		t.Error("a subject with a key separator must be refused, not turned into someone else's partition")
	}
	if f.gotOwner != "" {
		t.Errorf("the repository was read for %q", f.gotOwner)
	}
}

func TestCounterPropagatesTheReadError(t *testing.T) {
	if _, err := newCounter(&fakePending{err: errors.New("throttled")}).Pending(context.Background(), "u1"); err == nil {
		t.Error("a failed read must not look like zero pending")
	}
}
