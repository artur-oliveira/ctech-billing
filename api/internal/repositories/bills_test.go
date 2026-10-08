package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func fixtureBill() finance.Bill {
	return finance.Bill{
		Direction: finance.Payable, Amount: 1000, AccountID: "bank", CategoryID: "rent",
		Competence: brcal.New(2026, time.March, 1), Due: brcal.New(2026, time.March, 10),
		Status: finance.BillForecast, Origin: finance.OriginManual,
	}
}

// Every method refuses before touching a table; a nil client would panic.
func TestEveryBillMethodChecksTheSpaceAndTheVerb(t *testing.T) {
	r := &BillRepository{}
	ctx, now := context.Background(), time.Now()
	var zero space.ResolvedSpace
	full, _ := space.ForJob("USER#u1", true)
	viewer := space.Narrow(full, space.Read)
	writer := space.Narrow(full, space.Read|space.Write) // no settle
	date := brcal.New(2026, time.March, 10)

	calls := map[string]func(sp space.ResolvedSpace) error{
		"Create": func(sp space.ResolvedSpace) error { _, err := r.Create(ctx, sp, fixtureBill(), PostMeta{}, now); return err },
		"Get":    func(sp space.ResolvedSpace) error { _, err := r.Get(ctx, sp, "b"); return err },
		"ListOpen": func(sp space.ResolvedSpace) error {
			_, err := r.ListOpen(ctx, sp, finance.Payable, 10, nil)
			return err
		},
		"Settle": func(sp space.ResolvedSpace) error {
			_, err := r.Settle(ctx, sp, "b", 1000, "", date, PostMeta{}, now)
			return err
		},
		"Cancel": func(sp space.ResolvedSpace) error { _, err := r.Cancel(ctx, sp, "b", date, PostMeta{}, now); return err },
		"Edit":   func(sp space.ResolvedSpace) error { _, err := r.Edit(ctx, sp, "b", BillEdit{}, date, PostMeta{}, now); return err },
	}
	for name, call := range calls {
		if err := call(zero); !errors.Is(err, space.ErrNoSpace) {
			t.Errorf("%s with the zero space: %v, want ErrNoSpace", name, err)
		}
	}
	for _, name := range []string{"Create", "Settle", "Cancel", "Edit"} {
		if err := calls[name](viewer); !errors.Is(err, space.ErrDenied) {
			t.Errorf("%s as a viewer: %v, want ErrDenied", name, err)
		}
	}
	if err := calls["Settle"](writer); !errors.Is(err, space.ErrDenied) {
		t.Errorf("Settle without finance.settle: %v, want ErrDenied", err)
	}
}

func TestCreateRefusesAnInvalidBillBeforeAnyWrite(t *testing.T) {
	r := &BillRepository{}
	sp, _ := space.ForJob("USER#u1", true)
	b := fixtureBill()
	b.Amount = billing.Cents(0)
	if _, err := r.Create(context.Background(), sp, b, PostMeta{}, time.Now()); !errors.Is(err, finance.ErrInvalidBill) {
		t.Fatalf("err = %v, want ErrInvalidBill", err)
	}
}
