package statement

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Date formats a CSV mapping may name. The separator between the parts may be
// '/', '-' or '.', and a two-digit year is read as 20yy.
const (
	DateDMY = "dd/mm/yyyy"
	DateYMD = "yyyy-mm-dd"
	DateMDY = "mm/dd/yyyy"
)

const (
	maxColumn   = 50
	maxSkipRows = 20
)

// Mapping is how one account's CSV export is read, saved per account (spec
// § 3.7). Columns are 1-based, as a spreadsheet shows them.
type Mapping struct {
	Delimiter   string // ";", "," or "\t"
	Decimal     string // "," or "."
	DateFormat  string // DateDMY, DateYMD or DateMDY
	SkipRows    int    // rows before the first transaction (a header), 0..20
	Date        int
	Description int
	// Amount is a signed amount column. With Debit set, it holds only what came
	// in, and Debit what left (either sign: a debit always leaves).
	Amount int
	Debit  int // 0 when the file has one signed column
}

// Validate refuses a mapping the parser could not apply.
func (m Mapping) Validate() error {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidMapping}, a...)...)
	}
	switch {
	case m.Delimiter != ";" && m.Delimiter != "," && m.Delimiter != "\t":
		return fail("delimiter must be ';', ',' or a tab")
	case m.Decimal != "," && m.Decimal != ".":
		return fail("decimal separator must be ',' or '.'")
	case m.Decimal == m.Delimiter:
		return fail("the decimal separator cannot be the delimiter")
	case m.DateFormat != DateDMY && m.DateFormat != DateYMD && m.DateFormat != DateMDY:
		return fail("unknown date format %q", m.DateFormat)
	case m.SkipRows < 0 || m.SkipRows > maxSkipRows:
		return fail("rows to skip must be 0..%d", maxSkipRows)
	}
	cols := map[int]string{}
	for _, c := range []struct {
		name     string
		n        int
		required bool
	}{{"date", m.Date, true}, {"description", m.Description, true}, {"amount", m.Amount, true}, {"debit", m.Debit, false}} {
		if c.n == 0 && !c.required {
			continue
		}
		if c.n < 1 || c.n > maxColumn {
			return fail("the %s column must be 1..%d", c.name, maxColumn)
		}
		if other, taken := cols[c.n]; taken {
			return fail("the %s and %s columns are the same", other, c.name)
		}
		cols[c.n] = c.name
	}
	return nil
}

// ParseCSV reads a CSV export with the account's mapping. Rows before
// SkipRows and blank rows are skipped; a row whose date or amount cannot be
// read, or that is a running-balance row ("SALDO …"), is Rejected with its
// 1-based row number and the rest import.
func ParseCSV(b []byte, m Mapping) (Parsed, error) {
	if err := m.Validate(); err != nil {
		return Parsed{}, err
	}
	if len(b) > MaxFileBytes {
		return Parsed{}, ErrTooLarge
	}
	r := csv.NewReader(strings.NewReader(decodeText(b)))
	r.Comma = rune(m.Delimiter[0])
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	var out Parsed
	row, n := 0, 0
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Parsed{}, fmt.Errorf("%w: %v", ErrUnreadable, err)
		}
		row++
		if row <= m.SkipRows || blank(rec) {
			continue
		}
		if n++; n > MaxLines {
			return Parsed{}, ErrTooMany
		}
		l, reason := m.line(rec)
		if reason != "" {
			out.Rejected = append(out.Rejected, Rejected{Line: row, Reason: reason})
			continue
		}
		out.Lines = append(out.Lines, l)
	}
	if n == 0 {
		return Parsed{}, ErrEmpty
	}
	return out, nil
}

func blank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

func field(rec []string, col int) string {
	if col < 1 || col > len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[col-1])
}

func (m Mapping) line(rec []string) (Line, string) {
	desc := cleanText(field(rec, m.Description), maxDescription)
	if balanceRow(desc) {
		return Line{}, ReasonBalanceRow
	}
	date, ok := parseCSVDate(field(rec, m.Date), m.DateFormat)
	if !ok {
		return Line{}, ReasonDate
	}
	dec := m.Decimal[0]
	var amount billing.Cents
	if m.Debit == 0 {
		if amount, ok = parseAmount(field(rec, m.Amount), dec); !ok {
			return Line{}, ReasonAmount
		}
	} else {
		in, out := field(rec, m.Amount), field(rec, m.Debit)
		credit, debit := billing.Cents(0), billing.Cents(0)
		if in != "" {
			if credit, ok = parseAmount(in, dec); !ok {
				return Line{}, ReasonAmount
			}
		}
		if out != "" {
			if debit, ok = parseAmount(out, dec); !ok {
				return Line{}, ReasonAmount
			}
		}
		if credit != 0 && debit != 0 {
			return Line{}, ReasonAmount // both columns filled: which one is it?
		}
		amount = abs(credit) - abs(debit)
	}
	if amount == 0 {
		return Line{}, ReasonZero
	}
	return Line{Date: date, Amount: amount, Description: desc}, ""
}

func abs(c billing.Cents) billing.Cents {
	if c < 0 {
		return -c
	}
	return c
}

// balanceRow recognises the running-balance rows Brazilian bank exports mix
// with transactions. They carry an amount and are not money that moved.
func balanceRow(desc string) bool {
	switch strings.ToUpper(desc) {
	case "SALDO", "SALDO ANTERIOR", "SALDO DO DIA", "SALDO FINAL", "SALDO ATUAL", "SALDO INICIAL",
		"SALDO DISPONIVEL", "SALDO DISPONÍVEL", "S A L D O":
		return true
	}
	return false
}

// parseCSVDate reads the first token of the cell (a time may follow) in the
// mapping's format.
func parseCSVDate(s, format string) (brcal.Date, bool) {
	if f := strings.Fields(s); len(f) > 0 {
		s = f[0]
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '-' || r == '.' })
	if len(parts) != 3 {
		return brcal.Date{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if !digits(p) || len(p) > 4 {
			return brcal.Date{}, false
		}
		nums[i], _ = strconv.Atoi(p)
	}
	var y, m, d int
	var yearText string
	switch format {
	case DateDMY:
		d, m, y, yearText = nums[0], nums[1], nums[2], parts[2]
	case DateMDY:
		m, d, y, yearText = nums[0], nums[1], nums[2], parts[2]
	default:
		y, m, d, yearText = nums[0], nums[1], nums[2], parts[0]
	}
	if len(yearText) == 2 {
		y += 2000
	} else if len(yearText) != 4 {
		return brcal.Date{}, false
	}
	return validDate(y, m, d)
}
