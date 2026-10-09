package v1

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// checks collects field errors for one request, so a form learns every problem
// in one answer instead of one per submit. The rules are the API's: the UI
// enforces the same numbers (ui/src/lib/limits.json) only to spare a round trip.
type checks struct {
	errs []problem.FieldError
}

func (c *checks) fail(field, code, msg string, params ...any) {
	c.errs = append(c.errs, fieldErr(field, code, msg, params...))
}

// text: at most max characters (runes, so "ç" is one), no control characters
// (a newline or a NUL in a name is a paste accident or an attack on whatever
// renders it later), and not blank when required.
func (c *checks) text(field, v string, required bool, max int) {
	switch {
	case required && strings.TrimSpace(v) == "":
		c.fail(field, "required", "required")
	case utf8.RuneCountInString(v) > max:
		c.fail(field, "too_long", fmt.Sprintf("at most %d characters", max), "max", max)
	case strings.IndexFunc(v, unicode.IsControl) >= 0:
		c.fail(field, "invalid_chars", "contains invalid characters")
	}
}

// amount: a positive amount, at most limits.MaxAmountCents.
func (c *checks) amount(field string, v billing.Cents) {
	if v <= 0 || v > limits.MaxAmountCents {
		c.fail(field, "out_of_range", "enter a value between 0.01 and the maximum", "min", 1, "max", int64(limits.MaxAmountCents))
	}
}

// signedAmount: a balance, which may be negative but not zero.
func (c *checks) signedAmount(field string, v billing.Cents) {
	if v == 0 || v > limits.MaxAmountCents || v < -limits.MaxAmountCents {
		c.fail(field, "out_of_range", "enter a non-zero value within the maximum", "min", -int64(limits.MaxAmountCents), "max", int64(limits.MaxAmountCents), "non_zero", true)
	}
}

// price: zero (free) up to the maximum.
func (c *checks) price(field string, v billing.Cents) {
	if v < 0 || v > limits.MaxAmountCents {
		c.fail(field, "out_of_range", "enter a value between 0 and the maximum", "min", 0, "max", int64(limits.MaxAmountCents))
	}
}

// date: required, and within [min, max].
func (c *checks) date(field string, d, min, max brcal.Date) {
	switch {
	case d.IsZero():
		c.fail(field, "required", "required")
	case d.Before(min):
		c.fail(field, "date_too_early", "the earliest accepted date is "+min.String(), "min", min.String())
	case d.After(max):
		c.fail(field, "date_too_late", "the latest accepted date is "+max.String(), "max", max.String())
	}
}

// id: a server-made id (an account, a category, a bill): letters, digits, '_'
// and '-', at most 64. Anything else cannot name a row, and saying so here is
// clearer than a 404 or a 422 from deeper in.
func (c *checks) id(field, v string, required bool) {
	if v == "" {
		if required {
			c.fail(field, "required", "required")
		}
		return
	}
	if len(v) > 64 || strings.IndexFunc(v, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) >= 0 {
		c.fail(field, "invalid_id", "invalid identifier")
	}
}

// email: one bare address, at most limits.Email.
func (c *checks) email(field, v string, required bool) {
	if v == "" {
		if required {
			c.fail(field, "required", "required")
		}
		return
	}
	a, err := mail.ParseAddress(v)
	if err != nil || a.Address != v || a.Name != "" || len(v) > limits.Email || !strings.Contains(v[strings.LastIndexByte(v, '@')+1:], ".") {
		c.fail(field, "invalid_email", "invalid email address")
	}
}

// taxID: a CPF or a CNPJ (alphanumeric since 2026). checkDigits verifies the
// document; without it only the shape is checked — for the M2M routes, whose
// integrators already send test documents that a check-digit rule would start
// refusing overnight.
func (c *checks) taxID(field, v string, required, checkDigits bool) {
	if v == "" {
		if required {
			c.fail(field, "required", "required")
		}
		return
	}
	if len(v) > limits.TaxID {
		c.fail(field, "invalid_tax_id", "invalid CPF or CNPJ")
		return
	}
	if checkDigits {
		if _, _, ok := billing.NormalizeTaxID(v); !ok {
			c.fail(field, "invalid_tax_id", "invalid CPF or CNPJ")
		}
		return
	}
	n := 0
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			n++
		case r == '.' || r == '/' || r == '-' || r == ' ':
		default:
			n = -100
		}
	}
	if n != 11 && n != 14 {
		c.fail(field, "invalid_tax_id", "invalid CPF or CNPJ")
	}
}
