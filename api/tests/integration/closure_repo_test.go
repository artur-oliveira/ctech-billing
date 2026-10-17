//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/closure"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func newClosure(acct string) *closure.Closure {
	return closure.New("clo_"+id.New(), acct, "usr_owner", seedNow)
}

func TestOneOpenClosurePerTenantAndTheMarkerIsReleasedOnCancel(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewClosureRepository(testDB, testCfg)
	acct := newSpaceOrgID()
	c := newClosure(acct)
	if err := repo.Create(ctx, c, seedNow); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, newClosure(acct), seedNow); !errors.Is(err, closure.ErrOpenClosure) {
		t.Fatalf("second open closure: %v, want ErrOpenClosure", err)
	}
	open, err := repo.Open(ctx, acct)
	if err != nil || open.ID != c.ID || open.State != closure.AwaitingConfirmation {
		t.Fatalf("open = %+v, %v", open, err)
	}

	if err := c.Confirm(seedNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, c, closure.AwaitingConfirmation, seedNow); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, c, closure.AwaitingConfirmation, seedNow); !errors.Is(err, closure.ErrWrongState) {
		t.Fatalf("a stale save: %v, want ErrWrongState", err)
	}
	if err := c.Cancel(seedNow.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, c, closure.Closing, seedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Open(ctx, acct); !errors.Is(err, closure.ErrNotFound) {
		t.Fatalf("after cancel: %v, want no open closure", err)
	}
	if err := repo.Create(ctx, newClosure(acct), seedNow); err != nil {
		t.Fatalf("a new request after a cancel: %v", err)
	}
	got, err := repo.Get(ctx, c.ID)
	if err != nil || got.State != closure.Cancelled || got.CancelledAt.IsZero() {
		t.Fatalf("stored = %+v, %v", got, err)
	}
}

func TestReadOnlyFlagsAreRaisedAndLiftedPerTenantRow(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewClosureRepository(testDB, testCfg)
	live, test := "ten_"+id.New(), "ten_"+id.New()
	if on, err := repo.ReadOnly(ctx, live); err != nil || on {
		t.Fatalf("before: %v %v", on, err)
	}
	if err := repo.SetReadOnly(ctx, []string{live, test}, "clo_x", true, seedNow); err != nil {
		t.Fatal(err)
	}
	if on, _ := repo.ReadOnly(ctx, live); !on {
		t.Fatal("live tenant must be read-only")
	}
	if err := repo.SetReadOnly(ctx, []string{live, test}, "clo_x", false, seedNow); err != nil {
		t.Fatal(err)
	}
	if on, _ := repo.ReadOnly(ctx, test); on {
		t.Fatal("the flag must be lifted")
	}
}

func TestDueListsOnlyClosuresWhoseNextActionHasArrived(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewClosureRepository(testDB, testCfg)
	c := newClosure(newSpaceOrgID())
	if err := repo.Create(ctx, c, seedNow); err != nil {
		t.Fatal(err)
	}
	has := func(at time.Time) bool {
		due, err := repo.Due(ctx, at)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range due {
			if d.Closure.ID == c.ID {
				return true
			}
		}
		return false
	}
	if has(seedNow.Add(closure.ConfirmWindow - time.Minute)) {
		t.Fatal("an unconfirmed request is not due before its window ends")
	}
	if !has(seedNow.Add(closure.ConfirmWindow + time.Minute)) {
		t.Fatal("an unconfirmed request is due once its window ended (it expires)")
	}
}
