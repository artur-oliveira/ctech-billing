package services

import (
	"context"
	"errors"
	"testing"
)

type fakePushPurger struct {
	n   int
	err error
	got []string
}

func (f *fakePushPurger) Purge(_ context.Context, sub string) (int, error) {
	f.got = append(f.got, sub)
	return f.n, f.err
}

func TestErasePushDeletesAndCounts(t *testing.T) {
	f := &fakePushPurger{n: 4}
	counts := map[string]int{}
	if err := (&Eraser{}).WithPush(f).erasePush(context.Background(), "u1", counts); err != nil {
		t.Fatal(err)
	}
	if counts["push_rows"] != 4 || len(f.got) != 1 || f.got[0] != "u1" {
		t.Errorf("counts = %v, purged for %v", counts, f.got)
	}
}

func TestErasePushIsANoopWithoutAPurger(t *testing.T) {
	counts := map[string]int{}
	if err := (&Eraser{}).erasePush(context.Background(), "u1", counts); err != nil || len(counts) != 0 {
		t.Errorf("err = %v counts = %v", err, counts)
	}
}

func TestErasePushFailureFailsTheErasure(t *testing.T) {
	e := (&Eraser{}).WithPush(&fakePushPurger{err: errors.New("throttled")})
	if err := e.erasePush(context.Background(), "u1", map[string]int{}); err == nil {
		t.Error("a push purge that failed must fail the erasure so the saga retries it")
	}
}
