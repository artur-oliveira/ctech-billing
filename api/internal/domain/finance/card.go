package finance

import (
	"errors"
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// ErrInvalidCard wraps every reason a card is refused.
var ErrInvalidCard = errors.New("invalid card")

// Card is a credit card's settings (spec § 3.6). Its money is a liability
// account in the ledger with the same id; this is when its statements close,
// when they are due, and which account pays them.
type Card struct {
	ID              string
	ClosingDay      int
	DueDay          int
	PayingAccountID string
	// OpenMonth is the first statement not yet closed: every installment lands
	// on it or later, so a closed statement is never changed.
	OpenMonth Month
}

func (c Card) Validate() error {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidCard}, a...)...)
	}
	switch {
	case c.ClosingDay < 1 || c.ClosingDay > 31:
		return fail("closing day must be 1..31")
	case c.DueDay < 1 || c.DueDay > 31:
		return fail("due day must be 1..31")
	case c.PayingAccountID == "":
		return fail("the paying account is required")
	case c.OpenMonth == (Month{}):
		return fail("the open month is required")
	}
	return nil
}

func clampDay(m Month, day int) brcal.Date {
	return brcal.New(m.Year, m.Month, min(day, brcal.DaysInMonth(m.Year, m.Month)))
}

// ClosingDate is the day statement m closes: its closing day, clamped to the
// month's last day (a card closing on the 31st closes on Feb 28).
func ClosingDate(m Month, closingDay int) brcal.Date { return clampDay(m, closingDay) }

// DueDate is the first due day strictly after the statement's closing date:
// in the same month when the due day comes after the closing day, otherwise in
// the next one.
func DueDate(m Month, closingDay, dueDay int) brcal.Date {
	if d := clampDay(m, dueDay); d.After(ClosingDate(m, closingDay)) {
		return d
	}
	return clampDay(m.Add(1), dueDay)
}

// CardFlow is the cash-flow attribution of paying a card's statement (6.4
// scope decision 8): one line per card in the cash flow.
func CardFlow(cardID string) string { return "card:" + cardID }

// StatementRef names one statement of one card: the statement bill's
// origin_ref and payment_group.
func StatementRef(cardID string, m Month) string { return cardID + "#" + m.String() }
