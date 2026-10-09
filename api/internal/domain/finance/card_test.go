package finance

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func TestStatementDates(t *testing.T) {
	m := func(y int, mo time.Month) Month { return Month{Year: y, Month: mo} }
	for _, tc := range []struct {
		name               string
		month              Month
		closing, due       int
		wantClose, wantDue brcal.Date
	}{
		{"due after closing, same month", m(2026, time.March), 3, 10, brcal.New(2026, time.March, 3), brcal.New(2026, time.March, 10)},
		{"due before closing, next month", m(2026, time.March), 25, 5, brcal.New(2026, time.March, 25), brcal.New(2026, time.April, 5)},
		{"due equal to closing, next month", m(2026, time.March), 10, 10, brcal.New(2026, time.March, 10), brcal.New(2026, time.April, 10)},
		{"closing 31 in February clamps", m(2026, time.February), 31, 10, brcal.New(2026, time.February, 28), brcal.New(2026, time.March, 10)},
		{"December into January", m(2026, time.December), 28, 5, brcal.New(2026, time.December, 28), brcal.New(2027, time.January, 5)},
		{"due 31 clamps in a short month", m(2026, time.March), 25, 31, brcal.New(2026, time.March, 25), brcal.New(2026, time.March, 31)},
		{"due 31 next month clamps", m(2026, time.January), 31, 30, brcal.New(2026, time.January, 31), brcal.New(2026, time.February, 28)},
	} {
		if got := ClosingDate(tc.month, tc.closing); got != tc.wantClose {
			t.Errorf("%s: closing = %s, want %s", tc.name, got, tc.wantClose)
		}
		if got := DueDate(tc.month, tc.closing, tc.due); got != tc.wantDue {
			t.Errorf("%s: due = %s, want %s", tc.name, got, tc.wantDue)
		}
	}
}

func TestCardValidate(t *testing.T) {
	ok := Card{ID: "c", ClosingDay: 3, DueDay: 10, PayingAccountID: "bank", OpenMonth: Month{2026, time.March}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]Card{
		"closing 0": {ID: "c", ClosingDay: 0, DueDay: 10, PayingAccountID: "bank", OpenMonth: ok.OpenMonth},
		"due 32":    {ID: "c", ClosingDay: 3, DueDay: 32, PayingAccountID: "bank", OpenMonth: ok.OpenMonth},
		"no payer":  {ID: "c", ClosingDay: 3, DueDay: 10, OpenMonth: ok.OpenMonth},
		"no month":  {ID: "c", ClosingDay: 3, DueDay: 10, PayingAccountID: "bank"},
	} {
		if err := c.Validate(); !errors.Is(err, ErrInvalidCard) {
			t.Errorf("%s: %v, want ErrInvalidCard", name, err)
		}
	}
}

// UX batch 3: a card names its brand (a closed set) and, optionally, the last
// four digits, for the person to tell their cards apart. Both are attributes;
// neither is ever the full number.
func TestCardBrandAndLast4(t *testing.T) {
	base := Card{ID: "c", ClosingDay: 3, DueDay: 10, PayingAccountID: "bank", OpenMonth: Month{2026, time.March}}
	for _, b := range []CardBrand{"", BrandVisa, BrandMastercard, BrandElo, BrandAmex, BrandHipercard, BrandDiners, BrandOther} {
		c := base
		c.Brand, c.Last4 = b, "1234"
		if err := c.Validate(); err != nil {
			t.Errorf("brand %q: %v", b, err)
		}
	}
	for name, mutate := range map[string]func(*Card){
		"unknown brand":    func(c *Card) { c.Brand = "discover" },
		"three digits":     func(c *Card) { c.Last4 = "123" },
		"five digits":      func(c *Card) { c.Last4 = "12345" },
		"a letter":         func(c *Card) { c.Last4 = "12a4" },
		"full-width digit": func(c *Card) { c.Last4 = "123４" },
	} {
		c := base
		mutate(&c)
		if err := c.Validate(); !errors.Is(err, ErrInvalidCard) {
			t.Errorf("%s: err = %v, want ErrInvalidCard", name, err)
		}
	}
	if !ValidCardBrand(BrandElo) || ValidCardBrand("") || ValidCardBrand("visa ") {
		t.Fatal("ValidCardBrand accepts only the closed set")
	}
	if !ValidLast4("0000") || ValidLast4("") || ValidLast4("０１２３") {
		t.Fatal("ValidLast4 accepts exactly four ASCII digits")
	}
}
