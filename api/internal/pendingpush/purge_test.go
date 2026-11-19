package pendingpush

import (
	"context"
	"errors"
	"testing"
)

type fakeUserPurge struct {
	got []string
	n   int
	err error
}

func (f *fakeUserPurge) PurgeUser(_ context.Context, sub string) (int, error) {
	f.got = append(f.got, sub)
	return f.n, f.err
}

func TestPurgeErasesThePersonsPushRowsAndReportsTheCount(t *testing.T) {
	f := &fakeUserPurge{n: 5}
	n, err := NewPurger(f).Purge(context.Background(), "u1")
	if err != nil || n != 5 || len(f.got) != 1 || f.got[0] != "u1" {
		t.Errorf("Purge = %d, %v; purged for %v", n, err, f.got)
	}
}

func TestPurgeReportsAFailureSoTheSagaRetries(t *testing.T) {
	if _, err := NewPurger(&fakeUserPurge{err: errors.New("throttled")}).Purge(context.Background(), "u1"); err == nil {
		t.Error("an erasure that could not delete must say so")
	}
}

func TestPurgeRefusesAnEmptySubject(t *testing.T) {
	f := &fakeUserPurge{}
	if _, err := NewPurger(f).Purge(context.Background(), ""); err == nil || len(f.got) != 0 {
		t.Errorf("err = %v, calls = %v: an empty sub would address the partition 'USER#'", err, f.got)
	}
}
