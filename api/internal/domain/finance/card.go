package finance

import (
	"errors"
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// ErrInvalidCard wraps every reason a card is refused.
var ErrInvalidCard = errors.New("invalid card")

// CardBrand is the network printed on a card: a closed set, so the console can
// show its mark. Empty means not given.
type CardBrand string

const (
	BrandVisa       CardBrand = "visa"
	BrandMastercard CardBrand = "mastercard"
	BrandElo        CardBrand = "elo"
	BrandAmex       CardBrand = "amex"
	BrandHipercard  CardBrand = "hipercard"
	BrandDiners     CardBrand = "diners"
	BrandOther      CardBrand = "other"
)

// CardBrands is the closed set, in the order the console offers it.
var CardBrands = []CardBrand{BrandVisa, BrandMastercard, BrandElo, BrandAmex, BrandHipercard, BrandDiners, BrandOther}

// ValidCardBrand reports whether b is one of CardBrands (empty is not).
func ValidCardBrand(b CardBrand) bool {
	for _, x := range CardBrands {
		if b == x {
			return true
		}
	}
	return false
}

// ValidLast4 reports whether s is exactly four ASCII digits. It is the only
// part of a card number billing ever holds: enough to tell two cards apart.
func ValidLast4(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

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
	// Brand and Last4 only identify the card to the person (UX batch 3); both
	// are optional and neither changes how statements close or are paid.
	Brand CardBrand
	Last4 string
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
	case c.Brand != "" && !ValidCardBrand(c.Brand):
		return fail("unknown brand %q", c.Brand)
	case c.Last4 != "" && !ValidLast4(c.Last4):
		return fail("last4 must be exactly four digits")
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
