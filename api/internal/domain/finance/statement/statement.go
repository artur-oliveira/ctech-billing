// Package statement reads bank statement files — OFX and CSV — into lines, and
// offers each line against the open bills it may be (spec § 3.7). It is pure:
// bytes in, lines out. The file is never kept; what survives the request is the
// lines a repository writes.
package statement

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
)

// Format is the kind of file uploaded.
type Format string

const (
	FormatOFX Format = "ofx"
	FormatCSV Format = "csv"
)

// What one upload may carry. A year of a busy checking account is well under
// both; a file past them is a wrong file, not a big statement.
const (
	MaxFileBytes = 1 << 20
	MaxLines     = 2000
	// maxDescription matches a bill's description, which a "new" line becomes.
	maxDescription = 200
	maxFITID       = 255
)

var (
	// ErrUnreadable is a file that is not the format it was sent as.
	ErrUnreadable = errors.New("the statement file could not be read")
	// ErrCardStatement is an OFX credit card statement (CCSTMTRS). Card lines are
	// purchases and installments, not bill payments: v1 does not import them.
	ErrCardStatement = errors.New("credit card statements are not imported")
	// ErrManyAccounts is an OFX file with more than one account's statement.
	ErrManyAccounts = errors.New("the file holds more than one account")
	// ErrCurrency is a statement in a currency other than reais.
	ErrCurrency = errors.New("the statement is not in reais")
	ErrTooLarge = errors.New("the statement file is too large")
	ErrTooMany  = errors.New("the statement has too many lines")
	// ErrEmpty is a file with no transaction at all.
	ErrEmpty = errors.New("the statement has no transactions")
	// ErrNoMapping is a CSV upload to an account whose columns were never mapped.
	ErrNoMapping = errors.New("the account has no CSV column mapping yet")
	// ErrInvalidMapping wraps every reason a CSV column mapping is refused.
	ErrInvalidMapping = errors.New("invalid CSV column mapping")
)

// Line is one transaction as the bank reported it, signed from the account's
// side: negative left the account, positive came in.
type Line struct {
	Date        brcal.Date
	Amount      billing.Cents
	Description string
	// FITID is the bank's own id for the transaction (OFX only; may be empty).
	FITID string
}

// Reasons a line in the file was not imported. The rest of the file imports.
const (
	ReasonDate       = "invalid_date"
	ReasonAmount     = "invalid_amount"
	ReasonZero       = "zero_amount"
	ReasonBalanceRow = "balance_row"
)

// Rejected is a line the file carried that could not be read.
type Rejected struct {
	Line   int // 1-based: the transaction's position (OFX) or the file's row (CSV)
	Reason string
}

// Parsed is a file read into lines.
type Parsed struct {
	Lines    []Line
	Rejected []Rejected
}

// decodeText turns the file into UTF-8. Brazilian banks declare OFX 1.x as
// CHARSET:1252 and Excel saves CSV as Windows-1252, but the declaration is not
// reliable — real exports declare 1252 and are UTF-8. So the bytes decide: valid
// UTF-8 (BOM stripped) is kept as it is, anything else is read as Windows-1252,
// a superset of printable ISO-8859-1.
func decodeText(b []byte) string {
	b = bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF"))
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/8)
	for _, c := range b {
		if c >= 0x80 && c < 0xA0 {
			sb.WriteRune(cp1252[c-0x80])
		} else {
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

// cp1252 is what Windows-1252 puts at 0x80..0x9F, where ISO-8859-1 has
// control characters.
var cp1252 = [32]rune{
	'€', '�', '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', '�', 'Ž', '�',
	'�', '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', '�', 'ž', 'Ÿ',
}

// cleanText drops control characters (a NUL or an escape the API would refuse
// with a message about something nobody can see), collapses whitespace, and
// bounds the length in BYTES — what finance.Bill.Validate counts — cutting on a
// rune boundary so an accented text never ends in half a character.
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r) || r == utf8.RuneError:
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = strings.TrimSpace(s[:cut])
	}
	return s
}

// parseAmount reads a money amount into centavos. decimal is ',' or '.', or 0
// to decide from the text: the last of '.' and ',' is the decimal separator and
// the other is a thousands separator ("1.234,56", "1,234.56", "-12.5"). A sign
// may lead or trail ("-10,00", "10,00-"), or parentheses may wrap the amount.
// More than two decimals are accepted only when the extra digits are zeros.
func parseAmount(s string, decimal byte) (billing.Cents, bool) {
	s = strings.NewReplacer(" ", "", " ", "", "R$", "").Replace(strings.TrimSpace(s))
	neg := false
	switch {
	case strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")"):
		neg, s = true, s[1:len(s)-1]
	case strings.HasPrefix(s, "-"):
		neg, s = true, s[1:]
	case strings.HasSuffix(s, "-"):
		neg, s = true, s[:len(s)-1]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	if decimal == 0 {
		dot, comma := strings.LastIndexByte(s, '.'), strings.LastIndexByte(s, ',')
		switch {
		case dot < 0 && comma < 0:
			decimal = '.'
		case dot > comma:
			decimal = '.'
		default:
			decimal = ','
		}
		// One separator that repeats ("1.234.567") is a thousands separator.
		if strings.Count(s, string(decimal)) > 1 {
			s = strings.ReplaceAll(s, string(decimal), "")
		}
	}
	thousands := ","
	if decimal == ',' {
		thousands = "."
	}
	s = strings.ReplaceAll(s, thousands, "")
	whole, frac, _ := strings.Cut(s, string(decimal))
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 2 {
		if strings.Trim(frac[2:], "0") != "" {
			return 0, false
		}
		frac = frac[:2]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if len(whole) > 13 || !digits(whole) || !digits(frac) {
		return 0, false
	}
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil || billing.Cents(n) > finance.MaxLegAmount {
		return 0, false
	}
	if neg {
		n = -n
	}
	return billing.Cents(n), true
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// validDate builds y-m-d and refuses what brcal.New would silently normalise
// (31 February is not 3 March).
func validDate(y, m, d int) (brcal.Date, bool) {
	if y < 1900 || y > 2999 || m < 1 || m > 12 || d < 1 || d > 31 {
		return brcal.Date{}, false
	}
	out := brcal.New(y, time.Month(m), d)
	if out.Day != d || int(out.Month) != m {
		return brcal.Date{}, false
	}
	return out, true
}
