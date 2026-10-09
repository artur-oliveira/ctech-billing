package finance

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func sumLegs(tx Transaction) billing.Cents {
	var s billing.Cents
	for _, l := range tx.Legs {
		s += l.Amount
	}
	return s
}

func TestNewTransactionBalances(t *testing.T) {
	tx, err := NewTransaction(KindTransfer, d(2026, time.March, 5),
		Leg{AccountID: "savings", Amount: 1000}, Leg{AccountID: "checking", Amount: -1000})
	if err != nil {
		t.Fatal(err)
	}
	if sumLegs(tx) != 0 || len(tx.Legs) != 2 {
		t.Fatalf("tx = %+v", tx)
	}
}

func TestNewTransactionRefuses(t *testing.T) {
	day := d(2026, time.March, 5)
	nine := make([]Leg, 9)
	for i := range nine {
		nine[i] = Leg{AccountID: "a", Amount: 1}
	}
	nine[8].Amount = -8
	for name, legs := range map[string][]Leg{
		"unbalanced":    {{AccountID: "a", Amount: 1000}, {AccountID: "b", Amount: -999}},
		"one leg":       {{AccountID: "a", Amount: 0}},
		"no legs":       nil,
		"zero leg":      {{AccountID: "a", Amount: 1000}, {AccountID: "b", Amount: -1000}, {AccountID: "c", Amount: 0}},
		"no account":    {{AccountID: "", Amount: 1000}, {AccountID: "b", Amount: -1000}},
		"too many legs": nine,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTransaction(KindTransfer, day, legs...); !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("err = %v, want ErrInvalidTransaction", err)
			}
		})
	}
	if _, err := NewTransaction(KindTransfer, brcal.Date{}, Leg{AccountID: "a", Amount: 1000}, Leg{AccountID: "b", Amount: -1000}); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("no date: err = %v, want ErrInvalidTransaction", err)
	}
}

func TestNewTransactionCopiesItsLegs(t *testing.T) {
	legs := []Leg{{AccountID: "a", Amount: 1000}, {AccountID: "b", Amount: -1000}}
	tx, err := NewTransaction(KindTransfer, d(2026, time.March, 5), legs...)
	if err != nil {
		t.Fatal(err)
	}
	legs[0].Amount = 5
	if tx.Legs[0].Amount != 1000 {
		t.Fatal("the transaction shares its legs with the caller's slice")
	}
}

func TestReverseNegatesEveryLegAndPointsBack(t *testing.T) {
	orig, err := NewTransaction(KindCardPurchase, d(2026, time.March, 5), Leg{AccountID: "groceries", Amount: 12000}, Leg{AccountID: "card", Amount: -12000})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := Reverse(orig, "tx_1", d(2026, time.March, 9))
	if err != nil {
		t.Fatal(err)
	}
	if rev.Kind != KindReversal || rev.Adjusts != "tx_1" || rev.Date != d(2026, time.March, 9) {
		t.Fatalf("rev = %+v", rev)
	}
	for i := range orig.Legs {
		if rev.Legs[i].AccountID != orig.Legs[i].AccountID || rev.Legs[i].Amount != -orig.Legs[i].Amount {
			t.Fatalf("leg %d: %+v does not undo %+v", i, rev.Legs[i], orig.Legs[i])
		}
	}
	if _, err := Reverse(rev, "tx_2", rev.Date); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("reversing a reversal: err = %v", err)
	}
	if _, err := Reverse(orig, "", rev.Date); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("reversal without an id: err = %v", err)
	}
}

func TestDREGroupAllowsClass(t *testing.T) {
	if !GroupGrossRevenue.AllowsClass(ClassIncome) || GroupGrossRevenue.AllowsClass(ClassExpense) {
		t.Fatal("gross revenue is income only")
	}
	if !GroupOperatingExpenses.AllowsClass(ClassExpense) || GroupOperatingExpenses.AllowsClass(ClassIncome) {
		t.Fatal("operating expenses are expense only")
	}
	if !GroupFinancialResult.AllowsClass(ClassIncome) || !GroupFinancialResult.AllowsClass(ClassExpense) {
		t.Fatal("financial result takes both")
	}
	if GroupOther.AllowsClass(ClassAsset) || DREGroup("made_up").AllowsClass(ClassIncome) {
		t.Fatal("assets never sit in the DRE, and unknown groups take nothing")
	}
}

func TestReverseCarriesFlowOver(t *testing.T) {
	sys, _ := DefaultSystemAccounts()
	tx, _ := SettleBill(sys, BillFacts{Direction: Payable, Amount: 100, CategoryID: "rent", AccountID: "bank"}, 100, "", d(2026, time.March, 9))
	rev, err := Reverse(tx, "tx_1", d(2026, time.March, 10))
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range rev.Legs {
		if l.Flow != tx.Legs[i].Flow || l.Amount != -tx.Legs[i].Amount {
			t.Errorf("leg %d = %+v, want the negation of %+v with the same flow", i, l, tx.Legs[i])
		}
	}
}
