package finance

import "gopkg.aoctech.app/billing/api/internal/domain/billing"

// Totals is one account's debits and credits in one month. Both are
// non-negative: Credits is the magnitude of the credit legs.
type Totals struct {
	Debits, Credits billing.Cents
}

// SummaryKey names one SUMMARY row: an account in a calendar month.
type SummaryKey struct {
	Month     Month
	AccountID string
}

// Derived is everything the stored balance and SUMMARY rows cache. The rows are
// a performance device; this is the definition, and the rebuild command
// compares the two (spec § 10, "cache equals derivation").
type Derived struct {
	Balances map[string]billing.Cents
	Summary  map[SummaryKey]Totals
}

// Derive folds transactions into balances (the sum of leg amounts per account,
// debit positive) and monthly debit/credit totals by the transaction's date.
func Derive(txs []Transaction) Derived {
	out := Derived{
		Balances: make(map[string]billing.Cents),
		Summary:  make(map[SummaryKey]Totals),
	}
	for _, tx := range txs {
		m := MonthOf(tx.Date)
		for _, l := range tx.Legs {
			out.Balances[l.AccountID] += l.Amount
			k := SummaryKey{Month: m, AccountID: l.AccountID}
			t := out.Summary[k]
			if l.Amount > 0 {
				t.Debits += l.Amount
			} else {
				t.Credits += -l.Amount
			}
			out.Summary[k] = t
		}
	}
	return out
}
