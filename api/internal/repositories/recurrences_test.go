package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func fixtureRecurrence() finance.Recurrence {
	return finance.Recurrence{
		Direction: finance.Payable, Amount: 150000, CategoryID: "rent", AccountID: "bank", Description: "Aluguel",
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 10}, Start: brcal.New(2026, time.January, 1), Adjust: finance.AdjustNone},
	}
}

func TestEveryRecurrenceMethodChecksTheSpaceAndTheVerb(t *testing.T) {
	r := &RecurrenceRepository{}
	ctx, now := context.Background(), time.Now()
	var zero space.ResolvedSpace
	full, _ := space.ForJob("USER#u1", true)
	viewer := space.Narrow(full, space.Read)

	calls := map[string]func(sp space.ResolvedSpace) error{
		"Create": func(sp space.ResolvedSpace) error {
			_, err := r.Create(ctx, sp, fixtureRecurrence(), PostMeta{}, now)
			return err
		},
		"Get":     func(sp space.ResolvedSpace) error { _, err := r.Get(ctx, sp, "r"); return err },
		"List":    func(sp space.ResolvedSpace) error { _, err := r.List(ctx, sp); return err },
		"Update":  func(sp space.ResolvedSpace) error { return r.Update(ctx, sp, "r", RecurrencePatch{}, now) },
		"Archive": func(sp space.ResolvedSpace) error { return r.Archive(ctx, sp, "r", now) },
	}
	for name, call := range calls {
		if err := call(zero); !errors.Is(err, space.ErrNoSpace) {
			t.Errorf("%s with the zero space: %v, want ErrNoSpace", name, err)
		}
	}
	for _, name := range []string{"Create", "Update", "Archive"} {
		if err := calls[name](viewer); !errors.Is(err, space.ErrDenied) {
			t.Errorf("%s as a viewer: %v, want ErrDenied", name, err)
		}
	}
}

func TestCreateRefusesAnInvalidRecurrenceBeforeAnyWrite(t *testing.T) {
	r := &RecurrenceRepository{}
	sp, _ := space.ForJob("USER#u1", true)
	bad := fixtureRecurrence()
	bad.Amount = 0
	if _, err := r.Create(context.Background(), sp, bad, PostMeta{}, time.Now()); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("err = %v", err)
	}
}

// auto_settle makes the daily job settle on the user's behalf. Choosing it is
// therefore settling: a writer without finance.settle must not be able to turn
// it on, or the job settles for them.
func TestAutoSettleNeedsTheSettleVerb(t *testing.T) {
	r := &RecurrenceRepository{}
	full, _ := space.ForJob("USER#u1", true)
	writer := space.Narrow(full, space.Read|space.Write) // no settle
	now := time.Now()

	rec := fixtureRecurrence()
	rec.AutoSettle = true
	if _, err := r.Create(context.Background(), writer, rec, PostMeta{}, now); !errors.Is(err, space.ErrDenied) {
		t.Fatalf("Create with auto_settle as a writer: %v, want ErrDenied", err)
	}
	on := true
	if err := r.Update(context.Background(), writer, "r", RecurrencePatch{AutoSettle: &on}, now); !errors.Is(err, space.ErrDenied) {
		t.Fatalf("Update turning auto_settle on as a writer: %v, want ErrDenied", err)
	}
}
