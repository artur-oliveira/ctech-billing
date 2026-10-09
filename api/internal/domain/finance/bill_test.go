package finance

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func validBill() Bill {
	return Bill{
		ID: "b1", Direction: Payable, Amount: 10000, AccountID: "bank", CategoryID: "rent",
		Description: "Aluguel", Competence: d(2026, time.March, 5), Due: d(2026, time.March, 10),
		Status: BillForecast, Origin: OriginManual,
	}
}

func TestBillValidate(t *testing.T) {
	if err := validBill().Validate(); err != nil {
		t.Fatalf("a valid bill was refused: %v", err)
	}
	bad := map[string]func(*Bill){
		"zero amount":          func(b *Bill) { b.Amount = 0 },
		"negative amount":      func(b *Bill) { b.Amount = -1 },
		"amount past the cap":  func(b *Bill) { b.Amount = MaxLegAmount + 1 },
		"no direction":         func(b *Bill) { b.Direction = "" },
		"no account":           func(b *Bill) { b.AccountID = "" },
		"no category":          func(b *Bill) { b.CategoryID = "" },
		"no competence":        func(b *Bill) { b.Competence = brcal.Date{} },
		"no due date":          func(b *Bill) { b.Due = brcal.Date{} },
		"unknown status":       func(b *Bill) { b.Status = "weird" },
		"unknown origin":       func(b *Bill) { b.Origin = "weird" },
		"recurrence w/o ref":   func(b *Bill) { b.Origin = OriginRecurrence },
		"description too long": func(b *Bill) { b.Description = string(make([]byte, 201)) },
		"paid without a date":  func(b *Bill) { b.Status = BillPaid },
	}
	for name, mutate := range bad {
		b := validBill()
		mutate(&b)
		if err := b.Validate(); !errors.Is(err, ErrInvalidBill) {
			t.Errorf("%s: err = %v, want ErrInvalidBill", name, err)
		}
	}
}

func TestBillLifecycle(t *testing.T) {
	b := validBill()
	if !b.CanEdit() || b.CanCancel() != nil || b.CanSettle() != nil {
		t.Fatal("a forecast bill must be editable, cancellable and settleable")
	}
	paid := b
	paid.Status, paid.PaidDate = BillPaid, d(2026, time.March, 9)
	if paid.CanEdit() {
		t.Error("a paid bill is not editable")
	}
	if err := paid.CanCancel(); !errors.Is(err, ErrBillState) {
		t.Errorf("cancelling a paid bill: %v, want ErrBillState (reverse the settlement first)", err)
	}
	if err := paid.CanSettle(); !errors.Is(err, ErrBillState) {
		t.Errorf("settling a paid bill: %v, want ErrBillState", err)
	}
	canceled := b
	canceled.Status = BillCanceled
	if canceled.CanEdit() || canceled.CanSettle() == nil || canceled.CanCancel() == nil {
		t.Error("a cancelled bill allows nothing")
	}
}

func TestBillBuckets(t *testing.T) {
	today := d(2026, time.March, 10)
	b := validBill()
	for due, want := range map[int]Bucket{9: BucketOverdue, 10: BucketToday, 11: BucketUpcoming} {
		b.Due = d(2026, time.March, due)
		if got := b.BucketOn(today); got != want {
			t.Errorf("due %d: bucket = %s, want %s", due, got, want)
		}
	}
}

func TestFactsCarryWhatThePostingRulesNeed(t *testing.T) {
	f := validBill().Facts()
	if f.Direction != Payable || f.Amount != 10000 || f.CategoryID != "rent" || f.AccountID != "bank" {
		t.Fatalf("facts = %+v", f)
	}
}

func TestAStatementBillIsNotCanceledAndClearsItsCard(t *testing.T) {
	b := Bill{Direction: Payable, Amount: 1, AccountID: "bank", CategoryID: "visa", Status: BillForecast,
		Origin: OriginCardStatement, OriginRef: "visa#2026-03"}
	if err := b.CanCancel(); !errors.Is(err, ErrBillState) {
		t.Fatalf("CanCancel = %v", err)
	}
	if f := b.Facts(); f.Clears != "visa" {
		t.Fatalf("Facts().Clears = %q", f.Clears)
	}
	b.Origin = OriginManual
	if f := b.Facts(); f.Clears != "" {
		t.Fatalf("a manual bill clears payables, got %q", f.Clears)
	}
}
