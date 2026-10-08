package finance

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

func billFacts(amount int64, cat string) BillFacts {
	return BillFacts{Direction: Payable, Amount: billing.Cents(amount), CategoryID: cat, AccountID: "bank"}
}

// An edit that changes what was recognised must be ONE transaction: DynamoDB
// forbids two writes to the same item in a single TransactWriteItems, and a
// reverse + re-recognise would touch the payables account twice.
func TestAdjustBillNetsTheOldAndTheNewRecognition(t *testing.T) {
	sys, _ := DefaultSystemAccounts()
	date := d(2026, time.March, 5)

	tx, err := AdjustBill(sys, billFacts(1000, "rent"), billFacts(1500, "rent"), date)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Kind != KindAdjustment || tx.Date != date || len(tx.Legs) != 2 {
		t.Fatalf("tx = %+v", tx)
	}
	got := map[string]int64{}
	for _, l := range tx.Legs {
		got[l.AccountID] = int64(l.Amount)
	}
	// Payable: expense debit grows by 500, payables (liability) credit grows by 500.
	if got["rent"] != 500 || got[sys.Payables] != -500 {
		t.Fatalf("legs = %v", got)
	}

	// A category change: old category credited back, new one debited, payables unchanged.
	tx, err = AdjustBill(sys, billFacts(1000, "rent"), billFacts(1000, "food"), date)
	if err != nil {
		t.Fatal(err)
	}
	got = map[string]int64{}
	for _, l := range tx.Legs {
		got[l.AccountID] = int64(l.Amount)
	}
	if len(got) != 2 || got["rent"] != -1000 || got["food"] != 1000 {
		t.Fatalf("category change legs = %v", got)
	}
}

func TestAdjustBillRefusesNothingToAdjust(t *testing.T) {
	sys, _ := DefaultSystemAccounts()
	f := billFacts(1000, "rent")
	if _, err := AdjustBill(sys, f, f, d(2026, time.March, 5)); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("err = %v, want ErrInvalidTransaction", err)
	}
}

func TestCancelBillNegatesTheCurrentRecognition(t *testing.T) {
	sys, _ := DefaultSystemAccounts()
	date := d(2026, time.March, 5)
	f := billFacts(1000, "rent")
	rec, _ := RecognizeBill(sys, f, date)
	cancel, err := CancelBill(sys, f, date)
	if err != nil {
		t.Fatal(err)
	}
	net := map[string]int64{}
	for _, l := range append(append([]Leg(nil), rec.Legs...), cancel.Legs...) {
		net[l.AccountID] += int64(l.Amount)
	}
	for acct, v := range net {
		if v != 0 {
			t.Errorf("%s nets to %d after recognise + cancel", acct, v)
		}
	}
	if cancel.Kind != KindAdjustment {
		t.Errorf("kind = %s", cancel.Kind)
	}
}
