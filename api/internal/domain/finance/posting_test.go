package finance

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

var sys = SystemAccounts{Payables: "sys_payables", Receivables: "sys_receivables", OpeningBalance: "sys_opening"}

// balances folds transactions into a per-account balance, in the leg convention.
// It is the derivation the stored balance and SUMMARY rows cache (spec § 3.1).
func balances(txs ...Transaction) map[string]billing.Cents {
	b := map[string]billing.Cents{}
	for _, tx := range txs {
		for _, l := range tx.Legs {
			b[l.AccountID] += l.Amount
		}
	}
	return b
}

// ok returns a checker for a posting rule's (Transaction, error) result, so a
// call reads ok(t)(RecognizeBill(...)).
func ok(t *testing.T) func(Transaction, error) Transaction {
	return func(tx Transaction, err error) Transaction {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if sumLegs(tx) != 0 {
			t.Fatalf("unbalanced: %+v", tx)
		}
		return tx
	}
}

func TestAPayableRecognisedInMarchAndPaidInApril(t *testing.T) {
	rent := BillFacts{Direction: Payable, Amount: 200000, CategoryID: "rent", AccountID: "checking"}
	rec := ok(t)(RecognizeBill(sys, rent, d(2026, time.March, 1)))
	pay := ok(t)(SettleBill(sys, rent, 200000, "", d(2026, time.April, 6)))

	if rec.Date != d(2026, time.March, 1) || pay.Date != d(2026, time.April, 6) {
		t.Fatal("recognition carries the competence date and settlement the payment date")
	}
	b := balances(rec, pay)
	if b["rent"] != 200000 {
		t.Fatalf("expense = %d, want 200000 (in the DRE)", b["rent"])
	}
	if b["checking"] != -200000 {
		t.Fatalf("checking = %d, want -200000 (out of cash)", b["checking"])
	}
	if b["sys_payables"] != 0 {
		t.Fatalf("payables = %d, want 0 once paid", b["sys_payables"])
	}
	if got := balances(rec)["sys_payables"]; got != -200000 {
		t.Fatalf("payables before payment = %d, want -200000 (owed)", got)
	}
}

func TestAReceivableRecognisedAndReceived(t *testing.T) {
	fee := BillFacts{Direction: Receivable, Amount: 50000, CategoryID: "services", AccountID: "checking"}
	b := balances(
		ok(t)(RecognizeBill(sys, fee, d(2026, time.March, 1))),
		ok(t)(SettleBill(sys, fee, 50000, "", d(2026, time.March, 10))),
	)
	if b["services"] != -50000 || b["checking"] != 50000 || b["sys_receivables"] != 0 {
		t.Fatalf("balances = %v", b)
	}
}

func TestSettlementForADifferentAmount(t *testing.T) {
	cases := []struct {
		name     string
		bill     BillFacts
		paid     billing.Cents
		wantDiff billing.Cents // on the difference category, leg convention
		wantCash billing.Cents
	}{
		{"payable paid late with interest", BillFacts{Payable, 10000, "power", "checking"}, 10500, 500, -10500},
		{"payable paid early with a discount", BillFacts{Payable, 10000, "power", "checking"}, 9500, -500, -9500},
		{"receivable received short", BillFacts{Receivable, 10000, "services", "checking"}, 9800, 200, 9800},
		{"receivable received with interest", BillFacts{Receivable, 10000, "services", "checking"}, 10300, -300, 10300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := balances(
				ok(t)(RecognizeBill(sys, tc.bill, d(2026, time.March, 1))),
				ok(t)(SettleBill(sys, tc.bill, tc.paid, "difference", d(2026, time.March, 20))),
			)
			if b["difference"] != tc.wantDiff || b["checking"] != tc.wantCash {
				t.Fatalf("balances = %v", b)
			}
			if b["sys_payables"] != 0 || b["sys_receivables"] != 0 {
				t.Fatalf("the bill is not cleared: %v", b)
			}
		})
	}
}

func TestSettlementRefuses(t *testing.T) {
	bill := BillFacts{Payable, 10000, "power", "checking"}
	day := d(2026, time.March, 20)
	if _, err := SettleBill(sys, bill, 10500, "", day); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("a difference without a category: err = %v", err)
	}
	if _, err := SettleBill(sys, bill, 0, "", day); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("a zero settlement: err = %v", err)
	}
	for name, b := range map[string]BillFacts{
		"no direction":    {"", 10000, "power", "checking"},
		"negative amount": {Payable, -1, "power", "checking"},
		"no category":     {Payable, 10000, "", "checking"},
		"no account":      {Payable, 10000, "power", ""},
	} {
		if _, err := RecognizeBill(sys, b, day); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestACardPurchaseInInstallmentsHitsTheDREWholeAndCashPerStatement(t *testing.T) {
	purchase := ok(t)(CardPurchase("card", "electronics", 120000, d(2026, time.March, 5)))
	first := ok(t)(PayStatement("card", "checking", 10000, d(2026, time.April, 10)))

	b := balances(purchase)
	if b["electronics"] != 120000 || b["card"] != -120000 {
		t.Fatalf("after the purchase: %v", b)
	}
	b = balances(purchase, first)
	if b["electronics"] != 120000 {
		t.Fatalf("paying a statement moved the DRE: %v", b)
	}
	if b["checking"] != -10000 || b["card"] != -110000 {
		t.Fatalf("after the first statement: %v", b)
	}
}

func TestTransferNeverTouchesACategory(t *testing.T) {
	b := balances(ok(t)(Transfer("checking", "savings", 30000, d(2026, time.March, 5))))
	if len(b) != 2 || b["savings"] != 30000 || b["checking"] != -30000 {
		t.Fatalf("balances = %v", b)
	}
	if _, err := Transfer("checking", "checking", 1, d(2026, time.March, 5)); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("a transfer to itself: err = %v", err)
	}
}

func TestOpeningBalance(t *testing.T) {
	b := balances(
		ok(t)(OpeningBalance(sys, "checking", 50000, d(2026, time.January, 1))),
		ok(t)(OpeningBalance(sys, "card", -30000, d(2026, time.January, 1))),
	)
	if b["checking"] != 50000 || b["card"] != -30000 || b["sys_opening"] != -20000 {
		t.Fatalf("balances = %v", b)
	}
	if _, err := OpeningBalance(sys, "checking", 0, d(2026, time.January, 1)); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("zero: err = %v", err)
	}
}

func TestCancellingARecognisedBillIsAReversal(t *testing.T) {
	bill := BillFacts{Payable, 10000, "power", "checking"}
	rec := ok(t)(RecognizeBill(sys, bill, d(2026, time.March, 1)))
	rev := ok(t)(Reverse(rec, "tx_rec", d(2026, time.March, 15)))
	for account, v := range balances(rec, rev) {
		if v != 0 {
			t.Fatalf("%s = %d after the reversal, want 0", account, v)
		}
	}
}

func TestSettlementAttributesTheCashToTheBillsCategory(t *testing.T) {
	sys, _ := DefaultSystemAccounts()
	for _, dir := range []Direction{Payable, Receivable} {
		b := BillFacts{Direction: dir, Amount: 10000, CategoryID: "rent", AccountID: "bank"}
		tx, err := SettleBill(sys, b, 10500, "interest", brcal.New(2026, time.March, 10))
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range tx.Legs {
			want := ""
			if l.AccountID == "bank" {
				want = "rent"
			}
			if l.Flow != want {
				t.Errorf("%s: leg %s flow = %q, want %q", dir, l.AccountID, l.Flow, want)
			}
		}
	}
}

func TestTransfersAndOpeningBalancesAreNotCashFlow(t *testing.T) {
	sys, _ := DefaultSystemAccounts()
	d := brcal.New(2026, time.March, 1)
	tr, err := Transfer("bank", "cash", 5000, d)
	if err != nil {
		t.Fatal(err)
	}
	ob, err := OpeningBalance(sys, "bank", 100000, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range append(tr.Legs, ob.Legs[0]) {
		if l.Flow != FlowNone {
			t.Errorf("leg %s flow = %q, want FlowNone", l.AccountID, l.Flow)
		}
	}
}
