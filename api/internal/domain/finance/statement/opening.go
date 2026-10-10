package statement

import (
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Balance is what the bank says the account held: OFX's LEDGERBAL (BALAMT
// on DTASOF). The available balance (AVAILBAL) is not it.
type Balance struct {
	Amount billing.Cents
	AsOf   brcal.Date
}

// Opening is an opening balance proposed from a statement (UX batch 5, spec
// § 3.7).
type Opening struct {
	Amount billing.Cents
	Date   brcal.Date
	// Ledger is the balance it came from, and Lines how many lines were taken
	// from it, so the console can show the arithmetic.
	Ledger Balance
	Lines  int
}

// OpeningFrom proposes the opening balance of an account that starts with this
// file: the ledger balance minus the sum of EVERY parsed line (credits in,
// debits out, as Line.Amount is signed), dated the day before the file's first
// line, so that the opening plus the lines lands on the bank's balance. Lines
// the parser rejected are not in it (they have no amount to take), nor is a
// line the person later ignores: an ignored line is still in the bank, so the
// account then ends that much away from the statement. No ledger balance, or no
// line, is no proposal.
func OpeningFrom(p Parsed) (Opening, bool) {
	if p.Ledger == nil || len(p.Lines) == 0 {
		return Opening{}, false
	}
	var sum billing.Cents
	first := p.Lines[0].Date
	for _, l := range p.Lines {
		sum += l.Amount
		if l.Date.Before(first) {
			first = l.Date
		}
	}
	return Opening{Amount: p.Ledger.Amount - sum, Date: first.AddDays(-1), Ledger: *p.Ledger, Lines: len(p.Lines)}, true
}

// rawBalance is a <LEDGERBAL> as text.
type rawBalance struct {
	amount, asOf string
}

// balance reads it, or nothing when either part cannot be read.
func (r rawBalance) balance() *Balance {
	date, ok := parseOFXDate(r.asOf)
	if !ok {
		return nil
	}
	amount, ok := parseAmount(r.amount, 0)
	if !ok {
		return nil
	}
	return &Balance{Amount: amount, AsOf: date}
}
