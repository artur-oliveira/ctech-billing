package finance

import (
	"errors"
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// MaxInstallments bounds a card purchase. 48× exists in Brazilian retail; more
// does not.
const MaxInstallments = 48

// ErrInvalidInstallments wraps every reason an installment plan is refused.
var ErrInvalidInstallments = errors.New("invalid installment plan")

// Installment is one part of a card purchase and the statement it is billed on.
type Installment struct {
	Number    int // 1-based
	Amount    billing.Cents
	Statement Month
}

// SplitInstallments divides total into n parts that **sum back to total**. The
// remainder of the integer division goes to the first installment, which is how
// Brazilian card issuers print it (R$ 100,00 em 3×: 33,34 + 33,33 + 33,33).
// Independent rounding of each part is what breaks the sum, as it would in
// proration.
func SplitInstallments(total billing.Cents, n int) ([]billing.Cents, error) {
	if n < 1 || n > MaxInstallments {
		return nil, fmt.Errorf("%w: %d installments, want 1..%d", ErrInvalidInstallments, n, MaxInstallments)
	}
	if total < billing.Cents(n) {
		return nil, fmt.Errorf("%w: %s cannot be split into %d non-zero installments", ErrInvalidInstallments, total, n)
	}
	base := total / billing.Cents(n)
	parts := make([]billing.Cents, n)
	for i := range parts {
		parts[i] = base
	}
	parts[0] += total - base*billing.Cents(n)
	return parts, nil
}

// StatementFor returns the statement a purchase on `purchase` is billed on, for
// a card closing on closingDay. A purchase on or before the closing day falls
// into that month's statement, after it into the next (spec § 3.6). A closing
// day past the month's end clamps to its last day, like every day-of-month rule
// in this package. closingDay must be 1..31: AllocateInstallments checks it, and
// the card entity must too (a value <= 0 would push every purchase to the next
// statement).
func StatementFor(purchase brcal.Date, closingDay int) Month {
	m := MonthOf(purchase)
	if purchase.Day <= min(closingDay, brcal.DaysInMonth(m.Year, m.Month)) {
		return m
	}
	return m.Add(1)
}

// AllocateInstallments splits a purchase and places installment k on the
// statement k−1 months after the first one.
func AllocateInstallments(total billing.Cents, n int, purchase brcal.Date, closingDay int) ([]Installment, error) {
	if closingDay < 1 || closingDay > 31 {
		return nil, fmt.Errorf("%w: closing day %d is not 1..31", ErrInvalidInstallments, closingDay)
	}
	parts, err := SplitInstallments(total, n)
	if err != nil {
		return nil, err
	}
	first := StatementFor(purchase, closingDay)
	out := make([]Installment, n)
	for i, amount := range parts {
		out[i] = Installment{Number: i + 1, Amount: amount, Statement: first.Add(i)}
	}
	return out, nil
}

// Advance moves every installment billed after `open` onto `open` — antecipação.
// Installments on or before it stay where they are. It changes allocation only:
// the DRE already holds the full purchase (CardPurchase).
func Advance(plan []Installment, open Month) []Installment {
	out := make([]Installment, len(plan))
	for i, inst := range plan {
		if inst.Statement.Compare(open) > 0 {
			inst.Statement = open
		}
		out[i] = inst
	}
	return out
}

// RefundPlan splits a refunded purchase's installments by what was already
// billed. Installments on statements before `open` were billed on closed
// statements and come back as a credit on `open`; the rest are simply removed,
// since they were never charged. A closed statement is never edited
// (spec § 3.6).
func RefundPlan(plan []Installment, open Month) (credit billing.Cents, removed []Installment) {
	for _, inst := range plan {
		if inst.Statement.Compare(open) < 0 {
			credit += inst.Amount
		} else {
			removed = append(removed, inst)
		}
	}
	return credit, removed
}
