package finance

import (
	"math/rand/v2"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

func TestDeriveAddsLegsPerAccountAndMonth(t *testing.T) {
	a, err := NewTransaction(KindTransfer, d(2026, time.March, 31), Leg{AccountID: "bank", Amount: -1000}, Leg{AccountID: "cash", Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewTransaction(KindTransfer, d(2026, time.April, 1), Leg{AccountID: "bank", Amount: -500}, Leg{AccountID: "cash", Amount: 500})
	if err != nil {
		t.Fatal(err)
	}
	got := Derive([]Transaction{a, b})
	if got.Balances["bank"] != -1500 || got.Balances["cash"] != 1500 {
		t.Fatalf("balances = %v", got.Balances)
	}
	march := SummaryKey{Month{2026, time.March}, "bank"}
	if got.Summary[march] != (Totals{Debits: 0, Credits: 1000}) {
		t.Fatalf("march bank = %+v", got.Summary[march])
	}
	april := SummaryKey{Month{2026, time.April}, "cash"}
	if got.Summary[april] != (Totals{Debits: 500, Credits: 0}) {
		t.Fatalf("april cash = %+v", got.Summary[april])
	}
}

func TestDeriveOfATransactionAndItsReversalIsZero(t *testing.T) {
	orig, _ := NewTransaction(KindTransfer, d(2026, time.March, 2), Leg{AccountID: "a", Amount: 700}, Leg{AccountID: "b", Amount: -700})
	rev, err := Reverse(orig, "tx1", d(2026, time.March, 3))
	if err != nil {
		t.Fatal(err)
	}
	got := Derive([]Transaction{orig, rev})
	for acct, bal := range got.Balances {
		if bal != 0 {
			t.Fatalf("%s balance = %d after a reversal", acct, bal)
		}
	}
}

// The invariant the rebuild command relies on: for any balanced history the
// balances sum to zero, and per account debits-credits equals the balance.
func TestDeriveInvariantsOverARandomHistory(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	accounts := []string{"a", "b", "c", "d", "e", "f"}
	var txs []Transaction
	for len(txs) < 300 {
		n := 2 + r.IntN(4)
		perm := r.Perm(len(accounts))[:n]
		legs := make([]Leg, 0, n)
		var sum billing.Cents
		for i := 0; i < n-1; i++ {
			amt := billing.Cents(1 + r.IntN(100000))
			if r.IntN(2) == 0 {
				amt = -amt
			}
			legs = append(legs, Leg{AccountID: accounts[perm[i]], Amount: amt})
			sum += amt
		}
		if sum == 0 {
			continue
		}
		legs = append(legs, Leg{AccountID: accounts[perm[n-1]], Amount: -sum})
		date := d(2026, time.Month(1+r.IntN(12)), 1+r.IntN(28))
		tx, err := NewTransaction(KindTransfer, date, legs...)
		if err != nil {
			t.Fatal(err)
		}
		txs = append(txs, tx)
	}
	got := Derive(txs)
	var total billing.Cents
	for acct, bal := range got.Balances {
		total += bal
		var debits, credits billing.Cents
		for k, v := range got.Summary {
			if k.AccountID == acct {
				debits += v.Debits
				credits += v.Credits
			}
		}
		if debits-credits != bal {
			t.Fatalf("%s: debits %d - credits %d != balance %d", acct, debits, credits, bal)
		}
	}
	if total != 0 {
		t.Fatalf("balances sum to %d, want 0", total)
	}
}
